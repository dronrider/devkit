package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Раздел «Черновики»: список накопителя, текст записи, груминг, его исход и
// удаление записи. Список берётся у taskctl (draft list --json), как и доска:
// правду про накопитель держит утилита, а разбирать её печать грепом хрупко.
// Текст читается файлом напрямую, как текст задачи. «Провести груминг»
// поднимает сессию грумминга той же механикой, что и конвейер задачи (DK-250):
// tmux-сессия с headless-сессией конвейера, свой разбор накопителя у дашборда
// не заводится.

// groomPrompt это заказ сессии грумминга. Слова те же, какими эту работу
// заказывают в чате: по ним скилл board-groom и разводит разбор черновика.
// Уточнение человека едет в тот же заказ (LLD DK-328, решение 4): писать в
// закончившуюся сессию нечем, и ответ на вопрос груминга уходит новой ходкой,
// которая перечитает черновик и пойдёт заново.
func groomPrompt(id, ask string) string {
	prompt := "Проведи груминг " + id
	if ask != "" {
		prompt += ". Человек уточняет: " + ask
	}
	return prompt
}

// draftAskLimit ограничивает уточнение: это фраза в заказ груминга, а не
// постановка задачи.
const draftAskLimit = 4 << 10

// draftSession это имя tmux-сессии грумминга. Префикс task- взят не для
// красоты: грумминг кончается строкой доски с тем же ID (taskctl add --id), и
// работа видна там же, где остальные работы проекта (liveWorks привязывает их
// префиксом доски), а стоп ей достаётся тот же самый.
func draftSession(id string) string { return "task-" + id }

func (s *server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "накопитель черновиков")
	if found == nil {
		return
	}
	out, code, err := taskctlDo(found.Path, "draft", "list", "--json")
	if err != nil {
		s.logf("накопитель черновиков %s: %v", found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	var v struct {
		Drafts []json.RawMessage `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("ответ taskctl draft list --json не разобрался: %v", err)})
		return
	}
	resp := map[string]any{"project": found.Name, "drafts": draftsWithOrder(v.Drafts)}
	if len(v.Drafts) == 0 {
		// Пустой список без слов неотличим от неотрисованного раздела.
		resp["note"] = fmt.Sprintf("накопитель черновиков %s пуст: записанных мимо доски идей нет", found.Name)
	}
	writeJSON(w, http.StatusOK, resp)
}

// draftsWithOrder дописывает каждой записи накопителя заказ груминга
// дословно, той же строкой, что унесёт groomPrompt в headless-сессию
// (DK-286): подсказка кнопки «Провести груминг» читает готовую строку, а не
// собирает её второй раз. Ответ пересобирается по общей карте, а не типом:
// формат записи накопителя держит taskctl, и разбор в типизированную строку
// потерял бы поля, неизвестные дашборду. Неразобранная запись уезжает
// нетронутой, без подсказки: без неё запись рисуется по-старому.
func draftsWithOrder(items []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			out = append(out, raw)
			continue
		}
		var id string
		json.Unmarshal(m["id"], &id)
		if id != "" {
			if mark, err := json.Marshal(groomPrompt(id, "")); err == nil {
				m["order"] = mark
			}
		}
		enc, err := json.Marshal(m)
		if err != nil {
			out = append(out, raw)
			continue
		}
		out = append(out, enc)
	}
	return out
}

// draftPath отдаёт путь файла черновика и его путь от корня проекта.
func draftPathOf(projectPath, id string) (abs, rel string) {
	rel = draftFileRel(id)
	return filepath.Join(projectPath, filepath.FromSlash(rel)), rel
}

func (s *server) handleDraft(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "черновик")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	path, rel := draftPathOf(found.Path, id)
	text, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("черновика %s в %s нет: файла %s не видно, грумминг мог уже завести по нему задачу", id, found.Name, rel)})
		return
	}
	// Заказ едет и сюда, дословно: экран записи держит свою кнопку «Провести
	// груминг», и подсказка на ней читает то же поле, что и строка накопителя.
	writeJSON(w, http.StatusOK, map[string]string{
		"id": id, "file": rel, "text": string(text), "order": groomPrompt(id, "")})
}

// handleDraftGroom поднимает сессию грумминга черновика. Проверки те же и в том
// же порядке, что у запуска задачи: без tmux и без claude сессия умерла бы
// молча, а поверх живой работы с тем же ID вторую поднимать нельзя.
func (s *server) handleDraftGroom(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("груминг отклонён: чужой Origin 403")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "груминг")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		s.logf("груминг %s отклонён: кривой ID 400", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	ask, ok := s.draftAsk(w, r, id)
	if !ok {
		return
	}
	path, rel := draftPathOf(found.Path, id)
	if !isFile(path) {
		s.logf("груминг %s в %s отклонён: файл черновика не найден 404", id, found.Name)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("черновика %s в %s нет: файла %s не видно, оформлять нечего", id, found.Name, rel)})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		s.logf("груминг %s в %s отклонён: tmux не нашёлся 502", id, found.Name)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	sess := draftSession(id)
	for _, name := range tmuxSessions() {
		if name == sess || name == "goal-"+id {
			s.logf("грумминг %s в %s отклонён: tmux-сессия %s уже идёт", id, found.Name, name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("работа %s уже идёт (tmux-сессия %s): поднимать грумминг поверх живой сессии нельзя, сначала стоп", id, name)})
			return
		}
	}
	if m := claudeMissing(); m != "" {
		s.logf("грумминг %s в %s не удался: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		"claude -p "+shQuote(groomPrompt(id, ask))); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("грумминг %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.logf("грумминг %s в %s поднят (tmux-сессия %s)", id, found.Name, sess)
	message := fmt.Sprintf("грумминг %s поднят в tmux-сессии %s: разбор доведёт черновик до строки Backlog либо снимет его с причиной", id, sess)
	if ask != "" {
		message = fmt.Sprintf("грумминг %s поднят заново в tmux-сессии %s, уточнение уехало в заказ: агент перечитает черновик и пойдёт с начала", id, sess)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id": id, "kind": "task", "session": sess, "prompt": groomPrompt(id, ask),
		"message": message,
	})
}

// draftAsk достаёт уточнение из тела запроса. Тело бывает и пустым: кнопка
// «Провести груминг» шлёт заказ без слов, а уточнение приходит только с поля
// повторной ходки.
func (s *server) draftAsk(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	var body struct {
		Ask string `json:"ask"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftAskLimit)).Decode(&body)
	switch {
	case errors.Is(err, io.EOF):
		return "", true
	case err != nil:
		var mbe *http.MaxBytesError
		text := "жду JSON {\"ask\": \"...\"} либо пустое тело"
		if errors.As(err, &mbe) {
			text = fmt.Sprintf("уточнение длиннее предела %d КБ: в заказ груминга едет фраза, а не постановка", draftAskLimit/1024)
		}
		s.logf("груминг %s отклонён: тело запроса не разобралось 400", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": text})
		return "", false
	}
	return strings.Join(strings.Fields(body.Ask), " "), true
}

// handleDraftDrop удаляет черновик. Причина обязательна, как и у самой
// утилиты: файла после команды нет, и живёт причина только сообщением коммита,
// поэтому молча удалить запись ручка не даёт.
func (s *server) handleDraftDrop(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("удаление черновика отклонено: чужой Origin 403")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "удаление черновика")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		s.logf("удаление черновика %s отклонено: кривой ID 400", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftAskLimit)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		s.logf("удаление черновика %s отклонено: тело запроса не разобралось 400", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"reason\": \"...\"}"})
		return
	}
	reason := strings.Join(strings.Fields(body.Reason), " ")
	if reason == "" {
		s.logf("удаление черновика %s отклонено: причина не названа 400", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду причину: она уезжает в коммит доски, и без неё черновик не удаляется"})
		return
	}
	path, rel := draftPathOf(found.Path, id)
	if !isFile(path) {
		s.logf("удаление черновика %s в %s отклонено: файл не найден 404", id, found.Name)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("черновика %s в %s нет: файла %s не видно, удалять нечего", id, found.Name, rel)})
		return
	}
	out, code, err := taskctlDo(found.Path, "draft", "drop", id, "--reason", reason)
	if err != nil {
		s.logf("черновик %s в %s не удалился: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"id": id, "file": rel, "reason": reason, "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, "черновик удалён с дашборда: "+reason), rel); note != "" {
		resp["note"] = note
		s.logf("удаление черновика %s в %s: %s", id, found.Name, note)
	}
	s.logf("черновик %s в %s удалён: %s", id, found.Name, reason)
	writeJSON(w, http.StatusOK, resp)
}

// Исход груминга читается следами на диске (LLD DK-328, решение 4): у каждого
// из четырёх исходов разбора свой след, и гадать экрану не приходится. Строка с
// номером черновика на доске значит «оформлен», раздел «Из черновика <ID>» в
// чужом файле задачи значит «приписан», пометка в разделе «Грумминг» значит
// «отложен», а пропавший файл без единого следа значит «удалён»: причина
// удаления уехала в коммит доски.
const (
	draftStateRow      = "row"
	draftStateAttached = "attached"
	draftStateDeferred = "deferred"
	draftStateOpen     = "open"
	draftStateDropped  = "dropped"
)

// draftGroomHeading это заголовок раздела, в котором живёт пометка об
// отложенном. Раздел пишет taskctl draft defer (tools/taskctl/groom.go), и
// границы его те же: от заголовка до следующего заголовка второго уровня.
const draftGroomHeading = "## Грумминг"

// deferredRe находит пометку об отложенном. Формат ставит taskctl draft defer,
// здесь он только читается: дата и причина стоят в одной строке, и причина
// нужна экрану целиком.
var deferredRe = regexp.MustCompile(`^-\s*(\d{4}-\d{2}-\d{2}),\s*отложен:\s*(.+)$`)

// draftDeferred вынимает дату и причину пометки «отложен». Ищется она только в
// разделе «Грумминг»: сама запись это свободный текст человека, и строка того
// же вида посреди прозы выдавала бы за исход разбора то, чего разбор не ставил
// (замечание ревью DK-321).
func draftDeferred(text string) (when, reason string) {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) != draftGroomHeading {
			continue
		}
		for _, in := range lines[i+1:] {
			if strings.HasPrefix(in, "## ") {
				break
			}
			if m := deferredRe.FindStringSubmatch(strings.TrimSpace(in)); m != nil {
				return m[1], strings.TrimSpace(m[2])
			}
		}
		break
	}
	return "", ""
}

// draftAttachedTo ищет приёмника приписки: раздел «Из черновика <ID>» пишет
// taskctl draft attach, и стоит он в файле той задачи, к которой запись
// приписали. Накопитель из обхода выкинут: искать след черновика в нём самом
// незачем, а закрытые задачи уезжают в docs/tasks/archive и остаются видны.
func draftAttachedTo(projectPath, id string) (task, rel string) {
	head := "## Из черновика " + id
	base := filepath.Join(projectPath, "docs", "tasks")
	filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "drafts" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(ln)
			// Хвост заголовка это дата записи в скобках, и равенство её бы не
			// приняло; голое совпадение по началу спутало бы XR-5 с XR-50.
			if ln != head && !strings.HasPrefix(ln, head+" ") {
				continue
			}
			task = strings.TrimSuffix(d.Name(), ".md")
			if r, err := filepath.Rel(projectPath, path); err == nil {
				rel = filepath.ToSlash(r)
			}
			return fs.SkipAll
		}
		return nil
	})
	return task, rel
}

// draftQuestion отдаёт последнее слово агента из свежего разговора черновика:
// груминг, кончившийся вопросом, следа на диске не оставляет, и вопрос лежит
// только в транскрипте. Сессия ищется тем же порядком, что у экрана агента
// (sessionTask), второго способа связать разговор с задачей не заводится.
func (s *server) draftQuestion(projPath, id string) (text, sid string) {
	for _, f := range sessionFiles(s.transcriptRoots(), projPath) {
		task, _ := sessionTask(f.suffix, s.sessionHeadCached(f.path, f.stamp))
		if task != id {
			continue
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			return "", ""
		}
		items := parseReplies(data, 0)
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Role == "assistant" && strings.TrimSpace(items[i].Text) != "" {
				return items[i].Text, f.ID
			}
		}
		return "", f.ID
	}
	return "", ""
}

// handleDraftOutcome отвечает, чем кончился груминг черновика. Ручка не
// отказывает пропавшему черновику: пропажа это и есть один из исходов, и
// сказать про неё надо словами, а не кодом 404.
func (s *server) handleDraftOutcome(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "исход груминга")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	writeJSON(w, http.StatusOK, s.draftOutcome(found, id))
}

// draftOutcome собирает исход по следам. Порядок проверок не случаен:
// оформленный черновик уезжает файлом в docs/tasks и на месте его уже нет, а
// приписка и удаление оставляют одинаково пустой накопитель, и разводит их
// только раздел в чужом файле задачи.
func (s *server) draftOutcome(found *Project, id string) map[string]any {
	out := map[string]any{"project": found.Name, "id": id}
	if raw, err := s.projectBoard(found.Path); err == nil {
		if rows, err := parseBoardRows(raw); err == nil {
			if row, hit := rows[id]; hit {
				out["state"] = draftStateRow
				out["row"] = row
				out["note"] = fmt.Sprintf("груминг завёл строку: %s стоит на доске %s, секция %s",
					id, found.Name, row.Section)
				return out
			}
		}
	}
	if task, rel := draftAttachedTo(found.Path, id); task != "" {
		out["state"] = draftStateAttached
		out["task"], out["task_file"] = task, rel
		out["note"] = fmt.Sprintf("груминг признал запись частью стоящей работы: текст уехал разделом «Из черновика %s» в %s", id, rel)
		return out
	}
	path, rel := draftPathOf(found.Path, id)
	if text, err := os.ReadFile(path); err == nil {
		out["file"] = rel
		if when, reason := draftDeferred(string(text)); when != "" {
			out["state"] = draftStateDeferred
			out["deferred"], out["reason"] = when, reason
			out["note"] = fmt.Sprintf("груминг отложил запись %s: она осталась в накопителе, причина лежит в разделе «Грумминг» файла %s", when, rel)
			return out
		}
		out["state"] = draftStateOpen
		question, sid := s.draftQuestion(found.Path, id)
		if question != "" {
			out["question"], out["session"] = question, sid
			out["note"] = "груминг вышел, не оставив следа на диске: ни строки, ни приписки, ни пометки об отложенном"
			return out
		}
		out["note"] = fmt.Sprintf("груминг записи не касался: она лежит в накопителе файлом %s как записана", rel)
		return out
	}
	out["state"] = draftStateDropped
	out["note"] = fmt.Sprintf("следов груминга нет ни одного: файла %s не видно, строки %s на доске %s нет, приписки к чужой задаче нет. Так выглядит удалённый черновик, причина удаления уехала в коммит доски",
		rel, id, found.Name)
	return out
}

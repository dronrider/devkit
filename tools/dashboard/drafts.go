package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Раздел «Черновики»: список накопителя, текст записи, груминг, его исход и
// удаление записи. Список берётся у taskctl (draft list --json), как и доска:
// правду про накопитель держит утилита, а разбирать её печать грепом хрупко.
// Текст читается файлом напрямую, как текст задачи. «Грумить»
// поднимает сессию грумминга той же механикой, что и конвейер задачи (DK-250):
// tmux-сессия с сессией клиента, свой разбор накопителя у дашборда
// не заводится.

// groomPrompt это заказ сессии грумминга. Слова те же, какими эту работу
// заказывают в чате: по ним скилл board-groom и разводит разбор черновика.
// Уточнение человека едет в тот же заказ (LLD DK-328, решение 4): писать в
// закончившуюся сессию нечем, и ответ на вопрос груминга уходит новой ходкой,
// которая перечитает черновик и пойдёт заново.
// groomPrompt это заказ грумеру дословно. Заказ называет и то, как задавать
// вопросы: разбор идёт живым разговором (решение 1 LLD DK-354), человек сидит в
// той же панели, и вопрос ему задаётся прямо там, ожиданием ответа. Прежде
// вопрос был выходом из захода, а ответ уезжал новым заходом с уточнением, и
// грумер перечитывал черновик заново; экран черновика держал под это своё поле
// ответа, и оно ушло вместе с механикой (решение пользователя).
func groomPrompt(id, ask string) string {
	prompt := "Проведи груминг " + id +
		". Вопросы задавай в этом же разговоре, командой `taskctl ask " + id +
		" --question \"...\" --wait 480`, и жди ответа в ней: вопросом заход не кончай, " +
		"а не дождавшись, отложи запись с причиной"
	if ask != "" {
		prompt += ". Человек уточняет: " + ask
	}
	return prompt
}

// draftAskLimit ограничивает уточнение: это фраза в заказ груминга, а не
// постановка задачи.
const draftAskLimit = 4 << 10

// draftDateLayout это вид даты правки записи: тот же, каким taskctl печатает
// дату строки доски (поле moved), чтобы накопитель и доска говорили о времени
// одинаково.
const draftDateLayout = "2006-01-02"

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
	// Разобранные черновики оставляют за собой мёртвые разговоры, и уборка идёт
	// отсюда: экран накопителя это то место, где груминг заказывают и где
	// смотрят на его исход.
	s.groomSweep(found.Path)
	resp := map[string]any{"project": found.Name, "drafts": s.draftsWithOrder(found.Path, v.Drafts)}
	if len(v.Drafts) == 0 {
		// Пустой список без слов неотличим от неотрисованного раздела.
		resp["note"] = fmt.Sprintf("накопитель черновиков %s пуст: записанных мимо доски идей нет", found.Name)
	}
	writeJSON(w, http.StatusOK, resp)
}

// draftsWithOrder дописывает каждой записи накопителя заказ груминга
// дословно, той же строкой, что унесёт groomPrompt в headless-сессию
// (DK-286): подсказка кнопки «Грумить» читает готовую строку, а не
// собирает её второй раз. Ответ пересобирается по общей карте, а не типом:
// формат записи накопителя держит taskctl, и разбор в типизированную строку
// потерял бы поля, неизвестные дашборду. Неразобранная запись уезжает
// нетронутой, без подсказки: без неё запись рисуется по-старому.
func (s *server) draftsWithOrder(projPath string, items []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			out = append(out, raw)
			continue
		}
		// Дата правки записи теми же словами, что у строки доски: строка
		// накопителя показывает дату, а не возраст днями (замечание
		// пользователя). Считается она по файлу записи, как считает его
		// возраст сам taskctl, и молчит там, где файла не видно.
		var file string
		json.Unmarshal(m["file"], &file)
		if file != "" {
			if info, err := os.Stat(filepath.Join(projPath, filepath.FromSlash(file))); err == nil {
				if mark, err := json.Marshal(info.ModTime().Format(draftDateLayout)); err == nil {
					m["moved"] = mark
				}
				// Точное время правки едет рядом с днём: в ячейке стоит день, а
				// подсказка показывает час с минутой и давность («идиотская
				// подпись» вместо точной даты, замечание пользователя). День
				// остаётся отдельным полем: по нему список сортируется, и
				// разбирать секунды ради порядка незачем.
				if mark, err := json.Marshal(info.ModTime().Unix()); err == nil {
					m["moved_at"] = mark
				}
			}
		}
		var id string
		json.Unmarshal(m["id"], &id)
		if id != "" {
			if mark, err := json.Marshal(groomPrompt(id, "")); err == nil {
				m["order"] = mark
			}
			// Ожидание разговора едет тем же полем и в том же виде, что у
			// строки доски: груминг спрашивает в чате и ждёт там же, а строка
			// накопителя помечает это кружком на кнопке чата. Второго признака
			// ожидания у накопителя нет. Имя разговора у груминга это task-<ID>
			// (sessionChatName), туда же кладёт ответ панель, и признак лежит
			// под тем же именем.
			if w, ok := askWaiting(projPath, id, s.now()); ok {
				if mark, err := json.Marshal(w); err == nil {
					m["waiting"] = mark
				}
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

// draftHere отвечает на вопрос, лежит ли за этим ID запись накопителя. Спрашивает
// его отказ экрана задачи: доска и архив дают строку, а до грумминга ID живёт
// только файлом в docs/tasks/drafts/, и без этой проверки ссылка на черновик
// упиралась в «нет строки».
func draftHere(projectPath, id string) bool {
	abs, _ := draftPathOf(projectPath, id)
	st, err := os.Stat(abs)
	return err == nil && !st.IsDir()
}

// draftTitleOf читает заголовок записи накопителя и отвечает заодно, лежит ли
// она на месте. Спрашивает его блок «Связи»: лестница названий кончалась
// архивом, и упоминание черновика доезжало на экран задачей без названия.
// Заголовок берётся тем же разбором, что у файла задачи: первая строка вида
// «# DK-NNN: ...», префикс с номером с неё снимается.
func draftTitleOf(projectPath, id string) (string, bool) {
	abs, _ := draftPathOf(projectPath, id)
	text, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	return searchDocTitle(id, string(text)), true
}

// draftHash это база правки: короткий хэш текста записи. Экран получает его с
// текстом и возвращает при сохранении, а ручка сверяет с тем, что лежит на
// диске. Писателей у файла двое, человек с экрана и агент разбора, и без базы
// правка одного молча затирала бы правку другого.
func draftHash(text []byte) string {
	sum := sha256.Sum256(text)
	return fmt.Sprintf("%x", sum)[:12]
}

// draftBusy называет живую сессию разбора этой записи, а пустая строка значит,
// что запись свободна. Спрашивают его двое: подъём разбора (второй грумер
// поверх работающего) и правка текста (пока разбор идёт, запись принадлежит
// агенту, решение 3 LLD DK-354). Разговор про запись работой не считается: у
// него та же tmux-сессия, а файла он не трогает.
func (s *server) draftBusy(projPath, id string) string {
	talk := s.tmuxTalk(projPath)
	for _, name := range tmuxSessions() {
		if name != draftSession(id) && name != "goal-"+id {
			continue
		}
		if talk[name] {
			continue
		}
		return name
	}
	return ""
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
	out := map[string]any{
		"id": id, "file": rel, "text": string(text), "order": groomPrompt(id, ""),
		// База правки едет вместе с текстом: экран вернёт её при сохранении, и
		// разошедшуюся ручка отобьёт вместо молчаливого затирания.
		"hash": draftHash(text)}
	// И ожидание разговора тем же полем: кнопка чата груминга на экране записи
	// помечает его так же, как строка накопителя.
	if wait, ok := askWaiting(found.Path, id, s.now()); ok {
		out["waiting"] = wait
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDraftGroom поднимает сессию грумминга черновика. Проверки те же и в том
// же порядке, что у запуска задачи: без tmux и без claude сессия умерла бы
// молча, а поверх живой работы с тем же ID вторую поднимать нельзя.
// handleDraftPut переписывает текст записи целиком: экран черновика правит её
// тем же полем, что экран задачи правит постановку, и своей команды на это у
// taskctl нет. Пустой текст отбивается до записи: он затёр бы запись, а
// удаление у черновика своё, с причиной в коммит доски. Отказов, кроме
// пустоты, два, и оба про второго писателя: живой разбор и разошедшаяся база.
func (s *server) handleDraftPut(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "правка черновика")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	var body struct {
		Text string `json:"text"`
		Base string `json:"base"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, taskTextLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("текст длиннее предела %d КБ: в черновике лежит запись, а не вложение", taskTextLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	path, rel := draftPathOf(found.Path, id)
	was, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("черновика %s в %s нет: файла %s не видно, грумминг мог уже завести по нему задачу", id, found.Name, rel)})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустой текст затёр бы запись черновика: жду JSON {\"text\": \"...\"}"})
		return
	}
	// Замок разбора: пока по записи идёт грумминг, файл принадлежит агенту, он
	// его читает, дописывает и уносит исходом. Правка человека под живым
	// разбором либо пропала бы под ним, либо сделала бы исход ответом не на тот
	// текст. Исключение одно: агент, ждущий ответа, спит в инструменте ожидания
	// и файла не трогает, и ответ правкой текста это законная дорога.
	if _, waits := askWaiting(found.Path, id, s.now()); !waits {
		if sess := s.draftBusy(found.Path, id); sess != "" {
			s.logf("правка черновика %s в %s отклонена: разбор идёт (tmux-сессия %s)", id, found.Name, sess)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("по записи %s идёт разбор (tmux-сессия %s): пока он идёт, запись принадлежит агенту, сначала стоп", id, sess)})
			return
		}
	}
	// Сверка базы: запись могли переписать вторым окном или разбором, пока
	// человек набирал. Текущий текст с хэшем едут в отказ, чтобы экран показал
	// оба, а набранное не пропало.
	if now := draftHash(was); body.Base != now {
		why := fmt.Sprintf("запись %s изменилась с тех пор, как экран её прочитал: правка пришла с базой %q, а на диске %q",
			id, body.Base, now)
		if body.Base == "" {
			why = fmt.Sprintf("правка записи %s пришла без базы: сверять её не с чем, а писателей у записи двое, экран и разбор", id)
		}
		s.logf("правка черновика %s в %s отклонена: база разошлась", id, found.Name)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": why, "text": string(was), "hash": now})
		return
	}
	text := strings.TrimRight(body.Text, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("черновик не записался: %v", err)})
		return
	}
	resp := map[string]string{"id": id, "file": rel, "hash": draftHash([]byte(text)),
		"message": fmt.Sprintf("текст %s записан", rel)}
	if note := commitDocs(found.Path, boardCommitMsg(id, "правка черновика с дашборда"), rel); note != "" {
		resp["note"] = note
		s.logf("правка черновика %s в %s: %s", id, found.Name, note)
	}
	s.logf("черновик %s в %s переписан с дашборда", id, found.Name)
	writeJSON(w, http.StatusOK, resp)
}

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
	ask, want, wantTier, ok := s.draftAsk(w, r, id)
	if !ok {
		return
	}
	// Имя подписки сверяется с раскладкой машины той же проверкой, что у
	// запуска задачи: экран, устаревший на смену конфига, поднимал бы разбор
	// неизвестно на чём.
	var harness *Harness
	if want != "" {
		h, why := s.harnesses().pick(want)
		if h == nil {
			s.logf("груминг %s в %s отклонён: %s", id, found.Name, why)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
			return
		}
		harness = h
	}
	// Ярус разворачивается в модель раскладкой машины, и сверяется он с ней же:
	// имени яруса, которого у подписки нет, верить нельзя, как и имени подписки.
	// Названного яруса в теле может не быть вовсе, тогда берётся pro; не знает
	// раскладка и его (пустой список, agentctl не отвечает), значит разбор идёт
	// как раньше, клиентом без модели: отказывать тут не за что.
	view := s.harnesses()
	own := harness
	if own == nil {
		own = view.byDefault()
	}
	tier := wantTier
	if tier == "" {
		tier = groomTier
	}
	model := ""
	if own != nil {
		model = own.tierModel(tier)
	}
	if model == "" && wantTier != "" {
		known := "у подписки нет ни одного яруса"
		if own != nil && len(own.tierNames()) > 0 {
			known = "ярусы подписки: " + strings.Join(own.tierNames(), ", ")
		}
		why := fmt.Sprintf("яруса %s в раскладке машины нет, %s", wantTier, known)
		s.logf("груминг %s в %s отклонён: %s", id, found.Name, why)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
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
	// Живой разбор спрашивается тем же вопросом, что и правка текста: разойтись
	// этим двум местам нельзя, иначе экран запирал бы редактор там, где второй
	// грумер поднимается, и наоборот.
	if busy := s.draftBusy(found.Path, id); busy != "" {
		s.logf("грумминг %s в %s отклонён: tmux-сессия %s уже идёт", id, found.Name, busy)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("работа %s уже идёт (tmux-сессия %s): поднимать грумминг поверх живой сессии нельзя, сначала стоп", id, busy)})
		return
	}
	// Остаток прошлого разбора: сессия жива, а хода в ней нет. Повторный
	// груминг по той же записи как раз с этого и начинается, и отказ тут стоял
	// бы поперёк собственной кнопки экрана.
	for _, name := range tmuxSessions() {
		if name != sess && name != "goal-"+id {
			continue
		}
		s.logf("грумминг %s в %s: остаток прошлого разбора в tmux-сессии %s снят", id, found.Name, name)
		s.chatWatchOff(name)
		runProc("tmux", "kill-session", "-t", name)
	}
	if m := claudeMissing(); m != "" {
		s.logf("грумминг %s в %s не удался: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Груминг идёт живым чатом, а не headless-ходом (замечание 15
	// двенадцатого круга POC): человек смотрит на разбор в той же панели, где
	// разговаривает со всеми, а сессия называет себя в реестре записью
	// накопителя, и найти её потом можно тем же списком чатов.
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		groomCmd(s.launchEnv(id, sess, ""), groomPrompt(id, ask)+" "+orderRules(sess),
			harness, model)); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("грумминг %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	// Разбор ждут той же дорогой, что и чат: экран зовёт chatSewHere, а та
	// опрашивает поиск по имени tmux-сессии. Без отметки подъёма сторож про эту
	// сессию не знает, поиск молчит, и умерший грумер вешает на панели ту же
	// немую петлю ожидания, ради которой затевалась DK-728. Разговора у разбора
	// пока нет, поэтому смерть поедет в журнал задачи.
	s.chatRaised(sess, "", id, found.Name)
	s.logf("грумминг %s в %s поднят (tmux-сессия %s%s)", id, found.Name, sess, harnessTail(harness))
	message := fmt.Sprintf("грумминг %s поднят в tmux-сессии %s: разбор доведёт черновик до строки Backlog либо снимет его с причиной", id, sess)
	if ask != "" {
		message = fmt.Sprintf("грумминг %s поднят заново в tmux-сессии %s, уточнение уехало в заказ: агент перечитает черновик и пойдёт с начала", id, sess)
	}
	out := map[string]string{
		"id": id, "kind": "task", "session": sess, "prompt": groomPrompt(id, ask),
		"message": message, "tier": tier,
	}
	if model != "" {
		out["model"] = model
		out["message"] = fmt.Sprintf("%s, ярус %s (%s)", message, tier, model)
	}
	if harness != nil {
		out["harness"] = harness.Name
		out["message"] = fmt.Sprintf("грумминг %s поднят на подписке %s (tmux-сессия %s)", id, harness.Name, sess)
	}
	writeJSON(w, http.StatusOK, out)
}

// groomCmd собирает команду сессии разбора. Клиент чужой подписки поднимается
// её же обвязкой (agentctl exec), как у чата и у конвейера задачи: пары
// окружения второй подписки собирает она, а не дашборд, и режим разрешений ей
// называется флагом, иначе свежий профиль встал бы на первом же вопросе.
// Окружение приходит доводом: собирает его одно место на все дороги подъёма
// (launchEnv). Прежде разбор звал сборку сам, и стоило ей разойтись с прочими
// дорогами, как сессии разбора пропадали из панели.
func groomCmd(env, prompt string, h *Harness, model string) string {
	client := defaultClient
	if h != nil && !h.Default {
		client = shQuote(binPath(agentctlBin)) + " exec --harness " + shQuote(h.Name) +
			" -- " + shQuote(h.Bin) + " --permission-mode auto"
	}
	if model != "" {
		// Ярус разбора называется явно всякой подписке: без флага клиент берёт
		// свой дефолт, а он бывает и верхним ярусом, за который человек платить
		// не собирался. Имя из лестницы второй подписки её клиент принимает
		// той же дорогой, что и чат (DK-750).
		client += " --model " + shQuote(model)
	}
	return env + client + " " + shQuote(prompt)
}

// draftAsk достаёт уточнение из тела запроса. Тело бывает и пустым: кнопка
// «Грумить» шлёт заказ без слов, а уточнение приходит только с поля
// повторной ходки.
func (s *server) draftAsk(w http.ResponseWriter, r *http.Request, id string) (string, string, string, bool) {
	var body struct {
		Ask string `json:"ask"`
		// Подписка выбирается при запуске, как у конвейера задачи: разбор
		// черновика это такая же работа агента, и платить за неё человек хочет
		// той же квотой, какую выбрал (замечание пользователя).
		Harness string `json:"harness"`
		// Ярус едет тем же телом, что и подписка: подписка выбирает контур, а
		// ярус вес модели, и без него разбор шёл дефолтом клиента.
		Tier string `json:"tier"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftAskLimit)).Decode(&body)
	switch {
	case errors.Is(err, io.EOF):
		return "", "", "", true
	case err != nil:
		var mbe *http.MaxBytesError
		text := "жду JSON {\"ask\": \"...\"} либо пустое тело"
		if errors.As(err, &mbe) {
			text = fmt.Sprintf("уточнение длиннее предела %d КБ: в заказ груминга едет фраза, а не постановка", draftAskLimit/1024)
		}
		s.logf("груминг %s отклонён: тело запроса не разобралось 400", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": text})
		return "", "", "", false
	}
	return strings.Join(strings.Fields(body.Ask), " "), body.Harness, strings.TrimSpace(body.Tier), true
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
	out, code, err := s.taskctlWrite(found.Path, "draft", "drop", id, "--reason", reason)
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

// Исхода разбора дашборд не пересказывает: разговор с агентом всегда идёт в
// чате, там же виден и его исход, а на доске он виден по факту, строкой или её
// отсутствием (решение пользователя). Прежде тут жила ручка, собиравшая исход
// по следам на диске, и форма записи пересказывала его шестью состояниями
// вместо самой записи.

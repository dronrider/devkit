package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Правка строки и файла задачи с дашборда (LLD DK-112, «Экран задачи»):
// заголовок, слагаемые ранга, цена, зависимости в обе стороны и заведение
// файла задачи. Доску правит taskctl подпроцессом, своей записи в
// docs/TASKS.md у сервера нет: правду про формат держит утилита, и её отказы
// (кривая разбивка ранга, цикл зависимостей, линкованный worktree) доезжают
// до экрана её же словами. Порядок строк Backlog утилита выводит из ранга
// сама, поэтому ручки «поставить строку на место N» в API нет.

// taskTextLimit ограничивает тело правки файла задачи: это постановка на
// пару экранов, а не вложение.
const taskTextLimit = 256 << 10

// rankNames это слагаемые ранга по RANKING.md в порядке колонки R. Дашборд
// называет их в отказах, а проверку значений держит taskctl.
var rankNames = [5]string{"серьёзность", "ценность", "неопределённость", "поправка на баг", "рычаг"}

// boardRow это строка доски, как её отдаёт taskctl list --json, плюс секция,
// в которой она нашлась.
type boardRow struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	After   []string `json:"after,omitempty"`
	Fail    string   `json:"fail,omitempty"`
	Block   string   `json:"block,omitempty"`
	Type    string   `json:"type"`
	P       string   `json:"p"`
	R       int      `json:"r"`
	RParts  []int    `json:"r_parts"`
	Cost    string   `json:"cost"`
	Link    string   `json:"link"`
	Notes   []string `json:"notes,omitempty"`
	Sect    string   `json:"sect"`
	Section string   `json:"section"`
}

// parseBoardRows раскладывает ответ taskctl по ID: экрану задачи нужна одна
// строка, но заголовки соседей нужны карточке зависимостей.
func parseBoardRows(raw json.RawMessage) (map[string]boardRow, error) {
	var v struct {
		Sections []struct {
			Key   string     `json:"key"`
			Title string     `json:"title"`
			Rows  []boardRow `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	rows := map[string]boardRow{}
	for _, sec := range v.Sections {
		for _, row := range sec.Rows {
			row.Sect, row.Section = sec.Key, sec.Title
			rows[row.ID] = row
		}
	}
	return rows, nil
}

// taskctlDo гоняет изменяющую доску команду taskctl в корне проекта. Отказ
// утилиты это 400 с её же словами: рубежи живут в ней, и переписывать их
// дашборд не берётся. Ненайденный бинарь и сорванный срок это 502, там
// сломан не ввод, а окружение.
func taskctlDo(dir string, args ...string) (string, int, error) {
	return taskctlDoIn(dir, "", args...)
}

// taskctlDoIn это taskctlDo с текстом на входе подпроцесса: так уезжает текст
// черновика (разбор аргументов и страж подкоманд его бы не пропустили).
func taskctlDoIn(dir, stdin string, args ...string) (string, int, error) {
	bin := taskctlPath()
	if bin == "" {
		return "", http.StatusBadGateway, errors.New(taskctlMissing())
	}
	out, err := runProcIn(stdin, bin, append(args, "-C", dir)...)
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// Сорванный срок и прочие обломы запуска это поломка окружения, а
			// не отказ утилиты: ненулевой код она вернуть не успела.
			return "", http.StatusBadGateway, errors.New(procErr(err))
		}
		return "", http.StatusBadRequest, errors.New(procErr(err))
	}
	return strings.TrimSpace(string(out)), 0, nil
}

// taskRow находит проект и строку задачи на его доске; не найдя, отвечает
// словами сам и возвращает ok=false.
func (s *server) taskRow(w http.ResponseWriter, r *http.Request) (found *Project, id string, row boardRow, rows map[string]boardRow, ok bool) {
	found = s.findProject(w, r)
	if found == nil {
		return nil, "", boardRow{}, nil, false
	}
	id = r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return nil, "", boardRow{}, nil, false
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return nil, "", boardRow{}, nil, false
	}
	rows, err = parseBoardRows(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return nil, "", boardRow{}, nil, false
	}
	row, hit := rows[id]
	if !hit {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("на доске %s нет строки %s", found.Name, id)})
		return nil, "", boardRow{}, nil, false
	}
	return found, id, row, rows, true
}

// depRef это соседняя строка в карточке зависимостей: ID с заголовком и
// секцией, чтобы «после» и «держит» читались без похода на доску.
type depRef struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Sect    string `json:"sect,omitempty"`
	Section string `json:"section,omitempty"`
	R       int    `json:"r,omitempty"`
	P       string `json:"p,omitempty"`
	Note    string `json:"note,omitempty"`
}

func depRefs(ids []string, rows map[string]boardRow) []depRef {
	out := []depRef{}
	for _, id := range ids {
		row, hit := rows[id]
		if !hit {
			// Зависимость без строки это закрытая задача: она уехала в архив,
			// и молчать про неё нельзя, иначе ID выглядит опечаткой.
			out = append(out, depRef{ID: id, Note: "нет на доске: закрыта или в архиве"})
			continue
		}
		out = append(out, depRef{ID: id, Title: row.Title, Sect: row.Sect, Section: row.Section, R: row.R, P: row.P})
	}
	return out
}

// taskDeps спрашивает зависимости строки в обе стороны у самой утилиты:
// «после» лежит в заголовке строки, «держит» считается обратным поиском по
// доске, и оба направления знает dep list --json.
func taskDeps(dir, id string) (after, blocks []string, err error) {
	bin := taskctlPath()
	if bin == "" {
		return nil, nil, errors.New(taskctlMissing())
	}
	out, err := runProc(bin, "dep", "list", id, "--json", "-C", dir)
	if err != nil {
		return nil, nil, fmt.Errorf("taskctl dep list %s: %s", id, procErr(err))
	}
	var v struct {
		After  []string `json:"after"`
		Blocks []string `json:"blocks"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, nil, fmt.Errorf("ответ taskctl dep list не разобрался: %v", err)
	}
	return v.After, v.Blocks, nil
}

func taskFileRel(id string) string {
	return filepath.ToSlash(filepath.Join("docs", "tasks", id+".md"))
}

// handleTask отдаёт всё, из чего рисуется экран задачи: строку доски,
// зависимости в обе стороны и текст файла задачи.
func (s *server) handleTask(w http.ResponseWriter, r *http.Request) {
	found, id, row, rows, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	after, blocks, err := taskDeps(found.Path, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{
		"project": found.Name,
		"id":      id,
		"row":     row,
		"after":   depRefs(after, rows),
		"blocks":  depRefs(blocks, rows),
	}
	rel := taskFileRel(id)
	if text, err := os.ReadFile(filepath.Join(found.Path, filepath.FromSlash(rel))); err == nil {
		resp["file"] = rel
		resp["text"] = string(text)
	} else {
		// Пустой текст без причины неотличим от пустого файла: сервер говорит,
		// что файла нет и чем он заводится.
		resp["note"] = fmt.Sprintf("файла задачи %s нет: заводит его taskctl file, кнопка «Завести файл»", rel)
	}
	writeJSON(w, http.StatusOK, resp)
}

// rankArg собирает разбивку ранга для taskctl set. Слагаемые правятся по
// одному: пропущенное (null) остаётся прежним, разом идёт весь список.
// Пределы значений проверяет утилита по RANKING.md, дашборд их не повторяет.
func rankArg(parts []*int, cur []int) (string, error) {
	if len(parts) != len(rankNames) {
		return "", fmt.Errorf("в разбивке ранга %d слагаемых, жду %d по RANKING.md: %s",
			len(parts), len(rankNames), strings.Join(rankNames[:], ", "))
	}
	merged := mergeRank(parts, cur)
	out := make([]string, len(merged))
	for i, v := range merged {
		out[i] = strconv.Itoa(v)
	}
	return strings.Join(out, "+"), nil
}

// mergeRank накладывает правку слагаемых на то, что стоит в строке:
// пропущенное (null) остаётся прежним. По этой же склейке проверяется тип,
// потому что правка едет одним запросом.
func mergeRank(parts []*int, cur []int) []int {
	out := make([]int, len(parts))
	for i, p := range parts {
		switch {
		case p != nil:
			out[i] = *p
		case i < len(cur):
			out[i] = cur[i]
		}
	}
	return out
}

// parseRank разбирает разбивку строкой «а+б+в+г+д». Кривую строку тут никто не
// ругает: пределы и формат держит taskctl, дашборду разбор нужен ровно для
// сверки поправки на баг с типом.
func parseRank(s string) []int {
	fields := strings.Split(s, "+")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		v, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// bugPartRefusal держит шкалу RANKING.md: поправка на баг это про дефект или
// регресс, у новой работы её не бывает. Правило живёт и на клиенте, но ручка
// повторяет его своими руками: экран не единственный, кто ходит в API.
func bugPartRefusal(typ string, parts []int) string {
	if typ != "task" || len(parts) < 4 || parts[3] == 0 {
		return ""
	}
	return "поправка на баг у типа task не ставится: по RANKING.md " +
		"она про дефект или регресс, а не про новую работу; смени тип на bug"
}

// boardCommitMsg собирает subject коммита доски: ID в subject обязателен, по
// нему находятся коммиты задачи (ядро правил доски).
func boardCommitMsg(id, what string) string {
	return fmt.Sprintf("docs(tasks): %s %s", id, what)
}

func (s *server) handleTaskPatch(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, row, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		Title  string `json:"title"`
		Type   string `json:"type"`
		Rank   string `json:"rank"`
		RParts []*int `json:"r_parts"`
		Cost   string `json:"cost"`
		Link   string `json:"link"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON с полями title, type, r_parts (или rank), cost, link"})
		return
	}
	rank := body.Rank
	if body.RParts != nil {
		if rank != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "жду либо rank строкой «а+б+в+г+д», либо r_parts списком слагаемых, но не оба сразу"})
			return
		}
		parts, err := rankArg(body.RParts, row.RParts)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rank = parts
	}
	args := []string{"set", id}
	for _, f := range []struct{ flag, val string }{
		{"--title", body.Title}, {"--type", body.Type}, {"--rank", rank},
		{"--cost", body.Cost}, {"--link", body.Link},
	} {
		if f.val != "" {
			args = append(args, f.flag, f.val)
		}
	}
	if len(args) == 2 {
		// Порядок строк выводится из ранга, и ручки «поставить строку на место
		// N» тут нет: менять нечего это менять нечего.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "нечего менять, жду title, type, r_parts (или rank), cost или link; " +
				"порядок строк на доске выводится из ранга, ставить строку на место N нечем"})
		return
	}
	// Тип и слагаемые правятся одним запросом, поэтому сверяется то, что
	// получится после правки: пропущенное поле берётся из строки.
	typ := body.Type
	if typ == "" {
		typ = row.Type
	}
	merged := row.RParts
	if rank != "" {
		if parsed := parseRank(rank); parsed != nil {
			merged = parsed
		}
	}
	if refusal := bugPartRefusal(typ, merged); refusal != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": refusal})
		return
	}
	out, code, err := taskctlDo(found.Path, args...)
	if err != nil {
		s.logf("правка строки %s в %s не удалась: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"id": id, "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, "правка строки с дашборда"),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md"))); note != "" {
		resp["note"] = note
		s.logf("правка строки %s в %s: %s", id, found.Name, note)
	}
	s.logf("правка строки %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// handleTaskFilePost заводит файл задачи руками утилиты: taskctl file и
// создаёт docs/tasks/<ID>.md по шаблону, и чинит ссылку в строке доски.
func (s *server) handleTaskFilePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	out, code, err := taskctlDo(found.Path, "file", id)
	if err != nil {
		s.logf("заведение файла %s в %s не удалось: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"id": id, "file": taskFileRel(id), "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, "файл задачи заведён с дашборда"),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md")), taskFileRel(id)); note != "" {
		resp["note"] = note
		s.logf("заведение файла %s в %s: %s", id, found.Name, note)
	}
	s.logf("файл задачи %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// handleTaskFilePut кладёт текст файла задачи целиком. Своей команды на это у
// taskctl нет, файл пишется напрямую, как сообщение витку в messages.go, но
// заводится он всё равно утилитой: та же команда чинит ссылку в строке.
func (s *server) handleTaskFilePut(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, taskTextLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("текст длиннее предела %d КБ: в файле задачи лежит постановка, а не вложение", taskTextLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	rel := taskFileRel(id)
	path := filepath.Join(found.Path, filepath.FromSlash(rel))
	if !isFile(path) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("файла задачи %s нет: сначала заведи его кнопкой «Завести файл» (taskctl file), она же чинит ссылку в строке", rel)})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустой текст затёр бы постановку: жду JSON {\"text\": \"...\"}"})
		return
	}
	text := strings.TrimRight(body.Text, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл задачи не записался: %v", err)})
		return
	}
	resp := map[string]string{"id": id, "file": rel,
		"message": fmt.Sprintf("текст %s записан", rel)}
	if note := commitDocs(found.Path, boardCommitMsg(id, "правка файла задачи с дашборда"), rel); note != "" {
		resp["note"] = note
		s.logf("правка файла %s в %s: %s", id, found.Name, note)
	}
	s.logf("файл задачи %s в %s переписан с дашборда", id, found.Name)
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleTaskDepAdd(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"id\": \"DK-NNN\"}: после какой задачи делается эта"})
		return
	}
	dep := body.ID
	if !goalIDRe.MatchString(dep) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", dep)})
		return
	}
	s.depChange(w, found, id, dep, "add")
}

func (s *server) handleTaskDepRm(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	dep := r.PathValue("dep")
	if !goalIDRe.MatchString(dep) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", dep)})
		return
	}
	s.depChange(w, found, id, dep, "rm")
}

// depChange гоняет dep add/rm и коммитит доску. Цикл зависимостей, живую
// строку с незакрытой зависимостью и прочие рубежи держит утилита, её слова
// и уезжают на экран.
func (s *server) depChange(w http.ResponseWriter, found *Project, id, dep, sub string) {
	out, code, err := taskctlDo(found.Path, "dep", sub, id, dep)
	if err != nil {
		s.logf("зависимость %s от %s в %s (%s) не прошла: %v", id, dep, found.Name, sub, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	what := fmt.Sprintf("зависимость от %s с дашборда", dep)
	if sub == "rm" {
		what = fmt.Sprintf("зависимость от %s снята с дашборда", dep)
	}
	resp := map[string]string{"id": id, "dep": dep, "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, what),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md"))); note != "" {
		resp["note"] = note
		s.logf("зависимость %s от %s в %s: %s", id, dep, found.Name, note)
	}
	s.logf("зависимость %s от %s в %s: %s", id, dep, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

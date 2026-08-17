package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Почта любой живой сессии (DK-345, пункт 3 цели DK-397). Реплика с дашборда
// доезжает не только витку цели, а любой идущей сессии: исполнителю задачи в
// боковом дереве, грумеру черновика, окну человека. Носитель общий с целью,
// третьей копии механики нет: письма лежат строками в ящике
// .devkit/mail/<имя>.inbox дерева работы, адресат сессии записан в самой
// строке, а читает и доставляет их тот же почтальон hooks/inbox.py на ходе
// инструмента. Доставленная строка уходит из ящика, поэтому повтором считать
// надо строку, лежащую в ящике, а не отметку.
//
// Имя ящика живёт дольше сессии. task-<ID> это ящик задачи: ответ припаркованной
// задаче ложится туда без адресата и достаётся той сессии, что задачу продолжит
// (парковка и пробуждение за DK-402). sess-<ID> это личный ящик сессии без
// задачи. Ящик не коммитится: дерево работы локально, а почта это разговор, а
// не состояние доски.

// boxLine это строка письма: тот же формат, что у «Входящих» цели, с адресатом
// перед подписью дашборда. Разбор адресата и подписи один у дашборда и
// почтальона (addressee/said в hooks/inbox.py), и менять его можно только
// обеими сторонами разом.
func boxLine(at time.Time, sid, text string) string {
	return at.Format("2006-01-02 15:04") + ", сессии " + sid + ", из дашборда: " + text
}

// sessionTree называет дерево, в котором работает сессия. Пустой хвост
// каталога транскриптов это главное дерево проекта, непустой назвает боковое
// дерево задачи ../<проект>-<хвост>. Дерево обязано существовать: реплика в
// удалённое дерево легла бы в ящик, который никто не читает.
func sessionTree(projPath, suffix string) (string, bool) {
	if suffix == "" {
		return projPath, true
	}
	tree := filepath.Join(filepath.Dir(projPath), filepath.Base(projPath)+"-"+suffix)
	if fi, err := os.Stat(tree); err != nil || !fi.IsDir() {
		return "", false
	}
	return tree, true
}

// boxLockWait это срок ожидания замка ящика. Замок держит почтальон или
// соседняя отправка на секунды, и подождать их дешевле, чем отбить реплику.
const boxLockWait = 2 * time.Second

// takeBoxLock берёт flock файла замка ящика, тот же замок, каким собирается
// почтальон (take_mail_lock в hooks/inbox.py): без него отправка на пустом
// месте и доставка, убирающая ящик, теряли бы письма друг друга.
func takeBoxLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(boxLockWait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readBoxLines читает лежащие письма; пустой файл и отсутствие ящика это одно
// и то же «писем нет».
func readBoxLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// sessionBoxName называет ящик сессии. Задача узнаётся теми же тремя
// источниками, что и на экране сессий (sessionTask), и её ящик предпочитается
// личному: вопрос и ответ задачи живут в одном месте. Цель сюда не попадает:
// у неё своя ручка и свой носитель, «Входящие» файла цели.
func (s *server) sessionBoxName(projPath string, info sessionInfo, head sessionHead) (string, string) {
	task, note := sessionTask(info.suffix, head)
	if task == "" {
		return "sess-" + info.ID, note
	}
	if raw, err := s.projectBoard(projPath); err == nil {
		if rows, err := parseBoardRows(raw); err == nil {
			if row, ok := rows[task]; ok && isGoalTitle(row.Title) {
				return "", task
			}
		}
	}
	return "task-" + task, note
}

// handleSessionMessagePost кладёт реплику человека в ящик живой сессии.
func (s *server) handleSessionMessagePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "почта сессии")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	info, ok := findSession(s.transcriptRoots(), found.Path, sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf(
			"сессия %s среди транскриптов проекта %s не нашлась: письмо кладётся только известной по журналу сессии", sid, found.Name)})
		return
	}
	tree, ok := sessionTree(found.Path, info.suffix)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"дерево сессии %s не нашлось рядом с %s: боковое дерево удалено, и реплику читать некому", sid, found.Path)})
		return
	}
	head := s.sessionHeadCached(info.path, info.stamp)
	name, note := s.sessionBoxName(found.Path, info, head)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
			"сессия %s ведёт цель: переписка с целью идёт её ручкой, POST /api/projects/%s/goals/%s/message",
			sid, found.Name, note)})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("сообщение длиннее предела %d КБ: в ящик кладётся короткая строка", msgBodyLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое сообщение класть некуда: жду JSON {\"text\": \"...\"}"})
		return
	}
	dir := filepath.Join(tree, ".devkit", "mail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("каталог ящика не создался: %v", err)})
		return
	}
	lock, err := takeBoxLock(filepath.Join(dir, name+".lock"))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "ящик держит соседний прогон: почта расходится под замком, попробуйте ещё раз"})
		return
	}
	defer lock.Close()
	box := filepath.Join(dir, name+".inbox")
	for _, lying := range readBoxLines(box) {
		if strings.HasSuffix(lying, ": "+text) || lying == text {
			s.logf("повтор сообщения сессии %s в %s: письмо уже лежит в ящике %s", sid, found.Name, name)
			writeJSON(w, http.StatusOK, map[string]any{
				"session": sid, "box": name, "line": lying,
				"message": fmt.Sprintf("такое письмо уже лежит в ящике %s: второго не завожу, сессия %s прочитает одно", name, sid)})
			return
		}
	}
	line := boxLine(s.now(), sid, text)
	f, err := os.OpenFile(box, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ящик не записался: %v", err)})
		return
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ящик не записался: %v", err)})
		return
	}
	f.Close()
	resp := map[string]any{
		"session": sid, "box": name, "tree": tree, "line": line,
		"message": fmt.Sprintf("реплика легла в ящик %s дерева сессии %s: почтальон доставит её в идущий ход", name, sid),
	}
	// Честность о стоящей сессии та же, что у ручки цели (DK-319): письмо
	// ляжет и дождётся хода, но пообещать доставку сейчас нельзя, и человек
	// узнаёт это сразу, а не молчанием.
	if stale := s.now().Sub(info.mod); stale > sessionLiveTTL {
		resp["idle"] = true
		resp["message"] = fmt.Sprintf(
			"реплика легла в ящик %s, но транскрипт сессии %s молчит уже %s: возможно, она не идёт, и письмо дождётся её хода",
			name, sid, stale.Truncate(time.Minute))
	}
	s.logf("реплика для сессии %s в %s легла в ящик %s", sid, found.Name, name)
	writeJSON(w, http.StatusOK, resp)
}

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

// Разговор с любой живой сессией (DK-345, пункт 3 цели DK-397). Реплика с
// дашборда доезжает не только витку цели, а любой идущей сессии: исполнителю
// задачи в боковом дереве, грумеру черновика, окну человека. Носитель общий с
// целью, третьей копии механики нет: реплики лежат строками во входе разговора
// .devkit/chat/<имя>.in дерева работы, адресат сессии записан в самой строке, а
// читает и доставляет их тот же подхват hooks/chat-in.py на ходе инструмента.
// Доставленная строка уходит из входа, поэтому повтором считать надо строку,
// лежащую во входе, а не отметку.
//
// Имя разговора живёт дольше сессии. task-<ID> это разговор задачи: ответ
// припаркованной задаче ложится туда без адресата и достаётся той сессии, что
// задачу продолжит (парковка и пробуждение за DK-402). sess-<ID> это личный
// разговор сессии без задачи. Вход не коммитится: дерево работы локально, а
// говорят тут с живой сессией, а не с доской.

// chatLine это строка разговора: тот же формат, что у «Входящих» цели, с
// адресатом перед подписью дашборда. Разбор адресата и подписи один у дашборда
// и подхвата (addressee/said в hooks/chat-in.py), и менять его можно только
// обеими сторонами разом.
func chatLine(at time.Time, sid, text string) string {
	return at.Format("2006-01-02 15:04") + ", сессии " + sid + ", из дашборда: " + text
}

// Каталог входов разговора и расширение файла (DK-440). Пишет дашборд только
// новую пару, а прежнюю (.devkit/mail, <имя>.inbox) один выпуск дочитывает
// подхват: строка, легшая до выката, доезжает его ходом, а не пропадает.
const (
	chatDir    = "chat"
	chatSuffix = ".in"
)

// sessionTree называет дерево, в котором работает сессия. Пустой хвост
// каталога транскриптов это главное дерево проекта, непустой назвает боковое
// дерево задачи ../<проект>-<хвост>. Дерево обязано существовать: реплика в
// удалённое дерево легла бы во вход, который никто не читает.
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

// chatLockWait это срок ожидания замка разговора. Замок держит подхват или
// соседняя отправка на секунды, и подождать их дешевле, чем отбить реплику.
const chatLockWait = 2 * time.Second

// takeChatLock берёт flock файла замка разговора, тот же замок, каким
// собирается подхват (take_chat_lock в hooks/chat-in.py): без него отправка на
// пустом месте и доставка, убирающая вход, теряли бы строки друг друга.
func takeChatLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(chatLockWait)
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

// readChatLines читает лежащие реплики; пустой файл и отсутствие входа это
// одно и то же «сказать нечего».
func readChatLines(path string) []string {
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

// sessionChatName называет разговор сессии. Задача берётся из реестра чатов
// тем же порядком, что на экране сессий (sessionBinds.task), и разговор задачи
// предпочитается личному: вопрос и ответ задачи живут в одном месте. Разговор о
// задаче сюда не попадает: сессия, угаданная по первой реплике, писала бы во
// вход чужой работы, и ей остаётся свой личный вход. Цель тоже не попадает: у
// неё своя ручка и свой носитель, «Входящие» файла цели.
func (s *server) sessionChatName(projPath string, info sessionInfo, head sessionHead) (string, string) {
	task, note, bound := s.binds().task(info.ID, info.suffix, head)
	if task == "" || bound != boundLead {
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

// handleSessionMessagePost кладёт реплику человека во вход живой сессии.
func (s *server) handleSessionMessagePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "разговор сессии")
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
			"сессия %s среди транскриптов проекта %s не нашлась: реплика кладётся только известной по журналу сессии", sid, found.Name)})
		return
	}
	tree, ok := sessionTree(found.Path, info.suffix)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"дерево сессии %s не нашлось рядом с %s: боковое дерево удалено, и реплику читать некому", sid, found.Path)})
		return
	}
	head := s.sessionHeadCached(info.path, info.stamp)
	name, note := s.sessionChatName(found.Path, info, head)
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
				"error": fmt.Sprintf("сообщение длиннее предела %d КБ: во вход разговора кладётся короткая строка", msgBodyLimit/1024)})
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
	dir := filepath.Join(tree, ".devkit", chatDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("каталог разговоров не создался: %v", err)})
		return
	}
	lock, err := takeChatLock(filepath.Join(dir, name+".lock"))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "разговор держит соседний прогон: строки расходятся под замком, попробуйте ещё раз"})
		return
	}
	defer lock.Close()
	src := filepath.Join(dir, name+chatSuffix)
	for _, lying := range readChatLines(src) {
		if strings.HasSuffix(lying, ": "+text) || lying == text {
			s.logf("повтор сообщения сессии %s в %s: строка уже лежит в разговоре %s", sid, found.Name, name)
			writeJSON(w, http.StatusOK, map[string]any{
				"session": sid, "chat": name, "line": lying,
				"message": fmt.Sprintf("такая реплика уже лежит в разговоре %s: второй не завожу, сессия %s прочитает одну", name, sid)})
			return
		}
	}
	line := chatLine(s.now(), sid, text)
	f, err := os.OpenFile(src, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("вход разговора не записался: %v", err)})
		return
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("вход разговора не записался: %v", err)})
		return
	}
	f.Close()
	resp := map[string]any{
		"session": sid, "chat": name, "tree": tree, "line": line,
		"message": fmt.Sprintf("реплика легла в разговор %s дерева сессии %s: подхват доставит её в идущий ход", name, sid),
	}
	// Честность о стоящей сессии та же, что у ручки цели (DK-319): строка
	// ляжет и дождётся хода, но пообещать доставку сейчас нельзя, и человек
	// узнаёт это сразу, а не молчанием.
	if stale := s.now().Sub(info.mod); stale > sessionLiveTTL {
		resp["idle"] = true
		resp["message"] = fmt.Sprintf(
			"реплика легла в разговор %s, но транскрипт сессии %s молчит уже %s: возможно, она не идёт, и строка дождётся её хода",
			name, sid, stale.Truncate(time.Minute))
	}
	s.logf("реплика для сессии %s в %s легла в разговор %s", sid, found.Name, name)
	writeJSON(w, http.StatusOK, resp)
}

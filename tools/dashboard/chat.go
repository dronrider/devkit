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

// taskChatLine это безадресная строка разговора задачи. Адрес у реплики это
// адресат разговора, а не состояние собеседника (LLD DK-430, решение 2):
// реплика сессии всегда идёт с адресатом, реплика задаче всегда без него, и
// выбирает это ручка, а не догадка о живости. Безадресную строку берёт любая
// сессия дерева, поэтому ответ припаркованной задаче достаётся той, что задачу
// продолжит, а сторожок считает ответом только такую строку.
func taskChatLine(at time.Time, text string) string {
	return at.Format("2006-01-02 15:04") + ", из дашборда: " + text
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

// putChat кладёт готовую строку во вход разговора name дерева tree под тем же
// замком, каким собирается подхват. Непустой возврат lying это лежащая копия
// той же реплики: второй строки повтор не заводит, иначе сессия прочитала бы
// сказанное дважды. Писатель тут один на обе ручки: правило адресности живёт в
// строке, а не в записи файла, и разойтись двум записям нельзя.
func putChat(tree, name, text, line string) (lying string, code int, err error) {
	dir := filepath.Join(tree, ".devkit", chatDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("каталог разговоров не создался: %v", err)
	}
	lock, err := takeChatLock(filepath.Join(dir, name+".lock"))
	if err != nil {
		return "", http.StatusServiceUnavailable, errors.New(
			"разговор держит соседний прогон: строки расходятся под замком, попробуйте ещё раз")
	}
	defer lock.Close()
	src := filepath.Join(dir, name+chatSuffix)
	for _, l := range readChatLines(src) {
		if strings.HasSuffix(l, ": "+text) || l == text {
			return l, 0, nil
		}
	}
	f, err := os.OpenFile(src, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", http.StatusBadGateway, fmt.Errorf("вход разговора не записался: %v", err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return "", http.StatusBadGateway, fmt.Errorf("вход разговора не записался: %v", err)
	}
	f.Close()
	return "", 0, nil
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
	line := chatLine(s.now(), sid, text)
	lying, code, err := putChat(tree, name, text, line)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if lying != "" {
		s.logf("повтор сообщения сессии %s в %s: строка уже лежит в разговоре %s", sid, found.Name, name)
		writeJSON(w, http.StatusOK, map[string]any{
			"session": sid, "chat": name, "line": lying,
			"message": fmt.Sprintf("такая реплика уже лежит в разговоре %s: второй не завожу, сессия %s прочитает одну", name, sid)})
		return
	}
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

// parkedByAsk узнаёт строку, припаркованную вопросом: секция Blocked с машинным
// разрядом причины «вопрос:». Тот же разбор ведёт сторожок, паркуя и будя
// строку (parked_rows в tools/devkitctl/watch.py), и разряд тут читается
// машинно, а не по словам причины.
func parkedByAsk(row boardRow) bool {
	return row.Sect == "blocked" && strings.HasPrefix(strings.TrimSpace(row.Block), askBlockWord)
}

// askBlockWord это машинный разряд причины блока у парковки вопросом.
const askBlockWord = "вопрос:"

// Куда класть реплику разговора: «session» это живой разговор и ручка сессии,
// «task» это кончившийся разговор с живой задачей, и реплику берёт ручка
// задачи, а пустое значение это кончившийся разговор без задачи, у которого
// ввод гаснет (LLD DK-430, решения 2 и 6).
const (
	replyToSession = "session"
	replyToTask    = "task"
)

// chatReply называет ручку для реплики и причину словами. Кончившийся разговор
// мерится жёстко и машинно, порог живости транскрипта тут не работает вовсе:
// под него попадает окно человека, отошедшего от стола, а ошибка стоит реплики,
// ушедшей мимо адресата. Признаков три, и хватает любого: дерева сессии больше
// нет, задача припаркована вопросом, сессия поднята дашбордом, а tmux-сессии с
// её именем в списке уже нет. Не сошлось ничего, значит разговор живой, и
// реплика идёт адресно, даже когда транскрипт молчит час.
func (s *server) chatReply(projPath string, info sessionInfo, rows map[string]boardRow) (reply, note string) {
	rec := s.binds()[info.ID]
	over := ""
	switch {
	case !treeAlive(projPath, info.suffix):
		over = "дерева сессии больше нет"
	case info.Task != "" && parkedByAsk(rows[info.Task]):
		over = "задача " + info.Task + " припаркована вопросом"
	case rec.Source == bindOrder && rec.Tmux != "" && tmuxMissingCheck() == "" && !tmuxAlive(rec.Tmux):
		over = "сессию поднимал дашборд, а tmux-сессии " + rec.Tmux + " в списке уже нет"
	}
	if over == "" {
		return replyToSession, ""
	}
	_, onBoard := rows[info.Task]
	if info.Task == "" || info.Bound != boundLead || (rows != nil && !onBoard) {
		return "", over + ": разговор кончился, и продолжить его некому"
	}
	return replyToTask, over + ": реплика уйдёт задаче " + info.Task + ", её возьмёт тот, кто её продолжит"
}

// treeAlive это первый признак кончившегося разговора: дерево, в котором шла
// сессия, снесено слиянием задачи.
func treeAlive(projPath, suffix string) bool {
	_, ok := sessionTree(projPath, suffix)
	return ok
}

// tmuxAlive ищет имя среди живых tmux-сессий машины.
func tmuxAlive(name string) bool {
	for _, t := range tmuxList() {
		if t.Name == name {
			return true
		}
	}
	return false
}

// handleTaskMessagePost кладёт реплику человека во вход задачи основного
// чекаута безадресной строкой. Это ответ там, где живой сессии нет вовсе:
// припаркованная вопросом строка ждёт ответа, сторожок будит её по лежащей
// безадресной строке, а адресованную мёртвой сессии реплику не взял бы никто.
// Дерево тут не выбирается: чекаут переживает дерево задачи, которое сносится
// слиянием, и сторожок читает оба места (LLD DK-430, решение 2).
func (s *server) handleTaskMessagePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, row, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	if isGoalTitle(row.Title) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
			"%s это цель: переписка с целью идёт её ручкой, POST /api/projects/%s/goals/%s/message",
			id, found.Name, id)})
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
	name := "task-" + id
	line := taskChatLine(s.now(), text)
	lying, code, err := putChat(found.Path, name, text, line)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if lying != "" {
		s.logf("повтор сообщения задаче %s в %s: строка уже лежит в разговоре %s", id, found.Name, name)
		writeJSON(w, http.StatusOK, map[string]any{
			"task": id, "chat": name, "line": lying,
			"message": fmt.Sprintf("такая реплика уже лежит в разговоре %s: второй не завожу, задача прочитает одну", name)})
		return
	}
	resp := map[string]any{
		"task": id, "chat": name, "tree": found.Path, "line": line,
		"message": fmt.Sprintf(
			"реплика легла в разговор %s основного чекаута без адресата: её возьмёт первый же ход сессии задачи", name),
	}
	if parkedByAsk(row) {
		resp["parked"] = true
		resp["message"] = fmt.Sprintf(
			"реплика легла в разговор %s основного чекаута: строка %s припаркована вопросом, и ближайший тик сторожка вернёт её в работу",
			name, id)
	}
	s.logf("реплика задаче %s в %s легла в разговор %s", id, found.Name, name)
	writeJSON(w, http.StatusOK, resp)
}

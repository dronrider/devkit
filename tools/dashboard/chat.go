package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
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

// Механика носителя живёт в общем пакете internal/chat: строка с адресатом и
// без него, замок, вход и признак ожидания там одни на всех, потому что реплики
// пишет дашборд, а ждёт их taskctl ask (LLD DK-430, решение 3). Тут остаётся
// выбор ручки и ответ человеку словами.

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

// sessionChatName называет разговор сессии. Задача берётся из реестра чатов
// тем же порядком, что на экране сессий (sessionBinds.task), и разговор задачи
// предпочитается личному: вопрос и ответ задачи живут в одном месте. Разговор о
// задаче сюда не попадает: сессия, угаданная по первой реплике, писала бы во
// вход чужой работы, и ей остаётся свой личный вход. Цель тоже не попадает: у
// неё своя ручка и свой носитель, «Входящие» файла цели.
func (s *server) sessionChatName(projPath string, info sessionInfo, head sessionHead) (string, string) {
	task, note, bound := bindTask(s.binds(), info.ID, info.suffix, head)
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

// putChat кладёт строку во вход разговора и переводит отказ носителя в код
// ответа: занятый замок это «попробуйте ещё раз», остальное отказ носителя.
func putChat(tree, name, text, line string) (lying string, code int, err error) {
	lying, err = chat.Put(tree, name, text, line)
	switch {
	case errors.Is(err, chat.ErrLocked):
		return "", http.StatusServiceUnavailable, errors.New(
			"чат держит соседний прогон: строки расходятся под замком, попробуйте ещё раз")
	case err != nil:
		return "", http.StatusBadGateway, err
	}
	return lying, 0, nil
}

// sayToAsk кладёт реплику во вход разговора, когда сессия стоит на вопросе
// инструмента ожидания, и говорит ручке чата, что дорога выбрана. Второй
// возврат false значит, что никто тут не ждёт и реплика едет обычной дорогой.
//
// Дорога выбирается не живостью сессии, а живым признаком ожидания: сокет
// клиента слышит сам клиент, а ждущий сидит в ходе Bash и читает вход
// разговора раз в секунду. Признак сверяется по сессии, потому что вопрос
// одного собеседника не делает ждущими всех: ответ соседу забрал бы ждущий, и
// оба разговора получили бы не своё.
func (s *server) sayToAsk(p *Project, info sessionInfo, sid, text string) (map[string]any, bool) {
	head := s.sessionHeadCached(info.path, info.stamp)
	name, _ := s.sessionChatName(p.Path, info, head)
	// У сессии цели имени разговора нет, её реплики живут «Входящими» файла
	// цели, но вопрос розданной работы задаётся и ей: скан ниже общий для
	// всех разговоров.
	if name != "" {
		if done, ok := s.ownAskReply(p, info, sid, name, text); ok {
			return done, true
		}
	}
	return s.handedAskReply(p, sid, text)
}

// ownAskReply отвечает на вопрос, который задан в разговоре этой же задачи:
// признак лежит под именем разговора сессии, и ждёт ровно она.
// Признак лежит во входе основного чекаута: туда его кладёт taskctl ask,
// какое бы дерево ни было у самого хода.
func (s *server) ownAskReply(p *Project, info sessionInfo, sid, name, text string) (map[string]any, bool) {
	ask, has := chat.ReadAsk(chat.AskPath(p.Path, name))
	if !has || !s.now().Before(ask.Until) || ask.Session != sid {
		return nil, false
	}
	tree, ok := sessionTree(p.Path, info.suffix)
	if !ok {
		return nil, false
	}
	// Строка идёт с адресатом и ложится в дерево ждущей сессии: оттуда её
	// берёт и само ожидание (оно опрашивает своё дерево и чекаут), и подхват,
	// если ход ожидания к тому времени уже кончился.
	lying, _, err := putChat(tree, name, text, chat.Line(s.now(), sid, text))
	if err != nil {
		// Вход не взял строку: обычная дорога тут лучше отказа, реплика уедет
		// сокетом и человек её не потеряет.
		s.logf("ответ ждущей сессии %s во вход %s не лёг, иду обычной дорогой: %v", sid, name, err)
		return nil, false
	}
	out := map[string]any{"way": "ask", "chat": name, "task": ask.Task,
		"until": ask.Until.Unix(),
		"message": fmt.Sprintf(
			"ответ лёг во вход разговора %s: его ждёт инструмент ожидания и заберёт в тот же ход", name)}
	if lying != "" {
		out["message"] = "эта реплика уже лежит во входе разговора " + name + ", второй раз она не поедет"
	}
	s.logf("ответ человека сессии %s ушёл во вход разговора %s: сессия стоит на вопросе до %s",
		sid, name, ask.Until.Format("15:04:05"))
	return out, true
}

// handedAskReply отвечает на вопрос розданной работы: признак лежит под именем
// чужой задачи, а сессией в нём назван этот разговор (субагент ходит с ID
// внешней сессии) либо его делегат по реестру. Строка идёт безадресной во вход
// основного чекаута: его опрашивает само ожидание, а не дождавшийся срока
// заход оставляет её сторожку, и та же строка будит припаркованную вопросом
// задачу. Вопросов бывает несколько, пачка исполнителей спрашивает вразнобой:
// ответ уезжает ближнему по сроку, остальные названы в ответе ручки.
func (s *server) handedAskReply(p *Project, sid, text string) (map[string]any, bool) {
	asks := handedAsks(p.Path, sid, s.binds(), s.now())
	if len(asks) == 0 {
		return nil, false
	}
	h := asks[0]
	lying, _, err := putChat(p.Path, h.Name, text, chat.TaskLine(s.now(), text))
	if err != nil {
		// Вход не взял строку: обычная дорога тут лучше отказа, реплика уедет
		// сокетом и человек её не потеряет.
		s.logf("ответ раздавшего разговора %s во вход %s не лёг, иду обычной дорогой: %v", sid, h.Name, err)
		return nil, false
	}
	out := map[string]any{"way": "ask", "chat": h.Name, "task": h.Ask.Task,
		"until": h.Ask.Until.Unix(),
		"message": fmt.Sprintf(
			"ответ лёг во вход разговора %s: его ждёт инструмент ожидания и заберёт в тот же ход", h.Name)}
	if lying != "" {
		out["message"] = "эта реплика уже лежит во входе разговора " + h.Name + ", второй раз она не поедет"
	}
	if len(asks) > 1 {
		var rest []string
		for _, a := range asks[1:] {
			rest = append(rest, a.Ask.Task)
		}
		out["message"] = fmt.Sprintf("%s; следом ждут ответа %s",
			out["message"], strings.Join(rest, ", "))
	}
	s.logf("ответ раздавшего разговора %s ушёл во вход %s: вопрос задачи %s ждёт до %s",
		sid, h.Name, h.Ask.Task, h.Ask.Until.Format("15:04:05"))
	return out, true
}

// handleSessionMessagePost кладёт реплику человека во вход живой сессии.
func (s *server) handleSessionMessagePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "чат сессии")
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
				"error": fmt.Sprintf("сообщение длиннее предела %d КБ: во вход чата кладётся короткая строка", msgBodyLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	// Вход разговора строчный: одна реплика это одна строка файла, и перенос
	// там рассыпал бы её на несколько. Схлопывание тут нарочное, в отличие от
	// канала живых сессий, где переносы доезжают как есть (chatText).
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое сообщение класть некуда: жду JSON {\"text\": \"...\"}"})
		return
	}
	line := chat.Line(s.now(), sid, text)
	lying, code, err := putChat(tree, name, text, line)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if lying != "" {
		s.logf("повтор сообщения сессии %s в %s: строка уже лежит в чате %s", sid, found.Name, name)
		writeJSON(w, http.StatusOK, map[string]any{
			"session": sid, "chat": name, "line": lying,
			"message": fmt.Sprintf("такая реплика уже лежит в чате %s: второй не завожу, сессия %s прочитает одну", name, sid)})
		return
	}
	resp := map[string]any{
		"session": sid, "chat": name, "tree": tree, "line": line,
		"message": fmt.Sprintf("реплика легла в чат %s дерева сессии %s: подхват доставит её в идущий ход", name, sid),
	}
	// Честность о стоящей сессии та же, что у ручки цели (DK-319): строка
	// ляжет и дождётся хода, но пообещать доставку сейчас нельзя, и человек
	// узнаёт это сразу, а не молчанием.
	if stale := s.now().Sub(info.mod); stale > sessionLiveTTL {
		resp["idle"] = true
		resp["message"] = fmt.Sprintf(
			"реплика легла в чат %s, но транскрипт сессии %s молчит уже %s: возможно, она не идёт, и строка дождётся её хода",
			name, sid, stale.Truncate(time.Minute))
	}
	s.saidSay(saidSessionKey(sid), text, "вход сессии")
	s.logf("реплика для сессии %s в %s легла в чат %s", sid, found.Name, name)
	writeJSON(w, http.StatusOK, resp)
}

// parkedByAsk узнаёт строку, припаркованную вопросом: секция Blocked с машинным
// разрядом причины «вопрос:». Тот же разбор ведёт сторожок, паркуя и будя
// строку (parked_rows в tools/devkitctl/watch.py), и разряд тут читается
// машинно, а не по словам причины.
func parkedByAsk(row boardRow) bool {
	return parkedBlock(row.Sect, row.Block)
}

// parkedBlock это тот же разбор по секции и причине врозь: строку доски
// разметка ответа видит общими картами, а не типом boardRow, и состояние
// ожидания считается там по тем же двум полям.
func parkedBlock(sect, block string) bool {
	return sect == "blocked" && strings.HasPrefix(strings.TrimSpace(block), askBlockWord)
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
func (s *server) chatReply(projPath string, info sessionInfo, rows map[string]boardRow, alive func(string) bool) (reply, note string) {
	rec := s.binds()[info.ID]
	over := ""
	switch {
	case !treeAlive(projPath, info.suffix):
		over = "дерева сессии больше нет"
	case info.Task != "" && parkedByAsk(rows[info.Task]):
		over = "задача " + info.Task + " припаркована вопросом"
	case rec.Source == bindOrder && rec.Tmux != "" && !alive(rec.Tmux):
		over = "сессию поднимал дашборд, а tmux-сессии " + rec.Tmux + " в списке уже нет"
	}
	if over == "" {
		return replyToSession, ""
	}
	_, onBoard := rows[info.Task]
	if info.Task == "" || info.Bound != boundLead || (rows != nil && !onBoard) {
		return "", over + ": чат кончился, и продолжить его некому"
	}
	return replyToTask, over + ": реплика уйдёт задаче " + info.Task + ", её возьмёт тот, кто её продолжит"
}

// treeAlive это первый признак кончившегося разговора: дерево, в котором шла
// сессия, снесено слиянием задачи.
func treeAlive(projPath, suffix string) bool {
	_, ok := sessionTree(projPath, suffix)
	return ok
}

// tmuxAliveFn отдаёт меру живости tmux-сессии на один заход: список машины
// спрашивается один раз, даже когда мерить надо десяток разговоров списка.
// Спрашивается он лениво: у разговора из бокового дерева, которого уже нет,
// дело до tmux не доходит вовсе. Машина без tmux мерой не работает, и все
// имена там считаются живыми: иначе список разговоров разом объявил бы их
// кончившимися, не имея на то ни одного признака.
func tmuxAliveFn() func(string) bool {
	if tmuxMissingCheck() != "" {
		return func(string) bool { return true }
	}
	var names map[string]bool
	return func(name string) bool {
		if names == nil {
			names = map[string]bool{}
			for _, t := range tmuxList() {
				names[t.Name] = true
			}
		}
		return names[name]
	}
}

// taskNoLeadWhy это причина, по которой реплика во вход задачи остаётся
// недоставленной. Безадресную строку входа забирает только сессия, ведущая эту
// задачу (hooks/chat-in.py, owns_chat). Ведущей сессии нет, значит строка лежит
// во входе и ждёт, а обещать доставку тут нельзя: ровно так реплика человека
// уехала посторонней сессии того же чекаута (живой случай DK-466).
const taskNoLeadWhy = "работа по задаче не идёт, отвечать некому, и реплика ждёт во входе задачи"

// Причина у повтора: строка человека уже стоит в очереди задачи, и вторую ей
// заводить незачем. Отказ повтора это подтверждение, а не ошибка, и пузырь в
// панели от него не пропадает (живой случай DK-466).
const taskInQueueWhy = "реплика уже лежит в очереди задачи и ждёт первого хода её сессии: " +
	"второй строки не завожу"

// taskLead отвечает, ведёт ли задачу хоть одна живая сессия. Признак тут тот
// же, каким подхват решает, отдавать ли ей безадресную строку: разговор жив и
// задача у него своя. Окно vscode считается живым наравне с tmux, как считает
// его живым и кольцо агентов: своей tmux-сессии у него нет, а ход оно делает и
// строку входа заберёт.
func (s *server) taskLead(projPath, id string) bool {
	if id == "" {
		return false
	}
	for _, e := range s.chatEntries(projPath, chatListLimit) {
		if e.State != chatLive && e.State != chatVscode {
			continue
		}
		if hasTask(e.Tasks, id) {
			return true
		}
	}
	return false
}

// handleTaskMessageDelete снимает лежащую во входе задачи реплику: человек
// отменил недоставленное в панели. Без этой ручки отмена убирала пузырь с
// экрана, а строка оставалась в очереди и уезжала агенту первым же ходом
// (живой случай DK-466).
func (s *server) handleTaskMessageDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустую реплику отменять нечем: жду JSON {\"text\": \"...\"}"})
		return
	}
	name := chat.TaskName(id)
	gone, err := chat.Drop(found.Path, name, text)
	if err != nil {
		if errors.Is(err, chat.ErrLocked) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "чат держит соседний прогон: попробуйте ещё раз"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// Строки уже нет: её забрал подхват, и отмена опоздала. Это не ошибка, а
	// то, что человеку надо знать: реплика уехала агенту.
	if gone == 0 {
		s.logf("отмена реплики задаче %s в %s: строки во входе %s уже нет", id, found.Name, name)
		writeJSON(w, http.StatusOK, map[string]any{"task": id, "chat": name, "dropped": 0,
			"message": "реплики во входе задачи уже нет: её забрал ход агента"})
		return
	}
	s.logf("реплика задаче %s снята из чата %s в %s", id, name, found.Name)
	writeJSON(w, http.StatusOK, map[string]any{"task": id, "chat": name, "dropped": gone,
		"message": "реплика снята из очереди задачи"})
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
				"error": fmt.Sprintf("сообщение длиннее предела %d КБ: во вход чата кладётся короткая строка", msgBodyLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	// Вход разговора строчный: одна реплика это одна строка файла, и перенос
	// там рассыпал бы её на несколько. Схлопывание тут нарочное, в отличие от
	// канала живых сессий, где переносы доезжают как есть (chatText).
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое сообщение класть некуда: жду JSON {\"text\": \"...\"}"})
		return
	}
	name := chat.TaskName(id)
	line := chat.TaskLine(s.now(), text)
	lying, code, err := putChat(found.Path, name, text, line)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	repeat := lying != ""
	resp := map[string]any{
		"task": id, "chat": name, "tree": found.Path, "line": line,
		"message": fmt.Sprintf(
			"реплика легла в чат %s основного чекаута без адресата: её возьмёт первый же ход сессии задачи", name),
	}
	switch {
	case repeat:
		// Повтор это не отказ, а подтверждение: та же реплика уже лежит в
		// очереди задачи, и второй строки ей не надо. Прежде этот ответ уходил
		// мимо общего разбора, без признака доставки, и панель считала реплику
		// доставленной: пузырь снимался, а лента пустела совсем (живой случай
		// DK-466, повтор недоставленной реплики).
		resp["line"] = lying
		resp["repeat"] = true
		resp["message"] = fmt.Sprintf(
			"такая реплика уже лежит в чате %s: второй строки не завожу, задача прочитает одну", name)
		s.logf("повтор сообщения задаче %s в %s: строка уже лежит в чате %s", id, found.Name, name)
	case parkedByAsk(row):
		resp["parked"] = true
		resp["message"] = fmt.Sprintf(
			"реплика легла в чат %s основного чекаута: строка %s припаркована вопросом, и ближайший тик сторожка вернёт её в работу",
			name, id)
	}
	// Ведущей сессии у задачи нет ни одной. Строка остаётся лежать во входе, и
	// панель показывает реплику недоставленной с причиной: молчаливое
	// «доставлено» тут обещало бы адресата, которого нет.
	if !s.taskLead(found.Path, id) {
		resp["undelivered"] = true
		resp["why"] = taskNoLeadWhy
		if repeat {
			resp["why"] = taskInQueueWhy
		}
		// Прежние слова остаются на месте, а приписка добавляет к ним главное:
		// адресата пока нет. Парковка и пробуждение сторожком тут по-прежнему
		// правда, и стирать их ради приписки нельзя.
		resp["message"] = fmt.Sprintf("%s. Ведущей сессии у задачи нет, и реплика ждёт во входе",
			resp["message"])
		s.logf("реплика задаче %s в %s легла в чат %s без ведущей сессии", id, found.Name, name)
	}
	if repeat {
		// Второй записи о сказанном повтор не заводит: слова человека уже
		// записаны первой попыткой.
		writeJSON(w, http.StatusOK, resp)
		return
	}
	s.saidSay(saidTaskKey(id), text, "вход задачи")
	s.logf("реплика задаче %s в %s легла в чат %s", id, found.Name, name)
	writeJSON(w, http.StatusOK, resp)
}

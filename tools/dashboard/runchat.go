package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/sessions"
)

// Стоп у работы, которая идёт в окне разговора (DK-716). Строку задачи ведёт
// не только конвейер: чат дашборда, окно терминала и окно vscode двигают её
// теми же командами доски, и до этой правки «Стоп» у такой строки либо не
// показывался вовсе, либо шёл убивать tmux-сессию task-<ID>, которой нет.
//
// Разница с конвейером одна и она про обратимость. Конвейер снимают целиком:
// сессия у него служебная, возобновление это новый запуск, и он прочитает
// состояние с диска. Разговор снимать нельзя, у него память человека: там
// прерывают ход двумя Escape (chatStop), кладут в реестр «снята» и говорят об
// этом в самой ленте, чтобы агент, вернувшись, прочитал причину, а не гадал,
// почему его прервали.

// stopChat это рабочая сессия задачи, которую можно остановить отсюда: у неё
// есть наше окно, и клавиатура к нему.
type stopChat struct {
	Session string `json:"session"`
	Tmux    string `json:"tmux"`
	Title   string `json:"title,omitempty"`
	Live    string `json:"live,omitempty"`
	Moved   int64  `json:"moved,omitempty"`
}

// rowWorks собирает работы, присвоившие строку id. Спрашивается тут тот же
// признак, каким строка получила «Стоп» на экране: разойдись они, кнопка
// предлагала бы остановить работу, которой ручка не находит.
func rowWorks(works []Work, id string) []Work {
	var out []Work
	for _, w := range works {
		if w.Talk {
			continue
		}
		if hasRow(workRows(w), id) {
			out = append(out, w)
		}
	}
	return out
}

// stoppableChats отбирает из работ строки те, чей ход прерывается отсюда:
// сессия своя, окно наше, и ход в ней идёт. Стоящую работу сюда не берём:
// прерывать там нечего, а «снята» рукой это отдельное действие с формы.
// Порядок свежими сверху: при нескольких сессиях выбор человеку показывают
// списком, и первым в нём стоит тот, кто ходил последним.
func stoppableChats(rowed []Work) []stopChat {
	var out []stopChat
	for _, w := range rowed {
		if w.Via == workViaTmux || !w.Own || w.Tmux == "" {
			continue
		}
		if w.Live != workBusy && w.Live != workWait {
			continue
		}
		out = append(out, stopChat{Session: w.Session, Tmux: w.Tmux,
			Title: w.Title, Live: w.Live, Moved: w.Moved})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Moved > out[j].Moved })
	return out
}

// stopChatWord это слова, которые агент прочитает в своей ленте. Причина
// названа прямо: ход прерван человеком со строки задачи, и работа по ней
// кончена, а разговор жив и следующую реплику возьмёт.
func stopChatWord(id string) string {
	return fmt.Sprintf("работа по задаче %s остановлена человеком со строки доски: "+
		"текущий ход прерван, привязка сессии к задаче снята. "+
		"Разговор живой, следующая реплика придёт сюда же", id)
}

// stopChatWaitWord это слова агенту, когда стоп ещё дожимается: ход прерван, а
// его фоновая работа жива, и вернувшись из неё, агент прочитает в ленте, что
// продолжать нечего.
func stopChatWaitWord(id string) string {
	return fmt.Sprintf("работа по задаче %s остановлена человеком со строки доски: "+
		"текущий ход прерван, а фоновые субагенты дорабатывают своё. "+
		"Новых заданий не начинай, всякий поднявшийся ход будет прерван снова", id)
}

// stopChatWorkRelease кладёт в реестр конец работы сессии над задачей. Запись
// именная: соседние задачи того же разговора она не трогает, разбор в
// sessions.Works.
func (s *server) stopChatWorkRelease(sid, id, project, tmux string) error {
	b := sessionBind{Task: id, Source: bindOff, Project: project, Tmux: tmux}
	return appendBind(s.bindsPath(), sessions.Line(s.now(), sid, b, "стоп со строки доски"))
}

// stopChatWork прерывает ход рабочего разговора и снимает его с задачи. При
// нескольких рабочих сессиях выбор остаётся за человеком: остановить чужую
// работу вслепую дороже, чем спросить, а какая из них «та самая», знает только
// он. Выбранную называет параметр session.
func (s *server) stopChatWork(w http.ResponseWriter, found *Project, id string, chats []stopChat, want string) {
	pick := chats[0]
	if want != "" {
		hit := false
		for _, c := range chats {
			if c.Session == want {
				pick, hit = c, true
				break
			}
		}
		if !hit {
			s.logf("стоп %s отклонён: сессия %s по строке не работает 404", id, want)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf(
				"сессия %s по задаче %s не работает: останавливать в ней нечего", want, id)})
			return
		}
	} else if len(chats) > 1 {
		s.logf("стоп %s: по строке работают %d сессий, выбор за человеком", id, len(chats))
		writeJSON(w, http.StatusConflict, map[string]any{
			"id": id, "sessions": chats,
			"error": fmt.Sprintf("по задаче %s работают %d сессии: выбери, в какой прервать ход",
				id, len(chats))})
		return
	}
	if err := chatStop(pick.Tmux); err != nil {
		s.logf("стоп %s в %s не удался: прерывание не подалось в %s: %v", id, found.Name, pick.Tmux, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"прерывание не подалось в tmux-сессию %s: %s", pick.Tmux, procErr(err))})
		return
	}
	resp := map[string]any{"id": id, "kind": "chat", "session": pick.Session,
		"tmux": pick.Tmux, "state": "стоп"}
	// Фоновая работа переживает прерванный ход, и стоп на ней не кончается.
	// Субагент допишет своё и поднимет агента новым ходом, а снятая сейчас
	// привязка объявила бы строку свободной посреди идущей работы. Заказ
	// дожима держит и то и другое: строка стоит под «Стопом», а всякий
	// поднявшийся ход прерывается снова (stopwait.go).
	if s.chatSubBusy(found.Path, pick.Session) {
		s.stopWaitSet(pick.Tmux, pick.Session, id, found.Name, found.Path)
		s.saidMark(saidSessionKey(pick.Session), stopChatWaitWord(id))
		resp["state"] = "останавливается"
		resp["message"] = fmt.Sprintf("стоп: ход разговора %s прерван, но по %s ещё работают "+
			"фоновые субагенты; строка стоит под «Стопом», привязка снимется, когда работа встанет",
			pick.Tmux, id)
		s.logf("стоп %s в %s: ход разговора %s (сессия %s) прерван, фоновая работа жива, стоп дожимается",
			id, found.Name, pick.Tmux, pick.Session)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// Привязка снимается после удавшегося прерывания: снятая наперёд, она
	// вернула бы строке кнопку запуска при живом ходе.
	if err := s.stopChatWorkRelease(pick.Session, id, found.Name, pick.Tmux); err != nil {
		resp["note"] = fmt.Sprintf("привязка сессии к задаче не снялась: реестр %s не записался: %v",
			s.bindsPath(), err)
		s.logf("стоп %s в %s: %v", id, found.Name, resp["note"])
	}
	// Слова про остановку идут в ленту самого разговора: агент вернётся в неё
	// следующим ходом и прочитает причину там же, где читает всё остальное
	// (дорога DK-728).
	s.saidMark(saidSessionKey(pick.Session), stopChatWord(id))
	if note := s.sayStop(found.Path, found.Name, id, "chat"); note != "" {
		resp["note"] = strings.TrimPrefix(fmt.Sprintf("%v; %s", resp["note"], note), "<nil>; ")
		s.logf("стоп %s в %s: %s", id, found.Name, note)
	}
	resp["message"] = fmt.Sprintf("стоп: ход разговора %s прерван, работа по %s снята; "+
		"разговор жив и следующую реплику возьмёт", pick.Tmux, id)
	s.logf("стоп %s в %s: ход разговора %s (сессия %s) прерван, привязка снята",
		id, found.Name, pick.Tmux, pick.Session)
	writeJSON(w, http.StatusOK, resp)
}

// stopWithoutPipeline отвечает на стоп строки, за которой нет конвейерной
// сессии. Дорог три. Работа идёт в нашем окне разговора, значит прерывается
// ход. Работы за строкой нет вовсе, значит останавливать нечего. Работа есть, а
// снять её отсюда нечем: цикл цели ведёт сессия, которую дашборд не поднимал, а
// чужое окно ведёт человек, и клавиатуры к нему у дашборда нет.
func (s *server) stopWithoutPipeline(w http.ResponseWriter, r *http.Request, found *Project, id string, rowed []Work) {
	if chats := stoppableChats(rowed); len(chats) > 0 {
		s.stopChatWork(w, found, id, chats, strings.TrimSpace(r.URL.Query().Get("session")))
		return
	}
	if len(rowed) == 0 {
		s.logf("стоп %s отклонён: работа не идёт 404", id)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("работа %s в проекте %s не идёт: нет ни tmux-сессии "+
				"с префиксом его доски, ни записи в реестре целей", id, found.Name)})
		return
	}
	if rowed[0].Via == workViaRegistry {
		s.logf("стоп %s отклонён: цикл ведёт другая сессия 409", id)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("цикл цели %s ведёт другая сессия, tmux-сессии дашборда у него нет: "+
				"стоп отсюда недоступен, снимать там, где цикл поднят", id)})
		return
	}
	s.logf("стоп %s отклонён: интерактивная сессия 409", id)
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": fmt.Sprintf("работа %s это интерактивная сессия: её ведёт человек в окне, снимать нечего", id)})
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Новый чат по задаче: POST /api/projects/{p}/chats с телом
// {"id": "DK-436", "ask": "..."}. Разговор это не конвейер, и заводится он
// отдельной ручкой (LLD DK-430, решения 1 и 7): имя chat-<ID>-<n> не ложится
// под форму работы в internal/works, в занятость дерева не считается и с
// tmux-сессией task-<ID> не спорит. Отказ «работа уже идёт» остаётся там, где
// он и был, у второго конвейера: два исполнителя на одну строку это столкновение
// в дереве, а два разговора о ней это обычное дело.
//
// Разговора без задачи ручка не поднимает вовсе: заказать такой сессии нечего,
// и кнопка вела бы к пустому окну с промптом из воздуха. Поднятый рукой
// разговор виден списком сам, запись кладёт хук старта.

// chatSessionName выбирает имя нового чата: первый свободный номер среди живых
// tmux-сессий. Номер не растёт вечно, снятый чат отдаёт своё имя следующему:
// человек читает «chat-DK-436-2» как «второй разговор про эту задачу», а не как
// счётчик всех разговоров с начала времён.
func chatSessionName(id string, alive func(string) bool) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("chat-%s-%d", id, n)
		if !alive(name) {
			return name
		}
	}
}

// chatPrompt это первая реплика поднятого чата. Заказ тут разговорный, а не
// рабочий: сессия читает строку и файл задачи, говорит, где задача стоит, и
// ждёт следующих реплик человека. Работу по задаче она не начинает нарочно:
// конвейер поднимают кнопкой рядом, и второй исполнитель, заведённый
// разговором, сошёлся бы с первым в одном дереве. Вопрос человека, если он его
// написал, едет тем же заказом: иначе первый ход уходит на приветствие.
func chatPrompt(id, ask string) string {
	p := "Поговорим про " + id + ": прочитай строку доски и файл задачи, скажи коротко, " +
		"где она стоит и что в ней главное, и жди моих реплик. " +
		"Работу по задаче не начинай и статус не двигай, это разговор, а не заход."
	if ask != "" {
		p += " Первый вопрос: " + ask
	}
	return p
}

// chatCommand собирает команду tmux-сессии разговора. Клиент поднимается
// интерактивным, а не headless: headless-ход кончается вместе с ответом, и
// разговор из одной реплики был бы кончившимся к тому времени, как человек
// прочитает первый ответ. Живая сессия держит своё имя в списке tmux, а на нём
// стоит мера разговора (chatReply), и реплика с панели уходит адресно.
// Окружение то же, что у конвейера: задачу и имя tmux-сессии сессия называет о
// себе в реестре сама, хуком старта.
func chatCommand(agentctl string, h *Harness, prompt, id, sess string) string {
	if h == nil {
		return sessionEnv(id, sess) + defaultClient + " " + shQuote(prompt)
	}
	return sessionEnv(id, sess) + shQuote(agentctl) + " exec --harness " + shQuote(h.Name) + " -- " +
		shQuote(h.Bin) + " " + shQuote(prompt)
}

func (s *server) handleChatStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("подъём чата отклонён: чужой Origin 403")
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "подъём чата")
	if found == nil {
		return
	}
	var body struct {
		ID      string `json:"id"`
		Harness string `json:"harness"`
		// Ask это первый вопрос человека. Пусто значит «просто открой
		// разговор»: сессия расскажет, где задача стоит, и станет ждать.
		Ask string `json:"ask"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil || body.ID == "" {
		s.logf("подъём чата отклонён: битое тело запроса 400")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"id\": \"DK-NNN\"}"})
		return
	}
	id := strings.ToUpper(strings.TrimSpace(body.ID))
	ask := strings.Join(strings.Fields(body.Ask), " ")
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		s.logf("подъём чата %s в %s не удался: доска не прочиталась 502: %v", id, found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	row, ok := findRow(raw, id)
	if !ok {
		s.logf("подъём чата %s в %s отклонён: нет строки на доске 404", id, found.Name)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": rowGone(found, id)})
		return
	}
	// Цель разговаривает своей ручкой и своим носителем, «Входящими» файла
	// цели: поднятая тут сессия реплик с панели не увидела бы вовсе, потому что
	// уходят они мимо неё. Отказ называет дорогу, а не просто отказывает.
	if isGoalTitle(row.Title) {
		why := fmt.Sprintf("%s это цель: разговор с ней идёт её ручкой и её циклом, "+
			"а отдельный чат читал бы чужой вход; спрашивать цель тем же полем ввода панели", id)
		s.logf("подъём чата %s в %s отклонён: строка это цель", id, found.Name)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		s.logf("подъём чата %s в %s отклонён: tmux не нашёлся 502", id, found.Name)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	var harness *Harness
	if body.Harness != "" {
		h, why := s.harnesses().pick(body.Harness)
		if h == nil {
			s.logf("подъём чата %s в %s отклонён: %s", id, found.Name, why)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": why})
			return
		}
		harness = h
	}
	client := defaultClient
	if harness != nil {
		client = harness.Bin
	}
	if m := clientMissing(client); m != "" {
		s.logf("подъём чата %s в %s не удался: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	sess := chatSessionName(id, tmuxAliveFn())
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		chatCommand(binPath(agentctlBin), harness, chatPrompt(id, ask), id, sess)); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("подъём чата %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	// Ответ говорит и про то, чего сейчас не видно: ID сессии рождается позже
	// команды, и в списке разговоров новый чат встаёт первым своим ходом.
	// Молчание тут читалось бы как несработавшая кнопка.
	resp := map[string]string{"id": id, "session": sess,
		"message": fmt.Sprintf("разговор про %s поднят в tmux-сессии %s: в списке он встанет первым своим ходом", id, sess)}
	if harness != nil {
		resp["harness"] = harness.Name
		resp["message"] = fmt.Sprintf(
			"разговор про %s поднят на подписке %s (tmux-сессия %s): в списке он встанет первым своим ходом",
			id, harness.Name, sess)
	}
	s.logf("чат %s поднят в %s (tmux-сессия %s%s)", id, found.Name, sess, harnessTail(harness))
	writeJSON(w, http.StatusOK, resp)
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Имя нового чата это первый свободный номер: живые разговоры своих имён не
// отдают, а снятый отдаёт, и счётчик не растёт до бесконечности.
func TestChatSessionNameTakesFreeNumber(t *testing.T) {
	live := map[string]bool{"chat-XR-002-1": true, "chat-XR-002-3": true}
	alive := func(name string) bool { return live[name] }
	if got := chatSessionName("XR-002", alive); got != "chat-XR-002-2" {
		t.Errorf("имя нового чата %q, ждал первый свободный номер chat-XR-002-2", got)
	}
	if got := chatSessionName("XR-004", alive); got != "chat-XR-004-1" {
		t.Errorf("первый чат задачи назван %q, ждал chat-XR-004-1", got)
	}
}

// Заказ чата разговорный: сессия читает задачу и ждёт реплик, работу по ней не
// начиная, иначе разговор заводил бы второго исполнителя поверх конвейера.
// Вопрос человека едет тем же заказом, чтобы первый ход не уходил на приветствие.
func TestChatPromptTalksNotWorks(t *testing.T) {
	plain := chatPrompt("XR-002", "")
	for _, want := range []string{"Поговорим про XR-002", "жди моих реплик", "Работу по задаче не начинай"} {
		if !strings.Contains(plain, want) {
			t.Errorf("в заказе чата нет %q: %s", want, plain)
		}
	}
	if strings.Contains(plain, "Первый вопрос") {
		t.Errorf("пустой вопрос доехал до заказа: %s", plain)
	}
	asked := chatPrompt("XR-002", "почему ранг такой низкий")
	if !strings.Contains(asked, "Первый вопрос: почему ранг такой низкий") {
		t.Errorf("вопрос человека не доехал до заказа: %s", asked)
	}
}

// startChat поднимает чат и разбирает ответ.
func startChat(t *testing.T, e *testEnv, c *http.Client, ask string) (int, map[string]string) {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats", ask)
	text := body(t, resp)
	var v map[string]string
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ подъёма чата не разобрался: %v (%s)", err, text)
	}
	return resp.StatusCode, v
}

// Чат поднимается поверх живого конвейера той же задачи: отказ «работа уже
// идёт» остаётся у второго конвейера, а разговор с ним не конкурирует, потому
// что зовётся chat-<ID>-<n> и работой не считается (LLD DK-430, решение 1).
// Второй чат встаёт рядом с первым по той же причине.
func TestChatStartOverLiveRun(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "task-XR-002\\n")

	code, first := startChat(t, e, c, `{"id": "XR-002"}`)
	if code != http.StatusOK {
		t.Fatalf("подъём чата поверх конвейера: %d %v", code, first)
	}
	if first["session"] != "chat-XR-002-1" {
		t.Errorf("чат поднят сессией %q, ждал chat-XR-002-1", first["session"])
	}
	if !strings.Contains(first["message"], "первым своим ходом") {
		t.Errorf("ответ молчит о том, когда чат встанет в списке: %v", first)
	}
	calls := readFile(t, tmuxLog)
	if !strings.Contains(calls, "new-session -d -s chat-XR-002-1 -c "+e.proj) {
		t.Errorf("tmux позван не тем именем и не в чекауте проекта: %s", calls)
	}
	for _, want := range []string{"DEVKIT_TASK='XR-002'", "DEVKIT_TMUX='chat-XR-002-1'",
		"claude 'Поговорим про XR-002"} {
		if !strings.Contains(calls, want) {
			t.Errorf("в команде чата нет %q: %s", want, calls)
		}
	}
	// Клиент поднимается интерактивным: headless-ход кончился бы вместе с
	// первым ответом, и разговор был бы кончившимся к тому времени, как человек
	// его прочитает.
	if strings.Contains(calls, "claude -p 'Поговорим") {
		t.Errorf("чат поднят headless-ходом, а не живой сессией: %s", calls)
	}

	// Отказ второго конвейера при этом стоит на месте: два исполнителя на одну
	// строку это столкновение в дереве, и снимали тут не его.
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "уже идёт") {
		t.Errorf("второй конвейер задачи перестал отбиваться: %d %s", resp.StatusCode, text)
	}
}

// Второй чат той же задачи берёт следующий свободный номер, а не отбивается.
func TestChatStartSecondTalk(t *testing.T) {
	e, c, _ := runsEnv(t, "task-XR-002\\nchat-XR-002-1\\n")
	code, v := startChat(t, e, c, `{"id": "XR-002", "ask": "почему ранг такой низкий"}`)
	if code != http.StatusOK || v["session"] != "chat-XR-002-2" {
		t.Fatalf("второй чат задачи: %d %v", code, v)
	}
}

// Живой чат в занятость задачи не считается: строка доски не помечается идущей
// работой, и карточка живой работы у неё не появляется. Иначе разговор о задаче
// читался бы как её исполнение (DK-360).
func TestChatSessionIsNotWork(t *testing.T) {
	e, c, _ := runsEnv(t, "chat-XR-002-1\\n")
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/board", "")
	text := body(t, resp)
	var v struct {
		Works []Work `json:"works"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	for _, w := range v.Works {
		if w.ID == "XR-002" {
			t.Errorf("живой чат стал работой задачи: %+v", w)
		}
	}
	if strings.Contains(text, `"id":"XR-002","title":"Обычная задача","type":"task"`) &&
		strings.Contains(text, `"run":"tmux"`) {
		t.Errorf("строка задачи помечена идущей работой из-за чата: %s", text)
	}
}

// Цель отбивается словами и с дорогой: разговор с ней идёт её ручкой, а
// поднятая тут сессия читала бы чужой вход и молчала бы в ответ.
func TestChatStartGoalRefused(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	code, v := startChat(t, e, c, `{"id": "XR-100"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("подъём чата цели: %d %v", code, v)
	}
	if !strings.Contains(v["error"], "это цель") || !strings.Contains(v["error"], "полем ввода панели") {
		t.Errorf("отказ цели без дороги: %v", v)
	}
}

// Строки нет на доске: заказывать разговор не о чем, и отказ называет причину
// теми же словами, что у запуска работы.
func TestChatStartUnknownRow(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	code, v := startChat(t, e, c, fmt.Sprintf(`{"id": %q}`, "XR-999"))
	if code != http.StatusNotFound || v["error"] == "" {
		t.Fatalf("подъём чата по несуществующей строке: %d %v", code, v)
	}
}

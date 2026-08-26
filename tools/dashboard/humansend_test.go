package main

import (
	"strings"
	"testing"
)

// Агент, которому человек пишет из панели, отвечал ему через канал сессий:
// вызовом SendMessage обратно в сокет дашборда. В ленте это стояло служебным
// ходом инструмента, и человек своего ответа там не находил вовсе (живой
// случай груминга DK-509). Такой ход это реплика разговора, и сервер помечает
// его признаком human.

// peerLine это запись транскрипта с рамкой канала: так реплику человека кладёт
// туда харнес, получив её от дашборда.
func peerLine(sock, text string) string {
	return `{"type":"user","message":{"role":"user","content":"Another Claude session sent a message:\n` +
		`<cross-session-message from=\"` + sock + `\" from-name=\"dashboard\" from-mode=\"prompting\">\n` +
		text + `\n</cross-session-message>"},"timestamp":"2026-08-26T08:28:53.000Z"}` + "\n"
}

// sendLine это ход отправки: агент шлёт сообщение по адресу to.
func sendLine(to, summary, text string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use",` +
		`"id":"toolu_1","name":"SendMessage","input":{"to":"` + to + `","summary":"` + summary +
		`","message":"` + text + `"}}]},"timestamp":"2026-08-26T08:29:10.000Z"}` + "\n"
}

func toolReply(list []reply, summary string) *reply {
	for i := range list {
		if list[i].Role == "tool" && strings.Contains(list[i].Text, summary) {
			return &list[i]
		}
	}
	return nil
}

// Адрес дашборда узнаётся по самой переписке: сокет у него новый на каждый
// запуск, а разговор с прежним адресом лежит в транскрипте и читается дальше.
func TestFeedMarksSendToHuman(t *testing.T) {
	sock := "uds:/tmp/cc-socks/52214.sock"
	data := peerLine(sock, "Поясни свой вопрос подробнее") +
		sendLine(sock, "Пояснение вопроса по DK-509", "Поясняю попроще.") +
		sendLine("agent-42", "Задание субагенту", "Разбери находку.")

	list := parseReplies([]byte(data), 0)
	toMan := toolReply(list, "Пояснение вопроса по DK-509")
	if toMan == nil {
		t.Fatalf("хода отправки человеку нет в ленте: %+v", list)
	}
	if !toMan.Human {
		t.Error("отправка в сокет дашборда не помечена обращением к человеку: в ленте она останется служебным ходом")
	}
	toSub := toolReply(list, "Задание субагенту")
	if toSub == nil {
		t.Fatalf("хода отправки субагенту нет в ленте: %+v", list)
	}
	if toSub.Human {
		t.Error("реплика субагенту помечена обращением к человеку: пузырём разговора ей не место")
	}
}

// Разговор без единой реплики из панели своего адреса не выдумывает: отправка
// в чужой сокет остаётся служебным ходом.
func TestFeedSendWithoutDashboardStaysTool(t *testing.T) {
	data := sendLine("uds:/tmp/cc-socks/99999.sock", "Весть соседу", "Работа кончилась.")
	list := parseReplies([]byte(data), 0)
	got := toolReply(list, "Весть соседу")
	if got == nil {
		t.Fatalf("хода отправки нет в ленте: %+v", list)
	}
	if got.Human {
		t.Error("отправка в незнакомый сокет сочтена обращением к человеку")
	}
}

// Свой сокет тоже считается адресом человека: агент отвечает в него и без
// предыдущей реплики в этом транскрипте.
func TestFeedSendToOwnSocketIsHuman(t *testing.T) {
	data := sendLine(peerSelfAddr(), "Ответ в панель", "Готово.")
	list := parseReplies([]byte(data), 0)
	got := toolReply(list, "Ответ в панель")
	if got == nil {
		t.Fatalf("хода отправки нет в ленте: %+v", list)
	}
	if !got.Human {
		t.Errorf("отправка в свой сокет %s не сочтена обращением к человеку", peerSelfAddr())
	}
}

// Правило канала едет в каждый заказ, где агент разговаривает с человеком.
// Грумер DK-509 выбрал канал как раз потому, что у груминга заказ свой, и
// правило туда не поехало.
func TestOrderRulesCarryChannelEverywhere(t *testing.T) {
	sess := "task-XR-1"
	got := orderRules(sess)
	if !strings.Contains(got, channelRule) {
		t.Fatalf("в общих приписках заказа нет правила канала: %s", got)
	}
	if !strings.Contains(got, planRule) {
		t.Errorf("общие приписки заказа потеряли правило плана: %s", got)
	}
	// Текст правила один на все заказы: сверяется он дословной константой, а не
	// пересказом.
	for name, order := range map[string]string{
		"груминг черновика":  groomPrompt("XR-1", "") + " " + orderRules(sess),
		"конвейер задачи":    runPrompt("in-progress", "XR-1") + " " + orderRules(sess),
		"продолжение задачи": continuePrompt("XR-1", execRotateDefault, sess),
		"виток цели":         goalContinuePrompt("XR-100", execRotateDefault, sess),
		"подъём разговора":   chatCmd("", "opus", "", "посмотри доску", execRotateDefault, nil, "agentctl"),
	} {
		if !strings.Contains(order, channelRule) {
			t.Errorf("в заказе «%s» нет правила канала: %s", name, order)
		}
	}
}

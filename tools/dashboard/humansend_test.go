package main

import (
	"strconv"
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

// Места экрана, выбранные пикером, едут префиксом реплики и в пузыре не
// стоят: человек тычет в элемент вместо описания словами, а описатель нужен
// агенту, не читателю ленты.
func TestFeedCutsPickedBlock(t *testing.T) {
	said := "<picked screen=\"demo/board\">\n" +
		"- div.trow.task, id=row-XR-7 data-task=XR-7, «XR-7 строка задачи», внутри div.tbody < section.card\n" +
		"</picked>\nвот тут ранг не читается"
	data := `{"type":"user","message":{"role":"user","content":` +
		strconv.Quote(said) + `},"timestamp":"2026-08-26T09:00:00.000Z"}` + "\n"
	list := parseReplies([]byte(data), 0)
	if len(list) != 1 {
		t.Fatalf("реплика с местами разобралась не одной записью: %+v", list)
	}
	got := list[0]
	if got.Text != "вот тут ранг не читается" {
		t.Errorf("блок мест остался в словах человека: %q", got.Text)
	}
	if !strings.Contains(got.Pick, "data-task=XR-7") {
		t.Errorf("описатель места не доехал до ленты: %q", got.Pick)
	}
	if got.PickScreen != "demo/board" {
		t.Errorf("экран выбора потерялся: %q", got.PickScreen)
	}
}

// Блок мест не годится в заголовок разговора: его кладёт панель, а не человек.
func TestChatTitleSkipsPickedBlock(t *testing.T) {
	said := "<picked screen=\"demo/board\">\n- div.trow.task\n</picked>\nпочини эту строку"
	if got := cutFirstWraps(said); got != "почини эту строку" {
		t.Errorf("заголовок разговора взят вместе с блоком мест: %q", got)
	}
}

// Ответ агента цитирует реплику, на которую отвечает: механика «ответ на
// сообщение» стоит в самом пузыре, как в мессенджерах, и пару считает сервер.
// Ходы инструментов между репликой и ответом пару не рвут.
func TestFeedQuotesAskInAnswer(t *testing.T) {
	said := func(role, text string) string {
		return `{"type":"` + role + `","message":{"role":"` + role + `","content":` +
			strconv.Quote(text) + `},"timestamp":"2026-08-26T09:00:00.000Z"}` + "\n"
	}
	data := said("user", "почему стенд зелёный на старом коде") +
		sendLine("agent-42", "задание субагенту", "разбери находку") +
		said("assistant", "проверка стояла не на том поле")
	list := parseRepliesSpan([]byte(data), 0, parseSpan{src: "s"})

	var answer *reply
	var ask *reply
	for i := range list {
		if list[i].Role == "assistant" && list[i].Text != "" {
			answer = &list[i]
		}
		if list[i].Role == "user" && list[i].Text != "" {
			ask = &list[i]
		}
	}
	if answer == nil || ask == nil {
		t.Fatalf("реплика с ответом не разобрались: %+v", list)
	}
	if answer.Quote != "почему стенд зелёный на старом коде" {
		t.Errorf("ответ не цитирует реплику человека: %q", answer.Quote)
	}
	if answer.QuoteKey != ask.Key || ask.Key == "" {
		t.Errorf("цитата ведёт не к исходной реплике: %q против %q", answer.QuoteKey, ask.Key)
	}
	if answer.QuoteMany {
		t.Error("одна реплика названа пачкой")
	}
	if ask.Quote != "" {
		t.Errorf("реплика человека получила цитату сама на себя: %q", ask.Quote)
	}
	// Ход инструмента цитаты не носит: он не ответ.
	for _, r := range list {
		if r.Role == "tool" && r.Quote != "" {
			t.Errorf("ход инструмента получил цитату: %+v", r)
		}
	}
}

// Пачка реплик с одним ответом: цитируется последняя, и о пачке сказано
// признаком. Ответ без реплики перед ним цитаты не носит вовсе.
func TestFeedQuotesLastOfPack(t *testing.T) {
	said := func(role, text string) string {
		return `{"type":"` + role + `","message":{"role":"` + role + `","content":` +
			strconv.Quote(text) + `},"timestamp":"2026-08-26T09:00:00.000Z"}` + "\n"
	}
	data := said("user", "первое замечание") + said("user", "второе замечание") +
		said("user", "третье замечание") + said("assistant", "разобрал все три") +
		said("assistant", "и ещё добавлю")
	list := parseRepliesSpan([]byte(data), 0, parseSpan{src: "s"})

	var first, second *reply
	for i := range list {
		if list[i].Role != "assistant" {
			continue
		}
		if first == nil {
			first = &list[i]
			continue
		}
		if second == nil {
			second = &list[i]
		}
	}
	if first == nil || second == nil {
		t.Fatalf("два ответа не разобрались: %+v", list)
	}
	if first.Quote != "третье замечание" {
		t.Errorf("цитируется не последняя реплика пачки: %q", first.Quote)
	}
	if !first.QuoteMany {
		t.Error("о пачке реплик не сказано: подсказке цитаты нечего сообщить")
	}
	if second.Quote != "" || second.QuoteKey != "" {
		t.Errorf("второй ответ подряд цитирует уже отвеченное: %q", second.Quote)
	}
}

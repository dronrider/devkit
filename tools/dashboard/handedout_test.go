package main

import (
	"fmt"
	"testing"
	"time"
)

// Розданная работа в списке чатов (DK-581). Подпроцесс делегирования пишет свой
// транскрипт и до правки всплывал в списке строкой наравне с разговорами
// человека, причём первой: пишет он непрерывно и потому свежее всех. Отличает
// его от разговора поле родителя в реестре машины, которое ставит agentctl run.

// bindParent это строка реестра про сессию с названным родителем; пустой
// родитель это обычный разговор, поднятый сам по себе.
func bindParent(now time.Time, sid, parent string) string {
	if parent == "" {
		parent = "-"
	}
	return fmt.Sprintf("%s сессия %s задача - проект demo дерево /tmp/demo "+
		"транскрипт /tmp/%s.jsonl источник - повод startup tmux - родитель %s\n",
		now.Format("2006-01-02T15:04:05"), sid, sid, parent)
}

// Работа, которую раздал разговор, из списка уходит: видно её ходом в ленте
// того разговора, а второй строкой она только отжимает вниз беседы человека.
func TestChatListHidesHandedOutWork(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Now()
	own := "aaaa1111-1111-4111-8111-111111111111"
	sub := "bbbb2222-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", own, saidLine("разбери задачу", now.Add(-time.Hour)), now.Add(-time.Hour))
	writeSession(t, e.home, e.proj, "", sub, saidLine("Ты исполнитель задачи DK-577", now.Add(-time.Minute)), now.Add(-time.Minute))
	writeBinds(t, e.home, bindParent(now, own, ""), bindParent(now, sub, own))

	list, _ := chatsWindow(t, e, c, "")
	if !chatIn(list, own) {
		t.Errorf("разговор человека пропал из списка: %+v", list)
	}
	if chatIn(list, sub) {
		t.Errorf("розданная работа стоит в списке отдельной строкой: %+v", list)
	}
}

// Работа, поднятая сама по себе (руками, скриптом, мимо agentctl run), родителя
// не имеет и остаётся строкой: пропавший разговор человек ищет руками и злится,
// а лишняя строка стоит дешевле.
func TestChatListKeepsWorkWithoutParent(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Now()
	sub := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", sub, saidLine("Ты исполнитель задачи DK-577", now.Add(-time.Minute)), now.Add(-time.Minute))
	writeBinds(t, e.home, bindParent(now, sub, ""))

	list, _ := chatsWindow(t, e, c, "")
	if !chatIn(list, sub) {
		t.Errorf("работа без названного родителя пропала из списка: %+v", list)
	}
}

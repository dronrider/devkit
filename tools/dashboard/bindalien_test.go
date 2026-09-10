package main

import (
	"testing"
	"time"
)

// Рубеж на чтении реестра (DK-860). Строку в ~/.devkit/sessions.log кладёт не
// только живой разговор: шаг сценария, прогнанный хуком руками, и go test с
// фикстурами пишут туда же, а имя окна и подставную задачу берут из окружения и
// из фикстуры. Такая запись оказывалась свежее записи живого разговора, забирала
// у него имя tmux (панель объявляла его снятым, реплика человека не доходила) и
// вешала на него вымышленные задачи фикстур. Отличает её транскрипт: он лежит
// мимо каталогов сессий харнесов, а настоящий разговор пишет журнал только туда.

// alienBind это строка реестра от прогона: транскрипт назван, но лежит он на
// стороне, как у ручного прогона хука с /tmp/t.jsonl.
func alienBind(stamp, sid, task, proj, tmux string) string {
	return stamp + " сессия " + sid + " задача " + task + " проект demo дерево " + proj +
		" транскрипт /tmp/t.jsonl источник заказ повод startup tmux " + tmux + "\n"
}

// Живой разговор остаётся хозяином своего имени окна, сколько бы свежих записей
// с этим именем ни оставил прогон хука руками.
func TestChatListAlienBindDoesNotClaimTmux(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home,
		"2026-08-20T10:00:00 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+
			" транскрипт "+standTranscript(e.home, sid)+" источник заказ повод startup tmux task-XR-4\n",
		// Прогон хука руками в том же окне: сессия своя, выдуманная, а имя окна
		// приехало из DEVKIT_TMUX живого разговора.
		alienBind("2026-08-20T11:00:00", "rehearse", "XR-4", e.proj, "task-XR-4"))

	var got chatEntry
	for _, ch := range chatsOf(t, e, c) {
		if ch.ID == sid {
			got = ch
		}
		if ch.ID == "rehearse" {
			t.Errorf("прогон встал в список разговором: %+v", ch)
		}
	}
	if got.ID == "" {
		t.Fatal("живой разговор пропал из списка")
	}
	if got.Tmux != "task-XR-4" || got.State != chatLive {
		t.Errorf("прогон забрал имя окна у живого разговора: %+v", got)
	}
	if got.Gone != "" {
		t.Errorf("живой разговор объявлен снятым: %q", got.Gone)
	}
}

// Задачи из фикстур прогона на разговор не садятся: чипы строки собираются по
// записям реестра, а запись прогона в счёт не идёт.
func TestChatListAlienBindGivesNoTaskChips(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", "exit 1")
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home,
		"2026-08-20T10:00:00 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+
			" транскрипт "+standTranscript(e.home, sid)+" источник заказ повод startup tmux -\n",
		// go test с фикстурами: журнала у него свой нет, и запись легла в тот же
		// реестр под ID живой сессии стенда.
		alienBind("2026-08-20T11:00:00", sid, "XR-999", e.proj, "-"))

	var got chatEntry
	for _, ch := range chatsOf(t, e, c) {
		if ch.ID == sid {
			got = ch
		}
	}
	if got.ID == "" {
		t.Fatal("живой разговор пропал из списка")
	}
	if hasTask(got.Tasks, "XR-999") {
		t.Errorf("задача фикстуры встала чипом разговора: %+v", got.Tasks)
	}
	if !hasTask(got.Tasks, "XR-4") {
		t.Errorf("своя задача разговора потерялась: %+v", got.Tasks)
	}
}

// Задача чужой доски чипом на строке не встаёт. Прогон go test зовёт утилиты
// доски с ID фикстур, зовёт их живая сессия, и запись «работа» ложится в общий
// реестр под её ID. Транскрипта у такой записи нет вовсе, и отличает её префикс.
func TestChatListChipsKeepOwnBoard(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", "exit 1")
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home,
		"2026-08-20T10:00:00 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+
			" транскрипт "+standTranscript(e.home, sid)+" источник заказ повод startup tmux -\n",
		"2026-08-20T11:00:00 сессия "+sid+" задача ZZ-001 проект - дерево "+e.proj+
			"/tools/shipctl транскрипт - источник работа повод «shipctl start» tmux -\n")

	var got chatEntry
	for _, ch := range chatsOf(t, e, c) {
		if ch.ID == sid {
			got = ch
		}
	}
	if got.ID == "" {
		t.Fatal("живой разговор пропал из списка")
	}
	if hasTask(got.Tasks, "ZZ-001") {
		t.Errorf("задача чужой доски встала чипом разговора: %+v", got.Tasks)
	}
	if !hasTask(got.Tasks, "XR-4") {
		t.Errorf("своя задача разговора потерялась: %+v", got.Tasks)
	}
}

// Запись без транскрипта остаётся своей: строку по факту работы кладёт утилита
// доски, которой про транскрипт не известно ничего, и рубеж её ронять не вправе.
func TestBindOwnKeepsRecordWithoutTranscript(t *testing.T) {
	roots := []string{"/home/r/.claude/projects"}
	if !bindOwn(sessionBind{Task: "XR-4"}, roots) {
		t.Error("запись без транскрипта отсеяна")
	}
	if !bindOwn(sessionBind{Transcript: "/home/r/.claude/projects/-p/aaa.jsonl"}, roots) {
		t.Error("своя запись отсеяна")
	}
	if bindOwn(sessionBind{Transcript: "/tmp/t.jsonl"}, roots) {
		t.Error("запись с транскриптом на стороне зачтена")
	}
	// Сосед по имени каталога это не корень: рубеж сверяет путь по границе
	// каталога, а не по началу строки.
	if bindOwn(sessionBind{Transcript: "/home/r/.claude/projects-old/-p/aaa.jsonl"}, roots) {
		t.Error("каталог-сосед сошёл за корень")
	}
}

package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Привязка строки, возвращённая ответом человека (DK-1191). Цепочка тут та же,
// что на DK-920: сессия задачи запарковала строку вопросом, привязку сняла
// парковка (DK-716), окно закрылось, а ответ человека вернул строку в работу от
// имени дашборда. Записи о работе за этим ходом не оставалось ни одной, и
// поднятый резюмом разговор жил свободным чатом: задачи у него не было ни в
// чипе панели, ни в имени окна, а строка доски стояла без «Стопа».

// parkedAskedChat кладёт эту цепочку до ответа: кончившийся разговор задачи,
// снятая парковкой привязка и признак ожидания, который называет этот же
// разговор.
func parkedAskedChat(t *testing.T, e *testEnv, sid string) {
	t.Helper()
	old := time.Now().Add(-30 * time.Hour)
	writeSession(t, e.home, e.proj, "", sid, saidLine("вопрос задан вчера", old), old)
	writeBinds(t, e.home,
		"2026-09-01T10:00:00 сессия "+sid+" задача XR-7 проект demo дерево "+e.proj+
			" транскрипт "+standTranscript(e.home, sid)+
			" источник заказ повод startup tmux task-XR-7\n",
		"2026-09-01T10:30:00 сессия "+sid+" задача XR-7 источник снята повод taskctl move\n")
	if err := chat.WriteAsk(e.proj, chat.TaskName("XR-7"), chat.Ask{Task: "XR-7", Session: sid,
		Questions: []chat.Question{{Text: "какую схему брать"}}}); err != nil {
		t.Fatal(err)
	}
}

// Ответ человека возвращает строку в In progress, и за ждущей сессией остаётся
// запись «работа»: по ней строка доски видит её ведущей. Прежде этого следа не
// ставил никто. Строку двигает дашборд от своего имени, а отказавшая команда
// реестр не трогает (DK-1003), поэтому повторный move той же сессией записи не
// даёт и подавно.
func TestAnswerReturnsWorkToWaitingSession(t *testing.T) {
	e, c := parkedEnv(t)
	sid := "cccc3333-4444-4444-8444-555555555555"
	parkedAskedChat(t, e, sid)

	resp := postTaskMessage(t, c, e, "XR-7", "бери схему из LLD")
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ задаче не прошёл: %d %s", resp.StatusCode, got)
	}
	binds := readFile(t, filepath.Join(e.home, ".devkit", "sessions.log"))
	want := "сессия " + sid + " задача XR-7"
	if !strings.Contains(binds, want) || !strings.Contains(binds, "источник работа") {
		t.Fatalf("записи о работе ждущей сессии в реестре нет: %s", binds)
	}
	if !strings.Contains(binds, "повод возврат в работу ответом человека") {
		t.Errorf("запись о работе легла без своего повода: %s", binds)
	}
	if !sessionsWorkOn(t, e, sid, "XR-7") {
		t.Errorf("строка не видит ждущую сессию ведущей: %s", binds)
	}
}

// Ответ, не вернувший строку в работу, работы за сессией не оставляет: строка
// стоит припаркованной тем же вопросом, подъём повторит тик сторожка, и запись
// о работе тут обещала бы идущую правку там, где её нет.
func TestAnswerKeepsWorkOffWhenUnparkFailed(t *testing.T) {
	e, c := parkedEnv(t)
	sid := "dddd4444-5555-4555-8555-666666666666"
	parkedAskedChat(t, e, sid)
	writeScript(t, e.bin, "taskctl", `case "$*" in
*"move XR-7 in-progress"*) echo "git push: конфликт ветки" >&2; exit 1;;
*) echo '`+chatParkedBoard+`';;
esac`)

	resp := postTaskMessage(t, c, e, "XR-7", "бери схему из LLD")
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ задаче не прошёл: %d %s", resp.StatusCode, got)
	}
	binds := readFile(t, filepath.Join(e.home, ".devkit", "sessions.log"))
	if strings.Contains(binds, "источник работа") {
		t.Errorf("работа записана за сессией при непройденном возврате строки: %s", binds)
	}
}

// Реплика в тот же разговор после ответа поднимает его резюмом, и задача едет с
// подъёмом: запись реестра несёт XR-7, окно tmux зовётся её именем, а хук старта
// получает её в окружении. Прежде задачу подъём брал из тронутых записей, там
// стояла снятая привязка, и разговор поднимался окном chat-<n> с пустой задачей.
func TestChatResumeAfterAnswerCarriesTask(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)
	c := e.loggedClient(t)
	sid := "eeee5555-6666-4666-8666-777777777777"
	parkedAskedChat(t, e, sid)

	resp := postTaskMessage(t, c, e, "XR-7", "бери схему из LLD")
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ задаче не прошёл: %d %s", resp.StatusCode, got)
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай по схеме"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в разговор задачи не прошла: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `"way":"resume"`) {
		t.Fatalf("дорога реплики не резюм: %s", got)
	}
	said := readFile(t, tmuxLog)
	if !strings.Contains(said, "new-session -d -s chat-XR-7-1 ") {
		t.Errorf("окно продолжения поднято не по задаче: %s", said)
	}
	if !strings.Contains(said, "DEVKIT_TASK='XR-7'") {
		t.Errorf("подъём резюмом не назвал задачу в окружении: %s", said)
	}
	binds := readFile(t, filepath.Join(e.home, ".devkit", "sessions.log"))
	if !strings.Contains(binds, "задача XR-7 проект demo дерево "+e.proj) ||
		!strings.Contains(binds, "повод резюм чата tmux chat-XR-7-1") {
		t.Errorf("запись подъёма резюмом легла без задачи: %s", binds)
	}
}

// Вторая дорога ответа это клавиши в живое окно (DK-715): текст доставляет
// терминал, а признак и парковку сводит settleAsk. Признак там снимается первым
// же ходом, и ждущую сессию он называет сам, иначе возврат работы читал бы уже
// удалённый файл, а строка после ответа стояла бы без «Стопа».
func TestKeysAnswerReturnsWorkToLiveSession(t *testing.T) {
	e, tmuxLog := parkedRaiseEnv(t)
	c := e.loggedClient(t)
	sid := "ffff6666-7777-4777-8777-888888888888"
	parkedAskedChat(t, e, sid)
	// Окно сессии живо, и реестр называет хозяином её же: реплика поедет
	// клавишами, а не резюмом.
	writeTmuxFake(t, e.bin, tmuxLog, `task-XR-7\nчужая-сессия\n`)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "бери схему из LLD"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("клавишный ответ не прошёл: %d %s", resp.StatusCode, got)
	}
	if said := readFile(t, tmuxLog); !strings.Contains(said, "send-keys") {
		t.Fatalf("реплика уехала не клавишами: %s", said)
	}
	binds := readFile(t, filepath.Join(e.home, ".devkit", "sessions.log"))
	if !strings.Contains(binds, "сессия "+sid+" задача XR-7") ||
		!strings.Contains(binds, "источник работа") {
		t.Fatalf("работа за живой сессией после клавишного ответа не вернулась: %s", binds)
	}
	if !sessionsWorkOn(t, e, sid, "XR-7") {
		t.Errorf("строка не видит живую сессию ведущей: %s", binds)
	}
}

// sessionsWorkOn отвечает, ведёт ли сессия работу по строке по тому же
// критерию, каким её считает свёртка строки доски.
func sessionsWorkOn(t *testing.T, e *testEnv, sid, task string) bool {
	t.Helper()
	return leadsTask(e.s.bindsWork()[sid], "", task)
}

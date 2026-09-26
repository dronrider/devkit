package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Подъём головы по лежащей реплике (DK-1194). Реплика человека в чат задачи
// ложилась во вход и лежала там без адресата и без срока: подъём звался только у
// строки, припаркованной вопросом, а ведущей сессией считалось всякое касание
// реестра. Живой случай стоил двадцати минут молчания на строке в Check.

// replyBoard это доска с проверенной строкой под приёмкой человека. Ни обход
// ждущих, ни подъём сирот такую не берут: её ждёт человек, и голова на ней
// раньше вставала стопом приёмки.
const replyBoard = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"check","title":"Check","rows":[` +
	`{"id":"XR-9","title":"Проверенная","accept":"mixed","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]}]}`

// replyEnv это стенд с разложенной дорогой подъёма и доской replyBoard.
// Возврат это стенд, клиент и журнал tmux.
func replyEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", replyBoard))
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, `чужая-сессия\n`)
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
	writeTaskRunFake(t, filepath.Dir(e.proj))
	writePermsFake(t, filepath.Dir(e.proj), false)
	useHeadHome(t, e)
	return e, e.loggedClient(t), tmuxLog
}

// Реплика в проверенную строку поднимает её голову. Прежде подъём звался только
// у припаркованной вопросом строки, и чат такой задачи был мёртв по конструкции:
// ни панель, ни тик, ни соседние сессии реплику не двигали.
func TestTaskMessageRaisesCheckRowUnderUserAccept(t *testing.T) {
	e, c, tmuxLog := replyEnv(t)

	resp := postTaskMessage(t, c, e, "XR-9", "поясни, чем кончилась проверка")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "new-session -d -s task-XR-9") {
		t.Fatalf("реплика не подняла голову задачи: %q", got)
	}
	if !strings.Contains(text, `"raised":true`) {
		t.Errorf("ответ не сказал человеку про поднятую голову: %s", text)
	}
	if strings.Contains(text, `"undelivered":true`) {
		t.Errorf("реплика с поднятой головой названа недоставленной: %s", text)
	}
	// Доску подъём не правит: строка так и ждёт приёмки человеком, а ход только
	// отвечает в ленту.
	if got := readFile(t, tmuxLog); strings.Contains(got, "move") {
		t.Errorf("подъём по реплике двигал строку: %q", got)
	}
}

// Касание реестра соседней сессией на адресата не тянет. Ровно тут и стояла
// поломка: строку слила соседняя сессия, её касание осталось в реестре, и
// реплика считалась доставленной той, что задачу больше не ведёт.
func TestTaskMessageRaisesDespiteNeighbourTouch(t *testing.T) {
	e, c, tmuxLog := replyEnv(t)
	now := time.Now()
	// Соседняя сессия жива, XR-9 она когда-то двигала, а ведёт уже XR-4:
	// последняя запись реестра называет её задачу, и безадресную строку чата
	// XR-9 она не возьмёт (owns_chat в hooks/chat-in.py).
	writeSession(t, e.home, e.proj, "", "aaaa-1111", plainTalk, now)
	writeBinds(t, e.home,
		bindRecord(e.home, now.Add(-time.Hour).Format(bindStamp), "aaaa-1111", "XR-9", "работа"),
		bindRecord(e.home, now.Add(-time.Minute).Format(bindStamp), "aaaa-1111", "XR-4", "работа"))

	resp := postTaskMessage(t, c, e, "XR-9", "что со строкой")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "new-session -d -s task-XR-9") {
		t.Fatalf("касание соседней сессии сошло за ведущую, голова не поднята: %q", got)
	}
}

// Ведущую сессию подъём не трогает: реплику она прочитает подхватом сама, и
// вторая голова по задаче встала бы вторым собеседником в том же чате.
func TestTaskMessageKeepsQuietUnderLeadSession(t *testing.T) {
	e, c, tmuxLog := replyEnv(t)
	now := time.Now()
	writeSession(t, e.home, e.proj, "", "bbbb-2222", plainTalk, now)
	writeBinds(t, e.home, bindRecord(e.home, now.Format(bindStamp), "bbbb-2222", "XR-9", "работа"))

	resp := postTaskMessage(t, c, e, "XR-9", "что со строкой")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session -d -s task-XR-9") {
		t.Fatalf("голова поднята поверх ведущей сессии: %q", got)
	}
	if strings.Contains(text, `"undelivered":true`) {
		t.Errorf("реплика ведущей сессии названа недоставленной: %s", text)
	}
}

// Отказ лестницы человек видит зовом с готовой командой, а панель держит
// реплику недоставленной с причиной: молчание тут неотличимо от штатного
// ожидания, и ровно им кончился живой случай.
func TestTaskMessageCallsHumanWhenLadderRefuses(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", replyBoard))
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), `чужая-сессия\n`)
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
	writePermsFake(t, filepath.Dir(e.proj), false)
	// Оболочки конвейера в корнях нет, и предполёт отказывает до подъёма: так
	// же он откажет на машине без чекаута devkit.
	calls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), calls)
	useHeadHome(t, e)
	c := e.loggedClient(t)

	resp := postTaskMessage(t, c, e, "XR-9", "что со строкой")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"undelivered":true`) {
		t.Errorf("отказ подъёма не назвал реплику недоставленной: %s", text)
	}
	if !strings.Contains(text, "taskctl run XR-9") {
		t.Errorf("в ответе нет готовой команды подъёма: %s", text)
	}
	said := readFile(t, calls)
	if !strings.Contains(said, "недоставленной") || !strings.Contains(said, "taskctl run XR-9") {
		t.Fatalf("человека не позвали с готовой командой:\n%s", said)
	}
	// Реплика остаётся во входе: забрать её некому, и терять её нельзя.
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-9.in"))
	if !strings.Contains(src, "что со строкой") {
		t.Fatalf("реплика пропала из входа задачи:\n%s", src)
	}
}

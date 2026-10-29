package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/drafttx"
)

// Доставка в мёртвое окно (DK-1399). Отправитель не кладёт строку в доставку
// адресу, который её не заберёт: закрытой задаче и черновику отказ словами,
// строке Backlog без ведущей сессии запись в файл задачи. Лежащие строки
// подметает дозор тем же правилом, и повтор доставки сходится за один проход.

// holdBoard это доска стенда: XR-2 стоит в Backlog без ведущей сессии, XR-4
// начата, XR-7 припаркована вопросом. Строке In progress лежащая реплика это
// механизм пробуждения, и дозор её не трогает.
const holdBoard = `{"prefix":"XR","sections":[` +
	`{"key":"backlog","title":"Backlog","rows":[` +
	`{"id":"XR-2","title":"Непринятая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[` +
	`{"id":"XR-7","title":"Спрашивает","block":"вопрос: какую схему брать","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]}]}`

// holdEnv поднимает стенд дозора: доска holdBoard, tmux без сессий задач (у
// болванки общего стенда в списке стоит task-XR-5, и мёртвое окно выглядело бы
// живым), уведомитель пишет вызовы в журнал, дом головы подменён.
func holdEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", holdBoard))
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	calls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), calls)
	useHeadHome(t, e)
	return e, calls
}

// holdTaskFile кладёт файл задачи с постановкой и возвращает его путь.
func holdTaskFile(t *testing.T, proj, id string) string {
	t.Helper()
	dir := filepath.Join(proj, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte("# "+id+": постановка\n\n## Что происходит\n\nТекст.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// holdDraftFile кладёт файл черновика и возвращает его путь.
func holdDraftFile(t *testing.T, proj, id string) string {
	t.Helper()
	dir := filepath.Join(proj, "docs", "tasks", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte("# "+id+": черновик\n\n## Что происходит\n\nТекст.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// holdPutLine кладёт лежащую строку в разговор задачи основного чекаута: так
// выглядит вход, который доставка отбивала бы вечно.
func holdPutLine(t *testing.T, proj, id string, lines ...string) {
	t.Helper()
	path := filepath.Join(proj, ".devkit", "chat", "task-"+id+".in")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Стенд из заглушек гоняет три адресата (DoD): живое окно, мёртвое окно и
// несуществующая строка доски. Живому окну строка остаётся лежать во входе:
// её заберёт хозяин. Мёртвому окну строки Backlog без ведущей сессии вход не
// разберёт никто, и дозор сводит их записью в файл задачи с причиной.
// Несуществующей строке записываться некуда, и исход остаётся строкой журнала
// со звоном человеку. Припаркованная вопросом строка при этом не трогается:
// её будит сторожок, и лежащий ответ для него рабочий механизм.
func TestChatHoldSweepSortsThreeAddressees(t *testing.T) {
	e, calls := holdEnv(t)
	deadFile := holdTaskFile(t, e.proj, "XR-2")
	// Живое окно: сессия работает в боковом дереве начатой строки, и разговор
	// задачи ведёт её.
	sideTree(t, e.proj, "xr-4")
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-4444", plainTalk, time.Now())

	holdPutLine(t, e.proj, "XR-2", chat.TaskLine(time.Now(), "возьми эту задачу"))
	holdPutLine(t, e.proj, "XR-4", chat.TaskLine(time.Now(), "ответ исполнителю"))
	holdPutLine(t, e.proj, "XR-7", chat.TaskLine(time.Now(), "бери схему из LLD"))
	holdPutLine(t, e.proj, "XR-777", chat.Line(time.Now(), "cccc-1234", "адресованная умершей сессии"))

	e.s.chatHoldSweep()

	// Мёртвое окно Backlog: строка ушла из входа и легла в файл задачи.
	if _, err := os.Stat(filepath.Join(e.proj, ".devkit", "chat", "task-XR-2.in")); !os.IsNotExist(err) {
		t.Fatalf("строка Backlog осталась во входе: %v", err)
	}
	note := readFile(t, deadFile)
	for _, want := range []string{"## Входящие", "отложено из чата задачи", "Backlog", "возьми эту задачу"} {
		if !strings.Contains(note, want) {
			t.Errorf("в файле задачи нет %q:\n%s", want, note)
		}
	}
	// Живое окно: строка ждёт хозяина во входе.
	if src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-4.in")); !strings.Contains(src, "ответ исполнителю") {
		t.Fatalf("дозор тронул вход живого окна:\n%s", src)
	}
	// Припаркованная вопросом: ответ сторожка, вход не тронут.
	if src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in")); !strings.Contains(src, "бери схему из LLD") {
		t.Fatalf("дозор тронул вход припаркованной вопросом строки:\n%s", src)
	}
	// Несуществующая строка: вход опустел, записи нет, случай назван человеку.
	if _, err := os.Stat(filepath.Join(e.proj, ".devkit", "chat", "task-XR-777.in")); !os.IsNotExist(err) {
		t.Fatalf("строка несуществующей задачи осталась во входе: %v", err)
	}
	said := readFile(t, calls)
	for _, want := range []string{"task_nolead", "XR-2", "XR-777"} {
		if !strings.Contains(said, want) {
			t.Errorf("случай %s не назван человеку:\n%s", want, said)
		}
	}

	// Второй проход ничего не добавляет: сходимость не плодит записи и зовы.
	// Зов считается по ключу задачи: одна строка вызова несёт ID дважды, в ключе
	// и в заголовке.
	e.s.chatHoldSweep()
	if got := strings.Count(readFile(t, deadFile), "возьми эту задачу"); got != 1 {
		t.Fatalf("второй проход завёл %d записей реплики, ждали одну", got)
	}
	if got := strings.Count(readFile(t, calls), "--task\tXR-777"); got != 1 {
		t.Fatalf("второй проход позвал человека по несуществующей строке %d раз", got)
	}
	if got := strings.Count(readFile(t, calls), "--task\tXR-2"); got != 1 {
		t.Fatalf("второй проход позвал человека по строке Backlog %d раз", got)
	}
}

// Реплика строки Backlog не поднимает её голову, а откладывается в файл задачи
// с причиной: лежать во входе ей некому, и повтор доставки бился бы об отказ
// вечно (живой случай DK-706, сорок пять дней молчания).
func TestTaskMessageBacklogDeferredToFile(t *testing.T) {
	e, calls := holdEnv(t)
	deadFile := holdTaskFile(t, e.proj, "XR-2")
	c := e.loggedClient(t)

	resp := postTaskMessage(t, c, e, "XR-2", "возьми эту задачу")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"held":true`, "Backlog", "файла задачи"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	// В доставку строка не легла, а в файл легла: отказ повтора и запись
	// исхода в одном шаге.
	if _, err := os.Stat(filepath.Join(e.proj, ".devkit", "chat", "task-XR-2.in")); !os.IsNotExist(err) {
		t.Fatalf("реплика легла в доставку мёртвого окна: %v", err)
	}
	note := readFile(t, deadFile)
	if !strings.Contains(note, "возьми эту задачу") || !strings.Contains(note, "## Входящие") {
		t.Fatalf("реплика не отложилась в файл задачи:\n%s", note)
	}
	// Человека стенд не зовёт: он только что писал реплику и читает ответ
	// ручки, второй баннер об одном молчании хуже одного.
	if said := readFile(t, calls); strings.Contains(said, "XR-2") {
		t.Errorf("ручка позвала человека по своей же реплике:\n%s", said)
	}
}

// Строки Backlog без файла задачи дозор снимает без записи: исход остаётся
// строкой журнала, и человек узнаёт об этом словами ответа, а не молчанием.
func TestTaskMessageBacklogWithoutFileNamedInAnswer(t *testing.T) {
	e, _ := holdEnv(t)
	c := e.loggedClient(t)

	resp := postTaskMessage(t, c, e, "XR-2", "возьми эту задачу")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"held":true`, "Файла задачи нет", "журнала"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(e.proj, ".devkit", "chat", "task-XR-2.in")); !os.IsNotExist(err) {
		t.Fatalf("реплика легла в доставку мёртвого окна: %v", err)
	}
}

// Закрытой задаче и черновику реплика не предлагается вовсе: строки на доске у
// них нет, и отказ приходит отправителю с причиной сразу, до всякой доставки
// (DoD). Закрытая узнаётся по архиву, черновик по своему файлу.
func TestTaskMessageRefusesClosedAndDraft(t *testing.T) {
	e, _ := holdEnv(t)
	holdDraftFile(t, e.proj, "XR-5")
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "TASKS-archive.md"),
		[]byte("| Задача | Название | Тип | P | Закрыта | Файл |\n|-\n"+
			"| XR-8 | Закрытая задача | task | P2 | 07.09 | tasks/XR-8.md |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)

	resp := postTaskMessage(t, c, e, "XR-8", "привет закрытой")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "уже закрыта") {
		t.Errorf("закрытая задача: %d %s", resp.StatusCode, text)
	}
	resp = postTaskMessage(t, c, e, "XR-5", "привет черновику")
	text = body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, `"draft":"XR-5"`) {
		t.Errorf("черновик: %d %s", resp.StatusCode, text)
	}
	for _, id := range []string{"XR-8", "XR-5"} {
		if _, err := os.Stat(filepath.Join(e.proj, ".devkit", "chat", "task-"+id+".in")); !os.IsNotExist(err) {
			t.Errorf("реплика задаче %s легла в доставку: %v", id, err)
		}
	}
}

// Ответ на вопрос умершего разговора черновика не принимается: розданный
// вопрос ждёт сессию, которой нет, и безадресная строка легла бы во вход
// навсегда (живой случай DK-555 и DK-698). Признак ожидания снимается, и
// панель больше не рисует мёртвый вопрос.
func TestAskAnswerRefusedForDraft(t *testing.T) {
	sid := "dddd4444-4444-4444-8444-444444444444"
	e, _ := holdEnv(t)
	holdDraftFile(t, e.proj, "XR-5")
	// Транскрипт ждущего старый: разговор черновика умер задолго до ответа, и
	// человек пишет в его панель уже без живого окна за ней.
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-2*time.Hour))
	if err := chat.WriteAsk(e.proj, "task-XR-5", chat.Ask{Session: sid, Task: "XR-5",
		Questions: []chat.Question{{Text: "механика сигнала"}}}); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("механика сигнала 1", "m-1"))
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{"ответ не принят", "черновика нет строки доски"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-5")); ok {
		t.Fatal("признак ожидания пережил отказ: панель рисовала бы мёртвый вопрос")
	}
	if lines := chatLines(t, e.proj, "task-XR-5"); len(lines) != 0 {
		t.Fatalf("ответ лег в доставку мёртвого окна: %q", lines)
	}
}

// Ответ на вопрос строки Backlog без ведущей сессии откладывается в файл
// задачи: разговор вопроса мёртв, а строка живая, и будущий исполнитель
// прочтёт ответ вместе с постановкой.
func TestAskAnswerDeferredForBacklog(t *testing.T) {
	sid := "dddd4444-4444-4444-8444-444444444444"
	e, _ := holdEnv(t)
	deadFile := holdTaskFile(t, e.proj, "XR-2")
	// Транскрипт ждущего старый: спрашивавший умер, и ответ заберет будущий
	// исполнитель из файла задачи, а не ждущий ход.
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-2*time.Hour))
	if err := chat.WriteAsk(e.proj, "task-XR-2", chat.Ask{Session: sid, Task: "XR-2",
		Questions: []chat.Question{{Text: "брать схему"}}}); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("бери из LLD", "m-2"))
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"held":true`, "Backlog", "Входящие"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-2")); ok {
		t.Fatal("признак ожидания пережил откладывание: панель рисовала бы отвеченный вопрос")
	}
	if lines := chatLines(t, e.proj, "task-XR-2"); len(lines) != 0 {
		t.Fatalf("ответ лег в доставку мёртвого окна: %q", lines)
	}
	if note := readFile(t, deadFile); !strings.Contains(note, "бери из LLD") {
		t.Fatalf("ответ не отложился в файл задачи:\n%s", note)
	}
}

// Раскладку «Входящих» держит один писатель (DK-1399, замечание ревью):
// holdInbox зовёт addInboxLine цели, и раздел в файле задачи встаёт перед
// «Журналом», а не второй раскладкой в конец файла. Двумя писателями раздел
// одной задачи вставал в два разных места смотря кто из них прошёл первым.
func TestHoldInboxSharesGoalSectionPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "XR-2.md")
	if err := os.WriteFile(path, []byte("# XR-2: постановка\n\n## Что происходит\n\nТекст.\n\n## Журнал\n\n- 2026-10-17 ход\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := chat.Line(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), "s1", "возьми задачу")
	if err := holdInbox(path, []string{line}, "строка в Backlog без ведущей сессии",
		time.Date(2026, 10, 17, 12, 5, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	note := readFile(t, path)
	if strings.Index(note, "## Входящие") > strings.Index(note, "## Журнал") {
		t.Fatalf("раздел «Входящие» встал не перед «Журналом»:\n%s", note)
	}
	if !strings.Contains(note, "возьми задачу") {
		t.Fatalf("отложенная реплика не легла в раздел:\n%s", note)
	}
}

// Вопрос живой строки разговора принимает ответ по-прежнему: сессия стоит на
// вопросе начатой задачи без терминала, и ответ едет во вход с адресатом,
// где его заберёт её следующий ход. Ворота мёртвого окна живых не трогают.
func TestAskAnswerStaysForLiveRow(t *testing.T) {
	sid := "dddd4444-4444-4444-8444-444444444444"
	e, _ := holdEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo "+
		"дерево %s транскрипт "+standTranscript(e.home, "t")+" источник заказ повод startup tmux -\n", sid, e.proj))
	if err := chat.WriteAsk(e.proj, "task-XR-4", chat.Ask{Session: sid, Task: "XR-4",
		Questions: []chat.Question{{Text: "режем строку"}}}); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("режем по шву", "m-3"))
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(text, "не принят") {
		t.Fatalf("ответ живому разговору отбит: %s", text)
	}
	lines := chatLines(t, e.proj, "task-XR-4")
	if len(lines) != 1 || chat.Said(lines[0]) != "режем по шву" {
		t.Fatalf("во входе разговора не тот ответ: %q", lines)
	}
}

func TestChatHoldSelectsFileUnderDraftLock(t *testing.T) {
	e, _ := holdEnv(t)
	lock, err := drafttx.Acquire(e.proj)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		filed, ok := e.s.chatHoldWrite(&Project{Path: e.proj, Name: "test"}, "XR-2", []string{chat.TaskLine(time.Now(), "текст")}, "окно закрыто")
		done <- filed && ok
	}()
	select {
	case <-done:
		lock.Close()
		t.Fatal("writer passed lock")
	case <-time.After(100 * time.Millisecond):
	}
	path := holdTaskFile(t, e.proj, "XR-2")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("new file was not selected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer blocked")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "текст") {
		t.Fatalf("file: %s %v", data, err)
	}
}

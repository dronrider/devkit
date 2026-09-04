package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Синтетический транскрипт: служебные записи, реплика человека строкой,
// размышления, текст с вызовом инструмента, tool_result, битая строка,
// ветка субагента. Ожидания ленты выводятся из него поле в поле.
const transcriptFixture = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-10T10:00:00.000Z"}
{"type":"user","message":{"role":"user","content":"возьми задачу XR-005 в работу"},"timestamp":"2026-08-10T10:00:01.000Z","gitBranch":"main"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"куда смотреть"}]},"timestamp":"2026-08-10T10:00:02.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Беру XR-005, смотрю доску."},{"type":"tool_use","name":"Bash","input":{"command":"taskctl list | head -5","description":"Показать доску"}}]},"timestamp":"2026-08-10T10:00:03.000Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]},"timestamp":"2026-08-10T10:00:04.000Z"}
битая строка не json
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"реплика субагента"}]},"timestamp":"2026-08-10T10:00:05.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Доска прочитана."}]},"timestamp":"2026-08-10T10:00:06.000Z"}
`

// Ожидания ленты по POC ветки poc-chat: текст размышлений едет в ленту
// (прежде сервер его выбрасывал), ответ инструмента стоит своей записью, а
// вызов несёт пояснение хода отдельным полем.
var transcriptWant = []reply{
	{Seq: 0, Key: fixKey(1, 0), Role: "user", Time: "2026-08-10T10:00:01.000Z", Text: "возьми задачу XR-005 в работу"},
	{Seq: 1, Key: fixKey(2, 0), Role: "thinking", Time: "2026-08-10T10:00:02.000Z", Text: "куда смотреть", Spent: 1000},
	// Ответ агента цитирует реплику, на которую отвечает: пару считает разбор,
	// и в ленту она едет полями цитаты.
	{Seq: 2, Key: fixKey(3, 0), Role: "assistant", Time: "2026-08-10T10:00:03.000Z", Text: "Беру XR-005, смотрю доску.",
		Quote: "возьми задачу XR-005 в работу", QuoteKey: fixKey(1, 0)},
	{Seq: 3, Key: fixKey(3, 1), Role: "tool", Time: "2026-08-10T10:00:03.000Z", Tool: "Bash",
		Note: "taskctl list | head -5", About: "Показать доску",
		Text: "command: taskctl list | head -5\ndescription: Показать доску",
		Args: map[string]string{"command": "taskctl list | head -5", "description": "Показать доску"}},
	{Seq: 4, Key: fixKey(4, 0), Role: roleToolOut, Time: "2026-08-10T10:00:04.000Z", Text: "ok"},
	{Seq: 5, Key: fixKey(7, 0), Role: "assistant", Time: "2026-08-10T10:00:06.000Z", Text: "Доска прочитана."},
}

// fixKey это ключ записи фикстуры: смещение её строки в транскрипте и номер
// блока внутри строки. Ключ считается от смещения, а не от номера записи,
// потому что лента читается хвостом и номера записи не знает (feed.go), а
// выписывать смещения руками значит переписывать их на каждую правку фикстуры.
func fixKey(line, blk int) string {
	off := 0
	for i, ln := range strings.Split(transcriptFixture, "\n") {
		if i == line {
			break
		}
		off += len(ln) + 1
	}
	return fmt.Sprintf("m:%d.%d", off, blk)
}

// writeSession кладёт транскрипт в каталог ~/.claude/projects по раскладке
// Claude Code; dirSuffix изображает каталог бокового дерева задачи.
func writeSession(t *testing.T, home, projPath, dirSuffix, sid, content string, mtime time.Time) string {
	t.Helper()
	return writeSessionAt(t, filepath.Join(home, ".claude", "projects"), projPath, dirSuffix, sid, content, mtime)
}

// writeSessionAt кладёт транскрипт в названный корень журналов: у второй
// подписки это projects её каталога конфигурации, а не ~/.claude (DK-362).
func writeSessionAt(t *testing.T, root, projPath, dirSuffix, sid, content string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, claudeDirName(projPath)+dirSuffix)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

// Путь проекта кодируется в имя каталога как у Claude Code: всё, что не
// буква, не цифра и не дефис, становится дефисом.
func TestClaudeDirName(t *testing.T) {
	got := claudeDirName("/Users/x/my.proj_v2")
	if got != "-Users-x-my-proj-v2" {
		t.Fatalf("кодировка пути: %q", got)
	}
}

// Список сессий: свежие сверху по mtime, ветка, боковое дерево и первая
// человеческая реплика из шапки, служебная вставка в угловых скобках репликой
// не считается, сессия из каталога бокового дерева попадает в список.
func TestSessionsList(t *testing.T) {
	e := newTestEnv(t)
	older := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, older)
	tagged := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<ide_opened_file>шум</ide_opened_file>"},{"type":"text","text":"Выполни XR-007"}]},"timestamp":"2026-08-10T11:00:00.000Z","gitBranch":"dk-219"}` + "\n"
	writeSession(t, e.home, e.proj, "-dk-5", "bbb-2", tagged, newer)

	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions", "")
	var got struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	// Ручка реплики и свежесть едут в списке вместе с привязкой: по ним список
	// чатов панели говорит состояние словом (DK-436). Дерева dk-5 рядом с
	// проектом нет, и чат из него честно назван кончившимся. У сессии главного
	// дерева задачи нет вовсе: ID, прозвучавший в первой реплике, сессию больше
	// не привязывает, и подпись у неё честная.
	want := []sessionInfo{
		{ID: "bbb-2", Mtime: "2026-08-10T12:00:00Z", Branch: "dk-219", Tree: "dk-5",
			First: "Выполни XR-007", Task: "DK-5", TaskNote: "по дереву задачи", Bound: boundLead,
			ReplyNote: "дерева сессии больше нет: чат кончился, и продолжить его некому"},
		{ID: "aaa-1", Mtime: "2026-08-10T09:00:00Z", Branch: "main", First: "возьми задачу XR-005 в работу",
			TaskNote: unknownTaskNote, Reply: replyToSession},
	}
	if !reflect.DeepEqual(got.Sessions, want) {
		t.Errorf("список сессий:\n%+v\nожидал:\n%+v", got.Sessions, want)
	}
}

// Список разговоров проекта без задачи (?free=1): им живёт общий чат доски, и
// разговор с узнанной задачей туда не идёт. Пустота названа словами, как и у
// списка задачи, а вместе с ?task= ключ отбивается: спрашивают они разное.
func TestSessionsFreeOnly(t *testing.T) {
	e := newTestEnv(t)
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("возьми задачу XR-101 в работу", "main"), base)
	writeSession(t, e.home, e.proj, "", "bbb-2",
		sessionLine("почини роутер, доступы в local-docs", "main"), base.Add(time.Hour))
	// Задачу первой сессии называет запись реестра: ID из первой реплики
	// привязкой больше не считается, и без записи оба чата уехали бы в общий
	// список доски.
	writeBinds(t, e.home, bindRecord("2026-08-10T09:00:00", "aaa-1", "XR-101", bindHand))
	c := e.loggedClient(t)

	_, list, _ := getSessions(t, e, c, "?free=1")
	if len(list) != 1 || list[0].ID != "bbb-2" {
		t.Fatalf("разговоры доски: %+v", list)
	}
	if list[0].Task != "" || list[0].Bound != "" {
		t.Errorf("в разговоры доски попала сессия с задачей: %+v", list[0])
	}

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions?free=1&task=XR-101", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "вместе не читаются") {
		t.Errorf("free и task вместе не отбиты: %d %s", resp.StatusCode, text)
	}
}

// listedBind это строка реестра про сессию, поднятую дашбордом: источник
// «заказ» и имя tmux-сессии, по которому меряется, жив ли разговор. Только у
// такой записи мера доходит до tmux, и без неё список разговоров спрашивает его
// ноль раз (замечание ревью DK-436).
func listedBind(sid, task, tmux string) string {
	return "2026-08-18T12:03:11 сессия " + sid + " задача " + task + " проект demo " +
		"дерево /tmp транскрипт /tmp/t.jsonl источник заказ повод startup tmux " + tmux + "\n"
}

// Список разговоров спрашивает tmux один раз на заход, сколько бы разговоров в
// нём ни стояло: мера живости у списка общая (tmuxAliveFn), а не своя на каждую
// строку. Без кеша десяток разговоров задачи стоил бы десятка подпроцессов на
// каждое открытие панели, и это стало бы заметно только на живой машине.
func TestSessionsListAsksTmuxOnce(t *testing.T) {
	e := newTestEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "task-XR-5\n")
	sideTree(t, e.proj, "xr-5")
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	for i, sid := range []string{"aaa-1", "bbb-2", "ccc-3"} {
		writeSession(t, e.home, e.proj, "-xr-5", sid,
			sessionLine("поговорим про XR-005", "main"), base.Add(time.Duration(i)*time.Hour))
	}
	writeBinds(t, e.home,
		listedBind("aaa-1", "XR-005", "chat-XR-005-1"),
		listedBind("bbb-2", "XR-005", "chat-XR-005-2"),
		listedBind("ccc-3", "XR-005", "task-XR-5"))
	c := e.loggedClient(t)

	_, list, note := getSessions(t, e, c, "?task=XR-005")
	if len(list) != 3 {
		t.Fatalf("сессии задачи XR-005: %+v, приписка: %s", list, note)
	}
	asks := 0
	for _, ln := range strings.Split(readFile(t, tmuxLog), "\n") {
		if strings.HasPrefix(ln, "ls") {
			asks++
		}
	}
	if asks != 1 {
		t.Errorf("список из трёх разговоров спросил tmux %d раз, ждал один: мера живости считается на каждую строку", asks)
	}
	// Мера при этом настоящая: разговор с живым именем пишет своей ручкой, а
	// два со снятыми названы кончившимися. Иначе один вызов был бы куплен
	// потерей самой меры.
	state := map[string]string{}
	for _, s := range list {
		state[s.ID] = s.Reply
	}
	if state["ccc-3"] != replyToSession {
		t.Errorf("живая tmux-сессия разговора посчитана мёртвой: %+v", list)
	}
	for _, sid := range []string{"aaa-1", "bbb-2"} {
		if state[sid] != replyToTask {
			t.Errorf("разговор %s со снятой tmux-сессией не отдал реплику задаче: %+v", sid, list)
		}
	}
}

// На машине без tmux мера живости вовсе не работает: имена там считаются живыми, и
// список разговоров не хоронит их разом. Обратное поведение погасило бы ввод у
// всех разговоров сразу, не имея на то ни одного признака.
func TestSessionsListWithoutTmuxKeepsTalksAlive(t *testing.T) {
	e := newTestEnv(t)
	sideTree(t, e.proj, "xr-5")
	writeSession(t, e.home, e.proj, "-xr-5", "aaa-1",
		sessionLine("поговорим про XR-005", "main"), time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC))
	writeBinds(t, e.home, listedBind("aaa-1", "XR-005", "chat-XR-005-1"))
	c := e.loggedClient(t)
	// PATH без tmux, но с доской: спросить про имя нечем, а всё остальное
	// работает как раньше.
	bare := t.TempDir()
	writeScript(t, bare, "taskctl", fmt.Sprintf("echo '%s'", boardFixtureJSON))
	t.Setenv("PATH", bare)

	_, list, note := getSessions(t, e, c, "?task=XR-005")
	if len(list) != 1 {
		t.Fatalf("сессии задачи XR-005: %+v, приписка: %s", list, note)
	}
	if list[0].Reply != replyToSession || list[0].ReplyNote != "" {
		t.Errorf("без tmux разговор объявлен кончившимся: %+v", list[0])
	}
}

// Пустой список разговоров доски называет причину: у всех транскриптов проекта
// нашлась своя задача, и это не то же самое, что «транскриптов нет».
func TestSessionsFreeNoneNamed(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("возьми задачу XR-101 в работу", "main"),
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC))
	writeBinds(t, e.home, bindRecord("2026-08-10T09:00:00", "aaa-1", "XR-101", bindHand))
	c := e.loggedClient(t)

	_, list, note := getSessions(t, e, c, "?free=1")
	if len(list) != 0 {
		t.Fatalf("разговоры доски: %+v", list)
	}
	if !strings.Contains(note, "чатов без задачи") {
		t.Errorf("пустой список разговоров доски молчит о причине: %q", note)
	}
}

// Список разговоров задачи говорит то, чем список панели их различает:
// свежесть транскрипта, подписку и ручку реплики. Без подписки два разговора
// одной задачи на разных подписках отличались бы только временем, а без
// свежести идущий разговор был бы неотличим от вчерашнего.
func TestSessionsListStateFields(t *testing.T) {
	e := newTestEnv(t)
	home := filepath.Join(e.home, ".devkit", "claude-second")
	writeAgentctlFake(t, e.bin, fmt.Sprintf(harnessJSONHomeFixture, home))
	writeSessionAt(t, filepath.Join(home, "projects"), e.proj, "", "hls-1",
		headlessLine("Выполни XR-101", "main"), time.Now())
	writeSession(t, e.home, e.proj, "", "old-1",
		sessionLine("возьми задачу XR-101 в работу", "main"),
		time.Now().Add(-2*time.Hour))
	// Задачу обеим сессиям называет реестр: свежей заказом с доски, старой
	// рукой человека.
	writeBinds(t, e.home,
		bindRecord("2026-08-10T09:00:00", "hls-1", "XR-101", bindOrder),
		bindRecord("2026-08-10T09:00:00", "old-1", "XR-101", bindHand))
	c := e.loggedClient(t)

	_, list, note := getSessions(t, e, c, "?task=XR-101")
	if len(list) != 2 {
		t.Fatalf("сессии задачи XR-101: %+v, приписка: %s", list, note)
	}
	fresh, stale := list[0], list[1]
	if fresh.ID != "hls-1" || stale.ID != "old-1" {
		t.Fatalf("порядок списка не свежими сверху: %+v", list)
	}
	if fresh.Harness != "втораяtest" {
		t.Errorf("разговор второй подписки не называет её: %+v", fresh)
	}
	if stale.Harness != "" {
		t.Errorf("разговору своего хозяйства приписана подписка: %+v", stale)
	}
	if !fresh.Live || stale.Live {
		t.Errorf("свежесть транскриптов перепутана: свежий %v, двухчасовой %v", fresh.Live, stale.Live)
	}
	for _, s := range list {
		if s.Reply != replyToSession {
			t.Errorf("живому разговору %s не названа ручка реплики: %+v", s.ID, s)
		}
	}
}

// Пустота различима: без единого транскрипта список пуст и причина названа
// словами, а не пустым экраном.
func TestSessionsNoneNamed(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("сессии без каталога: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"sessions":[]`, "транскриптов нет"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
}

// sessionLine это одна строка транскрипта с репликой человека: задача
// узнаётся по ней, поэтому текст и ветка задаются вызовом.
func sessionLine(text, branch string) string {
	return fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":%q},"timestamp":"2026-08-10T10:00:01.000Z","gitBranch":%q}`,
		text, branch) + "\n"
}

// getSessions ходит за списком сессий и разбирает ответ.
func getSessions(t *testing.T, e *testEnv, c *http.Client, query string) (string, []sessionInfo, string) {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions"+query, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список сессий%s: %d %s", query, resp.StatusCode, text)
	}
	var got struct {
		Sessions []sessionInfo `json:"sessions"`
		Note     string        `json:"note"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	return text, got.Sessions, got.Note
}

// Экран задачи отдаёт только свою сессию: соседнее окно того же проекта
// делает другую задачу и пишет свежее, и до DK-252 экран брал по mtime именно
// его. Задача узнаётся записью реестра в главном дереве и именем бокового
// дерева.
func TestSessionsByTaskOnlyOwn(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("возьми задачу XR-101 в работу и доведи её конвейером", "main"),
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC))
	writeSession(t, e.home, e.proj, "", "bbb-2",
		sessionLine("Выполни цель XR-102", "main"),
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	writeSession(t, e.home, e.proj, "-xr-103", "ccc-3",
		sessionLine("продолжай, я подожду", "xr-103"),
		time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC))
	writeBinds(t, e.home,
		bindRecord("2026-08-10T09:00:00", "aaa-1", "XR-101", bindHand),
		bindRecord("2026-08-10T12:00:00", "bbb-2", "XR-102", bindHand))
	c := e.loggedClient(t)

	text, list, _ := getSessions(t, e, c, "?task=XR-101")
	want := []sessionInfo{{ID: "aaa-1", Mtime: "2026-08-10T09:00:00Z", Branch: "main",
		First: "возьми задачу XR-101 в работу и доведи её конвейером",
		Task:  "XR-101", TaskNote: handNote, Bound: boundLead, Reply: replyToSession}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("сессии задачи XR-101:\n%+v\nожидал:\n%+v", list, want)
	}
	for _, alien := range []string{"bbb-2", "ccc-3"} {
		if strings.Contains(text, alien) {
			t.Errorf("чужая сессия %s приписана задаче XR-101: %s", alien, text)
		}
	}
	if _, list, _ = getSessions(t, e, c, "?task=XR-102"); len(list) != 1 || list[0].ID != "bbb-2" {
		t.Errorf("сессии задачи XR-102: %+v", list)
	}
	_, list, _ = getSessions(t, e, c, "?task=XR-103")
	if len(list) != 1 || list[0].ID != "ccc-3" || list[0].TaskNote != "по дереву задачи" {
		t.Errorf("сессия бокового дерева XR-103: %+v", list)
	}
}

// Разговоры одной задачи различимы и тогда, когда идут по одной ветке: разбор
// ведут в главном дереве, исполнение в боковом, ветка у обоих остаётся своей
// прежней, и подписи в списке выходили одинаковыми. Дерево называет сервер, у
// главного оно пусто (DK-290).
func TestSessionsNameTheirTree(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("разбери задачу XR-101", "main"),
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC))
	writeSession(t, e.home, e.proj, "-xr-101", "bbb-2",
		sessionLine("продолжай, я подожду", "main"),
		time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC))
	writeBinds(t, e.home, bindRecord("2026-08-10T09:00:00", "aaa-1", "XR-101", bindHand))
	c := e.loggedClient(t)

	_, list, note := getSessions(t, e, c, "?task=XR-101")
	if len(list) != 2 {
		t.Fatalf("разговоры задачи XR-101: %+v, %q", list, note)
	}
	tree := map[string]string{}
	for _, s := range list {
		tree[s.ID] = s.Tree
	}
	if tree["bbb-2"] != "xr-101" {
		t.Errorf("разговор бокового дерева не назвал дерева: %+v", list)
	}
	if tree["aaa-1"] != "" {
		t.Errorf("разговор главного дерева назвал деревом %q", tree["aaa-1"])
	}
}

// Сессия без узнанной задачи не пропадает: в общем списке она подписана
// словами, а в ленту задачи не идёт. «top-10» задачей не считается: ID пишется
// прописными.
func TestSessionUnknownTaskSigned(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("посмотри top-10 процессов", "main"),
		time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC))
	c := e.loggedClient(t)

	_, list, _ := getSessions(t, e, c, "")
	if len(list) != 1 || list[0].Task != "" || list[0].TaskNote != unknownTaskNote {
		t.Fatalf("подпись нераспознанной сессии: %+v", list)
	}
	text, list, note := getSessions(t, e, c, "?task=XR-101")
	if len(list) != 0 {
		t.Fatalf("нераспознанная сессия попала в ленту задачи: %s", text)
	}
	for _, want := range []string{"сессий задачи XR-101 нет", "1 без распознанной задачи"} {
		if !strings.Contains(note, want) {
			t.Errorf("в словах о пустоте нет %q: %q", want, note)
		}
	}
}

// Пустоты различимы: «сессий этой задачи нет» и «транскриптов нет вовсе» это
// разные слова, иначе по экрану не понять, ищется работа или отсутствует
// вовсе.
func TestSessionsEmptyKindsDiffer(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	_, _, none := getSessions(t, e, c, "?task=XR-101")
	if !strings.Contains(none, "транскриптов нет") {
		t.Errorf("пустой каталог назван не своими словами: %q", none)
	}
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("Выполни цель XR-102", "main"), time.Now())
	writeBinds(t, e.home, bindRecord("2026-08-10T09:00:00", "aaa-1", "XR-102", bindHand))
	_, _, other := getSessions(t, e, c, "?task=XR-101")
	if !strings.Contains(other, "сессий задачи XR-101 нет") || !strings.Contains(other, "1 о других задачах") {
		t.Errorf("чужие сессии названы не своими словами: %q", other)
	}
	if none == other {
		t.Error("обе пустоты сказаны одними словами")
	}
}

// Параметр ?task= это ID задачи, а не любая строка из адреса: мусор отбивается
// словами, а не молчаливым списком всех сессий подряд.
func TestSessionsTaskParamSifted(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("возьми задачу XR-101 в работу", "main"), time.Now())
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions?task=../etc", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "не похоже на ID задачи") {
		t.Fatalf("мусор в ?task=: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(text, "aaa-1") {
		t.Errorf("по мусорному ?task= уехал список сессий: %s", text)
	}
}

// Шапка транскрипта читается не на каждый запрос: пока отпечаток файла тот же,
// ответ идёт из памяти процесса, а сменившийся отпечаток перечитывается. Файл
// подменяется тем же размером и временем, поэтому старый ответ и есть
// доказательство памяти.
func TestSessionHeadCached(t *testing.T) {
	e := newTestEnv(t)
	stamped := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	// Память ловится первой репликой шапки: задачу сессии называет реестр, а не
	// текст, и подменой текста её не сдвинуть. Длина реплики держится прежней,
	// иначе подмена сменила бы и отпечаток файла.
	const said, rewritten = "возьми задачу XR-101 в работу", "возьми задачу XR-102 в работу"
	path := writeSession(t, e.home, e.proj, "", "aaa-1", sessionLine(said, "main"), stamped)
	c := e.loggedClient(t)
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].First != said {
		t.Fatalf("первое чтение шапки: %+v", list)
	}

	same := sessionLine(rewritten, "main")
	if err := os.WriteFile(path, []byte(same), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamped, stamped); err != nil {
		t.Fatal(err)
	}
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].First != said {
		t.Errorf("шапка перечитана при том же отпечатке: %+v", list)
	}

	moved := stamped.Add(time.Minute)
	if err := os.Chtimes(path, moved, moved); err != nil {
		t.Fatal(err)
	}
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].First != rewritten {
		t.Errorf("дописанный транскрипт остался в памяти старым: %+v", list)
	}
}

// bigTranscript это транскрипт длиннее предела головы: заказ работы первой
// строкой, дальше служебные записи набивкой. Дописывание живой сессии
// изображается именно так, поэтому набивка идёт разбираемыми записями, а не
// мусором.
func bigTranscript(order string) string {
	pad := `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-10T10:00:02.000Z"}` + "\n"
	return sessionLine(order, "main") + strings.Repeat(pad, metaScanLimit/len(pad)+2)
}

// Голова, дочитанная до предела, переживает дописывание: jsonl только растёт,
// и перечитывать первую четверть мегабайта на каждый запрос незачем, хотя
// отпечаток файла сменился. Живая сессия пишется в транскрипт постоянно,
// поэтому без этой ветки память процесса на ней не работала бы вовсе.
// Подмена головы вместе с дописыванием и есть доказательство: перечитанный
// файл назвал бы другую задачу.
func TestSessionHeadCachedWhenFull(t *testing.T) {
	e := newTestEnv(t)
	stamped := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	const said = "возьми задачу XR-101 в работу"
	path := writeSession(t, e.home, e.proj, "", "aaa-1", bigTranscript(said), stamped)
	c := e.loggedClient(t)
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].First != said {
		t.Fatalf("первое чтение большой шапки: %+v", list)
	}

	grown := bigTranscript("возьми задачу XR-102 в работу") +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Готово."}]},"timestamp":"2026-08-10T10:00:07.000Z"}` + "\n"
	if err := os.WriteFile(path, []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	moved := stamped.Add(time.Minute)
	if err := os.Chtimes(path, moved, moved); err != nil {
		t.Fatal(err)
	}
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].First != said {
		t.Errorf("дочитанная голова перечитана после дописывания: %+v", list)
	}
}

// Разговор законченной работы находится и тогда, когда лежит глубже окна
// свежих: до DK-280 шапки читались только у headScanMax сессий, и работа,
// после которой в проекте прошёл десяток чужих, пропадала с экрана задачи
// насовсем. Шаг 1 сценария проверки: сессия задачи стоит последней в списке
// свежести.
func TestSessionsFoundBeyondHeadScan(t *testing.T) {
	e := newTestEnv(t)
	old := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	writeSession(t, e.home, e.proj, "", "old-1",
		sessionLine("возьми задачу XR-101 в работу", "main"), old)
	writeBinds(t, e.home, bindRecord("2026-08-01T09:00:00", "old-1", "XR-101", bindHand))
	for i := 0; i < headScanMax+10; i++ {
		writeSession(t, e.home, e.proj, "", fmt.Sprintf("new-%02d", i),
			sessionLine("посмотри логи", "main"), old.Add(time.Duration(i+1)*time.Hour))
	}
	c := e.loggedClient(t)

	_, list, note := getSessions(t, e, c, "?task=XR-101")
	if len(list) != 1 || list[0].ID != "old-1" {
		t.Fatalf("разговор задачи за окном свежих сессий не найден: %+v, %q", list, note)
	}
	if note != "" {
		t.Errorf("при найденном разговоре сказано лишнее: %q", note)
	}
	// Общий список остаётся дешёвым: показывают его десятком строк, и читать
	// ради него шапки у всех транскриптов проекта незачем.
	if _, all, _ := getSessions(t, e, c, ""); len(all) != 20 {
		t.Errorf("общий список сессий: %d строк, ожидал 20", len(all))
	}
	// Ненайденное называется словами и говорит, что искали по всем, а не по
	// свежей полусотне.
	_, list, note = getSessions(t, e, c, "?task=XR-999")
	if len(list) != 0 {
		t.Fatalf("по задаче без сессий приехал список: %+v", list)
	}
	for _, want := range []string{"сессий задачи XR-999 нет",
		fmt.Sprintf("просмотрены все %d транскриптов проекта", headScanMax+11),
		"1 о других задачах", fmt.Sprintf("%d без распознанной задачи", headScanMax+10)} {
		if !strings.Contains(note, want) {
			t.Errorf("в словах о пустоте нет %q: %q", want, note)
		}
	}
}

// Панель разговора: разбор адреса, выбор ручки для реплики, четыре причины
// гашения ввода и память о ширине. Предмет проверки это собранная панель и
// разобранный адрес, а не написанное в исходнике, поэтому статика поднимается в
// node с заглушкой DOM (стенд testdata/chat_panel.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestChatPanelAddressAndWay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд панели разговора пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "chat_panel.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("панель разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Панель разговора живёт своим показом: по умолчанию она колонка экрана и
// сжимает доску рядом, ниже 1100 точек ложится поверх неё, а на узком экране
// занимает его целиком. Предмет проверки тут сами правила: сложение
// медиазапросов проверкой одного узла не берётся, а поломка тут ровно такая,
// как была у переключателя разговоров (DK-280), и ловится тем же разбором
// правил, что спор hidden с display (DK-284).
func TestStaticChatPanelWidths(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	if !strings.Contains(css, ".cpanel{position:relative;flex:none;width:var(--cw,420px)") {
		t.Error("панель не берёт ширину переменной --cw: хват не сдвинет её, а доска рядом не сожмётся")
	}
	// Колонки со списком чатов у панели больше нет: список стоит выпадающим
	// списком в шапке окна, узел колонки погашен, и запомненную ширину целиком
	// занимает лента.
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, "side.hidden = true") {
		t.Error("колонка списка чатов не погашена: она отъест у ленты ширину панели")
	}
	over := funcBody(t, css, "@media (max-width:1100px){")
	if !strings.Contains(over, ".cpanel{position:fixed") {
		t.Error("ниже 1100 точек панель не ложится поверх доски: доска режется на две узкие колонки")
	}
	narrow := funcBody(t, css, "@media (max-width:900px){")
	if !strings.Contains(narrow, ".cpanel{width:auto") || !strings.Contains(narrow, ".cgrab{display:none}") {
		t.Error("на узком экране панель не занимает его целиком: чат рядом с доской там нечитаем")
	}
	// Полосы tmux нет ни в разметке, ни в стилях: снимок она брала только у
	// сессий дашборда и у работы из чужого окна всегда пустовала (DK-435).
	if strings.Contains(css, ".tmuxbar") {
		t.Error("в стилях осталась полоса tmux: панель убрана не совсем")
	}
	if strings.Contains(app, `"card tmuxbar"`) {
		t.Error("панель tmux осталась на экране дашборда")
	}
}

// Долгий обход не выдаётся за полный: когда бюджет поиска кончился, ответ
// говорит, сколько транскриптов просмотрено из скольких. Часы стенда идут
// шагами по две секунды, поэтому бюджета хватает на пару файлов, а не на весь
// каталог.
func TestSessionsScanBudgetNamed(t *testing.T) {
	e := newTestEnv(t)
	base := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		writeSession(t, e.home, e.proj, "", fmt.Sprintf("new-%02d", i),
			sessionLine("посмотри логи", "main"), base.Add(time.Duration(i)*time.Hour))
	}
	c := e.loggedClient(t)
	ticks := 0
	e.s.now = func() time.Time {
		ticks++
		return base.Add(time.Duration(ticks) * 2 * time.Second)
	}

	_, list, note := getSessions(t, e, c, "?task=XR-101")
	if len(list) != 0 {
		t.Fatalf("сессии задачи XR-101: %+v", list)
	}
	if !strings.Contains(note, "обход прерван по времени") || !strings.Contains(note, "из 10") {
		t.Errorf("прерванный обход назван не своими словами: %q", note)
	}
}

// Панель обязана показывать под заголовком задачи её чаты и подписывать чат
// словами. Список приезжает ручкой /chats целиком, а по задаче отбирается на
// клиенте: переключатель фильтра работает тогда без похода на сервер.
// Держится грепом по статике, как слова про паузу в тесте стопа.
func TestStaticSessionsAskedByTask(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(funcBody(t, text, "function chatsURL("), `"/chats"`) {
		t.Error("адрес списка чатов собран мимо ручки /chats: брать список больше неоткуда")
	}
	// Ключ keep едет тем же запросом: список приезжает окном свежести, и
	// открытый разговор обязан пережить окно любого возраста.
	if !strings.Contains(funcBody(t, text, "async function chatState("),
		`await api(chatsURL(project) + "?all=1" + chatKeepArg(st))`) {
		t.Error("панель берёт список чатов не общей ручкой машины (?all=1)")
	}
	if !strings.Contains(funcBody(t, text, "function chatVisible("), "(c.tasks || []).includes(st.task)") {
		t.Error("список панели не отбирается по задаче: под заголовком задачи пойдёт чужой чат")
	}
	if !strings.Contains(text, "function sessionSign") {
		t.Error("в static/app.js нет подписи сессии: нераспознанная работа пропадёт молча")
	}
}

// Лента реплик сверяется с фикстурой поле в поле (шаг 3 сценария DK-219):
// ход агента и его ответы на месте, служебное, битое и ветки субагентов
// выпали, вызов инструмента свёрнут в одну строку.
func TestSessionRepliesFieldByField(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1", "")
	var got struct {
		Session string  `json:"session"`
		Total   int     `json:"total"`
		Items   []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Session != "aaa-1" || got.Total != len(transcriptWant) {
		t.Fatalf("шапка ленты: %+v", got)
	}
	if !reflect.DeepEqual(got.Items, transcriptWant) {
		t.Errorf("лента разошлась с фикстурой:\n%+v\nожидал:\n%+v", got.Items, transcriptWant)
	}
}

// Разговор по id сессии отдаёт вместе с лентой шапку: ветку, боковое дерево,
// первую реплику и задачу с подписью, чем она узнана. Экран агента открывается
// и по id сессии, строки доски у такого захода нет, и заголовок ему брать
// больше неоткуда (DK-294).
// Первая реплика со служебными обёртками панели (снимок экрана, выделение)
// даёт шапке слова человека: обёртки срезаются до чтения First. Без среза
// реплика, начатая снимком, оставляла First пустым, и чат жил с фолбэком
// «чат <id8>» до заказа haiku (живой случай, сессия d055dcf5 с осмысленной
// первой репликой).
func TestSessionHeadCutsReplyWraps(t *testing.T) {
	e := newTestEnv(t)
	said := "<screenshot file=\"/tmp/shot.png\">\nвставлен снимок экрана\n</screenshot>\n" +
		"<selection file=\"docs/a.md\">\nстрока выделения\n</selection>\n" +
		"у коллеги не получилось завершить задачу\nвторая строка про сам разбор"
	writeSession(t, e.home, e.proj, "", "ccc-3", sessionLine(said, "main"), time.Now())
	c := e.loggedClient(t)
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 ||
		list[0].First != "у коллеги не получилось завершить задачу" {
		t.Errorf("шапка реплики с обёртками потеряла слова человека: %+v", list)
	}
}

func TestSessionHeadNamesTask(t *testing.T) {
	e := newTestEnv(t)
	// Боковое дерево задачи на месте: без него шапка честно назвала бы разговор
	// кончившимся, а тут проверяется именование задачи (мера кончившегося
	// разговора стоит своими стендами в chat_test.go).
	sideTree(t, e.proj, "xr-5")
	writeSession(t, e.home, e.proj, "-xr-5", "aaa-1", transcriptFixture, time.Now())
	writeSession(t, e.home, e.proj, "", "bbb-2",
		sessionLine("почини роутер, доступы в local-docs", "main"), time.Now())
	c := e.loggedClient(t)

	head := func(sid string) sessionInfo {
		t.Helper()
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("разговор %s: %d %s", sid, resp.StatusCode, text)
		}
		var got struct {
			Head sessionInfo `json:"head"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatal(err)
		}
		return got.Head
	}

	// Узнанная сессия: боковое дерево называет задачу, ветка и первая реплика
	// собирают заголовок экрана. Время последней записи в ожидание не пишется,
	// его ставит файловая система при записи фикстуры.
	one := head("aaa-1")
	want := sessionInfo{ID: "aaa-1", Mtime: one.Mtime, Branch: "main", Tree: "xr-5",
		First: "возьми задачу XR-005 в работу", Task: "XR-5", TaskNote: "по дереву задачи",
		Bound: boundLead, Reply: replyToSession, Live: true}
	if !reflect.DeepEqual(one, want) {
		t.Errorf("шапка узнанной сессии:\n%+v\nожидал:\n%+v", one, want)
	}
	if one.Mtime == "" {
		t.Error("шапка узнанной сессии без времени последней записи")
	}
	// Неузнанная сессия: задача пуста, но подпись стоит словами, и экран по ней
	// говорит, что узнать её не удалось.
	got := head("bbb-2")
	if got.Task != "" || got.TaskNote != unknownTaskNote {
		t.Errorf("шапка неузнанной сессии называет задачу: %+v", got)
	}
	if got.First != "почини роутер, доступы в local-docs" || got.Branch != "main" {
		t.Errorf("шапка неузнанной сессии без первой реплики и ветки: %+v", got)
	}
}

// Пагинация назад: ?n= режет хвост, ?before= отдаёт реплики до курсора, до
// начала ленты доходит без дыр. Курсор это устойчивый ключ записи, а не её
// место в ленте: место плывёт от роста боковых журналов субагентов.
func TestSessionPagination(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)

	// page ходит за страницей свежей структурой: повторный Unmarshal в тот же
	// срез оставил бы поля прежних элементов.
	page := func(query string) (int, []reply) {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?"+query, "")
		var got struct {
			Total int     `json:"total"`
			Items []reply `json:"items"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Total, got.Items
	}
	total, items := page("n=2")
	if total != len(transcriptWant) || !reflect.DeepEqual(items, transcriptWant[4:]) {
		t.Fatalf("хвост n=2: total=%d %+v", total, items)
	}
	if _, items = page("n=2&before=" + fixKey(3, 1)); !reflect.DeepEqual(items, transcriptWant[1:3]) {
		t.Fatalf("страница before=%s: %+v", fixKey(3, 1), items)
	}
	if _, items = page("n=2&before=" + fixKey(2, 0)); !reflect.DeepEqual(items, transcriptWant[:1]) {
		t.Fatalf("страница before=%s: %+v", fixKey(2, 0), items)
	}
}

// Транскрипта нет, и это 404 со словами, а не пустая лента.
func TestSessionMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/no-such", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "транскрипта no-such нет") {
		t.Fatalf("несуществующая сессия: %d %s", resp.StatusCode, text)
	}
}

// Обход путей через sid отбивается до склейки пути: {sid} приходит из URL с
// раскодированными %2F, мультиплексор их пропускает, и без сита sessionIDRe
// склейка findSession отдала бы чужой файл с диска (замечание ревью DK-219).
func TestSessionTraversalBlocked(t *testing.T) {
	e := newTestEnv(t)
	secret := `{"type":"user","message":{"role":"user","content":"секретное содержимое"},"timestamp":"2026-08-10T10:00:01.000Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(e.home, "secret.jsonl"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/..%2F..%2F..%2Fsecret", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("обход путей: %d %s, ожидал 400", resp.StatusCode, text)
	}
	if strings.Contains(text, "секретное содержимое") {
		t.Errorf("чужой файл утёк наружу: %s", text)
	}
}

// Пустая лента в потоке называется первым событием note, а не молчит: тихий
// стрим неотличим от оборвавшегося; дострение после note идёт как обычно.
func TestSessionStreamEmptyNamed(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeSession(t, e.home, e.proj, "", "aaa-1", "", time.Now())
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/sessions/aaa-1?stream=1")
	defer done()
	event, data := sseNext(t, r)
	if event != "note" || data != "в транскрипте пока нет реплик" {
		t.Fatalf("пустая лента без имени: event=%q data=%q", event, data)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Готово."}]},"timestamp":"2026-08-10T10:00:07.000Z"}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var item reply
	_, data = sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	want := reply{Seq: 0, Key: "m:0.0", Role: "assistant", Time: "2026-08-10T10:00:07.000Z", Text: "Готово."}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("дострение после note: %+v, ожидал %+v", item, want)
	}
}

// Экран транскрипта обязан слушать событие note и гасить надпись начала
// разговора, пока раньше есть что подгрузить: держится грепом по статике, как
// слово «пауза» в тесте стопа. note слушают обе панели, журнал и транскрипт.
func TestStaticTranscriptEmptyHandled(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if strings.Count(text, `addEventListener("note"`) < 2 {
		t.Error("в static/app.js меньше двух слушателей note: пустой транскрипт останется пустой коробкой")
	}
	if !strings.Contains(text, "atStart.hidden") {
		t.Error("в static/app.js надпись начала разговора не гаснет: висит и там, где раньше есть что показать")
	}
}

// SSE транскрипта: последние реплики приходят сразу, дописанная запись
// доезжает живым дострением с продолжением нумерации.
func TestSessionStreamAppends(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/sessions/aaa-1?stream=1")
	defer done()
	for i, want := range transcriptWant {
		var item reply
		_, data := sseNext(t, r)
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(item, want) {
			t.Fatalf("событие %d: %+v, ожидал %+v", i, item, want)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Готово."}]},"timestamp":"2026-08-10T10:00:07.000Z"}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var item reply
	_, data := sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	// Ключ дописанной записи считается от её смещения в файле: строка легла
	// сразу за фикстурой.
	want := reply{Seq: 6, Key: fmt.Sprintf("m:%d.0", len(transcriptFixture)),
		Role: "assistant", Time: "2026-08-10T10:00:07.000Z", Text: "Готово."}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("живое дострение: %+v, ожидал %+v", item, want)
	}
}

// Панель держит взгляд якорями общего куска (DK-371): своей механики ленты у
// неё нет, а поколение живых потоков приходит своё, чтобы перерисованный рядом
// экран ленту не гасил.
func TestStaticChatFeedKeepsPlace(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "function wireChatFeed(")
	for _, want := range []string{"return wireFeed(project, sid, {", "scroll: feed",
		"item: chatItem", "live: chatLive", "era: () => chatGen"} {
		if !strings.Contains(body, want) {
			t.Errorf("в ленте панели нет %q: лента поднимается не общим куском", want)
		}
	}
	for _, gone := range []string{"EventSource", "?before=", "keepBottom("} {
		if strings.Contains(body, gone) {
			t.Errorf("в ленте панели осталась своя механика ленты (%s): правка ленты снова делается дважды", gone)
		}
	}
	feed := funcBody(t, text, "async function wireFeed(")
	for _, want := range []string{"const bottom = atBottom(scroll)", "keepBottom(scroll, true)", "keepPlace(scroll, rest)"} {
		if !strings.Contains(feed, want) {
			t.Errorf("в ленте нет %q: прокрутка сорвётся при дострении", want)
		}
	}
}

// Лента разговора написана один раз: стрим и пагинация лежат в общем куске, и
// второго их набора в статике нет. До DK-371 копий было две, и всякая правка
// ленты делалась дважды.
func TestStaticFeedIsOneCopy(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if n := strings.Count(text, `sessionURL(project, sid) + "?stream=1"`); n != 1 {
		t.Errorf("поток разговора поднимается %d раз, ожидал один", n)
	}
	if n := strings.Count(text, `"?before=" + encodeURIComponent(firstKey)`); n != 1 {
		t.Errorf("пагинация ленты написана %d раз, ожидал один", n)
	}
	if n := strings.Count(text, `sessionURL(project, sid) +`); n != 5 {
		t.Errorf("адрес разговора собирается %d раз, ожидал пять "+
			"(хвост, история, догон после обрыва, поток, ручка реплики)", n)
	}
	chat := funcBody(t, text, "function wireChatFeed(")
	for _, gone := range []string{"EventSource", "?before=", "es.onmessage"} {
		if strings.Contains(chat, gone) {
			t.Errorf("в ленте чата осталась своя механика (%s): вынос не закрыт", gone)
		}
	}
}

// Механика общей ленты проверяется исполнением, а не текстом исходника:
// предмет тут это отсев повторов потока, подгрузка от прокрутки вверх и отбор
// реплики, которым и различаются экраны. Стенд поднимает статику в node с заглушкой DOM
// (testdata/feed_shared.mjs) и гоняет по одному разговору оба экрана. Без node
// шаг пропускается: узел стенда, а не рабочей части.
func TestFeedSharedByBothScreens(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд общей ленты пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "feed_shared.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("общая лента разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Транскрипт без ID задачи в шапке: окно человека, о котором известно только
// то, что оно работает.
const plainSessionFixture = `{"type":"user","message":{"role":"user","content":"поправь вёрстку карточки"},"timestamp":"2026-08-11T11:59:00.000Z","gitBranch":"main"}
`

// Интерактивные сессии видны живыми работами: свежий транскрипт даёт работу,
// протухший по порогу не даёт, сессия без задачи остаётся в списке и подписана
// заголовком своего чата, вид работы берётся со строки доски, а сессия задачи,
// у которой уже идёт tmux-работа, второй карточкой не задваивается.
func TestLiveWorksSessions(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", "live-plain", plainSessionFixture, now.Add(-time.Minute))
	writeSession(t, e.home, e.proj, "-xr-005", "live-task", transcriptFixture, now.Add(-2*time.Minute))
	writeSession(t, e.home, e.proj, "-xr-5", "live-dup", transcriptFixture, now.Add(-3*time.Minute))
	writeSession(t, e.home, e.proj, "-xr-88", "stale", transcriptFixture, now.Add(-30*time.Minute))

	// Своей работу делает запись реестра с именем tmux-сессии, а не образец
	// имени: её кладёт подъём дашборда, и записана она только у goal-XR-9.
	// Сессию task-XR-5 с тем же образцом имени завела рука в терминале, и
	// своей она не считается (жалоба пользователя на признак без опоры).
	writeBinds(t, e.home, bindTmux("2026-08-11T11:00:00",
		"aaaa9999-9999-4999-8999-999999999999", "XR-9", "goal-XR-9"))

	want := []Work{
		{ID: "XR-9", Kind: "goal", Via: "tmux", Own: true, Tmux: "goal-XR-9", Model: chatModelDefault},
		{ID: "XR-5", Kind: "task", Via: "tmux", Model: chatModelDefault},
		{ID: "XR-112", Kind: "goal", Via: "registry"},
		// Окна человека дашборд не поднимал: имени tmux-сессии у них нет, и
		// своими они не считаются.
		{Kind: "session", Via: "session", Session: "live-plain", Note: "поправь вёрстку карточки",
			Model: chatModelDefault, Harness: "перваяtest"},
		// Боковое дерево называет задачу именем каталога, и работой сессия
		// считается по нему же: строка XR-005 у неё своя.
		{ID: "XR-005", Kind: "task", Title: "Задача в работе", Sect: "in-progress", Via: "session",
			Session: "live-task", Model: chatModelDefault, Harness: "перваяtest",
			Rows: []string{"XR-005"}},
	}
	if got := bareWorks(boardWorks(t, e)); !reflect.DeepEqual(got, want) {
		t.Errorf("живые работы:\n%+v\nожидал:\n%+v", got, want)
	}
}

// Два окна одной задачи это одна работа: без дедупликации по задаче доска
// показывала бы двух агентов там, где сидит один человек. tmux в этом
// окружении сессий не держит, дедуп делают сами сессии.
func TestLiveWorksSessionsSameTask(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "-xr-002", "win-new", transcriptFixture, now.Add(-time.Minute))
	writeSession(t, e.home, e.proj, "-xr-002", "win-old", transcriptFixture, now.Add(-2*time.Minute))
	writeSession(t, e.home, e.proj, "-xr-100", "win-goal", transcriptFixture, now.Add(-3*time.Minute))

	// Цель со строки доски остаётся целью и в интерактивном окне: по виду
	// работы клиент открывает переписку, и обычной задаче она не положена.
	want := []Work{
		{ID: "XR-002", Kind: "task", Title: "Обычная задача", Sect: "backlog", Via: "session",
			Session: "win-new", Model: chatModelDefault, Harness: "перваяtest",
			Rows: []string{"XR-002"}},
		{ID: "XR-100", Kind: "goal", Title: "Цель: пробный цикл", Sect: "in-progress", Via: "session",
			Session: "win-goal", Model: chatModelDefault, Harness: "перваяtest",
			Rows: []string{"XR-100"}},
	}
	got := boardWorks(t, e)
	var sessions []Work
	for _, w := range got {
		if w.Via == "session" {
			sessions = append(sessions, w)
		}
	}
	if !reflect.DeepEqual(bareWorks(sessions), want) {
		t.Errorf("работы из сессий:\n%+v\nожидал:\n%+v", sessions, want)
	}
}

// Задача с чужим префиксом проекту не приписывается: ходить по ней на экран
// задачи некуда, и работа остаётся в списке. Подписи «задача не с доски
// проекта» на карточке больше нет: заголовок чата встаёт поверх любой подписи
// о неузнанной задаче (sessionWorks), и чужая задача видна так же, как её
// отсутствие.
func TestLiveWorksSessionForeignTask(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	// Чужую задачу называет боковое дерево ab-9: имя ветки задачу больше не
	// привязывает, и назвать её транскриптом больше нечем.
	writeSession(t, e.home, e.proj, "-ab-9", "win-foreign", plainSessionFixture, now.Add(-time.Minute))

	want := []Work{{Kind: "session", Via: "session", Session: "win-foreign",
		Note: "поправь вёрстку карточки", Model: chatModelDefault, Harness: "перваяtest"}}
	var sessions []Work
	for _, w := range boardWorks(t, e) {
		if w.Via == "session" {
			sessions = append(sessions, w)
		}
	}
	if !reflect.DeepEqual(bareWorks(sessions), want) {
		t.Errorf("работа с чужой задачей:\n%+v\nожидал:\n%+v", sessions, want)
	}
}

// Транскрипт, поднятый самим дашбордом: первая реплика это заказ headless-сеанса,
// словами groomPrompt (DK-358).
const groomOrderFixture = `{"type":"user","message":{"role":"user","content":"Проведи груминг XR-007"},"timestamp":"2026-08-11T11:59:00.000Z","gitBranch":"main"}
`

// Законченный headless-разбор не висит живой работой до порога протухания:
// транскрипт сессии, поднятой самим дашбордом, узнаётся по заказу первой
// реплики и из живых работ выкидывается. Пока разбор идёт, его держит карточка
// tmux-сессии, и транскрипт не дублирует её второй карточкой. Фильтр узнаёт
// заказ, а не говорившего, поэтому интерактивное окно с теми же словами в чате
// карточку тоже теряет: жертва названа в разборе DK-358.
func TestLiveWorksGroomOrderFollowsTmux(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", "groom-live", groomOrderFixture, now.Add(-time.Minute))
	writeSession(t, e.home, e.proj, "", "groom-done", groomOrderFixture, now.Add(-2*time.Minute))

	// Обе сессии мертвы для tmux: фикстура отвечает пустым списком, как
	// сервер tmux без единой сессии. Живым окнам груминг не встречается, а
	// мёртвым нечего занимать карточку работы.
	var sessions []Work
	for _, w := range boardWorks(t, e) {
		if w.Via == "session" {
			sessions = append(sessions, w)
		}
	}
	if len(sessions) != 0 {
		t.Errorf("законченные headless-разборы висят работами: %+v", sessions)
	}

	// Живая tmux-сессия груминга держит работу карточкой tmux, как и раньше:
	// транскрипт не дублирует её второй карточкой, а по смерти сессии работа
	// падает целиком, что и проверяет первая половина.
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "task-XR-007\n")
	var ids []string
	for _, w := range boardWorks(t, e) {
		if w.ID == "XR-007" {
			ids = append(ids, w.Via)
		}
	}
	if !reflect.DeepEqual(ids, []string{"tmux"}) {
		t.Errorf("живой headless-разбор даёт карточки %+v, ожидал одну tmux", ids)
	}
}

// boardWorks читает живые работы проекта из ответа доски.
// bareWorks снимает с работ состояние и время последнего хода: их считают живые
// источники (реестр клиента, транскрипт, признак ожидания), в сверке состава
// работ им не место, а своя проверка у них есть (TestWorkLiveState).
func bareWorks(list []Work) []Work {
	out := make([]Work, 0, len(list))
	for _, w := range list {
		w.Live, w.Moved = "", 0
		out = append(out, w)
	}
	return out
}

func boardWorks(t *testing.T, e *testEnv) []Work {
	t.Helper()
	resp := doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/board", "")
	var got struct {
		Works []Work `json:"works"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	return got.Works
}

// Стоп интерактивной сессии это отказ словами: её ведёт человек в окне, и
// снимать дашборду нечего.
func TestRunStopInteractiveSession(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "-xr-005", "live-task", transcriptFixture, now.Add(-time.Minute))

	c := e.loggedClient(t)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-005", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "человек в окне") {
		t.Fatalf("стоп интерактивной сессии: %d %s, ожидал 409 про окно человека", resp.StatusCode, text)
	}
}

// Полоски «работает N агентов» над доской больше нет вовсе: сессии стоят своим
// табом с числом на нём, а строка над доской повторяла это число и уводила туда
// же вторым способом (решение пользователя). Признак живости при этом остался
// на экране задачи и говорит словом из общего словаря состояний.
func TestStaticInteractiveWork(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if strings.Contains(text, "function renderLive(") {
		t.Error("сборка полоски работ осталась в статике мёртвым кодом")
	}
	if strings.Contains(readFile(t, filepath.Join("static", "index.html")), `id="live"`) {
		t.Error("узел полоски работ остался в разметке")
	}
	chip := funcBody(t, text, "function liveChip(")
	if !strings.Contains(chip, "workLiveChip(") {
		t.Error("форма задачи называет состояние своими словами, мимо словаря")
	}
	if !strings.Contains(chip, "интерактивная сессия") {
		t.Error("экран задачи не подписывает интерактивную сессию")
	}
	if !strings.Contains(funcBody(t, text, "async function renderTask("), "liveChip(work)") {
		t.Error("признак живости не встал на экран задачи")
	}
}

// Работа зовётся заголовком с доски и на полосе живых работ, и в шапке панели
// разговора, а служебное имя сессии в подписи не стоит.
func TestStaticWorkTitle(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	// Шапка панели зовёт чат его заголовком, а номер задачи стоит при нём
	// лейблом и ведёт на её экран. Заголовка задачи с доски в шапке больше нет:
	// место занял заголовок самого чата.
	head := funcBody(t, text, "function chatHead(")
	if !strings.Contains(head, "chatTitle(st.entry)") {
		t.Error("шапка панели подписана не заголовком чата: служебное имя сессии о занятии агента не говорит")
	}
	if !strings.Contains(head, `el("span", "cdtask", st.task)`) {
		t.Error("в шапке панели нет номера задачи: с чата не уйти на её экран")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".lcard span.wname.wtitle", ".lcard span.wname"} {
		if !strings.Contains(css, want) {
			t.Errorf("в стилях нет %s: заголовок и подпись рисуются одним кеглем", want)
		}
	}
	// Строки состояния под заголовком в шапке нет вовсе: имя инструмента и
	// давность хода повторяли ленту, которая идёт прямо под ней, а живость и
	// ожидание видны в кольце и его списке.
	if strings.Contains(head, `el("span", "cts"`) {
		t.Error("строка состояния вернулась в шапку панели: она повторяет ленту под собой")
	}
}

// Журнал витка называет источник подписью панели: строки приходят либо из
// журнала оболочки, либо из раздела «Журнал» файла цели, и путать их нельзя.
func TestStaticJournalSource(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "function wireJournal(")
	if !strings.Contains(body, `es.addEventListener("source"`) || !strings.Contains(body, "sub.textContent") {
		t.Error("подпись источника журнала не приходит с сервера")
	}
	if strings.Contains(funcBody(t, text, "async function renderTask("), `".devkit/goal-" + id + ".log"`) {
		t.Error("экран задачи называет журнал goal-<ID>.log до ответа сервера: у цели живого чата такого файла нет")
	}
}

// Язык экранов взят из макетов: журнал витка зовётся журналом витка, а жаргон
// «строки» и «запуска» с экранов ушёл. Слияние живого статуса с чатом (DK-435)
// унесло с экранов и «Лог витка» отдельной панелью, и полосу tmux.
func TestStaticAgentPaneWords(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(funcBody(t, app, "async function renderTask("), `pane("Журнал витка"`) {
		t.Error("на экране задачи нет журнала витка")
	}
	for _, gone := range []string{`"Транскрипт"`, `"Журнал цикла"`, `"Стоп цикла"`, "грумминг",
		`"строка обновилась`, `pane("Лог витка"`} {
		if strings.Contains(app, gone) {
			t.Errorf("в static/app.js осталось слово %q: экраны говорят с пользователем иначе", gone)
		}
	}
}

// Панель разговора открывается хвостом адреса, а старые адреса ведут в неё же
// (DK-435, решение 5 LLD DK-430): ссылка на «живой статус» и на разговор
// сессии, посланная себе в заметки полгода назад, обязана открываться.
func TestStaticChatRouteTail(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	rt := funcBody(t, app, "function route(")
	for _, want := range []string{`h.indexOf("/chat/")`,
		`h.match(/^([^/]+)\/(agent|session)\/(.+)$/)`, "rt.chat = "} {
		if !strings.Contains(rt, want) {
			t.Errorf("разбор адреса без %q: панель не откроется хвостом либо старая ссылка умрёт", want)
		}
	}
	if strings.Contains(funcBody(t, app, "function screenKey("), "rt.chat") {
		t.Error("разговор попал в ключ экрана: открытие панели пересоберёт доску под ней")
	}
	open := funcBody(t, app, "function openChat(")
	if !strings.Contains(open, "history.pushState(") {
		t.Error("панель открывается не pushState: «назад» перестанет её закрывать")
	}
	if !strings.Contains(funcBody(t, app, "function closeChat("), "history.back()") {
		t.Error("крестик панели не возвращает доску на прежнее место")
	}
}

// Ширина панели тянется хватом и помнится одним числом на весь дашборд, а не
// на задачу: человек ставит её под свой экран, а не под предмет разговора.
func TestStaticChatWidthRemembered(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, `const CHAT_W_KEY = "devkit.chat.width"`) {
		t.Error("ширина панели не помнится в localStorage: каждый заход начинается с умолчания")
	}
	// Потолок ширины меряется окном, а не точками: разговор бывает главным
	// делом экрана, и упор в 640 точек на широком мониторе мешал (замечание
	// пользователя). Пол остался прежним: уже 320 точек лента нечитаема.
	if !strings.Contains(app, "const CHAT_W_MIN = 320") {
		t.Error("нет нижнего предела ширины: панель схлопнется")
	}
	if strings.Contains(app, "const CHAT_W_MAX") {
		t.Error("вернулся потолок ширины в точках: панель снова не раздвинуть")
	}
	if !strings.Contains(app, "win - CHAT_W_KEEP") {
		t.Error("потолок ширины не меряется окном: доске не останется полосы")
	}
	grab := funcBody(t, app, "function wireChatGrab(")
	for _, want := range []string{"pointerdown", "pointermove", "window.innerWidth - ev.clientX",
		"saveChatWidth("} {
		if !strings.Contains(grab, want) {
			t.Errorf("в хвате панели нет %q: ширина не потянется или не запомнится", want)
		}
	}
}

// Ввод панели не гаснет молча. Четырёх причин гашения (решение 6 LLD DK-430)
// в POC не осталось вовсе: chatWay называет вид доставки одним из трёх слов и
// не гасит ввода ни в одном из них, потому что реплике есть куда ехать и у
// живой сессии, и у кончившейся. Молчаливое же гашение неотличимо от поломки,
// поэтому механика причины из панели не убрана: пришла причина, она стоит
// словами над вводом.
//
// Кнопки «Привязать к задаче» тут больше нет, и вместе со снятым угадыванием
// по первой реплике это значит, что чат, заведённый руками вне дерева задачи,
// привязать к ней с экрана нечем (ручка сессии на месте, звать её некому).
func TestStaticChatInputReasons(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	way := funcBody(t, app, "function chatWay(")
	for _, want := range []string{
		`return { kind: "new", off: false, why: "" }`,
		`return { kind: "say", off: false, why: "" }`,
		`return { kind: "resume", off: false, why: "" }`} {
		if !strings.Contains(way, want) {
			t.Errorf("вид доставки не назван словами: нет %q", want)
		}
	}
	panel := funcBody(t, app, "function chatPanel(")
	for _, want := range []string{"const way = chatWay(st)", `if (way.kind === "new") {`,
		"ta.disabled = Boolean(way.off)", "send.disabled = Boolean(way.off)"} {
		if !strings.Contains(panel, want) {
			t.Errorf("панель везёт реплику мимо вида доставки: нет %q", want)
		}
	}
	if !strings.Contains(panel, "if (way.why) {") ||
		!strings.Contains(panel, `note.append(el("span", "", way.why))`) {
		t.Error("причина не встаёт словами над вводом: гашение будет неотличимо от поломки")
	}
}

// harnessJSONHomeFixture это раскладка, где у второй подписки есть своё
// хозяйство: путь подставляется каталогом теста.
const harnessJSONHomeFixture = `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1"},
    {"name": "втораяtest", "enabled": true, "default": false, "bin": "клиент-2",
     "home": %q, "env": ["CLAUDE_CONFIG_DIR"]}
  ]
}`

// headlessLine это первая запись транскрипта headless-сессии, поднятой с
// доски: заказ приезжает в сессию промптом, и разговор начинается им, а не
// репликой из окна. Поля взяты с живого транскрипта такого запуска.
func headlessLine(prompt, branch string) string {
	return fmt.Sprintf(
		`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-10T10:00:00.000Z","content":%q}`+"\n"+
			`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":%q},`+
			`"timestamp":"2026-08-10T10:00:01.000Z","promptSource":"sdk","entrypoint":"sdk-cli","gitBranch":%q}`+"\n",
		prompt, prompt, branch)
}

// Запуск с доски идёт на выбранной подписке, а её клиент поднимается со своим
// каталогом конфигурации и журнал разговора пишет туда. Пока сессии искались
// в одном ~/.claude, headless-работа с доски была не видна вовсе: строка
// стояла в In progress, а разговора не было ни в списке задачи, ни на экране
// агента, и найти его можно было только раскопками по транскриптам (DK-362).
func TestSessionsSecondHarnessSeen(t *testing.T) {
	e := newTestEnv(t)
	home := filepath.Join(e.home, ".devkit", "claude-second")
	writeAgentctlFake(t, e.bin, fmt.Sprintf(harnessJSONHomeFixture, home))
	sid := "hls-1"
	writeSessionAt(t, filepath.Join(home, "projects"), e.proj, "", sid,
		headlessLine("Выполни XR-101", "main"),
		time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC))
	// Запуск с доски пишет в реестр строку с источником «заказ»: ею и названа
	// задача, ID из текста заказа сессию не привязывает.
	writeBinds(t, e.home, bindRecord("2026-08-10T10:00:00", sid, "XR-101", bindOrder))
	c := e.loggedClient(t)

	_, list, note := getSessions(t, e, c, "?task=XR-101")
	if len(list) != 1 || list[0].ID != sid {
		t.Fatalf("сессии задачи XR-101: %+v, приписка: %s", list, note)
	}
	if list[0].Task != "XR-101" || list[0].First != "Выполни XR-101" {
		t.Errorf("headless-сессия названа не заказом с доски: %+v", list[0])
	}
	// Экран агента открывается по той же ссылке, что у обычной сессии: без
	// второго корня ручка отвечала бы «транскрипта нет среди сессий проекта».
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("экран агента по headless-сессии: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "Выполни XR-101") {
		t.Errorf("в ленте нет заказа работы: %s", text)
	}
}

// Каталоги журналов перечисляются без повторов: подписка вправе назвать своим
// хозяйством тот же ~/.claude, и тогда обход шёл бы по нему дважды, а список
// сессий выходил бы с двойниками.
func TestTranscriptRootsNoDoubles(t *testing.T) {
	home := filepath.Join("/дом")
	got := transcriptRoots(home, []string{filepath.Join(home, ".claude"), "", "/второй"})
	want := []string{filepath.Join(home, ".claude", "projects"), filepath.Join("/второй", "projects")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("корни журналов %v, жду %v", got, want)
	}
}

// Панель разговора замеряется настоящим движком (DK-435): ширина, которую ей
// даёт хват, граница 1100 точек и узкий экран. Проверкой текста стилей такое не
// берётся, поломка тут даёт сложение правил из разных мест файла с раскладкой
// .screen: строки про ширину и медиазапросы стоят на месте, а панель при этом
// либо режет доску пополам на ноутбуке, либо схлопывается в полосу на телефоне.
// Ровно так это ловилось у экрана задачи (DK-284). Зажим ширины числом (320..640)
// живёт в статике и меряется стендом node (chat_panel.mjs, там гоняется сама
// chatWidth); браузер меряет то, что видно только браузеру, как переменная --cw
// доезжает до раскладки. Браузер стенду называет DASHBOARD_CHROME, без него шаг
// пропускается.
func TestStaticChatPanelMeasured(t *testing.T) {
	// Замер стоит на разметке, которую POC ветки poc-chat заменил: список разговоров стал выпадашкой, колонки слева больше нет
	// Стенд повторяет прежнюю вёрстку руками, и чинить его до конца POC дороже,
	// чем он стоит. Пропуск назван вслух, чтобы его не приняли за зелень.
	t.Skip("замер ждёт вёрстки, снятой POC: список разговоров больше не колонка панели")
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер панели разговора пропущен")
	}
	// Разметка стенда повторяет панель руками и разъехаться с рабочей может
	// молча: замер на своей вёрстке зеленел бы и после того, как панель
	// перестали собирать этими узлами.
	page := readFile(t, filepath.Join("static", "index.html"))
	for _, want := range []string{`<aside class="cpanel" id="cpanel" hidden`,
		`id="cgrab"`, `id="cpin"`, `id="clist"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("панели нет в разметке страницы (нет %q): замер говорил бы о своей вёрстке", want)
		}
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{`document.documentElement.style.setProperty("--cw"`,
		`panel.hidden = false`} {
		if !strings.Contains(app, want) {
			t.Fatalf("панель показывается и меряется не тем способом, что в замере (нет %q)", want)
		}
	}
	dir, stand := chromeStand(t, "chat_panel_widths.js")

	// Ноутбук: панель стоит колонкой экрана и сжимает доску рядом. Ширина
	// приходит переменной, той же, какую двигает хват.
	narrow := chromeMeasure(t, chrome, dir, stand, "1400,900", "w320")
	wide := chromeMeasure(t, chrome, dir, stand, "1400,900", "w640")
	// Запомненная ширина это ширина ленты: список разговоров стоит колонкой
	// слева и прибавляется к ней, а не делит её (DK-436). Делил бы, и панель на
	// 320 точках оставила бы чтению полосу.
	list := narrow["list-w"]
	if list < 140 {
		t.Fatalf("список разговоров ужался до %d точек: читать его там нечем", list)
	}
	if narrow["panel-w"] != 320+list || wide["panel-w"] != 640+list {
		t.Fatalf("панель не берёт ширину переменной --cw: %d и %d при 320 и 640 плюс список %d",
			narrow["panel-w"], wide["panel-w"], list)
	}
	for _, vals := range []map[string]int{narrow, wide} {
		if vals["list-left"] >= vals["pin-left"] {
			t.Errorf("список разговоров стоит не слева от ленты: %v", vals)
		}
	}
	for _, vals := range []map[string]int{narrow, wide} {
		if vals["board-cut"] != 1 || vals["fixed"] != 0 {
			t.Errorf("на 1400 точках панель легла поверх доски вместо колонки экрана: %v", vals)
		}
	}
	// Доска сжимается ровно на прибавку ленты: иначе она уезжает под панель
	// либо оставляет полосу пустоты.
	if got := narrow["main-w"] - wide["main-w"]; got != 320 {
		t.Errorf("доска сжалась на %d точек при прибавке панели в 320: %v / %v",
			got, narrow, wide)
	}
	if def := chromeMeasure(t, chrome, dir, stand, "1400,900", "none"); def["panel-w"] != 420+list {
		t.Errorf("панель без запомненной ширины встала на %d точек, ожидал умолчание стилей 420 плюс список %d",
			def["panel-w"], list)
	}

	// Ниже 1100 точек места на две колонки нет: панель ложится поверх доски, а
	// доска остаётся той же ширины, что и без панели.
	bare := chromeMeasure(t, chrome, dir, stand, "1000,900", "hidden")
	over := chromeMeasure(t, chrome, dir, stand, "1000,900", "w320")
	if over["fixed"] != 1 || over["board-cut"] != 0 {
		t.Errorf("на 1000 точках панель режет доску вместо того, чтобы лечь поверх: %v", over)
	}
	if over["main-w"] != bare["main-w"] {
		t.Errorf("панель поверх доски всё равно сжала её: %d против %d без панели",
			over["main-w"], bare["main-w"])
	}
	if over["panel-w"] != 320+list {
		t.Errorf("панель поверх доски потеряла запомненную ширину: %d", over["panel-w"])
	}
	// Рубеж проверяется с обеих сторон: на 1120 точках панель ещё колонка.
	if edge := chromeMeasure(t, chrome, dir, stand, "1120,900", "w320"); edge["board-cut"] != 1 {
		t.Errorf("на 1120 точках панель уже лежит поверх доски: рубеж 1100 съехал (%v)", edge)
	}

	// Узкий экран отдаёт панели весь экран тем же адресом, а хват там не нужен:
	// тянуть панель на телефоне некуда.
	phone := chromeMeasure(t, chrome, dir, stand, "390,844", "w320")
	if phone["panel-w"] != phone["screen"] || phone["panel-left"] != 0 {
		t.Errorf("на 390 точках панель не заняла экран целиком: %v", phone)
	}
	if phone["grab-w"] != 0 {
		t.Errorf("на телефоне остался хват ширины шириной %d точек", phone["grab-w"])
	}
	// На телефоне колонки не помещаются рядом: список ложится полосой над
	// лентой, отдавая ей всю ширину экрана.
	if phone["list-w"] != phone["screen"] || phone["list-left"] != phone["pin-left"] {
		t.Errorf("на 390 точках список разговоров остался колонкой: %v", phone)
	}
	// Панель на любой ширине остаётся пригодной для чтения и набора: поле ввода
	// и лента занимают её, а не полосу в углу.
	for name, vals := range map[string]map[string]int{"ноутбук": narrow, "поверх доски": over, "телефон": phone} {
		if vals["input-w"] < 240 || vals["feed-w"] < 240 {
			t.Errorf("на раскладке %q панель ужалась: поле ввода %d, лента %d",
				name, vals["input-w"], vals["feed-w"])
		}
	}
}

// writeSubLog кладёт боковой журнал субагента при транскрипте: мета-файл со
// своим toolUseId и сам журнал. Так их пишет харнес, и по мете дашборд их и
// находит.
func writeSubLog(t *testing.T, path, id, about, content string) string {
	t.Helper()
	dir := subDir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "agent-"+id+".meta.json")
	body := fmt.Sprintf(`{"agentType":"claude","description":%q,"toolUseId":%q}`, about, "tool-"+id)
	if err := os.WriteFile(meta, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "agent-"+id+".jsonl")
	if err := os.WriteFile(log, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return log
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// sideLine это запись бокового журнала: текст ответа субагента с меткой
// isSidechain, как их пишет харнес.
func sideLine(text, at string) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":%q}]},"timestamp":%q}`, text, at) + "\n"
}

// Субагента зовут и посреди открытого потока: его журнал заводится позже
// подписки, и стрим обязан заметить новый файл сам. Прежде стрим цеплял один
// «живой» журнал на открытии и ронял его насовсем, стоило в транскрипте
// закрыться любому вызову инструмента: после этого лента молчала до
// перезагрузки страницы, и ровно это человек и увидел.
func TestSessionStreamPicksUpNewSubLog(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/sessions/aaa-1?stream=1")
	defer done()
	for range transcriptWant {
		sseNext(t, r)
	}
	// Хвост уехал, поток открыт: теперь работа уходит субагенту, и его журнал
	// заводится только сейчас.
	log := writeSubLog(t, path, "new1", "разбор находки",
		sideLine("смотрю дерево", "2026-08-10T10:00:08.000Z"))
	var item reply
	_, data := sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	if item.Text != "смотрю дерево" || item.Sub == "" {
		t.Fatalf("запись нового субагента не доехала: %+v", item)
	}
	if item.Key != "agent-new1:0.0" {
		t.Fatalf("ключ записи нового журнала: %q, ожидал agent-new1:0.0", item.Key)
	}
	// Вызов в транскрипте закрылся ответом, а журнал субагента пишется дальше:
	// прежде стрим на этом ответе переставал его читать вовсе.
	appendLine(t, path, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-new1","content":"готово"}]},"timestamp":"2026-08-10T10:00:09.000Z"}`+"\n")
	_, data = sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	if item.Role != roleToolOut {
		t.Fatalf("ответ инструмента не доехал: %+v", item)
	}
	appendLine(t, log, sideLine("продолжаю после ответа", "2026-08-10T10:00:10.000Z"))
	_, data = sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	if item.Text != "продолжаю после ответа" {
		t.Fatalf("после ответа инструмента журнал субагента перестал читаться: %+v", item)
	}
	// Дописанная запись зовётся своим смещением: первая строка журнала уже
	// занимает своё место, и второй ключ начинается за ней.
	next := fmt.Sprintf("agent-new1:%d.0", len(sideLine("смотрю дерево", "2026-08-10T10:00:08.000Z")))
	if item.Key != next {
		t.Fatalf("ключ дописанной записи: %q, ожидал %s", item.Key, next)
	}
}

// Конец фоновой работы и финальный отчёт субагента это одно событие: в ленте
// они сходятся одним свёрнутым блоком, а сырым текстом отчёт рядом не стоит.
func TestAgentReportFoldsIntoNote(t *testing.T) {
	e := newTestEnv(t)
	done := `{"type":"user","message":{"role":"user","content":[{"type":"text",` +
		`"text":"<task-notification><status>completed</status><summary>вычитка готова</summary></task-notification>"}]},` +
		`"timestamp":"2026-08-10T10:00:09.000Z"}` + "\n"
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture+done, time.Now())
	writeSubLog(t, path, "rep1", "вычитка",
		sideLine("смотрю документ", "2026-08-10T10:00:07.000Z")+
			sideLine("Готово, семнадцать замечаний. sha: abc1234", "2026-08-10T10:00:08.000Z"))
	c := e.loggedClient(t)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?n=50", "")
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	var note *reply
	raw := 0
	for i, item := range got.Items {
		if item.Mark == "agent" {
			note = &got.Items[i]
		}
		if item.Sub != "" && strings.Contains(item.Text, "sha: abc1234") {
			raw++
		}
	}
	if note == nil {
		t.Fatal("записи о конце фоновой работы нет вовсе")
	}
	if !strings.Contains(note.Note, "завершил работу") || !strings.Contains(note.Note, "вычитка готова") {
		t.Fatalf("заголовок блока без сути: %q", note.Note)
	}
	if !strings.Contains(note.Text, "sha: abc1234") {
		t.Fatalf("отчёт субагента не уехал внутрь блока: %q", note.Text)
	}
	if raw != 0 {
		t.Fatalf("сырой отчёт остался в ленте вторым элементом: %d", raw)
	}
	// Промежуточные записи субагента остаются на месте: свернулся только отчёт.
	seen := false
	for _, item := range got.Items {
		if item.Sub != "" && strings.Contains(item.Text, "смотрю документ") {
			seen = true
		}
	}
	if !seen {
		t.Fatal("ход субагента пропал вместе с отчётом")
	}
}

// Реплика диспетчера субагенту в слитой ленте стоит дважды: карточкой
// SendMessage в транскрипте сессии и рамкой в его боковом журнале. Рамка тут
// чистый дубль, и в ленту она не идёт; встречные рамки остаются, у них пары
// нет.
func TestDispatchFrameFromSelfIsDropped(t *testing.T) {
	e := newTestEnv(t)
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	mine := "The coordinator sent a message while you were working:\nПочини стрим.\n\nAddress this before completing your current task."
	alien := "Another Claude session sent a message while you were working:\nЗагляни в LLD."
	line := func(text, at string) string {
		return fmt.Sprintf(`{"type":"user","isSidechain":true,"message":{"role":"user","content":%q},"timestamp":%q}`, text, at) + "\n"
	}
	writeSubLog(t, path, "dup1", "разбор находки",
		line(mine, "2026-08-10T10:00:08.000Z")+line(alien, "2026-08-10T10:00:09.000Z"))
	c := e.loggedClient(t)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?n=50", "")
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	for _, item := range got.Items {
		if item.Note == "диспетчер -> субагенту" {
			t.Fatalf("дубль реплики диспетчера доехал в ленту: %+v", item)
		}
	}
	alienSeen := false
	for _, item := range got.Items {
		if item.Note == "чужая сессия -> субагенту" && strings.Contains(item.Text, "LLD") {
			alienSeen = true
		}
	}
	if !alienSeen {
		t.Fatal("рамка чужой сессии пропала вместе с дублем")
	}
}

// Реплика, пришедшая работающему субагенту, лежит в его журнале в английской
// рамке харнеса. Со слиянием журналов в ленту рамка полезла человеку в чат
// сырьём: она стоит служебной строкой со своей подписью, а сама рамка из
// текста уходит.
func TestDispatchFrameIsService(t *testing.T) {
	e := newTestEnv(t)
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	frame := "Another Claude session sent a message while you were working:\nПочини стрим, он молчит.\n\nAddress this before completing your current task."
	line := fmt.Sprintf(`{"type":"user","isSidechain":true,"message":{"role":"user","content":%q},"timestamp":"2026-08-10T10:00:08.000Z"}`, frame) + "\n"
	writeSubLog(t, path, "disp1", "разбор находки", line)
	c := e.loggedClient(t)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?n=50", "")
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	var note *reply
	for i, item := range got.Items {
		if item.Role == roleNote && item.Note != "" {
			note = &got.Items[i]
		}
		if strings.Contains(item.Text, "sent a message while you were working") {
			t.Fatalf("рамка харнеса доехала в ленту сырьём: %+v", item)
		}
	}
	if note == nil {
		t.Fatal("реплика диспетчера не стала служебной строкой")
	}
	if note.Note != "чужая сессия -> субагенту" {
		t.Fatalf("подпись служебной строки: %q", note.Note)
	}
	if note.Text != "Почини стрим, он молчит." {
		t.Fatalf("текст реплики диспетчера: %q", note.Text)
	}
}

// Весть о законченной фоновой работе едет в ленту с машинной пометкой: по ней
// панель красит кружок события синим, не разбирая слов заголовка. Прежде
// пометки не было вовсе, и лента отличала эту запись от прочей служебки только
// текстом (замечание 9 четырнадцатого круга POC).
func TestBackgroundAgentNoteCarriesMark(t *testing.T) {
	text := "<task-notification>\n<task-id>a08d9d8</task-id>\n<status>completed</status>\n" +
		"<summary>Agent finished</summary>\n</task-notification>\nразобрал находку"
	var got []reply
	addUser(func(r reply) { got = append(got, r) }, "user", "2026-08-20T10:00:00Z", text, false)
	var note *reply
	for i := range got {
		if got[i].Role == roleNote {
			note = &got[i]
		}
	}
	if note == nil {
		t.Fatalf("служебной записи о фоновом агенте нет вовсе: %+v", got)
	}
	if note.Mark != "agent" {
		t.Fatalf("пометка записи %q, жду agent: %+v", note.Mark, *note)
	}
	if note.Note != "Фоновый агент завершил работу" {
		t.Fatalf("заголовок записи разошёлся: %q", note.Note)
	}
	// Слова человека из той же реплики пометки не носят: покрасить их синим
	// значило бы выдать реплику за событие фоновой работы.
	for _, r := range got {
		if r.Role == "user" && r.Mark != "" {
			t.Fatalf("реплика человека унесла пометку события: %+v", r)
		}
	}
}

// План сессии приходит из двух источников: файла ~/.devkit/plans/<sid>.json и
// последнего вызова TodoWrite в транскрипте. Побеждает свежий: в обход
// разрешений харнес TodoWrite не выдаёт вовсе, и файл там единственная дорога.
func TestSessionPlanFileWins(t *testing.T) {
	e := newTestEnv(t)
	todo := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"content":"из транскрипта","status":"in_progress","activeForm":"Делаю"}]}}]},"timestamp":"2026-08-10T10:00:03.000Z"}` + "\n"
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture+todo, time.Now())
	c := e.loggedClient(t)

	planOfSession := func() []planItem {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?n=5", "")
		var got struct {
			Plan []planItem `json:"plan"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Plan
	}
	if plan := planOfSession(); len(plan) != 1 || plan[0].Text != "из транскрипта" {
		t.Fatalf("план из транскрипта не прочитан: %+v", plan)
	}
	// Файл свежее записи: его и показываем.
	dir := planDir(e.home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := planPath(e.home, "aaa-1")
	body := `[{"text":"из файла","state":"completed"},{"text":"второй","state":"in_progress"},{"text":"третий","state":"кривое"}]`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := planOfSession()
	if len(plan) != 3 || plan[0].Text != "из файла" || plan[0].State != "completed" {
		t.Fatalf("план из файла не победил: %+v", plan)
	}
	// Чужое состояние не роняет разбор и читается как «ждёт».
	if plan[2].State != "pending" {
		t.Errorf("незнакомое состояние пункта: %q, ждал pending", plan[2].State)
	}
	// Битый файл это не поломка ленты: план тогда берётся из транскрипта.
	if err := os.WriteFile(file, []byte("{не json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := planOfSession(); len(got) != 1 || got[0].Text != "из транскрипта" {
		t.Fatalf("битый файл плана увёл ленту с транскрипта: %+v", got)
	}
	_ = path
}

// План сессии без файла по sid находится по имени tmux из реестра чатов:
// запасной адрес правила плана велит сессии с пустым CLAUDE_CODE_SESSION_ID
// (контур второй подписки, живой случай DK-269) писать план файлом имени своей
// tmux-сессии, и читатель кружка обязан смотреть туда же, иначе правило
// выполнено, а кольцо слепое.
func TestSessionPlanByTmuxName(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-2", transcriptFixture, time.Now())
	reg := filepath.Join(e.home, ".devkit", "sessions.log")
	if err := os.MkdirAll(filepath.Dir(reg), 0o755); err != nil {
		t.Fatal(err)
	}
	line := "2026-08-10T10:00:00 сессия aaa-2 задача XR-004 проект demo дерево - " +
		"транскрипт - источник заказ повод startup tmux task-XR-004\n"
	if err := os.WriteFile(reg, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(planDir(e.home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath(e.home, "task-XR-004"),
		[]byte(`[{"text":"по имени tmux","state":"in_progress"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)
	read := func() []planItem {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-2?n=5", "")
		var got struct {
			Plan []planItem `json:"plan"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Plan
	}
	if plan := read(); len(plan) != 1 || plan[0].Text != "по имени tmux" {
		t.Fatalf("план по имени tmux не прочитан: %+v", plan)
	}
	// Файл по sid главнее: его пишет сессия, знающая свой ID, и запасной адрес
	// при нём не смотрится вовсе.
	if err := os.WriteFile(planPath(e.home, "aaa-2"),
		[]byte(`[{"text":"по sid","state":"in_progress"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan := read(); len(plan) != 1 || plan[0].Text != "по sid" {
		t.Fatalf("файл по sid не победил запасной адрес: %+v", plan)
	}
}

// Приписка заказа с запасным адресом плана режется из пузыря целиком: имя tmux
// в ней у каждого заказа своё, и константой хвост не узнаётся.
func TestCutOrderRulesPlanFallback(t *testing.T) {
	rule := planRule + " Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом " +
		"~/.devkit/plans/task-DK-269.json."
	said, rules := cutOrderRules("сделай хорошо " + rule + " " + paceRule)
	if said != "сделай хорошо" {
		t.Errorf("слова человека обрезаны не так: %q", said)
	}
	if !strings.Contains(rules, "task-DK-269.json") || !strings.Contains(rules, paceRule) {
		t.Errorf("приписки заказа не собраны: %q", rules)
	}
}

// Автор реплики, пришедшей каналом живых сессий, читается по отправителю:
// дашборд несёт слова человека, живая сессия клиента это агент-диспетчер, а
// всё прочее просто агент. До этого любая такая реплика рисовалась пузырём
// «вы» и жёлтым цветом человека, хотя писал её другой процесс (замечание
// пользователя).
func TestPeerReplyAuthorBySource(t *testing.T) {
	dir := t.TempDir()
	old := peerRegistryDir
	peerRegistryDir = func() string { return dir }
	t.Cleanup(func() {
		peerRegistryDir = old
		forgetPeerKinds()
	})
	live := `{"pid":1,"sessionId":"aaa","name":"devkit-20","kind":"interactive"}`
	if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	other := `{"pid":2,"sessionId":"bbb","name":"devkit-sub","kind":"sdk"}`
	if err := os.WriteFile(filepath.Join(dir, "2.json"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	forgetPeerKinds()

	line := func(name, text string) string {
		body := fmt.Sprintf(`<cross-session-message from=\"uds:/tmp/cc-socks/9.sock\" `+
			`from-name=\"%s\" from-mode=\"prompting\">\n%s\n</cross-session-message>`, name, text)
		return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"%s"},`+
			`"timestamp":"2026-08-21T10:00:00.000Z"}`, body) + "\n"
	}
	data := []byte(line("dashboard", "слова человека") +
		line("devkit-20", "слова диспетчера") +
		line("devkit-sub", "слова субагента"))

	got := parseReplies(data, 0)
	if len(got) != 3 {
		t.Fatalf("реплик в ленте %d, ждал три: %+v", len(got), got)
	}
	for i, want := range []struct{ text, who, note string }{
		{"слова человека", "", ""},
		{"слова диспетчера", whoLead, "из сессии devkit-20"},
		{"слова субагента", whoAgent, "из сессии devkit-sub"},
	} {
		if got[i].Text != want.text {
			t.Fatalf("реплика %d: текст %q, ждал %q", i, got[i].Text, want.text)
		}
		if got[i].Who != want.who {
			t.Errorf("реплика %q подписана %q, ждал %q", want.text, got[i].Who, want.who)
		}
		if got[i].Note != want.note {
			t.Errorf("реплика %q: источник %q, ждал %q", want.text, got[i].Note, want.note)
		}
		if got[i].Role != "user" {
			t.Errorf("реплика %q сменила роль на %q", want.text, got[i].Role)
		}
	}
	// Умершая сессия из реестра пропала, и чем она была, сказать нечем: подпись
	// остаётся агентской, а не человеческой.
	gone := parseReplies([]byte(line("devkit-ff", "слова мёртвой сессии")), 0)
	if len(gone) != 1 || gone[0].Who != whoAgent {
		t.Fatalf("реплика умершей сессии подписана %q", gone[0].Who)
	}
}

// Ответы сессии, которая делегирует, подписаны диспетчерскими: боковой журнал
// субагента при транскрипте и есть признак делегирования. Записи самого
// субагента остаются агентскими, они стоят с отступом и заказом, а у сессии без
// субагентов ответы тоже просто агентские (замечание пользователя).
func TestLeadSessionAnswersSignedAsDispatcher(t *testing.T) {
	e := newTestEnv(t)
	at := "2026-08-21T10:00:00.000Z"
	main := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"выполни XR-1"},"timestamp":%q}`, at) + "\n" +
		fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"беру задачу"}]},"timestamp":%q}`, at) + "\n"
	solo := writeSession(t, e.home, e.proj, "", "aaa-1", main, time.Now())
	lead := writeSession(t, e.home, e.proj, "", "bbb-2", main, time.Now())
	writeSubLog(t, lead, "1", "разбор находки", sideLine("нашёл причину", at))

	who := func(path string) []string {
		forgetChunks()
		items := sessionFeedOf(path, 0).items
		var out []string
		for _, it := range items {
			if it.Role != "assistant" {
				continue
			}
			out = append(out, it.Text+"="+it.Who+"/"+it.Sub)
		}
		return out
	}
	// Субагентов нет: делегировать некому, и ответы остаются агентскими.
	if got := who(solo); len(got) != 1 || got[0] != "беру задачу=/" {
		t.Fatalf("сессия без субагентов подписана диспетчером: %v", got)
	}
	// Журнал есть: ответы главной диспетчерские, ответ субагента прежний.
	got := map[string]bool{}
	for _, line := range who(lead) {
		got[line] = true
	}
	if len(got) != 2 {
		t.Fatalf("записей в слитой ленте %d, ждал две: %v", len(got), got)
	}
	if !got["беру задачу="+whoLead+"/"] {
		t.Errorf("ответ главной сессии подписан не диспетчером: %v", got)
	}
	if !got["нашёл причину=/разбор находки"] {
		t.Errorf("ответ субагента подписан не агентом: %v", got)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Приписки заказа видны отдельно от слов человека: подъём сессии приклеивает к
// первой реплике правила (план, ротация, отзывчивость), и в ленте они стояли
// одним пузырём с текстом человека от его имени (живой пример: «Найди черновик
// или задачу...» плюс простыня правил). Пузырь несёт только сказанное,
// приписки едут свёрнутой служебной строкой следом, а неузнанный хвост
// остаётся в пузыре целиком: спрятать кусок реплики человека дороже, чем
// показать служебное.
func TestFeedSplitsOrderRules(t *testing.T) {
	said := "Найди черновик или задачу суть которой в выдаче разрешений агенту."
	full := said + " " + planRule + " " + rotateRule(500000) + " " + paceRule
	line := func(text string) []byte {
		rec := map[string]any{"type": "user",
			"message":   map[string]any{"role": "user", "content": text},
			"timestamp": "2026-08-22T10:00:00.000Z"}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		return append(data, '\n')
	}
	got := parseReplies(line(full), 0)
	if len(got) != 2 {
		t.Fatalf("реплик %d, ждал пузырь и приписки: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Text != said {
		t.Fatalf("пузырь несёт не только слова человека: %+v", got[0])
	}
	if got[1].Role != roleNote || got[1].Note != orderRulesWord {
		t.Fatalf("приписки не стали служебной строкой: %+v", got[1])
	}
	if want := planRule + " " + rotateRule(500000) + " " + paceRule; got[1].Text != want {
		t.Errorf("в приписках не весь заказ: %q", got[1].Text)
	}
	// Резюм приклеивает к реплике человека одно правило отзывчивости: режется
	// и такой хвост.
	pace := parseReplies(line("Продолжай. "+paceRule), 0)
	if len(pace) != 2 || pace[0].Text != "Продолжай." || pace[1].Note != orderRulesWord {
		t.Errorf("хвост правила отзывчивости не отрезан: %+v", pace)
	}
	// Сменившаяся формулировка не режется молча: узнанное начало с незнакомым
	// продолжением остаётся в пузыре целиком.
	odd := said + " " + planRule + " и ещё незнакомый хвост"
	kept := parseReplies(line(odd), 0)
	if len(kept) != 1 || kept[0].Text != odd {
		t.Errorf("неузнанный хвост спрятан: %+v", kept)
	}
	// Порог ротации в чужом заказе другой: правило узнаётся с любым числом.
	other := parseReplies(line(said+" "+planRule+" "+rotateRule(120000)+" "+paceRule), 0)
	if len(other) != 2 || other[0].Text != said {
		t.Errorf("ротация с другим порогом сломала разрез: %+v", other)
	}
}

// Служебный хвост заказа подписан в ленте словами, которые человеку что-то
// говорят: «приписки заказа» не говорили ничего, а это именно инструкции,
// которые дашборд даёт агенту поверх реплики (замечание пользователя).
func TestOrderRulesNoteNamesAgentRules(t *testing.T) {
	items := []reply{}
	addUser(func(r reply) { items = append(items, r) }, "user", "2026-08-24T10:00:00Z",
		"сделай хорошо "+planRule+" "+paceRule, false)
	var note *reply
	for i := range items {
		if items[i].Role == roleNote {
			note = &items[i]
		}
	}
	if note == nil {
		t.Fatalf("служебный хвост не отделился от слов человека: %+v", items)
	}
	if note.Note != "инструкции агента" {
		t.Fatalf("хвост заказа подписан %q, человек читает это как загадку", note.Note)
	}
	if !strings.Contains(note.Text, paceRule) {
		t.Fatalf("под подписью не сами инструкции: %q", note.Text)
	}
}

// Работы сессии видны по машинному следу, а не по дисциплине агента. Кольцо
// показывало план из файла, а файл пишет сам агент правилом заказа: раздал
// работу и не переписал файл, значит кольцо врёт. Так было трижды. Боковые
// журналы субагентов заводит харнес на каждый вызов, и каждый такой журнал это
// работа: подпись у неё из заказа, состояние из того, пишется ли журнал ещё.
func TestPlanTakesWorksFromSubLogs(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	// Ответ вызову субагента в транскрипте: он и говорит, что работа вернулась.
	answered := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":`+
		`[{"type":"tool_result","tool_use_id":"tool-done1","content":"готово"},`+
		`{"type":"tool_result","tool_use_id":"tool-done1b","content":"готово"}]},"timestamp":%q}`,
		now.Add(-time.Minute).Format(time.RFC3339)) + "\n"
	path := writeSession(t, e.home, e.proj, "", "bbb-2", transcriptFixture+answered, now)

	// Журнал брошенной работы: ответа не было, и журнал давно молчит.
	old := writeSubLog(t, path, "old1", "первая ходка",
		sideLine("сделал", now.Add(-2*time.Hour).Format(time.RFC3339)))
	// Журнал идущей работы: ответа вызову нет, а журнал пишется.
	fresh := writeSubLog(t, path, "new1", "вторая ходка",
		sideLine("иду", now.Add(-5*time.Second).Format(time.RFC3339)))
	// Журнал вернувшейся работы: ответ вызову пришёл, и журнал молчит.
	done := writeSubLog(t, path, "done1", "третья ходка",
		sideLine("отчитался", now.Add(-10*time.Minute).Format(time.RFC3339)))
	// Журнал работы, которую продолжили репликой: ответ на первый вызов есть, а
	// работа идёт дальше и пишет. Живой случай: сессия-диспетчер шлёт субагенту
	// новое задание через SendMessage, и по одному ответу такая работа
	// считалась бы закрытой прямо посреди хода.
	again := writeSubLog(t, path, "done1b", "четвёртая ходка",
		sideLine("продолжаю", now.Add(-3*time.Second).Format(time.RFC3339)))
	for at, file := range map[time.Time]string{
		now.Add(-2 * time.Hour):    old,
		now.Add(-5 * time.Second):  fresh,
		now.Add(-10 * time.Minute): done,
		now.Add(-3 * time.Second):  again,
	} {
		if err := os.Chtimes(file, at, at); err != nil {
			t.Fatal(err)
		}
	}

	// Файла плана нет вовсе, и это не повод показывать пустоту.
	plan := planOf(e.home, "bbb-2", "", path, now)
	if len(plan) != 4 {
		t.Fatalf("без файла плана работ из журналов %d, ждал четыре: %+v", len(plan), plan)
	}
	byText := map[string]string{}
	for _, it := range plan {
		byText[it.Text] = it.State
	}
	if byText["первая ходка"] != "completed" {
		t.Errorf("брошенная работа названа состоянием %q, ждал completed: %+v", byText["первая ходка"], plan)
	}
	if byText["вторая ходка"] != "in_progress" {
		t.Errorf("идущая работа названа состоянием %q, ждал in_progress: %+v", byText["вторая ходка"], plan)
	}
	// Свежий журнал сам по себе работы не продлевает: ответ вызову пришёл,
	// значит она вернулась, чем бы её журнал ни занимался после.
	if byText["третья ходка"] != "completed" {
		t.Errorf("вернувшаяся работа названа состоянием %q, ждал completed: %+v", byText["третья ходка"], plan)
	}
	if byText["четвёртая ходка"] != "in_progress" {
		t.Errorf("продолженная репликой работа названа состоянием %q, ждал in_progress: %+v",
			byText["четвёртая ходка"], plan)
	}

	// Файл плана есть, но про вторую ходку в нём не написано: работа из журнала
	// от этого не теряется, а встаёт наравне с пунктами.
	if err := os.MkdirAll(planDir(e.home), 0o755); err != nil {
		t.Fatal(err)
	}
	file := planPath(e.home, "bbb-2")
	if err := os.WriteFile(file, []byte(`[{"text":"первая ходка","state":"pending"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	plan = planOf(e.home, "bbb-2", "", path, now)
	if len(plan) != 4 {
		t.Fatalf("работ в кольце %d, ждал четыре (пункт плана и три работы мимо него): %+v", len(plan), plan)
	}
	// Место пункту в списке даёт теперь состояние, а не источник: ждущее стоит
	// последним, чей бы это пункт ни был (TestPlanOrderedByState). Важно тут
	// одно, что пункт плана не потерялся и остался ждущим.
	if plan[len(plan)-1].Text != "первая ходка" || plan[len(plan)-1].State != "pending" {
		t.Errorf("ждущий пункт плана встал не последним: %+v", plan)
	}
	byText = map[string]string{}
	for _, it := range plan {
		byText[it.Text] = it.State
	}
	if byText["вторая ходка"] != "in_progress" {
		t.Errorf("работа мимо плана потеряна или без состояния: %+v", plan)
	}
}

// sideOrder это первая реплика бокового журнала: заказ, который диспетчер
// написал субагенту. Признак isSidechain тут тот же, что у настоящего журнала.
func sideOrder(text, at string) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":true,"message":{"role":"user","content":%q},"timestamp":%q}`,
		text, at) + "\n"
}

// Работа из бокового журнала подписывается заказом. Короткий заказ пишет
// мета-файл вызова, а у вызова без него подписью оставалось служебное имя
// определения («claude», «Explore»), и список работ не читался вовсе
// (замечание пользователя). Тогда подпись берётся из самого журнала: первой
// содержательной строкой заказа, без служебных приписок и обрезанной по ширине
// строки.
func TestSubWorkLabelFromOrder(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	path := writeSession(t, e.home, e.proj, "", "bbb-2", transcriptFixture, now)

	// Заказ с приписками, которые дашборд клеит к тексту человека: в подпись
	// им не место, режет их тот же разбор, что и в ленте.
	// Пустая строка и маркер списка это разметка, а не слова: подпись начинается
	// с первой содержательной строки.
	order := "\n- Ссылка на черновик и переход с телефона\n\nДальше подробности. " +
		planRuleFor("bbb-2") + " " + paceRule
	plain := writeSubLog(t, path, "ord1", "", sideOrder(order, at)+sideLine("иду", at))
	// Длинный заказ режется по ширине строки, а не уезжает в кольцо целиком.
	long := strings.Repeat("очень длинная строка заказа ", 6)
	wide := writeSubLog(t, path, "ord2", "", sideOrder(long, at)+sideLine("иду", at))
	// У вызова с заказом мета-файла подпись остаётся его: она короче и читается
	// лучше всего.
	short := writeSubLog(t, path, "ord3", "Закрытие сессий",
		sideOrder("Тут длинная вводная про то, как всё устроено", at)+sideLine("иду", at))
	for _, file := range []string{plain, wide, short} {
		if err := os.Chtimes(file, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	texts := []string{}
	for _, it := range planOf(e.home, "bbb-2", "", path, now) {
		texts = append(texts, it.Text)
	}
	joined := strings.Join(texts, " | ")
	if !slices.Contains(texts, "Ссылка на черновик и переход с телефона") {
		t.Errorf("подпись работы взята не из заказа: %s", joined)
	}
	for _, gone := range []string{"claude", "Дальше подробности", "план работ", "Долгие дела"} {
		if strings.Contains(joined, gone) {
			t.Errorf("в подписи работы осталось служебное (%q): %s", gone, joined)
		}
	}
	if !slices.Contains(texts, "Закрытие сессий") {
		t.Errorf("короткий заказ мета-файла пропал из подписи: %s", joined)
	}
	cut := ""
	for _, text := range texts {
		if strings.HasPrefix(text, "очень длинная строка") {
			cut = text
		}
	}
	if cut == "" {
		t.Fatalf("длинный заказ пропал из работ: %s", joined)
	}
	// Ширина строки тут числом нарочно: стенд сверяет её с тем, что видит
	// человек, а не с той же константой, которой её и режут (subOrderLimit).
	if !strings.HasSuffix(cut, "...") || len([]rune(cut)) != 73 {
		t.Errorf("длинный заказ не обрезан по ширине строки: %q (%d знаков)", cut, len([]rune(cut)))
	}
}

// Живой опрос таба сессий спрашивает работы своей ручкой и по кругу, поэтому
// разбор хвоста транскрипта («идёт ли ход») держится в памяти под отпечатком
// файла: без памяти каждый заход перечитывал бы хвосты всех сессий машины, а
// это ровно те тормоза, которые лечили у ленты и кольца. Память сторожит
// отпечаток, а не срок: файл двинулся, значит разбор повторяется.
func TestSessionBusyRemembersByStamp(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	fresh := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":`+
		`[{"type":"text","text":"иду"}]},"timestamp":%q}`, now.Add(-time.Second).Format(time.RFC3339)) + "\n"
	path := filepath.Join(t.TempDir(), "live.jsonl")
	if err := os.WriteFile(path, []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	if !e.s.sessionBusy(path, now) {
		t.Fatalf("свежий транскрипт не признан идущим ходом")
	}

	// Содержимое подменено, а отпечаток тот же (время правки и размер не
	// двинулись): ответ идёт из памяти, файл второй раз не читается.
	stale := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":`+
		`[{"type":"text","text":"жду"}]},"timestamp":%q}`, now.Add(-time.Hour).Format(time.RFC3339)) + "\n"
	if len(stale) != len(fresh) {
		t.Fatalf("подмена не того же размера: %d против %d", len(stale), len(fresh))
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if !e.s.sessionBusy(path, now) {
		t.Errorf("память по отпечатку не сработала: хвост перечитан при неизменившемся файле")
	}

	// Файл двинулся: разбор повторяется, и ход больше не идёт.
	later := now.Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if e.s.sessionBusy(path, now) {
		t.Errorf("двинувшийся файл не перечитан: ход считается идущим по старой памяти")
	}

	// Ход часов память не рушит, но и не врёт: тот же разбор через час это уже
	// не работа, потому что решение считается из времени последней записи.
	if e.s.sessionBusy(path, now.Add(time.Hour)) {
		t.Errorf("память выдала работу спустя час после последней записи")
	}
}

// Работы проекта едут своей ручкой: живой опрос таба сессий спрашивает её по
// кругу, и общий список проектов ради этого опрашивал бы все доски машины
// разом.
func TestWorksHandle(t *testing.T) {
	e, c, _ := runsEnv(t, "task-XR-002\t1\t1786000000\n")
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/works", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("работы проекта: %d %s", resp.StatusCode, text)
	}
	var got struct {
		Project string `json:"project"`
		Works   []Work `json:"works"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v\n%s", err, text)
	}
	if got.Project != "demo" {
		t.Errorf("ручка назвала проект %q", got.Project)
	}
	w := workByID(got.Works, "XR-002")
	if w == nil {
		t.Fatalf("работы XR-002 в ответе нет: %s", text)
	}
	// Состояние едет тем же полем, что и в списке проектов: экран сортирует
	// строки по нему, и второго вида ответа тут быть не должно. Проверяется
	// каждая работа, а не одна: пустое состояние у части работ клиент рисовал
	// зелёным умолчанием, и молчащий разговор выходил работающим (замечание
	// пользователя).
	for _, one := range got.Works {
		if one.Live == "" {
			t.Errorf("работа %q приехала без состояния: %s", one.ID+one.Session, text)
		}
	}
	if w.Live == "" {
		t.Errorf("работа приехала без состояния: %s", text)
	}
	if resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/нетаких/works", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("работы неизвестного проекта: %d, ожидал 404", resp.StatusCode)
	}
}

// Кольцо стоит замороженным, пока метка боковых журналов не двинулась, и метки
// по каталогу не хватало: новый субагент каталог трогает, а ход уже начатой
// работы нет. Работа кончалась, сегмент обязан был позеленеть, а план не
// пересылался до следующего субагента (жалоба пользователя, третий заход к
// одной теме). Теперь в метку идёт и время последней записи в самих журналах.
func TestSubStampMovesOnJournalWrite(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, now)
	log := writeSubLog(t, path, "one", "первая ходка", sideLine("иду", now.Format(time.RFC3339)))
	// Время морозится у всего каталога, а не у одного журнала: рядом лежит
	// мета-файл вызова, и его настоящее время правки идёт в метку наравне с
	// журналом. Без этого стенд зеленел только до того часа суток, когда живые
	// часы машины перегоняли синтетический now, а дальше падал на ровном месте.
	dir := subDir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(log, now, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, now, now); err != nil {
		t.Fatal(err)
	}
	was := subStamp(path)
	if was == "" {
		t.Fatal("метки боковых журналов нет вовсе")
	}

	// Субагент дописал свой журнал: каталог тот же, файл тот же, а метка
	// обязана двинуться, иначе кольцо не узнает о ходе работы.
	later := now.Add(time.Minute)
	if err := os.Chtimes(log, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, now, now); err != nil {
		t.Fatal(err)
	}
	if got := subStamp(path); got == was {
		t.Errorf("дописанный журнал метку не двинул: %q", got)
	}

	// Новый субагент двигает её тем же порядком, как и раньше.
	moved := subStamp(path)
	writeSubLog(t, path, "two", "вторая ходка", sideLine("иду", later.Format(time.RFC3339)))
	if got := subStamp(path); got == moved {
		t.Errorf("новый журнал метку не двинул: %q", got)
	}
}

// Работа субагента в кольце помечена источником: в файле плана такого пункта
// нет вовсе, он собран по боковому журналу розданной работы. Без пометки
// человек, сверяющий кольцо с ~/.devkit/plans, читает эти пункты чужим планом
// (живой разбор сессии devkit-2e: кольцо «8 из 9», а в файле шесть закрытых
// пунктов и ни одного открытого).
func TestPlanMarksSubagentWorks(t *testing.T) {
	own := []planItem{{Text: "Разбор", State: "completed"}}
	subs := []subWork{{item: planItem{Text: "Ревью DK-520", State: "in_progress"}, alias: "review-high"}}
	got := withSubWorks(own, subs)
	if len(got) != 2 {
		t.Fatalf("работа субагента не встала в план: %+v", got)
	}
	if got[0].Src != "" {
		t.Errorf("пункт самого плана помечен источником: %+v", got[0])
	}
	if got[1].Src != planSrcSub {
		t.Errorf("работа субагента не помечена источником: %+v", got[1])
	}
}

// План и этапы кольца принадлежат ровно своей сессии: ни один пункт чужой в её
// кольцо не попадает ни при каких совпадениях имён. Имя tmux-сессии дашборд
// переиспользует (chat-1, task-DK-100), и файл плана по такому имени вполне
// бывает чужим, оставшимся от прошлого жильца имени. Живой случай: у сессии
// devkit-2e кольцо показывало «8 из 9» с чужим на вид незакрытым пунктом, и
// первым подозрением было именно совпадение имени.
func TestPlanKeepsToItsOwnSession(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mine := writeSession(t, home, proj, "", "aaa-своя", transcriptFixture, time.Now())
	if err := os.MkdirAll(planDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	// Чужой план под тем же именем tmux: он написан до того, как началась наша
	// сессия, то есть её планом быть не может.
	stale := planPath(home, "chat-семь")
	if err := os.WriteFile(stale, []byte(`[{"text":"Ревью, слияние, закрытие","state":"in_progress"},`+
		`{"text":"Полный прогон тестов","state":"completed"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	// Свой файл по sid: всё закрыто, незакрытых пунктов у сессии нет.
	if err := os.WriteFile(planPath(home, "aaa-своя"),
		[]byte(`[{"text":"Разбор","state":"completed"},{"text":"Правка","state":"completed"}]`),
		0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.Local)
	plan := planOf(home, "aaa-своя", "chat-семь", mine, now)
	for _, it := range plan {
		if strings.Contains(it.Text, "Ревью, слияние") {
			t.Fatalf("в кольцо своей сессии попал пункт чужой: %+v", plan)
		}
		if it.State != "completed" {
			t.Errorf("у сессии с закрытым планом висит незакрытый пункт: %+v", it)
		}
	}

	// Своего файла не стало (сессия второй подписки не знает своего ID): чужой
	// файл под тем же именем ей всё равно не достаётся.
	if err := os.Remove(planPath(home, "aaa-своя")); err != nil {
		t.Fatal(err)
	}
	for _, it := range planOf(home, "aaa-своя", "chat-семь", mine, now) {
		if strings.Contains(it.Text, "Ревью, слияние") {
			t.Fatalf("чужой план по имени tmux достался сессии: %+v", it)
		}
	}

	// А свой файл по имени tmux достаётся: он написан после начала сессии, и
	// запасной адрес правила плана работает, как работал.
	if err := os.WriteFile(stale, []byte(`[{"text":"Своя работа","state":"in_progress"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := time.Date(2026, 8, 10, 10, 30, 0, 0, time.UTC)
	if err := os.Chtimes(stale, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	got := planOf(home, "aaa-своя", "chat-семь", mine, now)
	if len(got) == 0 || got[0].Text != "Своя работа" {
		t.Fatalf("свой план по имени tmux потерялся: %+v", got)
	}
}

// Порядок работ в кольце. Прежде он собирался из двух кусков подряд: сперва
// план в том виде, в каком его написал агент, следом работы из журналов,
// которых в плане не нашлось. Закрытое, идущее и ждущее стояли вперемешку, и
// читать по кольцу ход работы было нечем («работы в кольце выстроены как
// зря», замечание пользователя). Теперь сверху закрытое, под ним идущее и
// ждущее, а внутри каждой группы прежний порядок: пункты плана в порядке
// письма, работы журналов в порядке своего начала.
func TestPlanOrderedByState(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mine := writeSession(t, home, proj, "", "ppp-порядок", transcriptFixture, time.Now())
	if err := os.MkdirAll(planDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath(home, "ppp-порядок"), []byte(`[
		{"text":"Первый разбор","state":"completed"},
		{"text":"Правка кода","state":"in_progress"},
		{"text":"Второй прогон","state":"completed"},
		{"text":"Дока","state":"pending"},
		{"text":"Тесты","state":"completed"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Работа из бокового журнала, которой в плане нет: журнал давно молчит,
	// значит работа закрыта, и место ей в верхней группе, а не в хвосте списка.
	old := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	log := writeSubLog(t, mine, "one", "Ревью правки", sideLine("готово", old.Format(time.RFC3339)))
	if err := os.Chtimes(log, old, old); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	var got []string
	for _, it := range planOf(home, "ppp-порядок", "", mine, now) {
		got = append(got, it.Text+"/"+it.State)
	}
	want := []string{
		"Первый разбор/completed",
		"Второй прогон/completed",
		"Тесты/completed",
		"Ревью правки/completed",
		"Правка кода/in_progress",
		"Дока/pending",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("порядок работ в кольце\n получен %v\n жду     %v", got, want)
	}
}

// План-объект. Правило велит писать план массивом пунктов, но живые агенты
// пишут и объектом: пункты полем stages, steps или items, а рядом собственные
// пометки про цель, виток и ветку. Разбор такой файл ронял целиком, кольцо
// вставало пустым, и человек видел пустоту у сессии, где этапность есть
// («кружок этапов не установился», жалоба пользователя по цели XR-286).
func TestPlanFileReadsObjectShape(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
		want []planItem
	}{
		{
			name: "stages",
			body: `{"goal":"XR-286","turn":1,"stages":[` +
				`{"text":"Состояние цели","state":"completed"},` +
				`{"text":"Нарезка","state":"in_progress"}]}`,
			want: []planItem{
				{Text: "Состояние цели", State: "completed"},
				{Text: "Нарезка", State: "in_progress"},
			},
		},
		{
			name: "steps",
			body: `{"task":"DK-397","tree":"poc","steps":[` +
				`{"id":1,"what":"разбор","state":"done"},` +
				`{"id":2,"what":"правка","state":"pending"}]}`,
			want: []planItem{
				{Text: "разбор", State: "completed"},
				{Text: "правка", State: "pending"},
			},
		},
		{
			name: "items",
			body: `{"title":"шесть работ","items":[` +
				`{"id":1,"text":"колонка действий","status":"in_progress"}]}`,
			want: []planItem{{Text: "колонка действий", State: "in_progress"}},
		},
		{
			name: "массив как прежде",
			body: `[{"text":"своя работа","state":"in_progress"}]`,
			want: []planItem{{Text: "своя работа", State: "in_progress"}},
		},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name+".json")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, at, bad := readPlanFile(path)
		if bad {
			t.Errorf("%s: файл прочитан, а разбор жалуется", c.name)
		}
		if at.IsZero() {
			t.Errorf("%s: у разобранного плана нет метки времени", c.name)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: пунктов %d, ждали %d: %+v", c.name, len(got), len(c.want), got)
		}
		for i, w := range c.want {
			if got[i].Text != w.Text || got[i].State != w.State {
				t.Errorf("%s: пункт %d это %+v, ждали %+v", c.name, i, got[i], w)
			}
		}
	}
}

// Нечитаемый файл плана говорит о себе. Прежде ошибка разбора возвращалась тем
// же пустым планом, что и отсутствие файла, и молчание было неотличимо от
// штатной работы: человек не мог понять, план у сессии не написан или написан
// так, что дашборд его не взял.
func TestPlanFileTellsAboutBrokenFile(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	sid := "ppp-битый-план"
	path := writeSession(t, home, proj, "", sid, transcriptFixture, time.Now())
	if err := os.MkdirAll(planDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := planPath(home, sid)
	// Объект без известного поля с пунктами: JSON целый, плана в нём нет.
	if err := os.WriteFile(broken, []byte(`{"task":"DK-397","scope":["static/app.js"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, bad := readPlanFile(broken); !bad {
		t.Fatal("нечитаемый файл плана прошёл как отсутствие плана")
	}
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.Local)
	got := planOf(home, sid, "", path, now)
	hit := false
	for _, it := range got {
		if it.Src == planSrcErr && strings.Contains(it.Text, filepath.Base(broken)) {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("про нечитаемый план в кольце не сказано ничего: %+v", got)
	}

	// Файла нет вовсе: жаловаться не на что, кольцо молчит, как молчало.
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	for _, it := range planOf(home, sid, "", path, now) {
		if it.Src == planSrcErr {
			t.Fatalf("жалоба на разбор у сессии без файла плана: %+v", it)
		}
	}
}

// markEnded дописывает в мету работы время возврата, как его пишет agentctl
// run, когда подпроцесс делегирования кончился.
func markEnded(t *testing.T, path, id string, at time.Time) {
	t.Helper()
	meta := filepath.Join(subDir(path), "agent-"+id+".meta.json")
	body := fmt.Sprintf(`{"agentType":"exec-high","description":%q,"toolUseId":%q,"ended":%q}`,
		"DK-577, исполнитель на подписке glm-code", "tool-"+id, at.UTC().Format(time.RFC3339))
	if err := os.WriteFile(meta, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Работа второй подписки закрывается меткой возврата в мете. Ответа на её
// вызов в транскрипте разговора нет вовсе (вызывал её не харнес, а run), и без
// метки свежий журнал держал бы кончившуюся работу идущей ещё полчаса
// (DK-581).
func TestSubWorkEndedClosesTheWork(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	path := writeSession(t, e.home, e.proj, "", "bbb-2", transcriptFixture, now)

	live := writeSubLog(t, path, "run1", "DK-581, исполнитель на подписке glm-code",
		sideLine("иду", at))
	done := writeSubLog(t, path, "run2", "DK-577, исполнитель на подписке glm-code",
		sideLine("иду", at))
	markEnded(t, path, "run2", now.Add(-time.Minute))
	// Журналы у обеих работ свежие: разводит их только метка возврата.
	for _, file := range []string{live, done} {
		if err := os.Chtimes(file, now.Add(-30*time.Second), now.Add(-30*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	states := map[string]string{}
	for _, it := range planOf(e.home, "bbb-2", "", path, now) {
		states[strings.SplitN(it.Text, ",", 2)[0]] = it.State
	}
	if states["DK-581"] != "in_progress" {
		t.Errorf("идущая работа не идёт: %v", states)
	}
	if states["DK-577"] != "completed" {
		t.Errorf("вернувшаяся работа осталась идущей: %v", states)
	}
}

// Пересказ съеденного начала разговора не выдаётся за реплику человека. Харнес
// кладёт его записью роли user с пометкой isCompactSummary, и лента честно
// рисовала это пузырём человека: несколько тысяч слов по-английски, будто их
// написал человек (замечание пользователя по чату «Выполнение XR-279»).
// Узнаётся запись по пометке, а не по первым словам: формулировку харнес
// меняет, а человек может её процитировать.
func TestCompactSummaryNotHumanReply(t *testing.T) {
	head := "This session is being continued from a previous conversation that ran " +
		"out of context. The summary below covers the earlier portion of the " +
		"conversation.\nSummary: разобрал очередь слияния и снял два замечания."
	line := fmt.Sprintf(
		`{"type":"user","isCompactSummary":true,"isVisibleInTranscriptOnly":true,`+
			`"message":{"role":"user","content":%q},"timestamp":%q,"gitBranch":"main"}`,
		head, "2026-07-30T10:45:41Z") + "\n"
	got := parseRepliesSpan([]byte(line), 0, parseSpan{src: "s"})
	if len(got) != 1 {
		t.Fatalf("записей в ленте %d, ждал одну: %+v", len(got), got)
	}
	if got[0].Role != roleNote {
		t.Fatalf("пересказ выдан за реплику человека: role=%q", got[0].Role)
	}
	if got[0].Mark != compactMark {
		t.Fatalf("пересказ без машинной пометки: mark=%q", got[0].Mark)
	}
	if got[0].Note != compactWord {
		t.Fatalf("заголовок пересказа не назван словами: note=%q", got[0].Note)
	}
	if !strings.Contains(got[0].Text, "разобрал очередь слияния") {
		t.Fatalf("тело пересказа потеряно: %q", got[0].Text)
	}
	// Слова заголовка про дело человека, а не про устройство харнеса.
	for _, own := range []string{"контекст", "токен", "compact", "summary"} {
		if strings.Contains(strings.ToLower(compactWord), own) {
			t.Fatalf("заголовок говорит устройством харнеса (%q): %q", own, compactWord)
		}
	}
}

// Служебная строка харнеса, подставленная от имени человека, тоже не пузырь.
// «Continue from where you left off» человек не писал, и пометка isMeta говорит
// об этом прямо.
func TestMetaLineNotHumanReply(t *testing.T) {
	line := fmt.Sprintf(
		`{"type":"user","isMeta":true,"message":{"role":"user","content":%q},`+
			`"timestamp":%q,"gitBranch":"main"}`,
		"Continue from where you left off.", "2026-07-30T10:46:00Z") + "\n"
	got := parseRepliesSpan([]byte(line), 0, parseSpan{src: "s"})
	if len(got) != 1 {
		t.Fatalf("записей в ленте %d, ждал одну: %+v", len(got), got)
	}
	if got[0].Role != roleNote {
		t.Fatalf("строка харнеса выдана за реплику человека: role=%q", got[0].Role)
	}
}

// Планы субагентов одной сессии видны все. CLAUDE_CODE_SESSION_ID у субагентов
// пачки общий, и по адресу <sid>-sub.json два исполнителя писали план поверх
// соседского: у DK-641 и DK-642 ход работы снаружи пропал вовсе (DK-527).
// Теперь адрес несёт метку субагента, а кольцо собирается по маске.
func TestPlanShowsEachSubagentPlan(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	sid := "aaa-пачка"
	path := writeSession(t, home, proj, "", "chat-пачка", transcriptFixture, time.Now())
	if err := os.MkdirAll(planDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(planDir(home), name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(sid+".json", `[{"text":"Пачка задач","state":"in_progress"}]`)
	write(sid+"-sub-DK-641.json", `[{"text":"Правка кольца","state":"in_progress"}]`)
	write(sid+"-sub-DK-642.json", `[{"text":"Правка ленты","state":"completed"}]`)
	// Старый адрес без метки читается по-прежнему: план, написанный до правила,
	// с экрана не пропадает.
	write(sid+"-sub.json", `[{"text":"Работа без метки","state":"pending"}]`)
	// Сосед по маске, чей план про другое, в кольцо не попадает.
	write(sid+"-submit.json", `[{"text":"Чужая запись","state":"pending"}]`)

	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.Local)
	plan := planOf(home, sid, "", path, now)
	seen := map[string]planItem{}
	for _, it := range plan {
		seen[it.Text] = it
	}
	for _, want := range []string{"DK-641: Правка кольца", "DK-642: Правка ленты", "Работа без метки"} {
		it, ok := seen[want]
		if !ok {
			t.Fatalf("плана субагента %q в кольце нет: %+v", want, plan)
		}
		if it.Src != planSrcSub {
			t.Errorf("пункт субагента %q не помечен источником: %+v", want, it)
		}
	}
	if seen["DK-641: Правка кольца"].State != "in_progress" {
		t.Errorf("состояние пункта субагента потерялось: %+v", seen["DK-641: Правка кольца"])
	}
	if _, bad := seen["Чужая запись"]; bad {
		t.Errorf("под маску планов субагентов попал сосед: %+v", plan)
	}
	if it, ok := seen["Пачка задач"]; !ok || it.Src != "" {
		t.Errorf("план самой сессии потерялся или помечен субагентом: %+v", plan)
	}
}

// Метка потока считает и планы субагентов: исполнитель пачки пишет свой файл, а
// транскрипт диспетчера при этом молчит, и без счёта правка доезжала бы до
// экрана только следующим тиком.
func TestPlanStampWatchesSubagentPlans(t *testing.T) {
	home := t.TempDir()
	sid := "bbb-пачка"
	if err := os.MkdirAll(planDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(planDir(home), sid+"-sub-DK-700.json")
	if err := os.WriteFile(file, []byte(`[{"text":"Шаг","state":"in_progress"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	was := planStamp(home, sid, "")
	later := time.Date(2026, 9, 2, 13, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if now := planStamp(home, sid, ""); now == was {
		t.Errorf("правка плана субагента метку не двинула: %q", now)
	}
}

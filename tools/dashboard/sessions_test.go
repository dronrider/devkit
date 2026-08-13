package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

var transcriptWant = []reply{
	{Seq: 0, Role: "user", Time: "2026-08-10T10:00:01.000Z", Text: "возьми задачу XR-005 в работу"},
	{Seq: 1, Role: "thinking", Time: "2026-08-10T10:00:02.000Z"},
	{Seq: 2, Role: "assistant", Time: "2026-08-10T10:00:03.000Z", Text: "Беру XR-005, смотрю доску."},
	{Seq: 3, Role: "tool", Time: "2026-08-10T10:00:03.000Z", Tool: "Bash", Note: "taskctl list | head -5"},
	{Seq: 4, Role: "assistant", Time: "2026-08-10T10:00:06.000Z", Text: "Доска прочитана."},
}

// writeSession кладёт транскрипт в каталог ~/.claude/projects по раскладке
// Claude Code; dirSuffix изображает каталог бокового дерева задачи.
func writeSession(t *testing.T, home, projPath, dirSuffix, sid, content string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudeDirName(projPath)+dirSuffix)
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

// Список сессий: свежие сверху по mtime, ветка и первая человеческая реплика
// из шапки, служебная вставка в угловых скобках репликой не считается,
// сессия из каталога бокового дерева попадает в список.
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
	want := []sessionInfo{
		{ID: "bbb-2", Mtime: "2026-08-10T12:00:00Z", Branch: "dk-219", First: "Выполни XR-007",
			Task: "DK-5", TaskNote: "по дереву задачи"},
		{ID: "aaa-1", Mtime: "2026-08-10T09:00:00Z", Branch: "main", First: "возьми задачу XR-005 в работу",
			Task: "XR-005", TaskNote: "по первой реплике"},
	}
	if !reflect.DeepEqual(got.Sessions, want) {
		t.Errorf("список сессий:\n%+v\nожидал:\n%+v", got.Sessions, want)
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
// его. Задача узнаётся первой репликой в главном дереве и именем бокового
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
	c := e.loggedClient(t)

	text, list, _ := getSessions(t, e, c, "?task=XR-101")
	want := []sessionInfo{{ID: "aaa-1", Mtime: "2026-08-10T09:00:00Z", Branch: "main",
		First: "возьми задачу XR-101 в работу и доведи её конвейером",
		Task:  "XR-101", TaskNote: "по первой реплике"}}
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
	path := writeSession(t, e.home, e.proj, "", "aaa-1",
		sessionLine("возьми задачу XR-101 в работу", "main"), stamped)
	c := e.loggedClient(t)
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].Task != "XR-101" {
		t.Fatalf("первое чтение шапки: %+v", list)
	}

	same := sessionLine("возьми задачу XR-102 в работу", "main")
	if err := os.WriteFile(path, []byte(same), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamped, stamped); err != nil {
		t.Fatal(err)
	}
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].Task != "XR-101" {
		t.Errorf("шапка перечитана при том же отпечатке: %+v", list)
	}

	moved := stamped.Add(time.Minute)
	if err := os.Chtimes(path, moved, moved); err != nil {
		t.Fatal(err)
	}
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].Task != "XR-102" {
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
	path := writeSession(t, e.home, e.proj, "", "aaa-1",
		bigTranscript("возьми задачу XR-101 в работу"), stamped)
	c := e.loggedClient(t)
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].Task != "XR-101" {
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
	if _, list, _ := getSessions(t, e, c, ""); len(list) != 1 || list[0].Task != "XR-101" {
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

// Экран задачи ведёт к разговору агента и тогда, когда работа кончилась, а
// разговоров у задачи бывает несколько. Предмет проверки это набор кнопок и
// собранная лента, а не написанное в исходнике, поэтому статика поднимается в
// node с заглушкой DOM (стенд testdata/task_sessions.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestTaskScreenOpensAgentTalk(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд перехода к разговору пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "task_sessions.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("переход к разговору с экрана задачи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// cssDisplay считает, каким показом кончает элемент класса cls: правила той же
// специфичности перебиваются по порядку, и действует последнее, а не первое.
// Правила приходят разобранными (cssRules) и отобранными по подлежащему
// (cssSubject): у «.seg div» подлежащее это div, и к самому переключателю
// правило не относится.
func cssDisplay(rules [][2]string, cls string) string {
	out := ""
	for _, rule := range rules {
		if !cssSubject(rule[0], cls) {
			continue
		}
		for _, decl := range strings.Split(rule[1], ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(decl), "display:"); ok {
				out = strings.TrimSpace(v)
			}
		}
	}
	return out
}

// Переключатель разговоров задачи виден на обеих ширинах, а переключатель
// панелей остаётся телефонным. Первым заходом разговоры собрались классом
// телефонных панелей, и на ноутбуке экран их не показывал: у того класса
// display:none по умолчанию, а display:flex он получает только внутри
// медиазапроса телефона (замечание ревью DK-280). Заглушка DOM такое не
// ловит, каскада она не считает, поэтому предмет проверки тут сами правила:
// чем переключатель разговоров собран в статике и каким показом класс кончает
// по умолчанию и на телефоне. Разбор идёт тем же cssRules, каким по DK-284
// ловится спор hidden с display: считать каскад дважды незачем, и своя
// половина разбора отдавала первое правило вместо последнего.
func TestStaticTranscriptSegVisible(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(funcBody(t, app, "async function wireTranscript("), `el("div", "tseg")`) {
		t.Fatal("переключатель разговоров собран не классом tseg: класс телефонных панелей " +
			"прячет его на ноутбуке")
	}
	if !strings.Contains(funcBody(t, app, "function renderAgent("), `el("div", "seg")`) {
		t.Error("переключатель панелей собран не классом seg: телефонные вкладки остались без стилей")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	narrow := funcBody(t, css, "@media (max-width:900px){")
	wide := cssRules(strings.Replace(css, narrow, "", 1))
	// На телефоне медиазапрос ложится поверх правил по умолчанию, поэтому
	// считается он вместе с ними, а не отдельно.
	phone := append(append([][2]string{}, wide...), cssRules(narrow)...)

	// Разговоры переключают на обеих ширинах: по умолчанию показ стоит, и
	// телефонный медиазапрос его не отнимает.
	if got := cssDisplay(wide, "tseg"); got != "flex" {
		t.Errorf("на ноутбуке переключатель разговоров кончает показом %q: соседний разговор "+
			"задачи открыть нечем, хотя в разметке он есть", got)
	}
	if got := cssDisplay(phone, "tseg"); got == "none" {
		t.Error("на телефоне переключатель разговоров спрятан")
	}
	// Панели переключают только на телефоне, и это остаётся как было: на
	// ноутбуке они стоят рядом, и вкладки там лишние.
	if got := cssDisplay(wide, "seg"); got != "none" {
		t.Errorf("на ноутбуке вкладки панелей кончают показом %q: панели там стоят рядом", got)
	}
	if got := cssDisplay(phone, "seg"); got != "flex" {
		t.Errorf("на телефоне вкладки панелей кончают показом %q", got)
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

// Экраны агента и переписки обязаны спрашивать сессии своей задачи и
// показывать подпись сессии: держится грепом по статике, как слова про паузу
// в тесте стопа.
func TestStaticSessionsAskedByTask(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if strings.Count(text, `"/sessions?task="`) < 2 {
		t.Error("в static/app.js меньше двух запросов сессий с ?task=: экран возьмёт чужую сессию по mtime")
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

// Пагинация назад: ?n= режет хвост, ?before= отдаёт реплики до курсора, до
// начала ленты доходит без дыр.
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
	if total != 5 || !reflect.DeepEqual(items, transcriptWant[3:]) {
		t.Fatalf("хвост n=2: total=%d %+v", total, items)
	}
	if _, items = page("n=2&before=3"); !reflect.DeepEqual(items, transcriptWant[1:3]) {
		t.Fatalf("страница before=3: %+v", items)
	}
	if _, items = page("n=2&before=1"); !reflect.DeepEqual(items, transcriptWant[:1]) {
		t.Fatalf("страница before=1: %+v", items)
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
// склейка sessionPath отдала бы чужой файл с диска (замечание ревью DK-219).
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
	want := reply{Seq: 0, Role: "assistant", Time: "2026-08-10T10:00:07.000Z", Text: "Готово."}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("дострение после note: %+v, ожидал %+v", item, want)
	}
}

// Экран транскрипта обязан слушать событие note и гасить кнопку «раньше»,
// когда раньше нечего показывать: держится грепом по статике, как слово
// «пауза» в тесте стопа. note слушают обе панели, журнал и транскрипт.
func TestStaticTranscriptEmptyHandled(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if strings.Count(text, `addEventListener("note"`) < 2 {
		t.Error("в static/app.js меньше двух слушателей note: пустой транскрипт останется пустой коробкой")
	}
	if !strings.Contains(text, "more.hidden") {
		t.Error("в static/app.js кнопка «раньше» не гаснет: мёртвая кнопка на пустой ленте")
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
	want := reply{Seq: 5, Role: "assistant", Time: "2026-08-10T10:00:07.000Z", Text: "Готово."}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("живое дострение: %+v, ожидал %+v", item, want)
	}
}

// Транскрипт держит взгляд теми же якорями, что чат: дострение прокручивает
// вниз, только когда лента и так стоит внизу, а догрузка истории возвращает
// прежнее место, а не бросает к последней реплике.
func TestStaticTranscriptKeepsPlace(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "function openTranscript(")
	for _, want := range []string{"const was = atBottom(tp.body)", "keepBottom(tp.body, was)", "keepPlace(tp.body, tail)"} {
		if !strings.Contains(body, want) {
			t.Errorf("в транскрипте нет %q: прокрутка сорвётся при дострении", want)
		}
	}
	if strings.Contains(body, "tp.body.scrollTop = tp.body.scrollHeight") {
		t.Error("транскрипт прокручивается вниз мимо якоря")
	}
}

// Транскрипт без ID задачи в шапке: окно человека, о котором известно только
// то, что оно работает.
const plainSessionFixture = `{"type":"user","message":{"role":"user","content":"поправь вёрстку карточки"},"timestamp":"2026-08-11T11:59:00.000Z","gitBranch":"main"}
`

// Транскрипт, чья задача узнаётся веткой чужой доски.
const foreignSessionFixture = `{"type":"user","message":{"role":"user","content":"поправь вёрстку карточки"},"timestamp":"2026-08-11T11:59:00.000Z","gitBranch":"ab-9"}
`

// Интерактивные сессии видны живыми работами: свежий транскрипт даёт работу,
// протухший по порогу не даёт, нераспознанная задача остаётся в списке с
// подписью, вид работы берётся со строки доски, а сессия задачи, у которой уже
// идёт tmux-работа, второй карточкой не задваивается.
func TestLiveWorksSessions(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", "live-plain", plainSessionFixture, now.Add(-time.Minute))
	writeSession(t, e.home, e.proj, "-xr-005", "live-task", transcriptFixture, now.Add(-2*time.Minute))
	writeSession(t, e.home, e.proj, "-xr-5", "live-dup", transcriptFixture, now.Add(-3*time.Minute))
	writeSession(t, e.home, e.proj, "-xr-88", "stale", transcriptFixture, now.Add(-30*time.Minute))

	want := []Work{
		{ID: "XR-9", Kind: "goal", Via: "tmux"},
		{ID: "XR-5", Kind: "task", Via: "tmux"},
		{ID: "XR-112", Kind: "goal", Via: "registry"},
		{Kind: "session", Via: "session", Session: "live-plain", Note: unknownTaskNote},
		{ID: "XR-005", Kind: "task", Title: "Задача в работе", Sect: "in-progress", Via: "session", Session: "live-task"},
	}
	if got := boardWorks(t, e); !reflect.DeepEqual(got, want) {
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
		{ID: "XR-002", Kind: "task", Title: "Обычная задача", Sect: "backlog", Via: "session", Session: "win-new"},
		{ID: "XR-100", Kind: "goal", Title: "Цель: пробный цикл", Sect: "in-progress", Via: "session", Session: "win-goal"},
	}
	got := boardWorks(t, e)
	var sessions []Work
	for _, w := range got {
		if w.Via == "session" {
			sessions = append(sessions, w)
		}
	}
	if !reflect.DeepEqual(sessions, want) {
		t.Errorf("работы из сессий:\n%+v\nожидал:\n%+v", sessions, want)
	}
}

// Задача с чужим префиксом проекту не приписывается: ходить по ней на экран
// задачи некуда, и работа остаётся в списке с подписью.
func TestLiveWorksSessionForeignTask(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", "win-foreign", foreignSessionFixture, now.Add(-time.Minute))

	want := []Work{{Kind: "session", Via: "session", Session: "win-foreign", Note: foreignTaskNote}}
	var sessions []Work
	for _, w := range boardWorks(t, e) {
		if w.Via == "session" {
			sessions = append(sessions, w)
		}
	}
	if !reflect.DeepEqual(sessions, want) {
		t.Errorf("работа с чужой задачей:\n%+v\nожидал:\n%+v", sessions, want)
	}
}

// boardWorks читает живые работы проекта из ответа доски.
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

// Клиент показывает интерактивную работу как таковую: в полосе живых работ у
// неё подпись вместо кнопки стопа, экран агента ставит фишку, а переписка
// открывается только у цели, потому что обычной задаче отправка ответила бы
// «не цель».
func TestStaticInteractiveWork(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	live := funcBody(t, text, "function renderLive(")
	if !strings.Contains(live, `} else if (w.via === "session") {`) {
		t.Error("полоса живых работ не различает интерактивную сессию")
	}
	if strings.Index(live, `w.via === "session"`) < strings.Index(live, `"btn btn-sm", "Стоп"`) {
		t.Error("ветка интерактивной сессии стоит до кнопки стопа: кнопка достанется и ей")
	}
	agent := funcBody(t, text, "function renderAgent(")
	if !strings.Contains(agent, `work.via === "session"`) || !strings.Contains(agent, "интерактивная сессия") {
		t.Error("экран агента не подписывает интерактивную сессию")
	}
	if !strings.Contains(agent, `if (!work || work.kind === "goal") {`) {
		t.Error("кнопка чата открыта не одной целью: у обычной задачи она ведёт в тупик")
	}
}

// Работа зовётся заголовком с доски и на полосе живых работ, и в шапке экрана
// агента, а имя сессии остаётся мелкой подписью рядом.
func TestStaticWorkTitle(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	live := funcBody(t, text, "function renderLive(")
	if !strings.Contains(live, `el("span", "wname wtitle", w.title)`) {
		t.Error("полоса живых работ подписана именем сессии, а не заголовком задачи")
	}
	if strings.Contains(live, `"goal-"`) {
		t.Error("в полосе живых работ осталось служебное имя сессии goal-<ID>: о занятии агента оно не говорит")
	}
	agent := funcBody(t, text, "function renderAgent(")
	if !strings.Contains(agent, "work.title") || !strings.Contains(agent, "title || name") {
		t.Error("шапка экрана агента подписана именем сессии, а не заголовком задачи")
	}
	if !strings.Contains(agent, `if (title) head.append(el("span", "wname", name));`) {
		t.Error("имя сессии не осталось подписью в шапке экрана агента")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".lcard span.wname.wtitle", ".lcard span.wname", ".ahead .wname"} {
		if !strings.Contains(css, want) {
			t.Errorf("в стилях нет %s: заголовок и подпись рисуются одним кеглем", want)
		}
	}
}

// Экран агента называет источник журнала подписью панели: строки приходят либо
// из журнала оболочки, либо из раздела «Журнал» файла цели, и путать их нельзя.
func TestStaticJournalSource(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "function wireJournal(")
	if !strings.Contains(body, `es.addEventListener("source"`) || !strings.Contains(body, "sub.textContent") {
		t.Error("подпись источника журнала не приходит с сервера")
	}
	if strings.Contains(funcBody(t, text, "function renderAgent("), `".devkit/goal-" + id + ".log"`) {
		t.Error("экран агента называет журнал goal-<ID>.log до ответа сервера: у цели живого чата такого файла нет")
	}
}

// Язык экрана агента взят из макета «03 Агент»: панель хода витка называется
// логом витка, а не транскриптом, журнал зовётся журналом агента, и жаргон
// «строки» и «запуска» с экранов ушёл.
func TestStaticAgentPaneWords(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function renderAgent(")
	for _, want := range []string{`pane("Журнал агента"`, `pane("Лог витка"`,
		`["Журнал", "Лог витка", "tmux"]`} {
		if !strings.Contains(body, want) {
			t.Errorf("в экране агента нет %q", want)
		}
	}
	for _, gone := range []string{`"Транскрипт"`, `"Журнал цикла"`, `"Стоп цикла"`, "грумминг",
		`"строка обновилась`} {
		if strings.Contains(app, gone) {
			t.Errorf("в static/app.js осталось слово %q: экраны говорят с пользователем иначе", gone)
		}
	}
}

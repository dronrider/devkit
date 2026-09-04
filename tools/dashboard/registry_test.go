package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Реестр чатов задачи (DK-431): строки ~/.devkit/sessions.log пишет
// SessionStart-хук и ручка привязки, читает дашборд. Ожидания тут выписаны
// руками по формату хука (hooks/session-task.py, record): посчитанное тем же
// разбором сошлось бы с любым его поведением.

// bindFixture это строка реестра про сессию, поднятую заказом дашборда, снятая
// с живого прогона хука.
const bindFixture = "2026-08-18T12:03:11 сессия 0f2c-e91 задача DK-430 проект devkit " +
	"дерево /Users/r/projects/devkit-dk-430 транскрипт /Users/r/.claude/projects/-p/0f2c-e91.jsonl " +
	"источник заказ повод startup tmux chat-DK-430-1\n"

// writeBinds кладёт реестр в дом стенда.
func writeBinds(t *testing.T, home string, lines ...string) string {
	t.Helper()
	path := sessions.Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// appendBinds дописывает строки в реестр, не трогая уже лежащих: стенду с
// несколькими сессиями надо накопить записи, а не переписать файл заново.
func appendBinds(t *testing.T, home string, lines ...string) {
	t.Helper()
	path := sessions.Path(home)
	for _, ln := range lines {
		if err := sessions.Append(path, ln); err != nil {
			t.Fatal(err)
		}
	}
}

// bindRecord собирает строку реестра руками, как её пишет хук.
func bindRecord(stamp, sid, task, source string) string {
	return fmt.Sprintf("%s сессия %s задача %s проект demo дерево /tmp/demo транскрипт /tmp/t.jsonl "+
		"источник %s повод startup tmux -\n", stamp, sid, task, source)
}

func TestParseBindLine(t *testing.T) {
	sid, b, ok := parseBindLine(bindFixture)
	if !ok {
		t.Fatal("живая строка реестра не разобралась")
	}
	if sid != "0f2c-e91" {
		t.Errorf("сессия: %q", sid)
	}
	want := sessionBind{Task: "DK-430", Source: bindOrder, Project: "devkit",
		Tree:       "/Users/r/projects/devkit-dk-430",
		Transcript: "/Users/r/.claude/projects/-p/0f2c-e91.jsonl",
		Tmux:       "chat-DK-430-1", Time: "2026-08-18T12:03:11"}
	if b != want {
		t.Errorf("запись:\n%+v\nожидал:\n%+v", b, want)
	}
}

// Пустое поле в строке стоит дефисом, и дефис это пустота, а не значение:
// иначе задачей сессии стал бы прочерк.
func TestParseBindLineDashesAreEmpty(t *testing.T) {
	sid, b, ok := parseBindLine(bindRecord("2026-08-18T12:00:00", "aaa-1", "-", "-"))
	if !ok || sid != "aaa-1" {
		t.Fatalf("строка без задачи: %q %v", sid, ok)
	}
	if b.Task != "" || b.Source != "" {
		t.Errorf("дефис доехал значением: %+v", b)
	}
}

// Путь с пробелом строку не рассыпает: значение собирается до следующего
// ключевого слова, а каталог с пробелом в имени законен.
func TestParseBindLineKeepsSpacedPath(t *testing.T) {
	line := "2026-08-18T12:00:00 сессия aaa-1 задача DK-1 проект demo дерево /tmp/my demo " +
		"транскрипт /tmp/t.jsonl источник дерево повод startup tmux -\n"
	_, b, ok := parseBindLine(line)
	if !ok || b.Tree != "/tmp/my demo" || b.Task != "DK-1" {
		t.Fatalf("путь с пробелом разобран не так: %+v %v", b, ok)
	}
}

// Чужая и битая строка пропускаются: журнал машинный, и разбор об него падать
// не должен.
func TestParseBindLineRefusesJunk(t *testing.T) {
	for _, line := range []string{
		"", "мусор",
		"2026-08-18T12:00:00 повод startup",
		"вчера сессия aaa-1 задача DK-1 источник рука",
		"2026-08-18T12:00:00 сессия - задача DK-1 источник рука",
	} {
		if _, _, ok := parseBindLine(line); ok {
			t.Errorf("строка принята зря: %q", line)
		}
	}
}

// Свёртка журнала: выигрывает последняя строка сессии, соседние сессии друг
// другу не мешают. Перепривязка тут и есть обычная запись.
func TestParseBindsLastWins(t *testing.T) {
	data := bindRecord("2026-08-18T12:00:00", "aaa-1", "DK-1", bindTree) +
		bindRecord("2026-08-18T12:00:00", "bbb-2", "DK-2", bindOrder) +
		"мусор посреди журнала\n" +
		bindRecord("2026-08-18T13:00:00", "aaa-1", "DK-9", bindHand)
	binds := parseBinds([]byte(data))
	if len(binds) != 2 {
		t.Fatalf("свёртка: %+v", binds)
	}
	if binds["aaa-1"].Task != "DK-9" || binds["aaa-1"].Source != bindHand {
		t.Errorf("перепривязка не выиграла: %+v", binds["aaa-1"])
	}
	if binds["bbb-2"].Task != "DK-2" {
		t.Errorf("соседняя сессия потерялась: %+v", binds["bbb-2"])
	}
}

// Разряды привязки: запись реестра это «ведёт», хвост бокового дерева врать не
// умеет и остаётся разрядом «ведёт» и без записи, а первая реплика и имя ветки
// задачу больше не называют вовсе: угадывание снято, и сессия без записи
// уходит в «задача не распознана».
func TestBindTaskRanks(t *testing.T) {
	binds := parseBinds([]byte(
		bindRecord("2026-08-18T12:00:00", "ordered", "DK-1", bindOrder) +
			bindRecord("2026-08-18T12:00:00", "byhand", "DK-2", bindHand) +
			bindRecord("2026-08-18T12:00:00", "bytree", "DK-3", bindTree) +
			bindRecord("2026-08-18T12:00:00", "newword", "DK-4", "звёзды")))
	head := sessionHead{Branch: "dk-77", Named: "DK-88"}
	for _, tc := range []struct {
		name, sid, suffix string
		head              sessionHead
		task, note, bound string
	}{
		{"заказ", "ordered", "", head, "DK-1", orderNote, boundLead},
		{"рука", "byhand", "", head, "DK-2", handNote, boundLead},
		{"дерево записью", "bytree", "", head, "DK-3", treeNote, boundLead},
		{"незнакомое слово источника", "newword", "", head, "DK-4", recordNote, boundLead},
		{"дерево без записи", "нет", "dk-5", head, "DK-5", treeNote, boundLead},
		{"первая реплика не привязывает", "нет", "", head, "", unknownTaskNote, ""},
		{"ветка не привязывает", "нет", "", sessionHead{Branch: "dk-77"}, "", unknownTaskNote, ""},
		{"ничего", "нет", "", sessionHead{}, "", unknownTaskNote, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, note, bound := bindTask(binds, tc.sid, tc.suffix, tc.head)
			if task != tc.task || note != tc.note || bound != tc.bound {
				t.Errorf("привязка: %q %q %q, ожидал %q %q %q", task, note, bound, tc.task, tc.note, tc.bound)
			}
		})
	}
}

// Запись реестра старше угадывания: сессия, привязанная рукой к DK-2, не
// возвращается к задаче своего дерева, иначе кнопка привязки ничего не меняла
// бы у сессии в боковом дереве.
func TestBindTaskRecordBeatsTheTree(t *testing.T) {
	binds := parseBinds([]byte(bindRecord("2026-08-18T12:00:00", "aaa-1", "DK-2", bindHand)))
	task, note, _ := bindTask(binds, "aaa-1", "dk-5", sessionHead{})
	if task != "DK-2" || note != handNote {
		t.Fatalf("рука не перебила дерево: %q %q", task, note)
	}
}

// Снятая привязка гасит и угадывание: человек сказал «это не работа задачи», и
// возвращать её первой репликой значило бы не слышать сказанного.
func TestBindTaskUnbindStopsGuessing(t *testing.T) {
	binds := parseBinds([]byte(bindRecord("2026-08-18T12:00:00", "aaa-1", "-", bindOff)))
	task, note, bound := bindTask(binds, "aaa-1", "dk-5", sessionHead{Named: "DK-88", Branch: "dk-77"})
	if task != "" || bound != "" || note != offNote {
		t.Fatalf("снятая привязка вернулась: %q %q %q", task, note, bound)
	}
}

// Сессия без задачи, записанная хуком (чат доски), это не отвязка: отвязку
// несёт только слово bindOff, а пустая запись оставляет право назвать задачу
// хвосту бокового дерева.
func TestBindTaskEmptyRecordIsNotUnbind(t *testing.T) {
	binds := parseBinds([]byte(bindRecord("2026-08-18T12:00:00", "aaa-1", "-", "-")))
	task, note, bound := bindTask(binds, "aaa-1", "dk-5", sessionHead{Named: "DK-88"})
	if task != "DK-5" || note != treeNote || bound != boundLead {
		t.Fatalf("пустая запись сошла за отвязку: %q %q %q", task, note, bound)
	}
}

// Чат о задаче живой работой не горит (DK-360): угадывания по первой реплике
// больше нет, карточка остаётся сессией без задачи, и подписана она заголовком
// самого чата.
func TestSessionWorksAboutIsNotWork(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", runsBoardJSON))
	writeSession(t, e.home, e.proj, "", "talker", sessionLine("а что там с XR-4?", "main"), time.Now())
	rows := map[string]boardRow{"XR-4": {ID: "XR-4", Title: "Начатая задача", Sect: "in-progress"}}

	works := e.s.sessionWorks(e.proj, "XR", rows, map[string]bool{})
	if len(works) != 1 {
		t.Fatalf("работы: %+v", works)
	}
	// Модель работы приезжает вместе с ней: по ней фильтруется раздел
	// «Агенты», и у сессии без своей записи это модель по умолчанию.
	// Подписка приезжает вместе с работой: узнаётся она по дому транскрипта, а
	// журнал этой сессии лежит в доме прогона, то есть у подписки по умолчанию.
	want := Work{Kind: "session", Via: "session", Session: "talker",
		Note: "а что там с XR-4", Model: chatModelDefault, Harness: "перваяtest"}
	if !reflect.DeepEqual(bareWorks(works)[0], want) {
		t.Errorf("работа:\n%+v\nожидал:\n%+v", works[0], want)
	}
}

// Запись о работе, наоборот, делает сессию работой задачи: заголовок и секция
// приезжают со строки доски, и на карточке это живая работа. Строку такой
// записи кладёт команда доски (taskctl, shipctl, agentctl stage), и работой
// сессия становится по факту хода, а не по имени своего окна.
func TestSessionWorksLeadIsWork(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", runsBoardJSON))
	writeSession(t, e.home, e.proj, "", "worker", sessionLine("а что там с XR-4?", "main"), time.Now())
	writeBinds(t, e.home, bindRecord("2026-08-18T12:00:00", "worker", "XR-4", sessions.BySrc))
	rows := map[string]boardRow{"XR-4": {ID: "XR-4", Title: "Начатая задача", Sect: "in-progress"}}

	works := e.s.sessionWorks(e.proj, "XR", rows, map[string]bool{})
	// Своей она не считается: имени tmux-сессии в записи нет, значит подняли её
	// мимо дашборда, и в разделе «Агенты» ей место в табе прочих.
	want := Work{ID: "XR-4", Kind: "task", Via: "session", Session: "worker",
		Title: "Начатая задача", Sect: "in-progress", Model: chatModelDefault,
		Harness: "перваяtest", Rows: []string{"XR-4"}}
	if len(works) != 1 || !reflect.DeepEqual(bareWorks(works)[0], want) {
		t.Fatalf("работа:\n%+v\nожидал:\n%+v", works, want)
	}
}

// Ручка привязки: нераспознанная сессия получает задачу рукой, и в выдаче
// сессий у неё разряд «ведёт» с подписью словами.
func TestSessionTaskPostBinds(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	writeSession(t, e.home, e.proj, "", "aaa-1", sessionLine("поговорим", "main"), time.Now())

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/sessions/aaa-1/task", `{"task": "dk-431"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("привязка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"task":"DK-431"`) {
		t.Errorf("ответ не называет задачу: %s", text)
	}
	binds := e.s.binds()
	if got := binds["aaa-1"]; got.Task != "DK-431" || got.Source != bindHand {
		t.Fatalf("запись реестра: %+v", got)
	}
	// Транскрипт в записи не украшение: живость разговора меряется его
	// свежестью, и вывести путь из одного ID дорого.
	if !strings.HasSuffix(binds["aaa-1"].Transcript, "aaa-1.jsonl") {
		t.Errorf("в записи нет транскрипта: %+v", binds["aaa-1"])
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1", "")
	var got struct {
		Head sessionInfo `json:"head"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Head.Task != "DK-431" || got.Head.TaskNote != handNote || got.Head.Bound != boundLead {
		t.Errorf("шапка привязанной сессии: %+v", got.Head)
	}
}

// Отвязка это пустое значение, и она ложится своей строкой: файл реестра
// никто не правит.
func TestSessionTaskPostUnbinds(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	writeSession(t, e.home, e.proj, "-dk-5", "aaa-1", sessionLine("поговорим", "main"), time.Now())

	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/sessions/aaa-1/task", `{"task": "DK-431"}`)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/sessions/aaa-1/task", `{"task": ""}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "привязка сессии aaa-1 снята") {
		t.Fatalf("отвязка: %d %s", resp.StatusCode, text)
	}
	lines := strings.Split(strings.TrimRight(readFile(t, sessions.Path(e.home)), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("отвязка не легла строкой: %q", lines)
	}
	if got := e.s.binds()["aaa-1"]; got.Task != "" || got.Source != bindOff {
		t.Errorf("после отвязки: %+v", got)
	}
}

// Имя tmux-сессии переезжает в новую запись: рука правит задачу, а не то, чем
// сессия поднята, и без имени мера живости разговора осталась бы без опоры.
func TestSessionTaskPostKeepsTmuxName(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	writeSession(t, e.home, e.proj, "", "0f2c-e91", sessionLine("поговорим", "main"), time.Now())
	writeBinds(t, e.home, bindFixture)

	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/sessions/0f2c-e91/task", `{"task": "DK-9"}`)
	if got := e.s.binds()["0f2c-e91"]; got.Tmux != "chat-DK-430-1" || got.Task != "DK-9" {
		t.Fatalf("после перепривязки: %+v", got)
	}
}

// Отказы ручки: чужая сессия, кривой ID задачи, кривой id сессии и битое тело
// называются словами, а не молчаливой записью в реестр.
func TestSessionTaskPostRefusals(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	writeSession(t, e.home, e.proj, "", "aaa-1", sessionLine("поговорим", "main"), time.Now())
	for _, tc := range []struct {
		name, sid, body string
		code            int
		want            string
	}{
		{"нет транскрипта", "no-such-sid", `{"task": "DK-1"}`, http.StatusNotFound, "привязывать нечего"},
		{"кривой id сессии", "сессия!", `{"task": "DK-1"}`, http.StatusBadRequest, "не похоже на id сессии"},
		{"кривой ID задачи", "aaa-1", `{"task": "задача"}`, http.StatusBadRequest, "не похоже на ID задачи"},
		{"битое тело", "aaa-1", `не json`, http.StatusBadRequest, "жду JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/sessions/"+tc.sid+"/task", tc.body)
			text := body(t, resp)
			if resp.StatusCode != tc.code || !strings.Contains(text, tc.want) {
				t.Fatalf("отказ: %d %s", resp.StatusCode, text)
			}
			if _, err := os.Stat(sessions.Path(e.home)); err == nil {
				t.Errorf("отказ всё равно написал строку в реестр")
			}
		})
	}
}

// Реестра нет вовсе это штатный первый запуск: привязок нет, угадывание
// работает как раньше, и падать тут нечему.
func TestBindsWithoutTheLog(t *testing.T) {
	e := newTestEnv(t)
	if got := e.s.binds(); len(got) != 0 {
		t.Fatalf("пустой реестр: %+v", got)
	}
}

// Разросшийся журнал режется до последних строк тем же правилом, что у
// python-писателя: два писателя с разными берегами оставили бы файл, растущий
// до диска.
func TestAppendBindCapsTheLog(t *testing.T) {
	path := sessions.Path(t.TempDir())
	long := strings.Repeat(bindRecord("2026-08-18T12:00:00", "old", "DK-1", bindTree), 900)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendBind(path, bindRecord("2026-08-18T13:00:00", "new", "DK-2", bindHand)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(readFile(t, path), "\n"), "\n")
	if len(lines) != bindLogKeep+1 {
		t.Fatalf("журнал не обрезан: строк %d", len(lines))
	}
	if binds := parseBinds([]byte(readFile(t, path))); binds["new"].Task != "DK-2" {
		t.Errorf("свежая строка потерялась при обрезке: %+v", binds)
	}
}

// Строка ручки читается тем же разбором, что строка хука: писателя два, формат
// один, и разъехавшись, они оставили бы читателя с половиной записей.
func TestBindLineIsReadBack(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 3, 11, 0, time.UTC)
	line := bindLine(now, "aaa-1", sessionBind{Task: "DK-431", Source: bindHand,
		Project: "devkit", Tree: "/tmp/devkit", Transcript: "/tmp/a.jsonl"})
	sid, b, ok := parseBindLine(line)
	if !ok || sid != "aaa-1" {
		t.Fatalf("строка ручки не разобралась: %q", line)
	}
	if b.Task != "DK-431" || b.Source != bindHand || b.Tmux != "" || b.Time != "2026-08-18T12:03:11" {
		t.Fatalf("разбор строки ручки: %+v", b)
	}
}

// Строку In progress присваивают рабочие сессии, а не всякий разговор о
// задаче: груминг черновика и привязка рукой это чтение задачи, и по ним
// запускать нечего. Груминг приезжает тем же заказом дашборда, что и чат по
// строке, и работой ни тот, ни другой не становятся до первой команды доски. Жалоба была на живой доске: задачу вели на другой машине, а
// строка предлагала кнопку запуска, потому что тут по ней когда-то грумили.
func TestTaskChatsCountsWorkSessionsOnly(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", runsBoardJSON))
	// Груминг приезжает тем же заказом дашборда, что запуск задачи, и по полям
	// реестра от него неотличим: разводит их первая реплика.
	writeSession(t, e.home, e.proj, "", "groomer",
		sessionLine("Проведи груминг XR-7", "main"), time.Now())
	writeSession(t, e.home, e.proj, "", "hands",
		sessionLine("посмотри, что там с XR-8", "main"), time.Now())
	writeSession(t, e.home, e.proj, "", "worker",
		sessionLine("возьми XR-9 в работу", "main"), time.Now())
	writeBinds(t, e.home,
		bindRecord("2026-08-18T12:00:00", "groomer", "XR-7", bindOrder),
		bindRecord("2026-08-18T12:01:00", "hands", "XR-8", bindHand),
		bindRecord("2026-08-18T12:02:00", "worker", "XR-9", sessions.BySrc))
	own := e.s.taskChats(e.proj)
	if _, hit := own["XR-7"]; hit {
		t.Error("груминг присвоил строку задачи: кнопки запуска вернулись на чужую работу")
	}
	if _, hit := own["XR-8"]; hit {
		t.Error("привязка рукой присвоила строку задачи")
	}
	if _, hit := own["XR-9"]; !hit {
		t.Errorf("рабочая сессия строку не присвоила: %+v", own)
	}
}

// Прогон стендов дашборда не пишет в живой реестр сессий. Стенды зовут
// настоящий taskctl, тот отмечает работу сессии строкой в реестре, и берётся
// ID сессии из окружения: прогон из живого разговора дописывал её именем
// записи деревьями /var/folders/... в живой ~/.devkit/sessions.log, реестр
// забывал имя tmux этой сессии, и дашборд переставал видеть, чем её снимать
// (живой случай chat-DK-397-1). ID снимает TestMain на весь бинарь прогона.
func TestRunLeavesLiveRegistryAlone(t *testing.T) {
	if sid := os.Getenv("CLAUDE_CODE_SESSION_ID"); sid != "" {
		t.Errorf("прогон унаследовал ID живой сессии %q: её записи станут записями прогона", sid)
	}
}

// Заказ подъёма собирается одним местом на все четыре дороги: разговор,
// конвейер задачи, цикл цели и разбор черновика. Прежде разбор звал сборку
// сбоку, и стоило дорогам разойтись, как поднятые сессии пропадали из панели:
// пять разборов DK-482..486 работали, а чаты в панели стояли пустыми.
func TestLaunchEnvSameForEveryOrder(t *testing.T) {
	s := newServer(&Config{Home: t.TempDir()}, nil, nil)
	env := s.launchEnv("XR-7", "task-XR-7", "")
	// Набор пар назван поимённо: молчаливое сравнение строк пропустило бы
	// потерю переменной, о которой никто не помнит.
	for _, want := range []string{"DEVKIT_NO_FOCUS=1", "HOME=", "DEVKIT_TASK='XR-7'",
		"DEVKIT_TMUX='task-XR-7'"} {
		if !strings.Contains(env, want) {
			t.Fatalf("в общей сборке окружения нет %s: %s", want, env)
		}
	}
	orders := map[string]string{
		"разговор": chatCmd(env, "opus", "", "привет", execRotateDefault, nil, "agentctl"),
		"конвейер": sessionCommand("agentctl", "task-run.py", nil, env, "выполни XR-7",
			"продолжай XR-7", "XR-7", "/тмп/проект", "проект", "opus"),
		"разбор":   groomCmd(env, "разбери XR-7", nil, "opus"),
	}
	for name, cmd := range orders {
		if !strings.HasPrefix(cmd, env) {
			t.Errorf("заказ вида %q собрал окружение по-своему:\n%s\nждал в начале:\n%s",
				name, cmd, env)
		}
	}
}

// Метку печатного режима носит одна дорога, конвейер задачи. Разговор, разбор
// черновика и окно входа поднимают живой REPL, там фоновый ход законен
// (контракт DK-678), и метка стоила бы им отказа рубежа с выдуманной причиной.
func TestHeadlessMarkOnlyForPipeline(t *testing.T) {
	s := newServer(&Config{Home: t.TempDir()}, nil, nil)
	env := s.launchEnv("XR-7", "task-XR-7", "")
	if strings.Contains(env, "DEVKIT_HEADLESS=") {
		t.Fatalf("общая сборка окружения несёт метку печатного режима: %s", env)
	}
	for name, cmd := range map[string]string{
		"разговор": chatCmd(env, "opus", "", "привет", execRotateDefault, nil, "agentctl"),
		"разбор":   groomCmd(env, "разбери XR-7", nil, "opus"),
		"вход":     env + " " + defaultClient,
	} {
		if strings.Contains(cmd, "DEVKIT_HEADLESS=") {
			t.Errorf("живая дорога %q помечена печатным режимом:\n%s", name, cmd)
		}
	}
	head := s.headlessEnv("XR-7", "task-XR-7")
	if !strings.Contains(head, headlessMark) {
		t.Fatalf("окружение конвейера без метки печатного режима: %s", head)
	}
	// Метка стоит после чистки, а не перед нею: парой впереди команды её снял бы
	// собственный `-u`, и рубеж снова считал бы конвейер живым окном.
	if strings.Index(head, headlessMark) < strings.Index(head, dropForeign()) {
		t.Errorf("метка стоит впереди чистки и снимется ею же:\n%s", head)
	}
	if !strings.Contains(dropForeign(), "-u DEVKIT_HEADLESS") {
		t.Errorf("унаследованная метка не снимается чисткой: %s", dropForeign())
	}
}

// Каталог самого дашборда поднятой сессии не раздаётся, а утилиты кита она
// берёт из штатного каталога машины. Экземпляр держит рядом с дашбордом свои
// копии agentctl и taskctl, plist ставит его каталог в PATH первым, и сессия
// неделю звала копии: вердикт pick, снимок квоты и гейт бюджета целей считала
// сборка без версии, а разбор DK-457 наступил на это прямо (DK-549).
func TestLaunchEnvNamesMachineKitInPath(t *testing.T) {
	inst, kit := t.TempDir(), t.TempDir()
	writeKit(t, inst)
	writeKit(t, kit)
	wasExe, wasKit := exeDir, kitDir
	exeDir = func() string { return inst }
	kitDir = func() string { return kit }
	t.Cleanup(func() { exeDir, kitDir = wasExe, wasKit })
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", inst+sep+"/usr/bin")
	s := newServer(&Config{Home: t.TempDir()}, nil, nil)

	env := s.launchEnv("XR-9", "task-XR-9", "")
	want := "PATH='" + kit + sep + "/usr/bin'"
	if !strings.Contains(env, want) {
		t.Fatalf("путь поднятой сессии собран не так: %s\nждал %s", env, want)
	}
	if strings.Contains(env, inst) {
		t.Errorf("каталог экземпляра уехал в сессию: %s", env)
	}
}

// Разбор пути по случаям. Второй кит экземпляра тут не выдумка: раскладка
// devkitctl update под подложным домом демона кладёт полный набор в
// <дом демона>/.local/bin, и он стоит в том же PATH, что каталог экземпляра.
func TestSessionPath(t *testing.T) {
	kit, inst, deep := t.TempDir(), t.TempDir(), t.TempDir()
	writeKit(t, kit)
	writeKit(t, inst)
	writeKit(t, deep)
	sep := string(os.PathListSeparator)
	cases := []struct {
		name string
		path string
		own  string
		kit  string
		want string
	}{
		{"каталог экземпляра уходит, штатный кит встаёт первым",
			inst + sep + "/usr/bin", inst, kit, kit + sep + "/usr/bin"},
		{"вторая копия кита в подложном доме проигрывает штатной",
			inst + sep + "/usr/bin" + sep + deep, inst, kit, kit + sep + "/usr/bin" + sep + deep},
		{"штатная раскладка остаётся как была",
			kit + sep + "/usr/bin", kit, kit, ""},
		{"штатного каталога не знаем, кит есть и без своего",
			inst + sep + deep + sep + "/usr/bin", inst, "", deep + sep + "/usr/bin"},
		{"штатного каталога не знаем, и кита без своего не остаётся",
			inst + sep + "/usr/bin", inst, "", ""},
		{"своего каталога в пути нет", kit + sep + "/usr/bin", inst, kit, ""},
		{"пустой путь", "", inst, kit, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionPath(tc.path, tc.own, tc.kit); got != tc.want {
				t.Errorf("sessionPath(%q, %q, %q) = %q, ждал %q",
					tc.path, tc.own, tc.kit, got, tc.want)
			}
		})
	}
}

// writeKit кладёт в каталог пустые исполнимые файлы утилит кита.
func writeKit(t *testing.T, dir string) {
	t.Helper()
	for _, name := range kitBins {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// Дома у дашборда и у поднятой сессии разные: сессия получает дом машины (без
// него раскладка подписок разворачивает тильду в тонкий каталог без логина), а
// дашборд живёт своим. Реестр из-за этого разъезжается, и читать надо оба дома,
// той же дорогой, какой читаются каталоги транскриптов.
func TestBindsReadBothHomes(t *testing.T) {
	own, machine := t.TempDir(), t.TempDir()
	was := realHomeFn
	realHomeFn = func() string { return machine }
	t.Cleanup(func() { realHomeFn = was })
	s := newServer(&Config{Home: own}, nil, nil)

	// Запись сессии разбора легла в дом машины: туда её кладёт хук старта по
	// своему HOME.
	line := "2026-08-25T23:58:04 сессия 7749edb9-1111 задача DK-483 проект devkit " +
		"дерево /tmp/devkit транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-DK-483\n"
	writeBindsAt(t, machine, line)
	// А в своём доме лежит запись другой сессии: оба дома обязаны доехать.
	writeBindsAt(t, own, "2026-08-25T20:00:00 сессия aaaa2222-2222 задача DK-400 проект devkit "+
		"дерево /tmp/devkit транскрипт - источник заказ повод startup tmux task-DK-400\n")

	got := s.binds()
	if rec, ok := got["7749edb9-1111"]; !ok || rec.Task != "DK-483" {
		t.Fatalf("сессия разбора из дома машины не видна дашборду: %+v", got)
	}
	if rec, ok := got["aaaa2222-2222"]; !ok || rec.Task != "DK-400" {
		t.Errorf("своя запись потерялась при чтении двух домов: %+v", got)
	}
	if sid, _ := got.Leads("DK-483"); sid != "7749edb9-1111" {
		t.Errorf("ведущая сессия задачи DK-483 не нашлась: %q", sid)
	}
	// Тот же склеенный журнал виден и списком записей на сессию: по нему панель
	// узнаёт имя tmux и дорогу к разговору.
	if recs := s.bindsAll()["7749edb9-1111"]; len(recs) != 1 || recs[0].Tmux != "task-DK-483" {
		t.Errorf("список записей сессии разбора не собрался: %+v", recs)
	}
}

// writeBindsAt кладёт журнал реестра в названный дом.
func writeBindsAt(t *testing.T, home, text string) {
	t.Helper()
	path := sessions.Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

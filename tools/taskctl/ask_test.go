package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
)

// askStand это ожидание с подменённым внешним миром: часы идут сном, а
// уведомитель, отметка этапа и парковка записываются, а не выполняются.
type askStand struct {
	now    time.Time
	deps   askDeps
	notes  []string
	stages []string
	parked []string
	env    map[string]string
}

// askStart это момент, с которого стенд считает время: часы двигает только сон
// ожидания, поэтому срок в признаке сверяется с ним, а не с живыми часами.
var askStart = time.Date(2026, 8, 19, 12, 0, 0, 0, time.Local)

func newAskStand(t *testing.T) *askStand {
	t.Helper()
	st := &askStand{now: askStart, env: map[string]string{}}
	st.deps = askDeps{
		Now:   func() time.Time { return st.now },
		Sleep: func(d time.Duration) { st.now = st.now.Add(d) },
		Notify: func(reason, id, title, body string) string {
			st.notes = append(st.notes, reason+" "+id+" "+body)
			return ""
		},
		Stage: func(id, note string) { st.stages = append(st.stages, id+" "+note) },
		Park: func(id, reason string) (string, error) {
			st.parked = append(st.parked, id+" "+reason)
			return id + " в blocked", nil
		},
		Home: t.TempDir(),
	}
	return st
}

func (st *askStand) run(root string, p AskParams) (string, error) {
	return runAsk(root, p, st.deps, func(k string) string { return st.env[k] }, nil)
}

// say кладёт во вход разговора реплику, как её кладёт ручка дашборда.
func say(t *testing.T, tree, name, text, sid string) {
	t.Helper()
	line := chat.TaskLine(time.Now(), text)
	if sid != "" {
		line = chat.Line(time.Now(), sid, text)
	}
	if _, err := chat.Put(tree, name, text, line); err != nil {
		t.Fatal(err)
	}
}

// Лежащий ответ кончает ожидание: агент видит текст реплики, признак снят,
// задача не паркуется, а заход продолжается.
func TestAskTakesTheLyingAnswer(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	say(t, root, chat.TaskName("XR-005"), "бери второй вариант", "")
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "какой вариант", Wait: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "бери второй вариант") {
		t.Fatalf("ответа человека в выводе нет:\n%s", out)
	}
	if len(st.parked) != 0 {
		t.Fatalf("дождавшись ответа, задачу припарковали: %v", st.parked)
	}
	if _, err := os.Stat(chat.AskPath(root, chat.TaskName("XR-005"))); !os.IsNotExist(err) {
		t.Fatalf("признак ожидания остался лежать: %v", err)
	}
	if len(st.notes) != 1 || !strings.HasPrefix(st.notes[0], reasonAsk+" XR-005") {
		t.Fatalf("громкий повод не позвали: %v", st.notes)
	}
	if len(st.stages) != 1 || !strings.Contains(st.stages[0], "вопрос: какой вариант") {
		t.Fatalf("этап «уточнение» не отмечен: %v", st.stages)
	}
}

// Реплика, адресованная другой сессии, ожиданию не достаётся: она написана
// живому окну человека, а не ждущему заходу.
func TestAskSkipsForeignAddressee(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.env[askSessionEnv] = "aaa-1"
	say(t, root, chat.TaskName("XR-005"), "это окну человека", "bbb-2")
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "какой вариант", Wait: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "это окну человека") {
		t.Fatalf("ожидание забрало чужую реплику:\n%s", out)
	}
	if len(st.parked) != 1 {
		t.Fatalf("не дождавшись, задачу обязаны припарковать: %v", st.parked)
	}
	if lines := chat.ReadLines(chat.Path(root, chat.TaskName("XR-005"))); len(lines) != 1 {
		t.Fatalf("чужая реплика пропала из входа: %v", lines)
	}
}

// Ответ доезжает и из своего дерева: панель пишет в дерево сессии, ручка задачи
// в основной чекаут, и опрашиваются оба.
func TestAskReadsBothTrees(t *testing.T) {
	root := setup(t)
	tree := t.TempDir()
	st := newAskStand(t)
	say(t, tree, chat.TaskName("XR-005"), "ответ из дерева задачи", "")
	out, err := runAskIn(t, st, root, tree, AskParams{ID: "XR-005", Question: "как быть", Wait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ответ из дерева задачи") {
		t.Fatalf("реплику из дерева задачи не взяли:\n%s", out)
	}
}

// Не дождавшись, ожидание паркует задачу само, причиной с машинным разрядом
// «вопрос:», и снимает признак: рук диспетчера это не ждёт.
func TestAskParksOnTimeout(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "нужен доступ к железу", Wait: 4 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 1 || !strings.Contains(st.parked[0], "вопрос: нужен доступ к железу") {
		t.Fatalf("парковка вопросом: %v", st.parked)
	}
	if !strings.Contains(out, "заход кончается рубежом") {
		t.Fatalf("агенту не сказано, чем кончился заход:\n%s", out)
	}
	if _, err := os.Stat(chat.AskPath(root, chat.TaskName("XR-005"))); !os.IsNotExist(err) {
		t.Fatalf("признак ожидания остался лежать: %v", err)
	}

}

// «Не жду, паркуй сразу»: признака нет вовсе, потому что ждущего нет, а лежащий
// признак запер бы вход подхвата на пустом месте.
func TestAskWaitZeroParksAtOnce(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	if _, err := st.run(root, AskParams{ID: "XR-005", Question: "дай доступ к роутеру", Wait: 0}); err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 1 {
		t.Fatalf("парковка сразу: %v", st.parked)
	}
	if _, err := os.Stat(chat.AskPath(root, chat.TaskName("XR-005"))); !os.IsNotExist(err) {
		t.Fatal("при --wait 0 признак писать некому: ждущего нет")
	}
	if !st.now.Equal(askStart) {
		t.Fatal("при --wait 0 ожидание спало")
	}
}

// Признак ожидания лежит во входе задачи основного чекаута и несёт срок, ждущую
// сессию, задачу и пачку вопросов: по нему подхват отдаёт ждущему его строки, а
// сторожок паркует брошенный вопрос.
func TestAskWritesTheStamp(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.env[askSessionEnv] = "aaa-1"
	var got chat.Ask
	sleep := st.deps.Sleep
	st.deps.Sleep = func(d time.Duration) {
		if a, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005"))); ok && got.Task == "" {
			got = a
		}
		sleep(d)
	}
	if _, err := st.run(root, AskParams{ID: "XR-005", Question: "поле или чип", Wait: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if got.Task != "XR-005" || got.Session != "aaa-1" {
		t.Fatalf("шапка признака: %+v", got)
	}
	if !got.Until.Equal(askStart.Add(2 * time.Second)) {
		t.Fatalf("срок признака %s: сторожок паркует брошенный вопрос по нему", got.Until)
	}
	if len(got.Questions) != 1 || got.Questions[0].Text != "поле или чип" {
		t.Fatalf("вопрос в признаке: %+v", got.Questions)
	}
}

// Пачка вопросов приезжает JSON на stdin: печатается человеку столбиком и целиком
// ложится в признак.
func TestAskReadsThePackFromStdin(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	pack := `{"questions":[{"text":"ставим поле","options":[{"label":"да","recommended":true},{"label":"нет"}]},
		{"text":"кто закрывает"}]}`
	out, err := st.run(root, AskParams{ID: "XR-005", Wait: 0, Stdin: strings.NewReader(pack)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ставим поле", "кто закрывает", "* да"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if len(st.parked) != 1 || !strings.Contains(st.parked[0], "ставим поле; кто закрывает") {
		t.Fatalf("суть пачки в причине блока: %v", st.parked)
	}
}

// Не назвали сессию ни ключом, ни окружением, значит её называет реестр чатов:
// адрес у ждущего общий с внешней сессией, и реплика не разминётся.
func TestAskTakesSessionFromRegistry(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	writeRegistry(t, st.deps.Home, "2026-08-19T11:00:00 сессия aaa-1 задача XR-005 проект xr дерево "+root+
		" транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-005")
	say(t, root, chat.TaskName("XR-005"), "ответ ждущему", "aaa-1")
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть", Wait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "сессия из реестра чатов") || !strings.Contains(out, "ответ ждущему") {
		t.Fatalf("реестр не назвал сессию:\n%s", out)
	}
}

// Реестра нет и переменной нет: ожидание идёт по безадресным строкам, и это
// сказано первой строкой вывода, а не скрыто.
func TestAskSaysWhenSessionIsUnknown(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть", Wait: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "сессия не назвалась") {
		t.Fatalf("молчание про неизвестную сессию:\n%s", out)
	}
}

// Черновик доски не занимает, и парковать там нечего: вопрос остаётся файлом
// исхода, из которого его берёт экран черновика.
func TestAskDraftLeavesTheQuestionFile(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	out, err := st.run(root, AskParams{ID: "XR-009", Question: "резать или ждать", Wait: 0, Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 0 {
		t.Fatalf("черновик припарковали на доске: %v", st.parked)
	}
	body, err := os.ReadFile(filepath.Join(root, ".devkit", "draft-XR-009.question"))
	if err != nil {
		t.Fatalf("файла исхода нет: %v", err)
	}
	if !strings.Contains(string(body), "резать или ждать") {
		t.Fatalf("вопрос в файле исхода: %s", body)
	}
	if !strings.Contains(out, "файлом исхода") {
		t.Fatalf("агенту не сказали, где остался вопрос:\n%s", out)
	}
}

// Парковка ответила отказом (потолок висящих вопросов, неуехавшая доска):
// заход от этого не падает, вопрос уже задан, но отказ назван, и агента
// отправляют смотреть строку.
func TestAskSurvivesTheRefusedParking(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.deps.Park = func(id, reason string) (string, error) {
		return "", errors.New("вопросов висит 2 из 2")
	}
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть", Wait: 0})
	if err != nil {
		t.Fatalf("отказ парковки уронил заход: %v", err)
	}
	if !strings.Contains(out, "вопросов висит 2 из 2") || !strings.Contains(out, "taskctl show XR-005") {
		t.Fatalf("отказ парковки не назван:\n%s", out)
	}
}

// Вопроса нет ни ключом, ни пачкой: команда отбивается, а не спрашивает пустоту.
func TestAskNeedsAQuestion(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	if _, err := st.run(root, AskParams{ID: "XR-005", Wait: 10 * time.Second}); err == nil {
		t.Fatal("пустой вопрос принят")
	}
}

// runAskIn гоняет ожидание из бокового дерева задачи: признак идёт в основной
// чекаут, а опрашиваются оба входа.
func runAskIn(t *testing.T, st *askStand, root, tree string, p AskParams) (string, error) {
	t.Helper()
	st.deps.Main = root
	return runAsk(tree, p, st.deps, func(k string) string { return st.env[k] }, nil)
}

func writeRegistry(t *testing.T, home, line string) {
	t.Helper()
	path := sessions.Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

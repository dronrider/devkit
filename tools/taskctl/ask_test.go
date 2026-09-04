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

// askStand это писатель признака с подменённым внешним миром: уведомитель,
// отметка этапа и парковка записываются, а не выполняются.
type askStand struct {
	notes  []string
	stages []string
	parked []string
	deps   askDeps
	env    map[string]string
}

func newAskStand(t *testing.T) *askStand {
	t.Helper()
	st := &askStand{env: map[string]string{}}
	st.deps = askDeps{
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
	st.deps.Main = root
	return runAsk(root, p, st.deps, func(k string) string { return st.env[k] })
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

// writeDraft кладёт запись накопителя на её обычное место: по нему писатель
// узнаёт, что за ID стоит черновик, а не строка доски. Путь берётся у того же
// draftFile, каким его считают тесты накопителя.
func writeDraft(t *testing.T, root, id, title string) {
	t.Helper()
	path := draftFile(root, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# "+id+": "+title+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Признак ожидания лежит во входе задачи основного чекаута без срока
// (DK-715): первой строкой файла стоит метка «без срока», а не штамп времени,
// и панель по ней прячет обратный отсчёт. Несёт он ждущую сессию, задачу и
// пачку вопросов: по нему подхват отдаёт ждущему строки, а панель рисует блок.
func TestAskWritesTheForeverStamp(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.env[askSessionEnv] = "aaa-1"
	if _, err := st.run(root, AskParams{ID: "XR-005", Question: "поле или чип"}); err != nil {
		t.Fatal(err)
	}
	got, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005")))
	if !ok {
		t.Fatal("признак не разобрался")
	}
	if !got.Until.IsZero() {
		t.Fatalf("срок обязан остаться нулевым: %v", got.Until)
	}
	if !got.Live(time.Now().AddDate(1, 0, 0)) {
		t.Fatal("признак без срока обязан жить и через год")
	}
	if got.Task != "XR-005" || got.Session != "aaa-1" {
		t.Fatalf("шапка признака: %+v", got)
	}
	if len(got.Questions) != 1 || got.Questions[0].Text != "поле или чип" {
		t.Fatalf("вопрос в признаке: %+v", got.Questions)
	}
}

// Писатель паркует строку сразу: ждать тут больше нечему, живая сессия,
// поднявшая вопрос, уже кончила ход хуком, и ответ доедет реплике, а не этому
// вызову.
func TestAskParksAtOnce(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "нужен доступ к железу"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 1 || !strings.Contains(st.parked[0], "вопрос: нужен доступ к железу") {
		t.Fatalf("парковка вопросом: %v", st.parked)
	}
	if !strings.Contains(out, "заход кончается рубежом") {
		t.Fatalf("агенту не сказано, чем кончился заход:\n%s", out)
	}
	// Признак остаётся лежать: снимает его ответ, а не эта команда.
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005"))); !ok {
		t.Fatal("признак ожидания не остался лежать: панели нечем рисовать блок до ответа")
	}
}

// Уведомитель и отметка этапа зовутся до парковки: человек узнаёт про вопрос
// сразу, а не после того, как строка уже ушла в blocked.
func TestAskNotifiesAndStagesBeforeParking(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	if _, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть"}); err != nil {
		t.Fatal(err)
	}
	if len(st.notes) != 1 || !strings.Contains(st.notes[0], "как быть") {
		t.Fatalf("уведомление: %v", st.notes)
	}
	if len(st.stages) != 1 || !strings.Contains(st.stages[0], "вопрос: как быть") {
		t.Fatalf("отметка этапа: %v", st.stages)
	}
}

// Пачка вопросов приезжает JSON на stdin: печатается человеку столбиком и
// целиком ложится в признак.
func TestAskReadsThePackFromStdin(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	pack := `{"questions":[{"text":"ставим поле","options":[{"label":"да","recommended":true},{"label":"нет"}]},
		{"text":"кто закрывает"}]}`
	out, err := st.run(root, AskParams{ID: "XR-005", Stdin: strings.NewReader(pack)})
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
// адрес у признака общий с внешней сессией, и ответ не разминётся.
func TestAskTakesSessionFromRegistry(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	writeRegistry(t, st.deps.Home, "2026-08-19T11:00:00 сессия aaa-1 задача XR-005 проект xr дерево "+root+
		" транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-005")
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "сессия из реестра чатов") {
		t.Fatalf("реестр не назвал сессию:\n%s", out)
	}
	got, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005")))
	if !ok || got.Session != "aaa-1" {
		t.Fatalf("сессия из реестра не легла в признак: %+v %v", got, ok)
	}
}

// Реестра нет и переменной нет: признак ждёт безадресные строки, и это сказано
// первой строкой вывода, а не скрыто.
func TestAskSaysWhenSessionIsUnknown(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "сессия не назвалась") {
		t.Fatalf("молчание про неизвестную сессию:\n%s", out)
	}
}

// Черновик доски не занимает, и парковать там нечего: вопрос лежит признаком
// и репликой в разговоре, а строку блокировать некому.
func TestAskDraftDoesNotPark(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	out, err := st.run(root, AskParams{ID: "XR-009", Question: "резать или ждать", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 0 {
		t.Fatalf("черновик припарковали на доске: %v", st.parked)
	}
	if !strings.Contains(out, "доски не занимает") {
		t.Fatalf("агенту не сказали, чем кончился вопрос по черновику:\n%s", out)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-009"))); !ok {
		t.Fatal("признак черновика не лёг: панель дашборда его не увидит")
	}
}

// Вид записи писатель узнаёт сам: заказ груминга зовёт его тем же
// `taskctl ask <ID>`, что и задачу, без всякого флага. Строки на доске нет, и
// парковка отвечала бы «нет на доске».
func TestAskGuessesTheDraft(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.deps.IsDraft = func(id string) bool {
		_, err := os.Stat(draftFile(root, id))
		return err == nil
	}
	writeDraft(t, root, "XR-D8", "вторая мысль")
	out, err := st.run(root, AskParams{ID: "XR-D8", Question: "заводить строкой"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 0 {
		t.Fatalf("парковку позвали по черновику: %v", st.parked)
	}
	if !strings.Contains(out, "запись накопителя") {
		t.Fatalf("агенту не сказали, что ждём по-черновому:\n%s", out)
	}
	// Признак ожидания лежал под тем же именем, под которым его читает
	// дашборд: иначе чип ожидания у записи не зажёгся бы.
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-D8"))); !ok {
		t.Fatal("признак угаданного черновика не лёг под именем задачи")
	}
}

// Строка доски черновиком не считается, даже если IsDraft ничего не нашёл: у
// задачи свой исход, парковка.
func TestAskKeepsTheTaskPath(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.deps.IsDraft = func(id string) bool { return false }
	if _, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть"}); err != nil {
		t.Fatal(err)
	}
	if len(st.parked) != 1 {
		t.Fatalf("задачу не припарковали: %v", st.parked)
	}
}

// Парковка ответила отказом (потолок висящих вопросов, неуехавшая доска):
// заход от этого не падает, вопрос уже задан и признак уже лежит, но отказ
// назван, и агента отправляют смотреть строку.
func TestAskSurvivesTheRefusedParking(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.deps.Park = func(id, reason string) (string, error) {
		return "", errors.New("вопросов висит 2 из 2")
	}
	out, err := st.run(root, AskParams{ID: "XR-005", Question: "как быть"})
	if err != nil {
		t.Fatalf("отказ парковки уронил заход: %v", err)
	}
	if !strings.Contains(out, "вопросов висит 2 из 2") || !strings.Contains(out, "taskctl show XR-005") {
		t.Fatalf("отказ парковки не назван:\n%s", out)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005"))); !ok {
		t.Fatal("признак снят при отказе парковки, а вопрос уже был задан")
	}
}

// Вопроса нет ни ключом, ни пачкой: команда отбивается, а не спрашивает
// пустоту, и признака после отказа не остаётся.
func TestAskNeedsAQuestion(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	if _, err := st.run(root, AskParams{ID: "XR-005"}); err == nil {
		t.Fatal("пустой вопрос принят")
	}
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005"))); ok {
		t.Fatal("отбитый вопрос всё равно оставил признак")
	}
}

// Пустой ID это отказ до всякой записи: спрашивать нечего, некому.
func TestAskNeedsAnID(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	if _, err := st.run(root, AskParams{Question: "как быть"}); err == nil {
		t.Fatal("пустой ID принят")
	}
}

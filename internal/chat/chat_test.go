package chat

import (
	"os"
	"strings"
	"testing"
	"time"
)

func at() time.Time { return time.Date(2026, 8, 19, 12, 30, 0, 0, time.Local) }

// Реплика задаче идёт без адресата, реплика сессии с ним: на этой паре стоит
// всё правило адресности, и сторожок считает ответом задаче только первую.
func TestLineAddressee(t *testing.T) {
	task := TaskLine(at(), "вот схема, продолжай")
	if to := Addressee(task); to != "" {
		t.Fatalf("реплика задаче ушла с адресатом %q: %s", to, task)
	}
	sess := Line(at(), "aaa-1", "стой, не туда")
	if to := Addressee(sess); to != "aaa-1" {
		t.Fatalf("адресат реплики сессии %q: %s", to, sess)
	}
	if said := Said(sess); said != "стой, не туда" {
		t.Fatalf("текст реплики %q", said)
	}
	if said := Said("рукописная строка"); said != "рукописная строка" {
		t.Fatalf("рукописная строка читается как есть, а вышло %q", said)
	}
}

// Повтор той же реплики второй строки не заводит: сессия прочитала бы сказанное
// дважды.
func TestPutSkipsTheLyingCopy(t *testing.T) {
	tree := t.TempDir()
	if lying, err := Put(tree, "task-XR-1", "вот схема", TaskLine(at(), "вот схема")); err != nil || lying != "" {
		t.Fatalf("первая строка: lying=%q err=%v", lying, err)
	}
	lying, err := Put(tree, "task-XR-1", "вот схема", TaskLine(at().Add(time.Minute), "вот схема"))
	if err != nil {
		t.Fatal(err)
	}
	if lying == "" {
		t.Fatal("повтор завёл вторую строку")
	}
	if got := len(ReadLines(Path(tree, "task-XR-1"))); got != 1 {
		t.Fatalf("строк во входе %d, ожидал одну", got)
	}
}

// Ждущий забирает своё и только своё: безадресную строку и адресованную себе.
// Реплика чужой сессии остаётся лежать, её разнесёт подхват своим ходом.
func TestTakeLeavesForeignLines(t *testing.T) {
	tree := t.TempDir()
	mustPut(t, tree, "task-XR-1", TaskLine(at(), "ответ задаче"))
	mustPut(t, tree, "task-XR-1", Line(at(), "aaa-1", "это ждущему"))
	mustPut(t, tree, "task-XR-1", Line(at(), "bbb-2", "это окну человека"))
	mine, err := Take(tree, "task-XR-1", "aaa-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Fatalf("забрано строк %d: %v", len(mine), mine)
	}
	rest := ReadLines(Path(tree, "task-XR-1"))
	if len(rest) != 1 || Addressee(rest[0]) != "bbb-2" {
		t.Fatalf("во входе осталось %v, ожидал одну реплику сессии bbb-2", rest)
	}
}

// Пустой вход это «сказать нечего», а не ошибка: ожидание опрашивает его раз в
// секунду.
func TestTakeOnEmptyChat(t *testing.T) {
	tree := t.TempDir()
	mine, err := Take(tree, "task-XR-1", "aaa-1")
	if err != nil || mine != nil {
		t.Fatalf("пустой вход: %v %v", mine, err)
	}
}

// Забранное уходит из входа целиком, и пустой файл убирается: лежащая строка
// всегда непрочитанная, отметок доставки у входа нет.
func TestTakeRemovesEmptiedChat(t *testing.T) {
	tree := t.TempDir()
	mustPut(t, tree, "task-XR-1", TaskLine(at(), "ответ задаче"))
	if _, err := Take(tree, "task-XR-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(tree, "task-XR-1")); !os.IsNotExist(err) {
		t.Fatalf("опустевший вход остался: %v", err)
	}
}

// Признак ожидания: срок первой строкой ради подхвата и сторожка, ниже ждущая
// сессия, задача и пачка вопросов.
func TestAskRoundTrip(t *testing.T) {
	tree := t.TempDir()
	want := Ask{
		Until:   at(),
		Session: "aaa-1",
		Task:    "XR-1",
		Questions: []Question{{Text: "ставим поле или чип", Options: []Option{
			{Label: "поле", Note: "видно в API", Recommended: true}, {Label: "чип"}}}},
	}
	if err := WriteAsk(tree, "task-XR-1", want); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(AskPath(tree, "task-XR-1"))
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(string(body), "\n", 2)[0]
	if _, err := time.ParseInLocation(Stamp, first, time.Local); err != nil {
		t.Fatalf("первой строкой обязан стоять срок, а стоит %q: подхват читает одну строку", first)
	}
	got, ok := ReadAsk(AskPath(tree, "task-XR-1"))
	if !ok {
		t.Fatal("признак не разобрался")
	}
	if !got.Until.Equal(want.Until) || got.Session != "aaa-1" || got.Task != "XR-1" {
		t.Fatalf("шапка признака: %+v", got)
	}
	if len(got.Questions) != 1 || got.Questions[0].Text != want.Questions[0].Text ||
		len(got.Questions[0].Options) != 2 || !got.Questions[0].Options[0].Recommended {
		t.Fatalf("пачка вопросов: %+v", got.Questions)
	}
}

// Однострочный признак цели читается тем же разбором: формат один, и вторая
// копия его разбора разъехалась бы с первой.
func TestParseAskOneLine(t *testing.T) {
	a, ok := ParseAsk(at().Format(Stamp) + "\n")
	if !ok || a.Session != "" || len(a.Questions) != 0 {
		t.Fatalf("однострочный признак: %+v %v", a, ok)
	}
	if _, ok := ParseAsk("вовсе не срок\n"); ok {
		t.Fatal("неразобранный срок это отсутствие ожидания, а не нулевое время")
	}
}

// Снятие признака идёт на любом выходе, включая падение, поэтому второй заход
// уборки штатен.
func TestDropAskTwice(t *testing.T) {
	tree := t.TempDir()
	if err := WriteAsk(tree, "task-XR-1", Ask{Until: at()}); err != nil {
		t.Fatal(err)
	}
	if err := DropAsk(tree, "task-XR-1"); err != nil {
		t.Fatal(err)
	}
	if err := DropAsk(tree, "task-XR-1"); err != nil {
		t.Fatalf("повторное снятие признака отбито: %v", err)
	}
}

func TestParsePack(t *testing.T) {
	qs, err := ParsePack([]byte(`{"questions":[{"text":"так или эдак","options":[{"label":"так"}]}]}`))
	if err != nil || len(qs) != 1 || qs[0].Options[0].Label != "так" {
		t.Fatalf("пачка объектом: %v %v", qs, err)
	}
	if qs, err := ParsePack([]byte(`[{"text":"голым списком"}]`)); err != nil || len(qs) != 1 {
		t.Fatalf("пачка списком: %v %v", qs, err)
	}
	if _, err := ParsePack([]byte(`{"questions":[{"text":"a"},{"text":"b"},{"text":"c"},{"text":"d"},{"text":"e"}]}`)); err == nil {
		t.Fatalf("потолок пачки %d не сработал", PackLimit)
	}
	if _, err := ParsePack([]byte("  ")); err == nil {
		t.Fatal("пустой stdin принят за пачку")
	}
	if _, err := ParsePack([]byte(`{"questions":[{"text":"  "}]}`)); err == nil {
		t.Fatal("вопрос без текста принят: человеку показывать нечего")
	}
}

func mustPut(t *testing.T, tree, name, line string) {
	t.Helper()
	if _, err := Put(tree, name, Said(line), line); err != nil {
		t.Fatal(err)
	}
}

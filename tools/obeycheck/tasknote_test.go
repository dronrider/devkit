package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/taskform"
)

func standNote(t *testing.T, scen []Scenario, base string) note {
	t.Helper()
	return note{
		Task:      "DK-836",
		Devkit:    devkitRoot(t),
		Tier:      "mini",
		Base:      base,
		Repeats:   5,
		Command:   "obeycheck --task DK-836 --for RULES.core.md -k 5 ../cand ../base",
		Table:     "сценарий  cand  base  вердикт\nнажать кнопку  5/5  1/5  польза",
		Scenarios: scen,
		Now:       time.Date(2026, 9, 5, 14, 10, 0, 0, time.UTC),
	}
}

func taskDoc(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "DK-836.md")
	if err := os.WriteFile(p, []byte("# DK-836\n\n## Проверка\n\nШаги гоняются из корня чекаута.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// След прогона пишет стенд, а не автор: в «Проверку» ложится отметка с
// машинными полями и под ней команда с таблицей.
func TestTaskNoteWritesMark(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	doc := read(t, p)
	marks := taskform.StandMarks(doc)
	if len(marks) != 1 {
		t.Fatalf("отметок в файле задачи %d:\n%s", len(marks), doc)
	}
	m := marks[0]
	if m.Failed || m.Base != baseOld || m.Tier != "mini" || m.Repeats != 5 ||
		strings.Join(m.Scenarios, ",") != "press" {
		t.Fatalf("поля отметки: %+v", m)
	}
	want, err := obey.Print(devkitRoot(t), runSubjects(n.Scenarios))
	if err != nil {
		t.Fatal(err)
	}
	if m.Print != want {
		t.Fatalf("отпечаток отметки %s, у дерева %s", m.Print, want)
	}
	if !strings.Contains(doc, "$ obeycheck --task DK-836") || !strings.Contains(doc, "нажать кнопку  5/5  1/5") {
		t.Fatalf("команды и таблицы под отметкой нет:\n%s", doc)
	}
	if !strings.Contains(doc, "Шаги гоняются из корня чекаута.") {
		t.Fatalf("проза раздела снесена:\n%s", doc)
	}
}

// Незачтённый прогон отмечается своей строкой: ворота она не открывает, а
// разбирать провал ревьюверу всё равно нужно.
func TestTaskNoteFailedMark(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseEmpty)
	n.Failed = true
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	doc := read(t, p)
	if !strings.Contains(doc, taskform.StandFailNote) || !strings.Contains(doc, "ворота закрыты") {
		t.Fatalf("красной отметки нет:\n%s", doc)
	}
}

// Ключ записи это сценарии вместе с базой: повторный прогон заменяет свою
// прошлую запись, а прогон с другой базой ложится рядом.
func TestTaskNoteReplacesSameKey(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	again := n
	again.Now = n.Now.Add(time.Hour)
	again.Table = "сценарий  cand  base  вердикт\nнажать кнопку  5/5  5/5  зелёный на обеих"
	if err := again.write(p); err != nil {
		t.Fatal(err)
	}
	doc := read(t, p)
	if n := len(taskform.StandMarks(doc)); n != 1 {
		t.Fatalf("после повторного прогона отметок %d:\n%s", n, doc)
	}
	if strings.Contains(doc, "1/5  польза") {
		t.Fatalf("таблица прошлого прогона осталась:\n%s", doc)
	}
	if !strings.Contains(doc, "15:10") {
		t.Fatalf("время новой записи не встало:\n%s", doc)
	}
	other := n
	other.Base = baseEmpty
	if err := other.write(p); err != nil {
		t.Fatal(err)
	}
	marks := taskform.StandMarks(read(t, p))
	if len(marks) != 2 {
		t.Fatalf("прогон с другой базой затёр соседа: %+v", marks)
	}
}

// Разведка следом не считается: одна и две сессии на раскладку не отличают
// правку от случайности ни при каком тесте.
func TestTaskNoteRefusesScouting(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	n.Repeats = 2
	err := n.write(p)
	if err == nil || !strings.Contains(err.Error(), "разведка") {
		t.Fatalf("ждал отказ на разведке, получил: %v", err)
	}
	if strings.Contains(read(t, p), taskform.StandNote) {
		t.Fatal("отметка на разведке всё-таки записана")
	}
}

// Три повтора это нижняя граница замера, а не разведки: ровно minTaskRepeats
// отметку писать не мешает.
func TestTaskNoteAcceptsMinimumRepeats(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	n.Repeats = minTaskRepeats
	if err := n.write(p); err != nil {
		t.Fatalf("отказ на границе k=%d: %v", minTaskRepeats, err)
	}
	if !strings.Contains(read(t, p), taskform.StandNote) {
		t.Fatal("отметка на границе k не записана")
	}
}

// Файл задачи ищется в корне репозитория текущей директории: стенд, позванный
// не из дерева задачи, следу писать некуда.
func TestTaskFileMissing(t *testing.T) {
	if _, err := taskFile(devkitRoot(t), "DK-000000"); err == nil ||
		!strings.Contains(err.Error(), "дерева задачи") {
		t.Fatalf("ждал отказ на отсутствующем файле задачи, получил: %v", err)
	}
	if _, err := taskFile(t.TempDir(), "DK-836"); err == nil {
		t.Fatal("вне репозитория файл задачи находиться не должен")
	}
}

// Раскладка-кандидат, собранная до правки, ловится по тексту предмета:
// отказом это не делается, но предупреждение видно и в консоли, и под отметкой.
func TestMissingInLayoutWarns(t *testing.T) {
	root := devkitRoot(t)
	sub := []obey.Subject{{Path: "RULES.core.md", Section: "Символы"}}
	stale := t.TempDir()
	if err := os.WriteFile(filepath.Join(stale, "RULES.md"), []byte("совсем другой текст\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	warn := missingInLayout(stale, root, sub)
	if len(warn) != 1 || !strings.Contains(warn[0], "раскладка собрана до правки") {
		t.Fatalf("предупреждения нет: %v", warn)
	}
	// Тот же текст, переверстанный по другой ширине, ложной тревоги не даёт.
	fresh := t.TempDir()
	text, err := obey.Text(root, sub[0])
	if err != nil {
		t.Fatal(err)
	}
	reflowed := strings.Join(strings.Fields(text), "\n")
	if err := os.WriteFile(filepath.Join(fresh, "RULES.core.md"), []byte(reflowed), 0o644); err != nil {
		t.Fatal(err)
	}
	if warn := missingInLayout(fresh, root, sub); len(warn) != 0 {
		t.Fatalf("перевёрстка дала ложную тревогу: %v", warn)
	}
	// Скиллы стенд берёт из дерева сам, и в раскладке их не ищет.
	if warn := missingInLayout(stale, root, []obey.Subject{{Path: "kit/skills/prose/SKILL.md"}}); len(warn) != 0 {
		t.Fatalf("скилл искали в раскладке: %v", warn)
	}
}

// Предупреждение доезжает до ревьювера: оно ложится в файл задачи под
// отметкой, а не остаётся в консоли автора.
func TestTaskNoteCarriesWarning(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	n.Warnings = []string{"текст предмета RULES.core.md в раскладке-кандидате не найден, раскладка собрана до правки?"}
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	if doc := read(t, p); !strings.Contains(doc, "Предупреждение: текст предмета RULES.core.md") {
		t.Fatalf("предупреждения под отметкой нет:\n%s", doc)
	}
}

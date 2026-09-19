package main

import (
	"os"
	"os/exec"
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

// Повторный прогон заменяет запись и тогда, когда у прошлой было
// предупреждение о несвежей раскладке: такие записи лежат в старых файлах
// задач (DK-996), новых стенд не пишет. Пара условий разом: ключ тот же, а
// строка предупреждения стоит между отметкой и таблицей вне ограждения.
func TestTaskNoteReplacesRecordWithWarning(t *testing.T) {
	p := taskDoc(t)
	first := standNote(t, scenarios(t, "press"), baseOld)
	if err := first.write(p); err != nil {
		t.Fatal(err)
	}
	doc := read(t, p)
	marks := taskform.StandMarks(doc)
	if len(marks) != 1 {
		t.Fatalf("отметок после первого прогона %d:\n%s", len(marks), doc)
	}
	warn := taskform.WarnLine + "текст предмета RULES.core.md в раскладке-кандидате не найден, раскладка собрана до правки?"
	doc = strings.Replace(doc, "\n```console", "\n"+warn+"\n\n```console", 1)
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	again := standNote(t, scenarios(t, "press"), baseOld)
	again.Now = first.Now.Add(time.Hour)
	again.Table = "сценарий  cand  base  вердикт\nнажать кнопку  5/5  5/5  зелёный на обеих"
	if err := again.write(p); err != nil {
		t.Fatal(err)
	}
	doc = read(t, p)
	if n := len(taskform.StandMarks(doc)); n != 1 {
		t.Fatalf("отметок после повторного прогона %d:\n%s", n, doc)
	}
	if n := strings.Count(doc, "```console"); n != 1 {
		t.Fatalf("блоков с таблицей %d, ждал один:\n%s", n, doc)
	}
	if strings.Contains(doc, taskform.WarnLine) {
		t.Fatalf("предупреждение прошлого прогона осталось:\n%s", doc)
	}
	if strings.Contains(doc, "1/5  польза") {
		t.Fatalf("таблица прошлого прогона осталась:\n%s", doc)
	}
}

// Таблица со спецсимволами разбора судьи коммитится нормализованной: рубеж
// hooks/check-symbols.py молчит на записанном файле без правки руками
// (DK-1056). У записи с судейской секцией под таблицей стоит строка про
// нормализацию и каталог прогона.
func TestTaskNoteNormalizesTableForFile(t *testing.T) {
	p := taskDoc(t)
	dirty := "цитата: " + string(rune(0x201c)) + "было" + string(rune(0x2014)) + "стало" + string(rune(0x201d))
	n := standNote(t, scenarios(t, "judge"), baseOld)
	n.Table = "сценарий  cand  base  вердикт\nсказать о себе  3/3  0/3  польза\n\nразбор судьи:\n  judge / cand / повтор 1: " + dirty
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	doc := read(t, p)
	if strings.Contains(doc, dirty) {
		t.Fatalf("цитата судьи попала в файл без нормализации:\n%s", doc)
	}
	if !strings.Contains(doc, "было - стало") {
		t.Fatalf("длинное тире цитаты не приведено к клавиатуре:\n%s", doc)
	}
	if !strings.Contains(doc, judgeAnswerNote) {
		t.Fatalf("строки про нормализацию и каталог прогона нет:\n%s", doc)
	}
	if code := runSymbolsHook(t, doc); code != 0 {
		t.Fatalf("рубеж символов против записанного файла (код %d):\n%s", code, doc)
	}
}

// Без судейской секции строка про нормализацию не нужна: нормализовать
// нечего в цитатах, которых не было, а дисклеймер был бы лишним.
func TestTaskNoteSkipsNoteWithoutJudge(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, p), judgeAnswerNote) {
		t.Fatal("строка про нормализацию появилась без судейской секции")
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

// Файл задачи ищется сперва в дереве --devkit, а не найден там, от startDir
// тем же путём, каким стенд находит журнал прогонов: стенд, позванный не из
// дерева задачи ни тем, ни другим путём, следу писать некуда.
func TestTaskFileMissing(t *testing.T) {
	if _, err := taskFile(devkitRoot(t), devkitRoot(t), "DK-000000"); err == nil ||
		!strings.Contains(err.Error(), "дерева задачи") {
		t.Fatalf("ждал отказ на отсутствующем файле задачи, получил: %v", err)
	}
	if _, err := taskFile(t.TempDir(), t.TempDir(), "DK-836"); err == nil {
		t.Fatal("вне репозитория файл задачи находиться не должен")
	}
}

// Дерево --devkit и дерево cwd разные: файл задачи ищется сперва в дереве
// --devkit, cwd роли не играет. До правки DK-1055 taskFile звали с "." вместо
// root, и след искался только от cwd.
func TestTaskFilePrefersDevkitTreeOverCwd(t *testing.T) {
	devkit := t.TempDir()
	want := filepath.Join(devkit, "docs", "tasks", "DK-836.md")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("# DK-836\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if out, err := exec.Command("git", "-C", cwd, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	got, err := taskFile(devkit, cwd, "DK-836")
	if err != nil {
		t.Fatalf("ждал файл из дерева --devkit, получил отказ: %v", err)
	}
	if got != want {
		t.Fatalf("путь %s, ждал %s из дерева --devkit", got, want)
	}
}

// Раскладка-кандидат, собранная до правки, ловится по тексту предмета.
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

// Предмет, которого в раскладке-кандидате нет, это отказ прогону с --task, а
// не предупреждение под зачтённой отметкой: раскладка full без ядра давала
// DK-996 зелёный след по правилу, которого сессия не читала (DK-1025).
// Причина печатается той же фразой, что раньше шла предупреждением.
func TestStaleLayoutRefuses(t *testing.T) {
	root := devkitRoot(t)
	sub := []obey.Subject{{Path: "RULES.core.md", Section: "Символы"}}
	stale := t.TempDir()
	if err := os.WriteFile(filepath.Join(stale, "RULES.md"), []byte("разбор без ядра\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := staleLayout(stale, root, sub)
	if err == nil {
		t.Fatal("раскладка без текста предмета прошла")
	}
	for _, want := range []string{"след не пишется", "текст предмета RULES.core.md «Символы»", "раскладка собрана до правки"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	fresh := t.TempDir()
	text, err := obey.Text(root, sub[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "RULES.core.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := staleLayout(fresh, root, sub); err != nil {
		t.Fatalf("свежая раскладка отбита: %v", err)
	}
}

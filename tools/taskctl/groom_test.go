package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runTaskctl гоняет собранный бинарь по синтетической доске: разбор аргументов
// живёт в main и на отказе выходит из процесса, из теста функцией его не позвать.
func runTaskctl(bin, dir string, args ...string) (string, error) {
	out, err := exec.Command(bin, append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}

// today это дата, которую команды разбора пишут в файл черновика.
func today() string { return time.Now().Format(draftDateLayout) }

// newDraft заводит черновик на синтетической доске и возвращает его ID.
func newDraft(t *testing.T, root, text string) string {
	t.Helper()
	msg, err := cmdDraft(root, text, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(msg, ":")
	return id
}

func draftBody(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(draftFile(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDraftDeferWritesMark: пометка живёт разделом «Грумминг» в самом файле
// черновика, повторный вызов причину заменяет, а не копит вторую строку.
func TestDraftDeferWritesMark(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы")

	msg, err := cmdDraftDefer(root, id, "ждём повторного случая с git status", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "отложен "+today()) {
		t.Fatalf("сообщение команды: %q", msg)
	}
	body := draftBody(t, root, id)
	want := fmt.Sprintf("\n%s\n\n- %s, отложен: ждём повторного случая с git status\n", draftGroomHeading, today())
	if !strings.HasSuffix(body, want) {
		t.Fatalf("файл черновика:\n%s\nждал в хвосте:\n%s", body, want)
	}
	if !strings.Contains(body, "## Черновик\n\nуведомитель шумит из песочницы") {
		t.Fatalf("пометка съела текст черновика:\n%s", body)
	}

	if _, err := cmdDraftDefer(root, id, "ждём ответа смежника", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body = draftBody(t, root, id)
	if n := strings.Count(body, ", отложен: "); n != 1 {
		t.Fatalf("повторный defer оставил %d строк пометки:\n%s", n, body)
	}
	if !strings.Contains(body, "ждём ответа смежника") || strings.Contains(body, "git status") {
		t.Fatalf("причина не заменилась:\n%s", body)
	}
	if n := strings.Count(body, draftGroomHeading); n != 1 {
		t.Fatalf("заголовков раздела %d:\n%s", n, body)
	}

	if _, err := cmdDraftDefer(root, id, "", false, CommitOpts{}); err == nil {
		t.Fatal("defer без причины должен отказывать")
	}
	if _, err := cmdDraftDefer(root, "XR-404", "причина", false, CommitOpts{}); err == nil {
		t.Fatal("defer по чужому ID должен отказывать")
	}
}

// TestDraftDeferCommitsOnlyDraft: коммит забирает файл черновика и ничего
// больше, доска пометкой не двигается.
func TestDraftDeferCommitsOnlyDraft(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "идея на потом")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")

	msg, err := cmdDraftDefer(root, id, "ждём релиза смежника", false, CommitOpts{Msg: "docs(tasks): " + id + " отложен"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, ", коммит ") {
		t.Fatalf("сообщение без хеша коммита: %q", msg)
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != draftRel(id) {
		t.Fatalf("в коммите не только черновик: %q", files)
	}
}

// TestDraftDeferClear: --clear снимает раздел целиком, вместе с заголовком, и
// на черновике без пометки не падает.
func TestDraftDeferClear(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы")
	before := draftBody(t, root, id)

	msg, err := cmdDraftDefer(root, id, "", true, CommitOpts{})
	if err != nil {
		t.Fatalf("--clear без пометки не должен падать: %v", err)
	}
	if !strings.Contains(msg, "пометки об отложенном не было") {
		t.Fatalf("сообщение: %q", msg)
	}
	if body := draftBody(t, root, id); body != before {
		t.Fatalf("холостой --clear тронул файл:\n%s", body)
	}

	if _, err := cmdDraftDefer(root, id, "ждём повода", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftDefer(root, id, "", true, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body := draftBody(t, root, id)
	if strings.Contains(body, draftGroomHeading) || strings.Contains(body, "отложен") {
		t.Fatalf("--clear оставил след раздела:\n%s", body)
	}
	if !strings.Contains(body, "уведомитель шумит из песочницы") {
		t.Fatalf("--clear унёс текст черновика:\n%s", body)
	}
	if _, err := cmdDraftDefer(root, id, "причина", true, CommitOpts{}); err == nil {
		t.Fatal("--clear с причиной должен отказывать")
	}
}

// TestDraftListShowsDeferMark: заход по накопителю начинается с draft list, и
// без пометки в выводе процедура открывала бы все файлы, чтобы узнать, кого уже
// разбирали.
func TestDraftListShowsDeferMark(t *testing.T) {
	root := setup(t)
	first := newDraft(t, root, "первая идея")
	second := newDraft(t, root, "вторая идея")
	ageDraft(t, root, first, 3)
	if _, err := cmdDraftDefer(root, first, "ждём повторного случая", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}

	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := fmt.Sprintf("%s (3 дня, отложен %s): первая идея", first, today())
	if !strings.Contains(out, wantFirst) {
		t.Fatalf("draft list:\n%s\nждал строку: %s", out, wantFirst)
	}
	if !strings.Contains(out, second+" (сегодня): вторая идея") {
		t.Fatalf("неотложенный черновик печатается иначе, чем раньше:\n%s", out)
	}

	list, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, fmt.Sprintf("%s (3 дня, отложен), %s (сегодня)", first, second)) {
		t.Fatalf("хвост list:\n%s", list)
	}
}

// TestDraftAgeSurvivesDefer: возраст считается по строке «записан», а не по
// времени правки, иначе первая же пометка делала бы трёхдневный черновик
// сегодняшним, то есть ломала бы ровно тот счётчик, ради которого заводится.
func TestDraftAgeSurvivesDefer(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "старая идея")
	ageDraft(t, root, id, 5)

	if _, err := cmdDraftDefer(root, id, "ждём повода", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (5 дней, отложен") {
		t.Fatalf("возраст сбит пометкой:\n%s", out)
	}
}

// TestDraftAgeFallsBackToModTime: черновик, записанный до появления строки
// «записан», считается по времени правки, а первая же запись в него эту строку
// проставляет, пока время правки ещё то самое.
func TestDraftAgeFallsBackToModTime(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "черновик старого формата")
	// Файл без строки «записан»: ровно то, что лежит в живом накопителе.
	body := "# " + id + ": черновик старого формата\n\n## Черновик\n\nчерновик старого формата\n"
	if err := os.WriteFile(draftFile(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -4)
	if err := os.Chtimes(draftFile(root, id), old, old); err != nil {
		t.Fatal(err)
	}

	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (4 дня): черновик старого формата") {
		t.Fatalf("фолбэк на время правки не сработал:\n%s", out)
	}

	msg, err := cmdDraftDefer(root, id, "ждём повода", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "дата записи проставлена ("+old.Format(draftDateLayout)+")") {
		t.Fatalf("команда молчит про проставленную дату: %q", msg)
	}
	if got := draftBody(t, root, id); !strings.Contains(got, draftWrittenPrefix+old.Format(draftDateLayout)) {
		t.Fatalf("строка «записан» не проставлена:\n%s", got)
	}
	out, err = cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id+" (4 дня, отложен") {
		t.Fatalf("после пометки возраст сбился:\n%s", out)
	}
}

// TestDraftAttachToTaskWithFile: у задачи с готовым файлом путей два, доска не
// трогается вовсе.
func TestDraftAttachToTaskWithFile(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "своя репродукция через -m")
	if _, err := cmdDraftDefer(root, id, "ждём разбора", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")
	boardBefore, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}

	msg, err := cmdDraftAttach(root, id, "XR-002", CommitOpts{Msg: fmt.Sprintf("docs(tasks): %s уехал в XR-002", id)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "приписан к XR-002") {
		t.Fatalf("сообщение: %q", msg)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf("## Из черновика %s (%s%s)", id, draftWrittenPrefix, today())
	for _, want := range []string{head, "своя репродукция через -m", "### Черновик", "отложен: ждём разбора"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("в файле задачи нет %q:\n%s", want, data)
		}
	}
	// Дата записи живёт в заголовке раздела, а голой строкой в теле она
	// повисла бы без контекста: черновик её несёт всегда, значит это штатный
	// вид приписки, а не краевой случай.
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == draftWrittenPrefix+today() {
			t.Fatalf("строка даты записи уехала в тело раздела:\n%s", data)
		}
	}
	if strings.Contains(string(data), "\n# "+id) {
		t.Fatalf("заголовок черновика уехал вторым H1:\n%s", data)
	}
	if _, err := os.Stat(draftFile(root, id)); !os.IsNotExist(err) {
		t.Fatalf("черновик остался на месте: %v", err)
	}
	boardAfter, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(boardAfter) != string(boardBefore) {
		t.Fatal("attach на задачу с файлом тронул доску")
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	for _, want := range []string{"docs/tasks/XR-002.md", draftRel(id)} {
		if !strings.Contains(files, want) {
			t.Fatalf("в коммите нет %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, "docs/TASKS.md") {
		t.Fatalf("доска уехала в коммит:\n%s", files)
	}
	if st := gitOut(t, root, "status", "--porcelain", "docs"); st != "" {
		t.Fatalf("после attach в docs/ осталось незакоммиченное: %q", st)
	}
}

// TestDraftAttachCreatesTaskFile: файла у задачи не было, значит путей три, и
// третий это доска со ссылкой, переставленной на новый файл (прецедент
// cmdReviewAdd). Строка XR-004 до этого ссылается на чужой файл, как строки
// задач цели ссылаются на файл цели: ссылка переезжает, и это ожидаемый исход.
func TestDraftAttachCreatesTaskFile(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Link: "[tasks/XR-005.md](tasks/XR-005.md)"}); err != nil {
		t.Fatal(err)
	}
	id := newDraft(t, root, "то же семейство, что у хвоста")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")

	msg, err := cmdDraftAttach(root, id, "XR-004", CommitOpts{Msg: fmt.Sprintf("docs(tasks): %s уехал в XR-004", id)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файл docs/tasks/XR-004.md создан") {
		t.Fatalf("сообщение: %q", msg)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if row := b.find("XR-004"); row == nil || row.Link != "[tasks/XR-004.md](tasks/XR-004.md)" {
		t.Fatalf("ссылка в строке: %+v", row)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-004"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "то же семейство, что у хвоста") {
		t.Fatalf("текст черновика потерян:\n%s", data)
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	for _, want := range []string{"docs/TASKS.md", "docs/tasks/XR-004.md", draftRel(id)} {
		if !strings.Contains(files, want) {
			t.Fatalf("в коммите нет %s:\n%s", want, files)
		}
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("после attach lint должен быть зелёным: %v", finds)
	}
}

// TestDraftAttachUnknownTask: приписывать некуда, значит команда отказывает,
// ничего не тронув.
func TestDraftAttachUnknownTask(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "идея")
	if _, err := cmdDraftAttach(root, id, "XR-404", CommitOpts{}); err == nil {
		t.Fatal("attach на задачу вне доски должен отказывать")
	}
	if _, err := os.Stat(draftFile(root, id)); err != nil {
		t.Fatalf("отказавший attach унёс черновик: %v", err)
	}
	if _, err := cmdDraftAttach(root, "XR-404", "XR-002", CommitOpts{}); err == nil {
		t.Fatal("attach несуществующего черновика должен отказывать")
	}
}

// TestDraftDrop: протухший черновик удаляется, удаление уезжает в индекс (без
// этого коммит команды его не заберёт), а без причины команда отказывает.
func TestDraftDrop(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	id := newDraft(t, root, "предмета больше нет")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "черновик")

	if _, err := cmdDraftDrop(root, id, "  ", CommitOpts{}); err == nil {
		t.Fatal("drop без причины должен отказывать")
	}
	if _, err := os.Stat(draftFile(root, id)); err != nil {
		t.Fatalf("отказавший drop тронул файл: %v", err)
	}

	msg, err := cmdDraftDrop(root, id, "код давно переписан, репродукции нет", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "код давно переписан") {
		t.Fatalf("команда не печатает причину: %q", msg)
	}
	if _, err := os.Stat(draftFile(root, id)); !os.IsNotExist(err) {
		t.Fatalf("файл черновика на месте: %v", err)
	}
	if st := gitOut(t, root, "diff", "--cached", "--name-status"); !strings.Contains(st, "D\t"+draftRel(id)) {
		t.Fatalf("удаление не в индексе: %q", st)
	}
}

// TestDraftSectionDemotesAllHeadings: у приписки опускаются заголовки любого
// уровня, иначе «### » черновика встало бы вровень с опущенным «## », а решётка
// внутри блока кода это текст примера, и её трогать нечем.
func TestDraftSectionDemotesAllHeadings(t *testing.T) {
	text := "# XR-008: заголовок\n\nзаписан 2026-08-05\n\n## Черновик\n\nтело\n\n### Подробности\n\n```\n## это пример, а не заголовок\n```\n"
	got := draftSection("XR-008", text)
	for _, want := range []string{
		"## Из черновика XR-008 (записан 2026-08-05)",
		"\n### Черновик\n",
		"\n#### Подробности\n",
		"\n## это пример, а не заголовок\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("в разделе нет %q:\n%s", want, got)
		}
	}
}

// TestDraftUncommittedRemoved: черновик, заведённый и ещё не закоммиченный
// (штатный ход, когда разбор идёт следом за записью), гиту неизвестен, git rm
// по нему падает. Такой файл удаляется обычным rm и в pathspec коммита не едет,
// иначе git commit валился бы на нём целиком, унося и правку файла задачи.
func TestDraftUncommittedRemoved(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)

	dropped := newDraft(t, root, "записал и сразу выбросил")
	msg, err := cmdDraftDrop(root, dropped, "предмета нет", CommitOpts{Msg: "docs(tasks): " + dropped + " удалён"})
	if err != nil {
		t.Fatalf("drop незакоммиченного черновика: %v", err)
	}
	if strings.Contains(msg, ", коммит ") {
		t.Fatalf("коммитить нечего, черновика гит не знал: %q", msg)
	}
	if _, err := os.Stat(draftFile(root, dropped)); !os.IsNotExist(err) {
		t.Fatalf("файл черновика на месте: %v", err)
	}

	attached := newDraft(t, root, "записал и сразу приписал")
	head := gitOut(t, root, "rev-parse", "HEAD")
	if _, err := cmdDraftAttach(root, attached, "XR-002", CommitOpts{Msg: "docs(tasks): " + attached + " уехал в XR-002"}); err != nil {
		t.Fatalf("attach незакоммиченного черновика: %v", err)
	}
	if _, err := os.Stat(draftFile(root, attached)); !os.IsNotExist(err) {
		t.Fatalf("файл черновика на месте: %v", err)
	}
	if gitOut(t, root, "rev-parse", "HEAD") == head {
		t.Fatal("attach не создал коммита с файлом задачи")
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != "docs/tasks/XR-002.md" {
		t.Fatalf("в коммите не только файл задачи: %q", files)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-002"))
	if err != nil || !strings.Contains(string(data), "записал и сразу приписал") {
		t.Fatalf("текст черновика не доехал: %q, %v", data, err)
	}
	if st := gitOut(t, root, "status", "--porcelain", "docs"); st != "" {
		t.Fatalf("после разбора в docs/ осталось незакоммиченное: %q", st)
	}
}

// TestDraftPromoteKeepsGroom: оформление отложенного черновика через add --id
// перевозит раздел «Грумминг» в файл задачи вместе с остальным текстом, и
// история разбора не теряется.
func TestDraftPromoteKeepsGroom(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "уведомитель шумит из песочницы")
	if _, err := cmdDraftDefer(root, id, "ждём повторного случая", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{ID: id, Title: "Оформленная", Type: "bug", Rank: "25+4+2+5+2", Cost: "S", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), draftGroomHeading) || !strings.Contains(string(data), "ждём повторного случая") {
		t.Fatalf("пометка не переехала в файл задачи:\n%s", data)
	}
}

// TestDraftSubcommandsNotTakenForText: подкоманда узнаётся точным совпадением
// первого позиционного аргумента, иначе «draft defer <ID> ...» молча завёл бы
// черновик с текстом «defer». Проверяется на собранном бинаре: разбор
// аргументов живёт в main, и вызов без ID выходит из процесса.
func TestDraftSubcommandsNotTakenForText(t *testing.T) {
	root := setup(t)
	bin := buildTaskctl(t)

	out, err := runTaskctl(bin, root, "draft", "defer")
	if err == nil {
		t.Fatalf("draft defer без ID должен отказывать:\n%s", out)
	}
	if !strings.Contains(out, "жду: draft defer") {
		t.Fatalf("подсказка не про форму команды:\n%s", out)
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("отказавший defer завёл черновик: %+v", drafts)
	}

	if out, err := runTaskctl(bin, root, "draft", "обычная запись черновика"); err != nil {
		t.Fatalf("запись черновика сломана новыми подкомандами: %v\n%s", err, out)
	}
	drafts, err = loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Title != "обычная запись черновика" {
		t.Fatalf("накопитель после обычной записи: %+v", drafts)
	}
	if out, err := runTaskctl(bin, root, "draft", "list"); err != nil {
		t.Fatalf("draft list сломан: %v\n%s", err, out)
	} else if !strings.Contains(out, "обычная запись черновика") {
		t.Fatalf("draft list:\n%s", out)
	}
}

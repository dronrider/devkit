package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func draftFile(root, id string) string {
	return filepath.Join(root, "docs", "tasks", "drafts", id+".md")
}

// ageDraft состаривает черновик на n дней: правит его строку «записан», по
// которой и считается возраст, а заодно время правки файла.
func ageDraft(t *testing.T, root, id string, days int) {
	t.Helper()
	path := draftFile(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().AddDate(0, 0, -days)
	body := strings.Replace(string(data),
		draftWrittenPrefix+time.Now().Format(draftDateLayout),
		draftWrittenPrefix+when.Format(draftDateLayout), 1)
	if body == string(data) {
		t.Fatalf("в черновике %s нет строки «записан»:\n%s", id, data)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// TestDraftWritesFileNotBoard: черновик это файл мимо доски, TASKS.md он не
// трогает вовсе, иначе Backlog перестанет быть разобранной работой.
func TestDraftWritesFileNotBoard(t *testing.T) {
	root := setup(t)
	before, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cmdDraft(root, "уведомитель шумит из песочницы\nвторой строкой подробности", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "XR-008: черновик") {
		t.Fatalf("сообщение команды: %q", msg)
	}
	data, err := os.ReadFile(draftFile(root, "XR-008"))
	if err != nil {
		t.Fatalf("файл черновика не создан: %v", err)
	}
	want := fmt.Sprintf("# XR-008: уведомитель шумит из песочницы\n\nзаписан %s\n\n## Черновик\n\nуведомитель шумит из песочницы\nвторой строкой подробности\n",
		time.Now().Format(draftDateLayout))
	if string(data) != want {
		t.Fatalf("содержимое черновика:\n%s\nожидал:\n%s", data, want)
	}
	after, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("draft тронул docs/TASKS.md")
	}
	if _, err := cmdDraft(root, "   \n  ", CommitOpts{}); err == nil {
		t.Fatal("пустой текст должен отбиваться")
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("черновик не должен давать находок lint: %v", finds)
	}
}

// TestNextIDCountsDrafts: ID выдаётся черновику сразу, при заведении, чтобы на
// него можно было сослаться. Не считай nextID черновики, второй задаче достался
// бы занятый номер, и строка доски разошлась бы с файлом.
func TestNextIDCountsDrafts(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "первая идея", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	id, err := cmdID(root)
	if err != nil {
		t.Fatal(err)
	}
	if id != "XR-009" {
		t.Fatalf("следующий свободный ID %s, ожидал XR-009 (XR-008 занял черновик)", id)
	}
	msg, err := cmdAdd(root, AddParams{Title: "Вторая", Type: "task", Rank: "0+1+1+0+1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "XR-009 заведена") {
		t.Fatalf("add после черновика выдал: %q", msg)
	}
	if _, err := os.Stat(draftFile(root, "XR-008")); err != nil {
		t.Fatalf("add без --id не должен трогать черновик: %v", err)
	}
}

// TestDraftPromotedByAdd: оформление доводит add --id, черновик переезжает в
// docs/tasks/<ID>.md, и ссылка в строке ведёт уже туда.
func TestDraftPromotedByAdd(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Оформленная", Type: "bug", Rank: "25+4+2+5+2", Cost: "S"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "черновик перенесён в docs/tasks/XR-008.md") {
		t.Fatalf("add молчит про перенос черновика: %q", msg)
	}
	if _, err := os.Stat(draftFile(root, "XR-008")); !os.IsNotExist(err) {
		t.Fatalf("черновик остался в drafts/: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatalf("файл задачи не появился: %v", err)
	}
	if !strings.Contains(string(data), "уведомитель шумит из песочницы") {
		t.Fatalf("текст черновика потерян: %s", data)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-008")
	if row == nil {
		t.Fatal("строки XR-008 нет на доске")
	}
	if row.Link != "[tasks/XR-008.md](tasks/XR-008.md)" {
		t.Fatalf("ссылка в строке %q", row.Link)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("после оформления lint должен быть зелёным: %v", finds)
	}
}

// TestAddPromotesUntrackedDraft: черновик, не закоммиченный командой draft (с
// пустым CommitOpts), при оформлении через add --id -m переезжает в
// docs/tasks/<ID>.md, а коммит не падает на pathspec по исчезнувшему пути
// drafts/<ID>.md. На старом коде gitMv отбивался от git mv и срабатывал
// запасной rename, а путь черновика всё равно ехал в pathspec коммита и ронял
// его: строка и перенос файла успевали, коммит и пуш оставались руками (DK-080).
func TestAddPromotesUntrackedDraft(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdDraft(root, "идея черновика", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	// Черновик незакоммичен: cmdDraft с пустым CommitOpts не зовёт git, и
	// git mv по такому файлу отбивается, оставаясь на rename. Гит печатает
	// накопитель целиком как untracked-директорию, поэтому ищем префикс.
	if out := gitOut(t, root, "status", "--porcelain"); !strings.Contains(out, "?? docs/tasks/drafts/") {
		t.Fatalf("черновик не untracked, статус:\n%s", out)
	}
	p := AddParams{
		ID: "XR-008", Title: "Оформленная", Type: "bug",
		Rank: "25+4+2+5+2", Cost: "S",
		Commit: CommitOpts{Msg: "docs(tasks): XR-008 оформлена"},
	}
	if _, err := cmdAdd(root, p); err != nil {
		t.Fatalf("add --id по untracked черновику упал: %v", err)
	}
	if _, err := os.Stat(draftFile(root, "XR-008")); !os.IsNotExist(err) {
		t.Fatalf("черновик остался в drafts/: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatalf("файл задачи не появился: %v", err)
	}
	if !strings.Contains(string(data), "идея черновика") {
		t.Fatalf("текст черновика потерян: %s", data)
	}
	if st := gitOut(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("после add в рабочем дереве осталось незакоммиченное:\n%s", st)
	}
	// Коммит увёз правку доски и новый файл задачи, а исходный путь drafts/
	// в pathspec не ехал: git его не знает, и pathspec по нему ронял бы коммит.
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	if !strings.Contains(files, "M\tdocs/TASKS.md") {
		t.Errorf("в коммите нет правки доски:\n%s", files)
	}
	if !strings.Contains(files, "A\tdocs/tasks/XR-008.md") {
		t.Errorf("в коммите нет нового файла задачи:\n%s", files)
	}
	if strings.Contains(files, "drafts/XR-008.md") {
		t.Errorf("в коммите всплыл путь из drafts/:\n%s", files)
	}
}

// TestAddValidationKeepsDraft: упавшая на разборе add не должна уносить
// черновик с прежнего места, иначе после ошибки его не найти ни там, ни там.
func TestAddValidationKeepsDraft(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "идея", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Кривой ранг", Type: "task", Rank: "3+1+1+0+1"}); err == nil {
		t.Fatal("серьёзность 3 не из шкалы, add обязан упасть")
	}
	if _, err := os.Stat(draftFile(root, "XR-008")); err != nil {
		t.Fatalf("черновик уехал при неудачном add: %v", err)
	}
}

// TestDraftListAndShow: строки на доске у черновика нет, поэтому видно его
// должно быть в list, draft list и show.
func TestDraftListAndShow(t *testing.T) {
	root := setup(t)
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if out != "черновиков нет" {
		t.Fatalf("пустой накопитель: %q", out)
	}
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraft(root, "вторая идея", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	ageDraft(t, root, "XR-008", 3)
	out, err = cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-008 (3 дня): уведомитель шумит из песочницы") ||
		!strings.Contains(out, "XR-009 (сегодня): вторая идея") {
		t.Fatalf("draft list:\n%s", out)
	}
	list, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "Черновики (2, целиком: taskctl draft list): XR-008 (3 дня), XR-009 (сегодня)") {
		t.Fatalf("list не называет черновики:\n%s", list)
	}
	if section, err := cmdList(root, "backlog"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(section, "Черновики") {
		t.Fatalf("срез по секции черновики не печатает:\n%s", section)
	}
	shown, err := cmdShow(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "XR-008 черновик (записан 3 дня)") ||
		!strings.Contains(shown, "уведомитель шумит из песочницы") {
		t.Fatalf("show черновика:\n%s", shown)
	}
	if _, err := cmdShow(root, "XR-404"); err == nil {
		t.Fatal("несуществующий ID должен падать")
	}
}

// TestDraftTextGuard: одно слово латиницей это промах мимо подкоманды, а не
// идея; живой текст черновика страж пропускает, включая тот, где латинское
// слово стоит внутри строки.
func TestDraftTextGuard(t *testing.T) {
	for _, word := range []string{"show", "add", "list", "DK-162", "draft-list", " show "} {
		if err := draftTextGuard(word); err == nil {
			t.Errorf("%q: страж пропустил слово латиницей", word)
		}
	}
	for _, text := range []string{
		"хук уронил Bash всем сессиям",
		"taskctl draft теряет текст",
		"идея",
		"go test падает на второй прогон",
		"",
	} {
		if err := draftTextGuard(text); err != nil {
			t.Errorf("%q: страж отбил живой текст: %v", text, err)
		}
	}
}

func TestAgeWords(t *testing.T) {
	cases := []struct {
		days int
		want string
	}{
		{0, "сегодня"}, {1, "вчера"}, {2, "2 дня"}, {5, "5 дней"},
		{11, "11 дней"}, {14, "14 дней"}, {21, "21 день"}, {104, "104 дня"},
	}
	for _, c := range cases {
		got := ageWords(time.Duration(c.days) * 24 * time.Hour)
		if got != c.want {
			t.Errorf("%d дней: получил %q, ожидал %q", c.days, got, c.want)
		}
	}
}

package main

import (
	"encoding/json"
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
	msg, err := cmdDraft(root, "уведомитель шумит из песочницы\nвторой строкой подробности", "mid", CommitOpts{})
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
	want := fmt.Sprintf("# XR-008: уведомитель шумит из песочницы\n\nзаписан %s\nприоритет: средний\n\n## Черновик\n\n### Ситуация\n\nвторой строкой подробности\n\n### Осложнение\n\n### Вопрос\n\n### Гипотеза\n",
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
	if _, err := cmdDraft(root, "   \n  ", "mid", CommitOpts{}); err == nil {
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
	if _, err := cmdDraft(root, "первая идея", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	id, err := cmdID(root)
	if err != nil {
		t.Fatal(err)
	}
	if id != "XR-009" {
		t.Fatalf("следующий свободный ID %s, ожидал XR-009 (XR-008 занял черновик)", id)
	}
	msg, err := cmdAdd(root, AddParams{Title: "Вторая", Type: "task", Rank: "0+1+1+0+1", Accept: "agent"})
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
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	msg, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Оформленная", Type: "bug", Rank: "25+4+2+5+2", Cost: "S", Accept: "agent"})
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
	if _, err := cmdDraft(root, "идея черновика", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	// Черновик незакоммичен: cmdDraft с пустым CommitOpts не зовёт git, и
	// git mv по такому файлу отбивается, оставаясь на rename. Гит печатает
	// накопитель целиком как untracked-директорию, поэтому ищем префикс.
	if out := gitOut(t, root, "status", "--porcelain"); !strings.Contains(out, "?? docs/tasks/drafts/") {
		t.Fatalf("черновик не untracked, статус:\n%s", out)
	}
	giveDraftDoD(t, root, "XR-008")
	p := AddParams{
		ID: "XR-008", Title: "Оформленная", Type: "bug",
		Rank: "25+4+2+5+2", Cost: "S",
		Accept: "agent",
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
	if _, err := cmdDraft(root, "идея", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Кривой ранг", Type: "task", Rank: "3+1+1+0+1", Accept: "agent"}); err == nil {
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
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraft(root, "вторая идея", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	ageDraft(t, root, "XR-008", 3)
	out, err = cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-008 (3 дня, средний): уведомитель шумит из песочницы") ||
		!strings.Contains(out, "XR-009 (сегодня, средний): вторая идея") {
		t.Fatalf("draft list:\n%s", out)
	}
	list, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "Черновики (2, целиком: taskctl draft list): XR-008 (3 дня, средний), XR-009 (сегодня, средний)") {
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

// TestDraftListJSON: машинный вид накопителя для дашборда. Пустой накопитель
// отдаёт пустой список, а не отсутствие поля: «черновиков нет» читатель обязан
// отличать от неприехавшего ответа. Возраст едет и днями, и словами утилиты:
// считать «вчера» второй раз своей шкалой значит разойтись с печатным list.
func TestDraftListJSON(t *testing.T) {
	root := setup(t)
	out, err := cmdDraftListJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	var empty struct {
		Drafts []jsonDraft `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &empty); err != nil {
		t.Fatalf("пустой накопитель не разобрался: %v\n%s", err, out)
	}
	if empty.Drafts == nil || len(empty.Drafts) != 0 {
		t.Fatalf("пустой накопитель обязан быть пустым списком: %s", out)
	}

	if _, err := cmdDraft(root, "уведомитель шумит из песочницы", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraft(root, "вторая идея", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	ageDraft(t, root, "XR-008", 3)
	if _, err := cmdDraftDefer(root, "XR-009", "ждёт решения по каналу", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err = cmdDraftListJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Drafts []jsonDraft `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("накопитель не разобрался: %v\n%s", err, out)
	}
	if len(got.Drafts) != 2 {
		t.Fatalf("черновиков в ответе %d, жду 2: %s", len(got.Drafts), out)
	}
	first := got.Drafts[0]
	if first.ID != "XR-008" || first.Title != "уведомитель шумит из песочницы" {
		t.Errorf("первый черновик не тот: %+v", first)
	}
	if first.File != "docs/tasks/drafts/XR-008.md" {
		t.Errorf("путь файла черновика %q, жду docs/tasks/drafts/XR-008.md", first.File)
	}
	if first.AgeDays != 3 || first.AgeWords != "3 дня" {
		t.Errorf("возраст черновика %d (%q), жду 3 дня словами утилиты", first.AgeDays, first.AgeWords)
	}
	if first.Written == "" {
		t.Errorf("дата записи не назвалась: %+v", first)
	}
	if second := got.Drafts[1]; second.ID != "XR-009" || second.Deferred == "" {
		t.Errorf("отложенный черновик приехал без пометки: %+v", second)
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

// TestDraftLongFirstLineRefused: черновик, записанный одним абзацем, отбивается
// на записи, а отказ называет страницу формы. Тот же текст с заголовком в
// первой строке ложится черновиком (так и приходит текст со stdin).
func TestDraftLongFirstLineRefused(t *testing.T) {
	root := setup(t)
	long := "уведомитель шумит из песочницы, потому что хук старта берёт адрес из окружения, а не из реестра чатов, и вторая сессия перебивает первую"
	_, err := cmdDraft(root, long, "mid", CommitOpts{})
	if err == nil {
		t.Fatal("простыня записалась черновиком")
	}
	for _, want := range []string{"TASKFORM.md", "72"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
	if _, serr := os.Stat(draftFile(root, "XR-008")); serr == nil {
		t.Fatal("отбитый черновик всё равно лёг файлом")
	}
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы\n\n"+long, "mid", CommitOpts{}); err != nil {
		t.Fatalf("черновик с заголовком не записался: %v", err)
	}
	data, err := os.ReadFile(draftFile(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# XR-008: уведомитель шумит из песочницы\n") {
		t.Fatalf("заголовок черновика: %q", data)
	}
}

// TestDraftListShowsTitleNotBody: накопитель печатает заголовок черновика, а не
// его тело; заголовок черновика, записанного до порога, режется по тому же
// порогу.
func TestDraftListShowsTitleNotBody(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы\n\nтело идеи с подробностями", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	long := "старая простыня на весь экран, записанная до порога первой строки, с подробностями внутри той же строки"
	if err := os.WriteFile(draftFile(root, "XR-009"), []byte("# XR-009: "+long+"\n\nзаписан 2026-08-01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("на два черновика строк %d:\n%s", len(lines), out)
	}
	if strings.Contains(out, "тело идеи") {
		t.Errorf("накопитель печатает тело:\n%s", out)
	}
	for _, ln := range lines {
		if n := len([]rune(ln)); n > 112 {
			t.Errorf("строка накопителя в %d символов:\n%s", n, ln)
		}
	}
	if !strings.Contains(out, "старая простыня") || !strings.HasSuffix(lines[1], "...") {
		t.Errorf("длинный заголовок не обрезан:\n%s", out)
	}
}

// TestDraftListJSONKeepsFullTitle: машинный список отдаёт заголовок целиком,
// обрезка clipTitle касается только печати накопителя на экран.
func TestDraftListJSONKeepsFullTitle(t *testing.T) {
	root := setup(t)
	long := "старая простыня на весь экран, записанная до порога первой строки, с подробностями внутри той же строки"
	if err := os.MkdirAll(draftsDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftFile(root, "XR-009"), []byte("# XR-009: "+long+"\n\nзаписан 2026-08-01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftListJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Drafts []jsonDraft `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Drafts) != 1 || got.Drafts[0].Title != long {
		t.Fatalf("заголовок в JSON обрезан или потерян: %s", out)
	}
	if text, _ := cmdDraftList(root); strings.Contains(text, long) {
		t.Fatalf("печатный список не обрезал заголовок: %s", text)
	}
}

// TestDraftDemandsPrio: запись без уровня разбора отказывает и учит форме
// команды, а сам черновик не заводится. Метка задаёт очередь разбора, и
// поставленная грумингом она появлялась тогда, когда разбор уже шёл (DK-520).
func TestDraftDemandsPrio(t *testing.T) {
	root := setup(t)
	_, err := cmdDraft(root, "идея без уровня", "", CommitOpts{})
	if err == nil {
		t.Fatal("запись без --prio должна отбиваться")
	}
	if !strings.Contains(err.Error(), "taskctl draft --prio high|mid|low") {
		t.Fatalf("подсказка не про форму команды: %v", err)
	}
	if _, err := cmdDraft(root, "идея с уровнем не из шкалы", "срочно", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "не из шкалы") {
		t.Fatalf("уровень мимо шкалы: %v", err)
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Fatalf("отказавшая запись завела черновик: %+v", drafts)
	}
}

// TestDraftKeepsPrioFromWrite: уровень с записи ложится в шапку той же строкой,
// что и у draft prio, и виден в накопителе без всякого разбора.
func TestDraftKeepsPrioFromWrite(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы\nвторой строкой подробности", "high", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(draftFile(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("записан %s\nприоритет: высокий\n", time.Now().Format(draftDateLayout))
	if !strings.Contains(string(data), want) {
		t.Fatalf("метка не в шапке:\n%s", data)
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Prio != "high" {
		t.Fatalf("накопитель читает уровень иначе: %+v", drafts)
	}
	out, err := cmdDraftList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-008 (сегодня, высокий): уведомитель шумит из песочницы") {
		t.Fatalf("draft list:\n%s", out)
	}
}

// TestDraftPrioOnTitleOnly: черновик из одного заголовка это болванка без тела,
// и метке в ней ложиться не на что, кроме строки «записан». Строка не должна
// уехать в раздел «Черновик» и стать текстом идеи.
func TestDraftPrioOnTitleOnly(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "болванка из одного заголовка", "low", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(draftFile(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(data), "## ")
	if !strings.Contains(head, "приоритет: низкий") {
		t.Fatalf("метка не в шапке болванки:\n%s", data)
	}
	if strings.Count(string(data), draftPrioPrefix) != 1 {
		t.Fatalf("метка задвоилась:\n%s", data)
	}
	if _, err := cmdDraftPrio(root, "XR-008", "high", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(draftFile(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), draftPrioPrefix) != 1 || !strings.Contains(string(data), "приоритет: высокий") {
		t.Fatalf("пересмотр уровня копит строки:\n%s", data)
	}
}

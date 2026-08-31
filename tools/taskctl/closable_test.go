package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// closableIDRe повторяет разбор ответа на стороне тика (CLOSABLE_ID в
// tools/devkitctl/watch.py): голый ID значит «закрывать можно», проза под этот
// вид не подходит. Копия тут сторожит контракт формата с той стороны, где его
// можно проверить прогоном.
var closableIDRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-\d+$`)

// Отбор строк Check, которые вправе закрыть автоматика (DK-516): вид приёмки
// agent, действующая на последний выкат отметка smoke и непустой раздел
// «Проверка». Тик сторожка спрашивает вердикт этой командой, и всё, чего в
// готовых нет, остаётся человеку.

// closableBoard кладёт доску, где перечисленные строки стоят в Check.
func closableBoard(t *testing.T, rows ...string) string {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	board := strings.Replace(fixtureBoard,
		"## Check (готово, ждёт проверки пользователем)\n\n| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n",
		"## Check (готово, ждёт проверки пользователем)\n\n| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"+strings.Join(rows, "\n")+"\n", 1)
	if board == fixtureBoard {
		t.Fatal("шапка Check в фикстуре не нашлась: тест не про то, что проверяет")
	}
	if err := os.WriteFile(boardPath(root), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// checkRow это строка Check с заголовком (вид приёмки живёт его суффиксом).
func checkRow(id, title string) string {
	return "| " + id + " | " + title + " | task | P2 | 30 (25+2+1+0+2) | M | [tasks/" + id + ".md](tasks/" + id + ".md) |"
}

// taskDoc кладёт файл задачи: запись слияния, за ней по надобности отметка
// smoke и раздел «Проверка» с вложенным выводом.
func taskDoc(t *testing.T, root, id string, smoke, verified bool) {
	t.Helper()
	doc := "# " + id + "\n" + fixtureScenario + "\n## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n"
	if smoke {
		doc += "- smoke прогнан, 2026-08-21\n"
	}
	if verified {
		doc += fixtureVerification
	} else {
		doc += "\n## Проверка\n\n"
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ready разбирает вывод команды: готовые идут голыми ID до строки «отказано:».
func ready(out string) []string {
	var ids []string
	for _, ln := range strings.Split(out, "\n") {
		if !closableIDRe.MatchString(strings.TrimSpace(ln)) {
			break
		}
		ids = append(ids, strings.TrimSpace(ln))
	}
	return ids
}

// TestClosableTakesReadyAgent: агентская строка с прогнанным smoke и вложенным
// выводом уходит в готовые. Она и есть тот случай, ради которого автоматика
// заведена: закрывать её человеку нечем.
func TestClosableTakesReadyAgent(t *testing.T) {
	root := closableBoard(t, checkRow("XR-010", "Готовая агентская"))
	taskDoc(t, root, "XR-010", true, true)
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 1 || got[0] != "XR-010" {
		t.Fatalf("готовой должна быть ровно XR-010, а вывод такой:\n%s", out)
	}
}

// TestClosableSkipsHumanKinds: виды user и mixed автоматике не отдаются, часть
// приёмки там за человеком (LLD DK-292, решение 2).
func TestClosableSkipsHumanKinds(t *testing.T) {
	root := closableBoard(t,
		checkRow("XR-011", "Пользовательская [приёмка: user]"),
		checkRow("XR-012", "Смешанная [приёмка: mixed]"))
	taskDoc(t, root, "XR-011", true, true)
	taskDoc(t, root, "XR-012", true, true)
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 0 {
		t.Fatalf("виды user и mixed автоматике не отдаются, а готовыми названы %v:\n%s", got, out)
	}
	for _, want := range []string{"XR-011: вид приёмки user", "XR-012: вид приёмки mixed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("отказ не называет вид приёмки (%q):\n%s", want, out)
		}
	}
}

// TestClosableNeedsSmoke: без отметки smoke на последний выкат сценарий после
// выката не прогнан, и закрывать нечего.
func TestClosableNeedsSmoke(t *testing.T) {
	root := closableBoard(t, checkRow("XR-013", "Выкат без прогона"))
	taskDoc(t, root, "XR-013", false, true)
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 0 {
		t.Fatalf("строка без отметки smoke закрытию не подлежит, а названа готовой %v:\n%s", got, out)
	}
	if !strings.Contains(out, "отметки smoke") {
		t.Fatalf("отказ не называет причину:\n%s", out)
	}
}

// TestClosableNeedsVerification: пустой раздел «Проверка» это тот же рубеж,
// что у ворот close (LLD DK-292, решение 4), и автоматика его не обходит.
func TestClosableNeedsVerification(t *testing.T) {
	root := closableBoard(t, checkRow("XR-014", "Без вывода прогона"))
	taskDoc(t, root, "XR-014", true, false)
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 0 {
		t.Fatalf("строка с пустой «Проверкой» закрытию не подлежит, а названа готовой %v:\n%s", got, out)
	}
	if !strings.Contains(out, "«Проверка» пуст") {
		t.Fatalf("отказ не называет причину:\n%s", out)
	}
}

// TestClosableNeedsTaskFile: без файла задачи вердикт строже ворот close,
// которые такую строку пропускают молча: автоматике нечего читать, и закрытие
// вслепую хуже строки, дождавшейся живой сессии.
func TestClosableNeedsTaskFile(t *testing.T) {
	root := closableBoard(t, checkRow("XR-015", "Без файла задачи"))
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 0 {
		t.Fatalf("строка без файла задачи закрытию не подлежит, а названа готовой %v:\n%s", got, out)
	}
	if !strings.Contains(out, "файла задачи нет") {
		t.Fatalf("отказ не называет причину:\n%s", out)
	}
}

// TestClosableSkipsFailedProd: непогашенный провал проверки держит очередь
// целиком, и закрыть такую строку не даст сам close.
func TestClosableSkipsFailedProd(t *testing.T) {
	root := closableBoard(t, checkRow("XR-016", "Сломала прод [провал: упал вход]"))
	taskDoc(t, root, "XR-016", true, true)
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 0 {
		t.Fatalf("строка с провалом закрытию не подлежит, а названа готовой %v:\n%s", got, out)
	}
	if !strings.Contains(out, "провал") {
		t.Fatalf("отказ не называет причину:\n%s", out)
	}
}

// TestClosableEmptyBoard: пустой Check это не молчание, а внятный ответ. Тик
// разбирает вывод по строкам, и голая пустота читалась бы им как готовый ID.
func TestClosableEmptyBoard(t *testing.T) {
	root := closableBoard(t, checkRow("XR-017", "Ждёт человека [приёмка: user]"))
	taskDoc(t, root, "XR-017", true, true)
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "закрывать автоматике нечего") {
		t.Fatalf("пустой список должен начинаться прозой, а не готовым ID:\n%s", out)
	}
}

// TestClosableRefusesAuthorVerifyRun: прогон сценария под исполнителем
// разработки уводит строку в отказы (DK-642). Тик сторожка по отказу молчит,
// а строка ждёт прогона другой моделью.
func TestClosableRefusesAuthorVerifyRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := closableBoard(t, checkRow("XR-010", "Агентская с прогоном автора"))
	doc := "# XR-010\n\n## Ход работы\n\n" +
		devStageLine("opus") + "\n" + verifyStageLine("opus") + "\n" +
		fixtureScenario + "\n## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n- smoke прогнан, 2026-08-21\n" +
		fixtureVerification
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-010.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdClosable(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ready(out); len(got) != 0 {
		t.Fatalf("строка с прогоном автора названа готовой:\n%s", out)
	}
	if !strings.Contains(out, "нужен прогон другой моделью") {
		t.Fatalf("в отказе нет причины про прогон:\n%s", out)
	}
}

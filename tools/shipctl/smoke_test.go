package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Двухступенчатый Check (LLD DK-400, решение 7): очередь выката держит выкат
// без отметки smoke, а не незакрытая задача. Отметка это строка «smoke
// прогнан, <дата>» в разделе «Выкат» файла задачи.

// deployedCheckTask имитирует выкаченную задачу в Check: коммит кода с ID в
// subject и файл задачи с записью слияния. smoke-строка дописывается в файл,
// если прогон уже состоялся.
func deployedCheckTask(t *testing.T, root, id, smoke string) {
	t.Helper()
	codeCommit(t, root, id, id+".txt")
	sha := gitT(t, root, "rev-parse", "--short", "HEAD")
	doc := "# " + id + ": ждёт приёмки\n\n## Выкат\n\n- 2026-08-01 слито: " + sha + "\n"
	if smoke != "" {
		doc += "- " + smokeNote + ", " + smoke + "\n"
	}
	write(t, root, "docs/tasks/"+id+".md", doc)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): "+id+" файл задачи")
}

// TestSmokeMarkFreesQueue: выкат с отметкой smoke освобождает очередь до
// закрытия строки пользователем. Следующее слияние едет одиночным выкатом, а
// не поездом, и status называет очередь свободной, видя приёмку глазами за
// человеком. На старом коде отметки не существовало, и слияние уходило в
// поездной режим.
func TestSmokeMarkFreesQueue(t *testing.T) {
	root, _ := setup(t, rowInProg, rowCheck)
	deployedCheckTask(t, root, "XR-009", "2026-08-02")

	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "true"})
	if err != nil {
		t.Fatalf("выкат с отметкой smoke должен освобождать очередь: %v", err)
	}
	if strings.Contains(msg, "слияние поездное") || !strings.Contains(msg, "выкат прошёл") {
		t.Fatalf("слияние должно пройти одиночным выкатом, а не поездом: %q", msg)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "очередь свободна") || !strings.Contains(st, "smoke прогнан за XR-009") {
		t.Fatalf("status должен видеть в Check задачу с прогнанным smoke и свободную очередь:\n%s", st)
	}
}

// TestQueueHeldByRecordWithoutSmoke: запись слияния без отметки smoke держит
// очередь, как и до двухступенчатого Check. Отметка это ослабление, а не
// отмена инварианта «непроверенный выкат один».
func TestQueueHeldByRecordWithoutSmoke(t *testing.T) {
	root, _ := setup(t, rowInProg, rowCheck)
	deployedCheckTask(t, root, "XR-009", "")

	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь занята: XR-009") || !strings.Contains(msg, "слияние поездное") {
		t.Fatalf("выкат без отметки smoke должен держать очередь: %q", msg)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "очередь занята: XR-009") || !strings.Contains(st, "shipctl smoke XR-009") {
		t.Fatalf("status обязан назвать путь освобождения очереди:\n%s", st)
	}
}

// TestSmokeStaleAfterRework: круг доработки дописывает в раздел новую строку
// слияния, и отметка прошлого круга новый выкат не прикрывает: считается
// отметка после последней записи, иначе доработка ехала бы на прод мимо
// инварианта.
func TestSmokeStaleAfterRework(t *testing.T) {
	root, _ := setup(t, rowInProg, rowCheck)
	deployedCheckTask(t, root, "XR-009", "2026-08-02")
	f, _ := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-009.md"))
	write(t, root, "docs/tasks/XR-009.md", string(f)+"- 2026-08-15 слито: abc1234\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-009 круг доработки")

	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь занята: XR-009") || !strings.Contains(msg, "слияние поездное") {
		t.Fatalf("отметка прошлого круга не должна прикрывать новый выкат: %q", msg)
	}
}

// TestSmokeCommand: отметку ставит команда после прогона агентской части
// сценария, повторная отметка отбивается, а очередь команда освобождает
// сразу.
func TestSmokeCommand(t *testing.T) {
	root, _ := setup(t, rowInProg, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")

	msg, err := cmdSmoke(root, SmokeParams{ID: "XR-009"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь выката задача больше не держит") {
		t.Fatalf("команда молчит про освобождение очереди: %q", msg)
	}
	body, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-009.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## Выкат\n\n") || !strings.Contains(string(body), "- "+smokeNote+", ") {
		t.Fatalf("отметка не встала в раздел «Выкат»:\n%s", body)
	}
	if log := gitT(t, root, "log", "--format=%s"); !strings.Contains(log, "docs(tasks): XR-009 smoke прогнан") {
		t.Fatalf("коммит отметки не найден:\n%s", log)
	}

	// Повторная отметка на тот же выкат не нужна.
	if _, err := cmdSmoke(root, SmokeParams{ID: "XR-009"}); err == nil ||
		!strings.Contains(err.Error(), "уже отмечен") {
		t.Fatalf("повторная отметка должна отбиваться: %v", err)
	}

	// Освобождённая очередь пропускает следующее слияние с выкатом.
	branchWithFix(t, root)
	msg, err = cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "true"})
	if err != nil {
		t.Fatalf("после отметки очередь должна быть свободна: %v", err)
	}
	if !strings.Contains(msg, "выкат прошёл") {
		t.Fatalf("слияние должно доехать до выката: %q", msg)
	}
}

// TestSmokeRefusedWithoutDeploy: отметка нужна ровно задаче в Check с
// непроверенным выкатом. Бескодовой задаче в Check и задаче вне Check она не
// ставится: освобождать нечего, а лишняя отметка только путала бы запись.
func TestSmokeRefusedWithoutDeploy(t *testing.T) {
	root, _ := setup(t, rowInProg, rowLLD)
	lldCommit(t, root, "XR-005")
	if _, err := cmdSmoke(root, SmokeParams{ID: "XR-005"}); err == nil ||
		!strings.Contains(err.Error(), "нет непроверенного выката") {
		t.Fatalf("бескодовой задаче отметка не нужна: %v", err)
	}
	if _, err := cmdSmoke(root, SmokeParams{ID: "XR-001"}); err == nil ||
		!strings.Contains(err.Error(), "не в Check") {
		t.Fatalf("задаче вне Check отметка не ставится: %v", err)
	}
}

// TestSmokeDoneParse: разбор отметки берёт раздел «Выкат» мимо цитат и
// прозы, и действующей считается отметка после последней строки с коммитами.
func TestSmokeDoneParse(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		doc  string
		want bool
	}{
		{"отметка после записи", "## Выкат\n\n- 2026-08-01 слито: 1a2b3c4\n- smoke прогнан, 2026-08-02\n", true},
		{"отметки нет", "## Выкат\n\n- 2026-08-01 слито: 1a2b3c4\n", false},
		{"новый круг после отметки", "## Выкат\n\n- 2026-08-01 слито: 1a2b3c4\n- smoke прогнан, 2026-08-02\n- 2026-08-15 слито: 5d6e7f8\n", false},
		{"отметка без записи", "## Выкат\n\n- smoke прогнан, 2026-08-02\n", true},
		{"проза с двоеточием", "## Выкат\n\n- smoke прогнан, 2026-08-02\n- перевыкат руками, коммит не записан: позже\n", true},
		{"отметка в цитате", "## Сценарий проверки\n\n```\n## Выкат\n\n- smoke прогнан, 2026-08-02\n```\n\n## Выкат\n\n- 2026-08-01 слито: 1a2b3c4\n", false},
		{"раздела нет", "## Ревью\n\n- замечание: исправлено\n", false},
	}
	for _, c := range cases {
		write(t, root, "docs/tasks/XR-777.md", "# XR-777: задача\n\n"+c.doc)
		got, err := smokeDone(root, "XR-777")
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: smokeDone = %v, ждал %v", c.name, got, c.want)
		}
	}
	if got, err := smokeDone(root, "XR-778"); err != nil || got {
		t.Errorf("задача без файла: %v, %v", got, err)
	}
}

// TestStatusNamesWhoWaitsForHuman: после smoke status называет ждущих человека
// поимённо и с видом приёмки, а агентскую строку отдаёт тику сторожка. До
// DK-516 обе строки шли одной фразой «приёмка глазами за пользователем», и
// совет «закрыть задачу» стоял над строкой, закрывать которую было некому.
func TestStatusNamesWhoWaitsForHuman(t *testing.T) {
	userRow := "| XR-008 | Ждёт глаз [приёмка: user] | task | P2 | 30 (25+5+0+0+0) |  |\n"
	root, _ := setup(t, rowInProg, rowCheck+userRow)
	deployedCheckTask(t, root, "XR-009", "2026-08-02")
	write(t, root, "docs/tasks/XR-009.md", readDoc(t, root, "XR-009")+"\n## Проверка\n\n- прогон пройден, вывод вложен.\n")
	deployedCheckTask(t, root, "XR-008", "2026-08-02")

	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "ждут человека в Check: XR-008 (user)") {
		t.Fatalf("status не называет ждущего человека поимённо и с видом:\n%s", st)
	}
	if !strings.Contains(st, "агентские XR-009 закроет тик devkitctl watch") {
		t.Fatalf("status не отдаёт агентскую строку тику:\n%s", st)
	}
	if strings.Contains(st, "приёмка глазами за пользователем") {
		t.Fatalf("общая фраза про приёмку глазами осталась:\n%s", st)
	}
	last := st[strings.LastIndex(st, "\n")+1:]
	if !strings.Contains(last, "приёмка за человеком по XR-008 (user)") {
		t.Fatalf("совет не называет, за кем приёмка: %q", last)
	}
	if !strings.Contains(last, "XR-009 доведёт до Done тик") {
		t.Fatalf("совет зовёт закрывать руками то, что закрывает тик: %q", last)
	}
}

// TestStatusNamesEmptyVerification: агентская строка с прогнанным smoke, но
// пустым разделом «Проверка», тику не по зубам (ворота close требуют вывода),
// и status зовёт вложить вывод, а не ждать закрытия.
func TestStatusNamesEmptyVerification(t *testing.T) {
	root, _ := setup(t, rowInProg, rowCheck)
	deployedCheckTask(t, root, "XR-009", "2026-08-02")

	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "вложить вывод прогона в раздел «Проверка» файла задачи XR-009") {
		t.Fatalf("status молчит про пустой раздел «Проверка»:\n%s", st)
	}
	if strings.Contains(st, "XR-009 доведёт до Done тик") {
		t.Fatalf("status обещает закрытие тиком там, где ворота close откажут:\n%s", st)
	}
}

// readDoc читает файл задачи стенда.
func readDoc(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

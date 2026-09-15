package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/sessions"
)

// runTaskctlSession запускает собранный бинарь как отдельный процесс со
// своим домом и своей сессией: интеграционному тесту нужен настоящий проход
// main(), где решается, класть ли отметку в реестр по исходу команды.
func runTaskctlSession(t *testing.T, bin, root, home, sess string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOME="+home, sessions.SessionEnv+"="+sess)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestRejectedMoveLeavesRegistryAlone: регрессия DK-1003. touchWork звался до
// исполнения команды и решал по разобранным аргументам, а не по её исходу, и
// отбитый воротами move в Check всё равно клал в реестр «снята»: строка
// оставалась в работе на доске, а сессия по реестру уже её отпустила. Отбитая
// команда обязана оставить реестр таким, каким он был до неё.
func TestRejectedMoveLeavesRegistryAlone(t *testing.T) {
	root := setup(t)
	bin := buildTaskctl(t)
	home := t.TempDir()
	sess := "aaaa1111-1111-4111-8111-111111111111"

	// Без раздела «Сценарий проверки» ворота move check отбивают строку.
	scenarioFile := filepath.Join(root, "docs", "tasks", "XR-004.md")
	if err := os.WriteFile(scenarioFile, []byte("# XR-004\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runTaskctlSession(t, bin, root, home, sess, "move", "XR-004", "in-progress"); err != nil {
		t.Fatalf("move in-progress должен пройти: %v\n%s", err, out)
	}
	before := sessions.LoadAll(home)[sess]
	if len(before) != 1 || len(sessions.Works(before)) != 1 {
		t.Fatalf("после взятия строки реестр: %+v", before)
	}

	out, err := runTaskctlSession(t, bin, root, home, sess, "move", "XR-004", "check")
	if err == nil {
		t.Fatalf("move в check без сценария должен отбиваться воротами:\n%s", out)
	}

	after := sessions.LoadAll(home)[sess]
	if len(after) != len(before) {
		t.Fatalf("отбитый move тронул реестр: было %+v, стало %+v", before, after)
	}
	if len(sessions.Works(after)) != 1 {
		t.Fatalf("отбитый move снял признак работы: %+v", after)
	}
}

// TestRejectedCloseLeavesRegistryAlone: тот же баг у close. Пустой раздел
// «Проверка» отбивает закрытие агентской задачи (DK-300), и реестр обязан
// остаться таким же, как до вызова: без записи, если её и не было.
func TestRejectedCloseLeavesRegistryAlone(t *testing.T) {
	root := setup(t)
	bin := buildTaskctl(t)
	home := t.TempDir()
	sess := "bbbb2222-2222-4222-8222-222222222222"

	// Сценарий на месте, а «Проверка» пуста: closeAgentGate отбивает close.
	taskFile := filepath.Join(root, "docs", "tasks", "XR-005.md")
	if err := os.WriteFile(taskFile, []byte("# XR-005\n"+fixtureScenario), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runTaskctlSession(t, bin, root, home, sess, "close", "XR-005")
	if err == nil {
		t.Fatalf("close с пустой «Проверкой» должен отбиваться воротами:\n%s", out)
	}

	if recs := sessions.LoadAll(home)[sess]; len(recs) != 0 {
		t.Fatalf("отбитый close завёл запись в реестре: %+v", recs)
	}
}

// TestMissingRowMoveSkipsRegistry: взятие несуществующей строки не заводит
// привязку вовсе, ни отметкой работы, ни отвязкой.
func TestMissingRowMoveSkipsRegistry(t *testing.T) {
	root := setup(t)
	bin := buildTaskctl(t)
	home := t.TempDir()
	sess := "cccc3333-3333-4333-8333-333333333333"

	out, err := runTaskctlSession(t, bin, root, home, sess, "move", "XR-777", "in-progress")
	if err == nil {
		t.Fatalf("move несуществующей строки должен отбиваться:\n%s", out)
	}
	if recs := sessions.LoadAll(home)[sess]; len(recs) != 0 {
		t.Fatalf("несуществующая строка завела привязку: %+v", recs)
	}
}

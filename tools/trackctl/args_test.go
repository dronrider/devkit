package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// goRunTrack запускает собранный trackctl в корне доски root с args. Разбор
// аргументов живёт в main, поэтому формы гоняются процессом.
func goRunTrack(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	full := append([]string{"run", ".", "-C", root}, args...)
	out, err := exec.Command("go", full...).CombinedOutput()
	return string(out), err
}

// TestSubmitFlagAnywhere: у trackctl submit флаг --log-only стоит где угодно
// относительно KEY, и обе формы доходят до одной и той же ошибки команды. Без
// привязки к трекеру submit падает на «нет привязки» одинаково для «submit
// ABC-12 --log-only» и «submit --log-only ABC-12». До DK-236 вторая форма
// отбивалась в разборе «жду: submit <KEY>», потому что args[1] проверяли до
// fs.Parse и флаг принимали за пропущенный KEY.
func TestSubmitFlagAnywhere(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte("# доска\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outAfter, _ := goRunTrack(t, root, "submit", "ABC-12", "--log-only")
	outBefore, _ := goRunTrack(t, root, "submit", "--log-only", "ABC-12")

	if !strings.Contains(outAfter, "нет привязки к трекеру") {
		t.Fatalf("submit <KEY> --log-only: жду «нет привязки», получил %q", outAfter)
	}
	// Главный симптом DK-236: на старом коде здесь было бы «жду: submit <KEY>»,
	// потому что флаг перед KEY принимали за пропущенный KEY. На новом разбор
	// пропускает флаг, и команда проходит до того же отказа, что и форма «флаг
	// после».
	if !strings.Contains(outBefore, "нет привязки к трекеру") {
		t.Fatalf("submit --log-only <KEY>: жду «нет привязки», получил %q", outBefore)
	}
	if strings.Contains(outBefore, "жду: submit") {
		t.Fatalf("submit --log-only <KEY>: разбор не переварил флаг перед KEY: %q", outBefore)
	}
}

// TestStatusExtraPositionalRejects: статус не принимает позиционных, и лишний
// аргумент у него раньше молчал (fs.Parse(args[1:]) без проверки хвоста). После
// DK-236 это отказ с «лишний аргумент» через NeedArgs(pos, 0, 0, ...).
func TestStatusExtraPositionalRejects(t *testing.T) {
	root := t.TempDir()
	out, err := goRunTrack(t, root, "status", "junk")
	if err == nil {
		t.Fatalf("status junk: лишний позиционный не отбит:\n%s", out)
	}
	if !strings.Contains(out, "лишний аргумент") {
		t.Fatalf("status junk: жду «лишний аргумент», получил %q", out)
	}
}

// TestIssueExtraPositionalRejects: лишний позиционный за KEY раньше съедал хвост
// флагов молча (DK-236). После починки это отказ с «лишний аргумент».
func TestIssueExtraPositionalRejects(t *testing.T) {
	root := t.TempDir()
	out, err := goRunTrack(t, root, "issue", "ABC-12", "junk")
	if err == nil {
		t.Fatalf("issue ABC-12 junk: лишний позиционный не отбит:\n%s", out)
	}
	if !strings.Contains(out, "лишний аргумент") {
		t.Fatalf("issue ABC-12 junk: жду «лишний аргумент», получил %q", out)
	}
}

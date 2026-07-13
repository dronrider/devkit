package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Params struct {
	Dir   string   // откуда искать корень репозитория
	Base  string   // реф старого кода, пустой выбирается эвристикой
	Tests []string // явный список тестовых файлов вместо автопоиска
	Cmd   []string // команда теста
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func runCmd(dir string, argv []string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tail обрезает вывод теста до хвоста: итог прогона внизу, а простыня целиком
// только зря съедает контекст читающего агента.
func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 30 {
		lines = append([]string{"..."}, lines[len(lines)-30:]...)
	}
	return strings.Join(lines, "\n")
}

// isTestPath отделяет тестовые файлы от правки: их переносим на старый код,
// остальное оставляем как было в базе.
func isTestPath(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Dir(p)), "/") {
		switch seg {
		case "test", "tests", "testdata", "__tests__", "spec", "specs":
			return true
		}
	}
	base := filepath.Base(p)
	if strings.HasPrefix(base, "test_") {
		return true
	}
	for _, m := range []string{"_test.", ".test.", ".spec.", "_spec."} {
		if strings.Contains(base, m) {
			return true
		}
	}
	return false
}

// pickBase выбирает, где живёт старый код: незакоммиченная правка сравнивается
// с HEAD, закоммиченная с main/master. Правку, уже закоммиченную на ветке
// поверх грязного дерева, эвристика не распознает, там нужен явный --base.
func pickBase(root, base string) (string, error) {
	if base == "" {
		dirty, err := gitOut(root, "status", "--porcelain")
		if err != nil {
			return "", err
		}
		if dirty != "" {
			base = "HEAD"
		} else {
			for _, b := range []string{"main", "master"} {
				if _, err := gitOut(root, "rev-parse", "--verify", b); err == nil {
					base = b
					break
				}
			}
			if base == "" {
				return "", fmt.Errorf("рабочее дерево чистое, а веток main/master нет; укажи --base явно")
			}
		}
	}
	if _, err := gitOut(root, "rev-parse", "--verify", base+"^{commit}"); err != nil {
		return "", fmt.Errorf("база %q не резолвится в коммит: %v", base, err)
	}
	return base, nil
}

func changedTests(root, base string) ([]string, error) {
	diff, err := gitOut(root, "diff", "--name-only", base)
	if err != nil {
		return nil, err
	}
	untracked, err := gitOut(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var tests []string
	for _, f := range strings.Split(diff+"\n"+untracked, "\n") {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] || !isTestPath(f) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			continue // удалённый тест переносить нечего
		}
		seen[f] = true
		tests = append(tests, f)
	}
	return tests, nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, info.Mode().Perm())
}

func Run(p Params) (string, error) {
	if len(p.Cmd) == 0 {
		return "", fmt.Errorf("нужна команда теста после «--», например: regcheck -- go test ./...")
	}
	root, err := gitOut(p.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	base, err := pickBase(root, p.Base)
	if err != nil {
		return "", err
	}
	tests := p.Tests
	if len(tests) == 0 {
		if tests, err = changedTests(root, base); err != nil {
			return "", err
		}
		if len(tests) == 0 {
			return "", fmt.Errorf("не нашёл изменённых тестов относительно %s; перечисли их флагом --tests", base)
		}
	}
	for _, t := range tests {
		if _, err := os.Stat(filepath.Join(root, t)); err != nil {
			return "", fmt.Errorf("тестовый файл %s не найден в рабочем дереве", t)
		}
	}
	if out, err := runCmd(root, p.Cmd); err != nil {
		return "", fmt.Errorf("тест не проходит на текущем коде, сначала чинить его:\n%s", tail(out))
	}
	tmp, err := os.MkdirTemp("", "regcheck-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	wt := filepath.Join(tmp, "old")
	if _, err := gitOut(root, "worktree", "add", "--detach", wt, base); err != nil {
		return "", err
	}
	defer gitOut(root, "worktree", "remove", "--force", wt)
	for _, t := range tests {
		if err := copyFile(filepath.Join(root, t), filepath.Join(wt, t)); err != nil {
			return "", err
		}
	}
	if _, err := runCmd(wt, p.Cmd); err == nil {
		return "", fmt.Errorf("тест зелёный и на старом коде (%s), регрессию он не ловит; "+
			"если правка уже закоммичена, укажи базу без неё (--base main)", base)
	}
	return fmt.Sprintf("тест краснеет на %s и проходит на текущем коде, регрессия закрыта (тесты: %s)",
		base, strings.Join(tests, ", ")), nil
}

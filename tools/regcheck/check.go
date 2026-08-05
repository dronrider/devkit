package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Params struct {
	Dir    string   // откуда искать корень репозитория
	Base   string   // реф старого кода, пустой выбирается эвристикой
	Tests  []string // явный список тестовых файлов вместо автопоиска
	Inline []string // файлы, где правка и тест в одном файле (см. spliceInline)
	Cmd    []string // команда теста
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

// errNoTestRegion значит, что в файле честно нет тестового региона (не
// ошибка поиска, а факт: например, в базовой версии теста ещё не было).
var errNoTestRegion = errors.New("тестовый регион не найден")

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// findTestRegion ищет границы тестовой части файла (1-based, включительно).
// Сначала явные маркеры regcheck:test-begin/regcheck:test-end (работают в
// комментарии любого языка, ставятся руками), если их нет, то блок Rust
// #[cfg(test)] mod ... { ... }. Неоднозначность (несколько блоков, маркер без
// пары, несбалансированные скобки) это ошибка, а не повод угадывать.
func findTestRegion(lines []string) (start, end int, err error) {
	beginIdx, endIdx, beginCount, endCount := -1, -1, 0, 0
	for i, l := range lines {
		if strings.Contains(l, "regcheck:test-begin") {
			beginIdx, beginCount = i, beginCount+1
		}
		if strings.Contains(l, "regcheck:test-end") {
			endIdx, endCount = i, endCount+1
		}
	}
	if beginCount > 1 || endCount > 1 {
		return 0, 0, fmt.Errorf("несколько маркеров regcheck:test-begin/regcheck:test-end, должен быть один")
	}
	if beginCount == 1 || endCount == 1 {
		if beginCount != endCount {
			return 0, 0, fmt.Errorf("regcheck:test-begin/regcheck:test-end не в паре")
		}
		if endIdx <= beginIdx {
			return 0, 0, fmt.Errorf("regcheck:test-end должен стоять на отдельной строке строго после regcheck:test-begin")
		}
		return beginIdx + 1, endIdx + 1, nil
	}
	return findRustCfgTest(lines)
}

// findRustCfgTest находит единственный блок #[cfg(test)] mod ... { ... } и
// возвращает его границы по совпадению фигурных скобок. Скобки внутри строк
// и комментариев не разбираются: на них эвристика ошибётся, но честно
// (несбалансированный подсчёт вернёт ошибку, а не тихо неверный регион).
func findRustCfgTest(lines []string) (start, end int, err error) {
	// атрибут ищем по префиксу, а не по точному совпадению строки: так
	// ловятся и «#[cfg(test)] mod tests {» одной строкой, и «#[cfg(test)] //
	// коммент» с mod на следующей строке.
	var attrs []int
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#[cfg(test)]") {
			attrs = append(attrs, i)
		}
	}
	if len(attrs) == 0 {
		return 0, 0, errNoTestRegion
	}
	if len(attrs) > 1 {
		return 0, 0, fmt.Errorf("несколько блоков #[cfg(test)], нужны явные маркеры regcheck:test-begin/regcheck:test-end")
	}
	i := attrs[0]
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "#[cfg(test)]"))
	j := i
	if !strings.HasPrefix(rest, "mod ") {
		j = i + 1
		for j < len(lines) {
			t := strings.TrimSpace(lines[j])
			if t == "" || strings.HasPrefix(t, "//") {
				j++
				continue
			}
			break
		}
		if j >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[j]), "mod ") {
			return 0, 0, fmt.Errorf("после #[cfg(test)] на строке %d не нашёл «mod» в поддерживаемой форме, "+
				"поставь маркеры regcheck:test-begin/regcheck:test-end", i+1)
		}
	}
	depth := 0
	opened := false
	for k := j; k < len(lines); k++ {
		for _, ch := range lines[k] {
			switch ch {
			case '{':
				depth++
				opened = true
			case '}':
				depth--
				if depth < 0 {
					return 0, 0, fmt.Errorf("несбалансированные скобки начиная со строки %d", j+1)
				}
				if depth == 0 && opened {
					return i + 1, k + 1, nil
				}
			}
		}
	}
	return 0, 0, fmt.Errorf("не нашёл закрывающую скобку блока mod, начатого на строке %d", j+1)
}

// spliceInline строит версию инлайнового файла для базового кода: код
// базовый (возможно с багом), тестовая часть текущая (новая, должна ловить
// баг). Если в базе тестового региона ещё нет, он дописывается в конец
// файла, иначе заменяет собой прежний.
func spliceInline(base, cur string) (string, error) {
	curLines := splitLines(cur)
	start, end, err := findTestRegion(curLines)
	if err != nil {
		if err == errNoTestRegion {
			return "", fmt.Errorf("не нашёл тестовый регион: нет #[cfg(test)] mod и нет маркеров " +
				"regcheck:test-begin/regcheck:test-end вокруг тестов")
		}
		return "", err
	}
	testLines := curLines[start-1 : end]

	baseLines := splitLines(base)
	bStart, bEnd, err := findTestRegion(baseLines)
	var result []string
	switch {
	case err == nil:
		result = append(append([]string{}, baseLines[:bStart-1]...), testLines...)
		result = append(result, baseLines[bEnd:]...)
	case errors.Is(err, errNoTestRegion):
		result = append(append([]string{}, baseLines...), testLines...)
	default:
		return "", fmt.Errorf("в базовой версии: %v", err)
	}
	return strings.Join(result, "\n") + "\n", nil
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
	}
	// файл, попавший и в tests (автопоиском или --tests), и в --inline, иначе
	// копируется целиком, и правка утекает на базу: --inline тут главнее.
	inlineSet := make(map[string]bool, len(p.Inline))
	for _, t := range p.Inline {
		inlineSet[t] = true
	}
	var kept []string
	for _, t := range tests {
		if !inlineSet[t] {
			kept = append(kept, t)
		}
	}
	tests = kept
	if len(tests) == 0 && len(p.Inline) == 0 {
		return "", fmt.Errorf("не нашёл изменённых тестов относительно %s; перечисли их флагом --tests или --inline", base)
	}
	for _, t := range tests {
		if _, err := os.Stat(filepath.Join(root, t)); err != nil {
			return "", fmt.Errorf("тестовый файл %s не найден в рабочем дереве", t)
		}
	}
	for _, t := range p.Inline {
		if _, err := os.Stat(filepath.Join(root, t)); err != nil {
			return "", fmt.Errorf("инлайновый файл %s не найден в рабочем дереве", t)
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
	for _, t := range p.Inline {
		curData, err := os.ReadFile(filepath.Join(root, t))
		if err != nil {
			return "", err
		}
		basePath := filepath.Join(wt, t)
		baseData, err := os.ReadFile(basePath)
		if err != nil {
			return "", fmt.Errorf("инлайновый файл %s не найден в базе %s; это целиком новый файл, "+
				"--inline тут ни при чём, перенеси его как обычный тест через --tests", t, base)
		}
		result, err := spliceInline(string(baseData), string(curData))
		if err != nil {
			return "", fmt.Errorf("инлайновый файл %s: %v", t, err)
		}
		if err := os.WriteFile(basePath, []byte(result), 0o644); err != nil {
			return "", err
		}
	}
	if _, err := runCmd(wt, p.Cmd); err == nil {
		return "", fmt.Errorf("тест зелёный и на старом коде (%s), регрессию он не ловит; "+
			"если правка уже закоммичена, укажи базу без неё (--base main)", base)
	}
	all := append(append([]string{}, tests...), p.Inline...)
	return fmt.Sprintf("тест краснеет на %s и проходит на текущем коде, регрессия закрыта (тесты: %s)",
		base, strings.Join(all, ", ")), nil
}

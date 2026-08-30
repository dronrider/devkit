package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Привязка проекта к трекеру лежит в боковой директории и несёт ключ repo с
// путём корп-клона. Здесь она нужна одним этим ключом: по нему потерянная
// обвязка клона отличается от обычного проекта без доски.
const corpTrackerPath = ".devkit/tracker.local"

// corpLocal отдаёт боковую директорию корп-контура для стартовой директории.
// В корп-клоне рабочие файлы devkit (доска, файлы задач, .devkit) лежат не в
// дереве проекта, а рядом с ним, и путь к ним записан в конфиге клона ключом
// devkit.local. Пустой ответ значит домашний проект, где корень ищется прежним
// подъёмом. Наличие редиректа и есть машинный признак корп-контура: остальные
// команды спрашивают контур этой же функцией, а не разбирают конфиг заново.
func corpLocal(start string) string {
	val, err := corpGit(start, "config", "--local", "--get", "devkit.local")
	if err != nil || val == "" {
		return ""
	}
	if filepath.IsAbs(val) {
		return filepath.Clean(val)
	}
	base, err := corpCheckout(start)
	if err != nil {
		return ""
	}
	return filepath.Join(base, val)
}

// corpCheckout отдаёт директорию основного чекаута репозитория, которому
// принадлежит start, то есть родителя git-common-dir. Относительный путь
// редиректа разрешается от неё, а не от cwd и не от вершины текущего дерева: у
// дерева ветки вершина своя, и от неё путь сходился бы, только пока дерево
// лежит сиблингом проекта, как его кладёт shipctl. Дерево в стороннем месте
// молча увело бы в чужую директорию.
func corpCheckout(start string) (string, error) {
	out, err := corpGit(start, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := out
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(start)
		if err != nil {
			return "", err
		}
		dir = filepath.Join(abs, dir)
	}
	return filepath.Dir(filepath.Clean(dir)), nil
}

// corpRootErr собирает отказ поиска корня. Обвязка корп-клона живёт в .git и в
// негитигнорнутом дереве, поэтому переклонирование и «git clean -xdf» теряют её
// молча, и клон выглядит обычным проектом без доски. Когда рядом лежит боковая
// директория с привязкой к этому клону, отказ называет команду восстановления
// вместо голого «не нашёл docs/TASKS.md».
func corpRootErr(start string) error {
	msg := fmt.Sprintf("не нашёл docs/TASKS.md вверх от %s", start)
	if local := corpLostLocal(start); local != "" {
		return fmt.Errorf("%s, а рядом лежит %s с привязкой к этому клону: обвязка корп-контура потеряна, восстанавливает её «devkitctl corp»", msg, local)
	}
	return fmt.Errorf("%s", msg)
}

// corpLostLocal ищет рядом с клоном боковую директорию «*-local», чья привязка
// указывает ключом repo на этот самый клон. Чужие соседние директории с таким
// именем не в счёт, привязка называет свой клон явно.
func corpLostLocal(start string) string {
	clone, err := corpCheckout(start)
	if err != nil {
		return ""
	}
	parent := filepath.Dir(clone)
	ents, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), "-local") {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		if corpBoundTo(dir, clone) {
			return dir
		}
		// Боковая директория контура общая на все его проекты и держит их
		// подкаталогами, поэтому привязка лежит уровнем ниже (DK-583).
		inner, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, sub := range inner {
			if !sub.IsDir() {
				continue
			}
			cand := filepath.Join(dir, sub.Name())
			if corpBoundTo(cand, clone) {
				return cand
			}
		}
	}
	return ""
}

// corpBoundTo говорит, называет ли привязка боковой директории ключом repo
// именно этот клон. Чужие соседние директории с похожим именем не в счёт.
func corpBoundTo(dir, clone string) bool {
	repo := corpTrackerRepo(dir)
	if repo == "" {
		return false
	}
	if !filepath.IsAbs(repo) {
		repo = filepath.Join(dir, repo)
	}
	return corpSamePath(repo, clone)
}

// corpTrackerRepo достаёт ключ repo из привязки боковой директории. Формат
// плоский, «key = value» с решёткой на комментарий, как у deploy.local.
func corpTrackerRepo(dir string) string {
	f, err := os.Open(filepath.Join(dir, corpTrackerPath))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "repo" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if q := val[0]; (q == '"' || q == '\'') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}
		return val
	}
	return ""
}

// corpSamePath сравнивает два пути с оглядкой на симлинки: временные
// директории на macOS лежат под /var, который сам симлинк на /private/var, и
// голое сравнение строк там расходится на ровном месте.
func corpSamePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// corpGit гоняет git в стартовой директории. Ошибка возвращается как есть:
// вне git-репозитория она значит домашнее поведение, а не поломку.
func corpGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Project это найденный проект с доской: имя каталога и путь. Имя это и есть
// имя в путях API; коллизия имён из разных корней - ошибка конфига, а не
// молчаливый выбор первого попавшегося.
type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

const boardRel = "docs/TASKS.md"

func hasBoard(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(boardRel)))
	return err == nil && !fi.IsDir()
}

// isLinkedWorktree узнаёт боковое дерево задачи расхождением git-dir и
// git-common-dir, как рубеж taskctl: у дерева та же доска, и без отсева
// каждый проект множился бы на свои деревья. Нет git или репозитория, значит
// и дерева нет.
func isLinkedWorktree(dir string) bool {
	one := gitLine(dir, "rev-parse", "--git-dir")
	common := gitLine(dir, "rev-parse", "--git-common-dir")
	if one == "" || common == "" {
		return false
	}
	if !filepath.IsAbs(one) {
		one = filepath.Join(dir, one)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return filepath.Clean(one) != filepath.Clean(common)
}

func gitLine(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// scanProjects обходит корни из конфига: проект это сам корень или его прямой
// подкаталог с docs/TASKS.md, глубже обход не идёт, чтобы не ползать по
// деревьям сборки. Отдаёт проекты по имени и список ошибок для /healthz.
func scanProjects(roots []string) ([]Project, []string) {
	var found []Project
	var errs []string
	for _, root := range roots {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			errs = append(errs, fmt.Sprintf("корня %s нет", root))
			continue
		}
		if hasBoard(root) {
			found = append(found, Project{Name: filepath.Base(root), Path: root})
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			errs = append(errs, fmt.Sprintf("корень %s не читается: %v", root, err))
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if !e.IsDir() || !hasBoard(dir) || isLinkedWorktree(dir) {
				continue
			}
			found = append(found, Project{Name: e.Name(), Path: dir})
		}
	}
	byName := map[string][]Project{}
	for _, p := range found {
		byName[p.Name] = append(byName[p.Name], p)
	}
	var projects []Project
	for name, ps := range byName {
		if len(ps) > 1 {
			var paths []string
			for _, p := range ps {
				paths = append(paths, p.Path)
			}
			sort.Strings(paths)
			errs = append(errs, fmt.Sprintf(
				"имя проекта %s встречается в разных корнях (%s): развести корни в конфиге, оба пока не показываются",
				name, strings.Join(paths, ", ")))
			continue
		}
		projects = append(projects, ps[0])
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	sort.Strings(errs)
	return projects, errs
}

package main

import (
	"fmt"
	"os"
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
// и дерева нет. Оба адреса спрашиваются одним rev-parse: подпроцесс тут самое
// дорогое, а печатает утилита что попросили и в том порядке, в каком спросили.
func isLinkedWorktree(dir string) bool {
	// Строки, а не поля: путь репозитория бывает и с пробелом в имени.
	lines := strings.Split(gitLine(dir, "rev-parse", "--git-dir", "--git-common-dir"), "\n")
	if len(lines) != 2 {
		return false
	}
	one, common := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
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

// gitLine ходит через runProc, как все подпроцессы сервера: обход корней
// стоит за /api/projects и открытым /healthz, и зависший git держал бы их
// горутины вечно.
func gitLine(dir string, args ...string) string {
	out, err := runProc("git", append([]string{"-C", dir}, args...)...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// scanProjects обходит корни из конфига: проект это сам корень или его прямой
// подкаталог с docs/TASKS.md, глубже обход не идёт, чтобы не ползать по
// деревьям сборки. Отдаёт проекты по имени и список ошибок для /healthz.
func scanProjects(roots []string) ([]Project, []string) {
	var found, cands []Project
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
			if !e.IsDir() || !hasBoard(dir) {
				continue
			}
			cands = append(cands, Project{Name: e.Name(), Path: dir})
		}
	}
	// Отсев боковых деревьев стоит подпроцесса git на каждого кандидата, и это
	// самое дорогое место обхода: кандидаты спрашиваются разом, а порядок всё
	// равно наводится ниже сортировкой.
	linked := make([]bool, len(cands))
	inParallel(scanWorkers, len(cands), func(i int) { linked[i] = isLinkedWorktree(cands[i].Path) })
	for i, c := range cands {
		if !linked[i] {
			found = append(found, c)
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

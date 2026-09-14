package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

// worktreeMemo это память вердиктов отсева по каталогам. Она нужна потому, что
// молчание git под нагрузкой неотличимо от ответа «дерева нет», а цена ошибки
// несимметрична: лишний проект в списке умножает опрос досок на число боковых
// деревьев и раскручивает нагрузку сам от себя (DK-992). Прежний вердикт живёт
// до конца процесса: боковым дерево становится один раз, а обратно уезжает
// вместе с каталогом.
type worktreeMemo struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newWorktreeMemo() *worktreeMemo { return &worktreeMemo{seen: map[string]bool{}} }

// recall отдаёт прежний вердикт по каталогу, если он был.
func (m *worktreeMemo) recall(dir string) (bool, bool) {
	if m == nil {
		return false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	linked, ok := m.seen[dir]
	return linked, ok
}

// remember кладёт вердикт, полученный от ответившего git.
func (m *worktreeMemo) remember(dir string, linked bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.seen[dir] = linked
	m.mu.Unlock()
}

// keep оставляет вердикты только по названным каталогам. Слитая задача уносит
// своё боковое дерево, и без чистки путь лежал бы в памяти до перезапуска
// демона. Чужих вердиктов чистка не задевает: каталог, которого нет среди
// кандидатов обхода, в список проектов не попадает и так.
func (m *worktreeMemo) keep(dirs []string) {
	if m == nil {
		return
	}
	live := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		live[d] = true
	}
	m.mu.Lock()
	for dir := range m.seen {
		if !live[dir] {
			delete(m.seen, dir)
		}
	}
	m.mu.Unlock()
}

// isLinkedWorktree узнаёт боковое дерево задачи расхождением git-dir и
// git-common-dir, как рубеж taskctl: у дерева та же доска, и без отсева
// каждый проект множился бы на свои деревья. Оба адреса спрашиваются одним
// rev-parse: подпроцесс тут самое дорогое, а печатает утилита что попросили и
// в том порядке, в каком спросили. Второй ответ говорит, был ли ответ вообще:
// молчащий git это не «дерева нет», а неизвестность, и решает её обход выше.
// Отказ git молчанием не считается: каталог без репозитория это честный ответ.
func isLinkedWorktree(dir string) (bool, bool) {
	out, err := gitLine(dir, "rev-parse", "--git-dir", "--git-common-dir")
	if err != nil {
		// Отказ git это ответ: репозитория тут нет, значит нет и дерева.
		// Молчание ответом не считается.
		return false, !procSilent(err)
	}
	// Строки, а не поля: путь репозитория бывает и с пробелом в имени.
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		return false, true
	}
	one, common := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if one == "" || common == "" {
		return false, true
	}
	if !filepath.IsAbs(one) {
		one = filepath.Join(dir, one)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return filepath.Clean(one) != filepath.Clean(common), true
}

// gitLine ходит через runProc, как все подпроцессы сервера: обход корней
// стоит за /api/projects и открытым /healthz, и зависший git держал бы их
// горутины вечно. Отказ отдаётся отдельно от вывода: вызывающему нужно
// различать пустой ответ и неответ.
func gitLine(dir string, args ...string) (string, error) {
	out, err := runProc("git", append([]string{"-C", dir}, args...)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// scanProjects обходит корни из конфига: проект это сам корень или его прямой
// подкаталог с docs/TASKS.md, глубже обход не идёт, чтобы не ползать по
// деревьям сборки. Память вердиктов отсева приходит аргументом и бывает
// пустой: у обхода без памяти неответивший git значит «дерево», как и у
// первого обхода. Отдаёт проекты по имени и список ошибок для /healthz.
func scanProjects(roots []string, memo *worktreeMemo) ([]Project, []string) {
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
	linked, said := make([]bool, len(cands)), make([]bool, len(cands))
	broken := inParallel(scanWorkers, len(cands), func(i int) {
		linked[i], said[i] = isLinkedWorktree(cands[i].Path)
		if said[i] {
			memo.remember(cands[i].Path, linked[i])
		}
	})
	for i, c := range cands {
		if broken[i] != nil {
			errs = append(errs, fmt.Sprintf("каталог %s не проверился на боковое дерево: %v", c.Path, broken[i]))
		}
		// Молчащий git не делает каталог проектом: держится прежний вердикт
		// обхода, а без прежнего каталог считается деревом и в список не идёт.
		// Лишнее дерево в списке умножает опрос досок, и заплатить за молчание
		// пропавшей строкой дешевле, чем нагрузкой на всю машину (DK-992).
		if !said[i] {
			was, ok := memo.recall(c.Path)
			linked[i] = !ok || was
			if broken[i] == nil {
				errs = append(errs, fmt.Sprintf(
					"каталог %s: git не сказал про боковое дерево, каталог считается %s",
					c.Path, worktreeWord(linked[i], ok)))
			}
		}
		if !linked[i] {
			found = append(found, c)
		}
	}
	paths := make([]string, 0, len(cands))
	for _, c := range cands {
		paths = append(paths, c.Path)
	}
	memo.keep(paths)
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

// worktreeWord называет исход молчания словами для /healthz: прежний вердикт
// или запасной.
func worktreeWord(linked, known bool) string {
	switch {
	case known && linked:
		return "боковым деревом по прежнему обходу"
	case known:
		return "проектом по прежнему обходу"
	default:
		return "боковым деревом: прежнего вердикта нет"
	}
}

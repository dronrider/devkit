package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Изоляция параллельных сессий: у каждой задачи своё рабочее дерево.
// Ветка задачи живёт не в общем чекауте, а в git worktree рядом с проектом,
// основной чекаут остаётся на main для доски и мелочи. Незакоммиченные правки
// одной сессии тогда не лежат под ногами у другой.

type worktreeInfo struct{ Path, Branch string }

// worktrees разбирает `git worktree list --porcelain`: первым блоком git
// всегда отдаёт основной чекаут, дальше линкованные деревья.
func worktrees(root string) (primary string, linked []worktreeInfo, err error) {
	out, err := git(root, "worktree", "list", "--porcelain")
	if err != nil {
		return "", nil, err
	}
	var cur worktreeInfo
	flush := func() {
		if cur.Path == "" {
			return
		}
		if primary == "" {
			primary = cur.Path
		} else {
			linked = append(linked, cur)
		}
		cur = worktreeInfo{}
	}
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(ln, "worktree ")
		case strings.HasPrefix(ln, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(ln, "branch refs/heads/")
		}
	}
	flush()
	if primary == "" {
		return "", nil, fmt.Errorf("git worktree list не вернул ни одного дерева")
	}
	return primary, linked, nil
}

// primaryRoot приводит корень к основному чекауту: команды можно запускать и
// из worktree задачи, но доска и fast-forward живут в основном дереве. Вторым
// значением возвращается worktree, из которого запустились (пустая строка,
// если запуск уже из основного).
func primaryRoot(root string) (string, string, error) {
	primary, _, err := worktrees(root)
	if err != nil {
		return "", "", err
	}
	top, err := git(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	// git хранит в списке развёрнутые пути, а снаружи путь может прийти через
	// симлинк (на macOS /tmp и /var), сравнивать надо канонические.
	rt, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", "", err
	}
	rp, err := filepath.EvalSymlinks(primary)
	if err != nil {
		return "", "", err
	}
	if rt == rp {
		return root, "", nil
	}
	return rp, rt, nil
}

// branchOfTask: ветка называется по ID строчными, с хвостом-слагом или без
// (`dk-005`, `dk-005-worktree`). Точного имени merge не знает, поэтому матч
// по префиксу до дефиса.
func branchOfTask(branch, id string) bool {
	b, low := strings.ToLower(branch), strings.ToLower(id)
	return b == low || strings.HasPrefix(b, low+"-")
}

// taskWorktree находит worktree с веткой задачи, nil если такого нет.
// Осиротелую регистрацию (директорию снесли мимо git, запись в списке
// осталась) чинит git worktree prune, иначе start и merge упирались бы в
// несуществующий путь.
func taskWorktree(root, id string) (*worktreeInfo, error) {
	for attempt := 0; ; attempt++ {
		_, linked, err := worktrees(root)
		if err != nil {
			return nil, err
		}
		var stale *worktreeInfo
		for i := range linked {
			if !branchOfTask(linked[i].Branch, id) {
				continue
			}
			if _, err := os.Stat(linked[i].Path); err == nil {
				return &linked[i], nil
			}
			stale = &linked[i]
			break
		}
		if stale == nil {
			return nil, nil
		}
		if attempt > 0 {
			return nil, fmt.Errorf("worktree %s числится в git, но директории нет и prune запись не снял", stale.Path)
		}
		if _, err := git(root, "worktree", "prune"); err != nil {
			return nil, err
		}
	}
}

// removeWorktree прибирает дерево слитой задачи и удаляет ветку. Провал не
// ошибка: слияние к этому моменту прошло, а неубранное дерево (например, с
// untracked-черновиками) прибирается руками, подсказка уходит в отчёт.
func removeWorktree(root, wt, branch string) string {
	if _, err := git(root, "worktree", "remove", wt); err != nil {
		return fmt.Sprintf("worktree %s не убрался (лежат untracked-файлы), прибрать руками: разобрать, что там осталось, затем git worktree remove --force %s && git branch -d %s", wt, wt, branch)
	}
	git(root, "branch", "-d", branch)
	return fmt.Sprintf("worktree %s убран, ветка %s удалена", wt, branch)
}

type StartParams struct {
	ID   string
	Slug string // хвост имени ветки: dk-005-<slug>; без него ветка по ID
	Push bool   // запушить коммит доски после перевода в In progress
}

// cmdStart берёт задачу в работу в отдельном дереве: ветка по ID в worktree
// рядом с проектом, задача из Backlog переводится в In progress. Основной
// чекаут не трогается и остаётся на main.
func cmdStart(root string, p StartParams) (string, error) {
	if _, err := exec.LookPath("taskctl"); err != nil {
		return "", fmt.Errorf("taskctl не найден в PATH, собери его из devkit/taskctl")
	}
	primary, wt, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	if wt != "" {
		return "", fmt.Errorf("start запускается из основного чекаута, а не из worktree %s", wt)
	}
	root = primary
	// Замок берётся и здесь: start единственный из четырёх команд пишет и
	// коммитит доску в основном дереве, и его taskctl move с пушем посреди
	// чужого слияния бьёт в тот же зазор, что и второе слияние.
	unlock, err := acquireLock(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	main, err := mainBranch(root)
	if err != nil {
		return "", err
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	sect := b.sectOf(p.ID)
	switch sect {
	case "backlog", "in-progress":
	case "":
		return "", fmt.Errorf("%s нет на доске", p.ID)
	default:
		return "", fmt.Errorf("%s в %s, в работу берут из Backlog или In progress", p.ID, sect)
	}
	if l, err := taskWorktree(root, p.ID); err != nil {
		return "", err
	} else if l != nil {
		return "", fmt.Errorf("%s уже в работе: ветка %s в worktree %s", p.ID, l.Branch, l.Path)
	}
	low := strings.ToLower(p.ID)
	branch := low
	if p.Slug != "" {
		branch = low + "-" + p.Slug
	}
	// Корп-контур строит ветку по шаблону привязки с ключом тикета вместо
	// локального ID: ветка уходит в корп-origin при MR и обязана нести
	// конвенцию компании (DK-074, «Конвейер: что остаётся от shipctl»), не
	// локальную нумерацию доски. Зеркальная строка несёт ключ тикета своим
	// ID, поэтому подстановка берёт p.ID, а не поле key привязки: оно
	// общепроектное (префикс), а не тикета. Неполная привязка (файл есть, а
	// key пуст) отказом start не считается, домашняя ветка остаётся как
	// есть, но об этом сказано вслух в отчёте, а не молчком.
	corpNote, corpBound := "", false
	if tb, ok := loadTrackerBinding(root); ok {
		if tb.Key == "" {
			corpNote = "привязка " + corpTrackerPath + " без ключа key, ветка по локальному ID, trackctl take не зовём"
		} else {
			branch = corpBranchName(tb.Branch, p.ID, p.Slug)
			corpBound = true
		}
	}
	// Второй заход на задачу: ветка осталась с прошлого раза, worktree
	// заводится на неё, а не на новую от main. Уцелевшая ветка сильнее
	// --slug, иначе у задачи молча появлялась бы вторая ветка.
	exists := false
	if names, err := git(root, "branch", "--list", "--format=%(refname:short)"); err == nil {
		for _, n := range strings.Split(names, "\n") {
			if branchOfTask(n, p.ID) {
				branch, exists = n, true
				break
			}
		}
	}
	wtPath := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+low)
	if _, err := os.Stat(wtPath); err == nil {
		return "", fmt.Errorf("директория %s уже существует, сначала прибрать её", wtPath)
	}
	msg := []string{fmt.Sprintf("ветка %s в worktree %s", branch, wtPath)}
	// Перевод доски коммитится раньше создания worktree: ветка тогда ветвится
	// от main уже с коммитом перевода, и правки доски в дереве задачи не
	// конфликтуют при ребейзе на merge.
	if sect == "backlog" {
		plan, err := resolveDeploy(root, "")
		if err != nil {
			return "", err
		}
		mvArgs := []string{"-C", root, "move", p.ID, "in-progress", "-m", "docs(tasks): " + p.ID + " в работу"}
		if p.Push || plan.autonomous {
			mvArgs = append(mvArgs, "--push")
		}
		out, err := exec.Command("taskctl", mvArgs...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("taskctl move: %v (%s); дочинить руками: taskctl move %s in-progress", err, strings.TrimSpace(string(out)), p.ID)
		}
		msg = append(msg, "доска: "+strings.TrimSpace(string(out)))
	}
	args := []string{"worktree", "add", wtPath}
	if exists {
		args = append(args, branch)
	} else {
		args = append(args, "-b", branch, main)
	}
	if out, err := git(root, args...); err != nil {
		if sect == "backlog" {
			return "", fmt.Errorf("доска переведена, но worktree не создался:\n%s", tail(out))
		}
		return "", fmt.Errorf("worktree не создался:\n%s", tail(out))
	}
	// .devkit гитигнорнут и в новое дерево не попадает, а без него в worktree
	// не пишется журнал запусков (по нему merge подсказывает про regcheck).
	os.Mkdir(filepath.Join(wtPath, ".devkit"), 0o755)
	msg = append(msg, fmt.Sprintf("работать в %s, по готовности: shipctl merge %s (оттуда же или из основного чекаута)", wtPath, p.ID))
	// trackctl take зовётся тем же порядком, каким taskMove выше зовёт
	// taskctl: shell out, вывод в отчёт. Ни отсутствие trackctl в PATH, ни
	// его отказ start не валят: доска не должна вставать из-за трекера,
	// но остаться незамеченным это тоже не дело, поэтому строка в отчёт
	// уходит всегда.
	switch {
	case corpNote != "":
		msg = append(msg, corpNote)
	case corpBound:
		if _, err := exec.LookPath("trackctl"); err != nil {
			msg = append(msg, "trackctl не найден в PATH, тикет не переведён и не назначен: доска не встаёт из-за трекера, довести руками")
		} else if out, err := exec.Command("trackctl", "-C", root, "take", p.ID).CombinedOutput(); err != nil {
			msg = append(msg, fmt.Sprintf("trackctl take не прошёл: %v (%s); доска не встаёт из-за трекера, довести руками", err, strings.TrimSpace(string(out))))
		} else {
			msg = append(msg, "трекер: "+strings.TrimSpace(string(out)))
		}
	}
	return strings.Join(msg, "\n"), nil
}

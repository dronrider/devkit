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

// treePath: дерево задачи лежит рядом с проектом и называется ../<проект>-<id>.
// Правило одно на start и на code, иначе dry-run обещал бы одно место, а start
// заводил дерево в другом.
func treePath(codeRoot, ref string) string {
	return filepath.Join(filepath.Dir(codeRoot), filepath.Base(codeRoot)+"-"+strings.ToLower(ref))
}

// branchOfTask: ветка называется по ID строчными, с хвостом-слагом или без
// (`dk-005`, `dk-005-worktree`). Точного имени merge не знает, поэтому матч
// по префиксу до дефиса.
func branchOfTask(branch, id string) bool {
	b, low := strings.ToLower(branch), strings.ToLower(id)
	return b == low || strings.HasPrefix(b, low+"-")
}

// taskWorktree находит worktree с веткой задачи, nil если такого нет.
func taskWorktree(root, id string) (*worktreeInfo, error) {
	return taskWorktreeBy(root, func(branch string) bool { return branchOfTask(branch, id) })
}

// taskWorktreeBy это тот же поиск с чужим правилом опознания ветки: дома
// ветку узнаёт branchOfTask, в корп-клоне ключ тикета внутри шаблонного имени
// (corpBranchOfTicket). Осиротелую регистрацию (директорию снесли мимо git,
// запись в списке осталась) чинит git worktree prune, иначе start и merge
// упирались бы в несуществующий путь.
func taskWorktreeBy(root string, match func(string) bool) (*worktreeInfo, error) {
	for attempt := 0; ; attempt++ {
		_, linked, err := worktrees(root)
		if err != nil {
			return nil, err
		}
		var stale *worktreeInfo
		for i := range linked {
			if !match(linked[i].Branch) {
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
		return "", fmt.Errorf("taskctl не найден в PATH, доску двигает он: поставить набор утилит devkit (python3 ~/projects/devkit/tools/devkitctl/devkitctl.py update)")
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
	// Корп-контур держит доску в боковой директории (root), а код проекта в
	// клоне, на который привязка указывает ключом repo: там и заводится
	// дерево задачи (DK-087, находка 1), там же спрашивается main или master,
	// потому что боковая директория без единого коммита сразу после
	// devkitctl corp иначе валит именно этот вызов (находка 4).
	//
	// В репозитории кода задача названа ключом тикета, а не локальным ID
	// доски: по ключу тикета идут и ветка, и коммиты, и MR, а локальный ID в
	// корп-артефакты не едет вовсе (DK-074, «Граница доски и тикета»), это же
	// стережёт рубеж следов. Ключ тикета берётся из ссылки зеркальной строки,
	// как его берёт trackctl (tools/trackctl/board.go): ID строки для этого не
	// годится, и первый заход DK-087 подставлял в шаблон именно его (DK-124).
	// Опознаётся ключ полем key привязки, поэтому без него ключ тикета не
	// вылавливается ничем, и ветка остаётся домашней с находкой в отчёте:
	// доска не должна вставать из-за ненастроенного трекера. Зато строка,
	// которая при настроенном key на тикет не ведёт, это отказ: имя ветки
	// корп-репозитория придумывать не из чего, а локальный ID туда не едет.
	// Шаблон и codeRoot переключаются одним условием (найден ли клон), а не
	// порознь: ветка по конвенции компании в дереве без кода компании была бы
	// половинчатым и обманчивым состоянием (ревью DK-087, «расходится с
	// кодом»). Домашний проект (привязки нет) не затронут: codeRoot остаётся
	// root, ветка домашней «id-slug».
	codeRoot := root
	corpNote := ""
	// ref это имя задачи в репозитории кода: дома локальный ID, в корп-клоне
	// ключ тикета. По нему опознаётся ветка прошлого захода и называется
	// директория дерева задачи, которую git записывает в .git корп-клона.
	ref, ticket := p.ID, ""
	tb, corpBound := loadTrackerBinding(root)
	if corpBound && tb.Key != "" {
		if r := b.rowOf(p.ID); r != nil {
			ticket = corpTicketKey(tb.Key, r.Link)
		}
		if ticket == "" {
			return "", fmt.Errorf("строка %s не ведёт на тикет %s-N: в корп-контуре ветку и коммиты именует ключ тикета (DK-074, «Граница доски и тикета»), а локальный ID доски в корп-репозиторий не едет; вписать ссылку на тикет в ячейку ссылки строки", p.ID, tb.Key)
		}
	}
	if corpBound {
		if clone := corpCloneDir(root, tb.Repo); clone != "" {
			codeRoot = clone
			if ticket != "" {
				ref = ticket
			} else {
				corpNote = "привязка " + corpTrackerPath + " без ключа key, ветка и дерево задачи названы локальным ID доски: ключ тикета вылавливается из ссылки строки этим ключом, и без него имя ветки расходится с конвенцией компании; дописать key и повторить"
			}
		} else {
			corpNote = "привязка " + corpTrackerPath + " без ключа repo, ветка и дерево задачи остаются домашними: кода клона нет, дописать repo и повторить"
		}
	}
	low := strings.ToLower(ref)
	branch := low
	if p.Slug != "" {
		branch = low + "-" + p.Slug
	}
	if ticket != "" && codeRoot != root {
		branch = corpBranchName(tb.Branch, ticket, p.Slug)
	}
	main, err := mainBranch(codeRoot)
	if err != nil {
		return "", err
	}
	// Ветку прошлого захода дома узнаёт домашнее правило, а в корп-клоне ключ
	// тикета внутри шаблонного имени: шаблон привязки волен нести префикс.
	isTaskBranch := func(name string) bool { return branchOfTask(name, ref) }
	if ticket != "" && codeRoot != root {
		isTaskBranch = func(name string) bool { return corpBranchOfTicket(name, ticket) }
	}
	if l, err := taskWorktreeBy(codeRoot, isTaskBranch); err != nil {
		return "", err
	} else if l != nil {
		return "", fmt.Errorf("%s уже в работе: ветка %s в worktree %s", p.ID, l.Branch, l.Path)
	}
	// Второй заход на задачу: ветка осталась с прошлого раза, worktree
	// заводится на неё, а не на новую от main. Уцелевшая ветка сильнее
	// --slug, иначе у задачи молча появлялась бы вторая ветка.
	exists := false
	if names, err := git(codeRoot, "branch", "--list", "--format=%(refname:short)"); err == nil {
		for _, n := range strings.Split(names, "\n") {
			if isTaskBranch(n) {
				branch, exists = n, true
				break
			}
		}
	}
	wtPath := treePath(codeRoot, low)
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
	if out, err := git(codeRoot, args...); err != nil {
		if sect == "backlog" {
			return "", fmt.Errorf("доска переведена, но worktree не создался:\n%s", tail(out))
		}
		return "", fmt.Errorf("worktree не создался:\n%s", tail(out))
	}
	if codeRoot == root {
		// .devkit гитигнорнут и в новое дерево не попадает, а без него в
		// worktree не пишется журнал запусков (по нему merge подсказывает про
		// regcheck). В корп-контуре с найденным клоном .devkit уже есть в
		// боковой директории (devkitctl corp его заводит), и findRoot дерева
		// задачи находит его редиректом, создавать тут нечего.
		os.Mkdir(filepath.Join(wtPath, ".devkit"), 0o755)
	}
	msg = append(msg, fmt.Sprintf("работать в %s, по готовности: shipctl merge %s (оттуда же или из основного чекаута)", wtPath, p.ID))
	if corpNote != "" {
		msg = append(msg, corpNote)
	}
	// trackctl take зовётся тем же порядком, каким taskMove выше зовёт
	// taskctl: shell out, вывод в отчёт. Ни отсутствие trackctl в PATH, ни
	// его отказ start не валят: доска не должна вставать из-за трекера,
	// но остаться незамеченным это тоже не дело, поэтому строка в отчёт
	// уходит всегда. Зовётся при любой привязке, даже неполной (без repo):
	// трекер про свой клон ничего не знает, take работает от ключа тикета.
	// Ключ берётся тот же, что ушёл в имя ветки: локальный ID доски трекеру
	// назвать нечем, там такого тикета нет (DK-124). Без ключа key в привязке
	// вылавливать ключ нечем, и в take уходит ID, как уходил раньше: контур
	// тогда всё равно не настроен, и отказ трекера скажет об этом сам.
	take := ticket
	if take == "" {
		take = p.ID
	}
	if corpBound {
		if _, err := exec.LookPath("trackctl"); err != nil {
			msg = append(msg, "trackctl не найден в PATH, тикет не переведён и не назначен: доска не встаёт из-за трекера, довести руками")
		} else if out, err := exec.Command("trackctl", "-C", root, "take", take).CombinedOutput(); err != nil {
			msg = append(msg, fmt.Sprintf("trackctl take не прошёл: %v (%s); доска не встаёт из-за трекера, довести руками", err, strings.TrimSpace(string(out))))
		} else {
			msg = append(msg, "трекер: "+strings.TrimSpace(string(out)))
		}
	}
	return strings.Join(msg, "\n"), nil
}

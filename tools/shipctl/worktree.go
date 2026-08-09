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

// placeWrapping кладёт в свежее дерево задачи каталог обвязки .devkit, а для
// корп-клона ещё и ссылки на соседние деревья правил. Каталог гитигнорнут и в
// новое дерево не попадает, а без него не пишется журнал запусков (по нему merge
// подсказывает про regcheck); в корп-клоне журнал всё равно идёт редиректом в
// боковую директорию (findRoot по devkit.local), а .devkit worktree несёт только
// ссылки -- через них тонкий файл разворачивает правила доски и корп-локальное.
// Своему проекту ссылки не нужны: дерево правил приезжает в него коммитом, и
// чекаут несёт их сам. Корп-репозиторий чужой, ссылки туда не коммитятся, и в
// свежее дерево их кладёт start. Дерево задачи лежит сиблингом клона (treePath),
// поэтому относительные цели готовых ссылок клона (их положил devkitctl corp)
// верны и тут -- копируются они дословно. Копия окна (code, p.Tree != "") тут
// не затрагивается: место у неё своё, и относительные цели клона там могут не
// сойтись. Провал молчит: обвязка это шов, а не предусловие, отсутствие ссылок
// ловит доктор.
func placeWrapping(wtPath, codeRoot, root string, fresh bool) {
	devkitDir := filepath.Join(wtPath, ".devkit")
	os.Mkdir(devkitDir, 0o755)
	if !fresh || codeRoot == root {
		return
	}
	// devkit -- на дерево правил, local -- на боковую директорию с AGENTS.md.
	// Имена это контракт с клиентом (импорт тонкого файла идёт как
	// @.devkit/<имя>/...), источник правды -- tools/devkitctl/rules.py
	// (DEVKIT_LINK, LOCAL_LINK): раскладку ссылок заводит devkitctl.
	for _, name := range []string{"devkit", "local"} {
		target, err := os.Readlink(filepath.Join(codeRoot, ".devkit", name))
		if err != nil {
			continue
		}
		os.Symlink(target, filepath.Join(devkitDir, name))
	}
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

// samePath сравнивает две директории по канону: git отдаёт пути развёрнутыми,
// а собранный по имени путь может лежать под симлинком (на macOS /tmp и /var),
// и сравнение строк тогда врёт.
func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}

// detachWorktree переводит копию окна на слитый коммит и удаляет ветку. Копия
// живёт дольше задачи: в ней открыто окно редактора, и снос директории из-под
// живого окна убивает окно вместе с сессией (DK-192). Отцепить ветку всё равно
// надо, иначе она занята деревом и git branch -d её не отдаёт.
func detachWorktree(root, wt, branch string) string {
	if out, err := git(wt, "checkout", "--detach"); err != nil {
		return fmt.Sprintf("копия окна %s не переведена на слитый коммит (%s): ветка %s осталась занятой, довести руками: git -C %s checkout --detach && git branch -d %s",
			wt, tail(out), branch, wt, branch)
	}
	if out, err := git(root, "branch", "-d", branch); err != nil {
		return fmt.Sprintf("копия окна %s стоит на слитом коммите, а ветка %s не удалилась (%s), убрать руками: git branch -d %s",
			wt, branch, tail(out), branch)
	}
	return fmt.Sprintf("копия окна %s переведена на слитый коммит, ветка %s удалена: окно в копии живо", wt, branch)
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
	// Tree это готовое дерево, в которое кладётся ветка задачи: копия окна
	// (shipctl code). Пустое значит обычный путь, своё одноразовое дерево на
	// задачу. Ветка при этом заводится одинаково, различается только место: у
	// задачи не должно появиться второго способа получить ветку.
	Tree string
}

// switchable проверяет, можно ли переключать готовое дерево на ветку задачи.
// Черновики прошлой задачи это отказ с перечнем файлов, а не молчаливый
// перенос: незакоммиченное уехало бы в чужую ветку, и заметили бы это в лучшем
// случае на ревью. Своя же ветка под ногами значит, что окно просто открывают
// заново, и переключать нечего.
func switchable(tree, branch string) error {
	cur, err := git(tree, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if cur == branch {
		return nil
	}
	// quotepath выключен: с ним русские имена файлов уезжают в отчёт
	// восьмеричными кодами, и перечень черновиков нечитаем ровно там, где по
	// нему надо решать, что с ними делать.
	st, err := git(tree, "-c", "core.quotepath=false", "status", "--porcelain")
	if err != nil {
		return err
	}
	if st != "" {
		return fmt.Errorf("в %s лежат правки прошлой задачи, на ветку %s дерево так не переключить: закоммитить их в свою ветку либо убрать, и повторить:\n%s", tree, branch, tail(st))
	}
	return nil
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
	} else if l != nil && !(p.Tree != "" && samePath(l.Path, p.Tree)) {
		busy := fmt.Sprintf("%s уже в работе: ветка %s в worktree %s", p.ID, l.Branch, l.Path)
		if p.Tree != "" {
			// Второе окно на занятой ветке: выложить её дважды git не даёт, и
			// звать это надо своим именем. Ревьюверу ветка нужна на чтение, и
			// берётся она detached, а замечания едут в файл задачи.
			busy += "; выложить её вторым деревом нельзя, на чтение брать detached (git -C " + p.Tree + " checkout --detach " + l.Branch + "), коммиты в ветку идут только из занявшего дерева"
		}
		return "", fmt.Errorf("%s", busy)
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
	place := "worktree"
	if p.Tree != "" {
		wtPath, place = p.Tree, "копии окна"
		// Чистота проверяется до перевода доски: отказ тогда ничего не успел
		// поменять, а перевод в In progress пришлось бы откатывать руками.
		if err := switchable(wtPath, branch); err != nil {
			return "", err
		}
	} else if _, err := os.Stat(wtPath); err == nil {
		return "", fmt.Errorf("директория %s уже существует, сначала прибрать её", wtPath)
	}
	msg := []string{fmt.Sprintf("ветка %s в %s %s", branch, place, wtPath)}
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
	// Готовое дерево переключается на ветку, своё заводится вместе с ней.
	where, args := codeRoot, []string{"worktree", "add", wtPath}
	if p.Tree != "" {
		where, args = wtPath, []string{"checkout", branch}
		if !exists {
			args = []string{"checkout", "-b", branch, main}
		}
	} else if exists {
		args = append(args, branch)
	} else {
		args = append(args, "-b", branch, main)
	}
	if out, err := git(where, args...); err != nil {
		if sect == "backlog" {
			return "", fmt.Errorf("доска переведена, но ветка не выложена в дерево:\n%s", tail(out))
		}
		return "", fmt.Errorf("ветка не выложена в дерево:\n%s", tail(out))
	}
	// Каталог обвязки .devkit в новое дерево не попадает (гитигнорнут), а без
	// него в worktree не пишется журнал запусков (по нему merge подсказывает про
	// regcheck); свежему дереву корп-клона он нужен ещё и под ссылки на соседние
	// деревья правил. Разбор -- в placeWrapping.
	placeWrapping(wtPath, codeRoot, root, p.Tree == "")
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

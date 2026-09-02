package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// mainAheadFinds ловит код-коммиты main впереди origin (DK-602): диагностика
// видна до отказа пуша, а не по факту отбитого хука. Раньше такое состояние
// запирало пуш чистой доски целиком, весь диапазон origin/main..main читался
// как «не доска» из-за единственного код-коммита где угодно в нём. Калитка
// shipctl push (tools/shipctl/push.go) теперь пускает диапазон по коммитам, но
// код, который origin ещё не видел, всё равно стоит назвать: это либо мелочь,
// слитая в main мимо ship/merge однокоммитным исключением из правила доски,
// либо забытый пуш. Без origin, без main или без опережения находок нет: вне
// git-репозитория (голая доска в тестовой директории) молчать честнее, чем
// падать.
func mainAheadFinds(root string) []string {
	main := gitMainBranch(root)
	if main == "" {
		return nil
	}
	up := "origin/" + main
	if _, err := gitRevParse(root, "--verify", up); err != nil {
		return nil
	}
	if err := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", up, main).Run(); err != nil {
		return nil
	}
	log, err := gitRun(root, "log", "--reverse", up+".."+main, "--format=%H%x09%s")
	if err != nil {
		return nil
	}
	log = strings.TrimSpace(log)
	if log == "" {
		return nil
	}
	var shas, subjs []string
	for _, ln := range strings.Split(log, "\n") {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok {
			continue
		}
		// --no-renames: у переименования --name-only печатает только путь
		// назначения, и перенос кода в docs/tasks/ сошёл бы за доску.
		files, err := gitRun(root, "show", "--no-renames", "--name-only", "--pretty=", sha)
		if err != nil {
			continue
		}
		if boardOnlyFiles(files) {
			continue
		}
		shas = append(shas, sha)
		subjs = append(subjs, subj)
	}
	if len(shas) == 0 {
		return nil
	}
	first := shas[0]
	if len(first) > 12 {
		first = first[:12]
	}
	return []string{fmt.Sprintf("%s: main впереди %s на %d %s с кодом, первый %s %q",
		boardPath(root), up, len(shas), pluralCommits(len(shas)), first, subjs[0])}
}

// boardOnlyFiles отвечает, что все файлы в списке это доска (docs/TASKS.md,
// docs/TASKS-archive.md, docs/tasks/). Тот же критерий, что boardOnly в
// tools/shipctl/ops.go: taskctl и shipctl это разные go-модули, а
// классификатор на три строки не стоит выносить в общий пакет ради одного
// вызова с каждой стороны.
func boardOnlyFiles(files string) bool {
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f != "docs/TASKS.md" && f != "docs/TASKS-archive.md" && !strings.HasPrefix(f, "docs/tasks/") {
			return false
		}
	}
	return true
}

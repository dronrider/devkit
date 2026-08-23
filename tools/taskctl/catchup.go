package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Отставание бокового дерева (DK-269): дерево харнеса живёт в detached HEAD,
// ветку main в него не чекаутить, она занята основным чекаутом, и доска в нём
// молча читается устаревшей как свежая. Отставание видно тремя местами:
// предупреждением в list/show (staleBoardNote), явной командой catchup и
// SessionStart-хуком hooks/board-catchup.sh, который зовёт её с --hook.
// Сеть не ходит ни в одном из них: указателем свежести служит локально
// известная ссылка, а освежает её обычный git-цикл машины (push, pull и fetch
// в основном чекауте двигают ссылки для всех деревьев репозитория, у
// линкованных они общие).

// catchupRef называет указатель, до которого дерево догоняет catchup: сначала
// удалённый, потому что доска пушится в origin сразу и это источник правды,
// а при его отсутствии локальная ветка, как в репозитории без remote.
func catchupRef(root string) string {
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := gitRevParse(root, "--verify", "--quiet", ref); err == nil {
			return ref
		}
	}
	return ""
}

// headBranch называет ветку, на которой стоит дерево, пустая строка значит
// detached HEAD. Вне git-репозитория тоже пустая: дерево без ветки и без
// указателя отстанет отказом команды, а не падением.
func headBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "symbolic-ref", "-q", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// treeLag возвращает отставание HEAD до ref в коммитах и то, что HEAD вообще
// его предок. Разошедшееся дерево с собственными коммитами «позади» не
// называется: догонять его чекаутом нельзя, собственные коммиты пропадут из
// HEAD.
func treeLag(root, ref string) (count int, ancestor bool) {
	if err := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", "HEAD", ref).Run(); err != nil {
		return 0, false
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD.."+ref).Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// treeClean требует чистого дерева по закоммиченным файлам. Непрочитанные
// (untracked) грязью не считаются: чекаут их не трогает, а коллизию с входящим
// файлом git отказывает сам.
func treeClean(root string) bool {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "-uno").Output()
	return err == nil && strings.TrimSpace(string(out)) == ""
}

// gitBusy замечает идущую операцию git. Чистое detached-дерево посреди rebase,
// merge или bisect выглядит «позади main» так же, как отставшее боковое
// дерево, и чекаут на середине сломал бы чужую операцию.
func gitBusy(root string) bool {
	for _, n := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
		p, err := gitRevParse(root, "--git-path", n)
		if err != nil || p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return true
		}
	}
	return false
}

// pluralCommits склоняет «коммит» по числу.
func pluralCommits(n int) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return "коммитов"
	}
	switch n % 10 {
	case 1:
		return "коммит"
	case 2, 3, 4:
		return "коммита"
	default:
		return "коммитов"
	}
}

// staleBoardNote это первая строка list и show, когда дерево с доской позади
// свежего указателя. Предупреждение стоит до секций и карты задачи: устаревшие
// строки обязаны встретиться читателю после объяснения, почему им нельзя
// верить, а не до него. Detached-дереву (боковые деревья харнеса) оно зовёт
// catchup, основному чекауту на main git pull, а деревья задач на ветках
// молчат: их доска отстаёт от main по построению, и предупреждение там шум.
// Машинный вывод --json предупреждение не носит: его читатели сверяют свои
// поля, а не разбирают печатный текст.
func staleBoardNote(root string) string {
	ref := catchupRef(root)
	if ref == "" {
		return ""
	}
	branch := headBranch(root)
	if branch != "" && branch != "main" && branch != "master" {
		return ""
	}
	n, behind := treeLag(root, ref)
	if !behind || n == 0 {
		return ""
	}
	how := "git pull"
	if branch == "" {
		how = "taskctl catchup"
	}
	return fmt.Sprintf("доска позади %s на %d %s, догнать: %s", ref, n, pluralCommits(n), how)
}

// cmdCatchup догоняет detached-дерево до свежего указателя чекаутом на sha.
// Гард «не на ветке, без идущей операции, чистое, позади» из постановки, отказ
// называет причину. Режим --hook зовёт SessionStart-хук: хук стоит у всех
// сессий машины, поэтому обо всём, до чего догонять нечего (основной чекаут,
// дерево задачи, уже свежее) или что не его дело (не линкованное дерево, нет
// указателя), catchup молчит, а сделанный подтяг и отказ с причиной наоборот
// печатаются: сессия обязана знать, что доска под ней могла остаться старой.
func cmdCatchup(root string, hook bool) (string, error) {
	if hook && !linkedWorktree(root) {
		return "", nil
	}
	branch := headBranch(root)
	if branch != "" {
		if hook {
			return "", nil
		}
		return "", fmt.Errorf("дерево стоит на ветке %s, а catchup догоняет боковое дерево в detached HEAD", branch)
	}
	ref := catchupRef(root)
	if ref == "" {
		if hook {
			return "", nil
		}
		return "", fmt.Errorf("не нашёл указателя свежести (origin/main или main), догонять не до чего")
	}
	if gitBusy(root) {
		return hookSay(hook, "в дереве идёт операция git (rebase, merge или bisect), catchup её не трогает")
	}
	if !treeClean(root) {
		return hookSay(hook, "дерево не чистое, двигать файлы под правками нельзя: закоммить или почисти его")
	}
	n, behind := treeLag(root, ref)
	if !behind {
		if hook {
			return "", nil
		}
		return "", fmt.Errorf("HEAD не позади %s: расходится с ним или впереди него, догонять нельзя", ref)
	}
	if n == 0 {
		if hook {
			return "", nil
		}
		return fmt.Sprintf("дерево уже на актуальном %s, отставания нет", ref), nil
	}
	sha, err := gitRevParse(root, ref)
	if err != nil {
		return hookSay(hook, "не смог снять sha с "+ref)
	}
	if out, err := exec.Command("git", "-C", root, "checkout", "-q", sha).CombinedOutput(); err != nil {
		return hookSay(hook, fmt.Sprintf("checkout на %s не прошёл: %v\n%s", ref, err, out))
	}
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return fmt.Sprintf("дерево догнано до %s (%s), приехало %d %s", sha, ref, n, pluralCommits(n)), nil
}

// hookSay печатает отказ в одной из двух форм: человек получает ошибку с кодом
// 1, хук строку в stdout, её увидит родившаяся сессия.
func hookSay(hook bool, reason string) (string, error) {
	if hook {
		return "боковое дерево не догнано: " + reason, nil
	}
	return "", fmt.Errorf("%s", reason)
}

// Package freshtree выкладывает коммит в одноразовое дерево и собирает
// окружение прогона, отвязанное от дома сессии. Прогретый чекаут и живые HOME
// с PATH прячут дефекты класса «зелено у исполнителя, красно на чужой машине»,
// и одинаково прячут их у слияния (shipctl merge) и у обкатки сценария
// (taskctl rehearse). Кирпичи писались под слияние (DK-641) и переехали сюда
// целиком, когда за ними пришёл второй читатель: две копии разошлись бы на
// первой правке списка переменных.
package freshtree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Env собирает окружение прогона. Живой HOME сессии подменяется временным,
// каталоги из-под него уходят из PATH, переменные харнеса CLAUDE* уносятся:
// прогон обязан зеленеть на чужой машине, а не на прогретой раскладке дома
// исполнителя. Уносятся и указатели внутрь дома (GOPATH, GOMODCACHE,
// PYTHONPATH, VIRTUAL_ENV): формально это не HOME и не PATH, но живую
// раскладку они возвращают в прогон тем же путём. Тулчейны вне дома
// (/usr/bin, /opt/homebrew) остаются, иначе команде нечем работать.
func Env(home, tmpHome string) []string {
	homePointers := map[string]bool{
		"GOPATH": true, "GOMODCACHE": true, "PYTHONPATH": true, "VIRTUAL_ENV": true,
	}
	var out []string
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		switch {
		case name == "HOME" || strings.HasPrefix(name, "CLAUDE") || homePointers[name]:
			continue
		case name == "PATH":
			kv = "PATH=" + TrimHomePath(val, home)
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+tmpHome)
}

// TrimHomePath убирает из PATH каталоги под home: там живут обвязки сессии
// вроде ~/bin и ~/.local/bin, которых на чужой машине нет.
func TrimHomePath(path, home string) string {
	if home == "" {
		return path
	}
	var kept []string
	for _, p := range filepath.SplitList(path) {
		if p == home || strings.HasPrefix(p, home+string(os.PathSeparator)) {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// Make выкладывает коммит во временное detached-дерево и заводит рядом пустой
// временный HOME. Прогретый worktree ветки несёт следы работы исполнителя
// (артефакты сборки, __pycache__), и зелень в нём не доказывает зелень свежего
// чекаута: на этой разнице дефект 98b43e7 проехал слияние и всплыл после
// выката. Уборка на вызывающем: cleanup зовётся и при провале прогона, отказ
// временных деревьев не копит. Префикс идёт в имя временного каталога, по нему
// в `git worktree list` видно, чей это прогон.
func Make(root, sha, prefix string) (tree, home string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", "", nil, err
	}
	tree = filepath.Join(tmp, "tree")
	home = filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		os.RemoveAll(tmp)
		return "", "", nil, err
	}
	if out, err := git(root, "worktree", "add", "--detach", tree, sha); err != nil {
		os.RemoveAll(tmp)
		return "", "", nil, wrap(err, out)
	}
	cleanup = func() {
		git(root, "worktree", "remove", "--force", tree)
		os.RemoveAll(tmp)
	}
	return tree, home, cleanup, nil
}

// git зовёт гит в каталоге root и отдаёт вывод вместе с ошибкой: у
// `worktree add` причина отказа живёт в выводе, а не в коде возврата.
func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// wrap приклеивает к ошибке гита его же вывод, когда он есть.
func wrap(err error, out string) error {
	if out == "" {
		return err
	}
	return &gitError{err: err, out: out}
}

type gitError struct {
	err error
	out string
}

func (e *gitError) Error() string { return e.err.Error() + ": " + e.out }
func (e *gitError) Unwrap() error { return e.err }

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// gitRevParse запускает «git -C root rev-parse <args>» и возвращает вывод без
// хвостового перевода строки. Ошибка возвращается как есть, толкует её вызывающий.
func gitRevParse(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root, "rev-parse"}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// linkedWorktree отвечает, что root это линкованное дерево git, а не основной
// чекаут: git-dir и git-common-dir расходятся только там. Вне git-репозитория
// и при недоступном git отвечает false, иначе доски на временных директориях
// без git (а таких большинство тестов) ловили бы отказ на ровном месте.
func linkedWorktree(root string) bool {
	gitDir, err := gitRevParse(root, "--git-dir")
	if err != nil {
		return false
	}
	commonDir, err := gitRevParse(root, "--git-common-dir")
	if err != nil {
		return false
	}
	return gitDir != commonDir
}

// boardGuard отказывает изменяющей доску команде, запущенной из линкованного
// worktree: доску правит только диспетчер и только в основном чекауте
// (RULES.board.md, «Доска в руках диспетчера»). Считается по root, который
// taskctl уже нашёл, а не по текущей директории: штатное слияние идёт из
// дерева задачи и двигает доску вызовом «taskctl -C <основной чекаут> move»
// (это делает shipctl), и отсчёт от cwd отказал бы собственному конвейеру.
func boardGuard(root, cmd string) error {
	if !linkedWorktree(root) {
		return nil
	}
	return fmt.Errorf("%s это линкованный worktree, доску правит только диспетчер в основном чекауте: запусти «taskctl -C <основной чекаут> %s ...»", root, cmd)
}

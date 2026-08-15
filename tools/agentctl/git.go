package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// recordCommitSubject складывает subject коммита строки вердикта: запись идёт
// в файл задачи, и subject держит тот же вид, что у ручных коммитов строк
// «Хода работы» в этом репозитории. Ярлык в subject строчный, как в истории,
// а в строку файла идёт с заглавной.
func recordCommitSubject(id, label string) string {
	return fmt.Sprintf("docs(tasks): %s строка %s в ход работы", id, strings.ToLower(label))
}

// taskRecordGit это git-обёртка для коммита строки вердикта. Отдельна от
// corpGit: той ошибка нужна как есть, а здесь удобна сводка с выводом git.
func taskRecordGit(root string, args ...string) (string, error) {
	return taskRecordGitCmd(root, nil, args...)
}

// taskRecordGitCmd запускает git с явным окружением и служит точкой перехвата
// в тесте: пуш обязан уйти ровно «git -C <корень> push» с DEVKIT_PUSH_OK=1,
// без открутки credential.helper и прочих -c.
var taskRecordGitCmd = func(root string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// recordWarnLine печатает отказ коммита: молчание вернуло бы исходный баг
// DK-120 в тихом виде, строка без коммита всплыла бы только на shipctl merge.
func recordWarnLine(err error) {
	fmt.Fprintln(recordWarn, "строка вердикта записана, но не закоммичена:", err)
}

// commitTaskRecord коммитит файл задачи после записи строки вердикта и
// запускает пуш. Приём тот же, что у taskctl -m --push: git -C корня
// репозитория, в add и commit идут ровно тронутые пути (коммит по pathspec
// не задевает чужой индекс), пуш идёт с DEVKIT_PUSH_OK=1, без него хук
// pre-push отобьёт пуш из сессии агента как самовольный. Вне git-дерева всё
// молчит: запись в файле уже лежит, и коммит остаётся за тем, кто звал.
// Отказ add или commit печатается в stderr: молчание вернуло бы исходный баг
// в тихом виде, строка без коммита всплыла бы только на shipctl merge.
func commitTaskRecord(root, path, subject string) {
	if _, err := taskRecordGit(root, "rev-parse", "--git-dir"); err != nil {
		return
	}
	if _, err := taskRecordGit(root, "add", "--", path); err != nil {
		recordWarnLine(err)
		return
	}
	if _, err := taskRecordGit(root, "commit", "-m", subject, "--", path); err != nil {
		recordWarnLine(err)
		return
	}
	taskRecordPush(root)
}

// recordWarn это выход предупреждений коммита строки, точка перехвата в тесте.
var recordWarn = func() io.Writer { return os.Stderr }()

// taskRecordPush пушит коммит строки вердикта. Отдельно от taskRecordGit:
// разрешение хука pre-push ставится только этому вызову, а не любой команде
// git. Приём тот же, что у taskctl -m --push: пустой credential.helper тут
// не лечит виснущий на залоченной связке osxkeychain, а отрезает рабочую
// учётку, из-за чего пуш падает даже на разблокированной связке.
func taskRecordPush(root string) {
	taskRecordGitCmd(root, append(os.Environ(), "DEVKIT_PUSH_OK=1"), "push")
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// recordCommitSubject складывает subject коммита строки вердикта: запись идёт
// в файл задачи, и subject держит тот же вид, что у ручных коммитов строк
// «Хода работы» в этом репозитории.
func recordCommitSubject(id, label string) string {
	return fmt.Sprintf("docs(tasks): %s строка %s в ход работы", id, label)
}

// taskRecordGit это git-обёртка для коммита строки вердикта. Отдельна от
// corpGit: corp-редирект читается без окружения, а пушу здесь нужен рубёж
// хука pre-push, и окружение с nil значит наследовать родительское целиком.
func taskRecordGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = pushEnv(args)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// pushEnv выдаёт разрешение на пуш хуку pre-push. Пуш коммита доски разрешён
// правилами и из сессии агента, и путь у него один, через утилиты devkit:
// рубеж отличает этот пуш от самовольного только по переменной. Нулевое
// окружение это наследование родительского, поэтому обычным командам git оно
// и остаётся.
func pushEnv(args []string) []string {
	if len(args) == 0 || args[0] != "push" {
		return nil
	}
	return append(os.Environ(), "DEVKIT_PUSH_OK=1")
}

// commitTaskRecord коммитит файл задачи после записи строки вердикта и
// запускает пуш. Приём тот же, что у taskctl -m --push: git -C корня
// репозитория, в add и commit идут ровно тронутые пути (коммит по pathspec
// не задевает чужой индекс), пуш идёт с DEVKIT_PUSH_OK=1 и бессеточным
// credential.helper, иначе неинтерактивная сессия висит на osxkeychain,
// ждя разблокировки связки, которой некому показать диалог. Вне git-дерева
// всё молчит: запись в файле уже лежит, и коммит остаётся за тем, кто звал.
func commitTaskRecord(root, path, subject string) {
	if _, err := taskRecordGit(root, "rev-parse", "--git-dir"); err != nil {
		return
	}
	if _, err := taskRecordGit(root, "add", "--", path); err != nil {
		return
	}
	if _, err := taskRecordGit(root, "commit", "-m", subject, "--", path); err != nil {
		return
	}
	taskRecordGit(root, "-c", "credential.helper=", "push")
}

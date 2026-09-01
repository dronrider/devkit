// Package gitrun зовёт git за утилиты доски. От голого exec.Command он
// отличается двумя вещами, и обе про сессию, в которой нет человека: запрос
// учётки закрыт, а разговор с удалённым репозиторием идёт с пределом времени.
// Повод завести пакет дал DK-697: пуш доски из неинтерактивной сессии вставал
// на диалоге связки ключей и не кончался ничем, коммиты копились локально, а
// соседние сессии ловили конфликты на отставшем remote. Копии в taskctl и
// shipctl разошлись бы на первой правке списка переменных, как уже разошлись
// копии каркаса перед DK-237.
package gitrun

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout это предел разговора с удалённым репозиторием. Пуш доски
// укладывается в пару секунд, слияние с тегом ненамного дольше, так что
// тридцати секунд хватает с запасом даже на медленном канале, а вечное
// ожидание связки ключей обрывается раньше, чем сессия успевает встать.
const DefaultTimeout = 30 * time.Second

// TimeoutEnv поднимает предел там, где канал и правда долгий: большой первый
// пуш, зеркало через океан, репозиторий с крупными файлами.
const TimeoutEnv = "DEVKIT_GIT_TIMEOUT"

// remoteCmds это команды, которые ходят в сеть и потому могут упереться в
// учётку. Локальным командам предел не ставится: коммит в большом дереве
// бывает долгим сам по себе, и обрывать его нечестно.
var remoteCmds = map[string]bool{
	"push": true, "fetch": true, "pull": true, "clone": true, "ls-remote": true,
}

// Timeout читает предел из окружения. Кривое значение это громкая ошибка, а не
// молчаливый возврат к умолчанию: иначе опечатка в переменной оборачивается
// тем самым зависанием, ради которого предел и заводился.
func Timeout() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(TimeoutEnv))
	if raw == "" {
		return DefaultTimeout, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s = %q: предел читается как длительность го, например 45s или 2m", TimeoutEnv, raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s = %q: предел должен быть больше нуля", TimeoutEnv, raw)
	}
	return d, nil
}

// Env собирает окружение вызова. Терминальный запрос git закрыт, помощники
// ввода пароля унесены, ssh идёт в пакетном режиме: у сессии агента нет
// человека, который ответит на вопрос, и вместо ответа получается тишина.
// Диалог связки ключей macOS ничем из этого не закрывается, его ловит предел
// времени. Пушу вдобавок выдаётся разрешение для хука pre-push: правила
// разрешают пуш доске и автономному режиму, а рубеж отличает их от
// самовольного пуша агента только по этой переменной.
func Env(args []string) []string {
	drop := map[string]bool{
		"GIT_ASKPASS": true, "SSH_ASKPASS": true, "GIT_TERMINAL_PROMPT": true,
		"SSH_ASKPASS_REQUIRE": true,
	}
	var out []string
	ssh := "ssh"
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		if name == "GIT_SSH_COMMAND" && strings.TrimSpace(val) != "" {
			ssh = val
			continue
		}
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS_REQUIRE=never",
		"GIT_SSH_COMMAND="+ssh+" -o BatchMode=yes",
	)
	if len(args) > 0 && args[0] == "push" {
		out = append(out, "DEVKIT_PUSH_OK=1")
	}
	return out
}

// Run выполняет git в дереве root. Вывод отдаётся склеенным, ошибка уже
// обёрнута именем команды, так что обёртки утилит остаются тонкими. Нулевой
// или отрицательный limit снимает предел, им пользуются тесты.
func Run(root string, args []string, limit time.Duration) (string, error) {
	return RunContext(context.Background(), root, args, limit)
}

// RunContext это Run со вторым поводом оборвать разговор. Предел времени
// стережёт вызов вслепую, по часам, а контекст приходит от того, кто знает про
// обрыв больше часов. Обрыв по контексту убивает ту же группу процессов, что и
// предел.
func RunContext(ctx context.Context, root string, args []string, limit time.Duration) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("git позван без команды")
	}
	full := args
	if root != "" {
		full = append([]string{"-C", root}, args...)
	}
	cmd := exec.Command("git", full...)
	cmd.Env = Env(args)
	// Убивается вся группа процессов, а не один git: помощник учётки это его
	// потомок, и смерть одного git оставила бы висеть спящий помощник вместе с
	// диалогом связки ключей.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Wait после смерти процесса ждёт закрытия труб, и держать их открытыми
	// может потомок, ушедший из группы своим форком. Секунда отсрочки закрывает
	// трубы сама, иначе вечное ожидание переезжает с процесса на чтение вывода.
	cmd.WaitDelay = time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf

	limited := limit > 0 && remoteCmds[args[0]]
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("git %s: %v", args[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Буфер читается только после Wait: до него в него льют горутины exec.Cmd,
	// и чтение вперёд Wait это гонка, а не просто неполный вывод.
	wait := func(err error) (string, error) {
		out := strings.TrimSpace(buf.String())
		if err != nil {
			return out, fmt.Errorf("git %s: %v (%s)", args[0], err, out)
		}
		return out, nil
	}
	kill := func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	// Нулевой канал в select блокирует навсегда, поэтому снятый предел и
	// пустой контекст обходятся без второй ветки кода.
	var fired <-chan time.Time
	if limited {
		timer := time.NewTimer(limit)
		defer timer.Stop()
		fired = timer.C
	}
	select {
	case err := <-done:
		return wait(err)
	case <-fired:
		kill()
		return strings.TrimSpace(buf.String()), hangErr(root, args, limit)
	case <-ctx.Done():
		kill()
		return strings.TrimSpace(buf.String()), fmt.Errorf("git %s оборван: %v", args[0], ctx.Err())
	}
}

// hangErr объясняет обрыв. Отказ по пределу видит человек, у которого только
// что встал конвейер, и ему нужны причина и ход руками, а не слово timeout.
func hangErr(root string, args []string, limit time.Duration) error {
	where := ""
	if root != "" {
		where = " -C " + root
	}
	return fmt.Errorf("git %s не кончился за %s и убит вместе с потомками. "+
		"Обычно так выглядит запрос учётки: помощник вроде osxkeychain ждёт человека, "+
		"а в сессии агента отвечать ему некому. Руками: git%s %s, "+
		"разблокировать связку ключей и повторить команду. "+
		"Если канал просто долгий, поднять предел переменной %s (например %s=2m)",
		args[0], limit, where, strings.Join(args, " "), TimeoutEnv, TimeoutEnv)
}

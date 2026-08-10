package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// cmdExec подставляет значение секрета в окружение подпроцесса и запускает
// команду. Имя переменной окружения равно имени секрета: так набор секретов
// задаёт набор переменных, а модель работает только именами. Значение в stdout
// самой secretctl не попадает: его пишет подпроцесс, и только если он сам
// решит его напечатать. Код выхода подпроцесса пробрасывается как есть, чтобы
// обвязка видела реальный исход команды, а не «exec прошёл».
func cmdExec(b Backend, name string, command []string) (int, error) {
	if len(command) == 0 {
		return 0, fmt.Errorf("жду команду после «--»: exec <имя> -- <команда> [аргументы...]")
	}
	value, err := b.Get(name)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = setEnv(os.Environ(), name, value)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		// Имя команды называем, значение секрета не называем никогда: оно
		// сидит в env подпроцесса, а не в сообщении утилиты.
		return 0, fmt.Errorf("не запустил %q: %w", command[0], err)
	}
	return 0, nil
}

// setEnv собирает окружение подпроцесса, заменяя существующую переменную с
// тем же именем, вместо того чтобы тащить обе. Голый append сработал бы (go
// exec берёт последнюю), но явная замена честнее: читателю не нужно держать в
// голове правило «последняя побеждает», а след от подмены остаётся один.
func setEnv(env []string, name, value string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	out = append(out, prefix+value)
	return out
}

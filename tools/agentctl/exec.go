package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// agentctl exec это запуск чужой команды на выбранной подписке: пары окружения
// харнеса из машинного слоя плюс DEVKIT_HARNESS, дальше команда как есть.
// Вердикта, ярусов и ролей команда не считает, поэтому годится и там, где
// вердикта нет вовсе: сессия конвейера дашборда, груминг черновика, разовый
// заход в чужой инструмент руками. Дизайн в
// docs/lld/DK-328-dashboard-screens.md, решение 3.

// cmdExec поднимает команду в окружении названного харнеса. Возвращает код
// выхода команды; ошибка это отказ самого exec, её печатает main единицей.
func cmdExec(start, name string, argv []string, out, errw io.Writer) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("жду имя харнеса: exec --harness <имя> -- <команда>")
	}
	if len(argv) == 0 {
		return 0, fmt.Errorf("жду команду после --: exec --harness %s -- <команда>", name)
	}
	dir, err := harnessDir(start)
	if err != nil {
		return 0, err
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return 0, err
	}
	// Профиль обязателен: имя без профиля это опечатка, а опечатка тут
	// оборачивается сессией, которая идёт на подписке по умолчанию под чужим
	// именем.
	if _, err := loadProfile(dir, name); err != nil {
		return 0, err
	}
	pairs := harnessPairs(l, name)
	// Одного имени мало, и это главное в команде. Без каталога конфигурации
	// второй подписки сессия поднимется на подписке по умолчанию, назовёт себя
	// чужим именем, и заметить это можно будет только по счёту.
	if len(pairs) == 0 && name != l.Default {
		return 0, fmt.Errorf("харнес %s не поднять: он не default машинного слоя (%s), а пар окружения у него нет, "+
			"и без своего каталога конфигурации команда пошла бы на подписке по умолчанию под чужим именем; "+
			"вписать env в секцию [%s] файла %s", name, l.Default, name, machineConfigPath())
	}
	if !inList(l.Enabled, name) {
		// Невключённому харнесу devkit не раскладывает правила, хуки и скиллы.
		// Отказом это делать нельзя, разовый заход законен, но и молчать нечестно.
		fmt.Fprintf(errw, "заметка: %s не в списке включённых (%s), правила, хуки и скиллы devkit ему не раскладываются\n", name, l.Source)
	}
	// Строка про запуск обязательна: без неё подмена подписки ничем не видна, а
	// вывод самой команды о ней не говорит. Имена переменных печатаются, значения
	// нет: в env кладут и токены, а вывод уезжает в логи и в панель сессии.
	tail := ""
	if names := envNames(pairs); len(names) > 0 {
		tail = ", окружение: " + strings.Join(names, ", ")
	}
	fmt.Fprintf(errw, "exec: харнес %s, команда %s%s\n", name, argv[0], tail)
	cmd := exec.Command(argv[0], argv[1:]...)
	// Ограничитель вложенности exec не ставит: команда тут это не делегированный
	// исполнитель, а сессия-диспетчер, и делегировать дальше ей и положено.
	cmd.Env = harnessEnviron(os.Environ(), pairs, name)
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = out, errw
	return runChild(cmd, errw, "команду выбирает тот, кто зовёт exec")
}

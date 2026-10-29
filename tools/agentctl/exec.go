package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/dronrider/devkit/internal/clientdir"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/stage"
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
	// Одного имени мало, и это главное в команде. Без пар окружения команда
	// пошла бы мимо обвязки подписки: каталог конфигурации, endpoint и токен
	// остались бы унаследованными от родителя, и подмена подписки была бы
	// переименованием. Чужой подписке без пар это ещё и уход на квоту
	// дефолта под чужим именем. Дефолт без пар молча пропускал обвязку, и чат
	// поднимался на профиле первой подписки с истёкшим входом (DK-1271).
	// Пустой машинный слой внятно отказывает с подсказкой доктора.
	if len(pairs) == 0 {
		if name != l.Default {
			return 0, fmt.Errorf("харнес %s не поднять: он не default машинного слоя (%s), а пар окружения у него нет, "+
				"и без своего каталога конфигурации команда пошла бы на подписке по умолчанию под чужим именем; "+
				"вписать env в секцию [%s] файла %s", name, l.Default, name, machineConfigPath())
		}
		return 0, fmt.Errorf("харнес %s не поднять: пар окружения у него нет, а без них команда пошла бы мимо обвязки подписки; "+
			"разложить слой: devkitctl doctor --fix", name)
	}
	// Сессия подписки без хуков devkit идёт мимо журнала и сторожей молча, а
	// заметить это потом можно только по пустому списку чатов. Отказ стоит ровно
	// на подъёме: чинится он одной командой, а разовый заход без обвязки
	// законен только у невключённого харнеса (его хуков доктор не кладёт).
	if gap := hooksGap(l, name); gap != "" {
		return 0, fmt.Errorf("%s", gap)
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
	workdir, err := execRunDir()
	if err != nil {
		return 0, err
	}
	var cloud []*draftmeta.CloudLock
	if id := os.Getenv("DEVKIT_TASK"); id != "" {
		main := stage.MainRoot(workdir)
		cloud, err = cloudForRoots(id, workdir, main)
		if err != nil {
			return 0, err
		}
		defer closeCloud(cloud)
		if err = draftmeta.GuardRecord(workdir, id); err != nil {
			return 0, err
		}
		if main != workdir {
			if err = draftmeta.GuardRecord(main, id); err != nil {
				return 0, err
			}
		}
	}
	if ack := os.Getenv("DEVKIT_CLOUD_ACK"); ack != "" {
		if len(cloud) == 0 {
			return 0, fmt.Errorf("нет резерва для подтверждения запуска")
		}
		if err := os.WriteFile(ack, []byte("ready"), 0600); err != nil {
			return 0, fmt.Errorf("подтверждение резерва: %w", err)
		}
	}
	fmt.Fprintf(errw, "exec: харнес %s, команда %s, каталог %s%s\n", name, argv[0], workdir, tail)
	cmd := exec.Command(argv[0], argv[1:]...)
	if cloud != nil {
		cmd.ExtraFiles = cloudFiles(cloud)
	}
	cmd.Dir = workdir
	// Ограничитель вложенности exec не ставит: команда тут это не делегированный
	// исполнитель, а сессия-диспетчер, и делегировать дальше ей и положено.
	cmd.Env = harnessEnviron(os.Environ(), pairs, name)
	if os.Getenv("DEVKIT_CLOUD_ACK") != "" {
		filtered := cmd.Env[:0]
		for _, pair := range cmd.Env {
			if !strings.HasPrefix(pair, "DEVKIT_CLOUD_ACK=") {
				filtered = append(filtered, pair)
			}
		}
		cmd.Env = filtered
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout, cmd.Stderr = out, errw
	return runChild(cmd, errw, "команду выбирает тот, кто зовёт exec")
}

// execRunDir выбирает рабочий каталог поднимаемой команды: текущий каталог
// поднявшего, если работать в нём уместно, иначе служебный пустой каталог под
// домом. Наследуемый каталог раньше уходил команде молча, а у launchd-обёртки
// (dashboard serve, сменные съёмщики) им оказывался корень файловой системы:
// поднятое дерево обходило весь диск и поднимало диалоги доступа macOS на имя
// дашборда (DK-1373). Дом и корень меняются на служебный каталог тем же
// правилом, что и у входа клиента.
func execRunDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("текущий каталог не найден (%v), команду поднимать негде", err)
	}
	home, _ := os.UserHomeDir()
	if clientdir.Check(cwd, home) == nil {
		return cwd, nil
	}
	return clientdir.Service(home)
}

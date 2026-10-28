package main

import (
	"fmt"
	"regexp"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// Назначение участника команды на строку (DK-1468). Суффикс «[исп: имя]»
// несёт handle участника из ~/.devkit/team.toml, назначение переживает close
// и попадает в архивную строку: по нему профиль участника считает авторство,
// а вердикт agentctl pick сужается до яруса назначенного агентского
// участника. Человек в назначении не останавливает агентскую работу по
// строке: вердикт подсобных подъёмов считается как раньше, а ведёт строку
// назначенный.

// assignOf читает handle назначенного участника из заголовка строки, пустая
// строка значит «назначения нет». Разбор без привязки к концу: назначение
// стоит между планом и сроком, и срок в хвосте не должен его прятать.
func assignOf(title string) string {
	return team.TitleHandle(title)
}

// assignSuffix собирает суффикс заголовка по handle, пустой handle суффикса
// не носит.
func assignSuffix(handle string) string {
	if handle == "" {
		return ""
	}
	return " [исп: " + handle + "]"
}

// handleRe отсекает заведомо чужие значения ещё до разговора с командой:
// суффикс заголовка несёт то же множество символов, что и имя секции
// участника, и всё вне его в доску не попадает.
var handleRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// parseAssign проверяет значение флага --assign: «-» снимает назначение,
// имя обязано называть участника команды с ролью разработки. Команда машины
// это источник правды о составе, назначение мимо неё оставило бы суффикс,
// на который pick отвечал бы предупреждением на каждой строке.
func parseAssign(v string) (string, error) {
	if v == "-" {
		return "", nil
	}
	if !handleRe.MatchString(v) {
		return "", fmt.Errorf("--assign %q не читается, жду имя участника команды (латиница, цифры, дефис) или «-» для снятия", v)
	}
	list, err := team.Load(stage.Home())
	if err != nil {
		return "", err
	}
	p, ok := team.Find(list, v)
	if !ok {
		return "", fmt.Errorf("участника %s нет в команде, состав показывает taskctl team list, заводит taskctl team add", v)
	}
	if !p.HasRole(team.RoleExec) {
		return "", fmt.Errorf("у участника %s нет роли разработки, назначение на строку требует её", v)
	}
	return v, nil
}

// noAssign печатает прежнего назначенного в ответе правки, у строки без
// назначения прочерк.
func noAssign(h string) string {
	if h == "" {
		return "-"
	}
	return h
}

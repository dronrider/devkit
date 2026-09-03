// Package deployconf читает проектную обвязку выката .devkit/deploy.local.
//
// Файл гитигнорнут: в команде выката обычно адрес или роль машины, а её место в
// локальном, а не коммитимом (RULES.board.md, «Трекинг задач» п. 8). Читателей
// у обвязки двое. shipctl берёт отсюда команду выката, команду тестов и флаг
// автономии. Дашборд смотрит тот же флаг, решая, поднимать ли прогон сценария
// после выката (DK-718): проект с выкатом за пользователем проверяющего не
// поднимает, там человек в окне и до Check дело доходит только с рук. Разбор
// формата стоит одним местом, потому что вторая копия разошлась бы с первой на
// первой же правке ключей.
package deployconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dronrider/devkit/internal/kvconf"
)

// Rel это путь обвязки внутри корня проекта.
const Rel = ".devkit/deploy.local"

// Config это прочитанная обвязка. Autonomous говорит, доверен ли агенту весь
// конвейер: катить на прод сам, без отдельного слова пользователя.
type Config struct {
	Deploy     string
	Test       string
	Autonomous bool
	Timeout    time.Duration
}

// DefaultTimeout это предел времени на шаг выката без ключа deploy_timeout.
// Запас взят к самому долгому штатному выкату, какой встречался: кросс-сборка
// релизных бинарей с нуля идёт единицы минут, получасовой предел её не режет,
// зато вставшая команда (сборка ждёт неподнятый демон Docker) кончается
// провалом, а не вечным молчанием (DK-154).
const DefaultTimeout = 30 * time.Minute

// Load читает обвязку корня, если она есть. Формат плоский, «ключ = значение»
// с решёткой под комментарий, и разбирает его kvconf вместе с review.conf.
// Отсутствие файла не ошибка. Выкат тогда остаётся за пользователем, как и до
// появления конфига.
func Load(root string) (Config, error) {
	c := Config{Timeout: DefaultTimeout}
	pairs, err := kvconf.Read(filepath.Join(root, filepath.FromSlash(Rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	for _, p := range pairs {
		switch p.Key {
		case "deploy":
			c.Deploy = p.Value
		case "test":
			c.Test = p.Value
		case "autonomous":
			c.Autonomous, _ = strconv.ParseBool(p.Value)
		case "deploy_timeout":
			// Молча вернуться к умолчанию нельзя: опечатка в пределе оставила бы
			// выкат с чужим временем ожидания, а заметить это можно только на
			// вставшей команде.
			d, err := time.ParseDuration(p.Value)
			if err != nil || d <= 0 {
				return c, fmt.Errorf("%s: deploy_timeout = %q не читается как предел времени, ждал длительность вида 90s, 30m, 2h", Rel, p.Value)
			}
			c.Timeout = d
		}
	}
	return c, nil
}

// Autonomous отвечает на единственный вопрос дашборда: доверен ли конвейер
// проекта агенту целиком. Битая обвязка считается неавтономной. Поднимать
// сессию по нечитаемому конфигу нельзя, а сказать о нём есть кому: тот же файл
// читает shipctl, и его ошибку человек видит на первом же merge.
func Autonomous(root string) bool {
	c, err := Load(root)
	return err == nil && c.Autonomous
}

// Unquote снимает одну окружающую пару кавычек у значения. Разбор живёт в
// kvconf, здесь остаётся имя, по которому его зовут читатели обвязки.
func Unquote(s string) string { return kvconf.Unquote(s) }

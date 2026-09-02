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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// Load читает обвязку корня, если она есть. Формат простой: строки вида
// key = value, # это комментарий, пустые строки пропускаются. Отсутствие файла
// не ошибка. Выкат тогда остаётся за пользователем, как и до появления конфига.
func Load(root string) (Config, error) {
	c := Config{Timeout: DefaultTimeout}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(Rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), Unquote(strings.TrimSpace(val))
		switch key {
		case "deploy":
			c.Deploy = val
		case "test":
			c.Test = val
		case "autonomous":
			c.Autonomous, _ = strconv.ParseBool(val)
		case "deploy_timeout":
			// Молча вернуться к умолчанию нельзя: опечатка в пределе оставила бы
			// выкат с чужим временем ожидания, а заметить это можно только на
			// вставшей команде.
			d, err := time.ParseDuration(val)
			if err != nil || d <= 0 {
				return c, fmt.Errorf("%s: deploy_timeout = %q не читается как предел времени, ждал длительность вида 90s, 30m, 2h", Rel, val)
			}
			c.Timeout = d
		}
	}
	return c, sc.Err()
}

// Autonomous отвечает на единственный вопрос дашборда: доверен ли конвейер
// проекта агенту целиком. Битая обвязка считается неавтономной. Поднимать
// сессию по нечитаемому конфигу нельзя, а сказать о нём есть кому: тот же файл
// читает shipctl, и его ошибку человек видит на первом же merge.
func Autonomous(root string) bool {
	c, err := Load(root)
	return err == nil && c.Autonomous
}

// Unquote снимает одну окружающую пару кавычек, если значение целиком в них
// завёрнуто. Кавычки внутри команды (ssh host 'systemctl restart foo') не
// трогаются: снимается ровно внешняя пара, не все подряд.
func Unquote(s string) string {
	if len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}
	return s
}

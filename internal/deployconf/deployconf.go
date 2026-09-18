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
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/kvconf"
)

// Rel это путь обвязки внутри корня проекта.
const Rel = ".devkit/deploy.local"

// Component это одна боевая единица выката монорепозитория: команда и пути,
// которые её задевают. Пара ключей на компонент, deploy.<имя> (команда) и
// deploy.<имя>.paths (пути через запятую), а не отдельная секция: формат
// конфига плоский и вторую разметку под секции заводить незачем (решение
// «раскладка», DK-894).
type Component struct {
	Name    string
	Command string
	Paths   []string
}

// Config это прочитанная обвязка. Autonomous говорит, доверен ли агенту весь
// конвейер: катить на прод сам, без отдельного слова пользователя. Components
// это раскладка монорепозитория, пустая, когда проект катится одной командой
// Deploy. Порядок среза это порядок ключей deploy.<имя> в файле, и в этом же
// порядке merge катит задетые компоненты при autonomous=true.
type Config struct {
	Deploy     string
	Test       string
	Autonomous bool
	Timeout    time.Duration
	Components []Component
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
	compIdx := map[string]int{}
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
		default:
			name, isPaths := componentKey(p.Key)
			if name == "" {
				// Прочая проза и опечатки в ключах уходят молча, как у одиночных
				// ключей выше: разбор не судья чужим строкам без знака deploy.
				continue
			}
			i, seen := compIdx[name]
			if !seen {
				i = len(c.Components)
				compIdx[name] = i
				c.Components = append(c.Components, Component{Name: name})
			}
			if isPaths {
				c.Components[i].Paths = splitPaths(p.Value)
			} else {
				c.Components[i].Command = p.Value
			}
		}
	}
	// Компонент с одной из двух строк недоделан: без пути его никогда не
	// заденет дифф, без команды нечем катить задетый. Тихо пропустить такую
	// половину значило бы завести компонент, который либо никогда не
	// сработает, либо откажет только на боевом диффе.
	for _, comp := range c.Components {
		if comp.Command == "" || len(comp.Paths) == 0 {
			return c, fmt.Errorf("%s: компонент %q неполный, нужны обе строки: deploy.%s (команда) и deploy.%s.paths (пути через запятую)",
				Rel, comp.Name, comp.Name, comp.Name)
		}
	}
	return c, nil
}

// componentKey разбирает ключ компонента. deploy.<имя> это её команда,
// deploy.<имя>.paths это пути, которые её задевают. Прочий ключ вида
// deploy.* (опечатка, будущий суффикс) не наш, вызывающий его пропускает.
func componentKey(key string) (name string, isPaths bool) {
	rest, ok := strings.CutPrefix(key, "deploy.")
	if !ok || rest == "" {
		return "", false
	}
	if before, after, cut := strings.Cut(rest, "."); cut {
		if after == "paths" && before != "" {
			return before, true
		}
		return "", false
	}
	return rest, false
}

// splitPaths разбирает список путей компонента через запятую: web/,
// shared/. Пробелы вокруг каждого пути обрезаются, пустые элементы (хвостовая
// или двойная запятая) пропускаются.
func splitPaths(s string) []string {
	var paths []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			paths = append(paths, part)
		}
	}
	return paths
}

// Match сопоставляет пути диффа компонентам раскладки. matched идёт в
// порядке конфига и называет задетый компонент ровно один раз (решение
// «двое», DK-894: катить оба по очереди в порядке из конфига). Пустые
// Components значат «раскладки нет», Match тогда не судья, и вызывающий
// остаётся при одиночной команде Deploy. Путь, не попавший ни в один
// компонент, останавливает разбор и возвращается как miss: частичный matched
// тут не нужен, отказ называет ровно один путь и подсказку, какой ключ
// завести.
func (c Config) Match(paths []string) (matched []Component, miss string) {
	if len(c.Components) == 0 {
		return nil, ""
	}
	touched := map[string]bool{}
	for _, path := range paths {
		name, ok := c.componentFor(path)
		if !ok {
			return nil, path
		}
		touched[name] = true
	}
	for _, comp := range c.Components {
		if touched[comp.Name] {
			matched = append(matched, comp)
		}
	}
	return matched, ""
}

func (c Config) componentFor(path string) (string, bool) {
	for _, comp := range c.Components {
		for _, prefix := range comp.Paths {
			if pathUnder(path, prefix) {
				return comp.Name, true
			}
		}
	}
	return "", false
}

// pathUnder проверяет, лежит ли path под prefix: как файл целиком, либо
// внутри каталога, который тот называет. Хвостовой слеш не обязателен,
// «web» и «web/» в списке путей компонента это одно и то же.
func pathUnder(path, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return false
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
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

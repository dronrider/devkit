// Package sessions читает реестр чатов задачи: машинный журнал
// ~/.devkit/sessions.log говорит, какую задачу ведёт сессия и где лежит её
// транскрипт. Строки дописывают SessionStart-хук hooks/session-task.py и ручка
// привязки рукой, а читателей у журнала трое: дашборд рисует привязку на
// экране, taskctl ask берёт отсюда сессию, когда её не назвали окружением, а
// сторожок меряет по транскрипту живость перед страховочной парковкой. Разбор
// целиком в docs/lld/DK-430-task-chat.md, решение 1.
package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rel это путь реестра от дома. Журнал машинный, а не проектный: сессии на
// машине общие, и доска у них не одна.
const Rel = "sessions.log"

// Stamp это формат времени в строке реестра, тот же, что у уведомителя.
const Stamp = "2006-01-02T15:04:05"

// Bind это свёрнутая запись реестра про одну сессию: чью задачу она ведёт, чем
// привязана, где идёт и где лежит её транскрипт. Tmux это имя tmux-сессии от
// того, кто её поднял: вывести chat-<ID>-<n> из записи нечем, а живость этого
// имени и есть мера кончившегося разговора.
type Bind struct {
	Task       string
	Source     string
	Project    string
	Tree       string
	Transcript string
	Tmux       string
	Time       string
	// Parent это разговор, раздавший работу этой сессии. Пусто у сессии,
	// которую подняли сами: из терминала, кнопкой дашборда, руками. Непустой
	// родитель значит, что сессия это чужая работа, а не разговор человека, и
	// показывать её надо ходом в ленте родителя, а не строкой списка (DK-581).
	Parent string
}

// keys это ключевые слова полей строки реестра в порядке записи
// (hooks/session-task.py, record).
var keys = map[string]bool{
	"сессия": true, "задача": true, "проект": true, "дерево": true,
	"транскрипт": true, "источник": true, "повод": true, "tmux": true,
	"родитель": true,
}

// dashless читает пустое поле, записанное дефисом.
func dashless(v string) string {
	if v = strings.TrimSpace(v); v == "-" {
		return ""
	}
	return v
}

// ParseLine разбирает строку реестра. Непонятая строка пропускается без
// обрушения разбора, как битая строка ленты уведомлений: журнал общий, и чужая
// строка в нём дороже стоила бы, чем пустая привязка. Значение поля собирается
// до следующего ключевого слова, поэтому пробел в пути дерева строку не
// рассыпает.
func ParseLine(line string) (string, Bind, bool) {
	var b Bind
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 3 {
		return "", b, false
	}
	if _, err := time.Parse(Stamp, f[0]); err != nil {
		return "", b, false
	}
	if f[1] != "сессия" {
		return "", b, false
	}
	vals := map[string]string{}
	key := ""
	for _, tok := range f[1:] {
		if keys[tok] && vals[key] != "" {
			key = tok
			continue
		}
		if key == "" {
			key = tok
			continue
		}
		vals[key] = strings.TrimSpace(vals[key] + " " + tok)
	}
	sid := dashless(vals["сессия"])
	if sid == "" {
		return "", b, false
	}
	b = Bind{
		Task:       strings.ToUpper(dashless(vals["задача"])),
		Source:     dashless(vals["источник"]),
		Project:    dashless(vals["проект"]),
		Tree:       dashless(vals["дерево"]),
		Transcript: dashless(vals["транскрипт"]),
		Tmux:       dashless(vals["tmux"]),
		Parent:     dashless(vals["родитель"]),
		Time:       f[0],
	}
	return sid, b, true
}

// Binds это свёрнутый реестр: сессия и её последняя запись. Выигрывает
// последняя строка, поэтому перепривязка и отвязка это обычные записи, а не
// правка файла.
type Binds map[string]Bind

// Parse сворачивает журнал целиком.
func Parse(data []byte) Binds {
	binds := Binds{}
	for _, ln := range strings.Split(string(data), "\n") {
		if sid, b, ok := ParseLine(ln); ok {
			binds[sid] = b
		}
	}
	return binds
}

// Path называет реестр в доме home.
func Path(home string) string { return filepath.Join(home, ".devkit", Rel) }

// Load читает реестр дома. Нечитаемый журнал это пустая привязка, а не отказ:
// реестра может не быть вовсе на машине, где хук старта ещё не подключён.
func Load(home string) Binds {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		return Binds{}
	}
	return Parse(data)
}

// Leads называет сессию, которая ведёт задачу id, и её запись. Выигрывает
// свежайшая: у задачи бывает несколько заходов подряд, и говорить надо с
// последним. Пустое имя значит, что реестр про задачу ничего не знает.
func (b Binds) Leads(id string) (string, Bind) {
	id = strings.ToUpper(strings.TrimSpace(id))
	best, rec := "", Bind{}
	for sid, r := range b {
		if r.Task != id {
			continue
		}
		if best == "" || r.Time > rec.Time {
			best, rec = sid, r
		}
	}
	return best, rec
}

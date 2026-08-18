package main

import (
	"fmt"
	"sort"
	"strings"
)

// fieldList это повторяемый флаг --field: каждое значение вида имя=значение.
type fieldList []string

func (f *fieldList) String() string { return strings.Join(*f, ", ") }

func (f *fieldList) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// editAxes это названные оси правки. Пустая ось не трогается: команда без
// ключей не делает к трекеру ни одного запроса.
type editAxes struct {
	estimate string
	assignee string
	status   string
	title    string
	kind     string
	fields   fieldList
	comment  string
}

// cmdEdit правит тикет по названным осям: оценка, исполнитель, статус,
// заголовок, тип, произвольные поля и комментарий. Обрядов тут нет: это
// замена ручных кликов в вебе, поэтому оценку правит и в конечный статус
// переводит, чего обряды take и submit себе не позволяют. Ось уезжает только
// тогда, когда её назвали, и в фиксированном порядке: исполнитель и оценка,
// поля, переход, комментарий. Отказ трекера останавливает работу, что после
// него осталось, видно по строкам вывода.
func cmdEdit(root, arg string, ax editAxes) (string, error) {
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	key := ticketKey(tr.bind, arg)

	updates := map[string]string{}
	if ax.title != "" {
		updates["summary"] = ax.title
	}
	if ax.kind != "" {
		updates["issuetype"] = ax.kind
	}
	for _, raw := range ax.fields {
		name, value, ok := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return "", fmt.Errorf("--field %q: жду имя=значение, значение может быть пустым, имя нет", raw)
		}
		// Коллизия это потерянная ось: --field summary затёр бы --title, а
		// повтор --field молча выбрал бы последнее значение. Отказ до
		// первого запроса честнее, чем тихо сделать не то, что назвали.
		if name == "summary" && ax.title != "" {
			return "", fmt.Errorf("ось \"summary\" названа дважды: --title и --field summary; оставьте одну")
		}
		if name == "issuetype" && ax.kind != "" {
			return "", fmt.Errorf("ось \"issuetype\" названа дважды: --type и --field issuetype; оставьте одну")
		}
		if _, seen := updates[name]; seen {
			return "", fmt.Errorf("--field %q назван дважды; оставьте одно значение", name)
		}
		updates[name] = value
	}
	if ax.estimate == "" && ax.assignee == "" && ax.status == "" && ax.comment == "" && len(updates) == 0 {
		return "", fmt.Errorf("не названо ни одной оси: жду хоть один из --estimate, --assignee, --status, --title, --type, --field, --comment; без них тикет не трогаю")
	}

	// Наличие осей проверяется до первого запроса: отказ адаптера не должен
	// оставлять полусобранную правку, у которой половина осей уже уехала.
	upd, canUpdate := tr.adapter.(updater)
	if len(updates) > 0 && !canUpdate {
		return "", fmt.Errorf("адаптер %s операции update не умеет: заголовок, тип и произвольные поля не правятся, оси %s не трогаю", tr.contour.Adapter, quoteKeys(updates))
	}
	com, canComment := tr.adapter.(commenter)
	if ax.comment != "" && !canComment {
		return "", fmt.Errorf("адаптер %s операции comment не умеет: комментария не будет", tr.contour.Adapter)
	}

	var lines []string
	if ax.assignee != "" {
		if err := tr.adapter.assign(key, ax.assignee); err != nil {
			return partial(lines, err)
		}
		lines = append(lines, fmt.Sprintf("исполнитель: %s", ax.assignee))
	}
	if ax.estimate != "" {
		if err := tr.adapter.estimate(key, ax.estimate); err != nil {
			return partial(lines, err)
		}
		lines = append(lines, fmt.Sprintf("оценка: %s", ax.estimate))
	}
	if len(updates) > 0 {
		if err := upd.update(key, updates); err != nil {
			return partial(lines, err)
		}
		lines = append(lines, fmt.Sprintf("поля: %s", strings.Join(sortedPairs(updates), ", ")))
	}
	if ax.status != "" {
		// Статус называется именем трекера, а не секцией доски. Если статус
		// расписан в [status], с ним уезжают поля секции [fields_*] тем же
		// порядком, что у обрядов, иначе переход идёт без полей и возможный
		// отказ трекера назовёт недостающее.
		sect, known := tr.contour.sectionOf(ax.status)
		var fields map[string]string
		if known {
			fields = tr.contour.fieldsFor(sect)
		}
		if err := tr.adapter.transition(key, ax.status, fields); err != nil {
			return partial(lines, err)
		}
		switch {
		case known && sect == sectDone:
			lines = append(lines, fmt.Sprintf("статус: «%s» (конечный)", ax.status))
		case known:
			lines = append(lines, fmt.Sprintf("статус: «%s» (секция доски: %s)", ax.status, boardNames[sect]))
		default:
			lines = append(lines, fmt.Sprintf("статус: «%s» (в таблице [status] контура его нет, ушёл без полей секции)", ax.status))
		}
	}
	if ax.comment != "" {
		if err := com.comment(key, ax.comment); err != nil {
			return partial(lines, err)
		}
		lines = append(lines, "комментарий: написан")
	}
	return strings.Join(lines, "\n"), nil
}

// partial отдаёт накопленные строки вместе с отказом: что до отказа успело
// уехать, видно по выводу команды, а не додумывается.
func partial(lines []string, err error) (string, error) {
	return strings.Join(lines, "\n"), err
}

func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k+"="+m[k])
	}
	sort.Strings(out)
	return out
}

// quoteKeys перечисляет поля в отказе: какие оси не уехали из-за адаптера.
func quoteKeys(m map[string]string) string {
	quoted := make([]string, 0, len(m))
	for _, pair := range sortedPairs(m) {
		quoted = append(quoted, fmt.Sprintf("%q", pair))
	}
	return strings.Join(quoted, ", ")
}

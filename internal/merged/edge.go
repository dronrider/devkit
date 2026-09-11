package merged

import (
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/accept"
)

// Слова состояния ребра. Так их отдаёт `taskctl dep list --json`, и так они
// стоят в отказах ворот.
const (
	StateClosed = "закрыта"
	StateMerged = "слита"
	StateWaits  = "ждёт"
)

// Разряды парковки зависимой (DK-932), которыми она ждёт снятия ребра.
const (
	ParkMerge = "слияние"
	ParkClose = "закрытие"
)

// Prereq это предпосылка глазами доски. Title это заголовок её строки целиком,
// с суффиксами. Пустой Title значит, что строки на доске нет.
type Prereq struct {
	ID       string
	Title    string
	Archived bool
}

// Edge это ответ снятости ребра «после Dep». Why говорит словами, чем ребро
// снято или чего ждёт. Park называет разряд парковки, на котором зависимой
// есть чего ждать, и пуст, когда ждать нечего или парковка не поможет.
type Edge struct {
	Dep, State, Why, Park string
}

// Lifted отвечает, снято ли ребро.
func (e Edge) Lifted() bool { return e.State != StateWaits }

var failRe = regexp.MustCompile(`\[провал: ([^|\[\]]*)\]`)

// Edge считает снятость ребра «после p.ID» по решению 2 LLD DK-933. Ребро
// снято, когда предпосылка в архиве. Иначе оно снято, когда её работа слита в
// main и не откачена, на строке нет пометки провала и вид приёмки не user. Вид
// user держит ребро до закрытия: зависимая, начатая до согласия человека,
// строится на ответе, которого ещё нет. Предпосылку, пропавшую с доски мимо
// архива, функция считает неснятой, иначе опечатка в ID пускала бы строку
// молча. Признак, который не спросить (нет git или main), ребро тоже держит, и
// снимает его тогда одно закрытие, как до решения 2.
func (b *Book) Edge(p Prereq) Edge {
	if e, ok := b.edges[p]; ok {
		return e
	}
	e := b.edge(p)
	if b.edges == nil {
		b.edges = map[Prereq]Edge{}
	}
	b.edges[p] = e
	return e
}

func (b *Book) edge(p Prereq) Edge {
	e := Edge{Dep: p.ID, State: StateWaits}
	switch {
	case p.Archived:
		e.State, e.Why = StateClosed, p.ID+" закрыта"
		return e
	case p.Title == "":
		e.Why = p.ID + " нет ни на доске, ни в архиве"
		return e
	case accept.KindOf(p.Title) == "user":
		e.Why, e.Park = p.ID+" с приёмкой user держит ребро до закрытия", ParkClose
		return e
	}
	if m := failRe.FindStringSubmatch(p.Title); m != nil {
		e.Why, e.Park = p.ID+" провалена: "+strings.TrimSpace(m[1]), ParkMerge
		return e
	}
	v, err := b.Task(p.ID)
	if err != nil {
		e.Why = "признак «слита» у " + p.ID + " не спрошен: " + err.Error()
		return e
	}
	e.Why = v.Said(p.ID)
	if v.Merged {
		e.State = StateMerged
		return e
	}
	e.Park = ParkMerge
	return e
}

var depsRe = regexp.MustCompile(`\[после ([^\]|]+)\]`)

// Deps вынимает ID из маркера «[после ...]» заголовка строки. Разбор нарочно
// терпимый: это чтение для утилит, которые строку не пишут, а порядок
// суффиксов держит taskctl lint.
func Deps(title string) []string {
	m := depsRe.FindStringSubmatch(title)
	if m == nil {
		return nil
	}
	var out []string
	for _, id := range strings.Split(m[1], ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

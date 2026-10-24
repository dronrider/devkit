// Package mainchat читает журнал назначений основного чата записи:
// машинный журнал ~/.devkit/main-chat.log говорит, какой разговор назначен
// записи основным (LLD DK-1422, решение 1). Формат строк тот же, что у
// реестра сессий: именованные поля, дописывание в конец, свёртка хвостом,
// и писать его могут только инструменты devkit одной машины. Читают журнал
// ручка адреса записи в дашборде и её панель, а пишут старт работы,
// оформление черновика, передача и миграция, каждая своим словом источника.
package mainchat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
)

// Rel это путь журнала от дома. Журнал машинный, как реестр сессий:
// назначение переживает дерево и снос слиянием, а доска это гит.
const Rel = "main-chat.log"

// Stamp это формат времени в строке журнала, общий с реестром сессий.
const Stamp = sessions.Stamp

// Слова источника строки журнала: кем поставлено назначение. Набор закрыт
// решением 1 LLD DK-1422, и слово со стороны читается записью без разбора:
// журнал пишут несколько инструментов, и новее этого пакета писатель не
// повод забывать само назначение.
const (
	ByDecor   = "оформление" // свободный разговор стал основным черновика
	ByWork    = "работа"     // старт работы назвал носителем конвейер либо виток цели
	ByMigrate = "миграция"   // подтверждённый разбор прежних разговоров
	ByHand    = "рука"       // выбор с экрана «Сделать основным»
	ByMove    = "передача"   // управление перешло новому носителю
	ByOff     = "снято"      // назначение снято, разговора у записи нет
)

// Bind это одна строка журнала и одновременно свёрнутое назначение записи.
// Record это ID записи, Chat ключ носителя: sid разговора либо tmux-<имя
// окна> у конвейера. Пустой Chat значит снятие, Prior несёт прежнего
// носителя у передачи, Time это метка строки.
type Bind struct {
	Record  string
	Chat    string
	Project string
	Source  string
	Prior   string
	Time    string
}

// keys это ключевые слова полей строки в порядке записи.
var keys = map[string]bool{
	"назначение": true, "разговор": true, "проект": true,
	"источник": true, "прежде": true,
}

// dashless читает пустое поле, записанное дефисом.
func dashless(v string) string {
	if v = strings.TrimSpace(v); v == "-" {
		return ""
	}
	return v
}

// ParseLine разбирает строку журнала. Непонятая строка пропускается без
// обрушения разбора: журнал общий, и чужая строка в нём дороже стоила бы,
// чем пустое назначение, как у реестра сессий.
func ParseLine(line string) (Bind, bool) {
	var b Bind
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 3 {
		return b, false
	}
	if _, err := time.Parse(Stamp, f[0]); err != nil {
		return b, false
	}
	if f[1] != "назначение" {
		return b, false
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
	rec := strings.ToUpper(dashless(vals["назначение"]))
	if rec == "" {
		return b, false
	}
	b = Bind{
		Record:  rec,
		Chat:    dashless(vals["разговор"]),
		Project: dashless(vals["проект"]),
		Source:  dashless(vals["источник"]),
		Prior:   dashless(vals["прежде"]),
		Time:    f[0],
	}
	return b, true
}

// Binds это свёрнутый журнал: запись и её действующее назначение. Выигрывает
// строка, свежая по времени, поэтому перепривязка и снятие это обычные
// записи, а не правка файла. Снятие читается пустым Chat, а не пропавшей записью: у записи,
// чьё назначение снимали, состояние известно и отличается от никогда не
// назначавшейся.
type Binds map[string]Bind

// Parse сворачивает журнал целиком. Строки обоих домов склеиваются в один
// поток, и свёртка берёт запись, свежую по времени, а не позднюю по порядку:
// порядок склейки журналов не решает, у реестра сессий тот же порядок
// (sessions.Last). Равная метка отдаёт строку, позднюю по порядку.
func Parse(data []byte) Binds {
	out := Binds{}
	for _, ln := range strings.Split(string(data), "\n") {
		b, ok := ParseLine(ln)
		if !ok {
			continue
		}
		if cur, seen := out[b.Record]; !seen || b.Time >= cur.Time {
			out[b.Record] = b
		}
	}
	return out
}

// All собирает строки журнала по записям, сохраняя порядок: история строк
// одной записи это её отрезки, и свёртка их не заменяет.
func All(data []byte) map[string][]Bind {
	out := map[string][]Bind{}
	for _, ln := range strings.Split(string(data), "\n") {
		b, ok := ParseLine(ln)
		if !ok {
			continue
		}
		out[b.Record] = append(out[b.Record], b)
	}
	return out
}

// Path называет журнал в доме home.
func Path(home string) string { return filepath.Join(home, ".devkit", Rel) }

// Load читает журнал дома. Нечитаемый журнал это пустое назначение, а не
// отказ: журнала может не быть вовсе на машине, где назначений ещё не было.
func Load(home string) Binds {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		return Binds{}
	}
	return Parse(data)
}

// Line собирает строку журнала: формат один на всех писателей, и разъехавшись,
// они оставили бы читателя с половиной записей. Пустые поля пишутся дефисом,
// как у реестра сессий, прежде пишется и пустым у строк вне передачи: разбор
// держится за ключевые слова, а не за наборы полей разных писателей.
func Line(now time.Time, b Bind) string {
	dash := func(v string) string {
		if v = strings.Join(strings.Fields(v), " "); v == "" {
			return "-"
		}
		return v
	}
	return now.Format(Stamp) + " назначение " + dash(b.Record) +
		" разговор " + dash(b.Chat) + " проект " + dash(b.Project) +
		" источник " + dash(b.Source) + " прежде " + dash(b.Prior) + "\n"
}

// Segment это отрезок основной ленты записи: период, когда назначение держал
// один разговор. Since это метка строки, открывшей отрезок, Until метка
// строки, его закрывшей, а пустой Until значит действующий отрезок. Подписи
// ролей и этапов на стыках отрезков ложатся отдельно (DK-871).
type Segment struct {
	Chat   string `json:"chat"`
	Source string `json:"source"`
	Since  string `json:"since"`
	Until  string `json:"until,omitempty"`
}

// Segments собирает отрезки записи из её строк по порядку журнала. Подряд
// идущие строки одного разговора сходятся в один отрезок: повторный старт
// работы того же конвейера продолжает отрезок, а не плодит новые. Снятие
// отрезка не открывает: пустого разговора в основной ленте не бывает.
func Segments(recs []Bind) []Segment {
	var out []Segment
	for _, b := range recs {
		if n := len(out); n > 0 && out[n-1].Until == "" {
			if b.Chat == out[n-1].Chat {
				continue
			}
			out[n-1].Until = b.Time
		}
		if b.Chat != "" {
			out = append(out, Segment{Chat: b.Chat, Source: b.Source, Since: b.Time})
		}
	}
	return out
}

// HeldError называет причину отказа писать: разговор уже основной у другой
// записи, и вторая запись с ним нарушала бы инвариант «разговор основной не
// больше чем для одной записи». Слова ошибки видны человеку с экрана.
type HeldError struct {
	Chat   string
	Record string
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("разговор %s уже основной у записи %s", e.Chat, e.Record)
}

// ErrBadRecord отказывает записи без ID: назначение без записи некому
// адресовать, и молча проглотить его значило бы поверить вызывающего на слово.
var ErrBadRecord = errors.New("назначение без записи")

// Put дописывает назначение в журнал под замком. Замок один на журнал и того
// же порядка, каким вход разговора держит internal/chat: пишут журнал
// несколько инструментов, и без замка их строки расходились бы с инвариантами.
// Перед дописыванием проверяется, что разговор не основной у другой записи:
// у самой записи действующее назначение одно по свёртке, новая строка его и
// заменяет. Откат это обратные строки: журнал при откате не переписывается,
// новая переписка, заведённая после назначения, не удаляется.
func Put(home string, b Bind) error {
	b.Record = strings.ToUpper(strings.TrimSpace(b.Record))
	if b.Record == "" {
		return ErrBadRecord
	}
	lock, err := chat.Lock(Path(home) + chat.LockSuffix)
	if err != nil {
		return err
	}
	defer lock.Close()
	data, _ := os.ReadFile(Path(home))
	if b.Chat != "" {
		for rec, cur := range Parse(data) {
			if cur.Chat == b.Chat && rec != b.Record {
				return &HeldError{Chat: b.Chat, Record: rec}
			}
		}
	}
	return appendLine(Path(home), Line(time.Now(), b))
}

// appendLine дописывает строку в журнал, обрезав разросшийся файл берегами
// реестра сессий, а порядком обрезки своим. У реестра первыми уходят записи
// по факту работы, потому что утилиты кладут их заново, а здесь строка
// работы это действующее назначение, и уходит она только хвостом.
func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > sessions.Limit {
		if data, err := os.ReadFile(path); err == nil {
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) > sessions.Keep {
				lines = lines[len(lines)-sessions.Keep:]
			}
			os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

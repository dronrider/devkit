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
	byID := map[string][]Bind{}
	order := []string{}
	for _, ln := range strings.Split(string(data), "\n") {
		sid, b, ok := ParseLine(ln)
		if !ok {
			continue
		}
		if _, seen := byID[sid]; !seen {
			order = append(order, sid)
		}
		byID[sid] = append(byID[sid], b)
	}
	binds := Binds{}
	for _, sid := range order {
		// Свёртка тут та же, что у Last: свежая запись выигрывает задачей и
		// поводом, а поля не задачные (дерево, транскрипт, имя tmux-сессии)
		// добираются из прежних. Записи по факту работы кладут утилиты доски,
		// и про tmux им не известно ничего: взяв такую запись целиком, реестр
		// забывал имя живой сессии, и дашборд переставал видеть, чем её
		// снимать (живой случай: сессия chat-DK-397-1 показана «мимо
		// дашборда» после чужого taskctl move).
		binds[sid] = Last(byID[sid])
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

// --- POC разговора (ветка poc-chat) ---
//
// Привязка сессии к задаче идёт по факту работы, и работ у одной сессии много:
// она двигает строку, зовёт ревью, сливает ветку. Поэтому свёртка «последняя
// запись выигрывает» тут не годится вовсе, и рядом лежит вторая: сессия и
// множество задач, которых она касалась.

// BySrc это слово источника у записи по факту работы: строку кладут taskctl,
// shipctl и agentctl, назвав в поводе саму команду.
const BySrc = "работа"

// Слова источника целиком. Первые три говорят о работе: сессию подняли под
// задачу («заказ»), она стартовала в её боковом дереве («дерево»), она двигала
// строку командой доски («работа»). «Рука» это привязка человеком с экрана, и
// работой она не считается: человек говорит, о чём разговор, а не что по
// задаче идёт правка. «Снята» отвязывает.
const (
	ByOrder = "заказ"
	ByTree  = "дерево"
	ByHand  = "рука"
	ByOff   = "снята"
)

// workSrc это слова, по которым сессия считается работой задачи. Их два, и
// «заказ» сюда не входит нарочно: кнопка чата дашборда поднимает разговор о
// задаче тем же словом, каким поднимается конвейер (DEVKIT_TASK в launchEnv),
// и чат, открытый ради вопроса, отбирал бы у строки кнопку запуска. Конвейер и
// цикл цели строку от этого не теряют: их работа видна списком tmux по
// собственному имени сессии, а до первой команды доски они всё равно не
// доживают (границы DK-716, защита DK-460).
var workSrc = map[string]bool{ByTree: true, BySrc: true}

// Works называет задачи, по которым сессия ведёт работу, свежими первыми
// (LLD DK-430, решение 8). Работой её делает слово источника, а не имя
// tmux-сессии: сессия с именем chat-DK-1-2 ведёт задачу ровно так же, как
// task-DK-1, и строка обязана показать обе.
//
// Отвязка читается с двух сторон. Запись «снята» с пустой задачей это отвязка
// всей сессии, дальше в прошлое смотреть нечего: человек сказал, что работой
// это не считается, и возвращать её записями задним числом нельзя. Запись
// «снята» с названной задачей снимает одну эту задачу и накопленного по
// соседним не трогает: работа по строке кончается своим порядком, у закрытия и
// у перевода из in-progress, а прочие задачи сессии в этот момент идут дальше.
func Works(recs []Bind) []string {
	var out []string
	seen, off := map[string]bool{}, map[string]bool{}
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.Source == ByOff {
			if r.Task == "" {
				break
			}
			off[r.Task] = true
			continue
		}
		if r.Task == "" || seen[r.Task] || off[r.Task] || !workSrc[r.Source] {
			continue
		}
		seen[r.Task] = true
		out = append(out, r.Task)
	}
	return out
}

// Off отвечает, снята ли привязка сессии к задаче: отвязка всей сессии либо
// «снята» с названной задачей. Спрашивают об этом там, где задачу называет не
// реестр, а имя бокового дерева: запись о работе такой привязке не нужна, а
// снятие обязано её убирать.
func Off(recs []Bind, task string) bool {
	task = strings.ToUpper(strings.TrimSpace(task))
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.Source != ByOff {
			continue
		}
		if r.Task == "" || r.Task == task {
			return true
		}
	}
	return false
}

// WorksOn отвечает, ведёт ли сессия работу по задаче task. Тот же критерий, что
// у Works, спрошенный про одну задачу: свёртку строки считают по множеству
// сессий, и собирать ради одного вопроса весь список незачем.
func WorksOn(recs []Bind, task string) bool {
	task = strings.ToUpper(strings.TrimSpace(task))
	if task == "" {
		return false
	}
	for _, id := range Works(recs) {
		if id == task {
			return true
		}
	}
	return false
}

// All сворачивает журнал в список записей на сессию, порядком записи.
func All(data []byte) map[string][]Bind {
	out := map[string][]Bind{}
	for _, ln := range strings.Split(string(data), "\n") {
		if sid, b, ok := ParseLine(ln); ok {
			out[sid] = append(out[sid], b)
		}
	}
	return out
}

// LoadAll читает журнал дома списком записей на сессию.
func LoadAll(home string) map[string][]Bind {
	data, err := os.ReadFile(Path(home))
	if err != nil {
		return map[string][]Bind{}
	}
	return All(data)
}

// Touched называет задачи, которых сессия касалась, свежими первыми. Касание
// шире работы: сюда идёт и привязка рукой, потому что список чатов подписывает
// разговор той задачей, о которой в нём говорят. Отвязка читается так же, как
// в Works: пустая задача у «снята» стирает накопленное целиком, названная
// снимает одну эту задачу.
func Touched(recs []Bind) []string {
	var out []string
	seen, off := map[string]bool{}, map[string]bool{}
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.Source == ByOff {
			if r.Task == "" {
				break
			}
			off[r.Task] = true
			continue
		}
		if r.Task == "" || seen[r.Task] || off[r.Task] {
			continue
		}
		seen[r.Task] = true
		out = append(out, r.Task)
	}
	return out
}

// Last отдаёт последнюю запись сессии: у неё берутся дерево, транскрипт и имя
// tmux-сессии, поля не задачные и перезаписью не портятся. Пустое поле свежей
// записи добирается из прежних: строку по факту работы кладёт утилита, которой
// про tmux ничего не известно.
func Last(recs []Bind) Bind {
	var b Bind
	for _, r := range recs {
		if r.Project != "" {
			b.Project = r.Project
		}
		if r.Tree != "" {
			b.Tree = r.Tree
		}
		if r.Transcript != "" {
			b.Transcript = r.Transcript
		}
		if r.Tmux != "" {
			b.Tmux = r.Tmux
		}
		// Родитель называется при рождении сессии, а последующие записи
		// (compact, resume) пишет тот же процесс с тем же окружением. Пустое
		// поле поздней записи родителя не снимает: розданная работа остаётся
		// розданной до конца сессии.
		if r.Parent != "" {
			b.Parent = r.Parent
		}
		b.Task, b.Source, b.Time = r.Task, r.Source, r.Time
	}
	return b
}

// Line собирает строку журнала: формат один на всех писателей, и разъехавшись,
// они оставили бы читателя с половиной записей (hooks/session-task.py, record).
func Line(now time.Time, sid string, b Bind, why string) string {
	dash := func(v string) string {
		if v = strings.Join(strings.Fields(v), " "); v == "" {
			return "-"
		}
		return v
	}
	return now.Format(Stamp) + " сессия " + dash(sid) + " задача " + dash(b.Task) +
		" проект " + dash(b.Project) + " дерево " + dash(b.Tree) +
		" транскрипт " + dash(b.Transcript) + " источник " + dash(b.Source) +
		" повод " + dash(why) + " tmux " + dash(b.Tmux) + "\n"
}

// Append дописывает строку в журнал, обрезав разросшийся файл теми же берегами,
// что держит писатель на python (hookio.append_capped).
func Append(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 100*1024 {
		if data, err := os.ReadFile(path); err == nil {
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) > 500 {
				lines = lines[len(lines)-500:]
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

// SessionEnv это ключ окружения, которым харнес называет сессии её же ID.
const SessionEnv = "CLAUDE_CODE_SESSION_ID"

// Touch отмечает в журнале, что сессия работала над задачей. Зовут его утилиты
// доски из своей main: сессии ID известен только из окружения, и вне сессии
// харнеса отметки не выходит вовсе, это штатное молчание. Ошибка записи гасится
// нарочно: журнал разговора не повод ронять команду доски.
func Touch(task, why string) { mark(task, BySrc, why) }

// Release отмечает конец работы сессии над задачей: запись «снята» с названной
// задачей. Кладут её те же утилиты доски там, где работа по строке кончилась
// (закрытие, перевод из in-progress), и без неё сессия числилась бы рабочей до
// собственной смерти, а строка держала бы «Стоп» вместо кнопки запуска.
// Соседние задачи той же сессии запись не трогает, разбор в Works.
func Release(task, why string) { mark(task, ByOff, why) }

func mark(task, src, why string) {
	sid := strings.TrimSpace(os.Getenv(SessionEnv))
	task = strings.ToUpper(strings.TrimSpace(task))
	if sid == "" || task == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	tree, _ := os.Getwd()
	Append(Path(home), Line(time.Now(), sid, Bind{Task: task, Source: src, Tree: tree}, why))
}

// TmuxOwner называет разговор, которому сейчас принадлежит tmux-сессия name:
// сессию из свежайшей записи реестра, назвавшей это имя. Пустое имя значит, что
// реестр про него не знает.
//
// Имя tmux живёт дольше разговора и переиспользуется нарочно: конвейер задачи
// поднимает task-<ID> тем же именем после снятия прежнего, а chatNewName отдаёт
// номер снятого диалога следующему. Поэтому «сессия с таким именем жива» не
// значит «жива та самая сессия», и адресовать по имени, не сверив хозяина,
// значит писать в чужой разговор (DK-397 POC: реплика уехала посторонней живой
// сессии, занявшей освободившееся имя).
func TmuxOwner(recs map[string][]Bind, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	best, at := "", ""
	for sid, rs := range recs {
		for _, r := range rs {
			if r.Tmux != name {
				continue
			}
			if best == "" || r.Time > at {
				best, at = sid, r.Time
			}
		}
	}
	return best
}

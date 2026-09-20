package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/spend"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// spendWorkRe ловит номер работы субагента в тексте этапа. Номер кладёт туда
// хук спавна (hooks/stagerun.py) и вердикт pick, и он же стоит отдельным полем
// живой записи: пакет, уехавший в «Ход работы», отдельного поля не несёт, а
// текст переживает и смену статуса, и архивацию.
var spendWorkRe = regexp.MustCompile(`работа ([0-9a-fA-F]{6,})`)

// spendQuotaRe ловит снимок квоты, который вердикт pick кладёт в текст этапа.
// Проценты недельного бакета считает сервер, и пересчёта токенов в них у
// devkit нет: они идут рядом справочной колонкой, а не складываются с
// токенами (развилка «валюта» задачи DK-912).
var spendQuotaRe = regexp.MustCompile(`week_\w+ \d+%`)

// spendMark это голова строки итога, которую taskctl close кладёт в «Ход
// работы» перед архивацией: живая запись к тому моменту стёрта, транскрипты
// на машине переживают задачу, но чистятся харнесом, а строка остаётся.
const spendMark = "- Токены: "

// spendStage это этап задачи, каким его видит свод: вид, время, текст, чья
// сессия его писала и какая работа субагента за ним стоит.
type spendStage struct {
	kind    string
	start   time.Time
	span    time.Duration
	note    string
	session string
	work    string
}

// spendRow это этап со сведённым расходом либо с причиной, по которой расхода
// не видно.
type spendRow struct {
	st   spendStage
	node spend.Node
	ok   bool
	why  string
}

// spendStages собирает этапы задачи. Живая запись старше файла: задача в
// работе держит этапы в ~/.devkit/runs, а в «Ход работы» пакет уезжает сменой
// статуса. У закрытой и заархивированной задачи остаётся только файл, и
// номер работы там лежит внутри текста этапа.
func spendStages(root, id string) ([]spendStage, string) {
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err == nil && len(rec.Stages) > 0 {
		now := timeNow()
		out := make([]spendStage, 0, len(rec.Stages))
		for _, s := range rec.Stages {
			span := time.Duration(0)
			if s.Ended() {
				span = s.End.Sub(s.Start)
			} else if now.After(s.Start) {
				span = now.Sub(s.Start)
			}
			work := s.Work
			if work == "" {
				work = spendWorkOf(s.Note)
			}
			out = append(out, spendStage{kind: s.Kind, start: s.Start, span: span,
				note: s.Note, session: s.Session, work: work})
		}
		return out, "живая запись этапов"
	}
	path, ok := spendTaskFile(root, id)
	if !ok {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	var out []spendStage
	for _, ln := range taskform.SectionLines(string(data), stageSection) {
		s, d, ok := stage.ParseLine(ln)
		if !ok {
			continue
		}
		out = append(out, spendStage{kind: s.Kind, start: s.Start, span: d,
			note: s.Note, work: spendWorkOf(s.Note)})
	}
	return out, "«Ход работы» " + filepath.Base(path)
}

// spendWorkOf вытаскивает номер работы из текста этапа.
func spendWorkOf(note string) string {
	if m := spendWorkRe.FindStringSubmatch(note); m != nil {
		return m[1]
	}
	return ""
}

// spendTaskFile отдаёт файл задачи, живой или заархивированный.
func spendTaskFile(root, id string) (string, bool) {
	path := taskFilePath(root, id)
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	got, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "archive", "*", id+".md"))
	if len(got) > 0 {
		return got[0], true
	}
	return "", false
}

// spendRows считает расход каждого этапа. Этап без номера работы и работа без
// транскрипта не пропускаются молча: у строки встаёт причина, по которой
// чисел нет. Заход второго харнеса выглядит отсюда так же, как стёртый
// транскрипт: журналов вне ~/.claude на машине нет, и обе причины называются
// одной строкой (развилка «второй харнес» цели DK-909).
func spendRows(home string, stages []spendStage) []spendRow {
	out := make([]spendRow, 0, len(stages))
	for _, st := range stages {
		row := spendRow{st: st}
		switch {
		case st.work == "":
			row.why = "номера работы в записи этапа нет"
			if stage.IsWait(st.kind) {
				row.why = "ожидание, субагента за ним нет"
			}
		default:
			n, ok := spend.Tree(home, st.session, st.work)
			if !ok {
				row.why = "транскрипта работы " + st.work + " нет в журналах харнеса claude: второй харнес либо журнал стёрт"
				break
			}
			row.node, row.ok = n, true
		}
		out = append(out, row)
	}
	return out
}

// spendTotal складывает расход всех этапов со сведёнными числами.
func spendTotal(rows []spendRow) (spend.Usage, int) {
	var total spend.Usage
	seen := 0
	for _, r := range rows {
		if !r.ok {
			continue
		}
		total = total.Add(r.node.Total())
		seen++
	}
	return total, seen
}

// humanTokens печатает число токенов коротко: тысячи и миллионы, как их
// называет человек, разбирая расход руками.
func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// spendNumbers это колонки расхода одной строки свода.
func spendNumbers(u spend.Usage) string {
	return fmt.Sprintf("ходов %d, вывод %s, свежий вход %s, чтение кэша %s",
		u.Turns, humanTokens(u.Output), humanTokens(u.Input), humanTokens(u.CacheRead))
}

// spendSpan печатает часы этапа так же, как их пишет «Ход работы».
func spendSpan(st spendStage) string {
	out := st.start.Format("2006-01-02 15:04")
	if st.span > 0 {
		out += "-" + st.start.Add(st.span).Format("15:04")
	}
	return out
}

// cmdSpend печатает расход токенов по этапам задачи. Считается на лету из
// транскриптов: они лежат на машине и переживают задачу, а хранить свод в
// файле значило бы держать вторую копию чисел, которая устареет на следующем
// же заходе (развилка «хранение свода» цели DK-909). В файл задачи ложится
// одна строка итога, и кладёт её закрытие.
func cmdSpend(root, id string) (string, error) {
	stages, src := spendStages(root, id)
	if len(stages) == 0 {
		return fmt.Sprintf("токены %s: этапов не нашлось ни в записи ~/.devkit/runs, ни в разделе «Ход работы» файла задачи, считать нечего", id), nil
	}
	home := stage.Home()
	rows := spendRows(home, stages)
	total, seen := spendTotal(rows)
	out := []string{fmt.Sprintf("токены %s: %s; этапов %d, со счётом %d, источник %s",
		id, spendNumbers(total), len(rows), seen, src)}
	for _, r := range rows {
		head := fmt.Sprintf("- %s %s", r.st.kind, spendSpan(r.st))
		if !r.ok {
			out = append(out, head+": нет данных, "+r.why)
			continue
		}
		line := fmt.Sprintf("%s, работа %s: %s", head, r.node.Work, spendNumbers(r.node.Total()))
		if q := spendQuotaRe.FindString(r.st.note); q != "" {
			line += ", квота " + q
		}
		out = append(out, line)
		for _, kid := range r.node.Kids {
			out = append(out, spendKidLines(kid, "  ")...)
		}
	}
	return strings.Join(out, "\n"), nil
}

// spendKidLines печатает вложенную работу под тем этапом, который её поднял.
func spendKidLines(n spend.Node, pad string) []string {
	kind := n.Kind
	if kind == "" {
		kind = "субагент"
	}
	out := []string{fmt.Sprintf("%s- вложенная работа %s (%s): %s", pad, n.Work, kind, spendNumbers(n.Total()))}
	for _, kid := range n.Kids {
		out = append(out, spendKidLines(kid, pad+"  ")...)
	}
	return out
}

// spendTotalLine собирает строку итога для «Хода работы». Второе значение
// говорит, есть ли что записывать: у задачи без сведённых этапов строка была
// бы нулями и врала бы про расход.
func spendTotalLine(root, id string, now time.Time) (string, bool) {
	stages, _ := spendStages(root, id)
	if len(stages) == 0 {
		return "", false
	}
	rows := spendRows(stage.Home(), stages)
	total, seen := spendTotal(rows)
	if seen == 0 {
		return "", false
	}
	return fmt.Sprintf("%s%s, этапов со счётом %d, %s.",
		spendMark, spendNumbers(total), seen, now.Format("2006-01-02")), true
}

// writeSpendTotal кладёт строку итога в «Ход работы» файла задачи. Провал
// записи не роняет закрытие: строка доски уже переписана, и терять её из-за
// свода нельзя, а хвост сообщения говорит, что итога нет.
func writeSpendTotal(root, id string, now time.Time) string {
	line, ok := spendTotalLine(root, id, now)
	if !ok {
		return ""
	}
	path := taskFilePath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return ", итог токенов записывать некуда: " + err.Error()
	}
	if err := os.WriteFile(path, []byte(stage.InsertIntoSection(string(data), stageSection, line)), 0o644); err != nil {
		return ", итог токенов не записан: " + err.Error()
	}
	return ", итог токенов в ход работы"
}

// spendPeriod это срез свода за период.
type spendPeriod struct {
	from, to time.Time
	set      bool
}

// parseSpendPeriod разбирает границы среза. Верхняя граница это весь день
// целиком: человек называет день, а не минуту.
func parseSpendPeriod(from, to string) (spendPeriod, error) {
	var p spendPeriod
	if from == "" && to == "" {
		return p, fmt.Errorf("жду spend <ID> либо spend --from 2026-09-01 --to 2026-09-20")
	}
	if from != "" {
		t, err := time.ParseInLocation("2006-01-02", from, time.Local)
		if err != nil {
			return p, fmt.Errorf("дата %q не вида ГГГГ-ММ-ДД", from)
		}
		p.from = t
	}
	p.to = timeNow()
	if to != "" {
		t, err := time.ParseInLocation("2006-01-02", to, time.Local)
		if err != nil {
			return p, fmt.Errorf("дата %q не вида ГГГГ-ММ-ДД", to)
		}
		p.to = t.Add(24 * time.Hour)
	}
	p.set = true
	return p, nil
}

// holds отвечает, попадает ли этап в срез.
func (p spendPeriod) holds(t time.Time) bool {
	if !p.from.IsZero() && t.Before(p.from) {
		return false
	}
	return t.Before(p.to)
}

// spendIDs собирает задачи, у которых есть файл: живой или в архиве. Обход
// идёт по файлам задач, а не по транскриптам машины: журналов там полтора
// гигабайта, и читаются из них только те потоки, чьи номера работ названы
// этапами (развилка «объём» задачи DK-912).
func spendIDs(root string) []string {
	var out []string
	seen := map[string]bool{}
	pats := []string{
		filepath.Join(root, "docs", "tasks", "*.md"),
		filepath.Join(root, "docs", "tasks", "archive", "*", "*.md"),
	}
	for _, pat := range pats {
		got, _ := filepath.Glob(pat)
		for _, p := range got {
			id := strings.TrimSuffix(filepath.Base(p), ".md")
			if strings.Contains(id, ".") || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// spendTask это расход одной задачи за срез.
type spendTask struct {
	id    string
	usage spend.Usage
	blind int
}

// cmdSpendPeriod печатает те же числа по всем задачам среза: сумму, разбивку
// по видам этапов, медиану задачи и хвост самых дорогих.
func cmdSpendPeriod(root string, p spendPeriod) (string, error) {
	home := stage.Home()
	var total spend.Usage
	byKind := map[string]spend.Usage{}
	counts := map[string]int{}
	var tasks []spendTask
	blind := 0
	for _, id := range spendIDs(root) {
		stages, _ := spendStages(root, id)
		var cut []spendStage
		for _, st := range stages {
			if p.holds(st.start) {
				cut = append(cut, st)
			}
		}
		if len(cut) == 0 {
			continue
		}
		rows := spendRows(home, cut)
		u, seen := spendTotal(rows)
		for _, r := range rows {
			if !r.ok {
				blind++
				continue
			}
			byKind[r.st.kind] = byKind[r.st.kind].Add(r.node.Total())
			counts[r.st.kind]++
		}
		if seen == 0 {
			continue
		}
		total = total.Add(u)
		tasks = append(tasks, spendTask{id: id, usage: u, blind: len(rows) - seen})
	}
	span := p.to.Add(-time.Nanosecond).Format("2006-01-02")
	head := "по " + span
	if !p.from.IsZero() {
		head = p.from.Format("2006-01-02") + ".." + span
	}
	if len(tasks) == 0 {
		return fmt.Sprintf("токены за %s: задач со сведёнными этапами нет, этапов без данных %d", head, blind), nil
	}
	out := []string{fmt.Sprintf("токены за %s: %s; задач %d, этапов без данных %d",
		head, spendNumbers(total), len(tasks), blind)}
	for _, k := range stage.Kinds {
		if counts[k] == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("- %s: %s, этапов %d", k, spendNumbers(byKind[k]), counts[k]))
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].usage.Output != tasks[j].usage.Output {
			return tasks[i].usage.Output > tasks[j].usage.Output
		}
		return tasks[i].id < tasks[j].id
	})
	out = append(out, "- медиана задачи: вывод "+humanTokens(spendMedian(tasks)))
	var tail []string
	for _, t := range tasks {
		if len(tail) == 3 {
			break
		}
		tail = append(tail, t.id+" "+humanTokens(t.usage.Output))
	}
	out = append(out, "- дороже прочих по выводу: "+strings.Join(tail, ", "))
	return strings.Join(out, "\n"), nil
}

// spendMedian это медиана вывода по задачам среза: средняя задача честнее
// среднего арифметического, которое перекашивает один дорогой заход.
func spendMedian(tasks []spendTask) int {
	if len(tasks) == 0 {
		return 0
	}
	vals := make([]int, 0, len(tasks))
	for _, t := range tasks {
		vals = append(vals, t.usage.Output)
	}
	sort.Ints(vals)
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid]
	}
	return (vals[mid-1] + vals[mid]) / 2
}

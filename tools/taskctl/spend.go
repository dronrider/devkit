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
func spendStages(root, main, id string) ([]spendStage, string) {
	rec, err := stage.Load(stage.Path(stage.Home(), main, id))
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
func spendRows(look *spend.Lookup, stages []spendStage) []spendRow {
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
			n, ok := look.Tree(st.session, st.work)
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
	main := stage.MainRoot(root)
	stages, src := spendStages(root, main, id)
	home := stage.Home()
	crew := newSpendCrew(home, main)
	items, loose, blind := crew.taskItems(id, stages, spendPeriod{})
	if stand, runs := spendStands(root, id, spendPeriod{}); runs > 0 {
		items.add(itemStand, id, stand)
		items.get(itemStand, id).parts = runs
	}
	list := items.list()
	if len(stages) == 0 && len(list) == 0 {
		return fmt.Sprintf("токены %s: ни этапов в записи ~/.devkit/runs и «Ходе работы», ни статей в журналах машины, считать нечего", id), nil
	}
	rows := spendRows(spend.NewLookup(home), stages)
	rows = spendDropSetup(rows, items)
	total, seen := spendTotal(rows)
	for _, it := range list {
		total = total.Add(it.usage)
	}
	out := []string{fmt.Sprintf("токены %s: %s; этапов %d, со счётом %d, статей %d, источник %s",
		id, spendNumbers(total), len(rows), seen, len(list), src)}
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
	out = append(out, spendItemLines(list, loose, blind)...)
	return strings.Join(out, "\n"), nil
}

// spendItemLines печатает статьи задачи теми же колонками, что и этапы. Хвост
// строки говорит, по скольким сессиям или прогонам она собрана.
func spendItemLines(list []*spendItem, loose spend.Usage, blind int) []string {
	var out []string
	for _, it := range list {
		tail := fmt.Sprintf(", сессий %d", it.parts)
		if it.kind == itemStand {
			tail = fmt.Sprintf(", прогонов %d", it.parts)
		}
		out = append(out, fmt.Sprintf("- статья %s: %s%s", it.kind, spendNumbers(it.usage), tail))
	}
	if !loose.Empty() {
		out = append(out, "- оркестрация без привязки: "+spendNumbers(loose)+
			", ходы сессии пачки, за которыми не стояло работы ни с одним ID")
	}
	if blind > 0 {
		out = append(out, fmt.Sprintf("- нет данных, сессий без транскрипта %d: %s", blind, whyNoTranscript))
	}
	return out
}

// spendDropSetup убирает строку этапа «постановка», когда та же запись стоит
// статьёй: разбор черновика ведёт головная сессия, номера работы у этапа нет, и
// строка этапа была бы «нет данных» рядом с честными числами статьи (кейс 3
// развилки «кейсы» задачи DK-913).
func spendDropSetup(rows []spendRow, items *spendItems) []spendRow {
	has := false
	for _, it := range items.list() {
		if it.kind == itemSetup {
			has = true
		}
	}
	if !has {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if r.st.kind == stage.Setup && !r.ok {
			continue
		}
		out = append(out, r)
	}
	return out
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
	main := stage.MainRoot(root)
	home := stage.Home()
	stages, _ := spendStages(root, main, id)
	crew := newSpendCrew(home, main)
	items, _, _ := crew.taskItems(id, stages, spendPeriod{})
	if stand, runs := spendStands(root, id, spendPeriod{}); runs > 0 {
		items.add(itemStand, id, stand)
	}
	list := items.list()
	if len(stages) == 0 && len(list) == 0 {
		return "", false
	}
	rows := spendRows(spend.NewLookup(home), stages)
	total, seen := spendTotal(rows)
	if seen == 0 && len(list) == 0 {
		return "", false
	}
	// Статьи входят в итог наравне с этапами: диспетчер задачи стоит дороже
	// её исполнителя, и строка без него занижала бы расход втрое (DK-913).
	for _, it := range list {
		total = total.Add(it.usage)
	}
	return fmt.Sprintf("%s%s, этапов со счётом %d, статей %d, %s.",
		spendMark, spendNumbers(total), seen, len(list), now.Format("2006-01-02")), true
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
}

// cmdSpendPeriod печатает те же числа по всем задачам среза и статьи рядом с
// этапами. Числа тут берутся по ходам, а не по потокам целиком: срез режет
// работу по датам, которые называет человек, а поток субагента переживает
// полночь как ни в чём не бывало. Остаток, которому не нашлось ни этапа, ни
// статьи, стоит строкой «вне статей», и сумма сходится с расходом всех
// транскриптов среза.
func cmdSpendPeriod(root string, p spendPeriod) (string, error) {
	home := stage.Home()
	look := spend.NewLookup(home)
	// Корень основного чекаута спрашивается у git, и на каждую задачу это был
	// бы свой подпроцесс: у боевой доски их под тысячу (замечание ревью
	// DK-912). Считается он один раз на весь срез.
	main := stage.MainRoot(root)
	crew := newSpendCrew(home, main)
	works, stageCount, blind := spendWorkMap(root, main, look, p)

	byKind := map[string]spend.Usage{}
	counts := map[string]int{}
	byTask := map[string]spend.Usage{}
	var stagesUsage, outside, total spend.Usage
	items := newSpendItems()
	heads, lost := 0, 0
	mute := map[string]bool{}
	// Потоков в срезе два разряда, и считаются они по-разному. Поток головной
	// сессии режется по ходам: за один заход она ведёт несколько задач, и
	// статью каждому ходу называет журнал агентов. Поток работы субагента
	// принадлежит одному этапу целиком, и от него нужны только ходы, попавшие
	// в срез.
	for _, st := range spend.Streams(home, p.from) {
		turns, err := spend.ReadTurns(st.Path)
		if err != nil {
			continue
		}
		var cut spend.Usage
		for _, t := range turns {
			if p.set && !p.holds(t.At) {
				continue
			}
			if st.Session == "" {
				cut = cut.Add(t.Usage)
				continue
			}
			total = total.Add(t.Usage)
			kind, key, ok := crew.place(st.Session, t.At)
			if !ok {
				outside = outside.Add(t.Usage)
				if b := crew.binds[st.Session]; b.Carrier == "" {
					mute[st.Session] = true
				}
				continue
			}
			items.add(kind, key, t.Usage)
		}
		if st.Session != "" {
			heads++
			continue
		}
		if cut.Empty() {
			continue
		}
		total = total.Add(cut)
		m, ok := works[st.Work]
		if !ok {
			outside = outside.Add(cut)
			lost++
			continue
		}
		stagesUsage = stagesUsage.Add(cut)
		byKind[m.kind] = byKind[m.kind].Add(cut)
		counts[m.kind]++
		byTask[m.task] = byTask[m.task].Add(cut)
	}
	for _, id := range spendIDs(root) {
		stand, runs := spendStands(root, id, p)
		if runs == 0 {
			continue
		}
		items.add(itemStand, id, stand)
		items.get(itemStand, id).parts = runs
		total = total.Add(stand)
	}
	// Статья на ID задачи это её же расход, и в медиану с хвостом дорогих она
	// входит наравне с этапами: диспетчер стоит дороже исполнителя, и свод,
	// который его не считает, врёт втрое. Фон цели в задачи не едет: цель это
	// запись, а не строка доски, и её виток меряется своим бюджетом.
	goals := map[string]bool{}
	for _, id := range crew.goals {
		goals[id] = true
	}
	for _, it := range items.list() {
		if it.kind == itemBack || it.key == keyLoose || goals[it.key] {
			continue
		}
		byTask[it.key] = byTask[it.key].Add(it.usage)
	}

	span := p.to.Add(-time.Nanosecond).Format("2006-01-02")
	// Срез без нижней границы это весь срок машины, и предлог у него свой:
	// «токены за по 18 сентября» не по-русски.
	head := "токены по " + span
	if !p.from.IsZero() {
		head = "токены за " + p.from.Format("2006-01-02") + ".." + span
	}
	if total.Empty() {
		return fmt.Sprintf("%s: транскриптов харнеса claude за срез нет, этапов без данных %d", head, blind), nil
	}
	tasks := spendTasks(byTask)
	out := []string{fmt.Sprintf("%s: %s; сессий %d, задач %d, этапов %d, без данных %d",
		head, spendNumbers(total), heads, len(tasks), stageCount, blind)}
	out = append(out, "- этапы: "+spendNumbers(stagesUsage)+spendShare(stagesUsage, total))
	for _, k := range stage.Kinds {
		if counts[k] == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("  - %s: %s, работ %d", k, spendNumbers(byKind[k]), counts[k]))
	}
	out = append(out, spendPeriodItems(items, total)...)
	out = append(out, "- вне статей: "+spendNumbers(outside)+spendShare(outside, total)+"; "+whyOutside)
	if len(mute) > 0 || lost > 0 {
		out = append(out, fmt.Sprintf("- нет данных: сессий без носителя в реестре %d, работ без этапа %d",
			len(mute), lost))
	}
	out = append(out, spendTaskLines(root, tasks)...)
	return strings.Join(out, "\n"), nil
}

// whyOutside говорит, из чего состоит остаток. Разговор человека и безголовый
// заход, чья строка реестра носителя не несёт, отсюда неотличимы, и сюда же
// идёт работа субагента, чей этап потерян.
const whyOutside = "разговоры человека, сессии без носителя в реестре и работы без этапа"

// spendMark это этап, которому принадлежит поток работы.
type spendWorkMark struct {
	task string
	kind string
}

// spendWorkMap раскладывает работы субагентов по этапам задач: какой работе
// какой этап и какая задача. Вложенные работы ложатся тому же этапу, который
// их поднял. Второе и третье значения это сколько этапов попало в срез и у
// скольких из них чисел не взять.
func spendWorkMap(root, main string, look *spend.Lookup, p spendPeriod) (map[string]spendWorkMark, int, int) {
	out := map[string]spendWorkMark{}
	count, blind := 0, 0
	for _, id := range spendIDs(root) {
		stages, _ := spendStages(root, main, id)
		for _, st := range stages {
			inCut := !p.set || p.holds(st.start)
			if inCut {
				count++
			}
			if st.work == "" {
				if inCut && !stage.IsWait(st.kind) {
					blind++
				}
				continue
			}
			n, ok := look.Tree(st.session, st.work)
			if !ok {
				if inCut {
					blind++
				}
				continue
			}
			spendMarkTree(out, n, spendWorkMark{task: id, kind: st.kind})
		}
	}
	return out, count, blind
}

// spendMarkTree помечает работу вместе с поднятыми из неё.
func spendMarkTree(out map[string]spendWorkMark, n spend.Node, m spendWorkMark) {
	if _, seen := out[n.Work]; !seen {
		out[n.Work] = m
	}
	for _, kid := range n.Kids {
		spendMarkTree(out, kid, m)
	}
}

// spendShare печатает долю строки от расхода среза. Доля считается по выводу:
// чтение кэша растёт от длины разговора, а вывод это то, что модель сделала.
func spendShare(u, total spend.Usage) string {
	if total.Output == 0 {
		return ""
	}
	return fmt.Sprintf(", доля вывода %d%%", u.Output*100/total.Output)
}

// spendPeriodItems печатает статьи среза: строка на вид и под ней ключи, дорогие
// первыми.
func spendPeriodItems(items *spendItems, total spend.Usage) []string {
	byKind := map[string]spend.Usage{}
	var order []string
	for _, it := range items.list() {
		if _, seen := byKind[it.kind]; !seen {
			order = append(order, it.kind)
		}
		byKind[it.kind] = byKind[it.kind].Add(it.usage)
	}
	var out []string
	for _, kind := range order {
		out = append(out, "- статья "+kind+": "+spendNumbers(byKind[kind])+spendShare(byKind[kind], total))
		for _, it := range items.list() {
			if it.kind != kind {
				continue
			}
			out = append(out, "  - "+it.key+": "+spendNumbers(it.usage))
		}
	}
	return out
}

// spendTasks сворачивает расход по задачам в список, дорогие первыми.
func spendTasks(byTask map[string]spend.Usage) []spendTask {
	var out []spendTask
	for id, u := range byTask {
		if u.Empty() {
			continue
		}
		out = append(out, spendTask{id: id, usage: u})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].usage.Output != out[j].usage.Output {
			return out[i].usage.Output > out[j].usage.Output
		}
		return out[i].id < out[j].id
	})
	return out
}

// spendTaskLines печатает медиану задачи, подгруппу проектирования и хвост
// самых дорогих. Задача типа LLD в медиану разработки не входит: проектирование
// идёт другим заходом и другой ценой, и общая медиана от него перекашивается.
func spendTaskLines(root string, tasks []spendTask) []string {
	lld := spendLLD(root)
	var plain, design []spendTask
	for _, t := range tasks {
		if lld[t.id] {
			design = append(design, t)
			continue
		}
		plain = append(plain, t)
	}
	out := []string{"- медиана задачи: вывод " + humanTokens(spendMedian(plain))}
	if len(design) > 0 {
		var u spend.Usage
		for _, t := range design {
			u = u.Add(t.usage)
		}
		out = append(out, fmt.Sprintf("- подгруппа LLD: %s, задач %d, в медиану не входит",
			spendNumbers(u), len(design)))
	}
	var tail []string
	for _, t := range tasks {
		if len(tail) == 3 {
			break
		}
		tail = append(tail, t.id+" "+humanTokens(t.usage.Output))
	}
	if len(tail) > 0 {
		out = append(out, "- дороже прочих по выводу: "+strings.Join(tail, ", "))
	}
	return out
}

// spendLLD называет задачи типа LLD по строке доски и по строке архива.
// Нечитаемая доска это пустая карта: свод печатается и без неё, просто одной
// подгруппой.
func spendLLD(root string) map[string]bool {
	out := map[string]bool{}
	if b, err := LoadBoard(boardPath(root)); err == nil {
		for _, r := range b.Rows {
			if isLLD(r.Type) {
				out[r.ID] = true
			}
		}
	}
	if a, err := LoadArchive(archivePath(root)); err == nil {
		for _, r := range a.Rows {
			if len(r.Cells) > 2 && isLLD(r.Cells[2]) {
				out[r.ID] = true
			}
		}
	}
	return out
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

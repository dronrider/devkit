package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// Разделы файла цели, которые читает гейт. Формат самого файла держит скилл
// goal-loop, agentctl из него берёт только эти два: в «Бюджете» лежат лимиты
// постановки, в «Журнале» снимки квоты по виткам.
const (
	goalBudgetSection  = "## Бюджет"
	goalJournalSection = "## Журнал"
)

// Префиксы машинных строк файла цели. Строка бюджета это «бюджет: week_all <=
// 25», потолок моделей «ярус: pro», снимок витка «снимок 2026-08-03T12:00:
// week_all 12%, week_max 4%».
const (
	goalLimitPrefix = "бюджет:"
	goalTierPrefix  = "ярус:"
	goalSnapPrefix  = "снимок "
)

// goalLimit это потолок расхода по одному бакету в процентных пунктах. Токены и
// доллары локально нечестны (DK-010), поэтому цена цели задаётся тем же, чем
// меряет панель.
type goalLimit struct {
	Bucket string
	Limit  int
}

// goalBudget это разобранный раздел «Бюджет». Потолка яруса может не быть,
// тогда вердикт pick работает как обычно.
type goalBudget struct {
	Limits []goalLimit
	Tier   string
}

// goalSnap это строка снимка из «Журнала»: момент снятия и проценты бакетов.
// Хранится процентами, а не долями: панель меряет целыми пунктами, и дельты
// считаются в них же, без плавающей арифметики.
type goalSnap struct {
	Taken time.Time
	Pct   map[string]int
}

// goalSection вырезает строки раздела до следующего заголовка того же уровня.
// Второй такой раздел в файле не ищется: читается первый, как и у override.
func goalSection(text, heading string) []string {
	var out []string
	in := false
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "## ") {
			if in {
				break
			}
			in = strings.HasPrefix(ln, heading)
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return out
}

// goalLine чистит строку раздела до машинной части: пункт списка и ограда
// code-fence отбрасываются, проза остаётся как есть и разбором не считается.
func goalLine(ln string) string {
	t := strings.TrimSpace(ln)
	t = strings.TrimPrefix(t, "- ")
	if strings.HasPrefix(t, "```") {
		return ""
	}
	return t
}

// parseGoalBudget разбирает раздел «Бюджет». Строка с известным префиксом,
// которую разобрать не вышло, это ошибка постановки, а не пропуск: гейт с
// недочитанным лимитом молча пустил бы цикл дальше своей цены. Незнакомый
// бакет ошибка по той же причине, и знание про бакеты приходит из профиля
// харнеса: нет объявления квоты, значит сверять не с чем, и имя берётся как
// написано.
func parseGoalBudget(lines []string, q *quotaSpec) (goalBudget, error) {
	var b goalBudget
	for _, ln := range lines {
		t := goalLine(ln)
		switch {
		case strings.HasPrefix(t, goalLimitPrefix):
			l, err := parseGoalLimit(strings.TrimSpace(strings.TrimPrefix(t, goalLimitPrefix)))
			if err != nil {
				return b, fmt.Errorf("строка бюджета %q: %v", t, err)
			}
			if q != nil && !q.known(l.Bucket) {
				return b, fmt.Errorf("строка бюджета %q: бакет %s харнесу %s незнаком, известны %s",
					t, l.Bucket, q.Harness, strings.Join(q.Buckets, ", "))
			}
			l.Bucket = canonBucket(l.Bucket)
			b.Limits = append(b.Limits, l)
		case strings.HasPrefix(t, goalTierPrefix):
			name := strings.TrimSpace(strings.TrimPrefix(t, goalTierPrefix))
			tier, ok := overrideTiers[name]
			if !ok {
				return b, fmt.Errorf("строка бюджета %q: неизвестный ярус %q, допустимы mini, base, pro, max и старые имена haiku, sonnet, opus, fable", t, name)
			}
			b.Tier = tier
		}
	}
	return b, nil
}

// parseGoalLimit разбирает «week_all <= 25». Знак пишется словом языка
// постановки, а не сравнением из кода: строку читает и пользователь.
func parseGoalLimit(s string) (goalLimit, error) {
	name, rest, ok := strings.Cut(s, "<=")
	if !ok {
		return goalLimit{}, fmt.Errorf("жду «бакет <= число процентных пунктов», вижу %q", s)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return goalLimit{}, fmt.Errorf("бакет не назван")
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "%")))
	if err != nil || n < 0 || n > 100 {
		return goalLimit{}, fmt.Errorf("потолок %q не в диапазоне 0-100", strings.TrimSpace(rest))
	}
	return goalLimit{Bucket: name, Limit: n}, nil
}

// parseGoalJournal собирает снимки витков. Битая строка снимка отбрасывается с
// предупреждением, а не роняет гейт: журнал пишет машина, но правят его и
// руками, и цикл из-за одной строки вставать не должен. Молчать про пропуск при
// этом нельзя, расход по неполной цепочке занижен.
func parseGoalJournal(lines []string) ([]goalSnap, []string) {
	var snaps []goalSnap
	var warns []string
	for _, ln := range lines {
		t := goalLine(ln)
		if !strings.HasPrefix(t, goalSnapPrefix) {
			continue
		}
		s, err := parseGoalSnap(strings.TrimPrefix(t, goalSnapPrefix))
		if err != nil {
			warns = append(warns, fmt.Sprintf("строка журнала %q не разобрана (%v), в расход не вошла", t, err))
			continue
		}
		snaps = append(snaps, s)
	}
	return snaps, warns
}

// parseGoalSnap разбирает «2026-08-03T12:00: week_all 12%, week_max 4%».
func parseGoalSnap(s string) (goalSnap, error) {
	head, rest, ok := strings.Cut(s, ": ")
	if !ok {
		return goalSnap{}, fmt.Errorf("жду «время: бакет N%%», вижу %q", s)
	}
	taken, err := parseQuotaTime(head)
	if err != nil {
		return goalSnap{}, fmt.Errorf("момент снятия %q не разобран", head)
	}
	snap := goalSnap{Taken: taken, Pct: map[string]int{}}
	for _, part := range strings.Split(rest, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) != 2 {
			return goalSnap{}, fmt.Errorf("бакет %q не разобран", strings.TrimSpace(part))
		}
		n, err := strconv.Atoi(strings.TrimSuffix(fields[1], "%"))
		if err != nil || n < 0 || n > 100 {
			return goalSnap{}, fmt.Errorf("процент %q не в диапазоне 0-100", fields[1])
		}
		snap.Pct[canonBucket(fields[0])] = n
	}
	return snap, nil
}

// snapOfQuota переводит живой снимок квоты в точку цепочки расхода.
func snapOfQuota(s snapshot) goalSnap {
	g := goalSnap{Taken: s.Taken, Pct: map[string]int{}}
	for _, b := range s.Buckets {
		g.Pct[b.Name] = int(math.Round(b.Used * 100))
	}
	return g
}

// spendOf считает расход бакета суммой пошаговых дельт цепочки. Панель меряет
// от действующего лимита, поэтому отрицательная дельта значит не возврат
// потраченного, а сброс окна: расходом тогда берётся значение нового окна
// целиком. Так сброс не обнуляет счётчик цели, бюджет это сумма потраченного за
// цикл, а не остаток в текущем окне. Снимки, где бакета нет, цепочку не рвут:
// предыдущим берётся последнее известное значение.
func spendOf(chain []goalSnap, bucket string) int {
	total, prev, seen := 0, 0, false
	for _, s := range chain {
		cur, ok := s.Pct[bucket]
		if !ok {
			continue
		}
		if seen {
			if d := cur - prev; d < 0 {
				total += cur
			} else {
				total += d
			}
		}
		prev, seen = cur, true
	}
	return total
}

// goalPathOf ищет файл цели: путь берётся как дан, а не нашёлся, значит
// считается от корня с доской. Скилл зовёт команду с путём вида
// docs/tasks/DK-NNN.md, и работать он обязан из любого дерева задачи.
func goalPathOf(root, path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	full := filepath.Join(root, path)
	if _, err := os.Stat(full); err == nil {
		return full, nil
	}
	return "", fmt.Errorf("файла цели нет: %s", path)
}

// recordGoalSnap дописывает строку снимка в конец раздела «Журнал» файла цели.
// Симметрия с pick --record: считает гейт, а пишет виток, и следующий вызов
// читает записанное как предыдущую точку цепочки.
func recordGoalSnap(path string, s goalSnap) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(s.Pct))
	for name := range s.Pct {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %d%%", name, s.Pct[name]))
	}
	line := fmt.Sprintf("- %s%s: %s", goalSnapPrefix, s.Taken.Format(quotaTimeLayout), strings.Join(parts, ", "))
	return os.WriteFile(path, []byte(stage.InsertIntoSection(string(data), goalJournalSection, line)), 0o644)
}

// Разделы файла цели, откуда сводка берёт состав работы, и её собственный
// заголовок в файле.
const (
	goalTasksSection = "## Задачи цели"
	goalStagesLabel  = "## Ход работы"
)

// Маркеры выхода витка. Список тот же, что у скилла goal-loop и у оболочки
// goal-run: строку витка пишет команда, и опечатка в маркере оставила бы
// журнал без признака, которым цикл кончился.
const goalGoOn = "continue"

var goalStops = []string{"done", "over", "wait-human", "stuck"}

// goalLapRe ловит начало витка в уже записанной строке журнала: сводка стопа
// считает время цикла от первого витка, а снимков квоты в журнале может не
// быть вовсе.
var goalLapRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})`)

// goalTaskRe ловит ID задачи в разделе «Задачи цели». Раздел это проза с
// пунктами кандидатов, а не машинный список, и ID в нём лежат по тексту.
var goalTaskRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-\d+\b`)

// humanDur пишет длительность так, как её читают в журнале: минуты до часа,
// дальше часы с минутами. Секунд нет нигде, ими не меряется ни виток, ни этап.
func humanDur(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 0 {
		m = 0
	}
	if m < 60 {
		return fmt.Sprintf("%dм", m)
	}
	return fmt.Sprintf("%dч %02dм", m/60, m%60)
}

// goalLapNote чистит текст витка до одной строки: перевод строки развалил бы
// пункт журнала, а хвостовая точка с запятой спорила бы с маркером.
func goalLapNote(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimRight(strings.TrimSpace(s), ".;, ")
}

// goalLapSpan собирает часы витка. День один, значит конец пишется часами, а
// перешагнувший полночь виток получает конец с датой: иначе строка читалась бы
// как виток длиной минус двадцать часов.
func goalLapSpan(start, end time.Time) string {
	head := start.Format(stage.LineStamp)
	if end.Format(stage.LineStamp) == head {
		return head
	}
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return head + "-" + end.Format("15:04")
	}
	return head + "-" + end.Format(stage.LineStamp)
}

// goalLaps считает записанные витки и находит начало первого. Виток это
// содержательная строка журнала, снимки квоты витками не считаются, тем же
// правилом их отличает детектор воронки в оболочке goal-run.
func goalLaps(lines []string) (int, time.Time) {
	n := 0
	var first time.Time
	for _, ln := range lines {
		t := goalLine(ln)
		if t == "" || strings.HasPrefix(t, goalSnapPrefix) {
			continue
		}
		n++
		m := goalLapRe.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		at, err := time.ParseInLocation(stage.LineStamp, m[1]+" "+m[2], time.Local)
		if err != nil || (!first.IsZero() && !at.Before(first)) {
			continue
		}
		first = at
	}
	return n, first
}

// goalCycleStart отвечает, когда начался цикл: раньше первого витка снимка
// быть не может, гейт стоит первым шагом, но журнал правят и руками, поэтому
// берётся самый ранний из двух известных моментов.
func goalCycleStart(snaps []goalSnap, firstLap time.Time) time.Time {
	start := firstLap
	for _, s := range snaps {
		if start.IsZero() || s.Taken.Before(start) {
			start = s.Taken
		}
	}
	return start
}

// cmdLap дописывает строку витка в «Журнал» файла цели. Времена ставит
// команда, а не рука пишущего: до этого в журнале была одна машинная метка,
// момент снимка квоты, и на вопрос «куда ушёл день» журнал не отвечал. Начало
// витка берётся из последнего снимка, его кладёт гейт первым шагом витка;
// явное --start нужен там, где гейт не звали.
func cmdLap(root, goalPath, note, marker string, start, now time.Time) (string, error) {
	note = goalLapNote(note)
	if note == "" {
		return "", fmt.Errorf("жду --note с тем, что виток сделал: пустая строка журнала неотличима от штатной работы")
	}
	stop := false
	for _, m := range goalStops {
		if marker == m {
			stop = true
		}
	}
	if !stop && marker != goalGoOn {
		return "", fmt.Errorf("неизвестный маркер %q, жду один из: %s, %s", marker, goalGoOn, strings.Join(goalStops, ", "))
	}
	path, err := goalPathOf(root, goalPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	journal := goalSection(string(data), goalJournalSection)
	snaps, _ := parseGoalJournal(journal)
	laps, firstLap := goalLaps(journal)

	var warn string
	if start.IsZero() {
		if len(snaps) > 0 {
			start = snaps[len(snaps)-1].Taken
		} else {
			start, warn = now, "начала витка не видно: гейт не записал снимок, и виток встал в журнал одним моментом"
		}
	}
	if start.After(now) {
		start = now
	}
	line := "- " + goalLapSpan(start, now) + ", " + note
	if stop {
		cycle := goalCycleStart(snaps, firstLap)
		if cycle.IsZero() || cycle.After(start) {
			cycle = start
		}
		line += fmt.Sprintf(", цикл %s, витков %d", humanDur(now.Sub(cycle)), laps+1)
	}
	line += "; " + marker
	if err := os.WriteFile(path, []byte(stage.InsertIntoSection(string(data), goalJournalSection, line)), 0o644); err != nil {
		return "", err
	}
	out := line + "\nвиток занял " + humanDur(now.Sub(start))
	if warn != "" {
		out += "; " + warn
	}
	return out, nil
}

// goalTaskIDs собирает состав цели из раздела «Задачи цели». Порядок
// сохраняется, повторы отбрасываются, а ID самой цели пропускается: раздел
// ссылается и на неё.
func goalTaskIDs(lines []string, self string) []string {
	var out []string
	seen := map[string]bool{self: true}
	for _, ln := range lines {
		for _, id := range goalTaskRe.FindAllString(goalLine(ln), -1) {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// goalTaskSpan складывает время этапов одной задачи по видам. Виды берутся
// словарём stage.Kinds, а не перечнем: словарь растёт, и сводка обязана
// показывать новый вид сама.
func goalTaskSpan(dir, id string) (map[string]time.Duration, map[string]int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, id+".md"))
	if err != nil {
		return nil, nil, false
	}
	spans, counts := map[string]time.Duration{}, map[string]int{}
	for _, ln := range goalSection(string(data), goalStagesLabel) {
		s, d, ok := stage.ParseLine(ln)
		if !ok {
			continue
		}
		spans[s.Kind] += d
		counts[s.Kind]++
	}
	return spans, counts, true
}

// cmdTally собирает сводку итога цели: куда ушло время задач цели, с разбивкой
// по видам деятельности и по самим задачам. Источник это разделы «Ход работы»
// файлов задач, куда этапы уезжают пакетом при смене статуса: живая запись
// stage к моменту итога уже стёрта, а файл задачи остаётся.
func cmdTally(root, goalPath string) (string, error) {
	path, err := goalPathOf(root, goalPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	self := strings.TrimSuffix(filepath.Base(path), ".md")
	ids := goalTaskIDs(goalSection(string(data), goalTasksSection), self)
	if len(ids) == 0 {
		return fmt.Sprintf("время задач цели: раздел «Задачи цели» файла %s не называет ни одной задачи, складывать нечего", path), nil
	}

	total := time.Duration(0)
	stages := 0
	spans, counts := map[string]time.Duration{}, map[string]int{}
	byTask := map[string]time.Duration{}
	var lost, empty []string
	for _, id := range ids {
		ts, tc, ok := goalTaskSpan(dir, id)
		if !ok {
			lost = append(lost, id)
			continue
		}
		task := time.Duration(0)
		for _, k := range stage.Kinds {
			spans[k] += ts[k]
			counts[k] += tc[k]
			task += ts[k]
			stages += tc[k]
		}
		if len(tc) == 0 {
			empty = append(empty, id)
			continue
		}
		byTask[id] = task
		total += task
	}

	out := []string{fmt.Sprintf("время задач цели: всего %s, этапов %d, задач %d", humanDur(total), stages, len(ids))}
	for _, k := range stage.Kinds {
		out = append(out, fmt.Sprintf("- %s: %s, этапов %d", k, humanDur(spans[k]), counts[k]))
	}
	if len(byTask) > 0 {
		names := make([]string, 0, len(byTask))
		for id := range byTask {
			names = append(names, id)
		}
		sort.Slice(names, func(i, j int) bool {
			if byTask[names[i]] != byTask[names[j]] {
				return byTask[names[i]] > byTask[names[j]]
			}
			return names[i] < names[j]
		})
		var parts []string
		for _, id := range names {
			if len(parts) == 3 {
				break
			}
			parts = append(parts, id+" "+humanDur(byTask[id]))
		}
		out = append(out, "- дольше прочих: "+strings.Join(parts, ", "))
	}
	if len(empty) > 0 {
		out = append(out, "- без записей «Хода работы»: "+strings.Join(empty, ", "))
	}
	if len(lost) > 0 {
		out = append(out, "- файла задачи нет: "+strings.Join(lost, ", "))
	}
	return strings.Join(out, "\n"), nil
}

const (
	gateOK   = "ok"
	gateOver = "over"
)

// Мера расхода честна ровно настолько, насколько честны бакеты панели: они
// общие на машину, и параллельная чужая сессия тратит тот же week_all. Лимит
// поэтому верхняя граница трат машины за время цикла, а не точный счётчик цели.
// Говорится это на стопе: там число решает судьбу цикла.
const goalMeasureTail = "мера верхняя: бакеты общие на машину, чужая сессия тратит те же проценты"

// cmdSpend это гейт бюджета цели: он стоит в начале каждого витка и отвечает
// одним словом, ехать дальше или останавливаться. Первая строка машинная, за
// ней человеческая с числами по каждому бакету.
func cmdSpend(root, goalPath string, record bool, now time.Time) (string, error) {
	path, err := goalPathOf(root, goalPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	hc := resolveHarnessContext(root, "")
	budget, err := parseGoalBudget(goalSection(text, goalBudgetSection), hc.Quota)
	if err != nil {
		return "", err
	}
	if len(budget.Limits) == 0 {
		return "", fmt.Errorf("в разделе «Бюджет» файла %s нет ни одной строки «бюджет: <бакет> <= <проценты>», гейту нечего считать", path)
	}
	chain, warns := parseGoalJournal(goalSection(text, goalJournalSection))

	var live snapshot
	switch {
	case hc.Quota == nil:
		warns = append(warns, hc.quotaWhy()+", расход посчитан по журналу")
	default:
		live, err = hc.Quota.read()
		if err != nil {
			return "", err
		}
		switch {
		case live.empty():
			warns = append(warns, "снимка квоты нет, расход посчитан по журналу; снять: agentctl quota refresh")
		case !live.fresh(now):
			warns = append(warns, "снимок квоты "+snapshotAge(live, now)+", виток начинать с agentctl quota refresh")
		}
		if w := hc.Quota.legacyWarn(); w != "" {
			warns = append(warns, w)
		}
		warns = append(warns, live.Warns...)
	}
	fresh := snapOfQuota(live)
	if len(fresh.Pct) > 0 {
		chain = append(chain, fresh)
	}

	gate := gateOK
	var parts, over []string
	for _, l := range budget.Limits {
		spent := spendOf(chain, l.Bucket)
		part := fmt.Sprintf("%s: потрачено %d из %d", l.Bucket, spent, l.Limit)
		if spent > l.Limit {
			gate = gateOver
			over = append(over, l.Bucket)
			part += ", исчерпан"
		}
		parts = append(parts, part)
	}
	if budget.Tier != "" {
		parts = append(parts, "потолок яруса "+budget.Tier)
	}
	parts = append(parts, fmt.Sprintf("точек расхода %d", len(chain)))
	if len(fresh.Pct) > 0 {
		parts = append(parts, snapshotAge(live, now))
	}
	if gate == gateOver {
		parts = append(parts, fmt.Sprintf("бюджет цели исчерпан по %s, цикл останавливается; %s",
			strings.Join(over, ", "), goalMeasureTail))
	}
	if record && len(fresh.Pct) > 0 {
		if err := recordGoalSnap(path, fresh); err != nil {
			return "", err
		}
	} else if record {
		warns = append(warns, "записывать в журнал нечего: живого снимка квоты нет")
	}
	line := strings.Join(parts, "; ")
	for _, w := range warns {
		line += "; " + w
	}
	return fmt.Sprintf("gate: %s\n%s", gate, line), nil
}

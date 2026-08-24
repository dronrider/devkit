package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
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

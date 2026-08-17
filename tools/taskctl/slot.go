package main

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/works"
)

// Планировщик слота (LLD DK-400, решение 3): отвечает на вопрос «что делать
// в освободившемся слоте», на который ранг не отвечает, потому что не делит
// важность на цену. Три слоя не смешиваются: булевы ворота, числовой счёт
// внутри полосы P, полка фона (DK-405, строится поверх отдельной задачей).

// Курсы цены C по дефицитному ресурсу (замер цели DK-395): время слота при
// дефиците параллелизма, пункты квоты при дефиците подписки. Ресурс выбирает
// вызывающий флагом, умолчание slot.
var slotRates = map[string]map[string]float64{
	"slot":  {"S": 0.8, "M": 1.0, "L": 1.2},
	"quota": {"S": 0.75, "M": 2, "L": 5.5},
}

// Плата за возврат: V = C * min(0.3 + 0.02 * h, 0.55), где h часы простоя.
// Числа замерены по 149 закрытым задачам и живут константами кода.
const (
	slotReturnBase    = 0.3
	slotReturnPerHour = 0.02
	slotReturnCap     = 0.55
)

// slotTreeHead это запас незакрытых деревьев поверх лимита пачки (LLD DK-400,
// решение 5): лимит жмёт параллельный старт, сверх него деревья растут от
// парковок и осиротевших строк. Перебивается окружением для стендов.
const (
	slotTreeHead = 2
	slotTreesEnv = "DEVKIT_SLOT_TREES"
)

// slotQuestionHead это потолок висящих вопросов на цель (LLD DK-400, решение 5):
// два вопроса закрываются одним коротким заходом человека, три это уже простыня,
// ради которой потолок и заводится. Держит его парковка вопросом (move в
// blocked с причиной «вопрос:»), перебивается окружением для стендов.
const (
	slotQuestionHead = 2
	slotQuestionsEnv = "DEVKIT_SLOT_QUESTIONS"
)

// questionCeiling отдаёт потолок висящих вопросов на цель. Окружение
// перебивает число для стендов.
func questionCeiling() int {
	if v := os.Getenv(slotQuestionsEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return slotQuestionHead
}

// slotAwayGate это порог «человек недоступен» (LLD DK-400, решение 3):
// машинный признак недоступности это припаркованный вопрос старше часа без
// ответа. Моложе часа вопрос ещё не повод считать человека ушедшим.
const slotAwayGate = time.Hour

// slotPick это прошедший ворота кандидат со слагаемыми счёта: печать называет
// каждое, чтобы вывод сверялся с ручным счётом формулы (DoD DK-404).
type slotPick struct {
	row   *Row
	C     float64
	F     float64
	mark  string
	V     float64
	W     float64
	score float64
	idle  float64 // часы простоя, из них набрана плата за возврат
	drift int     // насколько main ушёл от ветки задачи, справка, в счёт не входит
}

// blockReason достаёт текст причины из суффикса «[блок: ...]»: машинный
// префикс («вопрос:», «окружение:») и проза читаются одинаково.
func blockReason(blockSuf string) string {
	s := strings.TrimSpace(blockSuf)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "[блок:"), "]")
	return strings.TrimSpace(s)
}

// treeCeiling считает потолок незакрытых деревьев: лимит пачки плюс запас.
// Окружение перебивает число для стендов.
func treeCeiling(limit int) int {
	if v := os.Getenv(slotTreesEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return limit + slotTreeHead
}

// liveTrees считает живые линкованные worktree задач: слияние убирает дерево
// и ветку, стоящее дерево это незакрытая работа. Основной чекаут не считается.
// Вне git и при недоступном git деревьев нет, и потолок жмёт только свежие
// старты.
func liveTrees(root string) int {
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "worktree ") {
			n++
		}
	}
	return n - 1
}

// taskHasTree говорит, есть ли у задачи своя ветка: возврат к припаркованной
// не заводит новое дерево и потолок не жжёт. Ветка без коммитов впереди main
// всё равно считается: рабочее дерево задачи живо, пока жива ветка.
func taskHasTree(root, id string) bool {
	out, err := exec.Command("git", "-C", root, "for-each-ref",
		"--format=%(refname:short)", "refs/heads").Output()
	if err != nil {
		return false
	}
	for _, b := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if branchOfTask(b, id) {
			return true
		}
	}
	return false
}

// waitingTrees называет ждущие деревья: припаркованные строки с живой веткой.
// Отказ по потолку деревьев обязан печатать не только число, но и то, что
// разобрать (LLD DK-400, решение 5): разбор это ответ на ждущую строку, а не
// отказ от свежих стартов навсегда. Осиротевшая строка здесь не ждёт: без
// живой работы она сама кандидат слота, и возврат к ней дерево не заводит.
// Строки в Check тоже не ждут, их деревья съедает слияние.
func waitingTrees(root string, b *Board) []string {
	var waiters []string
	for _, r := range b.Rows {
		if r.Sect != SectBlocked || !taskHasTree(root, r.ID) {
			continue
		}
		_, _, _, _, blockSuf := splitTitle(r.Title)
		reason := blockReason(blockSuf)
		switch {
		case strings.HasPrefix(reason, "вопрос:"):
			waiters = append(waiters, r.ID+" (вопрос)")
		case strings.HasPrefix(reason, "окружение:"):
			waiters = append(waiters, r.ID+" (окружение)")
		default:
			waiters = append(waiters, r.ID+" (блокер)")
		}
	}
	return waiters
}

// branchTipTime отдаёт время последнего коммита ветки в unix-секундах: возраст
// коммита это вторая точка простоя задачи после правки строки доски.
func branchTipTime(root, branch string) int64 {
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%ct", branch).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return sec
}

// mainDrift считает, насколько main ушёл от ветки задачи (LLD DK-400, решение
// 3): число печатается рядом справкой и в вес не входит, это довод в пользу
// возврата, а не слагаемое замеренной формулы.
func mainDrift(root, id string) int {
	main := gitMainBranch(root)
	if main == "" {
		return 0
	}
	br, _ := unmergedTaskBranch(root, id)
	if br == "" {
		return 0
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", main, "^"+br).Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// rowIdleHours считает часы простоя строки: возраст последней правки строки
// доски, максимум с возрастом последнего коммита ветки задачи. Возраст строки
// честен только на чистой доске: blame берёт номера строк из HEAD, на грязном
// дереве время досталось бы не той строке. Ноль значит «посчитать нечем», и
// плата за возврат от него не растет.
func rowIdleHours(root string, r *Row, times map[int]int64, clean bool) float64 {
	h := 0.0
	if clean {
		if sec, ok := times[r.LineIdx]; ok {
			h = math.Max(h, timeNow().Sub(time.Unix(sec, 0)).Hours())
		}
	}
	if br, _ := unmergedTaskBranch(root, r.ID); br != "" {
		if sec := branchTipTime(root, br); sec > 0 {
			h = math.Max(h, timeNow().Sub(time.Unix(sec, 0)).Hours())
		}
	}
	if h < 0 {
		return 0
	}
	return h
}

// cmdSlot печатает порядок кандидатов слота: группы по полосам P сверху вниз,
// внутри полосы по score, и причину отказа по каждому отсеянному воротами.
// Ворота булевы, порядок фиксированный (LLD DK-400, решение 3), строка идёт
// в отказ по первой непройденной и ровно в одну группу, как у batch.
func cmdSlot(root string, limit int, resource string) (string, error) {
	rates, ok := slotRates[resource]
	if !ok {
		return "", fmt.Errorf("ресурс %q не известен, жду slot или quota", resource)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	busy := works.Busy(b.Prefix, home, root)
	clean := boardClean(root)
	times := boardTimes(root)
	ceiling := treeCeiling(limit)
	live := liveTrees(root)
	// Недоступность человека это припаркованный вопрос старше часа без ответа.
	humanAway := false
	for _, r := range b.Rows {
		_, _, _, _, blockSuf := splitTitle(r.Title)
		if r.Sect == SectBlocked && strings.HasPrefix(blockReason(blockSuf), "вопрос:") &&
			rowIdleHours(root, r, times, clean) > slotAwayGate.Hours() {
			humanAway = true
		}
	}

	g := &batchGroups{}
	byLink := map[string]string{}
	var picks []slotPick
	treeFull := false
	for _, r := range b.Rows {
		_, _, _, _, blockSuf := splitTitle(r.Title)
		reason := blockReason(blockSuf)
		unc := r.RParts[2]
		rate, priced := rates[r.Cost]
		switch {
		case limit < 1:
			g.add("нет квоты на пачку", r.ID)
		case len(openDeps(r, arch)) > 0:
			g.add("незакрытая предпосылка", fmt.Sprintf("%s (ждут %s)", r.ID, strings.Join(openDeps(r, arch), ", ")))
		case unc > 1:
			g.add("неопределённость выше 1", fmt.Sprintf("%s (%d)", r.ID, unc))
		case !priced:
			g.add("цена вне курса S/M/L", fmt.Sprintf("%s (%s)", r.ID, r.Cost))
		case r.Sect == SectBlocked && strings.HasPrefix(reason, "вопрос:"):
			g.add("висящий вопрос без ответа", r.ID)
		case r.Sect == SectBlocked && strings.HasPrefix(reason, "окружение:"):
			g.add("окружение задачи неготово", r.ID)
		case r.Sect == SectBlocked:
			g.add("внешний блокер", fmt.Sprintf("%s (%s)", r.ID, reason))
		case busy[r.ID]:
			g.add("дерево занято живой работой", r.ID)
		case byLink[r.Link] != "":
			g.add("ссылка та же, что у отобранной", fmt.Sprintf("%s (как у %s)", r.ID, byLink[r.Link]))
		case len(picks) == limit:
			g.add(fmt.Sprintf("потолок пачки %d исчерпан", limit), r.ID)
		case (r.Sect == SectBacklog || r.Sect == SectInProgress) &&
			!taskHasTree(root, r.ID) && live >= ceiling:
			g.add(fmt.Sprintf("потолок деревьев %d исчерпан", ceiling), r.ID)
			treeFull = true
		case acceptOf(r.Title) == acceptUser && humanAway:
			g.add("человек недоступен, приёмка вида user", r.ID)
		default:
			p, err := progressOfBoard(root, r.ID, b, arch)
			if err != nil {
				g.add("рубеж не читается", fmt.Sprintf("%s (%v)", r.ID, err))
				continue
			}
			// Плата за возврат берётся только у начатой задачи: свежей строке
			// возвращаться не к чему, её цену входа уже несёт C, и счёт цели
			// («свежая S: W = 0.8») идёт без платы.
			idle := rowIdleHours(root, r, times, clean)
			var v float64
			if p.F > progressBoard {
				v = rate * math.Min(slotReturnBase+slotReturnPerHour*idle, slotReturnCap)
			}
			w := rate*(1-p.F) + v
			picks = append(picks, slotPick{row: r, C: rate, F: p.F, mark: p.Mark, V: v, W: w,
				score: float64(r.RTotal) / w, idle: idle, drift: mainDrift(root, r.ID)})
			if r.Link != "-" {
				byLink[r.Link] = r.ID
			}
		}
	}

	// Полосы сверху вниз, внутри полосы по score: дешёвый P3 не выдергивает
	// наверх P0, это инвариант плюс-минус одной полосы.
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].row.P != picks[j].row.P {
			return picks[i].row.P < picks[j].row.P
		}
		return picks[i].score > picks[j].score
	})

	var out []string
	if len(picks) == 0 {
		out = append(out, "slot: -")
	} else {
		// Виток берёт первую прошедшую из верхней непустой полосы.
		out = append(out, "slot: "+picks[0].row.ID)
	}
	n := 1
	for i, p := range picks {
		if i == 0 || p.row.P != picks[i-1].row.P {
			out = append(out, p.row.P+":")
		}
		line := fmt.Sprintf("  %d. %s (%s, R %d): score %.1f, W %.2f (C %.2f, F %.2f «%s», V %.2f)",
			n, p.row.ID, p.row.Cost, p.row.RTotal, p.score, p.W, p.C, p.F, p.mark, p.V)
		if p.V > 0 {
			line += fmt.Sprintf(", простой %.1f ч", p.idle)
		}
		if p.drift > 0 {
			line += fmt.Sprintf(", main ушёл на %d", p.drift)
		}
		out = append(out, line)
		n++
	}
	if len(g.order) > 0 {
		out = append(out, "отказы:")
		for _, label := range g.order {
			out = append(out, "  "+label+": "+strings.Join(g.items[label], ", "))
		}
	}
	// Сработавший потолок деревьев называет и то, что разобрать: без этого
	// отказ жмёт старты, не показывая, чем их освободить.
	if treeFull {
		if waiters := waitingTrees(root, b); len(waiters) > 0 {
			out = append(out, "ждут разбора: "+strings.Join(waiters, ", "))
		}
	}
	return strings.Join(out, "\n"), nil
}

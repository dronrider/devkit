package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/attime"
	"github.com/dronrider/devkit/internal/taskform"
	"github.com/dronrider/devkit/internal/works"
)

// Взвод строки (LLD DK-933, решения 1, 3, 5 и 6). Взвод это согласие человека на
// то, что строка стартует сама, как только с неё сняты все рёбра «после».
// Хранится согласие суффиксом «[взвод]» в заголовке, сразу за «[после ...]» и
// до «[приёмка: ...]»: это такое же постоянное свойство строки, как ребро.
// Часовая форма «[взвод: <время>]» добавляет к согласию срок: раньше
// назначенного часа обход строку не поднимает.
// Состояние взведённой строки не хранится нигде, его считают по требованию
// `list`, `show` и хвост обхода ждущих: рёбра снимает та же функция, что у
// ворот старта, а отказ ворот ёмкости лежит файлом в .devkit/arm.

// armSufRe разбирает суффикс «[взвод]» и его часовую форму «[взвод: 2026-09-12
// 02:00]» (решение 6). Час лежит в суффиксе абсолютным местным временем без
// секунд: короткую форму «02:00» arm превращает в ближайший такой момент до
// записи, и на доске стоит время, не зависящее от минуты чтения.
var armSufRe = regexp.MustCompile(`\s*\[взвод(?:: ([^\]]+))?\]\s*$`)

// armSuffix это сам суффикс с ведущим пробелом, как он ложится в заголовок.
const armSuffix = " [взвод]"

// armAtLayout это запись часа в суффиксе и в печати.
const armAtLayout = "2006-01-02 15:04"

// armSuffixAt собирает суффикс с часовым условием.
func armSuffixAt(at time.Time) string {
	return " [взвод: " + at.Format(armAtLayout) + "]"
}

// armHour это час взвода строки. Пустой суффикс и суффикс без часа дают нулевое
// время, а час, который не разбирается, даёт ошибку: такой час в суффиксе
// оказывается только правкой заголовка руками, и обход с печатью зовут её
// словами, а не молчанием.
func armHour(title string) (time.Time, bool, error) {
	_, _, _, armSuf, _, _, _, _, _, _, _ := splitTitle(title)
	if armSuf == "" {
		return time.Time{}, false, nil
	}
	m := armSufRe.FindStringSubmatch(armSuf)
	if m == nil || m[1] == "" {
		return time.Time{}, false, nil
	}
	at, err := time.ParseInLocation(armAtLayout, m[1], time.Local)
	if err != nil {
		return time.Time{}, true, fmt.Errorf("час взвода %q не разобран, жду 2026-09-12 02:00", m[1])
	}
	return at, true, nil
}

// classArm это вид ждущих «взвод» в хвосте обхода (решение 5): строка Backlog,
// которой человек разрешил стартовать самой.
const classArm = "взвод"

// armDefaultLimit это потолок пачки, когда `agentctl budget` не позвать. Число
// то же, каким отвечает сам agentctl без снимка квоты: обход не должен ни
// вставать намертво от пропавшего бинаря, ни поднимать головы без счёта.
const armDefaultLimit = 3

// armed говорит, взведена ли строка.
func armed(title string) bool {
	_, _, _, armSuf, _, _, _, _, _, _, _ := splitTitle(title)
	return armSuf != ""
}

// armDir это каталог файлов отказа .devkit/arm проекта.
func armDir(root string) string { return filepath.Join(root, ".devkit", "arm") }

// armRefusal это последний отказ ворот ёмкости: время и причина.
type armRefusal struct {
	When time.Time
	Why  string
}

// armRefusalTime это формат времени в файле отказа: тот же, что в журнале
// .devkit/log, и печать берёт из него часы с минутами.
const armRefusalTime = "2006-01-02T15:04:05"

// readArmRefusal читает файл отказа. Пустой ответ значит, что прошлый обход
// строке не отказывал либо файл не прочитался.
func readArmRefusal(root, id string) (armRefusal, bool) {
	data, err := os.ReadFile(filepath.Join(armDir(root), id))
	if err != nil {
		return armRefusal{}, false
	}
	parts := strings.SplitN(strings.TrimSpace(string(data)), "\t", 2)
	if len(parts) != 2 {
		return armRefusal{}, false
	}
	when, err := time.ParseInLocation(armRefusalTime, parts[0], time.Local)
	if err != nil {
		return armRefusal{}, false
	}
	return armRefusal{When: when, Why: parts[1]}, true
}

// writeArmRefusal кладёт причину отказа файлом. Провал записи обход не роняет:
// без файла отказ повторится следующим проходом, а строка так и так стоит.
func writeArmRefusal(root, id, why string) {
	if err := os.MkdirAll(armDir(root), 0o755); err != nil {
		return
	}
	line := timeNow().Format(armRefusalTime) + "\t" + why + "\n"
	os.WriteFile(filepath.Join(armDir(root), id), []byte(line), 0o644)
}

// dropArmRefusal стирает файл отказа: строка стартовала либо взвод снят.
func dropArmRefusal(root, id string) {
	os.Remove(filepath.Join(armDir(root), id))
}

// armGates это ворота ёмкости, посчитанные один раз на обход (решение 5).
// Считают их те же функции, что у `slot` и старта задачи, второй копии правила
// тут нет.
type armGates struct {
	root string
	busy map[string]bool
	// busyErr это сорванный опрос tmux: занятость не известна, и ворота
	// отказывают всем строкам до следующего обхода (DK-904).
	busyErr error
	limit   int
	live    int
	ceiling int
}

// batchCeiling спрашивает потолок пачки у `agentctl budget`: тот считает его по
// остатку недельных лимитов там, где живёт снимок квоты. Стенды подменяют
// функцию целиком, живому вызову LLM не требуется, ответ читается из снимка.
var batchCeiling = func(root string) (int, string) {
	bin, err := exec.LookPath("agentctl")
	if err != nil {
		return armDefaultLimit, "agentctl не нашёлся в PATH, потолок по умолчанию"
	}
	cmd := exec.Command(bin, "budget")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return armDefaultLimit, "agentctl budget отказал, потолок по умолчанию"
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "batch:"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n, "потолок пачки от agentctl budget"
			}
		}
	}
	return armDefaultLimit, "agentctl budget потолка не назвал, по умолчанию"
}

func newArmGates(root string, b *Board) *armGates {
	home, _ := os.UserHomeDir()
	limit, _ := batchCeiling(root)
	busy, err := works.Busy(b.Prefix, home, root, b.sectOf)
	return &armGates{root: root, busy: busy, busyErr: err, limit: limit,
		live: liveTrees(root, b), ceiling: treeCeiling(limit)}
}

// armStillReady repeats the read-only gates just before an automatic wake.
func armStillReady(root string, b *Board, arch *Archive, row *Row) error {
	if row.Sect != SectBacklog || !armed(row.Title) {
		return fmt.Errorf("взвод уже снят")
	}
	if goal := goalOfTask(root, row.ID); goal != "" && b.find(goal) != nil {
		return fmt.Errorf("задача передана цели %s", goal)
	}
	at, hasAt, err := armHour(row.Title)
	if err != nil {
		return err
	}
	if hasAt && timeNow().Before(at) {
		return fmt.Errorf("час взвода ещё не наступил")
	}
	if held := newEdges(root, b, arch).held(row); len(held) > 0 {
		return fmt.Errorf("предпосылки изменились: %s", edgeWords(held))
	}
	if why := newArmGates(root, b).pass(row.ID); why != "" {
		return fmt.Errorf("ворота взвода изменились: %s", why)
	}
	return nil
}

// pass отвечает словами, чем ворота ёмкости отказали строке. Пустой ответ это
// проход. Ворота выбора из `slot` (неопределённость, цена, вид user при
// недоступном человеке) тут не стоят: они служат машинному выбору среди
// кандидатов, а взведённую строку выбрал человек, когда взводил.
func (g *armGates) pass(id string) string {
	if held := taskform.HoldingForks(readTaskDoc(g.root, id)); len(held) > 0 {
		return fmt.Sprintf("открытая развилка «%s», решает человек", held[0].Name)
	}
	if g.busyErr != nil {
		return "занятость деревьев не известна, опрос tmux сорван: " + g.busyErr.Error()
	}
	if g.busy[id] {
		return "дерево занято живой работой"
	}
	if g.limit < 1 {
		return "нет квоты на пачку"
	}
	if len(g.busy) >= g.limit {
		return fmt.Sprintf("потолок пачки %d исчерпан, живых работ %d", g.limit, len(g.busy))
	}
	if !taskHasTree(g.root, id) && g.live >= g.ceiling {
		return fmt.Sprintf("потолок деревьев %d из %d", g.live, g.ceiling)
	}
	return ""
}

// readTaskDoc читает файл задачи целиком; файла нет, значит и перечня развилок
// нет, и ворота молчат.
func readTaskDoc(root, id string) string {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return ""
	}
	return string(data)
}

// armRefuse ведёт отказ ворот: причина ложится файлом, а зов человеку и строка
// журнала уходят один раз на каждую новую причину. Потолок деревьев держится
// часами, и зов на каждом тике приучил бы зов не читать. Возврат это строка
// отчёта обхода либо пусто, когда причина та же, что была.
func armRefuse(root, id, why string) string {
	if prev, ok := readArmRefusal(root, id); ok && prev.Why == why {
		return ""
	}
	writeArmRefusal(root, id, why)
	logLine(root, "arm "+id+" отказ: "+why, 1)
	note := fmt.Sprintf("%s: взвод готов, а ёмкости нет: %s", id, why)
	return note + notify(root, reasonArm, id,
		fmt.Sprintf("%s: %s ждёт ёмкости", filepath.Base(root), id), why)
}

// armSweep стирает файлы отказа, которым не осталось строки: взведённая строка
// могла стартовать, потерять взвод или пропасть с доски вовсе (решение 3).
func armSweep(root string, live map[string]bool) {
	ents, err := os.ReadDir(armDir(root))
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() && !live[e.Name()] {
			dropArmRefusal(root, e.Name())
		}
	}
}

// armedWaiters это вид ждущих «взвод» в хвосте обхода (решение 5). Строки
// Backlog со взводом идут по одному правилу с парковкой: снятость рёбер
// считает та же функция, что у ворот старта, а поднимает прошедшую ворота
// ёмкости `taskctl run`. Второй возврат это строки отчёта про новые отказы.
func armedWaiters(root string, b *Board, arch *Archive) ([]waiter, []string) {
	var out []waiter
	var notes []string
	var ed *edges
	var gates *armGates
	live := map[string]bool{}
	for _, r := range b.Rows {
		if r.Sect != SectBacklog || !armed(r.Title) {
			continue
		}
		// Строку, вошедшую в цель уже взведённой, обход пропускает: её
		// поднимает цикл цели, а находку на ней печатает lint.
		if goal := goalOfTask(root, r.ID); goal != "" && b.find(goal) != nil {
			continue
		}
		live[r.ID] = true
		// Часовое условие стоит раньше рёбер и ворот (решение 6): строка ждёт
		// назначенного часа безусловно, и звонить по ней воротам ёмкости, пока
		// он не наступил, незачем. Машина, проспавшая час, стреляет первым
		// обходом после пробуждения: условие значит «не раньше», а не «ровно в».
		at, hasAt, err := armHour(r.Title)
		if err != nil {
			out = append(out, waiter{ID: r.ID, Class: classArm, Said: err.Error()})
			continue
		}
		if hasAt && timeNow().Before(at) {
			out = append(out, waiter{ID: r.ID, Class: classArm,
				Said: "ждёт часа " + at.Format(armAtLayout)})
			continue
		}
		if ed == nil {
			ed = newEdges(root, b, arch)
		}
		if held := ed.held(r); len(held) > 0 {
			out = append(out, waiter{ID: r.ID, Class: classArm, Said: edgeWords(held)})
			continue
		}
		if gates == nil {
			gates = newArmGates(root, b)
		}
		if why := gates.pass(r.ID); why != "" {
			if note := armRefuse(root, r.ID, why); note != "" {
				notes = append(notes, note)
			}
			out = append(out, waiter{ID: r.ID, Class: classArm, Said: why})
			continue
		}
		// Ворота пропустили: прошлый отказ больше не правда, и файл уходит,
		// не дожидаясь старта строки.
		dropArmRefusal(root, r.ID)
		out = append(out, waiter{ID: r.ID, Class: classArm, Said: "рёбра сняты", Met: true})
	}
	armSweep(root, live)
	return out, notes
}

// Состояния взведённой строки для печати (решение 1). Хранить их негде и
// незачем: и рёбра, и ворота считаются на месте.
const (
	armWaits = "ждёт"
	armReady = "готова"
	// armRefused это та же готовая строка, которой прошлый обход отказал по
	// ёмкости: разряд отдельный, потому что отказ это единственное из трёх
	// состояний, где человеку есть что сделать.
	armRefused = "отказ"
	// armHand это строка без взвода, с которой сняты все рёбра: поднимает её
	// только рука.
	armHand = "рукой"
)

// armView это состояние взвода строки: разряд одним словом и вся пометка,
// какой её печатают `list` и `show`. Разряд едет в JSON рядом с пометкой,
// потому что читателю вроде дашборда нужны оба: по разряду он выбирает вид
// метки, а словами объясняет её человеку.
type armView struct {
	State string
	Note  string
	// At это назначенный час взвода тем же форматом, каким он лежит в
	// заголовке: машинному читателю он нужен для сортировки и подсветки, а
	// словами его называет пометка.
	At string
}

// armNote это пометка строки про взвод, какой её печатают `list` и `show`.
// Взведённая строка говорит, чего ждёт или что готова, и отказ последнего
// обхода называется временем и причиной. Строка без взвода, с которой сняты
// все рёбра, называется свободной: поднять её некому, кроме руки человека, и
// заглохшей бывает только такая.
func armNote(root string, ed *edges, r *Row) string {
	if v := armState(root, ed, r); v != nil {
		return v.Note
	}
	return ""
}

// armState считает состояние взвода один раз на строку: печать берёт у него
// пометку, JSON берёт и пометку, и разряд. Второго разбора заголовка тут нет
// именно поэтому: разойтись печати с машинным ответом было бы негде видно.
func armState(root string, ed *edges, r *Row) *armView {
	if r.Sect != SectBacklog {
		return nil
	}
	_, deps, _, armSuf, _, _, _, _, _, _, _ := splitTitle(r.Title)
	if armSuf == "" {
		if len(deps) == 0 || len(ed.held(r)) > 0 {
			return nil
		}
		return &armView{State: armHand, Note: "свободна, старт рукой"}
	}
	// Строка ждёт и рёбер, и назначенного часа: пометка называет оба, а разряд
	// у ожидания один, человеку всё равно чего именно ждать.
	var waits []string
	var atSay string
	if held := ed.held(r); len(held) > 0 {
		waits = append(waits, edgeWords(held))
	}
	at, hasAt, err := armHour(r.Title)
	if err != nil {
		waits = append(waits, err.Error())
	} else if hasAt {
		atSay = at.Format(armAtLayout)
		if timeNow().Before(at) {
			waits = append(waits, "час "+atSay)
		}
	}
	if len(waits) > 0 {
		return &armView{State: armWaits, Note: "взвод: " + armWaits + ", " + strings.Join(waits, ", "), At: atSay}
	}
	if ref, ok := readArmRefusal(root, r.ID); ok {
		return &armView{State: armRefused, At: atSay, Note: fmt.Sprintf("взвод: %s, отказ %s, %s",
			armReady, ref.When.Format("15:04"), ref.Why)}
	}
	return &armView{State: armReady, Note: "взвод: " + armReady, At: atSay}
}

// cmdArm ставит и снимает взвод. Взводится только строка Backlog, и только та,
// которую без человека исполнителю отдать можно: открытая человеческая
// развилка, грумминговый вердикт (неопределённость 4-5 либо цена XL) и состав
// незакрытой цели взвод не получают (решение 1). Аргумент --at ставит часовое
// условие (решение 6): строка стартует не раньше назначенного местного часа.
func cmdArm(root, id, at string, off bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if off && at != "" {
		return "", fmt.Errorf("--at не сочетается с --off: снятие взвода убирает и час, ставьте взвод заново")
	}
	if err := boardGuard(root, "arm"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	if err := needTaskFile(root, id); err != nil {
		return "", err
	}
	base, deps, debtSuf, armSuf, planSuf, assignSuf, dueSuf, controlSuf, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
	if off {
		if armSuf == "" {
			return "", fmt.Errorf("%s и так без взвода", id)
		}
		armSuf = ""
	} else {
		if armSuf != "" {
			return "", fmt.Errorf("%s уже взведена", id)
		}
		if err := armReadiness(root, b, row); err != nil {
			return "", err
		}
		// Короткая форма «02:00» разбирается общим с `agentctl wait час`
		// пакетом attime и ложится на доску абсолютным временем (решение 6).
		if at != "" {
			t, err := attime.Parse(at, timeNow())
			if err != nil {
				return "", fmt.Errorf("--at: %v", err)
			}
			armSuf = armSuffixAt(t)
		} else {
			armSuf = armSuffix
		}
	}
	row.Title = joinTitle(base, deps, debtSuf, armSuf, planSuf, assignSuf, dueSuf, controlSuf, acceptSuf, failSuf, blockSuf)
	var ed *edges
	if !off {
		arch, err := LoadArchive(archivePath(root))
		if err != nil {
			return "", err
		}
		ed, err = newEdgesChecked(root, b, arch)
		if err != nil {
			return "", err
		}
	}
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	if off {
		dropArmRefusal(root, id)
		return fmt.Sprintf("%s: взвод снят, старт рукой%s", id, tail), nil
	}
	hourAt, hasAt, err := armHour(row.Title)
	hourWord := ""
	if err == nil && hasAt && timeNow().Before(hourAt) {
		hourWord = hourAt.Format(armAtLayout)
	}
	held := ed.held(row)
	switch {
	case len(held) > 0 && hourWord != "":
		return fmt.Sprintf("%s: взвод стоит, ждёт %s, час %s%s", id, edgeWords(held), hourWord, tail), nil
	case len(held) > 0:
		return fmt.Sprintf("%s: взвод стоит, ждёт %s%s", id, edgeWords(held), tail), nil
	case hourWord != "":
		return fmt.Sprintf("%s: взвод стоит, рёбра сняты, ждёт часа %s%s", id, hourWord, tail), nil
	}
	return fmt.Sprintf("%s: взвод стоит, рёбра сняты, поднимет ближайший обход%s", id, tail), nil
}

// armReadiness это готовность строки к самостоятельному старту. Отказ называет
// и то, чем он снимается: взвод ставят один раз, а причина отказа живёт в
// строке или в файле задачи, и искать её глазами не надо.
func armReadiness(root string, b *Board, row *Row) error {
	if row.Sect != SectBacklog {
		return fmt.Errorf("%s в %s, взводится только строка Backlog: взвод это согласие на старт, а эта уже начата",
			row.ID, sectTitles[row.Sect])
	}
	if goalRow(row.Title) {
		return fmt.Errorf("%s это цель, её строку поднимает свой цикл: взвод делил бы с ним бюджет и ответственность", row.ID)
	}
	doc := readTaskDoc(root, row.ID)
	if held := taskform.HoldingForks(doc); len(held) > 0 {
		return fmt.Errorf("%s", taskform.ForkGateNote(row.ID, "взвод", held))
	}
	if unc := row.RParts[2]; unc > 3 {
		return fmt.Errorf("%s с неопределённостью %d: такую строку сперва грумят, а не отдают исполнителю (RANKING.md, «Готовность к исполнению»)", row.ID, unc)
	}
	if row.Cost == "XL" {
		return fmt.Errorf("%s ценой XL: такую строку сперва режут, а не отдают исполнителю (RANKING.md, «Готовность к исполнению»)", row.ID)
	}
	if goal := goalOfTask(root, row.ID); goal != "" && b.find(goal) != nil {
		return fmt.Errorf("%s из состава незакрытой цели %s: её строки поднимает цикл цели, и второй источник запуска делил бы с ним бюджет и ответственность", row.ID, goal)
	}
	return nil
}

// goalOfTask это ID цели, в состав которой входит задача, по строке «Цель: ...»
// шапки её файла. Пусто, когда строки нет или файла цели нет на месте.
func goalOfTask(root, id string) string {
	rel := taskGoal(root, id)
	if rel == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(rel), ".md")
}

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

	"github.com/dronrider/devkit/internal/taskform"
	"github.com/dronrider/devkit/internal/works"
)

// Взвод строки (LLD DK-933, решения 1, 3 и 5). Взвод это согласие человека на
// то, что строка стартует сама, как только с неё сняты все рёбра «после».
// Хранится согласие суффиксом «[взвод]» в заголовке, сразу за «[после ...]» и
// до «[приёмка: ...]»: это такое же постоянное свойство строки, как ребро.
// Состояние взведённой строки не хранится нигде, его считают по требованию
// `list`, `show` и хвост обхода ждущих: рёбра снимает та же функция, что у
// ворот старта, а отказ ворот ёмкости лежит файлом в .devkit/arm.

// armSufRe разбирает суффикс «[взвод]». Аргумент часа (решение 6, строка
// DK-538) встанет сюда же, и до него суффикс идёт без содержимого.
var armSufRe = regexp.MustCompile(`\s*\[взвод\]\s*$`)

// armSuffix это сам суффикс с ведущим пробелом, как он ложится в заголовок.
const armSuffix = " [взвод]"

// classArm это вид ждущих «взвод» в хвосте обхода (решение 5): строка Backlog,
// которой человек разрешил стартовать самой.
const classArm = "взвод"

// armDefaultLimit это потолок пачки, когда `agentctl budget` не позвать. Число
// то же, каким отвечает сам agentctl без снимка квоты: обход не должен ни
// вставать намертво от пропавшего бинаря, ни поднимать головы без счёта.
const armDefaultLimit = 3

// armed говорит, взведена ли строка.
func armed(title string) bool {
	_, _, armSuf, _, _, _ := splitTitle(title)
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
	root    string
	busy    map[string]bool
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
	return &armGates{root: root, busy: works.Busy(b.Prefix, home, root, b.sectOf), limit: limit,
		live: liveTrees(root), ceiling: treeCeiling(limit)}
}

// pass отвечает словами, чем ворота ёмкости отказали строке. Пустой ответ это
// проход. Ворота выбора из `slot` (неопределённость, цена, вид user при
// недоступном человеке) тут не стоят: они служат машинному выбору среди
// кандидатов, а взведённую строку выбрал человек, когда взводил.
func (g *armGates) pass(id string) string {
	if held := taskform.HoldingForks(readTaskDoc(g.root, id)); len(held) > 0 {
		return fmt.Sprintf("открытая развилка «%s», решает человек", held[0].Name)
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
)

// armNote это пометка строки про взвод, какой её печатают `list` и `show`.
// Взведённая строка говорит, чего ждёт или что готова, и отказ последнего
// обхода называется временем и причиной. Строка без взвода, с которой сняты
// все рёбра, называется свободной: поднять её некому, кроме руки человека, и
// заглохшей бывает только такая.
func armNote(root string, ed *edges, r *Row) string {
	if r.Sect != SectBacklog {
		return ""
	}
	_, deps, armSuf, _, _, _ := splitTitle(r.Title)
	if armSuf == "" {
		if len(deps) == 0 || len(ed.held(r)) > 0 {
			return ""
		}
		return "свободна, старт рукой"
	}
	if held := ed.held(r); len(held) > 0 {
		return "взвод: " + armWaits + ", " + edgeWords(held)
	}
	note := "взвод: " + armReady
	if ref, ok := readArmRefusal(root, r.ID); ok {
		note += fmt.Sprintf(", отказ %s, %s", ref.When.Format("15:04"), ref.Why)
	}
	return note
}

// cmdArm ставит и снимает взвод. Взводится только строка Backlog, и только та,
// которую без человека исполнителю отдать можно: открытая человеческая
// развилка, грумминговый вердикт (неопределённость 4-5 либо цена XL) и состав
// незакрытой цели взвод не получают (решение 1).
func cmdArm(root, id string, off bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
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
	base, deps, armSuf, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
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
		armSuf = armSuffix
	}
	row.Title = joinTitle(base, deps, armSuf, acceptSuf, failSuf, blockSuf)
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
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	held := newEdges(root, b, arch).held(row)
	if len(held) > 0 {
		return fmt.Sprintf("%s: взвод стоит, ждёт %s%s", id, edgeWords(held), tail), nil
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

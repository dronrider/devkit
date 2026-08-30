package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Поправки к рангу (задача DK-428). Пять слагаемых разбивки это собственная
// оценка задачи, а хвост скобки после них это список поправок, которые
// считаются из строки и доски, а руками не пишутся. Видов поправок два.
// Аддитивная прибавляет или отнимает («S+2» это бонус за дешевизну из
// RANKING.md), подтягивающая переносит ранг зависимой на предпосылку по
// инварианту зависимости и пишется «от <ID>».
//
// Порядок счёта фиксирован:
//
//	own   = сумма пяти слагаемых
//	base  = clamp(own + сумма аддитивных, 0, 100)
//	R_eff = max(base, R_eff незакрытых задач, которые эта строка держит)
//
// Рёбер наследования два источника: суффикс «[после ...]» зависимой строки и
// раздел «Задачи цели» файла незакрытой цели. Оба дают одно ребро «эта задача
// держит ту», и считает их одна функция.

const (
	adjAdditive = "аддитивная"
	adjPull     = "подтягивающая"
)

// pullName это имя подтягивающей поправки в хвосте строки: «от DK-473».
const pullName = "от"

// Adjustment это поправка в хвосте скобки. У аддитивной заполнены Name и
// Delta, у подтягивающей From.
type Adjustment struct {
	Name  string
	Delta int
	From  string
}

func (a Adjustment) kind() string {
	if a.From != "" {
		return adjPull
	}
	return adjAdditive
}

func (a Adjustment) String() string {
	if a.From != "" {
		return pullName + " " + a.From
	}
	if a.Delta < 0 {
		return fmt.Sprintf("%s%d", a.Name, a.Delta)
	}
	return fmt.Sprintf("%s+%d", a.Name, a.Delta)
}

// adjRule это запись таблицы поправок: имя в хвосте, вид и функция счёта из
// строки. Новая надбавка или штраф это запись тут и тест, остальные команды
// (list, sort, slot, --json, lint) ходят через таблицу и правки не требуют.
type adjRule struct {
	Name string
	Kind string
	Calc func(r *Row) int
}

// costBonus это бонус за дешевизну из RANKING.md: S +2, M +1, L и XL 0.
func costBonus(cost string, delta int) func(*Row) int {
	return func(r *Row) int {
		if r.Cost == cost {
			return delta
		}
		return 0
	}
}

var adjTable = []adjRule{
	{Name: "S", Kind: adjAdditive, Calc: costBonus("S", 2)},
	{Name: "M", Kind: adjAdditive, Calc: costBonus("M", 1)},
}

// knownAdj говорит, знает ли таблица имя поправки. Незнакомое имя в хвосте
// это находка lint: хвост производный, и руками в него дописывают либо
// опечатку, либо поправку, которой в утилите нет.
func knownAdj(name string) bool {
	if name == pullName {
		return true
	}
	for _, rule := range adjTable {
		if rule.Name == name {
			return true
		}
	}
	return false
}

var (
	// rankCellRe разбирает ячейку R целиком: итог, пять слагаемых и хвост
	// поправок через запятую.
	rankCellRe = regexp.MustCompile(`^([0-9]+) \(([0-9]+)\+([0-9]+)\+([0-9]+)\+([0-9]+)\+([0-9]+)((?:, [^),]+)*)\)$`)
	addAdjRe   = regexp.MustCompile(`^([^+\-0-9]+)([+-])([0-9]+)$`)
	pullAdjRe  = regexp.MustCompile(`^от ([A-ZА-Я]+-[0-9]+)$`)
)

// parseAdjTail разбирает хвост скобки. Грамматику держит парсер, список имён
// таблица: имя не из таблицы разбирается и доезжает до lint, а сломанная
// грамматика это ошибка разбора строки.
func parseAdjTail(tail string) ([]Adjustment, error) {
	var out []Adjustment
	for _, item := range strings.Split(tail, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if m := pullAdjRe.FindStringSubmatch(item); m != nil {
			out = append(out, Adjustment{From: m[1]})
			continue
		}
		if m := addAdjRe.FindStringSubmatch(item); m != nil {
			n, _ := strconv.Atoi(m[3])
			if m[2] == "-" {
				n = -n
			}
			out = append(out, Adjustment{Name: m[1], Delta: n})
			continue
		}
		return nil, fmt.Errorf("поправка %q не разобрана, жду «имя+N», «имя-N» или «от ID»", item)
	}
	return out, nil
}

// formatAdjTail собирает хвост обратно; пустой список хвоста не даёт.
func formatAdjTail(adjs []Adjustment) string {
	if len(adjs) == 0 {
		return ""
	}
	var parts []string
	for _, a := range adjs {
		parts = append(parts, a.String())
	}
	return ", " + strings.Join(parts, ", ")
}

// rankCell собирает ячейку R из посчитанных полей строки.
func rankCell(r *Row) string {
	return fmt.Sprintf("%d (%d+%d+%d+%d+%d%s)",
		r.RTotal, r.RParts[0], r.RParts[1], r.RParts[2], r.RParts[3], r.RParts[4],
		formatAdjTail(r.RAdj))
}

func clampRank(v int) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// goalTasksHeading это раздел файла цели, из которого читается её состав.
const goalTasksHeading = "## Задачи цели"

var taskIDRe = regexp.MustCompile(`[A-ZА-Я]+-[0-9]+`)

// goalTaskIDs читает состав цели из раздела «Задачи цели» её файла. Ссылка на
// сам файл цели в списке не считается: задача не держит сама себя.
func goalTaskIDs(root, goalID string) []string {
	text, found, ok := readSectionFromPath(taskFilePath(root, goalID), goalTasksHeading)
	if !ok || !found {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range taskIDRe.FindAllString(text, -1) {
		if id == goalID || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// holdEdges строит рёбра наследования: holds[X] это задачи, которые держит X,
// то есть те, чей ранг X наследует. Ребро дают суффикс «[после X]» зависимой
// строки и состав незакрытой цели.
func holdEdges(root string, b *Board) map[string][]string {
	holds := map[string][]string{}
	add := func(holder, held string) {
		if holder == held || b.find(holder) == nil {
			return
		}
		for _, ex := range holds[holder] {
			if ex == held {
				return
			}
		}
		holds[holder] = append(holds[holder], held)
	}
	for _, r := range b.Rows {
		_, deps, _, _, _ := splitTitle(r.Title)
		for _, d := range deps {
			add(d, r.ID)
		}
		if goalRow(r.Title) {
			for _, t := range goalTaskIDs(root, r.ID) {
				add(t, r.ID)
			}
		}
	}
	return holds
}

// rootOfBoard достаёт корень проекта из пути доски: доска лежит в docs/ этого
// корня, а состав целей читается из docs/tasks/ рядом.
func rootOfBoard(path string) string {
	return filepath.Dir(filepath.Dir(path))
}

// computeRanks считает поправки и итоговый R всем строкам доски. Собственная
// сумма остаётся в ROwn, база с аддитивными в RBase, итог в RTotal, а список
// поправок в RAdj в порядке печати: сначала аддитивные по таблице, потом
// подтягивающая.
func computeRanks(b *Board) {
	base := map[string]int{}
	for _, r := range b.Rows {
		own := 0
		for _, v := range r.RParts {
			own += v
		}
		r.ROwn = own
		r.RAdj = nil
		add := 0
		for _, rule := range adjTable {
			if d := rule.Calc(r); d != 0 {
				r.RAdj = append(r.RAdj, Adjustment{Name: rule.Name, Delta: d})
				add += d
			}
		}
		r.RBase = clampRank(own + add)
		base[r.ID] = r.RBase
	}
	holds := holdEdges(rootOfBoard(b.Path), b)
	type memo struct {
		val    int
		origin string
	}
	done := map[string]memo{}
	busy := map[string]bool{}
	// Граф ацикличен: цикл отсекает dep add, а найденный на доске ловит lint.
	// Серая вершина тут страховка от зацикливания разбора, а не рабочий путь.
	var eff func(id string) memo
	eff = func(id string) memo {
		if m, ok := done[id]; ok {
			return m
		}
		if busy[id] {
			return memo{base[id], id}
		}
		busy[id] = true
		best := memo{base[id], id}
		for _, held := range holds[id] {
			if m := eff(held); m.val > best.val {
				best = m
			}
		}
		busy[id] = false
		done[id] = best
		return best
	}
	for _, r := range b.Rows {
		m := eff(r.ID)
		r.RTotal = m.val
		if m.origin != r.ID {
			r.RAdj = append(r.RAdj, Adjustment{From: m.origin})
		}
	}
}

// backlogOrder отдаёт строки Backlog в положенном порядке: по R_eff вниз, при
// равном R_eff предпосылка раньше зависимой, дальше по номеру. Порядок внутри
// полосы равных считается обходом рёбер наследования, а не парным сравнением:
// «раньше» тут отношение по графу, и сортировка парами разъезжается на
// цепочке из трёх.
func backlogOrder(root string, b *Board) []*Row {
	rows := append([]*Row{}, b.Sects[SectBacklog].Rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].RTotal > rows[j].RTotal })
	holds := holdEdges(root, b)
	var out []*Row
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].RTotal == rows[i].RTotal {
			j++
		}
		out = append(out, orderTier(rows[i:j], holds)...)
		i = j
	}
	return out
}

// orderTier раскладывает полосу равного R_eff: сначала те, кого никто из
// полосы не держит, среди готовых берётся меньший номер.
func orderTier(tier []*Row, holds map[string][]string) []*Row {
	in := map[string]*Row{}
	for _, r := range tier {
		in[r.ID] = r
	}
	deg := map[string]int{}
	for _, r := range tier {
		for _, held := range holds[r.ID] {
			if _, ok := in[held]; ok {
				deg[held]++
			}
		}
	}
	rest := append([]*Row{}, tier...)
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Num < rest[j].Num })
	var out []*Row
	for len(rest) > 0 {
		pick := -1
		for i, r := range rest {
			if deg[r.ID] == 0 {
				pick = i
				break
			}
		}
		if pick == -1 {
			pick = 0 // цикл внутри полосы: порядок по номеру, находку даст lint
		}
		r := rest[pick]
		rest = append(rest[:pick], rest[pick+1:]...)
		out = append(out, r)
		for _, held := range holds[r.ID] {
			if _, ok := in[held]; ok {
				deg[held]--
			}
		}
	}
	return out
}

// rehydrate переписывает ячейки R и P по пересчитанным поправкам и
// переставляет Backlog. Возвращает переезды строками вида
// «DK-471: 35 -> 61 от DK-473» и разобранную заново доску: перестановка
// двигает строки, и старые индексы после неё врут.
func rehydrate(b *Board) ([]string, *Board, error) {
	// Разбор с нуля: вызывающая команда уже вставила, убрала или переписала
	// строку, и в её разборе индексы строк после правки врут.
	b, err := parseLines(b.Path, b.Lines)
	if err != nil {
		return nil, nil, err
	}
	var moves []string
	for _, r := range b.Rows {
		want := rankCell(r)
		if want != r.RCell {
			moves = append(moves, fmt.Sprintf("%s: %s", r.ID, moveWords(r)))
		}
		r.RCell = want
		r.P = bucket(r.RTotal)
		b.Lines[r.LineIdx] = formatRow(r)
	}
	sec := b.Sects[SectBacklog]
	idxs := make([]int, len(sec.Rows))
	for i, r := range sec.Rows {
		idxs[i] = r.LineIdx
	}
	sorted := backlogOrder(rootOfBoard(b.Path), b)
	contents := make([]string, len(sorted))
	for i, r := range sorted {
		contents[i] = b.Lines[r.LineIdx]
	}
	for i := range idxs {
		b.Lines[idxs[i]] = contents[i]
	}
	nb, err := parseLines(b.Path, b.Lines)
	if err != nil {
		return nil, nil, err
	}
	return moves, nb, nil
}

// moveWords описывает переезд одной строки: старый итог, новый и от кого он
// подтянут.
func moveWords(r *Row) string {
	old := 0
	if m := rankCellRe.FindStringSubmatch(r.RCell); m != nil {
		old, _ = strconv.Atoi(m[1])
	}
	s := fmt.Sprintf("%d -> %d", old, r.RTotal)
	for _, a := range r.RAdj {
		if a.From != "" {
			s += " " + pullName + " " + a.From
		}
	}
	return s
}

// movesTail верстает переезды строк для вывода команды: каждый со своей
// строки, пустой список хвоста не даёт.
func movesTail(moves []string) string {
	if len(moves) == 0 {
		return ""
	}
	return "\n" + strings.Join(moves, "\n")
}

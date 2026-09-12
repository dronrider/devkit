package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

var linkRe = regexp.MustCompile(`\]\(([^)]+)\)`)

// cmdLint проверяет инварианты доски и архива. Жёсткие ошибки формата ловит
// уже разбор, здесь семантика: бакеты, сортировка, дубли, живость ссылок.
func cmdLint(root string) ([]string, error) {
	var finds []string
	bp, ap := boardPath(root), archivePath(root)
	b, err := LoadBoard(bp)
	if err != nil {
		return nil, err
	}
	arch, err := LoadArchive(ap)
	if err != nil {
		return nil, err
	}
	if b.Legacy {
		finds = append(finds, fmt.Sprintf("%s: доска без колонки «Цена», перевести в новый формат: taskctl sort", bp))
	}

	seen := map[string]string{}
	note := func(id, where string) {
		if prev, ok := seen[id]; ok {
			finds = append(finds, fmt.Sprintf("%s: дубль ID %s (уже есть: %s)", where, id, prev))
			return
		}
		seen[id] = where
	}
	for _, r := range b.Rows {
		note(r.ID, fmt.Sprintf("%s:%d", bp, r.LineIdx+1))
	}
	for _, r := range arch.Rows {
		note(r.ID, fmt.Sprintf("%s:%d", ap, r.LineIdx+1))
	}

	for _, r := range b.Rows {
		where := fmt.Sprintf("%s:%d: %s", bp, r.LineIdx+1, r.ID)
		if want := bucket(r.RTotal); r.P != want {
			finds = append(finds, fmt.Sprintf("%s: P=%s, а по R=%d должно быть %s", where, r.P, r.RTotal, want))
		}
		rank := fmt.Sprintf("%d+%d+%d+%d+%d", r.RParts[0], r.RParts[1], r.RParts[2], r.RParts[3], r.RParts[4])
		if _, _, err := parseRank(rank); err != nil {
			finds = append(finds, fmt.Sprintf("%s: разбивка ранга вне шкалы: %v", where, err))
		}
		finds = append(finds, lintRankCell(r, where)...)
		finds = append(finds, checkLinks(root, bp, r.LineIdx, b.Lines[r.LineIdx])...)
	}

	rows := b.Sects[SectBacklog].Rows
	want := backlogOrder(root, b)
	for i := range rows {
		if rows[i].ID != want[i].ID {
			finds = append(finds, fmt.Sprintf("%s:%d: Backlog не отсортирован: тут ждали %s (R=%d), а стоит %s (R=%d)",
				bp, rows[i].LineIdx+1, want[i].ID, want[i].RTotal, rows[i].ID, rows[i].RTotal))
			break
		}
	}

	// Взятая в работу задача ведётся файлом (RULES.board.md, «Трекинг задач»
	// п. 3): ход работы и раздел «Ревью» жить в строке доски не могут. В Check
	// без файла и со ссылкой-прочерком проверяющему некуда смотреть, а перевод
	// туда требует готового сценария проверки (п. 6).
	taskFile := func(id string) bool {
		_, err := os.Stat(filepath.Join(root, "docs", "tasks", id+".md"))
		return err == nil
	}
	for _, r := range b.Sects[SectInProgress].Rows {
		if !taskFile(r.ID) {
			finds = append(finds, fmt.Sprintf("%s:%d: %s в работе без файла задачи, завести: taskctl file %s",
				bp, r.LineIdx+1, r.ID, r.ID))
		}
	}
	for _, r := range b.Sects[SectCheck].Rows {
		if r.Link == "-" && !taskFile(r.ID) {
			finds = append(finds, fmt.Sprintf("%s:%d: %s в Check без файла задачи и без ссылки на сценарий проверки",
				bp, r.LineIdx+1, r.ID))
		}
	}
	finds = append(finds, lintUnmerged(root, b, bp)...)
	finds = append(finds, lintOrphanTaskFiles(root, b, arch, bp)...)

	for _, r := range arch.Rows {
		where := fmt.Sprintf("%s:%d: %s", ap, r.LineIdx+1, r.ID)
		if !dateRe.MatchString(r.Cells[4]) {
			finds = append(finds, fmt.Sprintf("%s: дата закрытия %q не вида ГГГГ-ММ-ДД", where, r.Cells[4]))
		}
		finds = append(finds, checkLinks(root, ap, r.LineIdx, arch.Lines[r.LineIdx])...)
	}
	finds = append(finds, lintDeps(root, b, arch, bp)...)
	finds = append(finds, lintFailed(b, bp)...)
	finds = append(finds, lintArmed(root, b, bp)...)
	finds = append(finds, lintAcceptance(root, b, bp)...)
	finds = append(finds, lintFormOrder(root, b)...)
	finds = append(finds, lintForks(root, b)...)
	finds = append(finds, mainAheadFinds(root)...)
	return finds, nil
}

// lintAcceptance ловит ошибку назначения вида в дорогую сторону (LLD DK-292,
// решение 6): у строки с видом user или mixed в файле задачи обязан стоять
// раздел «Приёмка» с ключом барьера из шести, иначе задача простояла в очереди
// без названной причины. Счёт обходов воротам move check (решение 4): на
// свежем скелете, который заводит add, обходов ноль, и проверка их числа в lint
// тут шумит, не ловя ошибку назначения.
func lintAcceptance(root string, b *Board, bp string) []string {
	var finds []string
	for _, r := range b.Rows {
		kind := acceptOf(r.Title)
		if kind == acceptAgent {
			continue
		}
		text, found, ok := acceptanceSection(root, r.ID)
		if !ok {
			finds = append(finds, fmt.Sprintf("%s:%d: %s вид %s, а файла задачи нет: виду с барьером нужен раздел «Приёмка» (taskctl file %s)",
				bp, r.LineIdx+1, r.ID, kind, r.ID))
			continue
		}
		if !found {
			finds = append(finds, fmt.Sprintf("%s:%d: %s вид %s без раздела «Приёмка» в файле задачи: назвать барьер и перебрать обходы",
				bp, r.LineIdx+1, r.ID, kind))
			continue
		}
		barrier, _ := parseAcceptance(text)
		if barrier == "" {
			finds = append(finds, fmt.Sprintf("%s:%d: %s вид %s, а в «Приёмка» нет строки «- барьер «<ключ>»:»",
				bp, r.LineIdx+1, r.ID, kind))
			continue
		}
		if _, known := acceptBarriers[barrier]; !known {
			finds = append(finds, fmt.Sprintf("%s:%d: %s барьер «%s» не из шести",
				bp, r.LineIdx+1, r.ID, barrier))
		}
	}
	return finds
}

// lintFormOrder ловит разделы файла задачи, вставшие не в том порядке
// (TASKFORM.md): читатель со свежим контекстом ищет разбор корня в начале, а
// вывод прогона в конце, и переставленный раздел он находит чтением файла
// целиком. Считаются только разделы формы, свои заголовки задачи проверка не
// трогает и порядок между ними не судит.
func lintFormOrder(root string, b *Board) []string {
	var finds []string
	for _, r := range b.Rows {
		path := taskFilePath(root, r.ID)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		mask, _ := stage.FenceMask(lines)
		prev, prevLine := -1, ""
		for i, ln := range lines {
			if mask[i] || !strings.HasPrefix(ln, "## ") {
				continue
			}
			rank := formRank(ln)
			if rank < 0 {
				continue
			}
			if rank < prev {
				finds = append(finds, fmt.Sprintf("%s:%d: раздел «%s» стоит после «%s», порядок разделов в %s",
					filepath.Join("docs", "tasks", r.ID+".md"), i+1,
					strings.TrimPrefix(ln, "## "), strings.TrimPrefix(prevLine, "## "), formDoc))
				break
			}
			prev, prevLine = rank, ln
		}
	}
	return finds
}

// lintForks судит перечень развилок записей той же формой, какой его читают
// ворота (LLD DK-552, решение 1): подстрока, начатая словом формы и не
// подошедшая под неё, повтор имени и строка «оставлена» при «решает: человек».
// Правка раздела руками законна, и сторож тут вместо отказа: перечень с
// опечаткой формы читается воротами не так, как его читал писавший.
func lintForks(root string, b *Board) []string {
	var finds []string
	paths := map[string]string{}
	for _, r := range b.Rows {
		paths[taskFilePath(root, r.ID)] = filepath.Join("docs", "tasks", r.ID+".md")
	}
	drafts, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "drafts", "*.md"))
	for _, d := range drafts {
		paths[d] = filepath.Join("docs", "tasks", "drafts", filepath.Base(d))
	}
	var names []string
	for abs := range paths {
		names = append(names, abs)
	}
	sort.Strings(names)
	for _, abs := range names {
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, f := range taskform.ForkFinds(string(data)) {
			finds = append(finds, fmt.Sprintf("%s:%d: %s", paths[abs], f.Line, f.Text))
		}
	}
	return finds
}

// lintFailed ловит признак провала проверки там, где его быть не может.
// Провал это сломанный прод у задачи, вернувшейся в работу: в Check его гасит
// сам move, а строка в Backlog с такой пометкой значит, что задачу отложили
// вместе со сломанным продом, и очередь выката стоит непонятно из-за чего.
func lintFailed(b *Board, bp string) []string {
	var finds []string
	for _, key := range []string{SectBacklog, SectCheck} {
		for _, r := range b.Sects[key].Rows {
			if _, _, _, _, failSuf, _ := splitTitle(r.Title); failSuf != "" {
				finds = append(finds, fmt.Sprintf("%s:%d: %s в %s с признаком провала проверки%s: он ставится задаче в работе, снять: taskctl fail %s --clear",
					bp, r.LineIdx+1, r.ID, sectTitles[key], failSuf, r.ID))
			}
		}
	}
	return finds
}

// lintArmed ловит взвод там, где он ничего не значит (решение 1 LLD DK-933).
// Взвод это согласие на самостоятельный старт, и живёт он только у строки
// Backlog: на начатой строке он своё отработал, а хвост обхода такую строку и
// не смотрит. Строку из состава незакрытой цели поднимает цикл цели, и взвод
// на ней делил бы с циклом бюджет и ответственность. Обе находки снимает
// `taskctl arm <ID> --off`.
func lintArmed(root string, b *Board, bp string) []string {
	var finds []string
	for _, r := range b.Rows {
		if !armed(r.Title) {
			continue
		}
		where := fmt.Sprintf("%s:%d: %s", bp, r.LineIdx+1, r.ID)
		if r.Sect != SectBacklog {
			finds = append(finds, fmt.Sprintf("%s: взвод на строке в %s, а взводится только Backlog, снять: taskctl arm %s --off",
				where, sectTitles[r.Sect], r.ID))
			continue
		}
		if goal := goalOfTask(root, r.ID); goal != "" && b.find(goal) != nil {
			finds = append(finds, fmt.Sprintf("%s: взвод на строке незакрытой цели %s, её строки поднимает цикл цели, снять: taskctl arm %s --off",
				where, goal, r.ID))
		}
	}
	return finds
}

// lintDeps проверяет инварианты маркера «[после ...]»: ID существует (на
// доске или в архиве), не сам на себя, без дублей внутри маркера, без
// циклов. Начатая строка с ребром, которое ворота старта не пропустили бы ни
// разу, это отдельная находка, и место у такой строки одно, Backlog: ждать
// своей же задачи это маркер «после», а Blocked отдан обстоятельствам снаружи
// доски (RULES.board.md, «Трекинг задач» п. 4). Ребро, вернувшееся откатом или
// провалом предпосылки, находкой не считается: такую строку держат ворота
// слияния, а вернуть её в Backlog значило бы потерять начатую работу (решение
// 2 LLD DK-933).
func lintDeps(root string, b *Board, arch *Archive, bp string) []string {
	var finds []string
	for _, r := range b.Rows {
		where := fmt.Sprintf("%s:%d: %s", bp, r.LineIdx+1, r.ID)
		_, deps, _, _, _, _ := splitTitle(r.Title)
		seen := map[string]bool{}
		for _, d := range deps {
			switch {
			case d == r.ID:
				finds = append(finds, fmt.Sprintf("%s: маркер «после» ссылается сам на себя (%s)", where, d))
			case seen[d]:
				finds = append(finds, fmt.Sprintf("%s: маркер «после» дублирует %s", where, d))
			case b.find(d) == nil && !arch.has(d):
				finds = append(finds, fmt.Sprintf("%s: маркер «после» ссылается на несуществующую задачу %s", where, d))
			}
			seen[d] = true
		}
	}
	ed := newEdges(root, b, arch)
	for _, key := range []string{SectInProgress, SectCheck, SectBlocked} {
		for _, r := range b.Sects[key].Rows {
			_, deps, _, _, _, _ := splitTitle(r.Title)
			for _, d := range deps {
				if ok, why := ed.startable(d); !ok {
					finds = append(finds, fmt.Sprintf("%s:%d: %s в %s с неснятым ребром на %s (%s), вернуть в Backlog",
						bp, r.LineIdx+1, r.ID, sectTitles[key], d, why))
				}
			}
		}
	}
	finds = append(finds, lintDepCycles(b.Rows, bp)...)
	return finds
}

// lintDepCycles ищет цикл в графе «после» обходом в глубину с раскраской
// вершин; при мануальной правке доски цепочка A после B после A может
// появиться в обход проверки в dep add, здесь её ловит lint.
func lintDepCycles(rows []*Row, bp string) []string {
	adj := map[string][]string{}
	line := map[string]int{}
	for _, r := range rows {
		line[r.ID] = r.LineIdx
		_, deps, _, _, _, _ := splitTitle(r.Title)
		// Ссылку на себя уже ловит отдельная проверка в lintDeps, вторым
		// циклом в две строки её дублировать незачем.
		for _, d := range deps {
			if d != r.ID {
				adj[r.ID] = append(adj[r.ID], d)
			}
		}
	}
	const (
		white = iota
		gray
		black
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		for _, d := range adj[id] {
			if color[d] == gray {
				i := 0
				for stack[i] != d {
					i++
				}
				cycle = append(append([]string{}, stack[i:]...), d)
				return true
			}
			if color[d] == white && visit(d) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	for _, r := range rows {
		if cycle != nil {
			break
		}
		if color[r.ID] == white {
			visit(r.ID)
		}
	}
	if cycle == nil {
		return nil
	}
	return []string{fmt.Sprintf("%s:%d: цикл зависимостей: %s", bp, line[cycle[0]]+1, strings.Join(cycle, " -> "))}
}

// lintOrphanTaskFiles ищет файлы задач, которых нет ни на доске, ни в архиве.
// Так выглядит правка ID задним числом (лишний ноль в номере, переезд между
// префиксами): строка уезжает под новым ID, а файл остаётся под прежним
// именем. В файле живут замечания ревью и запись слитых коммитов, по которой
// shipctl держит очередь выката и собирает откат, поэтому расхождение стоит
// дороже, чем битая ссылка: обе утилиты продолжают работать, но с чужой
// задачей. Закрытые задачи сюда не попадают, их файлы уезжают в
// tasks/archive/<год>.
func lintOrphanTaskFiles(root string, b *Board, arch *Archive, bp string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "docs", "tasks"))
	if err != nil {
		return nil
	}
	var finds []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// Журнал чужого ревью лежит тут же под именем <ID>.review.md, но
		// файлом задачи не является: судится он по своему ID, а не по имени
		// с приставкой.
		id := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".md"), ".review")
		if b.find(id) != nil || arch.has(id) {
			continue
		}
		finds = append(finds, fmt.Sprintf("%s: файл задачи docs/tasks/%s ни на доске, ни в архиве (ID строки правили задним числом?)",
			bp, e.Name()))
	}
	return finds
}

// checkLinks проверяет, что локальные markdown-ссылки строки ведут на
// существующие файлы. Пути в доске и архиве относительны docs/.
func checkLinks(root, file string, lineIdx int, line string) []string {
	var finds []string
	for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
		target := m[1]
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
			strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
			continue
		}
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		if target == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "docs", target)); err != nil {
			finds = append(finds, fmt.Sprintf("%s:%d: битая ссылка %s (нет файла docs/%s)",
				file, lineIdx+1, m[1], target))
		}
	}
	return finds
}

// lintRankCell сверяет ячейку R с пересчётом: хвост поправок производный от
// колонки цены и рёбер наследования, и рукописный хвост расходится с ним так
// же молча, как расходился бы P, посчитанный на глаз (DK-428). Незнакомое имя
// поправки называется отдельно: пересчёт его просто не напишет, а читателю
// строки надо знать, что имени такого в таблице нет.
func lintRankCell(r *Row, where string) []string {
	var finds []string
	if m := rankCellRe.FindStringSubmatch(r.RCell); m != nil {
		if adjs, err := parseAdjTail(m[7]); err == nil {
			for _, a := range adjs {
				if a.From == "" && !knownAdj(a.Name) {
					finds = append(finds, fmt.Sprintf("%s: поправка «%s» не из таблицы", where, a.Name))
				}
			}
		}
	}
	if want := rankCell(r); want != r.RCell {
		finds = append(finds, fmt.Sprintf("%s: ячейка R «%s» не сходится с пересчётом, ждали «%s», пересчитать: taskctl sort",
			where, r.RCell, want))
	}
	return finds
}

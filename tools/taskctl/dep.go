package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// depSufRe разбирает суффикс заголовка «[после ID, ID]» (задача делается
// после перечисленных). Хранится только это направление, обратное (кого
// держит задача) считается по всей доске. Порядок суффиксов в заголовке
// фиксирован (LLD DK-292, решение 3): сначала [после ...], потом
// [приёмка: ...], потом [провал: ...], потом [блок: ...], то есть постоянные
// свойства строки раньше временных пометок.
var depSufRe = regexp.MustCompile(`\s*\[после ([^\]|]+)\]\s*$`)

// splitTitle разбирает полный текст заголовка строки на основу, список
// зависимостей из «[после ...]» и хвосты «[приёмка: ...]», «[провал: ...]» и
// «[блок: ...]» как есть (с ведущим пробелом) либо пустые строки.
func splitTitle(title string) (base string, deps []string, acceptSuf, failSuf, blockSuf string) {
	rest := title
	if m := blockSufRe.FindString(rest); m != "" {
		rest = strings.TrimSuffix(rest, m)
		blockSuf = m
	}
	if m := failSufRe.FindString(rest); m != "" {
		rest = strings.TrimSuffix(rest, m)
		failSuf = m
	}
	if m := acceptSufRe.FindString(rest); m != "" {
		rest = strings.TrimSuffix(rest, m)
		acceptSuf = m
	}
	if m := depSufRe.FindStringSubmatch(rest); m != nil {
		base = rest[:len(rest)-len(m[0])]
		for _, id := range strings.Split(m[1], ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				deps = append(deps, id)
			}
		}
		return base, deps, acceptSuf, failSuf, blockSuf
	}
	return rest, nil, acceptSuf, failSuf, blockSuf
}

// joinTitle собирает заголовок обратно: основа, затем «[после ...]» (если
// список не пуст), затем хвосты приёмки, провала и блокировки как есть.
func joinTitle(base string, deps []string, acceptSuf, failSuf, blockSuf string) string {
	t := base
	if len(deps) > 0 {
		t += " [после " + strings.Join(deps, ", ") + "]"
	}
	return t + acceptSuf + failSuf + blockSuf
}

func joinOrDash(xs []string) string {
	if len(xs) == 0 {
		return "-"
	}
	return strings.Join(xs, ", ")
}

// reachable проверяет, достижим ли to из from по цепочке «после» (from
// зависит от ..., которая зависит от to). Граф строится из текущих строк
// доски: зависимость на закрытую (архивную) задачу тупиковая, у неё нет
// строки и, значит, исходящих рёбер.
func reachable(rows []*Row, from, to string) bool {
	adj := map[string][]string{}
	for _, r := range rows {
		_, deps, _, _, _ := splitTitle(r.Title)
		adj[r.ID] = deps
	}
	visited := map[string]bool{}
	var dfs func(string) bool
	dfs = func(id string) bool {
		if id == to {
			return true
		}
		if visited[id] {
			return false
		}
		visited[id] = true
		for _, d := range adj[id] {
			if dfs(d) {
				return true
			}
		}
		return false
	}
	return dfs(from)
}

type DepParams struct {
	ID, DepID string
	Commit    CommitOpts
}

// cmdDepAdd ставит «A после B»: A с этого момента не уходит в работу из
// Backlog, пока ребро не снято, то есть работа B не слита в main или B не
// закрыта (решение 2 LLD DK-933).
func cmdDepAdd(root string, p DepParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "dep add"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	row := b.find(p.ID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", p.ID)
	}
	if err := needTaskFile(root, p.ID); err != nil {
		return "", err
	}
	if p.DepID == p.ID {
		return "", fmt.Errorf("%s не может зависеть сам от себя", p.ID)
	}
	if b.find(p.DepID) == nil && !arch.has(p.DepID) {
		return "", fmt.Errorf("%s нет ни на доске, ни в архиве", p.DepID)
	}
	// Симметрично воротам старта: начатой строке ребро дописывается, только
	// когда оно уже снято (решение 2 LLD DK-933). Blocked тут наравне с
	// остальными: ждать своей же задачи и обстоятельства снаружи разом это
	// ровно та путаница, которую развели в RULES.board.md, «Трекинг задач» п. 4.
	if row.Sect == SectInProgress || row.Sect == SectCheck || row.Sect == SectBlocked {
		if ed := newEdges(root, b, arch).of(p.DepID); !ed.Lifted() {
			return "", fmt.Errorf("%s уже в %s, нельзя добавить неснятое ребро на %s: %s", p.ID, sectTitles[row.Sect], p.DepID, ed.Why)
		}
	}
	base, deps, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
	for _, d := range deps {
		if d == p.DepID {
			return "", fmt.Errorf("%s уже после %s", p.ID, p.DepID)
		}
	}
	if reachable(b.Rows, p.DepID, p.ID) {
		return "", fmt.Errorf("%s после %s замкнёт цикл зависимостей", p.ID, p.DepID)
	}
	deps = append(deps, p.DepID)
	row.Title = joinTitle(base, deps, acceptSuf, failSuf, blockSuf)
	b.updateLine(row.LineIdx, formatRow(row))
	// Свежее ребро тянет ранг предпосылки вверх по инварианту зависимости, и
	// строки затронутых задач переезжают тут же (DK-428).
	moves, b, err := rehydrate(b)
	if err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: после %s%s%s", p.ID, p.DepID, movesTail(moves), tail), nil
}

// cmdDepRm снимает зависимость руками (обычно это делает close, когда B
// закрывается; rm нужен, если зависимость поставили по ошибке).
func cmdDepRm(root string, p DepParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "dep rm"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(p.ID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", p.ID)
	}
	base, deps, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
	idx := -1
	for i, d := range deps {
		if d == p.DepID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return "", fmt.Errorf("%s не зависит от %s", p.ID, p.DepID)
	}
	deps = append(deps[:idx], deps[idx+1:]...)
	row.Title = joinTitle(base, deps, acceptSuf, failSuf, blockSuf)
	b.updateLine(row.LineIdx, formatRow(row))
	moves, b, err := rehydrate(b)
	if err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: зависимость от %s снята%s%s", p.ID, p.DepID, movesTail(moves), tail), nil
}

// depSides возвращает по каждой задаче с доски, после кого она делается
// (after) и кого держит (blocks, обратное направление). Результат кешируется
// в Board и пересчитывается только при явной инвалидации.
func depSides(b *Board) map[string]*struct{ after, blocks []string } {
	if b.depSidesCache != nil {
		return b.depSidesCache
	}
	all := map[string]*struct{ after, blocks []string }{}
	get := func(id string) *struct{ after, blocks []string } {
		if all[id] == nil {
			all[id] = &struct{ after, blocks []string }{}
		}
		return all[id]
	}
	for _, r := range b.Rows {
		_, deps, _, _, _ := splitTitle(r.Title)
		if len(deps) == 0 {
			continue
		}
		get(r.ID).after = deps
		for _, d := range deps {
			get(d).blocks = append(get(d).blocks, r.ID)
		}
	}
	b.depSidesCache = all
	return all
}

// inArchive говорит, лежит ли ID в архиве.
func inArchive(root, id string) (bool, error) {
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return false, err
	}
	return arch.has(id), nil
}

// cmdDepList без ID печатает зависимости всей доски в обе стороны, с ID
// печатает только для одной задачи; закрытая задача отвечает по архиву.
func cmdDepList(root, id string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	sides := depSides(b)
	if id != "" {
		s := sides[id]
		if s == nil {
			s = &struct{ after, blocks []string }{}
		}
		if b.find(id) == nil {
			// Архивная строка отвечает тем же, что и живая, кроме «после»:
			// после закрытия с её экрана берут в работу тех, кого она держала,
			// и это направление доска знает (маркеры целят и в архив).
			archived, err := inArchive(root, id)
			if err != nil {
				return "", err
			}
			if !archived {
				return "", fmt.Errorf("%s нет ни на доске, ни в архиве", id)
			}
			return fmt.Sprintf("%s после: %s\n%s держит: %s", id, archiveAfterNote, id, joinOrDash(s.blocks)), nil
		}
		return fmt.Sprintf("%s после: %s\n%s держит: %s", id, joinOrDash(s.after), id, joinOrDash(s.blocks)), nil
	}
	var out []string
	for _, r := range b.Rows {
		s := sides[r.ID]
		if s == nil {
			continue
		}
		if len(s.after) > 0 {
			out = append(out, fmt.Sprintf("%s после: %s", r.ID, strings.Join(s.after, ", ")))
		}
		if len(s.blocks) > 0 {
			out = append(out, fmt.Sprintf("%s держит: %s", r.ID, strings.Join(s.blocks, ", ")))
		}
	}
	if len(out) == 0 {
		return "зависимостей на доске нет", nil
	}
	return strings.Join(out, "\n"), nil
}

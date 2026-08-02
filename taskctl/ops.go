package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func boardPath(root string) string   { return filepath.Join(root, "docs", "TASKS.md") }
func archivePath(root string) string { return filepath.Join(root, "docs", "TASKS-archive.md") }

// findRoot ищет корень репозитория (директорию с docs/TASKS.md) вверх от start.
func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(boardPath(dir)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("не нашёл docs/TASKS.md вверх от %s", start)
		}
		dir = parent
	}
}

func checkCell(name, s string) error {
	if strings.ContainsAny(s, "|\n") {
		return fmt.Errorf("%s не может содержать «|» и переводы строк", name)
	}
	return nil
}

// checkReason это checkCell плюс запрет на квадратные скобки: причина
// блокировки приклеивается к заголовку суффиксом «[блок: ...]», и скобка в
// её тексте притворилась бы ещё одним суффиксом (например, границей
// «[после ...]»), которую разбор заголовка потом не отличит от настоящей.
func checkReason(reason string) error {
	if err := checkCell("причина", reason); err != nil {
		return err
	}
	if strings.ContainsAny(reason, "[]") {
		return fmt.Errorf("причина не может содержать «[» и «]»")
	}
	return nil
}

// nextID берёт префикс из существующих строк, а на пустой доске из шапки
// «(префикс XX)», чтобы первая задача заводилась без --id.
func nextID(b *Board, a *Archive) (string, error) {
	prefix := b.Prefix
	max := 0
	scan := func(id string, num int) error {
		m := idRe.FindStringSubmatch(id)
		if prefix == "" {
			prefix = m[1]
		} else if prefix != m[1] {
			return fmt.Errorf("на доске и в архиве разные префиксы ID: %s и %s", prefix, m[1])
		}
		if num > max {
			max = num
		}
		return nil
	}
	for _, r := range b.Rows {
		if err := scan(r.ID, r.Num); err != nil {
			return "", err
		}
	}
	for _, r := range a.Rows {
		if err := scan(r.ID, r.Num); err != nil {
			return "", err
		}
	}
	if prefix == "" {
		return "", fmt.Errorf("ни одной задачи и нет «(префикс XX)» в шапке доски, укажи --id явно")
	}
	return fmt.Sprintf("%s-%03d", prefix, max+1), nil
}

// CommitOpts это флаги -m/--push изменяющих команд: закоммитить (и запушить)
// ровно те файлы, которые тронула операция, не задевая чужой индекс.
type CommitOpts struct {
	Msg  string
	Push bool
}

// validate зовётся до первой записи на диск, чтобы кривая пара флагов не
// оставляла доску изменённой, но не закоммиченной.
func (c CommitOpts) validate() error {
	if c.Push && c.Msg == "" {
		return fmt.Errorf("--push работает только вместе с -m")
	}
	return nil
}

// apply возвращает хвост для сообщения команды («, коммит abc1234») либо
// пустую строку, когда -m не передан.
func (c CommitOpts) apply(root string, paths []string) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if c.Msg == "" {
		return "", nil
	}
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	// В add идут только существующие пути: файл, уехавший через git mv, уже
	// в индексе, а pathspec по нему упал бы. В pathspec коммита нужны все
	// пути, тогда staged-переименование попадает в коммит, а что агент
	// стейджил до этого, не попадает.
	var addPaths []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			addPaths = append(addPaths, p)
		}
	}
	if len(addPaths) > 0 {
		if _, err := git(append([]string{"add", "--"}, addPaths...)...); err != nil {
			return "", err
		}
	}
	if _, err := git(append([]string{"commit", "-m", c.Msg, "--"}, paths...)...); err != nil {
		return "", err
	}
	hash, err := git("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	suffix := ", коммит " + hash
	if c.Push {
		if _, err := git("push"); err != nil {
			return "", err
		}
		suffix += ", запушено"
	}
	return suffix, nil
}

// normalizeStatus приводит статус к ключу секции: «In progress» = in-progress.
func normalizeStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	return strings.ReplaceAll(s, "_", "-")
}

var barePathRe = regexp.MustCompile(`^[0-9A-Za-z._/~-]+$`)

// wrapLink оборачивает голый путь вида tasks/XR-001.md в markdown-ссылку,
// иначе ячейка не кликается и выпадает из проверки ссылок в lint.
func wrapLink(link string) string {
	if barePathRe.MatchString(link) && (strings.Contains(link, "/") || strings.HasSuffix(link, ".md")) {
		return fmt.Sprintf("[%s](%s)", link, link)
	}
	return link
}

type AddParams struct {
	ID, Title, Type, Rank, Cost, Link, Status, Reason string
	Commit                                            CommitOpts
}

func cmdAdd(root string, p AddParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
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
	id := p.ID
	if id == "" {
		if id, err = nextID(b, arch); err != nil {
			return "", err
		}
	} else if !idRe.MatchString(id) {
		return "", fmt.Errorf("ID %q не вида PREFIX-NNN", id)
	}
	if b.find(id) != nil || arch.has(id) {
		return "", fmt.Errorf("ID %s уже занят", id)
	}
	if strings.TrimSpace(p.Title) == "" {
		return "", fmt.Errorf("нужен --title")
	}
	if err := checkCell("заголовок", p.Title); err != nil {
		return "", err
	}
	if err := checkType(p.Type); err != nil {
		return "", err
	}
	total, parts, err := parseRank(p.Rank)
	if err != nil {
		return "", err
	}
	cost := p.Cost
	if cost == "" {
		cost = "-"
	}
	if err := checkCost(cost); err != nil {
		return "", err
	}
	link := wrapLink(p.Link)
	taskFile := ""
	if link == "" {
		// Без --link ссылка ведёт на файл задачи, а пока файла нет, в ячейке
		// плейсхолдер: однострочному бэклогу файл не положен.
		rel := fmt.Sprintf("tasks/%s.md", id)
		if _, err := os.Stat(filepath.Join(root, "docs", rel)); err == nil {
			link = fmt.Sprintf("[%s](%s)", rel, rel)
			taskFile = filepath.Join("docs", rel)
		} else {
			link = "-"
		}
	}
	if err := checkCell("ссылка", link); err != nil {
		return "", err
	}
	status := normalizeStatus(p.Status)
	if status == "" {
		status = SectBacklog
	}
	sec, ok := b.Sects[status]
	if !ok {
		return "", fmt.Errorf("неизвестный статус %q, жду backlog / in-progress / check / blocked", status)
	}
	title := p.Title
	if status == SectBlocked && strings.TrimSpace(p.Reason) != "" {
		if err := checkReason(p.Reason); err != nil {
			return "", err
		}
		title += " [блок: " + p.Reason + "]"
	}
	row := &Row{ID: id, Num: mustNum(id), Title: title, Type: p.Type, P: bucket(total), RTotal: total, RParts: parts, Cost: cost, Link: link}
	if err := insertRowLine(b, sec, row, formatRow(row)); err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	paths := []string{filepath.Join("docs", "TASKS.md")}
	if taskFile != "" {
		paths = append(paths, taskFile)
	}
	tail, err := p.Commit.apply(root, paths)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s заведена в %s: %s, R=%d%s", id, status, row.P, total, tail), nil
}

func mustNum(id string) int {
	m := idRe.FindStringSubmatch(id)
	n := 0
	fmt.Sscanf(m[2], "%d", &n)
	return n
}

// Содержимое причины не пускает «[»: иначе при перепутанном порядке
// суффиксов («[блок: ...] [после ...]» вместо «[после ...] [блок: ...]»)
// жадный класс дотягивался до конца строки и съедал маркер зависимости
// целиком, делая его невидимым для lint, move и close.
var blockSufRe = regexp.MustCompile(`\s*\[блок: [^|\[]*\]\s*$`)

func cmdMove(root, id, target, reason string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	target = normalizeStatus(target)
	if _, ok := b.Sects[target]; !ok {
		return "", fmt.Errorf("неизвестный статус %q, жду backlog / in-progress / check / blocked", target)
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	if row.Sect == target {
		return "", fmt.Errorf("%s уже в %s", id, target)
	}
	if target == SectInProgress {
		arch, err := LoadArchive(archivePath(root))
		if err != nil {
			return "", err
		}
		_, deps, _ := splitTitle(row.Title)
		for _, d := range deps {
			if !arch.has(d) {
				return "", fmt.Errorf("%s зависит от незакрытой %s, нельзя перевести в in-progress", id, d)
			}
		}
	}
	line := b.Lines[row.LineIdx]
	moved := *row
	if target == SectBlocked {
		if strings.TrimSpace(reason) == "" {
			return "", fmt.Errorf("для blocked обязателен --reason, одна строка почему")
		}
		if err := checkReason(reason); err != nil {
			return "", err
		}
		moved.Title = row.Title + " [блок: " + reason + "]"
		line = formatRow(&moved)
	} else if row.Sect == SectBlocked {
		// На выходе из Blocked причина в заголовке больше не нужна.
		if cleaned := blockSufRe.ReplaceAllString(row.Title, ""); cleaned != row.Title {
			moved.Title = cleaned
			line = formatRow(&moved)
		}
	}
	b.remove(row.LineIdx)
	b2, err := parseLines(b.Path, b.Lines)
	if err != nil {
		return "", err
	}
	if err := insertRowLine(b2, b2.Sects[target], &moved, line); err != nil {
		return "", err
	}
	if err := b2.Save(); err != nil {
		return "", err
	}
	// Задача доехала до Check или встала на блокере: повод громкий, и звучит
	// он тут же, где меняется статус, а не в shipctl или где-то ещё, кто бы
	// move ни позвал (RULES.board.md, «Ветки, ревью и деплой» п. 8).
	var note string
	base, _, _ := splitTitle(row.Title)
	switch target {
	case SectCheck:
		note = notify(root, fmt.Sprintf("%s: %s в Check", filepath.Base(root), id), base)
	case SectBlocked:
		note = notify(root, fmt.Sprintf("%s: %s на блокере", filepath.Base(root), id), reason)
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %s -> %s%s%s", id, row.Sect, target, tail, note), nil
}

type SetParams struct {
	ID, Title, Type, Rank, Cost, Link string
	Commit                            CommitOpts
}

func cmdSet(root string, p SetParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if p.Title == "" && p.Type == "" && p.Rank == "" && p.Cost == "" && p.Link == "" {
		return "", fmt.Errorf("нечего менять, жду --title, --type, --rank, --cost и/или --link")
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(p.ID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", p.ID)
	}
	var changes []string
	if p.Title != "" {
		if err := checkCell("заголовок", p.Title); err != nil {
			return "", err
		}
		title := p.Title
		// У строки с зависимостью и/или причиной блокировки эти хвосты живут
		// в заголовке, при замене текста они переносятся в новый (в исходном
		// порядке: сначала «после», потом «блок»).
		_, deps, blockSuf := splitTitle(row.Title)
		if len(deps) > 0 && !strings.Contains(title, "[после") {
			title = joinTitle(title, deps, "")
		}
		if blockSuf != "" && !strings.Contains(title, "[блок:") {
			title += blockSuf
		}
		if title != row.Title {
			changes = append(changes, "заголовок")
			row.Title = title
		}
	}
	if p.Type != "" {
		if err := checkType(p.Type); err != nil {
			return "", err
		}
		if p.Type != row.Type {
			changes = append(changes, fmt.Sprintf("тип %s -> %s", row.Type, p.Type))
			row.Type = p.Type
		}
	}
	if p.Cost != "" {
		if err := checkCost(p.Cost); err != nil {
			return "", err
		}
		if p.Cost != row.Cost {
			changes = append(changes, fmt.Sprintf("цена %s -> %s", row.Cost, p.Cost))
			row.Cost = p.Cost
		}
	}
	if p.Link != "" {
		link := wrapLink(p.Link)
		if err := checkCell("ссылка", link); err != nil {
			return "", err
		}
		if link != row.Link {
			changes = append(changes, "ссылка")
			row.Link = link
		}
	}
	rankChanged := false
	if p.Rank != "" {
		total, parts, err := parseRank(p.Rank)
		if err != nil {
			return "", err
		}
		if total != row.RTotal || parts != row.RParts {
			changes = append(changes, fmt.Sprintf("R %d -> %d", row.RTotal, total))
			row.RTotal, row.RParts = total, parts
			if np := bucket(total); np != row.P {
				changes = append(changes, fmt.Sprintf("P %s -> %s", row.P, np))
				row.P = np
			}
			rankChanged = true
		}
	}
	if len(changes) == 0 {
		return "", fmt.Errorf("у %s уже такие значения, менять нечего", p.ID)
	}
	line := formatRow(row)
	// В Backlog позиция строки зависит от ранга, поэтому при его смене строка
	// переставляется; в остальных секциях порядок ручной, ячейки меняются на месте.
	if rankChanged && row.Sect == SectBacklog {
		b.remove(row.LineIdx)
		b2, err := parseLines(b.Path, b.Lines)
		if err != nil {
			return "", err
		}
		if err := insertRowLine(b2, b2.Sects[SectBacklog], row, line); err != nil {
			return "", err
		}
		b = b2
	} else {
		b.Lines[row.LineIdx] = line
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %s%s", p.ID, strings.Join(changes, ", "), tail), nil
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

type CloseParams struct {
	ID, Commits, Date, Link string
	Commit                  CommitOpts
}

func cmdClose(root string, p CloseParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
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
	if arch.has(p.ID) {
		return "", fmt.Errorf("%s уже есть в архиве", p.ID)
	}
	date := p.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !dateRe.MatchString(date) {
		return "", fmt.Errorf("дата %q не вида ГГГГ-ММ-ДД", date)
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("дата %q не разбирается: %v", date, err)
	}
	var commits []string
	if p.Commits != "" {
		for _, c := range strings.Split(p.Commits, ",") {
			c = strings.TrimSpace(c)
			if !commitRe.MatchString(c) {
				return "", fmt.Errorf("%q не похоже на хеш коммита", c)
			}
			commits = append(commits, c)
		}
	}
	year := date[:4]
	moved := ""
	var changedFiles []string
	taskFile := filepath.Join(root, "docs", "tasks", p.ID+".md")
	if _, err := os.Stat(taskFile); err == nil {
		dst := filepath.Join(root, "docs", "tasks", "archive", year, p.ID+".md")
		if err := gitMv(root, taskFile, dst); err != nil {
			return "", err
		}

		oldBaseDir := filepath.Join(root, "docs", "tasks")
		newBaseDir := filepath.Join(root, "docs", "tasks", "archive", year)
		if err := rewriteLinksInFile(dst, oldBaseDir, newBaseDir); err != nil {
			return "", err
		}

		var err2 error
		changedFiles, err2 = findAndRewriteReferencesToFile(root, taskFile, dst)
		if err2 != nil {
			return "", err2
		}

		moved = fmt.Sprintf("tasks/archive/%s/%s.md", year, p.ID)
	}
	linkCell := p.Link
	if linkCell == "" {
		var parts []string
		if moved != "" {
			parts = append(parts, fmt.Sprintf("[%s](%s)", moved, moved))
		}
		for _, c := range commits {
			parts = append(parts, "`"+c+"`")
		}
		if len(parts) == 0 {
			parts = append(parts, row.Link)
		}
		linkCell = strings.Join(parts, ", ")
	}
	if err := checkCell("ссылка", linkCell); err != nil {
		return "", err
	}
	// В архивную строку маркер зависимости не попадает: закрытая задача
	// саму себя ждать больше не заставит.
	archBase, _, archBlockSuf := splitTitle(row.Title)
	cells := []string{p.ID, joinTitle(archBase, nil, archBlockSuf), row.Type, row.P, date, linkCell}
	if err := appendArchiveRow(archivePath(root), cells); err != nil {
		return "", err
	}
	// Закрытая зависимость выполнена: снять «[после <ID>]» со всех
	// остальных строк доски, протухшие маркеры на ней не живут.
	var depTouched []string
	for _, r := range b.Rows {
		if r.ID == p.ID {
			continue
		}
		base, deps, blockSuf := splitTitle(r.Title)
		idx := -1
		for i, d := range deps {
			if d == p.ID {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue
		}
		deps = append(deps[:idx], deps[idx+1:]...)
		r.Title = joinTitle(base, deps, blockSuf)
		b.Lines[r.LineIdx] = formatRow(r)
		depTouched = append(depTouched, r.ID)
	}
	b.remove(row.LineIdx)
	if err := b.Save(); err != nil {
		return "", err
	}
	paths := []string{filepath.Join("docs", "TASKS.md"), filepath.Join("docs", "TASKS-archive.md")}
	if moved != "" {
		paths = append(paths, filepath.Join("docs", "tasks", p.ID+".md"), filepath.Join("docs", moved))
	}
	paths = append(paths, changedFiles...)
	tail, err := p.Commit.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s закрыта %s, строка в архиве", p.ID, date)
	if moved != "" {
		msg += ", файл задачи в " + moved
	}
	if len(depTouched) > 0 {
		msg += ", маркер «после» снят у: " + strings.Join(depTouched, ", ")
	}
	return msg + tail, nil
}

// gitMv переносит файл через git mv, а вне git-репозитория (или для
// неотслеживаемого файла) обычным rename.
func gitMv(root, from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", root, "mv", from, to)
	if out, err := cmd.CombinedOutput(); err != nil {
		if renameErr := os.Rename(from, to); renameErr != nil {
			return fmt.Errorf("git mv: %v (%s); rename: %v", err, strings.TrimSpace(string(out)), renameErr)
		}
	}
	return nil
}

// fullLinkRe находит markdown-ссылки вида [текст](цель)
var fullLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)

// codeSpanRe находит инлайн-код в обратных кавычках: ссылка в примере команды
// это текст, а не ссылка.
var codeSpanRe = regexp.MustCompile("`+[^`]*`+")

// fenceRe находит забор блока кода: по CommonMark отступ не больше трёх
// пробелов, дальше три и больше символов ` или ~ подряд.
var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// rewriteLinksSkippingCodeBlocks переписывает ссылки в тексте функцией resolve,
// которая получает цель ссылки без якоря и возвращает новый путь (пустая строка
// значит «оставить как было»). Содержимое блоков кода не трогается.
func rewriteLinksSkippingCodeBlocks(text string, resolve func(path string) string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, line := range lines {
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			// Закрывает только забор из того же символа и не короче
			// открывающего, иначе вложенный ``` оборвал бы внешний ````.
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(line[len(m[0]):]) == "":
				fence = ""
			}
			continue
		}
		if fence == "" {
			lines[i] = rewriteLinksInLine(line, resolve)
		}
	}
	return strings.Join(lines, "\n")
}

// rewriteLinksInLine отделяет у каждой ссылки якорь, отбрасывает пустые,
// внешние и mailto-цели и собирает ссылку обратно с путём от resolve.
func rewriteLinksInLine(line string, resolve func(path string) string) string {
	var out strings.Builder
	pos := 0
	for _, span := range codeSpanRe.FindAllStringIndex(line, -1) {
		out.WriteString(rewriteLinksOutsideCode(line[pos:span[0]], resolve))
		out.WriteString(line[span[0]:span[1]])
		pos = span[1]
	}
	out.WriteString(rewriteLinksOutsideCode(line[pos:], resolve))
	return out.String()
}

func rewriteLinksOutsideCode(line string, resolve func(path string) string) string {
	return fullLinkRe.ReplaceAllStringFunc(line, func(match string) string {
		m := fullLinkRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		path, anchor, hasAnchor := strings.Cut(m[2], "#")
		if path == "" || strings.Contains(path, "://") || strings.HasPrefix(path, "mailto:") {
			return match
		}
		newPath := resolve(path)
		if newPath == "" {
			return match
		}
		if hasAnchor {
			anchor = "#" + anchor
		}
		return fmt.Sprintf("[%s](%s%s)", m[1], newPath, anchor)
	})
}

// rewriteLinksInFile переписывает относительные ссылки в файле при его переносе.
// Ссылка, которая разрешалась от oldBaseDir, переписывается так, чтобы разрешаться
// от newBaseDir в один и тот же целевой файл. Ссылки внутри блоков кода пропускаются.
func rewriteLinksInFile(filePath, oldBaseDir, newBaseDir string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	result := rewriteLinksSkippingCodeBlocks(string(content), func(path string) string {
		newPath, err := filepath.Rel(newBaseDir, filepath.Join(oldBaseDir, path))
		if err != nil {
			return ""
		}
		return newPath
	})

	return os.WriteFile(filePath, []byte(result), 0o644)
}

// skipDirs это директории, которые пропускаются при поиске файлов
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"local-docs": true, ".venv": true, "venv": true, "__pycache__": true,
	".idea": true, ".vscode": true,
}

// findAndRewriteReferencesToFile находит все markdown-файлы со ссылками на
// oldPath и переписывает их на newPath. Возвращает список изменённых файлов.
// Ссылки внутри блоков кода пропускаются.
func findAndRewriteReferencesToFile(root, oldPath, newPath string) ([]string, error) {
	var changed []string
	oldPath = filepath.Clean(oldPath)
	newPath = filepath.Clean(newPath)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fileDir := filepath.Dir(path)
		var pathChanged bool

		newContent := rewriteLinksSkippingCodeBlocks(string(content), func(linkPath string) string {
			if filepath.Join(fileDir, linkPath) != oldPath {
				return ""
			}
			newLinkPath, err := filepath.Rel(fileDir, newPath)
			if err != nil {
				return ""
			}
			pathChanged = true
			return newLinkPath
		})

		if pathChanged {
			if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			changed = append(changed, rel)
		}

		return nil
	})

	return changed, err
}

func cmdSort(root string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	sec := b.Sects[SectBacklog]
	idxs := make([]int, len(sec.Rows))
	for i, r := range sec.Rows {
		idxs[i] = r.LineIdx
	}
	sorted := append([]*Row{}, sec.Rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].RTotal != sorted[j].RTotal {
			return sorted[i].RTotal > sorted[j].RTotal
		}
		return sorted[i].Num < sorted[j].Num
	})
	contents := make([]string, len(sorted))
	for i, r := range sorted {
		contents[i] = b.Lines[r.LineIdx]
	}
	changed := 0
	for i := range idxs {
		if b.Lines[idxs[i]] != contents[i] {
			changed++
		}
		b.Lines[idxs[i]] = contents[i]
	}
	// Разбор доски старого формата уже перевёл строки в памяти, sort тогда
	// сохраняет файл даже без перестановок: это штатный способ миграции.
	if changed == 0 && !b.Legacy {
		return "Backlog уже отсортирован", nil
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	if changed == 0 {
		return "доска переведена в формат с колонкой «Цена»" + tail, nil
	}
	return fmt.Sprintf("Backlog пересортирован, строк переставлено: %d%s", changed, tail), nil
}

func cmdID(root string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	return nextID(b, arch)
}

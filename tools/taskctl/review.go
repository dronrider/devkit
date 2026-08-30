package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const reviewHeading = "## Ревью"

type reviewNote struct {
	LineIdx int
	Text    string // текст замечания без маркера списка, продолжения прирастают через пробел
	Span    int    // число строк элемента: маркерная плюс строки продолжения
}

// resolvedTailRe узнаёт исход по месту, где его пишет cmdReviewResolve
// ("<текст>: исправлено" либо "<текст>: отклонено, причина"), а не по факту
// появления слова где-то в тексте: цитата замечания, пересказывающая формат
// команды («...словами «исправлено»»), этому хвосту не соответствует и
// замечание не закрывает (DK-514).
var resolvedTailRe = regexp.MustCompile(`: (исправлено|отклонено)(,.*)?$`)

// cleanVerdictRe узнаёт чистый вердикт по голове элемента, куда его пишет
// ревьювер («Вердикт: без замечаний. пояснение» либо «замечаний нет» без
// пояснения), а не по факту оборота где-то в тексте: суть замечания,
// кончающаяся словами «про вердикт без замечаний нет ничего», голове не
// соответствует и замечание не закрывает (DK-469). За фразой идёт точка,
// двоеточие или конец текста, поэтому «замечаний неточностей» и продолжение
// фразы перечислением вердиктом не считаются. Маркер списка терпится ради
// копии в shipctl, где элементы разбираются вместе с маркером.
var cleanVerdictRe = regexp.MustCompile(`^(?:[-*] )?(?:(?:вердикт|ревью):\s*)?(?:без замечаний|замечаний нет)(?:[.:]|$)`)

// outcome возвращает исход замечания: «исправлено», «отклонено», «чисто»
// (ревью без замечаний, не требующее исхода) или пустую строку у открытого.
// Критерий тот же, каким shipctl merge решает, закрыто ли ревью. Порядок
// проверок исход -> чистый итог -> открыто, поэтому «гибрид: исправлено,
// теперь без замечаний» остаётся закрытым, а не уходит в чистый вердикт.
func (n reviewNote) outcome() string {
	low := strings.ToLower(n.Text)
	if m := resolvedTailRe.FindStringSubmatch(low); m != nil {
		return m[1]
	}
	if cleanVerdictRe.MatchString(low) {
		return "чисто"
	}
	return ""
}

// reviewFile это файл задачи с разобранным разделом «Ревью». Правки, как и у
// доски, точечные: нетронутые строки сохраняются байт в байт.
type reviewFile struct {
	path       string
	lines      []string
	notes      []reviewNote
	hasSection bool
	insertAt   int // строка, перед которой вставлять новое замечание
}

func loadReview(path string) (*reviewFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rf := &reviewFile{path: path, lines: strings.Split(string(data), "\n")}
	in := false
	contOpen := false
	for i, ln := range rf.lines {
		if strings.HasPrefix(ln, "## ") {
			if in {
				break
			}
			if strings.HasPrefix(ln, reviewHeading) {
				in, rf.hasSection = true, true
				rf.insertAt = i + 1
				if i+1 < len(rf.lines) && strings.TrimSpace(rf.lines[i+1]) == "" {
					rf.insertAt = i + 2
				}
			}
			continue
		}
		t := strings.TrimSpace(ln)
		if !in {
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			rf.notes = append(rf.notes, reviewNote{LineIdx: i, Text: strings.TrimSpace(t[2:]), Span: 1})
			rf.insertAt = i + 1
			contOpen = true
			continue
		}
		// Пустая строка закрывает текущий элемент: абзац после неё к замечанию
		// не прирастает, иначе исход из чужого абзаца закрыл бы замечание и
		// разошёлся бы с shipctl, чей reviewItems сбрасывает элемент на пустой
		// строке.
		if t == "" {
			contOpen = false
			continue
		}
		// Строка продолжения прирастает к последнему замечанию, пока элемент
		// не закрыт пустой строкой или новым маркером: длинное замечание,
		// перенесённое на несколько строк, судится целиком, и исход на строке
		// переноса закрывает его. Абзац без маркера вне элемента игнорируется
		// (чистый вердикт абзацем замечанием не считается).
		if contOpen && len(rf.notes) > 0 {
			n := &rf.notes[len(rf.notes)-1]
			if n.Text != "" {
				n.Text += " "
			}
			n.Text += t
			n.Span++
			rf.insertAt = i + 1
		}
	}
	return rf, nil
}

// ensureSection заводит раздел «Ревью», когда первое замечание пришло на файл
// без него. Место разделу выбирает форма (TASKFORM.md), а не конец файла:
// сценарий проверки к первому замечанию обычно уже написан, и приклеенный за
// ним раздел ловил бы сторож порядка на каждой отревьюенной задаче.
func (rf *reviewFile) ensureSection() {
	if rf.hasSection {
		return
	}
	rf.hasSection = true
	if at := formSectionAt(rf.lines, reviewHeading); at >= 0 {
		head := []string{reviewHeading, "", ""}
		if at > 0 && strings.TrimSpace(rf.lines[at-1]) != "" {
			head = append([]string{""}, head...)
		}
		tail := append([]string{}, rf.lines[at:]...)
		rf.lines = append(rf.lines[:at], append(head, tail...)...)
		rf.insertAt = at + len(head) - 1
		return
	}
	// Разделов формы ниже «Ревью» в файле нет, значит место ему в конце, и
	// хвостовые пустые строки перед ним лишние.
	for len(rf.lines) > 0 && strings.TrimSpace(rf.lines[len(rf.lines)-1]) == "" {
		rf.lines = rf.lines[:len(rf.lines)-1]
	}
	rf.lines = append(rf.lines, "", reviewHeading, "", "")
	rf.insertAt = len(rf.lines) - 1
}

func (rf *reviewFile) insert(idx int, line string) {
	rf.lines = append(rf.lines[:idx], append([]string{line}, rf.lines[idx:]...)...)
}

func (rf *reviewFile) save() error {
	return os.WriteFile(rf.path, []byte(strings.Join(rf.lines, "\n")), 0o644)
}

func taskFileAbs(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

func cmdReviewAdd(root, id, note string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return "", fmt.Errorf("жду текст замечания: review add <ID> \"суть\"")
	}
	if strings.Contains(note, "\n") {
		return "", fmt.Errorf("замечание пишется одной строкой")
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	paths := []string{filepath.Join("docs", "tasks", id+".md")}
	created, err := ensureTaskFile(root, id, row)
	if err != nil {
		return "", err
	}
	// review add остаётся разрешён из worktree (в отличие от file), а вот
	// ссылку в строке доски там не чинит: доску правит только диспетчер в
	// основном чекауте (RULES.board.md, «Доска в руках диспетчера»).
	linkHint := ""
	if created {
		rel := fmt.Sprintf("tasks/%s.md", id)
		switch {
		case linkedWorktree(root):
			linkHint = fmt.Sprintf(", ссылку на docs/%s поправь в основном чекауте: taskctl file %s", rel, id)
		default:
			if want := fmt.Sprintf("[%s](%s)", rel, rel); row.Link != want {
				row.Link = want
				b.Lines[row.LineIdx] = formatRow(row)
				if err := b.Save(); err != nil {
					return "", err
				}
				paths = append(paths, filepath.Join("docs", "TASKS.md"))
			}
		}
	}
	rf, err := loadReview(taskFileAbs(root, id))
	if err != nil {
		return "", err
	}
	rf.ensureSection()
	rf.insert(rf.insertAt, "- "+note)
	if err := rf.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s: замечание %d записано", id, len(rf.notes)+1)
	if created {
		msg += ", файл задачи создан"
	}
	return msg + linkHint + tail, nil
}

var outcomeNames = map[string]string{"fixed": "исправлено", "rejected": "отклонено"}

func cmdReviewResolve(root, id string, num int, outcome, reason string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	word, ok := outcomeNames[outcome]
	if !ok {
		return "", fmt.Errorf("неизвестный исход %q, жду fixed или rejected", outcome)
	}
	reason = strings.TrimSpace(reason)
	if outcome == "rejected" && reason == "" {
		return "", fmt.Errorf("для rejected обязателен --reason: отклонение фиксируется с причиной")
	}
	rf, err := loadReview(taskFileAbs(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("у %s нет файла задачи, ревью пусто", id)
		}
		return "", err
	}
	if num < 1 || num > len(rf.notes) {
		return "", fmt.Errorf("замечания %d нет, в ревью %s их %d (номера смотрит review show)", num, id, len(rf.notes))
	}
	n := rf.notes[num-1]
	if o := n.outcome(); o != "" {
		return "", fmt.Errorf("замечание %d уже закрыто (%s): %s", num, o, n.Text)
	}
	line := "- " + n.Text + ": " + word
	if reason != "" {
		line += ", " + reason
	}
	// Многострочный элемент схлопываем в одну строку: loadReview собрал его
	// продолжения в n.Text, и строки переноса из файла уходят, иначе резолвнутая
	// строка повторяла бы их после себя.
	span := n.Span
	if span < 1 {
		span = 1
	}
	rf.lines = append(rf.lines[:n.LineIdx], append([]string{line}, rf.lines[n.LineIdx+span:]...)...)
	if err := rf.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "tasks", id+".md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: замечание %d %s", id, num, word) + tail, nil
}

func cmdReviewShow(root, id string) (string, error) {
	rf, err := loadReview(taskFileAbs(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("у %s нет файла задачи, ревью пусто", id)
		}
		return "", err
	}
	if len(rf.notes) == 0 {
		return fmt.Sprintf("в ревью %s замечаний нет", id), nil
	}
	var out []string
	for i, n := range rf.notes {
		st := n.outcome()
		if st == "" {
			st = "открыто"
		}
		out = append(out, fmt.Sprintf("%d. [%s] %s", i+1, st, n.Text))
	}
	return strings.Join(out, "\n"), nil
}

func cmdReviewStats(root string) (string, error) {
	type agg struct{ tasks, notes, fixed, rejected, open int }
	var live, arch agg
	var openList []string
	count := func(files []string, a *agg) error {
		for _, f := range files {
			rf, err := loadReview(f)
			if err != nil {
				return err
			}
			if len(rf.notes) == 0 {
				continue
			}
			a.tasks++
			for i, n := range rf.notes {
				if n.outcome() == "чисто" {
					continue
				}
				a.notes++
				switch n.outcome() {
				case "исправлено":
					a.fixed++
				case "отклонено":
					a.rejected++
				default:
					a.open++
					id := strings.TrimSuffix(filepath.Base(f), ".md")
					openList = append(openList, fmt.Sprintf("%s, замечание %d: %s", id, i+1, n.Text))
				}
			}
		}
		return nil
	}
	liveFiles, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "*.md"))
	archFiles, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "archive", "*", "*.md"))
	if err := count(liveFiles, &live); err != nil {
		return "", err
	}
	if err := count(archFiles, &arch); err != nil {
		return "", err
	}
	total := agg{live.tasks + arch.tasks, live.notes + arch.notes,
		live.fixed + arch.fixed, live.rejected + arch.rejected, live.open + arch.open}
	if total.tasks == 0 {
		return "разделов «Ревью» пока нет ни в живых задачах, ни в архиве", nil
	}
	out := []string{
		fmt.Sprintf("задач с ревью: %d (живых %d, в архиве %d)", total.tasks, live.tasks, arch.tasks),
		fmt.Sprintf("замечаний %d: исправлено %d, отклонено %d, открыто %d",
			total.notes, total.fixed, total.rejected, total.open),
	}
	if closed := total.fixed + total.rejected; closed > 0 {
		out = append(out, fmt.Sprintf("доля исправленных среди закрытых: %d%%", total.fixed*100/closed))
	}
	if len(openList) > 0 {
		out = append(out, "открытые:")
		out = append(out, openList...)
	}
	return strings.Join(out, "\n"), nil
}

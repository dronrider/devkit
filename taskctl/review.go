package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const reviewHeading = "## Ревью"

type reviewNote struct {
	LineIdx int
	Text    string // текст замечания без маркера списка
}

// outcome возвращает исход замечания («исправлено», «отклонено») или пустую
// строку у открытого. Критерий тот же, каким shipctl merge решает, закрыто ли
// ревью: слово исхода где-то в строке.
func (n reviewNote) outcome() string {
	low := strings.ToLower(n.Text)
	switch {
	case strings.Contains(low, "исправлено"):
		return "исправлено"
	case strings.Contains(low, "отклонено"):
		return "отклонено"
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
		if in && (strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")) {
			rf.notes = append(rf.notes, reviewNote{LineIdx: i, Text: strings.TrimSpace(t[2:])})
			rf.insertAt = i + 1
		}
	}
	return rf, nil
}

func (rf *reviewFile) ensureSection() {
	if rf.hasSection {
		return
	}
	for len(rf.lines) > 0 && strings.TrimSpace(rf.lines[len(rf.lines)-1]) == "" {
		rf.lines = rf.lines[:len(rf.lines)-1]
	}
	rf.lines = append(rf.lines, "", reviewHeading, "", "")
	rf.hasSection = true
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
	if b.find(id) == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	paths := []string{filepath.Join("docs", "tasks", id+".md")}
	created := false
	if _, err := os.Stat(taskFileAbs(root, id)); os.IsNotExist(err) {
		if _, err := cmdFile(root, id, CommitOpts{}); err != nil {
			return "", err
		}
		created = true
		paths = append(paths, filepath.Join("docs", "TASKS.md"))
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
	return msg + tail, nil
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
	rf.lines[n.LineIdx] = line
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

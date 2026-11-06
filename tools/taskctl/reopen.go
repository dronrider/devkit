package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

type ReopenParams struct {
	ID, Reason, Rank, Size, Accept string
	Commit                         CommitOpts
}

func archiveBoardRow(r *ArchRow) (*Row, bool, error) {
	if len(r.Cells) != 9 || r.Cells[6] == "-" {
		return nil, true, nil
	}
	title := r.Cells[1]
	kind := r.Cells[8]
	if kind != "agent" && kind != "mixed" && kind != "user" {
		return nil, false, fmt.Errorf("%s: неизвестный вид приёмки %q в архиве", r.ID, kind)
	}
	base, deps, debt, arm, plan, assign, due, control, _, fail, block := splitTitle(title)
	suf := ""
	if kind != "agent" {
		suf = " [приёмка: " + kind + "]"
	}
	title = joinTitle(base, deps, debt, arm, plan, assign, due, control, suf, fail, block)
	row, _, err := parseBoardRow("| " + strings.Join([]string{r.ID, title, r.Cells[2], r.Cells[3], r.Cells[6], r.Cells[7], "[tasks/" + r.ID + ".md](tasks/" + r.ID + ".md)"}, " | ") + " |")
	return row, false, err
}

func cmdReopen(root string, p ReopenParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "reopen"); err != nil {
		return "", err
	}
	if strings.TrimSpace(p.Reason) == "" {
		return "", fmt.Errorf("возврату обязателен --reason: taskctl reopen %s --reason \"почему нужна доработка\"", p.ID)
	}
	if err := checkReason(p.Reason); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	a, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	if b.find(p.ID) != nil {
		return "", fmt.Errorf("%s уже на доске: taskctl show %s; статус меняется taskctl move", p.ID, p.ID)
	}
	ar := a.find(p.ID)
	if ar == nil {
		return "", fmt.Errorf("%s нет в архиве: проверьте ID через taskctl show %s", p.ID, p.ID)
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	if findDraft(drafts, p.ID) != nil {
		return "", fmt.Errorf("%s также есть в черновиках: сначала устраните дубликат через taskctl draft", p.ID)
	}
	count := 0
	for _, x := range a.Rows {
		if x.ID == p.ID {
			count++
		}
	}
	if count != 1 {
		return "", fmt.Errorf("%s имеет дубликаты в архиве: восстановите единственную строку из git перед reopen", p.ID)
	}
	row, legacy, err := archiveBoardRow(ar)
	if err != nil {
		return "", err
	}
	if legacy {
		if p.Rank == "" || p.Size == "" || p.Accept == "" {
			return "", fmt.Errorf("%s: старый архив не хранит ранг, цену и вид; укажите проверенные значения: taskctl reopen %s --reason \"...\" --rank <а+б+в+г+д> --size <S|M|L|XL> --accept <agent|mixed|user>", p.ID, p.ID)
		}
		if p.Size == "-" {
			return "", fmt.Errorf("укажите оцененную цену --size S|M|L|XL")
		}
		total, _, e := parseRank(p.Rank)
		if e != nil {
			return "", e
		}
		copyRow := *ar
		copyRow.Cells = append(append([]string{}, ar.Cells[:6]...), fmt.Sprintf("%d (%s)", total, p.Rank), p.Size, p.Accept)
		row, _, err = archiveBoardRow(&copyRow)
		if err != nil {
			return "", err
		}
	} else if p.Rank != "" || p.Size != "" || p.Accept != "" {
		return "", fmt.Errorf("%s: метаданные есть в архиве; измените их после возврата через taskctl set", p.ID)
	}
	rel, ok := archiveTaskFile(root, p.ID, ar.Cells[4])
	if !ok {
		return "", fmt.Errorf("%s: файл задачи отсутствует; восстановите его из git перед reopen", p.ID)
	}
	src := filepath.Join(root, rel)
	dst := taskFilePath(root, p.ID)
	if src != dst {
		if _, e := os.Stat(dst); !os.IsNotExist(e) {
			return "", fmt.Errorf("%s уже существует: устраните дубликат перед reopen", dst)
		}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	unchecked := false
	if err = cliReadOutside(root, func() error { unchecked = uncheckedDeploy(root, data); return nil }); err != nil {
		return "", err
	}
	if unchecked {
		return "", fmt.Errorf("%s: выкат не проверен; сначала выполните сценарий проверки и shipctl smoke %s", p.ID, p.ID)
	}
	row.Sect = SectBacklog
	if err = insertRowLine(b, b.Sects[SectBacklog], row, formatRow(row)); err != nil {
		return "", err
	}
	b, err = rehydrateReopened(root, b, src, dst)
	if err != nil {
		return "", err
	}
	paths := []string{"docs/TASKS.md", "docs/TASKS-archive.md"}
	// Спутники и журнал возвращаются одним набором; коллизии проверяются до переноса.
	sources := []string{src}
	if src != dst {
		matches, e := filepath.Glob(filepath.Join(filepath.Dir(src), p.ID+"-*"))
		if e != nil {
			return "", e
		}
		sources = append(sources, matches...)
		journal := filepath.Join(filepath.Dir(src), p.ID+".review.md")
		if _, e := os.Stat(journal); e == nil {
			sources = append(sources, journal)
		}
		for _, old := range sources[1:] {
			fi, e := os.Stat(old)
			if e != nil {
				return "", e
			}
			if fi.IsDir() {
				return "", fmt.Errorf("спутник %s это каталог, переносите отдельно", old)
			}
			target := filepath.Join(filepath.Dir(dst), filepath.Base(old))
			if _, e := os.Stat(target); !os.IsNotExist(e) {
				return "", fmt.Errorf("%s уже существует: устраните дубликат перед reopen", target)
			}
		}
		for _, old := range sources {
			target := filepath.Join(filepath.Dir(dst), filepath.Base(old))
			staged, e := gitMv(root, old, target)
			if e != nil {
				return "", e
			}
			oldRel, _ := filepath.Rel(root, old)
			newRel, _ := filepath.Rel(root, target)
			if staged {
				paths = append(paths, oldRel)
			}
			paths = append(paths, newRel)
			if filepath.Ext(target) == ".md" {
				if e := rewriteLinksInFile(target, filepath.Dir(old), filepath.Dir(target)); e != nil {
					return "", e
				}
			}
		}
	}
	data, err = os.ReadFile(dst)
	if err != nil {
		return "", err
	}
	text := string(data)
	if src != dst {
		text = strings.ReplaceAll(text, filepath.ToSlash(filepath.Dir(rel))+"/"+p.ID+"-", "docs/tasks/"+p.ID+"-")
	}
	line := fmt.Sprintf("- Возврат из архива %s: %s (закрыта %s). <!-- taskctl:reopen -->", time.Now().Format("2006-01-02"), p.Reason, ar.Cells[4])
	text = stage.InsertIntoSection(text, stageSection, line)
	if err = os.WriteFile(dst, []byte(text), 0o644); err != nil {
		return "", err
	}
	paths = append(paths, filepath.Join("docs", "tasks", p.ID+".md"))
	a.Lines = append(a.Lines[:ar.LineIdx], a.Lines[ar.LineIdx+1:]...)
	if err = os.WriteFile(a.Path, []byte(strings.Join(a.Lines, "\n")), 0o644); err != nil {
		return "", err
	}
	if err = b.Save(); err != nil {
		return "", err
	}
	if src != dst {
		for _, old := range sources {
			target := filepath.Join(filepath.Dir(dst), filepath.Base(old))
			changed, e := findAndRewriteReferencesToFile(root, old, target)
			if e != nil {
				return "", e
			}
			paths = append(paths, changed...)
		}
	}
	tail, err := p.Commit.apply(root, paths)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s возвращена в Backlog, возвратов: %d%s\nдальше: taskctl show %s", p.ID, reopenCountFile(dst), tail, p.ID), nil
}

var reopenMarkRe = regexp.MustCompile(`(?m)^- Возврат из архива [0-9]{4}-[0-9]{2}-[0-9]{2}: [^\n]* <!-- taskctl:reopen -->$`)

func reopenCountFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	mask, _ := stage.FenceMask(lines)
	count := 0
	inHistory := false
	for i, line := range lines {
		if mask[i] {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			inHistory = line == stageSection
			continue
		}
		if inHistory && reopenMarkRe.MatchString(line) {
			count++
		}
	}
	return count
}
func cmdReopenStats(root string, jsonOut bool) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	a, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	files := map[string]string{}
	for _, r := range b.Rows {
		files[r.ID] = taskFilePath(root, r.ID)
	}
	for _, r := range a.Rows {
		if rel, ok := archiveTaskFile(root, r.ID, r.Cells[4]); ok {
			files[r.ID] = filepath.Join(root, rel)
		}
	}
	tasks, events := 0, 0
	for _, f := range files {
		n := reopenCountFile(f)
		if n > 0 {
			tasks++
			events += n
		}
	}
	if jsonOut {
		return marshal(struct {
			Tasks       int `json:"tasks"`
			ReopenCount int `json:"reopen_count"`
		}{tasks, events})
	}
	return fmt.Sprintf("возвращалось задач: %d; возвратов из архива: %d", tasks, events), nil
}

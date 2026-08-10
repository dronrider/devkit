package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func decodeBoard(t *testing.T, s string) jsonBoard {
	t.Helper()
	var b jsonBoard
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		t.Fatalf("вывод не разобрался как JSON: %v\n%s", err, s)
	}
	return b
}

func TestListJSONWholeBoard(t *testing.T) {
	root := setup(t)
	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	b := decodeBoard(t, out)
	if b.Prefix != "XR" {
		t.Errorf("prefix = %q, ожидал XR", b.Prefix)
	}
	if len(b.Sections) != 4 {
		t.Fatalf("секций %d, ожидал 4", len(b.Sections))
	}
	order := []string{SectInProgress, SectCheck, SectBacklog, SectBlocked}
	counts := []int{1, 0, 4, 0}
	for i, sec := range b.Sections {
		if sec.Key != order[i] {
			t.Errorf("секция %d: ключ %q, ожидал %q", i, sec.Key, order[i])
		}
		if len(sec.Rows) != counts[i] {
			t.Errorf("секция %s: строк %d, ожидал %d", sec.Key, len(sec.Rows), counts[i])
		}
		if sec.Rows == nil {
			t.Errorf("секция %s: rows null, ожидал пустой список", sec.Key)
		}
	}
	r := b.Sections[2].Rows[0]
	if r.ID != "XR-002" || r.Title != "Верхняя" || r.Type != "bug" || r.P != "P1" ||
		r.R != 55 || r.RParts != [5]int{50, 0, 0, 5, 0} || r.Cost != "-" ||
		r.Link != "[tasks/XR-002.md](tasks/XR-002.md)" {
		t.Errorf("XR-002 разобрана неверно: %+v", r)
	}
}

// list --json не режет Backlog: печатный list показывает первые 10 строк ради
// контекста агента, а машинный вывод отдаёт доску целиком.
func TestListJSONBacklogNotTruncated(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	sep := b.Sects[SectBacklog].SepIdx
	var extra []string
	for i := 0; i < 12; i++ {
		extra = append(extra, fmt.Sprintf("| XR-1%02d | Наполнитель %d | task | P3 | 5 (0+0+0+0+5) | S | - |", i, i))
	}
	b.insert(sep+1, extra...)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBoard(t, out)
	if n := len(got.Sections[2].Rows); n != 16 {
		t.Fatalf("в Backlog %d строк, ожидал все 16", n)
	}
	plain, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "первые 10") {
		t.Fatal("печатный list перестал резать Backlog, обрезка должна остаться")
	}
}

func TestListJSONSection(t *testing.T) {
	root := setup(t)
	out, err := cmdListJSON(root, "In progress")
	if err != nil {
		t.Fatal(err)
	}
	b := decodeBoard(t, out)
	if len(b.Sections) != 1 || b.Sections[0].Key != SectInProgress {
		t.Fatalf("ожидал одну секцию in-progress, получил %+v", b.Sections)
	}
	if _, err := cmdListJSON(root, "космос"); err == nil {
		t.Fatal("неизвестная секция должна давать ошибку")
	}
}

// Суффиксы заголовка разложены по полям, а не остаются в title.
func TestListJSONTitleSuffixes(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [после XR-002, XR-001] [провал: прод упал] [блок: ждём смежника]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "backlog")
	if err != nil {
		t.Fatal(err)
	}
	rows := decodeBoard(t, out).Sections[0].Rows
	var got *jsonRow
	for i := range rows {
		if rows[i].ID == "XR-004" {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatal("XR-004 не нашлась в backlog")
	}
	if got.Title != "Хвост" || strings.Join(got.After, ",") != "XR-002,XR-001" ||
		got.Fail != "прод упал" || got.Block != "ждём смежника" {
		t.Fatalf("суффиксы разобраны неверно: %+v", got)
	}
}

func TestShowJSONBoardRow(t *testing.T) {
	root := setup(t)
	out, err := cmdShowJSON(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	var got jsonShow
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.ID != "XR-005" || got.Sect != SectInProgress || got.R != 30 {
		t.Errorf("строка разобрана неверно: %+v", got)
	}
	if got.File != "docs/tasks/XR-005.md" {
		t.Errorf("file = %q, ожидал docs/tasks/XR-005.md", got.File)
	}
}

func TestShowJSONBlocks(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-001")
	row.Title += " [после XR-002]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := cmdShowJSON(root, "XR-002")
	if err != nil {
		t.Fatal(err)
	}
	var got jsonShow
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Blocks, ",") != "XR-001" {
		t.Fatalf("blocks = %v, ожидал [XR-001]", got.Blocks)
	}
}

func TestShowJSONArchiveAndMissing(t *testing.T) {
	root := setup(t)
	out, err := cmdShowJSON(root, "XR-007")
	if err != nil {
		t.Fatal(err)
	}
	var got jsonShow
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Sect != "archive" || got.Closed != "2026-06-12" || got.Title != "Закрытая" {
		t.Fatalf("архивная строка разобрана неверно: %+v", got)
	}
	if _, err := cmdShowJSON(root, "XR-404"); err == nil {
		t.Fatal("несуществующий ID должен давать ошибку")
	}
}

// Черновик в show --json несёт заголовок, как и печатный show: машинному
// читателю про черновик известно не меньше, чем человеку.
func TestShowJSONDraftTitle(t *testing.T) {
	root := setup(t)
	dir := filepath.Join(root, "docs", "tasks", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Идея про дашборд\n\nзаписан 2026-08-01\n\nтекст черновика\n"
	if err := os.WriteFile(filepath.Join(dir, "XR-009.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdShowJSON(root, "XR-009")
	if err != nil {
		t.Fatal(err)
	}
	var got jsonShow
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Sect != "draft" || got.File != "docs/tasks/drafts/XR-009.md" {
		t.Fatalf("черновик разобран неверно: %+v", got)
	}
	if got.Title != "Идея про дашборд" {
		t.Fatalf("title = %q, ожидал заголовок черновика", got.Title)
	}
}

func TestDepListJSON(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-001")
	row.Title += " [после XR-002]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDepListJSON(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	var one jsonDep
	if err := json.Unmarshal([]byte(out), &one); err != nil {
		t.Fatal(err)
	}
	if one.ID != "XR-001" || strings.Join(one.After, ",") != "XR-002" || len(one.Blocks) != 0 {
		t.Fatalf("dep одной задачи: %+v", one)
	}
	out, err = cmdDepListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var all struct {
		Deps []jsonDep `json:"deps"`
	}
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		t.Fatal(err)
	}
	if len(all.Deps) != 2 {
		t.Fatalf("ожидал две записи (XR-002 держит, XR-001 после), получил %+v", all.Deps)
	}
	if _, err := cmdDepListJSON(root, "XR-404"); err == nil {
		t.Fatal("несуществующий ID должен давать ошибку")
	}
}

// Флаг --json доезжает до команд через CLI, а не только через функции.
func TestJSONFlagWiring(t *testing.T) {
	root := setup(t)
	for _, args := range [][]string{
		{"list", "--json"},
		{"list", "backlog", "--json"},
		{"show", "XR-005", "--json"},
		{"dep", "list", "--json"},
	} {
		out, err := exec.Command("go", append([]string{"run", ".", "-C", root}, args...)...).Output()
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var v any
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatalf("%v: вывод не JSON: %v\n%s", args, err, out)
		}
	}
}

// Дата последней правки строки едет в --json отдельным полем: дашборд
// показывает её вместо возраста днями (DK-243), и вычисляться она обязана на
// сервере, а не гадаться клиентом.
func TestListJSONMovedDate(t *testing.T) {
	root := setup(t)
	at := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", ".")
	gitCommitDated(t, root, at, "init")

	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	want := at.Local().Format("2006-01-02")
	for _, sec := range decodeBoard(t, out).Sections {
		for _, r := range sec.Rows {
			if r.Moved != want {
				t.Errorf("%s: moved = %q, ожидал %q", r.ID, r.Moved, want)
			}
		}
	}
}

// Вне git-репозитория даты нет, и поле молчит: выдуманная дата хуже пустого
// места, там же молчит и возраст в notes.
func TestListJSONMovedSilentWithoutGit(t *testing.T) {
	root := setup(t)
	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range decodeBoard(t, out).Sections {
		for _, r := range sec.Rows {
			if r.Moved != "" {
				t.Errorf("%s: вне git moved = %q, ожидал пусто", r.ID, r.Moved)
			}
		}
	}
}

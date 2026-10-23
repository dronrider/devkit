package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bareOrigin заводит origin.git и пушет туда main из root, тем же способом,
// что laggedWorktree в stale_test.go.
func bareOrigin(t *testing.T, root string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, root, "init", "-q", "--bare", bare)
	gitOut(t, root, "remote", "add", "origin", bare)
	gitOut(t, root, "push", "-q", "origin", "main")
	return bare
}

// TestLintMainAheadWithCode: находка DK-602 называет число код-коммитов
// впереди origin и первый из них по имени, до отказа пуша это видно заранее.
func TestLintMainAheadWithCode(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	bareOrigin(t, root)
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "app.txt")
	gitOut(t, root, "commit", "-q", "-m", "fix: мелкий баг")

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			found = f
			break
		}
	}
	if found == "" {
		t.Fatalf("нет находки про main впереди origin: %v", finds)
	}
	if !strings.Contains(found, "1 коммит") || !strings.Contains(found, "fix: мелкий баг") {
		t.Fatalf("находка не называет число и коммит: %q", found)
	}
}

// TestLintMainAheadSilentOnBoardOnly: чистая доска впереди origin находки не
// даёт, регрессия того же рода, что и раньше у пуша: сообщение только про
// код.
func TestLintMainAheadSilentOnBoardOnly(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	bareOrigin(t, root)
	p := filepath.Join(root, "docs", "tasks", "XR-001.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, []byte("ход\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "docs(tasks): XR-001 ход")

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			t.Fatalf("чистая доска впереди origin не должна давать находку: %q", f)
		}
	}
}

// TestLintMainAheadSilentWithoutOrigin: без origin (или без опережения)
// находка молчит, а не падает.
func TestLintMainAheadSilentWithoutOrigin(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			t.Fatalf("без origin находки быть не должно: %q", f)
		}
	}
}

// TestLintMainAheadSilentOnCorpNestedBoard: чистая доска боковой директории
// контура (<проект>/docs/TASKS.md с привязкой tracker.local) впереди origin
// находки не даёт, как и домашняя (DK-796). Привязка лежит в базе, а в
// коммит впереди origin идёт одна доска.
func TestLintMainAheadSilentOnCorpNestedBoard(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	proj := filepath.Join(root, "authn")
	if err := os.MkdirAll(filepath.Join(proj, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proj, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "docs", "TASKS.md"), []byte("# доска\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "docs", "TASKS-archive.md"), []byte("| архив |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".devkit", "tracker.local"), []byte("repo = .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "corp: обвязка authn")
	bareOrigin(t, root)
	if err := os.WriteFile(filepath.Join(proj, "docs", "TASKS.md"), []byte("# доска\nход\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "docs", "TASKS-archive.md"), []byte("| архив |\n| AU-001 | сдана |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "chore(authn): доска проекта")

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			t.Fatalf("доска подкаталога контура не должна давать находку: %q", f)
		}
	}
}

// TestLintMainAheadCountsNestedStandBoard: вложенный docs/TASKS.md без привязки
// (стенд внутри обычного проекта) остаётся кодом и находку даёт.
func TestLintMainAheadCountsNestedStandBoard(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	bareOrigin(t, root)
	stand := filepath.Join(root, "tools", "obeycheck", "testdata", "project", "docs")
	if err := os.MkdirAll(stand, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stand, "TASKS.md"), []byte("# стенд\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "правка стенда с доской")

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			found = f
			break
		}
	}
	if found == "" {
		t.Fatal("правка стенда с доской должна давать находку как код")
	}
}

// standBoard и standArchive это минимальная пустая доска стенда: разделы на
// месте, строк нет. cmdLint на ней доходит до git-правил, а не отказывает в
// разборе, и ничего своего не приносит.
const standBoard = `# стенд: задачи (префикс OB)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

Нет.
`

const standArchive = "# стенд: архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
	"|--------|--------|-----|---|---------|--------|\n"

// writeStandBoard кладёт в dir пустую доску стенда с архивом.
func writeStandBoard(t *testing.T, dir string) {
	t.Helper()
	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(docs, "TASKS.md"):         standBoard,
		filepath.Join(docs, "TASKS-archive.md"): standArchive,
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// aheadCodeCommit ставит в main репозитория root незапушенный код-коммит поверх
// origin: состояние «забытый пуш», которое должно видеть только lint корня
// этого репозитория.
func aheadCodeCommit(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "app.txt")
	gitOut(t, root, "commit", "-q", "-m", "fix: мелкий баг")
}

// TestLintMainAheadSilentOnNestedStand: доска стенда без своего гита не судит
// опережение main объемлющего репозитория (DK-1333). Фикстура obeycheck лежит
// в дереве devkit, git от неё поднимается к репозиторию носителя, и прогон
// слияния в свежем дереве worktree краснел от незапушенного кода автора
// сессии: TestFixtureProjectPassesLint находил чужой main впереди origin.
// Тот же незапушенный код виден lint у корня репозитория, как и раньше.
func TestLintMainAheadSilentOnNestedStand(t *testing.T) {
	root := setup(t)
	stand := filepath.Join(root, "tools", "obeycheck", "testdata", "project")
	writeStandBoard(t, stand)
	gitSetup(t, root)
	bareOrigin(t, root)
	aheadCodeCommit(t, root)

	finds, err := cmdLint(stand)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			t.Fatalf("стенд без своего гита не судит опережение объемлющего репозитория: %q", f)
		}
	}

	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			found = f
			break
		}
	}
	if found == "" {
		t.Fatal("незапушенный код в main должен находиться lint у корня репозитория")
	}
}

// TestLintMainAheadJudgesCorpSideDir: боковая директория контура с привязкой
// tracker.local (DK-796) вложена в свой репозиторий подкаталогом, и его
// опережение она судит, как и доска на вершине: послабление для стендов не
// сняло правило с контура.
func TestLintMainAheadJudgesCorpSideDir(t *testing.T) {
	root := setup(t)
	side := filepath.Join(root, "authn")
	writeStandBoard(t, side)
	if err := os.MkdirAll(filepath.Join(side, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(side, ".devkit", "tracker.local"), []byte("repo = .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSetup(t, root)
	bareOrigin(t, root)
	aheadCodeCommit(t, root)

	finds, err := cmdLint(side)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			found = f
			break
		}
	}
	if found == "" {
		t.Fatal("доска боковой директории контура должна судить опережение своего репозитория")
	}
}

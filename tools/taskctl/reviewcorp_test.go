package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Ревью в корп-контуре: корень доски приходит редиректом и живёт своей
// историей, а строка уровня несёт вершину дерева кода (DK-753). Команда тут
// зовётся собранной утилитой, а не функцией: проверяется вся дорога от флага
// -C до строки в файле задачи, и сам выбор дерева живёт как раз на ней.

// corpReviewStand поднимает контур под команды ревью: боковая доска с задачами
// и своей историей, клон кода с редиректом и разошедшейся вершиной.
func corpReviewStand(t *testing.T) (clone, local string) {
	t.Helper()
	_, clone, local = corpClone(t)
	corpWrite(t, boardPath(local), fixtureBoard)
	corpWrite(t, archivePath(local), fixtureArchive)
	corpWrite(t, filepath.Join(local, "docs", "tasks", "XR-005.md"), "# XR-005\n"+fixtureScenario+fixtureVerification)
	corpGitT(t, local, "init", "-q", "-b", "main")
	corpGitT(t, local, "config", "user.email", "test@test")
	corpGitT(t, local, "config", "user.name", "test")
	corpGitT(t, local, "add", ".")
	corpGitT(t, local, "commit", "-q", "-m", "доска")
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")
	corpWrite(t, filepath.Join(clone, "code.txt"), "правка ревью\n")
	corpGitT(t, clone, "add", ".")
	corpGitT(t, clone, "commit", "-q", "-m", "правка")
	if corpHead(t, clone) == corpHead(t, local) {
		t.Fatal("вершины клона и доски сошлись, стенд бракован")
	}
	return clone, local
}

func corpHead(t *testing.T, dir string) string {
	t.Helper()
	return corpGitT(t, dir, "rev-parse", "--short=7", "HEAD")
}

// corpLevel зовёт «taskctl review level» из директории dir.
func corpLevel(t *testing.T, dir, id, level, reason string) {
	t.Helper()
	out, err := exec.Command("go", "run", ".", "-C", dir, "review", "level", id, level, reason).CombinedOutput()
	if err != nil {
		t.Fatalf("review level из %s: %v\n%s", dir, err, out)
	}
}

func corpTaskText(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(taskFileAbs(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestReviewLevelCorpTakesCodeHead: ревьювер стоит в клоне кода, и вершина
// боковой доски в строку уровня не попадает. От вершины доски дифф второго
// круга не строится вовсе, и до правки строка несла именно её.
func TestReviewLevelCorpTakesCodeHead(t *testing.T) {
	clone, local := corpReviewStand(t)
	corpLevel(t, clone, "XR-005", "2", "тронут разбор диффа")

	got := corpTaskText(t, local, "XR-005")
	if !strings.Contains(got, "Уровень 2 до "+corpHead(t, clone)+":") {
		t.Fatalf("в строке уровня нет вершины кода %s:\n%s", corpHead(t, clone), got)
	}
	if strings.Contains(got, corpHead(t, local)) {
		t.Fatalf("в строке уровня вершина боковой доски %s:\n%s", corpHead(t, local), got)
	}
}

// TestReviewLevelCorpFromSubdir: команду зовут из поддиректории клона, и
// вершина берётся та же. Ловит мутацию «дерево кода это только корень клона».
func TestReviewLevelCorpFromSubdir(t *testing.T) {
	clone, local := corpReviewStand(t)
	sub := filepath.Join(clone, "sub", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	corpLevel(t, sub, "XR-005", "1", "рутина")

	got := corpTaskText(t, local, "XR-005")
	if !strings.Contains(got, "Уровень 1 до "+corpHead(t, clone)+":") {
		t.Fatalf("в строке уровня не вершина клона:\n%s", got)
	}
}

// TestReviewLevelCorpFromBoardDir: команду позвали из самой боковой доски.
// Редирект оттуда не виден, и дерево кода называет привязка tracker.local.
func TestReviewLevelCorpFromBoardDir(t *testing.T) {
	clone, local := corpReviewStand(t)
	corpWrite(t, filepath.Join(local, corpTrackerPath), "key = XR\nrepo = "+clone+"\n")
	corpLevel(t, local, "XR-005", "2", "тронут разбор диффа")

	got := corpTaskText(t, local, "XR-005")
	if !strings.Contains(got, "Уровень 2 до "+corpHead(t, clone)+":") {
		t.Fatalf("привязка не дала вершину кода:\n%s", got)
	}
}

// TestReviewLevelHomeKeepsBoardHead: домашний проект правка не трогает. Корень
// доски там же, где код, и вершина у них одна.
func TestReviewLevelHomeKeepsBoardHead(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	corpLevel(t, root, "XR-005", "2", "тронут tools/shipctl")

	got := corpTaskText(t, root, "XR-005")
	if !strings.Contains(got, "Уровень 2 до "+corpHead(t, root)+":") {
		t.Fatalf("в строке уровня не вершина проекта:\n%s", got)
	}
}

// TestReviewDraftHeadFollowsLevel: шапка файла замечаний своего источника не
// имеет, она читает строку уровня. Тест держит эту связь: вершина кода
// доезжает до шапки, и второй круг диффует от неё же.
func TestReviewDraftHeadFollowsLevel(t *testing.T) {
	clone, local := corpReviewStand(t)
	corpLevel(t, clone, "XR-005", "2", "тронут разбор диффа")
	if _, err := cmdReviewDraft(local, "XR-005", "гонка в close", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(reviewDraftAbs(local, "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ревью до: "+corpHead(t, clone)) {
		t.Fatalf("шапка замечаний не несёт вершину кода:\n%s", got)
	}
}

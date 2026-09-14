package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkProject(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	board := "# Тест: доска (префикс XR)\n\n## In progress\n\nНет.\n\n## Check\n\nНет.\n\n## Backlog\n\nНет.\n\n## Blocked\n\nНет.\n"
	if err := os.WriteFile(filepath.Join(dir, "docs", "TASKS.md"), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanProjects(t *testing.T) {
	root := t.TempDir()
	mkProject(t, filepath.Join(root, "alpha"))
	mkProject(t, filepath.Join(root, "beta"))
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Проект глубже прямого подкаталога не ищется.
	mkProject(t, filepath.Join(root, "plain", "deep"))

	projects, errs := scanProjects([]string{root}, newWorktreeMemo())
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "alpha,beta" {
		t.Errorf("проекты %v, ожидал alpha,beta", names)
	}
	if len(errs) != 0 {
		t.Errorf("не ждал ошибок: %v", errs)
	}
}

func TestScanProjectsRootItself(t *testing.T) {
	root := t.TempDir()
	mkProject(t, root)
	projects, _ := scanProjects([]string{root}, newWorktreeMemo())
	if len(projects) != 1 || projects[0].Path != root {
		t.Fatalf("корень с доской должен быть проектом: %v", projects)
	}
}

func TestScanProjectsMissingRoot(t *testing.T) {
	_, errs := scanProjects([]string{"/нет/такого/корня"}, newWorktreeMemo())
	if len(errs) != 1 || !strings.Contains(errs[0], "нет") {
		t.Fatalf("пропавший корень должен быть назван, получил %v", errs)
	}
}

// Коллизия имён из разных корней это ошибка конфига: оба проекта выпадают и
// называются, а не молча берётся первый попавшийся.
func TestScanProjectsNameCollision(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	mkProject(t, filepath.Join(rootA, "same"))
	mkProject(t, filepath.Join(rootB, "same"))
	projects, errs := scanProjects([]string{rootA, rootB}, newWorktreeMemo())
	if len(projects) != 0 {
		t.Errorf("проект-двойник не должен показываться: %v", projects)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "same") {
		t.Errorf("коллизия должна быть названа: %v", errs)
	}
}

// Зависший git не держит обход корней: gitLine идёт через runProc со сроком, а
// обход стоит за /api/projects и открытым /healthz. Молчание при этом не
// считается ответом «дерева нет»: под нагрузкой так в список проектов въезжали
// боковые деревья, опрос досок множился на их число и раскручивал нагрузку сам
// от себя (DK-992). Прежнего вердикта у каталога нет, значит он считается
// деревом, а причина едет в /healthz.
func TestScanProjectsHungGit(t *testing.T) {
	root := t.TempDir()
	mkProject(t, filepath.Join(root, "proj"))
	bin := t.TempDir()
	writeScript(t, bin, "git", "sleep 60")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := procTimeout
	procTimeout = 200 * time.Millisecond
	t.Cleanup(func() { procTimeout = old })

	start := time.Now()
	projects, errs := scanProjects([]string{root}, newWorktreeMemo())
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("обход занял %v: срок подпроцесса git не сработал", took)
	}
	if len(projects) != 0 {
		t.Fatalf("проекты %v: молчащий git пустил каталог в список, и опрос досок умножится на боковые деревья", projects)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "git не сказал про боковое дерево") {
		t.Fatalf("причины в /healthz нет: %v", errs)
	}
}

// Каталог без репозитория остаётся проектом: ненулевой код git это ответ, а не
// молчание, и отличать одно от другого обязан сам отсев.
func TestScanProjectsGitRefusalIsAnswer(t *testing.T) {
	root := t.TempDir()
	mkProject(t, filepath.Join(root, "proj"))
	bin := t.TempDir()
	writeScript(t, bin, "git", "echo 'not a git repository' >&2; exit 128")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	projects, errs := scanProjects([]string{root}, newWorktreeMemo())
	if len(projects) != 1 || len(errs) != 0 {
		t.Fatalf("проекты %v, ошибки %v: отказ git должен читаться ответом «дерева нет»", projects, errs)
	}
}

// Молчание git не меняет прежнего вердикта: каталог, который обход уже признал
// проектом, остаётся проектом и на круге, где git не ответил. Иначе пик
// нагрузки выметал бы со стартовой все проекты разом.
func TestScanProjectsSilentGitKeepsOldVerdict(t *testing.T) {
	root := t.TempDir()
	mkProject(t, filepath.Join(root, "proj"))
	bin := t.TempDir()
	writeScript(t, bin, "git", "printf '.git\n.git\n'")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	memo := newWorktreeMemo()
	if projects, _ := scanProjects([]string{root}, memo); len(projects) != 1 {
		t.Fatalf("первый обход дал %v, жду один проект", projects)
	}

	writeScript(t, bin, "git", "sleep 60")
	old := procTimeout
	procTimeout = 200 * time.Millisecond
	t.Cleanup(func() { procTimeout = old })
	projects, errs := scanProjects([]string{root}, memo)
	if len(projects) != 1 {
		t.Fatalf("проекты %v: молчание git сняло прежний вердикт обхода", projects)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "по прежнему обходу") {
		t.Fatalf("причины в /healthz нет: %v", errs)
	}
}

// Буквальный ход инцидента 2026-09-14: боковые деревья задач были опознаны
// прежним обходом, а под нагрузкой git замолчал по сроку. Прежний вердикт
// «дерево» держится, и в список проектов такое дерево не возвращается, иначе
// опрос досок снова умножился бы на число деревьев.
func TestScanProjectsSilentGitKeepsTreeVerdict(t *testing.T) {
	root := t.TempDir()
	mkProject(t, filepath.Join(root, "proj-dk-605"))
	bin := t.TempDir()
	writeScript(t, bin, "git", "printf '/repo/.git/worktrees/dk-605\n/repo/.git\n'")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	memo := newWorktreeMemo()
	if projects, errs := scanProjects([]string{root}, memo); len(projects) != 0 || len(errs) != 0 {
		t.Fatalf("первый обход дал проекты %v и ошибки %v, жду отсеянное дерево", projects, errs)
	}

	writeScript(t, bin, "git", "sleep 60")
	old := procTimeout
	procTimeout = 200 * time.Millisecond
	t.Cleanup(func() { procTimeout = old })
	projects, errs := scanProjects([]string{root}, memo)
	if len(projects) != 0 {
		t.Fatalf("проекты %v: молчащий git вернул опознанное дерево в список, и опрос досок умножился", projects)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "боковым деревом по прежнему обходу") {
		t.Fatalf("причины в /healthz нет: %v", errs)
	}
}

// Память вердиктов не растёт заброшенными путями: слитая задача уносит своё
// боковое дерево, и вердикта по нему на следующем обходе не остаётся.
func TestWorktreeMemoForgetsGoneDirs(t *testing.T) {
	root := t.TempDir()
	side := filepath.Join(root, "proj-dk-1")
	mkProject(t, filepath.Join(root, "proj"))
	mkProject(t, side)
	bin := t.TempDir()
	writeScript(t, bin, "git", "printf '.git\n.git\n'")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	memo := newWorktreeMemo()
	scanProjects([]string{root}, memo)
	if _, ok := memo.recall(side); !ok {
		t.Fatal("вердикта по каталогу нет после обхода: чистка сняла живой путь")
	}

	if err := os.RemoveAll(side); err != nil {
		t.Fatal(err)
	}
	scanProjects([]string{root}, memo)
	if _, ok := memo.recall(side); ok {
		t.Fatalf("вердикт по снесённому дереву %s остался: память растёт заброшенными путями", side)
	}
}

// Боковое дерево задачи (linked worktree) несёт ту же доску и отсеивается,
// иначе каждый проект множился бы на свои деревья.
func TestScanProjectsSkipsLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "proj")
	mkProject(t, main)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", main,
			"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	git("worktree", "add", "-q", filepath.Join(root, "proj-dk-1"), "-b", "dk-1")

	projects, errs := scanProjects([]string{root}, newWorktreeMemo())
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "proj" {
		t.Errorf("ожидал только proj, получил %v (ошибки %v)", names, errs)
	}
}

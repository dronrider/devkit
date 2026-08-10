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

	projects, errs := scanProjects([]string{root})
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
	projects, _ := scanProjects([]string{root})
	if len(projects) != 1 || projects[0].Path != root {
		t.Fatalf("корень с доской должен быть проектом: %v", projects)
	}
}

func TestScanProjectsMissingRoot(t *testing.T) {
	_, errs := scanProjects([]string{"/нет/такого/корня"})
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
	projects, errs := scanProjects([]string{rootA, rootB})
	if len(projects) != 0 {
		t.Errorf("проект-двойник не должен показываться: %v", projects)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "same") {
		t.Errorf("коллизия должна быть названа: %v", errs)
	}
}

// Зависший git не держит обход корней: gitLine идёт через runProc со сроком,
// а обход стоит за /api/projects и открытым /healthz. Git молчит, значит
// признака worktree нет и каталог остаётся проектом, а не висит навсегда.
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
	projects, errs := scanProjects([]string{root})
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("обход занял %v: срок подпроцесса git не сработал", took)
	}
	if len(projects) != 1 || len(errs) != 0 {
		t.Fatalf("проекты %v, ошибки %v: молчащий git не должен ронять обход", projects, errs)
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

	projects, errs := scanProjects([]string{root})
	var names []string
	for _, p := range projects {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "proj" {
		t.Errorf("ожидал только proj, получил %v (ошибки %v)", names, errs)
	}
}

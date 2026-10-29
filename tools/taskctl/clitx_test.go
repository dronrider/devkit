package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/drafttx"
)

func TestCLIPublicationReleasesLockBeforeHook(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	source, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "hook-ran")
	script := "#!/bin/sh\ncd " + shQuote(source) + "\ngo run . -C " + shQuote(root) + " show XR-001 >/dev/null || exit 1\nprintf done > " + shQuote(marker) + "\n"
	writeHook(t, root, "pre-commit", script)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-C", root, "draft", "--prio", "mid", "-m", "docs(tasks): XR черновик", "тестовая идея")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("draft commit: %v\n%s", err, out)
	}
	if ctx.Err() != nil {
		t.Fatal("hook deadlocked")
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "коммит") {
		t.Fatalf("no commit result: %s", out)
	}
}

func TestCLIDraftDropStagesAfterUnlock(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdDraft(root, "старый черновик", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "--", draftRel("XR-008"))
	gitOut(t, root, "commit", "-m", "fixture")
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	marker := filepath.Join(bindir, "nested")
	script := "#!/bin/sh\ncase \" $* \" in *\" rm \"*) " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; touch " + shQuote(marker) + ";; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "draft", "drop", "XR-008", "--reason", "устарел")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("git wrapper deadlocked on nested taskctl")
	}
	if err != nil {
		t.Fatalf("drop: %v %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("nested taskctl did not run: %v", err)
	}
	if got := gitOut(t, root, "diff", "--cached", "--name-status"); !strings.Contains(got, "D\t"+draftRel("XR-008")) {
		t.Fatalf("index: %s", got)
	}
}

func TestCLIDraftDropReportsIndexFailureAfterRemoval(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdDraft(root, "старый черновик", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "--", draftRel("XR-008"))
	gitOut(t, root, "commit", "-m", "fixture")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" rm \"*) echo broken >&2; exit 3;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := runCLI(t, "-C", root, "draft", "drop", "XR-008", "--reason", "устарел")
	if err == nil || !strings.Contains(out, "применено, индекс не обновлён") {
		t.Fatalf("missing index error: %v %s", err, out)
	}
	if _, err := os.Stat(draftPath(root, "XR-008")); !os.IsNotExist(err) {
		t.Fatalf("removal not applied: %v", err)
	}
}

func TestCLIDropUntrackedDoesNotPublish(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdDraft(root, "новый черновик", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	head := gitOut(t, root, "rev-parse", "HEAD")
	out, err := runCLI(t, "-C", root, "draft", "drop", "XR-008", "--reason", "устарел", "-m", "docs(tasks): XR-008 удалён")
	if err != nil {
		t.Fatalf("drop: %v %s", err, out)
	}
	if got := gitOut(t, root, "rev-parse", "HEAD"); got != head {
		t.Fatalf("untracked removal committed: %s", got)
	}
	if _, err := os.Stat(draftPath(root, "XR-008")); !os.IsNotExist(err) {
		t.Fatalf("draft still exists: %v", err)
	}
}

func TestCLIBoardGuardDoesNotCallGitUnderLock(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	marker := filepath.Join(bindir, "nested")
	script := "#!/bin/sh\ncase \" $* \" in *\" rev-parse \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; touch " + shQuote(marker) + "; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "draft", "--prio", "mid", "идея")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("git rev-parse deadlocked on nested taskctl")
	}
	if err != nil {
		t.Fatalf("draft: %v %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("nested taskctl did not run: %v", err)
	}
}

func TestCLIAddMovesTrackedDraftAfterUnlock(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdDraft(root, "исходный черновик", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "--", draftRel("XR-008"))
	gitOut(t, root, "commit", "-m", "fixture")
	giveDraftDoD(t, root, "XR-008")
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	marker := filepath.Join(bindir, "nested")
	script := "#!/bin/sh\ncase \" $* \" in *\" update-index \"*|*\" mv \"*) " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; touch " + shQuote(marker) + ";; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "add", "--id", "XR-008", "--title", "Оформленная", "--type", "bug", "--rank", "25+4+2+5+2", "--cost", "S", "--accept", "agent")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("git wrapper deadlocked on nested taskctl")
	}
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("nested taskctl did not run: %v", err)
	}
	if got := gitOut(t, root, "diff", "--cached", "--name-status", "--no-renames"); !strings.Contains(got, "D\t"+draftRel("XR-008")) || !strings.Contains(got, "A\tdocs/tasks/XR-008.md") {
		t.Fatalf("index: %s", got)
	}
}

func TestCLIAddPromotesUntrackedDraftWithCommit(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdDraft(root, "черновик", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	out, err := runCLI(t, "-C", root, "add", "--id", "XR-008", "--title", "Оформленная", "--type", "bug", "--rank", "25+4+2+5+2", "--cost", "S", "--accept", "agent", "-m", "docs(tasks): XR-008 оформлена")
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	files := gitOut(t, root, "show", "--name-only", "--pretty=")
	if !strings.Contains(files, "docs/tasks/XR-008.md") || strings.Contains(files, draftRel("XR-008")) {
		t.Fatalf("commit files: %s", files)
	}
}

func TestCLIPublicationFailureKeepsAppliedDraft(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	writeHook(t, root, "pre-commit", "#!/bin/sh\nexit 1\n")
	out, err := runCLI(t, "-C", root, "draft", "--prio", "mid", "-m", "docs(tasks): XR черновик", "тестовая идея")
	if err == nil || !strings.Contains(out, "применено, публикация не завершена") {
		t.Fatalf("publication: %v %s", err, out)
	}
	if _, err = os.Stat(draftPath(root, "XR-008")); err != nil {
		t.Fatalf("applied file missing: %v", err)
	}
}

func TestCLIDraftLocksBeforeAllocatingID(t *testing.T) {
	root := setup(t)
	lock, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-C", root, "draft", "--prio", "mid", "новая идея")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		lock.Close()
		t.Fatalf("finished before lock released: %v %s", err, output.String())
	case <-time.After(300 * time.Millisecond):
	}
	path := draftPath(root, "XR-020")
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("# XR-020: другая запись\n"), 0644); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil || !strings.Contains(output.String(), "XR-021") {
		t.Fatalf("allocation: %v %s", err, output.String())
	}
}

func TestCLINotifyCanCallTaskctl(t *testing.T) {
	root := setup(t)
	bin := filepath.Join(t.TempDir(), "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	dk := t.TempDir()
	hooks := filepath.Join(dk, "hooks")
	if err := os.MkdirAll(hooks, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	script := "import subprocess\nsubprocess.run([" + strconv.Quote(bin) + ", '-C', " + strconv.Quote(root) + ", 'show', 'XR-005'], check=True, timeout=5)\nopen(" + strconv.Quote(marker) + ", 'w').write('ok')\n"
	if err := os.WriteFile(filepath.Join(hooks, "notify.py"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", dk)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "move", "XR-005", "blocked", "--reason", "проверка")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("move: %v %s", err, out)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatalf("notify: %v %s", err, out)
	}
}

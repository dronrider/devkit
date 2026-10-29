package main

import (
	"bytes"
	"context"
	"fmt"
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

func TestCLICloseDrainAllowsNestedTaskctl(t *testing.T) {
	root := setup(t)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	marker := filepath.Join(t.TempDir(), "drain")
	writeShipStub(t, shQuote(bin)+" -C "+shQuote(root)+" show XR-001 >/dev/null || exit 2\ntouch "+shQuote(marker)+"\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-C", root, "close", "XR-005", "--date", "2026-08-26").CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("drain deadlocked on nested taskctl")
	}
	if err != nil {
		t.Fatalf("close: %v %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("drain did not finish: %v %s", err, out)
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

func TestCLIShowResolvesMainRootBeforeLock(t *testing.T) {
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
	script := "#!/bin/sh\ncase \" $* \" in *\" --path-format=absolute \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " id >/dev/null || exit 2; touch " + shQuote(marker) + "; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "show", "XR-001")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("main root git lookup deadlocked on nested taskctl")
	}
	if err != nil {
		t.Fatalf("show: %v %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("nested taskctl did not run: %v", err)
	}
}

func TestCLIShowRejectsEditDuringEdgeLookup(t *testing.T) {
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
	script := "#!/bin/sh\ncase \" $* \" in *\" --verify --quiet main \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " id >/dev/null || exit 2; printf '\\nconcurrent edge edit\\n' >> " + shQuote(taskFileAbs(root, "XR-001")) + "; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-C", root, "show", "XR-001").CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("edge git lookup deadlocked on nested taskctl")
	}
	if err == nil || !strings.Contains(string(out), "изменился") {
		t.Fatalf("expected stale conflict: %v %s", err, out)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-001"))
	if err != nil || !strings.Contains(string(data), "concurrent edge edit") {
		t.Fatalf("edit lost: %v %s", err, data)
	}
}

func TestCLIShowRejectsMainTreeEditFromLinkedWorktree(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	wt := addWorktree(t, root, "xr-777")
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" --verify --quiet main \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " draft --prio mid 'concurrent main draft' >/dev/null || exit 2; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-C", wt, "show", "XR-001").CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("main tree writer deadlocked")
	}
	if err == nil || !strings.Contains(string(out), "изменился") {
		t.Fatalf("main change not detected: %v %s", err, out)
	}
	if _, err := os.Stat(draftPath(root, "XR-008")); err != nil {
		t.Fatalf("main draft missing: %v", err)
	}
}

func TestCLISpendRejectsEditDuringCalculation(t *testing.T) {
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
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	script := "#!/bin/sh\ncase \" $* \" in *\" --git-common-dir \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then if [ -f " + shQuote(marker) + " ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; printf '\\nconcurrent spend edit\\n' >> " + shQuote(taskFileAbs(root, "XR-001")) + "; else touch " + shQuote(marker) + "; fi; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-C", root, "spend", "XR-001").CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("nested taskctl deadlocked during spend")
	}
	if err == nil || !strings.Contains(string(out), "изменился") {
		t.Fatalf("expected conflict: %v %s", err, out)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-001"))
	if err != nil || !strings.Contains(string(data), "concurrent spend edit") {
		t.Fatalf("edit lost: %v %s", err, data)
	}
}

func TestCLIReviewLevelRejectsEditDuringGit(t *testing.T) {
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
	script := "#!/bin/sh\ncase \" $* \" in *\" --short=7 HEAD \"*) " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; printf '\\nconcurrent review edit\\n' >> " + shQuote(taskFileAbs(root, "XR-001")) + ";; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "review", "level", "XR-001", "2", "проверка")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("nested taskctl deadlocked during review git")
	}
	if err == nil || !strings.Contains(string(out), "изменился") {
		t.Fatalf("expected conflict: %v %s", err, out)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-001"))
	if err != nil || !strings.Contains(string(data), "concurrent review edit") || strings.Contains(string(data), "Уровень 2 до") {
		t.Fatalf("review edit: %v %s", err, data)
	}
}

func TestCLIReviewAddRejectsEditDuringTreeCheck(t *testing.T) {
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
	script := "#!/bin/sh\ncase \" $* \" in *\" --verify main \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; printf '\\nconcurrent tree edit\\n' >> " + shQuote(taskFileAbs(root, "XR-001")) + "; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "review", "add", "XR-001", "новое замечание")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("nested taskctl deadlocked during tree check")
	}
	if err == nil || !strings.Contains(string(out), "изменился") {
		t.Fatalf("expected conflict: %v %s", err, out)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-001"))
	if err != nil || !strings.Contains(string(data), "concurrent tree edit") || strings.Contains(string(data), "новое замечание") {
		t.Fatalf("tree edit: %v %s", err, data)
	}
}

func TestCLIReviewLevelRejectsChangedProjectPath(t *testing.T) {
	first := setup(t)
	gitSetup(t, first)
	second := setup(t)
	gitSetup(t, second)
	link := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" --short=7 HEAD \"*) rm " + shQuote(link) + " && ln -s " + shQuote(second) + " " + shQuote(link) + " || exit 2;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := exec.Command(bin, "-C", link, "review", "level", "XR-001", "2", "проверка").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "путь проекта изменился") {
		t.Fatalf("changed path accepted: %v %s", err, out)
	}
	for _, root := range []string{first, second} {
		data, err := os.ReadFile(taskFileAbs(root, "XR-001"))
		if err != nil || strings.Contains(string(data), "Уровень 2 до") {
			t.Fatalf("unexpected write in %s: %v", root, err)
		}
	}
}

func TestWakePreflightReleasesLockAndRejectsStaleRow(t *testing.T) {
	root := setup(t)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	old := wakePreflight
	t.Cleanup(func() { wakePreflight = old })
	wakePreflight = func(root, id string, _ runOpts) error {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "-C", root, "show", id).CombinedOutput()
		if ctx.Err() != nil {
			return fmt.Errorf("nested taskctl deadlocked")
		}
		if err != nil {
			return fmt.Errorf("nested taskctl: %v %s", err, out)
		}
		f, err := os.OpenFile(taskFileAbs(root, id), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString("\nconcurrent wake edit\n")
		return err
	}
	before := sect(t, root, "XR-003")
	msg, ok := wakeRow(root, waiter{ID: "XR-003", Class: classArm}, wakeOpts{})
	if ok || !strings.Contains(msg, "изменился") {
		t.Fatalf("expected conflict: %v %s", ok, msg)
	}
	if got := sect(t, root, "XR-003"); got != before {
		t.Fatalf("stale row moved: %s -> %s", before, got)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-003"))
	if err != nil || !strings.Contains(string(data), "concurrent wake edit") {
		t.Fatalf("edit lost: %v %s", err, data)
	}
}

func TestWakeSelectionReleasesLockAndRejectsConcurrentEdit(t *testing.T) {
	root := setup(t)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	old := waiterSources
	t.Cleanup(func() { waiterSources = old })
	var probeErr error
	waiterSources = []waiterSource{func(root string, _ *Board, _ *Archive) ([]waiter, []string) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "-C", root, "show", "XR-003").CombinedOutput()
		if ctx.Err() != nil {
			probeErr = fmt.Errorf("nested taskctl deadlocked")
			return nil, nil
		}
		if err != nil {
			probeErr = fmt.Errorf("nested taskctl: %v %s", err, out)
			return nil, nil
		}
		f, err := os.OpenFile(taskFileAbs(root, "XR-003"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			probeErr = err
			return nil, nil
		}
		_, probeErr = f.WriteString("\nconcurrent selection edit\n")
		f.Close()
		return []waiter{{ID: "XR-003", Class: classArm, Met: true}}, nil
	}}
	before := sect(t, root, "XR-003")
	_, _, err := cmdWake(root, nil, wakeOpts{})
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	if err == nil || !strings.Contains(err.Error(), "изменился") {
		t.Fatalf("expected conflict: %v", err)
	}
	if got := sect(t, root, "XR-003"); got != before {
		t.Fatalf("stale row moved: %s -> %s", before, got)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-003"))
	if err != nil || !strings.Contains(string(data), "concurrent selection edit") {
		t.Fatalf("edit lost: %v %s", err, data)
	}
}

func TestWakeDoesNotRecordRefusalFromStaleSelection(t *testing.T) {
	root := armStand(t, 0)
	if _, err := cmdArm(root, "XR-003", "", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := waiterSources
	t.Cleanup(func() { waiterSources = old })
	waiterSources = []waiterSource{armedWaiters, func(root string, _ *Board, _ *Archive) ([]waiter, []string) {
		f, err := os.OpenFile(taskFileAbs(root, "XR-003"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("\nconcurrent change\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return nil, nil
	}}
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err == nil || !strings.Contains(err.Error(), "изменился") {
		t.Fatalf("expected stale selection: %v", err)
	}
	if refusal, ok := readArmRefusal(root, "XR-003"); ok {
		t.Fatalf("stale refusal was saved: %+v", refusal)
	}
}

func TestCLIMoveRejectsEditDuringBranchGate(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	markRehearsed(t, root)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" --verify main \"*) if [ \"$IN_GIT_PROBE\" != 1 ]; then IN_GIT_PROBE=1 " + shQuote(bin) + " -C " + shQuote(root) + " show XR-001 >/dev/null || exit 2; printf '\\nconcurrent gate edit\\n' >> " + shQuote(taskFileAbs(root, "XR-005")) + "; fi;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", root, "move", "XR-005", "check")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("nested taskctl deadlocked during branch gate")
	}
	if err == nil || !strings.Contains(string(out), "изменился") {
		t.Fatalf("expected conflict: %v %s", err, out)
	}
	if got := sect(t, root, "XR-005"); got != SectInProgress {
		t.Fatalf("row moved to %s", got)
	}
	data, err := os.ReadFile(taskFileAbs(root, "XR-005"))
	if err != nil || !strings.Contains(string(data), "concurrent gate edit") {
		t.Fatalf("edit lost: %v %s", err, data)
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

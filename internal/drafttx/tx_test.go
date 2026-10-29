package drafttx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoveryAfterEveryFile(t *testing.T) {
	for boundary := 0; boundary < 3; boundary++ {
		t.Run(string(rune('0'+boundary)), func(t *testing.T) {
			root := t.TempDir()
			var changes []Change
			for _, n := range []string{"a", "b", "source"} {
				p := filepath.Join(root, n)
				if err := os.WriteFile(p, []byte("before"), 0644); err != nil {
					t.Fatal(err)
				}
				before, err := Read(p)
				if err != nil {
					t.Fatal(err)
				}
				changes = append(changes, Change{Path: n, Before: before, After: Image{Exists: true, Data: []byte("after-" + n), Mode: 0644}})
			}
			l, err := Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			err = l.apply(root, changes, func(i int) error {
				if i == boundary {
					return errors.New("crash")
				}
				return nil
			})
			if err == nil {
				t.Fatal("no interruption")
			}
			l.Close()
			l, err = Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			l.Close()
			for _, c := range changes {
				got, err := Read(filepath.Join(root, c.Path))
				if err != nil || !equal(got, c.After) {
					t.Fatalf("%s: %+v %v", c.Path, got, err)
				}
			}
			l, err = Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			l.Close()
		})
	}
}

func TestRecoveryConflictKeepsForeignEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := Read(path)
	changes := []Change{{Path: "source", Before: before, After: Image{Exists: true, Data: []byte("after"), Mode: 0644}}}
	l, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	// A durable plan with no installed files models a crash before first rename.
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := plan{Version: 1, Root: canonicalRoot, Changes: changes}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomic(filepath.Join(l.dir, "pending.json"), Image{Exists: true, Data: data, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err = os.WriteFile(path, []byte("foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Acquire(root); err == nil || !strings.Contains(err.Error(), "конфликт") {
		t.Fatalf("conflict: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "foreign" {
		t.Fatalf("foreign overwritten: %q %v", got, err)
	}
}

func TestLockSerializesTransactions(t *testing.T) {
	root := t.TempDir()
	first, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		second, err := Acquire(root)
		if err == nil {
			err = second.Close()
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		t.Fatalf("second lock entered early: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lock did not proceed")
	}
}

func TestInterruptedPlanCannotBeReplaced(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one")
	if err := os.WriteFile(path, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := Read(path)
	change := Change{Path: "one", Before: before, After: Image{Exists: true, Data: []byte("b"), Mode: 0600}}
	l, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.apply(root, []Change{change}, func(int) error { return errors.New("crash") }); err == nil {
		t.Fatal("no crash")
	}
	if err = l.Apply(root, nil); err == nil {
		t.Fatal("pending plan replaced")
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "b" {
		t.Fatalf("result %q %v", got, err)
	}
}

func TestAcquireDoesNotCreateMissingProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := Acquire(root); err == nil {
		t.Fatal("missing project accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("project created: %v", err)
	}
}

func TestWorktreesKeepRecoveryInTheirOwnRoot(t *testing.T) {
	base := t.TempDir()
	common := filepath.Join(base, "common")
	bin := filepath.Join(base, "bin")
	for _, dir := range []string{common, bin} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nprintf '%s\\n' '"+common+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "source"), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := Acquire(a)
	if err != nil {
		t.Fatal(err)
	}
	before, err := Read(filepath.Join(a, "source"))
	if err != nil {
		t.Fatal(err)
	}
	change := Change{Path: "source", Before: before, After: Image{Exists: true, Data: []byte("after"), Mode: 0600}}
	if err = l.apply(a, []Change{change}, func(int) error { return errors.New("crash") }); err == nil {
		t.Fatal("no crash")
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Acquire(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Apply(a, nil); err == nil {
		t.Fatal("foreign root accepted")
	}
	other.Close()
	got, err := os.ReadFile(filepath.Join(b, "source"))
	if err != nil || string(got) != "before" {
		t.Fatalf("other worktree: %q %v", got, err)
	}
	l, err = Acquire(a)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	got, err = os.ReadFile(filepath.Join(a, "source"))
	if err != nil || string(got) != "after" {
		t.Fatalf("recovery: %q %v", got, err)
	}
}

func TestRecoveryRejectsForeignRoot(t *testing.T) {
	root, foreign := t.TempDir(), t.TempDir()
	l, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan{Version: 1, Root: foreign})
	if err != nil {
		t.Fatal(err)
	}
	if err = atomic(filepath.Join(l.dir, "pending.json"), Image{Exists: true, Data: data, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err = Acquire(root); err == nil {
		t.Fatal("foreign plan accepted")
	}
}

func TestLegacyPlanFailsClosedOrRecoversOwnRoot(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	l, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "item")
	if err = os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(serviceDir(root), "pending.json")
	p := plan{Version: 1, Root: other, Changes: []Change{{Path: "item", Before: before, After: Image{Exists: true, Data: []byte("after"), Mode: 0600}}}}
	data, _ := json.Marshal(p)
	if err = os.WriteFile(legacy, data, 0600); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err = Acquire(root); err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("foreign root: %v", err)
	}
	got, _ := os.ReadFile(legacy)
	if string(got) != string(data) {
		t.Fatal("foreign journal changed")
	}
	if err = os.WriteFile(legacy, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Acquire(root); err == nil {
		t.Fatal("bad journal accepted")
	}
	got, _ = os.ReadFile(legacy)
	if string(got) != "bad" {
		t.Fatal("bad journal changed")
	}
	p.Root = root
	data, _ = json.Marshal(p)
	if err = os.WriteFile(legacy, data, 0600); err != nil {
		t.Fatal(err)
	}
	l, err = Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	got, _ = os.ReadFile(path)
	if string(got) != "after" {
		t.Fatalf("recovery: %q", got)
	}
	if _, err = os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("journal remains: %v", err)
	}
}

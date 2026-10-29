package drafttx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	p := plan{Version: 1, Root: root, Changes: changes}
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

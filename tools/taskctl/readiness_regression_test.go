package main

import (
	"os"
	"strings"
	"testing"
)

func TestAddRejectsUnwrittenTask(t *testing.T) {
	for _, typ := range []string{"task", "bug"} {
		t.Run(typ, func(t *testing.T) {
			root := setup(t)
			before, _ := os.ReadFile(boardPath(root))
			_, err := cmdAdd(root, AddParams{Title: "Без описания", Type: typ, Rank: "0+1+1+0+1", Accept: "agent"})
			if err == nil {
				t.Fatal("add принял заголовок вместо постановки")
			}
			for _, h := range []string{"Что происходит", "Чего хотим", "DoD", "Ранг", "TASKFORM.md"} {
				if !strings.Contains(err.Error(), h) {
					t.Errorf("нет %s: %v", h, err)
				}
			}
			after, _ := os.ReadFile(boardPath(root))
			if string(after) != string(before) {
				t.Fatal("доска изменилась после отказа")
			}
			if _, err := os.Stat(taskFileAbs(root, "XR-008")); !os.IsNotExist(err) {
				t.Fatal("файл задачи появился после отказа")
			}
		})
	}
}

func TestPromoteRejectsEmptySectionsBeforeMove(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "Сырая мысль\n\n## DoD\n\n- [ ]\n", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(boardPath(root))
	draft, _ := os.ReadFile(draftPath(root, "XR-008"))
	_, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Постановка", Type: "task", Rank: "0+1+1+0+1", Accept: "agent"})
	if err == nil {
		t.Fatal("оформление пустых разделов прошло")
	}
	after, _ := os.ReadFile(boardPath(root))
	saved, _ := os.ReadFile(draftPath(root, "XR-008"))
	if string(after) != string(before) || string(saved) != string(draft) {
		t.Fatal("отказ изменил доску или черновик")
	}
	if _, err := os.Stat(taskFileAbs(root, "XR-008")); !os.IsNotExist(err) {
		t.Fatal("черновик перенесён при отказе")
	}
}

func TestReadinessLintAndMove(t *testing.T) {
	root := setup(t)
	if err := os.WriteFile(taskFileAbs(root, "XR-004"), []byte("# XR-004\n## DoD\n<!-- заполнить -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(boardPath(root))
	for _, dry := range []bool{true, false} {
		var err error
		if dry {
			_, err = cmdMoveDry(root, "XR-004", SectInProgress)
		} else {
			_, err = cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{})
		}
		if err == nil || !strings.Contains(err.Error(), "не заполнены") {
			t.Fatalf("dry=%v: %v", dry, err)
		}
	}
	after, _ := os.ReadFile(boardPath(root))
	if string(after) != string(before) {
		t.Fatal("move изменил доску")
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(finds, "\n"), "XR-004: не заполнены") {
		t.Fatalf("lint пропустил пустую карточку: %v", finds)
	}
}

package main

import (
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// TestTaskEffortFromStageRecord: taskEffort отдаёт уровень глубины из записи
// этапа исполнителя, а строке без записи и этапу без уровня отдаёт пусто
// (DK-1570, макет показа уровня).
func TestTaskEffortFromStageRecord(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	s := newServer(&Config{Home: home}, nil, nil)
	now := time.Now()

	if err := stage.Put(home, root, "XR-001", stage.Stage{
		Kind: stage.Dev, Start: now, Session: "tmux-1",
		Note: "opus/high по вердикту pick, работа X",
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.taskEffort(root, "XR-001"); got != "high" {
		t.Fatalf("taskEffort с записью этапа вернул %q, ждал high", got)
	}
	// Строки без записи этапа нет вовсе: чип без уровня не рисуется.
	if got := s.taskEffort(root, "XR-002"); got != "" {
		t.Fatalf("taskEffort без записи вернул %q, ждал пусто", got)
	}
	// Запись есть, но этап исполнителя уровня не назвал.
	if err := stage.Put(home, root, "XR-002", stage.Stage{
		Kind: stage.Dev, Start: now, Session: "tmux-1",
		Note: "работа без вердикта",
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.taskEffort(root, "XR-002"); got != "" {
		t.Fatalf("taskEffort этапа без уровня вернул %q, ждал пусто", got)
	}
}

// TestTaskRowCarriesEffort: ручка задачи отдаёт уровень записи этапа полем
// row.effort, а без записи поле пропадает из ответа (omitempty).
func TestTaskRowCarriesEffort(t *testing.T) {
	e := newTestEnv(t)
	if err := stage.Put(e.home, e.proj, "XR-005", stage.Stage{
		Kind: stage.Dev, Start: time.Now(), Session: "tmux-1",
		Note: "opus/high по вердикту pick, работа X",
	}); err != nil {
		t.Fatal(err)
	}
	task := getTask(t, e.loggedClient(t), e, "XR-005")
	effort, _ := taskRowField(t, task, "effort").(string)
	if effort != "high" {
		t.Fatalf("row.effort = %q, ждал high", effort)
	}
	// Строка без записи этапа: поля effort в ответе нет вовсе.
	task2 := getTask(t, e.loggedClient(t), e, "XR-002")
	if v := taskRowField(t, task2, "effort"); v != nil {
		t.Fatalf("row.effort без записи = %v, ждал отсутствие поля", v)
	}
}

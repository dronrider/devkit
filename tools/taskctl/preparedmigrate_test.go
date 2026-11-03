package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestPreparedMigrateRecordsAndAsks(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	if _, err = cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraft(root, "без разбора", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	state := draftproposal.State{Version: 1, State: "active", Analysis: "готово", Proposals: []draftproposal.Proposal{
		{ID: "p1", Revision: 1, Outcome: "drop", Text: "устаревшее", Reason: "лишнее"},
		{ID: "p2", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
	}}
	record, err := draftproposal.Replace([]byte("# XR-008: исходник\n\n## Текст\n\nматериал записи\n"), state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(draftPath(root, "XR-008"), record, 0644); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	old := chat.PreparedQuestion{Version: 1, Tree: root, Anchor: "XR-008", Sources: []chat.PreparedSource{{Source: "XR-008", Basis: view.Basis}}}
	for _, p := range state.Proposals {
		old.Sources[0].Proposals = append(old.Sources[0].Proposals, p)
	}
	ask := chat.Ask{Task: "XR-008", Questions: []chat.Question{{Text: "Карточки подготовлены. Проверьте описания и подтвердите или отклоните их.", Multi: true, Proposal: &old}}}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), ask); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftPreparedMigrate(root)
	if err != nil {
		t.Fatal(err)
	}
	var moved migrateResult
	if err = json.Unmarshal([]byte(out), &moved); err != nil || len(moved.Records) != 1 || moved.Records[0] != "XR-008" || len(moved.Asks) != 1 {
		t.Fatalf("migrate %s %v", out, err)
	}
	data, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Предложения разбора") || strings.Contains(string(data), "```json") || !strings.Contains(string(data), "материал записи") {
		t.Fatalf("блок не срезан из записи: %s", data)
	}
	prepared, err := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	if err != nil || !strings.Contains(string(prepared), "## p2 - Результат") {
		t.Fatalf("prepared-файл: %s %v", prepared, err)
	}
	if _, err = os.Stat(draftproposal.PreparedPath(root, "XR-009")); !os.IsNotExist(err) {
		t.Fatal("запись без разбора получила prepared-файл")
	}
	rewritten, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008")))
	if !ok || len(rewritten.Questions) != 1 {
		t.Fatal("ask пропал")
	}
	q := rewritten.Questions[0]
	if q.Prepared == nil || q.Proposal != nil || !strings.Contains(q.Text, "Подготовил задачи, согласуй их описание.") || !strings.Contains(q.Text, "/draft/XR-008/prepared/p2") {
		t.Fatalf("ask не переписан: %+v", q)
	}
	if strings.Contains(q.Text, "Тестовый запуск обнаружил пропуск") || q.Prepared.Sources[0].Proposals[1].Text != "" {
		t.Fatal("тела задач остались в ask")
	}
	raw, err = cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var after preparedView
	if err = json.Unmarshal([]byte(raw), &after); err != nil || after.Basis != view.Basis {
		t.Fatalf("basis пережил миграцию: %s против %s (%v)", after.Basis, view.Basis, err)
	}
	again, err := cmdDraftPreparedMigrate(root)
	if err != nil || strings.Contains(again, "XR-008") {
		t.Fatalf("повторная миграция не пуста: %s %v", again, err)
	}
}

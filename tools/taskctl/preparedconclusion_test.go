package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func conclusionFixture(t *testing.T) (string, preparedView) {
	t.Helper()
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "Исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	return root, conclusionView(t, root)
}

func conclusionView(t *testing.T, root string) preparedView {
	t.Helper()
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func conclusionRequest(view preparedView) []byte {
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: view.Basis, Revision: view.State.PackRevision}}}
	for _, p := range view.State.Proposals {
		if p.Status == "" {
			req.Decisions = append(req.Decisions, draftproposal.Decision{Source: view.ID, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
		}
	}
	data, _ := json.Marshal(req)
	return data
}

func TestConclusionLegacyReadyWithoutSave(t *testing.T) {
	for _, carrier := range []string{"prepared", "inline"} {
		t.Run(carrier, func(t *testing.T) {
			root, _ := conclusionFixture(t)
			state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Reason: "Уже не нужно"}}}
			if carrier == "prepared" {
				if err := draftproposal.SavePrepared(root, "XR-008", state); err != nil {
					t.Fatal(err)
				}
			} else {
				record, _ := os.ReadFile(draftPath(root, "XR-008"))
				data, err := draftproposal.Replace(record, state)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(draftPath(root, "XR-008"), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := collectDocs(root)
			view := conclusionView(t, root)
			after, _ := collectDocs(root)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("чтение записало старый разбор")
			}
			if !view.Ready || view.State.PackRevision != 1 || view.State.PreparedSource == "" {
				t.Fatalf("без Save не готов: %+v", view)
			}
			data := conclusionRequest(view)
			if _, err := cmdDraftPreparedConfirm(root, data, CommitOpts{}); err != nil {
				t.Fatal(err)
			}
			raw, err := cmdDraftPreparedConfirm(root, data, CommitOpts{})
			if err != nil || !strings.Contains(raw, `"repeated":true`) {
				t.Fatalf("replay: %s %v", raw, err)
			}
			_, got, err := readPrepared(root, "XR-008")
			if err != nil || got.State != "history" || got.PackRevision != 1 {
				t.Fatalf("history: %+v %v", got, err)
			}
		})
	}
}

func TestConclusionCompleteAddAttachAndReasonOnlyDrops(t *testing.T) {
	root, view := conclusionFixture(t)
	for _, text := range []string{"Протухшее первое", "Протухшее второе"} {
		if _, err := cmdDraft(root, text, "mid", CommitOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	proposals := []draftproposal.Proposal{
		{ID: "p1", Revision: 1, Outcome: "add", Title: "Нужная задача", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
		{ID: "p2", Revision: 1, Outcome: "attach", Target: "XR-001", Text: "Материал приписки"},
		{ID: "p3", Revision: 1, Outcome: "drop", Target: "XR-009", Reason: "Дубликат"},
		{ID: "p4", Revision: 1, Outcome: "drop", Target: "XR-010", Reason: "Устарело"},
	}
	input, _ := json.Marshal(map[string]any{"proposals": proposals, "complete": true})
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatalf("готовый состав требует фиктивный Text: %v", err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Ready {
		t.Fatalf("не готов: %s", raw)
	}
	for _, p := range view.State.Proposals {
		if p.Outcome != "add" && p.BoardID != "" {
			t.Fatalf("действию выдан номер задачи: %+v", p)
		}
	}
	p := view.State.Proposals[0]
	p.Revision++
	p.Title = "Уточнённая задача"
	input, _ = json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{p}, "complete": true})
	if _, err = cmdDraftPreparedSave(root, view.ID, view.Snapshot, input); err != nil {
		t.Fatal(err)
	}
	view = conclusionView(t, root)
	data := conclusionRequest(view)
	if _, err = cmdDraftPreparedConfirm(root, data, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"XR-009", "XR-010"} {
		if _, err = os.Stat(draftPath(root, id)); !os.IsNotExist(err) {
			t.Fatalf("адресная цель %s не удалена: %v", id, err)
		}
	}
	attached, _ := os.ReadFile(taskFileAbs(root, "XR-001"))
	if !strings.Contains(string(attached), "Материал приписки") {
		t.Fatal("приписка не применена")
	}
	if _, err = os.Stat(taskFileAbs(root, view.State.Proposals[0].BoardID)); err != nil {
		t.Fatal(err)
	}
	before, _ := collectDocs(root)
	raw, err = cmdDraftPreparedConfirm(root, data, CommitOpts{})
	after, _ := collectDocs(root)
	if err != nil || !strings.Contains(raw, `"repeated":true`) || !reflect.DeepEqual(before, after) {
		t.Fatalf("replay: %s %v", raw, err)
	}
}

func TestConclusionAddressedDropSnapshotAndAtomicFailure(t *testing.T) {
	root, view := conclusionFixture(t)
	if _, err := cmdDraft(root, "Цель удаления", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"proposals":[{"id":"drop","revision":1,"outcome":"drop","target":"XR-009","reason":"лишнее"}],"complete":true}`)
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if err = os.WriteFile(draftPath(root, "XR-009"), []byte("# Цель изменилась\nтип: task\nисточник: thought\nзамок: unlocked\nприоритет: mid\n\nНовая мысль\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := collectDocs(root)
	if _, err = cmdDraftPreparedConfirm(root, conclusionRequest(view), CommitOpts{}); err == nil || !strings.Contains(err.Error(), "устарел") {
		t.Fatalf("изменённая цель принята: %v", err)
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("stale изменил файлы")
	}
	view = conclusionView(t, root)
	input = []byte(`{"proposals":[{"id":"bad","revision":1,"outcome":"drop"}],"complete":true}`)
	before, _ = collectDocs(root)
	if _, err = cmdDraftPreparedSave(root, view.ID, view.Snapshot, input); err == nil || strings.Count(err.Error(), "bad:") != 1 {
		t.Fatalf("нет единственного ID ошибки: %v", err)
	}
	after, _ = collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ошибка последнего пункта изменила файлы")
	}
}

func TestConclusionAbsentDropIsShownAndNotReceiptReplay(t *testing.T) {
	root, view := conclusionFixture(t)
	input := []byte(`{"conclusion":"Убрать устаревшее, новых задач не нужно.","proposals":[{"id":"drop","revision":1,"outcome":"drop","target":"XR-099","reason":"лишнее"}],"complete":true}`)
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "уже отсутствует") {
		t.Fatalf("отсутствие не показано: %s", raw)
	}
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedConfirm(root, conclusionRequest(view), CommitOpts{})
	if err != nil || strings.Contains(raw, `"repeated":true`) || !strings.Contains(raw, "удаление не требуется") {
		t.Fatalf("неточный replay: %s %v", raw, err)
	}
}

func TestConclusionHistoricalTargetIsShownAndPreserved(t *testing.T) {
	root, view := conclusionFixture(t)
	if _, err := cmdDraft(root, "Уже завершённая цель", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	receipt := draftproposal.Receipt{Source: "XR-009", Key: "previous", Proposal: "old", Revision: 1, Outcome: "drop", Result: "Прежде подтверждено"}
	state := draftproposal.State{Version: 1, State: "history", Proposals: []draftproposal.Proposal{{ID: "old", Revision: 1, Outcome: "drop", Reason: "Лишнее", Status: "accepted", Result: &receipt}}, Receipts: []draftproposal.Receipt{receipt}}
	if err := draftproposal.SavePrepared(root, "XR-009", state); err != nil {
		t.Fatal(err)
	}
	history, _ := os.ReadFile(draftproposal.PreparedPath(root, "XR-009"))
	input := []byte(`{"proposals":[{"id":"drop","revision":1,"outcome":"drop","target":"XR-009","reason":"лишнее"}],"complete":true}`)
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil || !strings.Contains(raw, "уже отсутствует в активных черновиках") {
		t.Fatalf("history не показана: %s %v", raw, err)
	}
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedConfirm(root, conclusionRequest(view), CommitOpts{})
	if err != nil || strings.Contains(raw, `"repeated":true`) {
		t.Fatalf("новый результат не применён: %s %v", raw, err)
	}
	after, _ := os.ReadFile(draftproposal.PreparedPath(root, "XR-009"))
	if string(after) != string(history) {
		t.Fatal("историческая квитанция цели изменена")
	}
	if _, err = os.Stat(draftPath(root, "XR-009")); err != nil {
		t.Fatal("исторический исходник цели удалён")
	}
}

func TestConclusionModernIncompleteAndNewIdeasRemainBlocked(t *testing.T) {
	root, view := conclusionFixture(t)
	input := []byte(`{"proposals":[{"id":"drop","revision":1,"outcome":"drop","reason":"лишнее"}]}`)
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.Ready || view.State.PackRevision != 1 || view.State.PreparedSource != "" {
		t.Fatalf("современный неполный пакет стал готов: %s", raw)
	}
	_, state, _ := readPrepared(root, view.ID)
	state.PackRevision = 0
	if err = draftproposal.SavePrepared(root, view.ID, state); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftIdea(root, view.ID, "Новая неразобранная мысль", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	view = conclusionView(t, root)
	if view.Ready {
		t.Fatal("legacy с новыми мыслями стал готов")
	}
}

func TestConclusionChangedTextRejectsShownChatAgreement(t *testing.T) {
	root, view := conclusionFixture(t)
	input := []byte(`{"conclusion":"Предлагаю убрать лишнее.","proposals":[{"id":"drop","revision":1,"outcome":"drop","reason":"лишнее"}],"complete":true}`)
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	shown := conclusionRequest(view)
	input = []byte(`{"conclusion":"Предлагаю другое обоснование.","complete":true}`)
	if _, err = cmdDraftPreparedSave(root, view.ID, view.Snapshot, input); err != nil {
		t.Fatal(err)
	}
	before, _ := collectDocs(root)
	if _, err = cmdDraftPreparedConfirm(root, shown, CommitOpts{}); err == nil {
		t.Fatal("старое согласие приняло новое заключение")
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("старое согласие изменило файлы")
	}
}

func TestConclusionStageErrorHasOneID(t *testing.T) {
	root, view := conclusionFixture(t)
	record, _, err := readPrepared(root, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "bad", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "mixed", Barrier: "неизвестный"}}}
	if err = validatePreparedBundle(root, view.ID, record, state); err == nil || strings.Count(err.Error(), "bad:") != 1 {
		t.Fatalf("идентификатор ошибки повторился: %v", err)
	}
}

func TestConclusionDeferPreservesSourcePrivacy(t *testing.T) {
	for _, targetLock := range []string{"projection", "unlocked", "locked"} {
		t.Run(targetLock, func(t *testing.T) {
			root, _ := conclusionFixture(t)
			if _, err := cmdDraftMeta(root, "XR-008", "", "", "locked", CommitOpts{}); err != nil {
				t.Fatal(err)
			}
			source, _ := os.ReadFile(draftPath(root, "XR-008"))
			if h, err := draftmeta.Parse(source); err != nil || h.Lock != "locked" {
				t.Fatalf("закрытый исходник: %+v %v", h, err)
			}
			p := draftproposal.Proposal{ID: "defer-private", Revision: 1, Outcome: "defer", Reason: "Закрытый довод отсрочки", Text: "Закрытая выдержка"}
			var original []byte
			if targetLock != "projection" {
				if _, err := cmdDraft(root, "Материал назначения", "high", CommitOpts{}); err != nil {
					t.Fatal(err)
				}
				p.Target = "XR-009"
				if _, err := cmdDraftMeta(root, p.Target, "", "", targetLock, CommitOpts{}); err != nil {
					t.Fatal(err)
				}
				original, _ = os.ReadFile(draftPath(root, p.Target))
			}
			view := conclusionView(t, root)
			payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{p}, "complete": true})
			raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, payload)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(raw), &view); err != nil {
				t.Fatal(err)
			}
			if p.Target != "" {
				staged, _ := os.ReadFile(draftPath(root, p.Target))
				if string(staged) != string(original) {
					t.Fatal("подготовка изменила живое назначение")
				}
			}
			req := conclusionRequest(view)
			raw, err = cmdDraftPreparedConfirm(root, req, CommitOpts{})
			if err != nil {
				t.Fatal(err)
			}
			var result confirmationResult
			if err = json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			target := result.Results[0].IDs[0]
			data, err := os.ReadFile(draftPath(root, target))
			if err != nil {
				t.Fatal(err)
			}
			h, err := draftmeta.Parse(data)
			if err != nil || h.Lock != "locked" || !strings.Contains(string(data), p.Reason) {
				t.Fatalf("назначение потеряло закрытый довод или защиту: %+v %v\n%s", h, err, data)
			}
			if original != nil {
				before, _ := draftmeta.Parse(original)
				if h.Type != before.Type || h.Source != before.Source || h.Priority != before.Priority || !strings.Contains(string(data), "Материал назначения") {
					t.Fatalf("потеряны поля назначения: %+v -> %+v", before, h)
				}
			}
			if replay, err := cmdDraftPreparedConfirm(root, req, CommitOpts{}); err != nil || !strings.Contains(replay, `"repeated":true`) {
				t.Fatalf("повтор квитанции: %s %v", replay, err)
			}
			after, _ := os.ReadFile(draftPath(root, target))
			if string(after) != string(data) {
				t.Fatal("повтор изменил закрытое назначение")
			}
		})
	}
}

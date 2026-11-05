package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

// assignedKey подставляет выданный номер вместо присланного ключа карточки:
// после сохранения карточка живёт полным ключом (DK-1564, решение 6).
func assignedKey(view preparedView, sent string) string {
	if id, ok := view.Assigned[sent]; ok {
		return id
	}
	return sent
}

// draftIDFromMsg вырезает ID из ответа cmdDraft: команда печатает человеку
// сообщение целиком, а тесту нужен сам номер.
func draftIDFromMsg(msg string) string {
	id, _, _ := strings.Cut(msg, ":")
	return strings.TrimSpace(id)
}

func TestPreparedSaveAndStale(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
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
	input := []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"add","text":"первая"},{"id":"p2","revision":1,"outcome":"drop","reason":"лишнее"}],"analysis":"готово"}`)
	saved, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(saved), &view); err != nil || len(view.State.Proposals) != 2 {
		t.Fatalf("%s: %v", saved, err)
	}
	first := view.Assigned["p1"]
	if !idRe.MatchString(first) || view.State.Proposals[0].ID != first {
		t.Fatalf("новая задача не получила номер доски: %s", saved)
	}
	record, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record), "Предложения разбора") || strings.Contains(string(record), "```json") {
		t.Fatalf("машинный блок остался в записи: %s", record)
	}
	prepared, err := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	if err != nil || !strings.Contains(string(prepared), "## "+first+"\n") || !strings.Contains(string(prepared), "первая") {
		t.Fatalf("prepared-файл: %s %v", prepared, err)
	}
	if _, err = cmdDraftPreparedSave(root, "XR-008", "stale", input); err == nil || !strings.Contains(err.Error(), "изменилась") {
		t.Fatalf("stale: %v", err)
	}
	edit := []byte(`{"proposals":[{"id":"` + first + `","revision":2,"outcome":"add","text":"правка"}]}`)
	saved, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, edit)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(saved), &view); err != nil || len(view.State.Proposals) != 2 || view.State.Proposals[0].ID != first || view.State.Proposals[0].Text != "правка" || view.State.Analysis != "готово" {
		t.Fatalf("%s: %v", saved, err)
	}
	if _, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p3","revision":1,"outcome":"drop","status":"accepted"}]}`)); err == nil {
		t.Fatal("forged decision")
	}
}

// TestPreparedSaveReservesStableBoardID закрывает постоянный номер
// подготовленной задачи (DK-1564, решение 6): номер выдаётся при сохранении
// через общий механизм, не достаётся ни строке, ни черновику, не меняется
// правкой и соседним разбором, а подтверждение заводит строку тем же номером.
func TestPreparedSaveReservesStableBoardID(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	card := draftproposal.Proposal{ID: "p1", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}
	payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{card}})
	saved, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(saved), &view)
	id := view.Assigned["p1"]
	if !idRe.MatchString(id) {
		t.Fatalf("номер не выдан: %s", saved)
	}
	free, err := cmdID(root)
	if err != nil {
		t.Fatal(err)
	}
	if free == id {
		t.Fatalf("номер %s выдан повторно", id)
	}
	// Черновик и вторая запись мимо зарезервированного номера.
	msg, err := cmdDraft(root, "соседний материал", "mid", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	secondDraft := draftIDFromMsg(msg)
	if !idRe.MatchString(secondDraft) || secondDraft == id {
		t.Fatalf("черновик занял зарезервированный номер: %s", msg)
	}
	raw2, err := cmdDraftPrepared(root, secondDraft)
	if err != nil {
		t.Fatal(err)
	}
	var other preparedView
	json.Unmarshal([]byte(raw2), &other)
	saved2, err := cmdDraftPreparedSave(root, secondDraft, other.Snapshot, []byte(`{"proposals":[{"id":"q1","revision":1,"outcome":"drop","reason":"лишнее"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(saved2), &other)
	second := other.Assigned["q1"]
	if second == id || second == secondDraft {
		t.Fatalf("повтор номера: %s, %s и %s", id, secondDraft, second)
	}
	// Правка следующей редакцией номер не меняет и нового не выдаёт.
	edit := card
	edit.ID = id
	edit.Revision = 2
	payload, _ = json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{edit}})
	saved, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	view.Assigned = nil
	json.Unmarshal([]byte(saved), &view)
	if view.State.Proposals[0].ID != id || view.State.Proposals[0].Revision != 2 || len(view.Assigned) != 0 {
		t.Fatalf("номер сменился при правке: %s", saved)
	}
	// Подтверждение заводит строку тем же номером, без перенумерования.
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}},
		Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: id, Revision: 2, Action: "accept"}}}
	l, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, req)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	if err = l.Apply(root, plan.Changes); err != nil {
		l.Close()
		t.Fatal(err)
	}
	l.Close()
	if len(plan.Results) != 1 || len(plan.Results[0].IDs) != 1 || plan.Results[0].IDs[0] != id {
		t.Fatalf("строка заведена чужим номером: %+v", plan.Results)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if b.find(id) == nil {
		t.Fatalf("строки %s на доске нет", id)
	}
	if _, err := os.Stat(taskFileAbs(root, id)); err != nil {
		t.Fatalf("файл задачи %s не заведён: %v", id, err)
	}
}

// TestPreparedSaveRemapsAfterToIssuedID: ссылка «после» новой карточки
// приходит присланным ключом соседней задачи и хранится выданным номером,
// иначе подтверждение не найдёт предшественницу.
func TestPreparedSaveRemapsAfterToIssuedID(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	input := []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"лишнее","reason":"устарело"},{"id":"p2","revision":1,"outcome":"add","title":"Результат","text":"тело результата","board_type":"task","rank":"0+1+1+0+1","size":"M","accept":"agent","after":["p1"]}]}`)
	saved, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(saved), &view)
	if len(view.State.Proposals) != 2 || len(view.State.Proposals[1].After) != 1 || view.State.Proposals[1].After[0] != view.Assigned["p1"] {
		t.Fatalf("ссылка «после» не перебита номером: %s", saved)
	}
}

// TestPreparedLegacyShortKeyStillWorks: prepared-файл старого вида с коротким
// ключом читается, правится редакцией без смены ключа, а подтверждение
// заводит строку свежим номером прежним путём.
func TestPreparedLegacyShortKeyStillWorks(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "старый разбор", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "add", Title: "Наследие", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}}}
	if err := draftproposal.SavePrepared(root, "XR-008", old); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	if len(view.State.Proposals) != 1 || view.State.Proposals[0].ID != "p1" {
		t.Fatalf("старый файл не прочитан: %s", raw)
	}
	revised := old.Proposals[0]
	revised.Revision = 2
	payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{revised}})
	saved, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(saved), &view)
	if view.State.Proposals[0].ID != "p1" || view.State.Proposals[0].Revision != 2 || len(view.Assigned) != 0 {
		t.Fatalf("ключ старой задачи сменился: %s", saved)
	}
	if !idRe.MatchString(view.State.Proposals[0].BoardID) {
		t.Fatalf("номер за старой задачей не закреплён при правке: %s", saved)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}},
		Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 2, Action: "accept"}}}
	l, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, req)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	if err = l.Apply(root, plan.Changes); err != nil {
		l.Close()
		t.Fatal(err)
	}
	l.Close()
	id := plan.Results[0].IDs[0]
	if id != view.State.Proposals[0].BoardID {
		t.Fatalf("строка заведена чужим номером: %s против %s", id, view.State.Proposals[0].BoardID)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if b.find(id) == nil {
		t.Fatalf("строки %s на доске нет", id)
	}
}

// legacyChatDelivery собирает синтетический аналог живой записи с коротким
// ключом chat-delivery: одна нерешённая задача без номера и одна решённая,
// чей номер уже принадлежит строке.
func legacyChatDelivery() draftproposal.State {
	receipt := draftproposal.Receipt{Key: "k1", Source: "XR-008", Proposal: "done-key", Revision: 1, Outcome: "add", IDs: []string{"XR-004"}, Result: "заведено"}
	return draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{
		{ID: "chat-delivery", Revision: 3, Outcome: "add", Title: "Доставка чата", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
		{ID: "done-key", Revision: 1, Outcome: "add", Status: "accepted", Result: &receipt, Title: "Прежняя", Text: "тело", BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
	}, Receipts: []draftproposal.Receipt{receipt}}
}

// TestPreparedAdoptIssuesStableBoardID закрывает унаследованный случай
// (DK-1564, решение 6): существующей подготовленной задаче номер закрепляется
// до подтверждения, ключ и раздел не меняются, старая ссылка открывает ту же
// задачу, решённые карточки не трогаются, подтверждение заводит строку тем же
// номером.
func TestPreparedAdoptIssuesStableBoardID(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "живой аналог", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if err := draftproposal.SavePrepared(root, "XR-008", legacyChatDelivery()); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftPreparedAdopt(root, []string{"XR-008"})
	if err != nil {
		t.Fatal(err)
	}
	var res adoptResult
	if err = json.Unmarshal([]byte(out), &res); err != nil || res.Changed != 1 || len(res.Views) != 1 {
		t.Fatalf("%s: %v", out, err)
	}
	state := res.Views[0].State
	if state.Proposals[0].ID != "chat-delivery" || !idRe.MatchString(state.Proposals[0].BoardID) {
		t.Fatalf("ключ сменился или номер не выдан: %+v", state.Proposals[0])
	}
	if state.Proposals[1].BoardID != "" || state.Proposals[1].Status != "accepted" {
		t.Fatalf("решённая карточка потревожена: %+v", state.Proposals[1])
	}
	number := state.Proposals[0].BoardID
	data, err := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	if err != nil || !strings.Contains(string(data), "## chat-delivery - Доставка чата") || !strings.Contains(string(data), "board_id = \""+number+"\"") {
		t.Fatalf("раздел или номер в файле: %s %v", data, err)
	}
	// Повторное закрепление меняет ничего и файл не трогает.
	before, _ := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	out, err = cmdDraftPreparedAdopt(root, []string{"XR-008"})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(out), &res); err != nil || res.Changed != 0 {
		t.Fatalf("повторное закрепление: %s %v", out, err)
	}
	after, _ := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	if !bytes.Equal(before, after) {
		t.Fatal("повторное закрепление переписало файл")
	}
	// Отказ по старому ключу из ссылки продолжает находить задачу.
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedReject(root, "XR-008", "chat-delivery", view.Snapshot, "не так")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.Proposals[0].Status != "rejected" || view.State.Proposals[0].ID != "chat-delivery" || view.State.Proposals[0].BoardID != number {
		t.Fatalf("отказ по старому ключу: %s", raw)
	}
}

// TestPreparedAdoptConfirmByLegacyKeyUsesNumber: подтверждение унаследованной
// задачи по старому ключу заводит строку закреплённым номером.
func TestPreparedAdoptConfirmByLegacyKeyUsesNumber(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "живой аналог", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if err := draftproposal.SavePrepared(root, "XR-008", legacyChatDelivery()); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDraftPreparedAdopt(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var res adoptResult
	json.Unmarshal([]byte(out), &res)
	number := res.Views[0].State.Proposals[0].BoardID
	var view preparedView
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}},
		Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "chat-delivery", Revision: 3, Action: "accept"}}}
	l, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, req)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	if err = l.Apply(root, plan.Changes); err != nil {
		l.Close()
		t.Fatal(err)
	}
	l.Close()
	if len(plan.Results) != 1 || plan.Results[0].IDs[0] != number {
		t.Fatalf("строка заведена не закреплённым номером: %+v", plan.Results)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if b.find(number) == nil {
		t.Fatalf("строки %s на доске нет", number)
	}
}

// TestAddExplicitIDRefusesReserved: явное заведение с чужим закреплённым
// номером отказывает, а штатное подтверждение владельца работает.
func TestAddExplicitIDRefusesReserved(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	card := draftproposal.Proposal{ID: "p1", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}
	payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{card}})
	saved, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(saved), &view)
	id := view.Assigned["p1"]
	body := filepath.Join(root, "body.md")
	if err := os.WriteFile(body, []byte(fixtureTaskBody), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = cmdAdd(root, AddParams{ID: id, Title: "Чужая", Type: "task", Accept: "agent", BodyFile: body})
	if err == nil || !strings.Contains(err.Error(), "закреплён за подготовленной задачей") {
		t.Fatalf("явный add занял резерв: %v", err)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}},
		Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: id, Revision: 1, Action: "accept"}}}
	l, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, req)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	if err = l.Apply(root, plan.Changes); err != nil {
		l.Close()
		t.Fatal(err)
	}
	l.Close()
	if len(plan.Results) != 1 || plan.Results[0].IDs[0] != id {
		t.Fatalf("подтверждение владельца не завело строку номером: %+v", plan.Results)
	}
}

// TestPreparedAdoptAllAtomic: битый prepared-файл валит закрепление целиком,
// половина номеров не остаётся.
func TestPreparedAdoptAllAtomic(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "годный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraft(root, "битый материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	good := legacyChatDelivery()
	good.Proposals = good.Proposals[:1]
	if err := draftproposal.SavePrepared(root, "XR-008", good); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftproposal.PreparedPath(root, "XR-009"), []byte("мусор без забора"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftPreparedAdopt(root, nil); err == nil {
		t.Fatal("битый файл не завалил закрепление")
	}
	data, err := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	if err != nil || strings.Contains(string(data), "board_id") {
		t.Fatalf("годный файл получил номер при отказе: %s %v", data, err)
	}
}

func TestPreparedRejectPartialAndHistory(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop"},{"id":"p2","revision":1,"outcome":"drop"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedReject(root, "XR-008", assignedKey(view, "p1"), view.Snapshot, "не нужно")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "active" || view.State.Proposals[0].Result.Reason != "не нужно" {
		t.Fatalf("partial: %s", raw)
	}
	raw, err = cmdDraftPreparedReject(root, "XR-008", assignedKey(view, "p2"), view.Snapshot, "лишнее")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "history" {
		t.Fatalf("history: %s", raw)
	}
	raw, err = cmdDraftPrepared(root, "XR-008")
	if err != nil || !strings.Contains(raw, "не нужно") {
		t.Fatalf("history read: %s %v", raw, err)
	}
}

func TestEmptyAnalysisAndReanalysisPreserveDecisions(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[],"analysis":"empty"}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "active" || len(view.State.Proposals) != 0 || view.State.Analysis != "empty" {
		t.Fatalf("empty: %s", raw)
	}
	raw, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"старое","reason":"нет нужды"},{"id":"p2","revision":1,"outcome":"drop","text":"второе","reason":"нет нужды"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedReject(root, "XR-008", assignedKey(view, "p1"), view.Snapshot, "отклонено человеком")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p3","revision":1,"outcome":"drop","text":"новое","reason":"нет нужды"}],"analysis":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if len(view.State.Proposals) != 3 || view.State.Proposals[0].Status != "rejected" || view.State.Proposals[0].Result.Reason != "отклонено человеком" {
		t.Fatalf("reanalysis: %s", raw)
	}
}

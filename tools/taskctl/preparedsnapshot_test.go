package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

func TestPreparedBasisChangesWithTargetAndType(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := []byte("# XR-8: исходник\n")
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "attach", Target: "XR-001"}}}
	initial, err := preparedBasis(root, source, state)
	if err != nil {
		t.Fatal(err)
	}
	path := taskFileAbs(root, "XR-001")
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("target edit"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := preparedBasis(root, source, state)
	if err != nil || initial == changed {
		t.Fatalf("target: %s %s %v", initial, changed, err)
	}
	project := filepath.Join(root, ".devkit", "draft-types", "task.toml")
	if err = os.MkdirAll(filepath.Dir(project), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(kit, "draft-types", "task.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(project, data, 0644); err != nil {
		t.Fatal(err)
	}
	replaced, err := preparedBasis(root, source, state)
	if err != nil || replaced == changed {
		t.Fatalf("type: %s %s %v", changed, replaced, err)
	}
	teamPath := filepath.Join(home, ".devkit", "team.toml")
	if err := os.MkdirAll(filepath.Dir(teamPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(teamPath, []byte("# команда\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changedTeam, err := preparedBasis(root, source, state)
	if err != nil || changedTeam != replaced {
		t.Fatalf("team: %s %s %v", replaced, changedTeam, err)
	}
	// Доска и архив ездят своими коммитами весь день, и вопрос подготовки
	// живёт часами: ход чужой задачи не должен ломать подтверждение
	// (инцидент DK-1531 от 2026-11-04, отказ «показанные карточки
	// изменились» после двух коммитов доски).
	board, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), append(board, []byte("| XR-9 | Чужая строка | task | P3 | 10 (10+0+0+0+0) | S | [tasks/XR-9.md](tasks/XR-9.md) |\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(archivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath(root), append(archive, []byte("| XR-9 | Закрытая | task | P3 | 01.09 |\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	afterBoard, err := preparedBasis(root, source, state)
	if err != nil || afterBoard != replaced {
		t.Fatalf("board: %s %s %v", replaced, afterBoard, err)
	}
	// Сама запись черновика входит в дайджест первой частью: правка её
	// под висящим вопросом ломает подтверждение так же, как и правка
	// определения типа.
	edited := append(append([]byte{}, source...), []byte("правка записи\n")...)
	afterRecord, err := preparedBasis(root, edited, state)
	if err != nil || afterRecord == afterBoard {
		t.Fatalf("record: %s %s %v", afterBoard, afterRecord, err)
	}
}

func TestNumberAdoptionWriteAheadCannotApproveOtherState(t *testing.T) {
	root, answer, _ := legacyAskForAdoption(t, false)
	record, state, err := readPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	expected := state
	expected.Proposals = append([]draftproposal.Proposal(nil), state.Proposals...)
	expected.Proposals[0].BoardID = "XR-999"
	after, err := preparedBasis(root, record, expected)
	if err != nil {
		t.Fatal(err)
	}
	if err = drafttx.RecordNumberAdoption(root, "XR-008", answer.Snapshots[0].Digest, after); err != nil {
		t.Fatal(err)
	}
	changes, err := preparedChanges(root, "XR-008", record, expected)
	if err != nil {
		t.Fatal(err)
	}
	changes[0].Before.Exists = !changes[0].Before.Exists
	lock, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	err = lock.Apply(root, changes)
	lock.Close()
	if err == nil {
		t.Fatal("фикстура не прервала Apply")
	}
	current, unchanged, err := readPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Proposals[0].BoardID != "" {
		t.Fatal("неудачный Apply назначил номер")
	}
	actual, err := preparedBasis(root, append(current, []byte("\nНовый материал\n")...), unchanged)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := preparedBasisMatches(root, "XR-008", actual, answer.Snapshots[0].Digest)
	if err != nil || ok {
		t.Fatalf("неактивный переход разрешил другую запись: %v %v", ok, err)
	}
}

func TestNumberAdoptionCapturesOneBasisContext(t *testing.T) {
	root, _, _ := legacyAskForAdoption(t, true)
	record, state, err := readPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := preparedBasisInputs(root, record, state)
	if err != nil {
		t.Fatal(err)
	}
	before, err := preparedBasisFromInputs(inputs, state)
	if err != nil {
		t.Fatal(err)
	}
	path := taskFileAbs(root, "XR-001")
	target, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(target, []byte("\nИзменение между снимками\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	state.Proposals[0].BoardID = "XR-999"
	after, err := preparedBasisFromInputs(inputs, state)
	if err != nil {
		t.Fatal(err)
	}
	if err = drafttx.RecordNumberAdoption(root, "XR-008", before, after); err != nil {
		t.Fatal(err)
	}
	actual, err := preparedBasis(root, record, state)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := preparedBasisMatches(root, "XR-008", actual, before)
	if err != nil || matches {
		t.Fatalf("изменённые между снимками входы разрешены: %v %v", matches, err)
	}
}

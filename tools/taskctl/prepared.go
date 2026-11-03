package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

// Группа draft prepared читает и правит подготовленные задачи записи
// (DK-1469): носитель это prepared-файл docs/tasks/prepared/<ID черновика>.md,
// запись черновика остаётся материалом без машинного блока. Старый блок
// «Предложения разбора» читается ридером совместимости, а после первого
// сохранения срезается из записи вовсе.

type preparedView struct {
	ID       string              `json:"id"`
	Snapshot string              `json:"snapshot"`
	Basis    string              `json:"basis"`
	State    draftproposal.State `json:"state"`
}

// readPrepared читает запись и её prepared-состояние одним заходом: сначала
// prepared-файл, при его отсутствии старый блок самой записи.
func readPrepared(root, id string) ([]byte, draftproposal.State, error) {
	data, err := os.ReadFile(draftPath(root, id))
	if err != nil {
		return nil, draftproposal.State{}, err
	}
	state, err := draftproposal.Load(root, id, data)
	if err != nil {
		return nil, draftproposal.State{}, err
	}
	return data, state, nil
}

func preparedViewOf(root, id string, record []byte, state draftproposal.State) (preparedView, error) {
	basis, err := preparedBasis(root, record, state)
	if err != nil {
		return preparedView{}, err
	}
	snapshot, err := preparedSnapshot(state, record)
	if err != nil {
		return preparedView{}, err
	}
	return preparedView{ID: id, Snapshot: snapshot, Basis: basis, State: state}, nil
}

func cmdDraftPrepared(root, id string) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	if findDraft(drafts, id) == nil {
		return "", fmt.Errorf("%s: черновик не найден", id)
	}
	record, state, err := readPrepared(root, id)
	if err != nil {
		return "", err
	}
	view, err := preparedViewOf(root, id, record, state)
	if err != nil {
		return "", err
	}
	return marshal(view)
}

// preparedChanges собирает список правок транзакции: состояние пишется в
// prepared-файл, а старый блок срезается из записи, если он там ещё был.
func preparedChanges(root, id string, record []byte, state draftproposal.State) ([]drafttx.Change, error) {
	render, err := draftproposal.RenderPrepared(state)
	if err != nil {
		return nil, err
	}
	before, err := drafttx.Read(draftproposal.PreparedPath(root, id))
	if err != nil {
		return nil, err
	}
	changes := []drafttx.Change{{Path: draftproposal.PreparedRel(id), Before: before, After: drafttx.Image{Exists: true, Data: render, Mode: 0644}}}
	material, had, err := draftproposal.Material(record)
	if err != nil {
		return nil, err
	}
	if had {
		recBefore, err := drafttx.Read(draftPath(root, id))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(recBefore.Data, record) {
			return nil, fmt.Errorf("%s: запись изменилась, откройте её снова", id)
		}
		changes = append(changes, drafttx.Change{Path: draftRel(id), Before: recBefore, After: drafttx.Image{Exists: true, Data: material, Mode: recBefore.Mode}})
	}
	return changes, nil
}

func cmdDraftPreparedSave(root, id, base string, input []byte) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	if findDraft(drafts, id) == nil {
		return "", fmt.Errorf("%s: черновик не найден", id)
	}
	record, old, err := readPrepared(root, id)
	if err != nil {
		return "", err
	}
	snapshot, err := preparedSnapshot(old, record)
	if err != nil {
		return "", err
	}
	if base == "" || base != snapshot {
		return "", fmt.Errorf("%s: запись изменилась, откройте её снова", id)
	}
	var incoming struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
		Analysis  *string                  `json:"analysis"`
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&incoming); err != nil {
		return "", err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("лишние данные после JSON")
	}
	// A new analysis adds tasks; existing tasks are preserved. Undecided tasks
	// can be edited only by supplying their next revision, decided ones cannot.
	index := map[string]int{}
	for i, p := range old.Proposals {
		index[p.ID] = i
	}
	seen := map[string]bool{}
	for _, p := range incoming.Proposals {
		if seen[p.ID] {
			return "", fmt.Errorf("повтор задачи %s", p.ID)
		}
		seen[p.ID] = true
		if p.Status != "" || p.Result != nil {
			return "", fmt.Errorf("%s: решение нельзя задать при записи разбора", p.ID)
		}
		if i, ok := index[p.ID]; ok {
			previous := old.Proposals[i]
			if previous.Status != "" {
				return "", fmt.Errorf("%s: решённую задачу изменить нельзя", p.ID)
			}
			if p.Revision != previous.Revision+1 || reflect.DeepEqual(p, previous) {
				return "", fmt.Errorf("%s: нужна следующая редакция", p.ID)
			}
			old.Proposals[i] = p
		} else {
			if p.Revision != 1 {
				return "", fmt.Errorf("%s: первая редакция должна быть 1", p.ID)
			}
			index[p.ID] = len(old.Proposals)
			old.Proposals = append(old.Proposals, p)
		}
	}
	if len(incoming.Proposals) > 0 {
		old.State = "active"
	}
	if incoming.Analysis != nil {
		old.Analysis = *incoming.Analysis
	}
	changes, err := preparedChanges(root, id, record, old)
	if err != nil {
		return "", err
	}
	if err = l.Apply(root, changes); err != nil {
		return "", err
	}
	material, _, err := draftproposal.Material(record)
	if err != nil {
		return "", err
	}
	view, err := preparedViewOf(root, id, material, old)
	if err != nil {
		return "", err
	}
	return marshal(view)
}

func cmdDraftPreparedReject(root, id, task, base, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("укажите причину отказа")
	}
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	if _, err := findDraftFor(root, id); err != nil {
		return "", err
	}
	record, state, err := readPrepared(root, id)
	if err != nil {
		return "", err
	}
	snapshot, err := preparedSnapshot(state, record)
	if err != nil {
		return "", err
	}
	if base == "" || base != snapshot {
		return "", fmt.Errorf("%s: запись изменилась, откройте её снова", id)
	}
	found := false
	allDone := len(state.Proposals) > 0
	for i := range state.Proposals {
		p := &state.Proposals[i]
		if p.ID == task {
			if p.Status != "" {
				return "", fmt.Errorf("%s: решение уже принято", p.ID)
			}
			p.Status = "rejected"
			p.Result = &draftproposal.Receipt{Proposal: p.ID, Revision: p.Revision, Outcome: p.Outcome, Result: "отклонено", Reason: strings.TrimSpace(reason)}
			found = true
		}
		if p.Status == "" {
			allDone = false
		}
	}
	if !found {
		return "", fmt.Errorf("задача %s не найдена", task)
	}
	if allDone {
		state.State = "history"
	}
	changes, err := preparedChanges(root, id, record, state)
	if err != nil {
		return "", err
	}
	if err = l.Apply(root, changes); err != nil {
		return "", err
	}
	material, _, err := draftproposal.Material(record)
	if err != nil {
		return "", err
	}
	view, err := preparedViewOf(root, id, material, state)
	if err != nil {
		return "", err
	}
	return marshal(view)
}

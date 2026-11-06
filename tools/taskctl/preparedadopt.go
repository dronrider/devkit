package main

import (
	"fmt"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

type adoptResult struct {
	Changed int `json:"changed"`
	// Adopted называет записи, получившие номера: дашборд коммитит файлы
	// закрепления по этому списку (DK-1623), количество Changed его не заменяет.
	Adopted []string       `json:"adopted"`
	Views   []preparedView `json:"views"`
}

// cmdDraftPreparedAdopt закрепляет номера доски за унаследованными
// подготовленными задачами (DK-1564, решение 6): карточкам старого вида без
// board_id выдаётся номер тем же счётом, что и новым задачам, ключ и раздел
// остаются прежними, поэтому старые ссылки из чатов продолжают открывать ту
// же задачу. Решённые карточки не трогаются: их номер уже принадлежит строке
// или отказу. Пустой список идёт по всем черновикам, а вычисление номеров и
// запись файлов живут одним замком и одной транзакцией: отказ на полпути не
// оставляет половину закреплённых номеров.
func cmdDraftPreparedAdopt(root string, ids []string) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	chosen := make([]string, 0, len(ids))
	if len(ids) == 0 {
		for _, d := range drafts {
			chosen = append(chosen, d.ID)
		}
	} else {
		for _, id := range ids {
			if findDraft(drafts, id) == nil {
				return "", fmt.Errorf("%s: черновик не найден", id)
			}
			chosen = append(chosen, id)
		}
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	type entry struct {
		id      string
		record  []byte
		state   draftproposal.State
		adopted bool
	}
	entries := make([]entry, 0, len(chosen))
	var changes []drafttx.Change
	out := adoptResult{Adopted: []string{}, Views: []preparedView{}}
	for _, id := range chosen {
		record, state, err := readPrepared(root, id)
		if err != nil {
			return "", err
		}
		inputs, err := preparedBasisInputs(root, record, state)
		if err != nil {
			return "", err
		}
		before, err := preparedBasisFromInputs(inputs, state)
		if err != nil {
			return "", err
		}
		e := entry{id: id, record: record, state: state}
		for i := range state.Proposals {
			p := &state.Proposals[i]
			if p.BoardID != "" || p.Status != "" || p.Outcome != "add" {
				continue
			}
			num, err := allocID(root, b, arch, drafts)
			if err != nil {
				return "", err
			}
			p.BoardID = num
			e.adopted = true
			out.Changed++
		}
		if e.adopted {
			after, err := preparedBasisFromInputs(inputs, state)
			if err != nil {
				return "", err
			}
			if err = drafttx.RecordNumberAdoption(root, id, before, after); err != nil {
				return "", err
			}
			out.Adopted = append(out.Adopted, id)
			ch, err := preparedChanges(root, id, record, state)
			if err != nil {
				return "", err
			}
			changes = append(changes, ch...)
		}
		entries = append(entries, e)
	}
	if len(changes) > 0 {
		if err = l.Apply(root, changes); err != nil {
			return "", err
		}
	}
	for _, e := range entries {
		material, _, err := draftproposal.Material(e.record)
		if err != nil {
			return "", err
		}
		view, err := preparedViewOf(root, e.id, material, e.state)
		if err != nil {
			return "", err
		}
		out.Views = append(out.Views, view)
	}
	return marshal(out)
}

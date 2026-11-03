package main

import (
	"os"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/stage"
)

type migrateResult struct {
	Records []string `json:"records"`
	Asks    []string `json:"asks"`
}

// cmdDraftPreparedMigrate переносит старые блоки «Предложения разбора» в
// prepared-файлы записей: блок срезается из записи, состояние пишется новым
// видом, висящие ask-файлы переписываются короткой формой без тел (DK-1469,
// решение 2). Повторный прогон ничего не меняет: записи без старого блока
// пропускаются.
func cmdDraftPreparedMigrate(root string) (string, error) {
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	var out migrateResult
	for _, d := range drafts {
		record, err := os.ReadFile(draftPath(root, d.ID))
		if err != nil {
			return "", err
		}
		_, found, err := draftproposal.Material(record)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		state, err := draftproposal.Parse(record)
		if err != nil {
			return "", err
		}
		changes, err := preparedChanges(root, d.ID, record, state)
		if err != nil {
			return "", err
		}
		if err = lock.Apply(root, changes); err != nil {
			return "", err
		}
		out.Records = append(out.Records, d.ID)
		rewritten, err := shortenPreparedAsk(stage.MainRoot(root), d.ID)
		if err != nil {
			return "", err
		}
		if rewritten {
			out.Asks = append(out.Asks, d.ID)
		}
	}
	text, err := marshal(out)
	if err != nil {
		return "", err
	}
	return text, nil
}

// shortenPreparedAsk переписывает висящий вопрос согласования черновика в
// коротком виде: полезная нагрузка теряет тела задач, реплика и опции
// собираются заново. Снимки и показанные редакции не трогаются, поэтому ответ
// по-прежнему сверяется с показанным.
func shortenPreparedAsk(tree, id string) (bool, error) {
	name := chat.TaskName(id)
	ask, ok := chat.ReadAsk(chat.AskPath(tree, name))
	if !ok {
		return false, nil
	}
	changed := false
	for i := range ask.Questions {
		q := &ask.Questions[i]
		payload := q.Prepared
		if payload == nil {
			payload = q.Proposal
		}
		if payload == nil {
			continue
		}
		for j := range payload.Sources {
			for k := range payload.Sources[j].Proposals {
				payload.Sources[j].Proposals[k].Text = ""
			}
		}
		text, options := preparedQuestionShape(payload.Tree, *payload)
		q.Text, q.Options, q.Multi = text, options, true
		q.Prepared, q.Proposal = payload, nil
		changed = true
	}
	if !changed {
		return false, nil
	}
	if err := chat.WriteAsk(tree, name, ask); err != nil {
		return false, err
	}
	return true, nil
}

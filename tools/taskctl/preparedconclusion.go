package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func resolvePrepared(root, id string, record []byte, state draftproposal.State) (draftproposal.State, string) {
	if state.PackRevision != 0 || state.PreparedSource != "" || state.State == "history" {
		return state, preparedNotReady(record, state)
	}
	if state.Analysis == "empty" || state.Analysis == "error" {
		return state, "Разбор не завершён"
	}
	if err := validatePreparedBundle(root, id, record, state); err != nil {
		return state, err.Error()
	}
	digest, err := preparedMaterialDigest(record)
	if err != nil {
		return state, err.Error()
	}
	state.PackRevision = 1
	state.PreparedSource = digest
	return state, ""
}

func preparedConclusion(root, source string, state draftproposal.State) (string, error) {
	return preparedConclusionProjection(root, source, state, false)
}

func preparedLegacyConclusion(root, source string, state draftproposal.State) (string, error) {
	return preparedConclusionProjection(root, source, state, true)
}

func preparedConclusionProjection(root, source string, state draftproposal.State, legacy bool) (string, error) {
	var lines []string
	authored := strings.TrimSpace(state.Conclusion)
	if authored != "" {
		lines = append(lines, authored)
	}
	if !legacy && len(state.Proposals) > 0 {
		lines = append(lines, "### Состав разбора")
	}
	for _, p := range state.Proposals {
		if legacy && p.Outcome == "add" {
			continue
		}
		var action string
		switch p.Outcome {
		case "add":
			number := p.BoardID
			if number == "" && idRe.MatchString(p.ID) {
				number = p.ID
			}
			label := number
			if label == "" {
				label = "задачу"
			}
			route := fmt.Sprintf("#%s/draft/%s/prepared/%s", filepath.Base(taskMainRoot(root)), source, p.ID)
			if p.Status == "accepted" && p.Result != nil && len(p.Result.IDs) > 0 {
				label = p.Result.IDs[0]
				route = label
			}
			action = "Создать [" + label + "](" + route + ")"
			if title := strings.TrimSpace(p.Title); title != "" {
				action += " - " + title
			}
		case "attach":
			action = "Приписать к " + p.Target
		case "defer":
			action = "Отложить материал"
			if p.Target != "" {
				action = "Отложить " + p.Target
			}
			if p.Until != "" {
				action += " до " + p.Until
			}
		case "drop":
			action = "Удалить материал"
			if p.Target != "" {
				action = "Удалить " + p.Target
				if p.Status == "" {
					active, err := preparedTargetActive(root, p.Target)
					if err != nil {
						return "", err
					}
					if !active {
						action = p.Target + " уже отсутствует в активных черновиках, удаление не требуется"
					}
				}
			}
		}
		if p.Status == "accepted" {
			action += " (подтверждено)"
		} else if p.Status == "rejected" {
			action += " (отклонено)"
		}
		details := []string{p.Text, p.Reason, p.Rationale}
		if p.Outcome == "add" {
			details = []string{p.Reason, p.Rationale}
		}
		if !legacy && authored != "" {
			details = nil
		}
		for _, text := range details {
			if text = strings.TrimSpace(text); text != "" {
				action += ". " + text
			}
		}
		lines = append(lines, "- "+action)
	}
	if legacy && len(lines) == 0 && len(state.Proposals) > 0 {
		lines = append(lines, "Предлагаю создать подготовленные задачи.")
	}
	return strings.Join(lines, "\n\n"), nil
}

func preparedTargetPaths(root string, p draftproposal.Proposal) []string {
	if p.Target == "" {
		return nil
	}
	if p.Outcome == "drop" || p.Outcome == "defer" {
		return []string{draftPath(root, p.Target), draftproposal.PreparedPath(root, p.Target)}
	}
	return []string{taskFileAbs(root, p.Target)}
}

func preparedTargetActive(root, id string) (bool, error) {
	data, err := os.ReadFile(draftPath(root, id))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state, err := draftproposal.Load(root, id, data)
	if err != nil {
		return false, err
	}
	return state.State != "history", nil
}

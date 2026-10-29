package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
)

type stagedResult struct {
	ID   string
	Text string
}

func (s *stagingBoard) projection(sourceID string, h draftmeta.Header, p draftproposal.Proposal) (string, error) {
	id, err := cmdID(s.root)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return "", fmt.Errorf("%s: текст результата пуст", p.ID)
	}
	text += "\n\nИсточник: " + sourceID
	if _, err = cmdDraft(s.root, text, h.Priority, CommitOpts{}); err != nil {
		return "", err
	}
	t, err := loadDraftText(draftPath(s.root, id))
	if err != nil {
		return "", err
	}
	setDraftMeta(t, h.Type, h.Source, h.Lock)
	if err = t.save(); err != nil {
		return "", err
	}
	return id, nil
}

// stageOutcome uses the existing taskctl commands on a private projection.
// The live source is never passed to a consuming command.
func (s *stagingBoard) stageOutcome(sourceID string, h draftmeta.Header, p draftproposal.Proposal) (stagedResult, error) {
	var result stagedResult
	switch p.Outcome {
	case "add":
		id, err := cmdID(s.root)
		if err != nil {
			return result, err
		}
		file := filepath.Join(s.root, "proposal-body.md")
		if err = os.WriteFile(file, []byte(p.Text), 0600); err != nil {
			return result, err
		}
		defer os.Remove(file)
		msg, err := cmdAdd(s.root, AddParams{Title: p.Title, Type: p.BoardType, Rank: p.Rank, Cost: p.Size, Accept: p.Accept, Barrier: p.Barrier, BodyFile: file})
		if err != nil {
			return result, err
		}
		if p.Due != "" || p.Assignee != "" || p.Control != "" {
			if _, err = cmdSet(s.root, SetParams{ID: id, Due: p.Due, Assign: p.Assignee, Control: p.Control}); err != nil {
				return result, err
			}
		}
		result = stagedResult{ID: id, Text: msg}
	case "attach", "defer", "drop":
		id, err := s.projection(sourceID, h, p)
		if err != nil {
			return result, err
		}
		var msg string
		switch p.Outcome {
		case "attach":
			msg, err = cmdDraftAttach(s.root, id, p.Target, CommitOpts{})
			result.ID = p.Target
		case "defer":
			msg, err = cmdDraftDefer(s.root, id, p.Reason, p.Until, false, CommitOpts{})
			result.ID = id
		case "drop":
			msg, err = cmdDraftDrop(s.root, id, p.Reason, CommitOpts{})
		}
		if err != nil {
			return stagedResult{}, err
		}
		result.Text = msg
	default:
		return result, fmt.Errorf("неизвестный исход %q", p.Outcome)
	}
	if p.Outcome == "add" {
		path := taskFileAbs(s.root, result.ID)
		data, err := os.ReadFile(path)
		if err != nil {
			return stagedResult{}, err
		}
		provenance := fmt.Sprintf("\n\n## Происхождение\n\nЧерновик %s, тип %s, источник %s.\n", sourceID, h.Type, h.Source)
		if err = os.WriteFile(path, append([]byte(strings.TrimRight(string(data), "\n")), []byte(provenance)...), 0644); err != nil {
			return stagedResult{}, err
		}
	}
	if h.Lock == "locked" && (p.Outcome == "add" || p.Outcome == "attach") {
		id := result.ID
		if p.Outcome == "attach" {
			id = p.Target
		}
		path := taskFileAbs(s.root, id)
		data, err := os.ReadFile(path)
		if err != nil {
			return stagedResult{}, err
		}
		data, err = draftmeta.ProtectRecord(data)
		if err != nil {
			return stagedResult{}, err
		}
		if err = os.WriteFile(path, data, 0644); err != nil {
			return stagedResult{}, err
		}
	}
	return result, nil
}

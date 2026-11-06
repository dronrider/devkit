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
		return "", fmt.Errorf("текст результата пуст")
	}
	link := sourceID + ".md"
	if p.Outcome == "attach" {
		link = "drafts/" + link
	}
	text += fmt.Sprintf("\n\nИсточник: [черновик %s](%s)", sourceID, link)
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
		normalized, err := normalizePreparedBarrier(p)
		if err != nil {
			return result, err
		}
		p = normalized
		// Номер задачи закреплён заранее: у новых стоит в ключе и board_id,
		// у унаследованных в board_id, и строка заводится тем же номером без
		// перенумерования (DK-1564, решение 6). Карточка вовсе без номера это
		// глубокая старина: ей выделяется свежий номер прежним путём.
		var id string
		if p.BoardID != "" {
			id = p.BoardID
		} else if idRe.MatchString(p.ID) {
			id = p.ID
		} else if id, err = cmdID(s.root); err != nil {
			return result, err
		}
		file := filepath.Join(s.root, "proposal-body.md")
		if err = os.WriteFile(file, []byte(p.Text), 0600); err != nil {
			return result, err
		}
		defer os.Remove(file)
		msg, err := cmdAdd(s.root, AddParams{ID: id, Title: p.Title, Type: p.BoardType, Rank: p.Rank, Cost: p.Size, Accept: p.Accept, Barrier: p.Barrier, BodyFile: file, OwnsReserve: true})
		if err != nil {
			return result, err
		}
		if p.Due != "" || p.Assignee != "" || p.Control != "" {
			if _, err = cmdSet(s.root, SetParams{ID: id, Due: p.Due, Assign: p.Assignee, Control: p.Control}); err != nil {
				return result, err
			}
		}
		result = stagedResult{ID: id, Text: msg}
	case "drop":
		if strings.TrimSpace(p.Reason) == "" {
			return result, fmt.Errorf("нужна причина удаления")
		}
		if p.Target == "" {
			return stagedResult{Text: "Материал отклонён: " + strings.TrimSpace(p.Reason)}, nil
		}
		if !idRe.MatchString(p.Target) || p.Target == sourceID {
			return result, fmt.Errorf("неверная цель удаления %q", p.Target)
		}
		if active, err := preparedTargetActive(s.root, p.Target); err == nil && !active {
			return stagedResult{Text: p.Target + " уже отсутствует в активных черновиках; удаление не требуется: " + strings.TrimSpace(p.Reason)}, nil
		} else if err != nil {
			return result, err
		}
		if h.Lock != "locked" {
			if err := draftmeta.GuardRecord(s.root, p.Target); err != nil {
				return result, err
			}
		}
		msg, err := cmdDraftDrop(s.root, p.Target, p.Reason, CommitOpts{})
		if err != nil {
			return result, err
		}
		return stagedResult{Text: msg}, nil
	case "attach", "defer":
		if p.Outcome == "defer" && p.Target != "" {
			if !idRe.MatchString(p.Target) || p.Target == sourceID {
				return result, fmt.Errorf("неверная цель отсрочки %q", p.Target)
			}
			if h.Lock != "locked" {
				if err := draftmeta.GuardRecord(s.root, p.Target); err != nil {
					return result, err
				}
			}
			if h.Lock == "locked" {
				t, err := loadDraftText(draftPath(s.root, p.Target))
				if err != nil {
					return result, err
				}
				setDraftMeta(t, "", "", "locked")
				if err = t.save(); err != nil {
					return result, err
				}
			}
			msg, err := cmdDraftDefer(s.root, p.Target, p.Reason, p.Until, false, CommitOpts{})
			return stagedResult{ID: p.Target, Text: msg}, err
		}
		id, err := s.projection(sourceID, h, p)
		if err != nil {
			return result, err
		}
		var msg string
		switch p.Outcome {
		case "attach":
			msg, err = cmdDraftAttachStage(s.root, id, p.Target, CommitOpts{}, true)
			result.ID = p.Target
		case "defer":
			msg, err = cmdDraftDefer(s.root, id, p.Reason, p.Until, false, CommitOpts{})
			result.ID = id
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
		provenance := fmt.Sprintf("\n\n## Происхождение\n\n[Черновик %s](drafts/%s.md), тип %s, источник %s.\n", sourceID, sourceID, h.Type, h.Source)
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

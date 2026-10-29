// Package draftproposal stores grooming proposals in their source Markdown file.
package draftproposal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const heading = "## Предложения разбора"

type Proposal struct {
	ID        string   `json:"id"`
	Revision  int      `json:"revision"`
	Outcome   string   `json:"outcome"`
	Text      string   `json:"text"`
	Reason    string   `json:"reason"`
	Title     string   `json:"title,omitempty"`
	BoardType string   `json:"board_type,omitempty"`
	Rank      string   `json:"rank,omitempty"`
	Size      string   `json:"size,omitempty"`
	Accept    string   `json:"accept,omitempty"`
	Barrier   string   `json:"barrier,omitempty"`
	Control   string   `json:"control,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Due       string   `json:"due,omitempty"`
	Target    string   `json:"target,omitempty"`
	Until     string   `json:"until,omitempty"`
	After     []string `json:"after,omitempty"`
	Status    string   `json:"status,omitempty"`
	Result    *Receipt `json:"result,omitempty"`
}
type Receipt struct {
	Key      string   `json:"key"`
	Proposal string   `json:"proposal"`
	Revision int      `json:"revision"`
	Outcome  string   `json:"outcome"`
	IDs      []string `json:"ids,omitempty"`
	Result   string   `json:"result"`
	Reason   string   `json:"reason,omitempty"`
}
type State struct {
	Version   int        `json:"version"`
	State     string     `json:"state"`
	Proposals []Proposal `json:"proposals"`
	Receipts  []Receipt  `json:"receipts,omitempty"`
	Analysis  string     `json:"analysis,omitempty"`
}
type Decision struct {
	Source   string `json:"source"`
	Proposal string `json:"proposal"`
	Revision int    `json:"revision"`
	Action   string `json:"action"`
}
type Snapshot struct {
	Source string `json:"source"`
	Digest string `json:"digest"`
}
type ConfirmRequest struct {
	Snapshots []Snapshot `json:"snapshots"`
	Decisions []Decision `json:"decisions"`
}

func rangeOf(text string) (int, int, bool, error) {
	start := 0
	for start < len(text) {
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += start
		}
		if strings.TrimSpace(text[start:end]) == heading {
			next := end
			if next < len(text) {
				next++
			}
			stop := len(text)
			offset := next
			for offset < len(text) {
				n := strings.IndexByte(text[offset:], '\n')
				if n < 0 {
					n = len(text)
				} else {
					n += offset
				}
				if strings.HasPrefix(strings.TrimSpace(text[offset:n]), "## ") {
					stop = offset
					break
				}
				if n == len(text) {
					break
				}
				offset = n + 1
			}
			if stop < len(text) && (strings.HasPrefix(text[stop:], heading+"\n") || strings.Contains(text[stop:], "\n"+heading+"\n")) {
				return 0, 0, false, fmt.Errorf("повтор раздела предложений")
			}
			return start, stop, true, nil
		}
		if end == len(text) {
			break
		}
		start = end + 1
	}
	return 0, 0, false, nil
}

func Parse(data []byte) (State, error) {
	state := State{Version: 1, State: "active", Proposals: []Proposal{}}
	text := string(data)
	start, end, ok, err := rangeOf(text)
	if err != nil {
		return state, err
	}
	if !ok {
		return state, nil
	}
	body := strings.TrimSpace(strings.TrimPrefix(text[start:end], heading))
	if !strings.HasPrefix(body, "```json\n") || !strings.HasSuffix(body, "\n```") {
		return state, fmt.Errorf("раздел предложений должен содержать один блок JSON")
	}
	body = strings.TrimSuffix(strings.TrimPrefix(body, "```json\n"), "\n```")
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&state); err != nil {
		return State{}, fmt.Errorf("раздел предложений: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return State{}, fmt.Errorf("лишние данные после предложений")
	}
	if err = state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}
func (s State) Validate() error {
	if s.Version != 1 || (s.State != "active" && s.State != "history") {
		return fmt.Errorf("неверная версия или состояние разбора")
	}
	if s.State == "history" && len(s.Proposals) == 0 {
		return fmt.Errorf("пустой разбор остаётся активным")
	}
	ids := map[string]bool{}
	for _, p := range s.Proposals {
		if p.ID == "" || ids[p.ID] || p.Revision < 1 {
			return fmt.Errorf("неверный или повторный ID предложения %q", p.ID)
		}
		ids[p.ID] = true
		switch p.Outcome {
		case "add", "attach", "defer", "drop":
		default:
			return fmt.Errorf("%s: неизвестный исход %q", p.ID, p.Outcome)
		}
		switch p.Status {
		case "", "accepted", "rejected":
		default:
			return fmt.Errorf("%s: неизвестное решение %q", p.ID, p.Status)
		}
		if s.State == "history" && p.Status == "" {
			return fmt.Errorf("%s: предложение без решения в истории", p.ID)
		}
		if p.Status == "accepted" && (p.Result == nil || p.Result.Proposal != p.ID || p.Result.Revision != p.Revision) {
			return fmt.Errorf("%s: нет результата принятой редакции", p.ID)
		}
	}
	return nil
}
func Replace(data []byte, state State) ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	jsonData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	block := heading + "\n\n```json\n" + string(jsonData) + "\n```\n\n"
	text := string(data)
	start, end, ok, err := rangeOf(text)
	if err != nil {
		return nil, err
	}
	if ok {
		return []byte(text[:start] + block + text[end:]), nil
	}
	return []byte(strings.TrimRight(text, "\n") + "\n\n" + block), nil
}

// Digest is length-delimited so independent snapshot parts cannot collide by concatenation.
func Digest(parts ...[]byte) string {
	var b bytes.Buffer
	for _, p := range parts {
		fmt.Fprintf(&b, "%d:", len(p))
		b.Write(p)
	}
	h := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(h[:])
}

// Package draftproposal хранит подготовленные задачи груминга. Носитель это
// prepared-файл записи (prepared.go), старый блок «Предложения разбора» в самой
// записи читается ридером совместимости до миграции записи.
package draftproposal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

const heading = "## Предложения разбора"

type Proposal struct {
	ID string `json:"id"`
	// BoardID это закреплённый за задачей номер доски (DK-1564, решение 6):
	// выдаётся до применения решения, виден в списке и составе, а подтверждение
	// заводит строку тем же номером. У новых задач совпадает с ключом, у
	// унаследованных живёт рядом с прежним ключом, и старые ссылки продолжают
	// открывать ту же задачу.
	BoardID   string   `json:"board_id,omitempty"`
	Revision  int      `json:"revision"`
	Outcome   string   `json:"outcome"`
	Text      string   `json:"text"`
	Reason    string   `json:"reason"`
	Rationale string   `json:"rationale,omitempty"`
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
	Source   string   `json:"source,omitempty"`
	Key      string   `json:"key"`
	Proposal string   `json:"proposal"`
	Revision int      `json:"revision"`
	Outcome  string   `json:"outcome"`
	IDs      []string `json:"ids,omitempty"`
	Result   string   `json:"result"`
	Reason   string   `json:"reason,omitempty"`
}
type State struct {
	PackRevision   int        `json:"pack_revision,omitempty"`
	PreparedSource string     `json:"prepared_source,omitempty"`
	RetiredIDs     []string   `json:"retired_ids,omitempty"`
	Version        int        `json:"version"`
	State          string     `json:"state"`
	Proposals      []Proposal `json:"proposals"`
	Receipts       []Receipt  `json:"receipts,omitempty"`
	Analysis       string     `json:"analysis,omitempty"`
}
type Decision struct {
	Source   string `json:"source"`
	Proposal string `json:"proposal"`
	Revision int    `json:"revision"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
}
type Snapshot struct {
	Revision int    `json:"revision,omitempty"`
	Source   string `json:"source"`
	Digest   string `json:"digest"`
}
type ConfirmRequest struct {
	Snapshots []Snapshot `json:"snapshots"`
	Decisions []Decision `json:"decisions"`
}

func rangeOf(text string) (int, int, bool, error) {
	lines := strings.Split(text, "\n")
	mask, _ := taskform.FenceMask(lines)
	start, end, offset := -1, len(text), 0
	for i, line := range lines {
		if !mask[i] && strings.TrimSpace(line) == heading {
			if start >= 0 {
				return 0, 0, false, fmt.Errorf("повтор раздела карточек")
			}
			start = offset
		} else if start >= 0 && end == len(text) && !mask[i] && strings.HasPrefix(strings.TrimSpace(line), "## ") {
			end = offset
		}
		offset += len(line) + 1
	}
	if start < 0 {
		return 0, 0, false, nil
	}
	return start, end, true, nil
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
		return state, fmt.Errorf("раздел карточек должен содержать один блок JSON")
	}
	body = strings.TrimSuffix(strings.TrimPrefix(body, "```json\n"), "\n```")
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&state); err != nil {
		return State{}, fmt.Errorf("раздел карточек: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return State{}, fmt.Errorf("лишние данные после карточек")
	}
	if err = state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}
func (s State) Validate() error { return s.validate(false) }

// validatePrepared разрешает пустой набор задач с state=history: закончившийся
// разбор без задач пишется именно так (DK-1469, решение 1).
func (s State) validatePrepared() error { return s.validate(true) }

func (s State) validate(emptyHistory bool) error {
	retired := map[string]bool{}
	for _, id := range s.RetiredIDs {
		if id == "" || retired[id] {
			return fmt.Errorf("неверный или повторный снятый номер %q", id)
		}
		retired[id] = true
	}
	if s.Version != 1 || (s.State != "active" && s.State != "history") {
		return fmt.Errorf("неверная версия или состояние разбора")
	}
	if s.State == "history" && len(s.Proposals) == 0 && !emptyHistory {
		return fmt.Errorf("пустой разбор остаётся активным")
	}
	ids := map[string]bool{}
	for _, p := range s.Proposals {
		if p.ID == "" || ids[p.ID] || p.Revision < 1 {
			return fmt.Errorf("неверный или повторный ID карточки %q", p.ID)
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
			return fmt.Errorf("%s: карточка без решения в истории", p.ID)
		}
		if p.Status == "accepted" && !acceptedResult(s, p) {
			return fmt.Errorf("%s: нет результата принятой редакции", p.ID)
		}
	}
	return nil
}

// acceptedResult признаёт результат принятой редакции: старая схема держала
// его в самой карточке, prepared-файл восстанавливает его из журнала receipts
// по ключу и редакции.
func acceptedResult(s State, p Proposal) bool {
	if p.Result != nil && p.Result.Proposal == p.ID && p.Result.Revision == p.Revision {
		return true
	}
	for _, r := range s.Receipts {
		if r.Proposal == p.ID && r.Revision == p.Revision {
			return true
		}
	}
	return false
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

// Material removes the machine section for an editor; the caller keeps the
// original state and restores it when saving the edited material.
func Material(data []byte) ([]byte, bool, error) {
	text := string(data)
	start, end, ok, err := rangeOf(text)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return data, false, nil
	}
	if _, err = Parse(data); err != nil {
		return nil, false, err
	}
	return []byte(strings.TrimRight(text[:start], "\n") + "\n\n" + strings.TrimLeft(text[end:], "\n")), true, nil
}

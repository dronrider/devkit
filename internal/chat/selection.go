package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type Selection struct {
	Task      string             `json:"task"`
	Tree      string             `json:"tree"`
	Questions []SelectedQuestion `json:"questions"`
}

type SelectedQuestion struct {
	Fork     string   `json:"fork"`
	Snapshot string   `json:"snapshot"`
	IDs      []string `json:"ids"`
	Labels   []string `json:"labels,omitempty"`
}

func QuestionSnapshot(q Question) string {
	q.Scenario, q.Proposal = nil, nil
	data, _ := json.Marshal(q)
	sum := sha256.Sum256(data)
	return "q1-" + hex.EncodeToString(sum[:])
}

const SelectionWire = "Ответ с вложением панели: "

type SelectionMessage struct {
	Version   int        `json:"version"`
	Text      string     `json:"text"`
	Selection *Selection `json:"selection,omitempty"`
	Result    string     `json:"result,omitempty"`
}

func (m SelectionMessage) Wire() string {
	data, _ := json.Marshal(m)
	return SelectionWire + string(data)
}

func ReadSelectionMessage(wire string) (SelectionMessage, bool) {
	var message SelectionMessage
	if !strings.HasPrefix(wire, SelectionWire) {
		return message, false
	}
	err := json.Unmarshal([]byte(strings.TrimPrefix(wire, SelectionWire)), &message)
	if err != nil || message.Version != 1 {
		return SelectionMessage{}, false
	}
	if message.Selection != nil && (message.Selection.Task == "" || len(message.Selection.Questions) == 0) {
		return SelectionMessage{}, false
	}
	return message, true
}

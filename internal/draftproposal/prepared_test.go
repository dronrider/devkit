package draftproposal

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func samplePreparedState() State {
	return State{
		Version: 1, State: "active", Analysis: "две задачи закрывают запись",
		Proposals: []Proposal{
			{ID: "p1", Revision: 2, Outcome: "add", Title: "Первая задача", BoardType: "task", Rank: "25+5+3+0+4", Size: "M", Accept: "agent", Target: "XR-001", Text: "## Что происходит\n\nтело задачи с заголовком TASKFORM"},
			{ID: "p2", Revision: 1, Outcome: "defer", Reason: "ждёт веху", Until: "2026-12-01", After: []string{"p1"}},
		},
		Receipts: []Receipt{{Source: "XR-8", Key: "abc", Proposal: "p1", Revision: 1, Outcome: "add", IDs: []string{"XR-9"}, Result: "XR-9 заведена"}},
	}
}

func TestPreparedRoundTrip(t *testing.T) {
	state := samplePreparedState()
	data, err := RenderPrepared(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("```toml\n")) || !bytes.Contains(data, []byte("## p1 - Первая задача")) {
		t.Fatalf("вид файла: %s", data)
	}
	parsed, err := ParsePrepared(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Proposals, parsed.Proposals) {
		t.Fatalf("задачи: %+v против %+v", state.Proposals, parsed.Proposals)
	}
	if !reflect.DeepEqual(state.Receipts, parsed.Receipts) || parsed.Analysis != state.Analysis || parsed.State != "active" {
		t.Fatalf("журнал: %+v", parsed)
	}
	again, err := RenderPrepared(parsed)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("вид не канонический: %s против %s (%v)", data, again, err)
	}
	history := State{Version: 1, State: "history",
		Proposals: []Proposal{
			{ID: "p1", Revision: 2, Outcome: "add", Status: "accepted", Text: "тело"},
			{ID: "p2", Revision: 1, Outcome: "drop", Status: "rejected", Reason: "дубль"},
		},
		Receipts: []Receipt{
			{Source: "XR-8", Key: "k1", Proposal: "p1", Revision: 2, Outcome: "add", IDs: []string{"XR-9"}, Result: "XR-9 заведена"},
			{Source: "XR-8", Key: "k2", Proposal: "p2", Revision: 1, Outcome: "drop", Result: "отклонено"},
		},
	}
	data, err = RenderPrepared(history)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParsePrepared(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Proposals[0].Result == nil || parsed.Proposals[0].Result.IDs[0] != "XR-9" || parsed.Proposals[1].Result == nil || parsed.Proposals[1].Result.Result != "отклонено" {
		t.Fatalf("результаты из журнала: %+v", parsed.Proposals)
	}
	// Тело с переводом строки в начале и своим заголовком внутри: перевод
	// принадлежит телу, а не формату, иначе круг рендер-разбор менял бы
	// канонический вид и снимок показанного расходился после сохранения.
	bodies := State{Version: 1, State: "active", Proposals: []Proposal{
		{ID: "p1", Revision: 1, Outcome: "add", Title: "С переносом", Text: "\n## Что происходит\n\nтело начинается строкой ниже"},
		{ID: "p2", Revision: 1, Outcome: "drop", Reason: "лишнее"},
	}}
	data, err = RenderPrepared(bodies)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = ParsePrepared(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Proposals[0].Text != bodies.Proposals[0].Text || parsed.Proposals[1].Text != "" {
		t.Fatalf("тела задач: %q", parsed.Proposals[0].Text)
	}
	again, err = RenderPrepared(parsed)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("вид с переносами не канонический: %q против %q (%v)", data, again, err)
	}
}

func TestPreparedEmptyHistory(t *testing.T) {
	state := State{Version: 1, State: "history"}
	data, err := RenderPrepared(state)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePrepared(data)
	if err != nil || parsed.State != "history" || len(parsed.Proposals) != 0 {
		t.Fatalf("пустая история: %+v %v", parsed, err)
	}
	if err = state.Validate(); err == nil {
		t.Fatal("старая схема приняла пустую историю")
	}
}

func TestPreparedRejectsUnknownFields(t *testing.T) {
	state := samplePreparedState()
	data, err := RenderPrepared(state)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "analysis = ", "bogus = 1\nanalysis = ", 1)
	if _, err = ParsePrepared([]byte(text)); err == nil {
		t.Fatal("машинный блок принял чужое поле")
	}
	text = strings.Replace(string(data), "revision = 2", "revision = 2\nbogus = \"x\"", 1)
	if _, err = ParsePrepared([]byte(text)); err == nil {
		t.Fatal("раздел задачи принял чужое поле")
	}
}

func TestLoadPrefersPreparedFile(t *testing.T) {
	root := t.TempDir()
	old := samplePreparedState()
	old.Proposals[0].Revision = 1
	record, err := Replace([]byte("# XR-8: запись\n\n## Текст\nматериал\n"), old)
	if err != nil {
		t.Fatal(err)
	}
	state, err := Load(root, "XR-8", record)
	if err != nil || len(state.Proposals) != 2 || state.Proposals[0].Revision != 1 {
		t.Fatalf("старый блок: %+v %v", state, err)
	}
	if err = SavePrepared(root, "XR-8", samplePreparedState()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "docs", "tasks", "prepared", "XR-8.md")); err != nil {
		t.Fatal(err)
	}
	state, err = Load(root, "XR-8", record)
	if err != nil || state.Proposals[0].Revision != 2 {
		t.Fatalf("prepared-файл не приоритетен: %+v %v", state, err)
	}
}

package chat

import (
	"encoding/json"
	"github.com/dronrider/devkit/internal/draftproposal"
	"testing"
)

func TestPreparedQuestionRoundTripAndAnswer(t *testing.T) {
	q := PreparedQuestion{Version: 1, Tree: "/tmp/tree", Anchor: "XR-1", Sources: []PreparedSource{{Source: "XR-1", Basis: "shown", Revision: 3, Proposals: []draftproposal.Proposal{{ID: "a", Revision: 1, Outcome: "add"}, {ID: "b", Revision: 2, Outcome: "add", After: []string{"a"}}}}}}
	a := Ask{Task: "XR-1", Questions: []Question{{Text: "выбор", Prepared: &q}}}
	parsed, ok := ParseAsk(a.Text())
	if !ok || parsed.Questions[0].Prepared.Sources[0].Proposals[1].After[0] != "a" {
		t.Fatal(parsed)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-1", Revision: 3, Digest: "shown"}}, Decisions: []draftproposal.Decision{{Source: "XR-1", Proposal: "a", Revision: 1, Action: "accept"}, {Source: "XR-1", Proposal: "b", Revision: 2, Action: "accept"}}}
	if err := CheckPreparedAnswer(q, "/tmp/tree", req); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*draftproposal.ConfirmRequest)
		tree   string
	}{
		{"tree", func(*draftproposal.ConfirmRequest) {}, "/tmp/other"},
		{"partial", func(r *draftproposal.ConfirmRequest) { r.Decisions = r.Decisions[:1] }, "/tmp/tree"},
		{"pack revision", func(r *draftproposal.ConfirmRequest) { r.Snapshots[0].Revision++ }, "/tmp/tree"},
		{"basis", func(r *draftproposal.ConfirmRequest) { r.Snapshots[0].Digest = "fresh" }, "/tmp/tree"},
		{"hidden", func(r *draftproposal.ConfirmRequest) { r.Decisions[0].Proposal = "new" }, "/tmp/tree"},
		{"revision", func(r *draftproposal.ConfirmRequest) { r.Decisions[0].Revision++ }, "/tmp/tree"},
		{"reason", func(r *draftproposal.ConfirmRequest) { r.Decisions[0].Action = "reject" }, "/tmp/tree"},
		{"duplicate", func(r *draftproposal.ConfirmRequest) { r.Decisions = append(r.Decisions, r.Decisions[0]) }, "/tmp/tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(req)
			var copy draftproposal.ConfirmRequest
			json.Unmarshal(b, &copy)
			tc.change(&copy)
			if CheckPreparedAnswer(q, tc.tree, copy) == nil {
				t.Fatal("accepted")
			}
		})
	}
	qdata, _ := json.Marshal(q)
	mixed := `{"questions":[{"text":"выбор","prepared":` + string(qdata) + `,"scenario":{"snapshot":"x"}}]}`
	if _, err := ParsePack([]byte(mixed)); err == nil {
		t.Fatal("mixed accepted")
	}
}

func TestPreparedQuestionRejectsBodies(t *testing.T) {
	q := PreparedQuestion{Version: 1, Tree: "/tmp/tree", Anchor: "XR-1", Sources: []PreparedSource{{Source: "XR-1", Basis: "shown", Proposals: []draftproposal.Proposal{{ID: "a", Revision: 1, Outcome: "add", Text: "портянка"}}}}}
	if err := q.Validate(); err == nil {
		t.Fatal("тело задачи прошло в вопрос")
	}
}

// Старые ask-файлы несут вопрос согласования полем proposal, новые пишутся
// полем prepared. Поле остаётся ридером прежних записей (DK-1564).
func TestPreparedQuestionReadsLegacyProposalField(t *testing.T) {
	q := PreparedQuestion{Version: 1, Tree: "/tmp/tree", Anchor: "XR-1", Sources: []PreparedSource{{Source: "XR-1", Basis: "shown", Proposals: []draftproposal.Proposal{{ID: "a", Revision: 1, Outcome: "add"}}}}}
	data, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"questions":[{"text":"выбор","proposal":` + string(data) + `}]}`
	questions, err := ParsePack([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if questions[0].Proposal == nil || questions[0].Prepared != nil {
		t.Fatal("старое поле не прочиталось")
	}
}

func TestDropAskIfKeepsReplacement(t *testing.T) {
	root := t.TempDir()
	first := Ask{Task: "XR-1", Questions: []Question{{Text: "первый"}}}
	next := Ask{Task: "XR-1", Questions: []Question{{Text: "второй"}}}
	if err := WriteAsk(root, TaskName("XR-1"), first); err != nil {
		t.Fatal(err)
	}
	if err := WriteAsk(root, TaskName("XR-1"), next); err != nil {
		t.Fatal(err)
	}
	removed, err := DropAskIf(root, TaskName("XR-1"), first)
	if err != nil || removed {
		t.Fatalf("replacement removed: %v %v", removed, err)
	}
	got, ok := ReadAsk(AskPath(root, TaskName("XR-1")))
	if !ok || got.Questions[0].Text != "второй" {
		t.Fatal(got)
	}
	removed, err = DropAskIf(root, TaskName("XR-1"), next)
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
}

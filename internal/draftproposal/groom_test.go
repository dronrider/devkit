package draftproposal

import "testing"

func TestHasGroomResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
		state  State
		want   bool
	}{
		{name: "raw"},
		{name: "incomplete", state: State{Version: 1, State: "active", PackRevision: 1}},
		{name: "first-error", state: State{Analysis: "error"}},
		{name: "empty", state: State{Analysis: "empty"}, want: true},
		{name: "conclusion", state: State{Conclusion: "Разбор завершён."}, want: true},
		{name: "proposals-not-ready", state: State{PackRevision: 1, Proposals: []Proposal{{ID: "p", Outcome: "drop"}}}, want: true},
		{name: "retry-error-keeps-proposals", state: State{Analysis: "error", Proposals: []Proposal{{ID: "p", Outcome: "drop"}}}, want: true},
		{name: "retry-error-keeps-conclusion", state: State{Analysis: "error", Conclusion: "Прежний результат."}, want: true},
		{name: "legacy-groom", record: "## Грумминг\n\nОтложено до релиза.\n", want: true},
		{name: "legacy-decided", record: "## Идеи\n\n- Старая мысль -> влита в материал записи\n", want: true},
		{name: "pending", record: "## Идеи\n\n- Первая мысль\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasGroomResult([]byte(tc.record), tc.state); got != tc.want {
				t.Fatalf("HasGroomResult = %v, want %v", got, tc.want)
			}
		})
	}
}

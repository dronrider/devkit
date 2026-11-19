package reviewaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestNameProgressContinuesWithDamagedTargets(t *testing.T) {
	for _, damage := range []string{"add", "remove", "change", "reorder", "duplicate"} {
		t.Run(damage, func(t *testing.T) {
			root, path := fixture(t)
			write(t, filepath.Join(root, "a.go"), "package sample\nfunc alpha() {}\n")
			write(t, filepath.Join(root, "b.go"), "package sample\nfunc beta() {}\n")
			report := completed(t, root, path)
			rule := findTargetRule(t, &report, namingRule)
			original := slices.Clone(rule.Targets)
			rule.Objects = []string{original[0].Object, original[1].Object}
			rule.Evidence = nil
			switch damage {
			case "add":
				rule.Targets = append(rule.Targets, Target{Object: "b.go:2#Extra", Symbol: "Extra", Location: "b.go:2"})
			case "remove":
				rule.Targets = rule.Targets[1:]
				rule.Objects = rule.Objects[1:]
				rule.Files[0].Objects = nil
			case "change":
				rule.Targets[0].Symbol = "altered"
			case "reorder":
				slices.Reverse(rule.Targets)
			case "duplicate":
				rule.Targets = append(rule.Targets, rule.Targets[0])
			}
			save(t, path, report)
			before, _ := os.ReadFile(path)
			state, err := NameProgress(root, path, "XR-1", "", "")
			if err != nil {
				t.Fatalf("повреждённый targets остановил очередь: %v", err)
			}
			if state.TargetsTotal != 2 || len(state.OpenFiles) != 2 || state.ReadyForSummary {
				t.Fatalf("обязательные цели потеряны или принят итог: %+v", state)
			}
			if state.OpenFiles[0].Goals[0].Object != original[0].Object || state.OpenFiles[1].Goals[0].Object != original[1].Object {
				t.Fatalf("очередь не построена по исходному снимку: %+v", state.OpenFiles)
			}
			body, _ := json.Marshal(state)
			var wire map[string]json.RawMessage
			json.Unmarshal(body, &wire)
			var issues []map[string]json.RawMessage
			json.Unmarshal(wire["inventory_issues"], &issues)
			if len(issues) != 1 || string(issues[0]["code"]) != `"targets_modified"` {
				t.Fatalf("нет отдельной ошибки перечня: %s", body)
			}
			field := map[string]string{"add": "added", "remove": "removed", "change": "changed", "reorder": "sequence_changed", "duplicate": "sequence_changed"}[damage]
			if len(issues[0][field]) == 0 {
				t.Fatalf("нет различий %s: %s", field, body)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("audit-progress изменил исходный аудит")
			}
			assertTargetCheck(t, CheckCoverage(root, path, "XR-1", filepath.Join(t.TempDir(), "report.md")), "перечень targets из диффа изменён")
			write(t, filepath.Join(root, "b.go"), "package sample\nfunc changed() {}\n")
			_, err = NameProgress(root, path, "XR-1", "", "")
			assertTargetCheck(t, err, "код или постановка изменились")
		})
	}
}

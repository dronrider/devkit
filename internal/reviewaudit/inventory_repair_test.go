package reviewaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestRepairTargetsPreservesEveryOtherField(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "a.go"), "package sample\nfunc createInvoice() string { return \"archive\" }\n")
	report := completed(t, root, path)
	rule := findTargetRule(t, &report, namingRule)
	original := slices.Clone(rule.Targets)
	rule.Targets = append(rule.Targets, Target{Object: "a.go:1#Extra"})
	rule.Evidence = []Evidence{{Object: original[0].Object, Check: "result", Result: "violated",
		ObjectComparison: &NameObjectComparison{Expected: []string{"Invoice"}, Actual: "archive"}}}
	save(t, path, report)
	before, _ := os.ReadFile(path)
	var want map[string]any
	json.Unmarshal(before, &want)
	// Explicit empty fields must also survive without omitempty normalization.
	want["architecture"].(map[string]any)["reason"] = ""
	data, _ := json.Marshal(want)
	write(t, path, string(data))
	before, _ = os.ReadFile(path)
	json.Unmarshal(before, &want)
	if _, err := RepairTargets(root, path, "XR-1", "", ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	var got map[string]any
	json.Unmarshal(after, &got)
	for _, raw := range want["rules"].([]any) {
		r := raw.(map[string]any)
		if r["id"] == namingRule {
			value, _ := json.Marshal(original)
			var targets any
			json.Unmarshal(value, &targets)
			r["targets"] = targets
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("восстановление изменило поля вне targets:\n%s", after)
	}
	backups, _ := filepath.Glob(path + ".previous-*")
	if len(backups) != 1 {
		t.Fatalf("ожидалась одна копия исходного аудита: %v", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if string(backup) != string(before) {
		t.Fatal("резервная копия отличается от исходного аудита")
	}
	if _, err := RepairTargets(root, path, "XR-1", "", ""); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(after) {
		t.Fatal("повторное восстановление переписало аудит")
	}
}

func TestRepairTargetsRejectsChangedSnapshot(t *testing.T) {
	for _, changed := range []string{"code", "request", "source", "rule", "head"} {
		t.Run(changed, func(t *testing.T) {
			root, path := fixture(t)
			write(t, filepath.Join(root, "a.go"), "package sample\nfunc alpha() {}\n")
			report := completed(t, root, path)
			findTargetRule(t, &report, namingRule).Targets = nil
			switch changed {
			case "code":
				write(t, filepath.Join(root, "a.go"), "package sample\nfunc different() {}\n")
			case "request":
				write(t, filepath.Join(root, "docs/tasks/XR-1.md"), "# Другая задача\n")
			case "source":
				write(t, filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.style.md"), "- **STYLE-001.** Другое правило.\n")
			case "rule":
				findTargetRule(t, &report, namingRule).Text = "подменено"
			case "head":
				report.HeadSHA = "wrong"
			}
			save(t, path, report)
			before, _ := os.ReadFile(path)
			if _, err := RepairTargets(root, path, "XR-1", "", ""); err == nil {
				t.Fatal("принят изменившийся снимок")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("отказавшая команда изменила аудит")
			}
		})
	}
}

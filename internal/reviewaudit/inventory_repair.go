package reviewaudit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type TargetChange struct {
	Before Target `json:"before"`
	After  Target `json:"after"`
}

type TargetInventoryIssue struct {
	Rule     string         `json:"rule"`
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Added    []Target       `json:"added,omitempty"`
	Removed  []Target       `json:"removed,omitempty"`
	Changed  []TargetChange `json:"changed,omitempty"`
	Sequence bool           `json:"sequence_changed,omitempty"`
	Next     string         `json:"next"`
}

// Only the in-memory view is repaired. Coverage still checks the saved inventory.
func canonicalTargetInventory(report, snapshot Report) (Report, []TargetInventoryIssue) {
	report.Rules = slices.Clone(report.Rules)
	var issues []TargetInventoryIssue
	for i, rule := range report.Rules {
		index := slices.IndexFunc(snapshot.Rules, func(r Rule) bool { return r.ID == rule.ID })
		if index == -1 {
			continue // validateInventory rejects unknown and duplicate rules.
		}
		want := snapshot.Rules[index].Targets
		if validateTargetList(rule, want) == nil {
			continue
		}
		issue := TargetInventoryIssue{Rule: rule.ID, Code: "targets_modified",
			Message: "перечень targets изменён; очередь рассчитана по снимку",
			Next:    "Выполните review audit-repair <ID> --targets. Дополнительные объекты зарегистрируйте в objects правила и files.objects; сохраните evidence."}
		for _, saved := range rule.Targets {
			j := slices.IndexFunc(want, func(t Target) bool { return t.Object == saved.Object })
			if j == -1 {
				issue.Added = append(issue.Added, saved)
			} else if !sameTarget(saved, want[j]) {
				issue.Changed = append(issue.Changed, TargetChange{Before: saved, After: want[j]})
			}
		}
		for _, target := range want {
			if !slices.ContainsFunc(rule.Targets, func(t Target) bool { return t.Object == target.Object }) {
				issue.Removed = append(issue.Removed, target)
			}
		}
		issue.Sequence = len(issue.Added)+len(issue.Removed)+len(issue.Changed) == 0
		issues = append(issues, issue)
		report.Rules[i].Targets = slices.Clone(want)
	}
	return report, issues
}

// RepairTargets restores derived lists without refreshing or rewriting conclusions.
func RepairTargets(root, path, id, request, summary string) (string, error) {
	lock, err := lockAuditDirectory(path)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	_, report, snapshot, _, err := snapshotForCoverage(root, path, id, summary, request)
	if err != nil {
		return "", err
	}
	canonical, issues := canonicalTargetInventory(report, snapshot)
	if _, err := validateInventory(canonical, snapshot, id); err != nil {
		return "", err
	}
	if report.HeadSHA != snapshot.HeadSHA {
		return "", fmt.Errorf("HEAD изменился после отчёта; восстановление targets не обновляет снимок")
	}
	if len(issues) == 0 {
		return "targets совпадают со снимком; отчёт не изменён", nil
	}
	// Raw fields preserve explicit empty arrays and all evidence fields.
	var document map[string]json.RawMessage
	if err := json.Unmarshal(before, &document); err != nil {
		return "", err
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(document["rules"], &rules); err != nil {
		return "", err
	}
	for i := range rules {
		if !slices.ContainsFunc(issues, func(issue TargetInventoryIssue) bool { return issue.Rule == report.Rules[i].ID }) {
			continue
		}
		data, err := json.Marshal(canonical.Rules[i].Targets)
		if err != nil {
			return "", err
		}
		rules[i]["targets"] = data
	}
	document["rules"], err = json.Marshal(rules)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".audit-write-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(before, current) {
		return "", fmt.Errorf("аудит изменился во время восстановления; повторите команду")
	}
	backup, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".previous-")
	if err != nil {
		return "", err
	}
	_, writeErr = backup.Write(before)
	closeErr = backup.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return "targets восстановлены; доказательства и выводы сохранены; прежний отчёт: " + backup.Name(), nil
}

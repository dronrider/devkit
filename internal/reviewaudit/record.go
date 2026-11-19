package reviewaudit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// The directory inode stays the same when a writer replaces the report by rename.
func lockAuditDirectory(path string) (*os.File, error) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		dir.Close()
		return nil, fmt.Errorf("блокировка аудита %s: %w", path, err)
	}
	return dir, nil
}

func replaceAuditFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".audit-write-")
	if err != nil {
		return fmt.Errorf("запись аудита %s: %w", path, err)
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Chmod(mode.Perm())
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("запись аудита %s: %w", path, err)
	}
	return nil
}

// RecordNameEvidence replaces only the submitted checks. The progress and coverage
// readers also accept partially filled audits, so rejected evidence stays editable.
func RecordNameEvidence(root, path, id, ruleID, object, input, request, summary string) (string, error) {
	if (ruleID != "STYLE-001" && ruleID != namingRule) || object == "" || input == "" {
		return "", fmt.Errorf("укажите --rule STYLE-001 --object <ключ цели> --input <файл JSON>")
	}
	body, err := os.ReadFile(input)
	if err != nil {
		return "", err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		return "", fmt.Errorf("%s: %w", input, err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("%s: нужен массив записей цели", input)
	}
	lock, err := lockAuditDirectory(path)
	if err != nil {
		return "", fmt.Errorf("запись аудита %s: %w", path, err)
	}
	defer lock.Close()

	root, report, snapshot, output, err := snapshotForCoverage(root, path, id, summary, request)
	if err != nil {
		return "", err
	}
	if report.HeadSHA != snapshot.HeadSHA {
		return "", fmt.Errorf("HEAD изменился после создания аудита")
	}
	i := slices.IndexFunc(report.Rules, func(r Rule) bool { return r.ID == namingRule })
	if i < 0 || !slices.Contains(report.Rules[i].Objects, object) {
		return "", fmt.Errorf("%s: цель %q отсутствует в objects правила STYLE-001", path, object)
	}
	// Open the original for writing without truncating it. A writable directory
	// alone would permit renaming over a read-only report.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	var doc map[string]json.RawMessage
	if err := json.NewDecoder(file).Decode(&doc); err != nil {
		return "", err
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(doc["rules"], &rules); err != nil {
		return "", err
	}
	rule := &report.Rules[i]
	rules[i]["evidence"], err = mergeNameEvidence(rule, rules[i]["evidence"], rows, object, input)
	if err != nil {
		return "", err
	}
	progress, err := nameProgressForSnapshot(root, path, id, output, report, snapshot)
	if err != nil {
		return "", err
	}
	doc["rules"], err = json.Marshal(rules)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := replaceAuditFile(path, append(data, '\n'), info.Mode()); err != nil {
		return "", err
	}
	return recordFeedback(path, object, rule.Evidence, progress), nil
}

func mergeNameEvidence(rule *Rule, original json.RawMessage, rows []json.RawMessage, object, input string) (json.RawMessage, error) {
	var evidence []json.RawMessage
	if err := json.Unmarshal(original, &evidence); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		var entry Evidence
		decoder := json.NewDecoder(bytes.NewReader(row))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&entry); err != nil {
			return nil, fmt.Errorf("%s: %w", input, err)
		}
		if entry.Object != object || !slices.Contains(requiredChecks(namingRule), entry.Check) || seen[entry.Check] {
			return nil, fmt.Errorf("%s: чужая цель, неизвестная или повторная подпроверка %q / %q", input, entry.Object, entry.Check)
		}
		seen[entry.Check] = true
		j := slices.IndexFunc(rule.Evidence, func(e Evidence) bool { return e.Object == object && e.Check == entry.Check })
		if j < 0 {
			evidence = append(evidence, row)
			rule.Evidence = append(rule.Evidence, entry)
		} else {
			evidence[j], rule.Evidence[j] = row, entry
		}
	}
	return json.Marshal(evidence)
}

func recordFeedback(path, object string, evidence []Evidence, progress NameProgressState) string {
	goal := NameProgressGoal{}
	for _, file := range progress.OpenFiles {
		for _, open := range file.Goals {
			if open.Object == object {
				goal = open
			}
		}
	}
	lines := []string{fmt.Sprintf("%s: доказательства сохранены в %s", object, path)}
	for _, entry := range evidence {
		if entry.Object == object && !slices.Contains(goal.RejectedChecks, entry.Check) {
			lines = append(lines, fmt.Sprintf("%s: %s", entry.Check, entry.Result))
		}
	}
	if len(goal.MissingChecks) != 0 {
		lines = append(lines, "Заполните: "+strings.Join(goal.MissingChecks, ", "))
	}
	lines = append(lines, goal.Issues...)
	for _, blocker := range goal.Blockers {
		if blocker.Stage == "conclusion" || blocker.Code == "missing_consumer" {
			lines = append(lines, blocker.Next)
		}
	}
	if slices.Contains(progress.Unassigned, object) {
		lines = append(lines, "Укажите файл объявления в files.objects")
	}
	return strings.Join(lines, "\n")
}

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
	result, evidence, err := recordNameEvidence(root, path, id, object, input, request, summary)
	if err != nil {
		return "", err
	}
	return recordFeedback(path, object, evidence, *result.Progress), nil
}

type NameEvidenceBatchResult struct {
	Saved     bool                     `json:"saved"`
	Submitted int                      `json:"submitted"`
	Progress  *NameProgressState       `json:"progress,omitempty"`
	Issues    []NameEvidenceBatchIssue `json:"issues,omitempty"`
}

type NameEvidenceBatchIssue struct {
	Row     int    `json:"row,omitempty"`
	Object  string `json:"object,omitempty"`
	Check   string `json:"check,omitempty"`
	Message string `json:"message"`
}

// RecordNameEvidenceBatch checks evidence for multiple objects against one snapshot.
// Invalid submitted rows leave the audit unchanged and are returned in Issues.
// Missing or rejected evidence outside the submission is reported in Progress.
func RecordNameEvidenceBatch(root, path, id, input, request, summary string) (NameEvidenceBatchResult, error) {
	if input == "" {
		return NameEvidenceBatchResult{}, fmt.Errorf("укажите --batch --input <файл JSON>")
	}
	result, _, err := recordNameEvidence(root, path, id, "", input, request, summary)
	return result, err
}

func recordNameEvidence(root, path, id, object, input, request, summary string) (NameEvidenceBatchResult, []Evidence, error) {
	batch := object == ""
	var result NameEvidenceBatchResult
	body, err := os.ReadFile(input)
	if err != nil {
		return result, nil, err
	}
	var rows []json.RawMessage
	err = json.Unmarshal(body, &rows)
	if err == nil && len(rows) == 0 {
		err = fmt.Errorf("нужен массив записей цели")
	}
	if err != nil {
		if batch {
			result.Issues = []NameEvidenceBatchIssue{{Message: fmt.Sprintf("%s: %v", input, err)}}
			return result, nil, nil
		}
		return result, nil, fmt.Errorf("%s: %w", input, err)
	}
	result.Submitted = len(rows)
	lock, err := lockAuditDirectory(path)
	if err != nil {
		return result, nil, fmt.Errorf("запись аудита %s: %w", path, err)
	}
	defer lock.Close()

	root, report, snapshot, output, err := snapshotForCoverage(root, path, id, summary, request)
	if err != nil {
		return result, nil, err
	}
	if report.HeadSHA != snapshot.HeadSHA {
		return result, nil, fmt.Errorf("HEAD изменился после создания аудита")
	}
	i := slices.IndexFunc(report.Rules, func(r Rule) bool { return r.ID == namingRule })
	if !batch && (i < 0 || !slices.Contains(report.Rules[i].Objects, object)) {
		return result, nil, fmt.Errorf("%s: цель %q отсутствует в objects правила STYLE-001", path, object)
	}
	if i < 0 {
		return result, nil, fmt.Errorf("%s: в отчёте нет правила STYLE-001", path)
	}
	rule := &report.Rules[i]
	entries, issues := prepareNameEvidence(rule.Objects, rows, object)
	if !batch && len(issues) != 0 {
		return result, nil, fmt.Errorf("%s: %s", input, issues[0].Message)
	}
	result.Issues = issues
	// Open the original for writing without truncating it. A writable directory
	// alone would permit renaming over a read-only report.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return result, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.NewDecoder(file).Decode(&doc); err != nil {
		return result, nil, err
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(doc["rules"], &rules); err != nil {
		return result, nil, err
	}
	rules[i]["evidence"], err = mergeNameEvidence(rule, rules[i]["evidence"], entries)
	if err != nil {
		return result, nil, err
	}
	checked, err := nameProgressForSnapshot(root, path, id, output, report, snapshot)
	if err != nil {
		return result, nil, err
	}
	result.Progress = &checked.state
	if batch {
		result.Issues = append(result.Issues, rejectedNameEvidenceIssues(entries, checked.issues)...)
		slices.SortStableFunc(result.Issues, func(a, b NameEvidenceBatchIssue) int { return a.Row - b.Row })
		if len(result.Issues) != 0 {
			return result, nil, nil
		}
		head, err := git(root, "rev-parse", "HEAD")
		if err != nil {
			return result, nil, err
		}
		if strings.TrimSpace(head) != snapshot.HeadSHA {
			return result, nil, fmt.Errorf("HEAD изменился во время проверки пакета доказательств")
		}
	}
	doc["rules"], err = json.Marshal(rules)
	if err != nil {
		return result, nil, err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return result, nil, err
	}
	if err := replaceAuditFile(path, append(data, '\n'), info.Mode()); err != nil {
		return result, nil, err
	}
	result.Saved = true
	return result, rule.Evidence, nil
}

type nameEvidenceRow struct {
	entry  Evidence
	raw    json.RawMessage
	number int
}

func prepareNameEvidence(objects []string, rows []json.RawMessage, object string) ([]nameEvidenceRow, []NameEvidenceBatchIssue) {
	known := make(map[string]bool, len(objects))
	for _, object := range objects {
		known[object] = true
	}
	checks := requiredChecks(namingRule)
	seen := make(map[evidenceKey]bool, len(rows))
	var entries []nameEvidenceRow
	var issues []NameEvidenceBatchIssue
	for i, row := range rows {
		var entry Evidence
		decoder := json.NewDecoder(bytes.NewReader(row))
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&entry)
		key := evidenceKey{entry.Object, entry.Check}
		if err == nil {
			validCheck, duplicate := slices.Contains(checks, entry.Check), seen[key]
			switch {
			case object != "" && (entry.Object != object || !validCheck || duplicate):
				err = fmt.Errorf("чужая цель, неизвестная или повторная подпроверка %q / %q", entry.Object, entry.Check)
			case object == "" && (strings.TrimSpace(entry.Object) == "" || !known[entry.Object]):
				err = fmt.Errorf("цель %q отсутствует в objects правила STYLE-001", entry.Object)
			case !validCheck:
				err = fmt.Errorf("неизвестная подпроверка %q", entry.Check)
			case duplicate:
				err = fmt.Errorf("повторное доказательство для %s", key)
			}
		}
		if err != nil {
			issues = append(issues, NameEvidenceBatchIssue{Row: i + 1, Object: entry.Object, Check: entry.Check, Message: err.Error()})
			continue
		}
		seen[key] = true
		entries = append(entries, nameEvidenceRow{entry: entry, raw: row, number: i + 1})
	}
	return entries, issues
}

func mergeNameEvidence(rule *Rule, original json.RawMessage, entries []nameEvidenceRow) (json.RawMessage, error) {
	var evidence []json.RawMessage
	if err := json.Unmarshal(original, &evidence); err != nil {
		return nil, err
	}
	indices := make(map[evidenceKey]int, len(rule.Evidence))
	for i, entry := range rule.Evidence {
		key := evidenceKey{entry.Object, entry.Check}
		if _, found := indices[key]; !found {
			indices[key] = i
		}
	}
	for _, row := range entries {
		if i, found := indices[evidenceKey{row.entry.Object, row.entry.Check}]; found {
			evidence[i], rule.Evidence[i] = row.raw, row.entry
			continue
		}
		evidence = append(evidence, row.raw)
		rule.Evidence = append(rule.Evidence, row.entry)
	}
	return json.Marshal(evidence)
}

func rejectedNameEvidenceIssues(entries []nameEvidenceRow, checks error) []NameEvidenceBatchIssue {
	submitted := make(map[evidenceKey]int, len(entries))
	for _, row := range entries {
		submitted[evidenceKey{row.entry.Object, row.entry.Check}] = row.number
	}
	var issues []NameEvidenceBatchIssue
	eachAuditIssue(checks, func(err error) {
		if issue, ok := err.(*objectIssue); ok && !issue.missing {
			if row, found := submitted[issue.key]; found {
				issues = append(issues, NameEvidenceBatchIssue{Row: row, Object: issue.key.object,
					Check: issue.key.check, Message: issue.err.Error()})
			}
		}
	})
	return issues
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

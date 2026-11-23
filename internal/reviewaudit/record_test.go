package reviewaudit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Removing validation of a submitted runtime proof would persist the tampered
// payload or accept the repository's own allowlist.
func TestRecordNameEvidenceBatchChecksSignedProof(t *testing.T) {
	source, key, run, makeEvidence := nameRuntimeFixture(t)
	root, path := source.root, source.reportPath
	if _, err := Create(root, path, "XR-1", "HEAD^", false); err != nil {
		t.Fatal(err)
	}
	report, err := readReport(path)
	if err != nil {
		t.Fatal(err)
	}
	rule := findTargetRule(t, &report, namingRule)
	index := slices.IndexFunc(rule.Targets, func(target Target) bool { return target.Symbol == "SetActivity" })
	if index < 0 {
		t.Fatalf("нет цели для подписанного прогона: %+v", rule.Targets)
	}
	target := rule.Targets[index]
	rule.Applicability = "applicable"
	save(t, path, report)
	evidence := makeEvidence()
	evidence.Object, evidence.Location, evidence.Observation = target.Object, target.Location, "Независимый прогон наблюдал запись и чтение"
	run.Object = target.Object
	proof := signNameRuntimeRecord(t, key, run)
	evidence.ObjectComparison.Proof.Runtime = proof
	input := filepath.Join(t.TempDir(), "evidence.json")
	record := func(t *testing.T) (NameEvidenceBatchResult, error) {
		t.Helper()
		body, err := json.Marshal([]Evidence{evidence})
		if err != nil {
			t.Fatal(err)
		}
		write(t, input, string(body))
		return RecordNameEvidenceBatch(root, path, "XR-1", input, "", "")
	}
	result, err := record(t)
	if err != nil || !result.Saved {
		t.Fatalf("подписанный импорт: %+v, %v", result, err)
	}
	saved, err := readReport(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := findTargetRule(t, &saved, namingRule).Evidence
	if len(entries) != 1 || *entries[0].ObjectComparison.Proof.Runtime != *proof {
		t.Fatalf("изменены подписанные байты: %+v", entries)
	}
	read := func() []byte {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	before := read()
	for _, tc := range []struct {
		name string
		edit func(*testing.T)
		want string
	}{
		{"изменён payload", func(t *testing.T) {
			original := proof.Payload
			proof.Payload += " "
			t.Cleanup(func() { proof.Payload = original })
		}, "подпись не подтверждена"},
		{"список ключей в репозитории", func(t *testing.T) {
			allowed := filepath.Join(root, ".git", "allowed_signers")
			writeNameRuntimeAllowed(t, allowed, key)
			t.Setenv(nameRuntimeSigners, allowed)
		}, "вне репозитория"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.edit(t)
			result, err := record(t)
			if err != nil || result.Saved || !bytes.Equal(before, read()) || len(result.Issues) != 1 ||
				result.Issues[0].Row != 1 || !strings.Contains(result.Issues[0].Message, tc.want) {
				t.Fatalf("неподтверждённая подпись: %+v, %v", result, err)
			}
		})
	}
	t.Run("изменена постановка", func(t *testing.T) {
		task := taskPath(path)
		body, err := os.ReadFile(task)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { write(t, task, string(body)) })
		write(t, task, strings.Replace(string(body), "Правильный результат.", "Другой результат.", 1))
		result, err := record(t)
		assertTargetCheck(t, err, "код или постановка изменились")
		if result.Saved || !bytes.Equal(before, read()) {
			t.Fatalf("устаревший отчёт изменён: %+v", result)
		}
	})
	gitT(t, root, "commit", "--allow-empty", "-qm", "advance HEAD")
	result, err = record(t)
	assertTargetCheck(t, err, "HEAD изменился")
	if result.Saved || !bytes.Equal(before, read()) {
		t.Fatalf("устаревший отчёт изменён: %+v", result)
	}
}

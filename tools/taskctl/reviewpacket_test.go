package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIReviewVerdict(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	const id = "XR-005"
	audit := completeReviewAudit(t, root, id)
	currentHead := func() string {
		t.Helper()
		sha, err := gitRevParse(root, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	prior := currentHead()
	dir := t.TempDir()
	put := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	checks := filepath.Join(dir, "checks")
	put(checks, "head_sha: "+prior+"\nPASS\n")
	calls := filepath.Join(dir, "calls")
	t.Setenv("VERDICT_CLI_TEST_CALLS", calls)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	reply := `{"status":"changes_requested","issues":[{"object":"code.go:1","finding":"Уточнить контракт","evidence":"Строка не задаёт результат"}],"missing_facts":[]}`
	put(filepath.Join(dir, "agentctl"), `#!/bin/sh
printf '%s\n' invoked >> "$VERDICT_CLI_TEST_CALLS"
grep -Fq 'Новое замечание после проверенного коммита' || exit 1
printf '%s\n' '`+reviewVerdictTestTrace+`' >&2
cat <<'EVENTS'
`+reviewVerdictTestStart+verdictTestReply(reply)+reviewVerdictTestEnd+"EVENTS\n")
	run := func(extra ...string) (string, error) {
		return runCLI(t, append([]string{"-C", root, "review", "verdict", id, "--checks", checks, "--model", "gpt-6.2-sol", "--effort", "high"}, extra...)...)
	}
	original, err := os.ReadFile(audit)
	if err != nil {
		t.Fatal(err)
	}
	put(audit, strings.Replace(string(original), `"result": "checked"`, `"result": "pending"`, 1))
	if output, err := run(); err == nil || !strings.Contains(output, "архитектурная оценка") {
		t.Fatalf("запуск при неполном аудите: %s %v", output, err)
	}
	put(audit, string(original))

	card := taskFileAbs(root, id)
	doc, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	commitCard := func(body string) {
		t.Helper()
		put(card, body)
		cmd := exec.Command("git", "-C", root, "commit", "-qam", "review metadata")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("коммит карточки: %s %v", out, err)
		}
		put(checks, "head_sha: "+currentHead()+"\nPASS\n")
	}
	commitCard(string(doc) + "\n## Ревью\n\n- Открытое замечание.\n")
	if output, err := run("--since", prior); err == nil || !strings.Contains(output, "нет чистого вердикта") {
		t.Fatalf("дельта без прежнего вердикта: %s %v", output, err)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("Codex запускался до прохождения ворот: %v", err)
	}
	commitCard(string(doc) + "\n## Ревью\n\n- Вердикт: без замечаний до " + prior + ".\n- Новое замечание после проверенного коммита.\n")
	output, err := run("--since", prior)
	var verdict struct {
		HeadSHA     string              `json:"head_sha"`
		DiffBaseSHA string              `json:"diff_base_sha"`
		Result      reviewVerdictResult `json:"result"`
	}
	if err != nil || json.Unmarshal([]byte(output), &verdict) != nil || verdict.HeadSHA != currentHead() ||
		verdict.DiffBaseSHA != prior || verdict.Result.ModelRequests != 1 || verdict.Result.Verdict.Status != "changes_requested" ||
		len(verdict.Result.Verdict.Issues) != 1 {
		t.Fatalf("результат CLI: %s %v", output, err)
	}
	if log, err := os.ReadFile(calls); err != nil || string(log) != "invoked\n" {
		t.Fatalf("ожидался один запуск Codex: %q %v", log, err)
	}
}

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/reviewaudit"
)

// The installed CLI must accept the signed JavaScript result as checked and
// re-evaluate the saved proof when its signature or source no longer matches.
func TestCLIRuntimeResultInstalled(t *testing.T) {
	artifact := os.Getenv("DEVKIT_TASKCTL_ARTIFACT")
	node, err := exec.LookPath("node")
	if err != nil {
		if artifact != "" {
			t.Fatal("для проверки установленного taskctl нужен Node.js: ", err)
		}
		t.Skip("для прогона JavaScript нужен Node.js")
	}
	root := setup(t)
	t.Setenv("DEVKIT_HOME", filepath.Clean(filepath.Join(repoKitPlans(t), "..", "..")))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitSetup(t, root)
	base := gitOut(t, root, "rev-parse", "HEAD")
	const (
		id          = "XR-005"
		file        = "sample.js"
		symbol      = "readReport"
		declaration = file + ":1"
		object      = declaration + "#" + symbol
		returnAt    = file + ":2"
		returnExpr  = "input.report"
		returnQuote = "return " + returnExpr + ";"
		readAt      = file + ":17"
		readExpr    = "reportResult"
		readQuote   = "console.log(JSON.stringify(observation(" + readExpr + ")));"
		identity    = "reports:current"
	)
	const source = `function readReport(input) {
  ` + returnQuote + `
}

function observation(value) {
  return {identity: value.id, value: JSON.stringify(value)};
}

const report = {id: "reports:current", pages: Number(process.argv[2])};
const input = {
  get report() {
    console.log(JSON.stringify(observation(report)));
    return report;
  },
};
const reportResult = readReport(input);
` + readQuote + `
if (reportResult !== report) throw new Error("the consumer received a different object");
`
	filePath := filepath.Join(root, file)
	writeRuntimeResultFile(t, filePath, []byte(source))
	gitOut(t, root, "add", file)
	gitOut(t, root, "commit", "-qm", "add JavaScript report")
	commit := gitOut(t, root, "rev-parse", "HEAD")
	if status := gitOut(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("для прогона нужен чистый коммит: %s", status)
	}

	type observation struct {
		Identity string `json:"identity"`
		Value    string `json:"value"`
		At       string `json:"at"`
		Quote    string `json:"quote"`
		Expr     string `json:"expression"`
	}
	type run struct {
		Command  []string    `json:"command"`
		Returned observation `json:"returned"`
		Read     observation `json:"read"`
	}
	observe := func(pages, want string) run {
		t.Helper()
		argv := []string{node, file, pages}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = root
		cmd.Env = []string{"HOME=" + os.Getenv("HOME")}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) != 2 {
			t.Fatalf("ожидались два наблюдения: %s", out)
		}
		observed := run{Command: argv}
		for i, value := range []*observation{&observed.Returned, &observed.Read} {
			if err := json.Unmarshal([]byte(lines[i]), value); err != nil {
				t.Fatalf("наблюдение %q: %v", lines[i], err)
			}
			if value.Identity != identity || value.Value != want {
				t.Fatalf("ожидался %s со значением %s, получено %+v", identity, want, value)
			}
		}
		observed.Returned.At, observed.Returned.Quote, observed.Returned.Expr = returnAt, returnQuote, returnExpr
		observed.Read.At, observed.Read.Quote, observed.Read.Expr = readAt, readQuote, readExpr
		return observed
	}
	positive := observe("7", `{"id":"reports:current","pages":7}`)
	negative := observe("2", `{"id":"reports:current","pages":2}`)
	word := "Report"
	words := []reviewaudit.NameProofWord{{Word: word, Role: "object", Fact: reviewaudit.ReviewFact{
		Location: returnAt, Quote: returnQuote, Value: returnExpr, Text: "Свойство report содержит возвращаемый отчёт",
	}}}
	payload := marshalRuntimeResult(t, struct {
		Version     int                         `json:"version"`
		Task        string                      `json:"task"`
		Commit      string                      `json:"commit"`
		Object      string                      `json:"object"`
		Declaration string                      `json:"declaration"`
		Actual      string                      `json:"actual"`
		Expected    []string                    `json:"expected"`
		Words       []reviewaudit.NameProofWord `json:"words"`
		run
		Negative run `json:"negative"`
	}{Version: 2, Task: id, Commit: commit, Object: object, Declaration: declaration,
		Actual: returnExpr, Expected: []string{word}, Words: words, run: positive, Negative: negative})

	// Create the signing key only after both independent observations.
	reviewer := t.TempDir()
	key := filepath.Join(reviewer, "reviewer")
	sshKeygen := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("ssh-keygen", args...).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen %v: %v\n%s", args, err, out)
		}
	}
	sshKeygen("-q", "-t", "ed25519", "-N", "", "-f", key)
	const principal = "test-reviewer"
	allowed := filepath.Join(reviewer, "allowed_signers")
	writeRuntimeResultFile(t, allowed, []byte(principal+" "+string(readRuntimeResultFile(t, key+".pub"))))
	t.Setenv("DEVKIT_STYLE_RUN_ALLOWED_SIGNERS", allowed)
	receipt := filepath.Join(reviewer, "receipt.json")
	writeRuntimeResultFile(t, receipt, payload)
	sshKeygen("-Y", "sign", "-f", key, "-n", "devkit-style-001", receipt)
	signed := &reviewaudit.NameRuntimeProof{
		Runner: principal, Payload: string(payload), Signature: string(readRuntimeResultFile(t, receipt+".sig")),
	}

	cli := func(args ...string) string {
		t.Helper()
		args = append([]string{"-C", root, "review"}, args...)
		var output string
		var err error
		if artifact == "" {
			output, err = runCLI(t, args...)
		} else {
			out, runErr := exec.Command(artifact, args...).CombinedOutput()
			output, err = string(out), runErr
		}
		if err != nil {
			t.Fatalf("taskctl %v: %v\n%s", args, err, output)
		}
		return output
	}
	reportPath := filepath.Join(root, "docs", "tasks", id+".review-audit.json")
	prepareAudit := func(flags ...string) {
		t.Helper()
		cli(append([]string{"audit", id}, flags...)...)
		var report reviewaudit.Report
		if err := json.Unmarshal(readRuntimeResultFile(t, reportPath), &report); err != nil {
			t.Fatal(err)
		}
		for i := range report.Rules {
			rule := &report.Rules[i]
			if rule.ID == "common-style:STYLE-001" {
				rule.Applicability, rule.Result, rule.Reason = "applicable", "checked", "Функция читает отчёт"
				rule.Objects = []string{object}
				rule.Files = []reviewaudit.FileReview{{Path: file, Objects: []string{object}}}
				writeRuntimeResultFile(t, reportPath, marshalRuntimeResult(t, report))
				return
			}
		}
		t.Fatal("в отчёте отсутствует STYLE-001")
	}
	prepareAudit("--base", base)
	entries := []reviewaudit.Evidence{
		{Object: object, Check: "meaning", Result: "checked", Symbol: symbol, Location: declaration, Observation: "Имя соответствует чтению отчёта",
			WordChecks: []reviewaudit.WordCheck{
				{Word: "read", Meaning: "прочитать", Fact: "Читает свойство входа", Location: returnAt, Result: "checked"},
				{Word: word, Meaning: "отчёт", Fact: "Свойство report содержит отчёт", Location: returnAt, Result: "checked"},
			}},
		{Object: object, Check: "writes", Result: "not_applicable", Location: declaration, Observation: "Функция читает свойство входа"},
		{Object: object, Check: "result", Result: "checked", Location: returnAt, Observation: "Потребитель получил тот же отчёт",
			ObjectComparison: &reviewaudit.NameObjectComparison{
				Expected: []string{word}, Actual: returnExpr, ActualAt: returnAt,
				Proof: &reviewaudit.NameObjectProof{Expression: returnExpr, Runtime: signed, Words: words},
			},
			ResultEvidence: &reviewaudit.ResultEvidence{Word: &word, Returned: returnExpr, ReturnAt: returnAt, Consumer: readExpr, ConsumerAt: readAt}},
	}
	input := filepath.Join(reviewer, "evidence.json")
	record := func() string {
		t.Helper()
		writeRuntimeResultFile(t, input, marshalRuntimeResult(t, entries))
		return cli("audit-record", id, "--rule", "STYLE-001", "--object", object, "--input", input)
	}
	progress := func() reviewaudit.NameProgressState {
		t.Helper()
		out := cli("audit-progress", id, "--json")
		var state reviewaudit.NameProgressState
		if err := json.Unmarshal([]byte(out), &state); err != nil {
			t.Fatalf("audit-progress: %v\n%s", err, out)
		}
		return state
	}
	accepted := record()
	if state := progress(); !strings.Contains(accepted, "result: checked") || state.Closed != 1 || state.Rejected != 0 || !state.ReadyForSummary {
		t.Fatalf("подписанный результат не принят:\n%s\n%+v", accepted, state)
	}
	t.Log("audit-record: result checked, audit-progress: closed 1, rejected 0")

	checkRejected := func(reason string) {
		t.Helper()
		feedback := record()
		if strings.Contains(feedback, "result: checked") || !strings.Contains(feedback, reason) {
			t.Fatalf("audit-record должен сохранить запись и показать %q:\n%s", reason, feedback)
		}
		state := progress()
		if state.Closed != 0 || state.Rejected != 1 || len(state.OpenFiles) != 1 || len(state.OpenFiles[0].Goals) != 1 {
			t.Fatalf("audit-progress должен отклонить запись: %+v", state)
		}
		goal := state.OpenFiles[0].Goals[0]
		if goal.Object != object || !slices.Equal(goal.RejectedChecks, []string{"result"}) || !strings.Contains(strings.Join(goal.Issues, "\n"), reason) {
			t.Fatalf("audit-progress должен отклонить result с причиной %q: %+v", reason, goal)
		}
		t.Logf("audit-record сохранил запись, audit-progress отклонил result: %s", reason)
	}
	// Trailing whitespace preserves the parsed JSON but changes the signed bytes.
	signed.Payload += "\n"
	checkRejected("proof.runtime.signature")
	signed.Payload = string(payload)

	writeRuntimeResultFile(t, filePath, []byte(strings.Replace(source, returnQuote, "return {...input.report};", 1)))
	prepareAudit("--refresh")
	checkRejected("proof.runtime.payload.commit")
}

func marshalRuntimeResult(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeRuntimeResultFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func readRuntimeResultFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

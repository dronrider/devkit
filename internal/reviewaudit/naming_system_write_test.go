package reviewaudit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNameSystemWrite(t *testing.T) {
	for _, tc := range []struct {
		name, kind, imported, body, call, actual, target, value, want string
	}{
		{"unsetenv", "unsetenv", `env "os"`, "", `env.Unsetenv("CODEX_THREAD_ID")`, "CODEX_THREAD_ID", "CODEX_THREAD_ID", "unset", ""},
		{"signal", "signal", `"syscall"`, "", "syscall.Kill(run.PID, syscall.SIGTERM)", "PID", "run.PID", "syscall.SIGTERM", ""},
		{"other import", "unsetenv", `env "example.org/os"`, "", `env.Unsetenv("CODEX_THREAD_ID")`, "CODEX_THREAD_ID", "CODEX_THREAD_ID", "unset", "proof.effect"},
		{"shadowed os", "unsetenv", `env "os"`, "env := local", `env.Unsetenv("CODEX_THREAD_ID")`, "CODEX_THREAD_ID", "CODEX_THREAD_ID", "unset", "proof.effect"},
		{"literal key", "unsetenv", `env "os"`, `const CODEX_THREAD_ID = "CODEX_THREAD_ID"`, "env.Unsetenv(CODEX_THREAD_ID)", "CODEX_THREAD_ID", "CODEX_THREAD_ID", "unset", "proof.effect"},
		{"different key", "unsetenv", `env "os"`, "", `env.Unsetenv("CODEX_THREAD_ID")`, "CODEX_THREAD_ID", "OTHER", "unset", "proof.effect"},
		{"unset is not empty", "unsetenv", `env "os"`, "", `env.Unsetenv("CODEX_THREAD_ID")`, "CODEX_THREAD_ID", "CODEX_THREAD_ID", `""`, "proof.effect"},
		{"shadowed syscall", "signal", `"syscall"`, "syscall := local", "syscall.Kill(run.PID, syscall.SIGTERM)", "PID", "run.PID", "syscall.SIGTERM", "proof.effect"},
		{"different pid", "signal", `"syscall"`, "", "syscall.Kill(run.PID, syscall.SIGTERM)", "PID", "other.PID", "syscall.SIGTERM", "proof.effect"},
		{"pid selector", "signal", `"syscall"`, "", "syscall.Kill(run.PID, syscall.SIGTERM)", "run", "run.PID", "syscall.SIGTERM", "proof.effect"},
		{"different signal", "signal", `"syscall"`, "", "syscall.Kill(run.PID, syscall.SIGTERM)", "PID", "run.PID", "syscall.SIGKILL", "proof.effect"},
		{"unknown kind", "kill", `"syscall"`, "", "syscall.Kill(run.PID, syscall.SIGTERM)", "PID", "run.PID", "syscall.SIGTERM", "proof.effect.kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameCurrentProofFixture(t, "package sample\nimport "+tc.imported+"\nfunc stopChecker() {\n"+tc.body+"\n"+tc.call+"\n}\n")
			e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{
				"expected":["Checker"],"actual":`+strconvQuote(tc.actual)+`,"actual_at":`+strconvQuote("@"+tc.call)+`,
				"proof":{"expression":`+strconvQuote(tc.call)+`,"effect":{"kind":`+strconvQuote(tc.kind)+`},
				"words":[{"word":"Checker","role":"object","fact":{"location":`+strconvQuote("@"+tc.call)+`,"quote":`+strconvQuote(tc.call)+`,"value":`+strconvQuote(tc.actual)+`,"text":"Изменяется состояние проверяющего."}}]}},
				"write_evidence":{"word":"Checker","target":`+strconvQuote(tc.target)+`,"value":`+strconvQuote(tc.value)+`,"write_at":`+strconvQuote("@"+tc.call)+`}}`, at)
			check := func(want string) {
				t.Helper()
				assertTargetCheck(t, checkNameEntry(e, at("func stopChecker"), identifierWords("stopChecker"), source), want)
			}
			check(tc.want)
			if tc.want != "" {
				return
			}
			e.ObjectComparison.ActualAt = at("func stopChecker")
			assertTargetCheck(t, checkNameEntry(e, at("func stopChecker"), identifierWords("stopChecker"), source), "actual_at")
			e.ObjectComparison.ActualAt = e.WriteEvidence.WriteAt
			e.ObjectComparison.Proof.Expression = tc.target
			check("proof.effect")
			e.ObjectComparison.Proof.Expression = tc.call
			e.ObjectComparison.Proof.Setting = &NameSettingWrite{}
			check("выберите")
		})
	}
}

func TestNameSystemWriteTemplate(t *testing.T) {
	templates, err := EvidenceTemplates("", "STYLE-001", "writes", "sample.go:3#stopChecker", "checked", "effect")
	if err != nil {
		t.Fatal(err)
	}
	e := templates[0]
	if e.ObjectComparison == nil || e.ObjectComparison.Proof == nil {
		t.Fatalf("нет шаблона proof.effect: %+v", e.ObjectComparison)
	}
	p := e.ObjectComparison.Proof
	wire, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Expression, "вызов") || !strings.Contains(string(wire), `"effect":{"kind":"`) ||
		len(p.Words) != 1 || p.Words[0].Word != "Checker" {
		t.Fatalf("неполный шаблон proof.effect: %+v", p)
	}
}

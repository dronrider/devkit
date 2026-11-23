package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	reviewVerdictTestStart = "{\"type\":\"thread.started\",\"thread_id\":\"t\"}\n{\"type\":\"turn.started\"}\n"
	reviewVerdictTestEnd   = "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":8,\"cached_input_tokens\":3,\"output_tokens\":2}}\n"
	reviewVerdictTestOK    = `{"status":"approve","issues":[],"missing_facts":[]}`
	reviewVerdictTestTrace = `2026-11-23T22:22:22.471384Z  INFO codex_otel.trace_safe: event.name="codex.api_request" duration_ms=3 http.response.status_code=200 attempt=0 auth.header_attached=false auth.retry_after_unauthorized=false endpoint="/responses"`
)

func verdictTestReply(answer string) string {
	return fmt.Sprintf("{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":%q}}\n", answer)
}

func TestParseReviewVerdictEvents(t *testing.T) {
	answer := `{"status":"missing_facts","issues":[{"object":"task.go","finding":"лишний вызов","evidence":"diff:22"}],"missing_facts":[{"object":"TestRound","fact":"исход прогона"}]}`
	reasoning := "{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"reasoning\",\"text\":\"summary\"}}\n"
	message := verdictTestReply(answer)
	warning := fmt.Sprintf("{\"type\":\"item.completed\",\"item\":{\"type\":\"error\",\"message\":%q}}\n", reviewVerdictNoCodeMode)
	start := strings.Replace(reviewVerdictTestStart, "{\"type\":\"turn.started\"}", warning+"{\"type\":\"turn.started\"}", 1)
	result, err := parseReviewVerdictEvents(strings.NewReader(start + reasoning + message + reviewVerdictTestEnd))
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelResponses != 1 || result.Usage != (reviewVerdictUsage{InputTokens: 8, CachedInputTokens: 3, OutputTokens: 2}) {
		t.Fatalf("неожиданный счётчик %+v", result)
	}
	if result.Verdict.Status != "missing_facts" || len(result.Verdict.MissingFacts) != 1 || len(result.Verdict.Issues) != 1 {
		t.Fatalf("неожиданный вердикт %+v", result.Verdict)
	}
	if result.Verdict.MissingFacts[0].Object != "TestRound" || result.Verdict.Issues[0].Evidence != "diff:22" {
		t.Fatalf("потеряны ссылки на объекты %+v", result.Verdict)
	}
}

func TestParseReviewVerdictRejectsInvalidRun(t *testing.T) {
	message := verdictTestReply(reviewVerdictTestOK)
	tests := []struct {
		name, events, want string
	}{
		{"extra answer", reviewVerdictTestStart + message + message + reviewVerdictTestEnd, "ответов 2"},
		{"extra turn", reviewVerdictTestStart + "{\"type\":\"turn.started\"}\n" + message + reviewVerdictTestEnd, "порядок начала"},
		{"reordered events", reviewVerdictTestEnd + message + reviewVerdictTestStart, "порядка"},
		{"truncated turn", reviewVerdictTestStart + message, "завершено 0"},
		{"command", reviewVerdictTestStart + "{\"type\":\"item.started\",\"item\":{\"type\":\"command_execution\"}}\n" + message + reviewVerdictTestEnd, "command_execution"},
		{"retry", reviewVerdictTestStart + "{\"type\":\"error\",\"message\":\"Reconnecting... 1/5\"}\n" + message + reviewVerdictTestEnd, "Reconnecting"},
		{"failed turn", reviewVerdictTestStart + "{\"type\":\"turn.failed\",\"error\":{\"message\":\"request failed\"}}\n", "request failed"},
		{"absent usage", reviewVerdictTestStart + message + "{\"type\":\"turn.completed\",\"usage\":{}}\n", "токенов"},
		{"invalid usage", reviewVerdictTestStart + message + strings.Replace(reviewVerdictTestEnd, `"cached_input_tokens":3`, `"cached_input_tokens":9`, 1), "расход"},
		{"bad JSON", reviewVerdictTestStart + verdictTestReply("no") + reviewVerdictTestEnd, "JSON"},
		{"contradictory verdict", reviewVerdictTestStart + verdictTestReply(`{"status":"missing_facts","issues":[],"missing_facts":[]}`) + reviewVerdictTestEnd, "расходится"},
		{"unnamed object", reviewVerdictTestStart + verdictTestReply(`{"status":"changes_requested","issues":[{"object":" ","finding":"f","evidence":"e"}],"missing_facts":[]}`) + reviewVerdictTestEnd, "объект"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseReviewVerdictEvents(strings.NewReader(test.events))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ошибка %v, ждали %q", err, test.want)
			}
		})
	}
}

func TestRunReviewVerdict(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REVIEW_VERDICT_TEST_DIR", dir)
	t.Setenv("REVIEW_VERDICT_TEST_EXTRA_TRACE", "")
	runner := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$REVIEW_VERDICT_TEST_DIR/args"
while [ "$1" != "--output-schema" ]; do shift; done
cat "$2" > "$REVIEW_VERDICT_TEST_DIR/schema"
cat > "$REVIEW_VERDICT_TEST_DIR/prompt"
printf '%s\n' '` + reviewVerdictTestTrace + `' >&2
if [ -n "$REVIEW_VERDICT_TEST_EXTRA_TRACE" ]; then
  printf '%s\n' "$REVIEW_VERDICT_TEST_EXTRA_TRACE" >&2
fi
cat <<'EVENTS'
` + reviewVerdictTestStart + verdictTestReply(reviewVerdictTestOK) + reviewVerdictTestEnd + `EVENTS
`
	if err := os.WriteFile(filepath.Join(dir, "agentctl"), []byte(runner), 0700); err != nil {
		t.Fatal(err)
	}
	packet := `{"id":"XR-001","diff":"+code"}`
	out, err := runReviewVerdict(context.Background(), []byte(packet), "gpt-6.2-sol", "high")
	if err != nil {
		t.Fatal(err)
	}
	var got reviewVerdictResult
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ModelRequests != 1 || got.ModelResponses != 1 || got.Verdict.Status != "approve" {
		t.Fatalf("вердикт %s, ошибка %v", out, err)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"exec\n--harness\ncodex\n--\nenv\nRUST_LOG=error,codex_otel.trace_safe=info\ncodex\nexec\n",
		"--ignore-user-config\n", "--ephemeral\n", "--json\n",
		"--sandbox\nread-only\n", "--disable\nshell_tool\n",
		"--disable\nmulti_agent\n", "--disable\napps\n", "--disable\nhooks\n",
		"--disable\ncode_mode_host\n",
		"web_search=\"disabled\"\n", "--model=gpt-6.2-sol\n",
		"request_max_retries=0,stream_max_retries=0,supports_websockets=false",
		"model_reasoning_effort=\"high\"\n-\n",
	} {
		if !strings.Contains(string(args), expected) {
			t.Errorf("в аргументах запуска нет %q", expected)
		}
	}
	schema, err := os.ReadFile(filepath.Join(dir, "schema"))
	if err != nil || !json.Valid(schema) || !strings.Contains(string(schema), `"additionalProperties": false`) {
		t.Errorf("схема вердикта %q, ошибка %v", schema, err)
	}
	prompt, err := os.ReadFile(filepath.Join(dir, "prompt"))
	if err != nil || string(prompt) != reviewVerdictPrompt+packet {
		t.Errorf("ввод модели %q, ошибка %v", prompt, err)
	}
	t.Setenv("REVIEW_VERDICT_TEST_EXTRA_TRACE", strings.Replace(reviewVerdictTestTrace, "status_code=200", "status_code=500", 1))
	out, err = runReviewVerdict(context.Background(), []byte(packet), "gpt-6.2-sol", "high")
	if out != "" || err == nil || !strings.Contains(err.Error(), "зафиксировано 2") {
		t.Fatalf("вердикт при двух запросах %q, ошибка %v", out, err)
	}
	t.Setenv("REVIEW_VERDICT_TEST_EXTRA_TRACE", `codex codex_otel.trace_safe: event.name="codex.tool_call_received" tool_name="exec"`)
	out, err = runReviewVerdict(context.Background(), []byte(packet), "gpt-6.2-sol", "high")
	if out != "" || err == nil || !strings.Contains(err.Error(), "модель запросила инструмент") {
		t.Fatalf("вердикт после попытки инструмента %q, ошибка %v", out, err)
	}
}

func TestCountReviewVerdictRequests(t *testing.T) {
	modelList := strings.Replace(reviewVerdictTestTrace, `endpoint="/responses"`, `endpoint="/models"`, 1)
	failedRequest := strings.Replace(reviewVerdictTestTrace, "status_code=200", "status_code=500", 1)
	ws := `2026-11-23T22:22:22.471384Z  INFO codex_otel.trace_safe: event.name="codex.websocket_request" duration_ms=1 success="true"`
	if count := countReviewVerdictRequests(strings.Join([]string{modelList, failedRequest, reviewVerdictTestTrace, ws}, "\n")); count != 3 {
		t.Fatalf("запросов %d, ждали 3", count)
	}
}

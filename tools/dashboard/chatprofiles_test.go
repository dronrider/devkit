package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Проверяет реальную обвязку exec: служебный preflight видит новый профиль,
// а рабочее дерево задачи содержит только старый каталог kit/harness.
func TestChatLaunchUsesPreflightProfiles(t *testing.T) {
	e := newTestEnv(t)
	useHeadHome(t, e)
	home := e.home
	kit := filepath.Join(home, "projects", "devkit")
	stale, err := filepath.EvalSymlinks(e.proj)
	if err != nil {
		t.Fatal(err)
	}
	putDoc(t, stale, "docs/TASKS.md", "# synthetic board\n")
	putDoc(t, stale, "kit/harness/old.toml", "[detect]\n")
	putDoc(t, kit, "kit/harness/current.toml", `[detect]
[rules]
mode = "embed"
[delegate]
mode = "none"
[hooks]
protocol = "codex"
[quota]
`)
	cli := filepath.Join(e.bin, "profile-client")
	writeScript(t, e.bin, "profile-client", `printf '%s\n' "$PWD" "$DEVKIT_HOME" "$DEVKIT_HARNESS" "$MACHINE_MARK" "${CLAUDE_CODE_SESSION_ID-unset}" "${CLAUDE_CONFIG_DIR-unset}" "$@"`)
	putDoc(t, home, ".devkit/harness.local", "enabled = [\"current\"]\ndefault = \"current\"\n[current]\nbin = "+strconv.Quote(cli)+"\nenv = [\"MACHINE_MARK=selected\"]\n")
	putDoc(t, stale, ".devkit/harness.local", "enabled = []\n")
	agent := filepath.Join(t.TempDir(), "agentctl")
	build := exec.Command("go", "build", "-o", agent, ".")
	build.Dir = "../agentctl"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agentctl: %v\n%s", err, out)
	}
	old := agentctlBin
	agentctlBin = agent
	t.Cleanup(func() { agentctlBin = old })
	t.Setenv("HOME", home)
	t.Setenv("DEVKIT_DASH_HOME", home)
	t.Setenv("DEVKIT_HOME", "")
	// Сначала воспроизвести причину инцидента настоящим exec без launchEnv.
	raw := exec.Command(agent, "exec", "--harness", "current", "--", cli)
	raw.Dir = stale
	raw.Env = standSpawnEnv("HOME="+home, "DEVKIT_HOME=", "DEVKIT_TASK=")
	if out, err := raw.CombinedOutput(); err == nil || !strings.Contains(string(out), "current.toml") {
		t.Fatalf("stale profile failure absent: %v\n%s", err, out)
	}
	view := e.s.harnesses()
	if len(view.Harnesses) != 1 || view.Harnesses[0].Name != "current" {
		t.Fatalf("preflight: %+v", view)
	}
	h := &view.Harnesses[0]
	command := chatCmd(e.s.launchEnv("", "chat-stand", ""), "chosen-model", "", "first\nmessage", "", h, agent)
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = stale
	// Окно tmux может хранить чужой каталог кита и переменные старой сессии.
	cmd.Env = standSpawnEnv("HOME="+home, "DEVKIT_HOME="+stale, "DEVKIT_TASK=", "CLAUDE_CODE_SESSION_ID=old-session", "CLAUDE_CONFIG_DIR=old-subscription")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch after successful preflight: %v\n%s", err, out)
	}
	for _, want := range []string{stale + "\n" + kit + "\ncurrent\nselected\nunset\nunset\n", "chosen-model\n", "first\nmessage\n", "current не в списке включённых"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q in client output:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(stale, "kit/harness/current.toml")); !os.IsNotExist(err) {
		t.Fatalf("stale tree changed: %v", err)
	}
	// Источник живёт в том же кэше, что и модель подписки: смена каталога
	// службы применяется после штатного TTL, без отдельной памяти запуска.
	other := filepath.Join(home, "other kit")
	profile, err := os.ReadFile(filepath.Join(kit, "kit/harness/current.toml"))
	if err != nil {
		t.Fatal(err)
	}
	putDoc(t, other, "kit/harness/current.toml", string(profile))
	t.Setenv("DEVKIT_HOME", other)
	if env := e.s.launchEnv("", "chat-cached", ""); !strings.Contains(env, "DEVKIT_HOME="+shQuote(kit)) {
		t.Fatalf("cached source changed: %s", env)
	}
	now := e.s.now()
	e.s.now = func() time.Time { return now.Add(harnessTTL + time.Second) }
	command = chatCmd(e.s.launchEnv("", "chat-refreshed", ""), "chosen-model", "", "next", "", h, agent)
	cmd = exec.Command("/bin/sh", "-c", command)
	cmd.Dir = stale
	cmd.Env = standSpawnEnv("HOME="+home, "DEVKIT_HOME="+stale, "DEVKIT_TASK=")
	out, err = cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), stale+"\n"+other+"\ncurrent\nselected\nunset\nunset\n") {
		t.Fatalf("refreshed source: %v\n%s", err, out)
	}
	// Относительный DEVKIT_HOME должен стать абсолютным ещё у владельца
	// раскладки, до смены рабочего каталога команды.
	relative, err := filepath.Rel(stale, kit)
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.Command(agent, "harness", "--json")
	probe.Dir = stale
	probe.Env = standSpawnEnv("HOME="+home, "DEVKIT_HOME="+relative)
	data, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("relative source: %v\n%s", err, data)
	}
	var layout struct {
		ProfileDir string `json:"profile_dir"`
	}
	if err := json.Unmarshal(data, &layout); err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(filepath.Join(kit, "kit/harness"))
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(layout.ProfileDir)
	if err != nil || !filepath.IsAbs(layout.ProfileDir) || gotDir != wantDir {
		t.Fatalf("absolute source: %q, %v", layout.ProfileDir, err)
	}

}

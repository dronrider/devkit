package taskhead

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// stubAgentctl кладёт в PATH стаб agentctl, который на `harness --json` печатает
// body. Пустой body значит отказ утилиты. Стаб нужен стенду имени профиля:
// настоящий agentctl читает живой машинный слой, а тесту нужен свой default.
func stubAgentctl(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if body == "" {
		script += "exit 1\n"
	} else {
		script += "cat <<'EOF'\n" + body + "\nEOF\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "agentctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Имя профиля при пустых имени и переменной берёт default машинного слоя у
// `agentctl harness --json` (DK-1364): тик goal-watch поднимает голову без
// --harness, и хардкод claude-code уводил её на чужую подписку.
func TestHarnessNameTakesMachineDefault(t *testing.T) {
	t.Setenv(HarnessEnv, "")
	stubAgentctl(t, `{"default":"mimo","source":"/home/u/.devkit/harness.local","harnesses":[]}`)
	if got := HarnessName(""); got != "mimo" {
		t.Errorf("без имени и окружения %q, ждал default машинного слоя", got)
	}
	// Названное имя и переменная идут впереди машинного default.
	if got := HarnessName("другой"); got != "другой" {
		t.Errorf("названное имя %q, ждал его, а не машинный default", got)
	}
	t.Setenv(HarnessEnv, "из-окружения")
	if got := HarnessName(""); got != "из-окружения" {
		t.Errorf("переменная %q, ждал её, а не машинный default", got)
	}
}

// Без agentctl в PATH и при пустом default остаётся прежний claude-code.
func TestHarnessNameFallsBackWithoutAgentctl(t *testing.T) {
	t.Setenv(HarnessEnv, "")
	t.Setenv("PATH", t.TempDir())
	if got := HarnessName(""); got != DefaultHarness {
		t.Errorf("без agentctl %q, ждал %q", got, DefaultHarness)
	}
	stubAgentctl(t, `{"default":"","source":"x","harnesses":[]}`)
	if got := HarnessName(""); got != DefaultHarness {
		t.Errorf("с пустым default %q, ждал %q", got, DefaultHarness)
	}
	stubAgentctl(t, "не json")
	if got := HarnessName(""); got != DefaultHarness {
		t.Errorf("с неразборным ответом %q, ждал %q", got, DefaultHarness)
	}
}

// Настоящие профили обоих харнесов называют клиента и резюм: прибитой строки
// клиента в коде больше нет, и профиль без них оставил бы подъём без головы.
func TestRealProfilesNameTheHead(t *testing.T) {
	devkit := filepath.Join("..", "..")
	for _, name := range []string{"claude-code", "glm-code"} {
		h, err := ReadHead(ProfilePath(devkit, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.Bin != "claude" || len(h.Client) == 0 || h.TurnEnd != TurnMark {
			t.Fatalf("%s: голова %+v", name, h)
		}
		if got := strings.Join(h.ResumeCommand("S1", "opus"), " "); !strings.Contains(got, "--resume S1") ||
			!strings.Contains(got, "--model opus") || strings.Contains(got, "{model}") {
			t.Fatalf("%s: резюм %q", name, got)
		}
		cmd := strings.Join(h.Command("opus", "S2", "DK-1 работа"), " ")
		if !strings.Contains(cmd, "--permission-mode auto") || !strings.Contains(cmd, "--model opus") ||
			!strings.Contains(cmd, "--session-id S2") || !strings.Contains(cmd, "--name DK-1 работа") {
			t.Fatalf("%s: клиент %q", name, cmd)
		}
	}
	h, _ := ReadHead(ProfilePath(devkit, "glm-code"))
	if h.Client[0] != "agentctl" {
		t.Fatalf("вторая подписка поднимается мимо agentctl exec: %v", h.Client)
	}
}

func TestReadHeadParsesStringsAndLists(t *testing.T) {
	p := writeProfile(t, `# шапка
[delegate]
command = ["x"]

[head]
# клиент
client = ["cl", "--flag", "a \"b\" \\c"]
bin = "cl-bin"
turn_end = "exit"
unknown_key = 5

[quota]
client = ["чужое"]
`)
	h, err := ReadHead(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.Client, []string{"cl", "--flag", `a "b" \c`}) || h.Bin != "cl-bin" || h.TurnEnd != TurnExit {
		t.Fatalf("разобрано %+v", h)
	}
	if got := h.Command("m", "s", "n"); !reflect.DeepEqual(got, h.Client) {
		t.Fatalf("без ключей model, session и name клиент оброс флагами: %v", got)
	}
}

func TestReadHeadRefusals(t *testing.T) {
	cases := map[string]string{
		"[detect]\n":                                      "нет секции [head]",
		"[head]\nbin = \"x\"\n":                           "нет ключа client",
		"[head]\nclient = \"x\"\n":                        "жду массив строк",
		"[head]\nclient = [\"x\" \"y\"]\n":                "нет запятой",
		"[head]\nclient = [\"x\"]\nturn_end = \"hook\"\n": "turn_end",
		"[head]\nclient = [\"x]\n":                        "без закрывающей кавычки",
	}
	for body, want := range cases {
		_, err := ReadHead(writeProfile(t, body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("профиль %q: жду отказ со словами %q, а пришло %v", body, want, err)
		}
	}
	if _, err := ReadHead(filepath.Join(t.TempDir(), "нет.toml")); err == nil || !strings.Contains(err.Error(), "нет профиля") {
		t.Fatalf("пропавший профиль: %v", err)
	}
}

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
		if got := strings.Join(h.ResumeCommand("S1"), " "); !strings.Contains(got, "--resume S1") {
			t.Fatalf("%s: резюм %q", name, got)
		}
		cmd := strings.Join(h.Command("opus", "S2"), " ")
		if !strings.Contains(cmd, "--permission-mode auto") || !strings.Contains(cmd, "--model opus") ||
			!strings.Contains(cmd, "--session-id S2") {
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
	if got := h.Command("m", "s"); !reflect.DeepEqual(got, h.Client) {
		t.Fatalf("без ключей model и session клиент оброс флагами: %v", got)
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

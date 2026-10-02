package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Барьер подъёма без хуков (DK-1286): сессия включённой подписки, в чьих
// настройках нет обвязки devkit, не поднимается молча.

const hookedProfile = `[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "exit 0"]

[hooks]
protocol = "claude-code"
config = "{home}/settings.json"

[quota]
`

// hooksKit это машина с домашним харнесом и второй подпиской, чей профиль
// называет файл настроек под {home}. Каталог подписки заведён, настроек в нём
// ещё нет.
func hooksKit(t *testing.T) (kit, second string) {
	t.Helper()
	kit = fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeProfile(t, kit, "secondcli", hookedProfile)
	writeMachine(t, kit, execMachine)
	second = filepath.Join(kit, ".claude-second")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	return kit, second
}

func writeSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExecRefusesWithoutHooks: настроек нет вовсе либо в них нет хука журнала,
// и exec отказывает находкой с командой лечения, не запустив команду.
func TestExecRefusesWithoutHooks(t *testing.T) {
	cases := map[string]string{
		"файла нет":    "",
		"хуков нет":    `{"env": {"X": "1"}}`,
		"чужие хуки":   `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo"}]}]}}`,
		"пустой хуков": `{"hooks": {}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			kit, second := hooksKit(t)
			if body != "" {
				writeSettings(t, second, body)
			}
			marker := filepath.Join(t.TempDir(), "ran")
			var out, errw bytes.Buffer
			_, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", `echo > "$1"`, "sh", marker}, &out, &errw)
			if err == nil {
				t.Fatal("жду отказ: у подписки нет хуков devkit")
			}
			for _, want := range []string{"обвязки нет: devkitctl doctor --fix", "secondcli", filepath.Join(second, "settings.json")} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("в отказе нет %q: %v", want, err)
				}
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Fatal("команда запустилась, хотя exec отказал")
			}
		})
	}
}

// TestExecPassesWithHooks: хук журнала на месте, и подъём идёт как обычно.
func TestExecPassesWithHooks(t *testing.T) {
	kit, second := hooksKit(t)
	writeSettings(t, second, `{"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "python3 /x/hooks/session-task.py --hook claude-code"}]}]}}`)
	if code, out := execOut(t, kit, "secondcli", []string{"/bin/sh", "-c", "exit 0"}); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
}

// TestExecNoHooksGapForNotEnabled: невключённому харнесу devkit хуков не
// кладёт, и их отсутствие там не находка.
func TestExecNoHooksGapForNotEnabled(t *testing.T) {
	kit, _ := hooksKit(t)
	writeMachine(t, kit, strings.Replace(execMachine, `enabled = ["homecli", "secondcli"]`, `enabled = ["homecli"]`, 1))
	if code, out := execOut(t, kit, "secondcli", []string{"/bin/sh", "-c", "exit 0"}); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
}

// TestExecNoHooksGapWithoutConfigKey: профиль не называет файл настроек, и
// судить не о чем. Таков профиль всех прежних тестовых харнесов.
func TestExecNoHooksGapWithoutConfigKey(t *testing.T) {
	kit := execKit(t)
	if code, out := execOut(t, kit, "secondcli", []string{"/bin/sh", "-c", "exit 0"}); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
}

// TestHarnessJSONHooksGap: машинный вид несёт находку только у харнеса без
// хуков, и дашборд читает её отсюда.
func TestHarnessJSONHooksGap(t *testing.T) {
	kit, second := hooksKit(t)
	read := func() map[string]string {
		raw, err := cmdHarnessJSON(kit)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Harnesses []struct {
				Name     string `json:"name"`
				HooksGap string `json:"hooks_gap"`
			} `json:"harnesses"`
		}
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, h := range v.Harnesses {
			got[h.Name] = h.HooksGap
		}
		return got
	}
	got := read()
	if !strings.Contains(got["secondcli"], "devkitctl doctor --fix") {
		t.Fatalf("у подписки без хуков нет находки: %q", got["secondcli"])
	}
	if got["homecli"] != "" {
		t.Fatalf("у харнеса без ключа config находка %q", got["homecli"])
	}
	writeSettings(t, second, `{"hooks": {"SessionStart": [{"hooks": [{"command": "python3 hooks/session-task.py --hook claude-code"}]}]}}`)
	if got := read(); got["secondcli"] != "" {
		t.Fatalf("хуки легли, а находка осталась: %q", got["secondcli"])
	}
}

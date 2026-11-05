package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeadChatLiftCarriesManualEffort(t *testing.T) {
	const sid = "aaaa1111-2222-4222-8222-333333333333"
	for _, route := range []string{"say", "continue"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max", ""} {
			t.Run(route+"/"+effort, func(t *testing.T) {
				e, c, _ := tasksEnv(t)
				log := filepath.Join(e.home, "tmux.log")
				writeTmuxFake(t, e.bin, log, "")
				writeClientFakes(t, e.bin, "клиент-1", "клиент-2")
				writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
				writeTaskRunFake(t, filepath.Dir(e.proj))
				t.Setenv("DEVKIT_HOME", filepath.Join(filepath.Dir(e.proj), "devkit"))
				useHeadHome(t, e)
				endedChat(t, e, sid, "XR-004", "chat-7")
				storeModel(t, e, sid, "модель-pro")
				endpoint := "/api/projects/demo/tasks/XR-004/continue"
				payload := `{"effort":"` + effort + `"}`
				if route == "say" {
					endpoint = "/api/projects/demo/chats/" + sid + "/say"
					payload = `{"text":"продолжай проверку","effort":"` + effort + `"}`
				}
				resp := doReq(t, c, "POST", e.srv.URL+endpoint, payload)
				text := body(t, resp)
				if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"way":"resume"`) {
					t.Fatalf("продолжение не поднято резюмом: %d %s", resp.StatusCode, text)
				}
				command := readFile(t, log)
				if !strings.Contains(command, "new-session -d -s chat-7 ") || !strings.Contains(command, "--resume '"+sid+"'") {
					t.Fatalf("не поднят прежний разговор: %s", command)
				}
				if effort != "" && !strings.Contains(command, "DEVKIT_EFFORT='"+effort+"'") {
					t.Fatalf("ручная глубина потеряна при подъёме: %s", command)
				}
				if effort == "" && strings.Contains(command, "DEVKIT_EFFORT='") {
					t.Fatalf("без выбора появился ручной уровень: %s", command)
				}
			})
		}
	}
}

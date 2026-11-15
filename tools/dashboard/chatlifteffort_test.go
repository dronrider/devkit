package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeadChatLiftCarriesManualEffort(t *testing.T) {
	const sid = "aaaa1111-2222-4222-8222-333333333333"
	// Доска одна на все двенадцать случаев: семь процессов taskctl, строивших
	// её на каждый случай, весили больше самой проверки. Фикстуры PATH и дом
	// головы носителя ставит родитель, а случаю достаются своя строка, своё
	// окно разговора и свой журнал tmux, и перезапись фикстуры безопасна:
	// случаи идут по очереди. Окно тоже своё: подъём пишет на имя порог
	// присмотра, и на общем имени следующий случай прочитал бы его чужим.
	e, c, _ := tasksEnv(t)
	writeClientFakes(t, e.bin, "клиент-1", "клиент-2")
	writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
	writeTaskRunFake(t, filepath.Dir(e.proj))
	t.Setenv("DEVKIT_HOME", filepath.Join(filepath.Dir(e.proj), "devkit"))
	useHeadHome(t, e)
	row := 0
	for _, route := range []string{"say", "continue"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max", ""} {
			row++
			task := fmt.Sprintf("XR-2%02d", row)
			tmux := fmt.Sprintf("chat-2%02d", row)
			log := filepath.Join(e.home, fmt.Sprintf("tmux-%02d.log", row))
			t.Run(route+"/"+effort, func(t *testing.T) {
				runTaskctl(t, e.proj, "add", "--id", task, "--title", "Ручная глубина подъёма",
					"--rank", "0+1+0+0+0", "--accept", "agent", "--body-file", taskBodyFixture(t))
				writeTmuxFake(t, e.bin, log, "")
				endedChat(t, e, sid, task, tmux)
				storeModel(t, e, sid, "модель-pro")
				endpoint := "/api/projects/demo/tasks/" + task + "/continue"
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
				if !strings.Contains(command, "new-session -d -s "+tmux+" ") || !strings.Contains(command, "--resume '"+sid+"'") {
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

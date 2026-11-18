package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatModelPreferenceReplacementMetadata(t *testing.T) {
	e, c := appEnv(t)
	catalog := strings.Replace(appHarnessJSON, `"protocol": "codex", "models": [{"tier": "pro", "model": "модель-codex"}]`,
		`"protocol": "codex", "chat": ["gpt-6.2-astra"], "models": [{"tier": "pro", "model": "gpt-6.1-sol"}]`, 1)
	writeScript(t, e.bin, "agentctl", `printf '%s' '`+catalog+`'`)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
	var got struct {
		Models []struct {
			Model    string   `json:"model"`
			Replaces []string `json:"replaces"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"gpt-6.2-sol":   "gpt-6-sol,gpt-6.1-sol",
		"gpt-6.2-astra": "gpt-6-astra,gpt-6.1-astra",
	}
	for _, option := range got.Models {
		if previous, ok := want[option.Model]; ok {
			if strings.Join(option.Replaces, ",") != previous {
				t.Fatalf("список не передал прежние ID для предпочтения %s: %+v", option.Model, got.Models)
			}
			delete(want, option.Model)
		}
	}
	if len(want) != 0 {
		t.Fatalf("в ответе нет актуальных моделей: %+v", want)
	}
}

func TestChatLegacyModelDisplayBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("браузер стенда не найден; задать DASHBOARD_CHROME")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if !needPlaywright(t, node) {
		t.Fatal("playwright недоступен; задать NODE_PATH")
	}
	e, _ := appEnv(t)
	probe := exec.Command(node, "testdata/chat_models_browser.mjs", e.srv.URL, chrome, e.cfg.Token)
	probe.Env = os.Environ()
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("прежняя подпись и новый выбор в браузере: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}

func TestChatLegacyModelsCannotBeSelected(t *testing.T) {
	e, c := appEnv(t)
	catalog := strings.Replace(appHarnessJSON, `"protocol": "codex", "models":`,
		`"chat": ["gpt-6.1-sol", "gpt-6-astra", "gpt-6.2-sol", "gpt-6.2-astra"], "protocol": "codex", "models":`, 1)
	writeScript(t, e.bin, "agentctl", `printf '%s' '`+catalog+`'`)
	sid := "aa173600-1111-4111-8111-111111111111"
	if err := e.s.chatStoreWrite(sid, chatStore{Project: "demo", Model: "gpt-6.2-sol", Harness: "codextest", Protocol: "codex"}); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"gpt-6-sol", "gpt-6.1-sol", "gpt-6-astra", "gpt-6.1-astra"} {
		for _, harness := range []string{"codextest", ""} {
			for _, path := range []string{"", "/blank", "/" + sid + "/model"} {
				t.Run(old+"/"+harness+path, func(t *testing.T) {
					resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats"+path,
						fmt.Sprintf(`{"model":%q,"harness":%q,"text":"не запускать"}`, old, harness))
					got := body(t, resp)
					if resp.StatusCode != http.StatusConflict {
						t.Fatalf("прежний ID принят новым выбором: %d %s", resp.StatusCode, got)
					}
					if st := e.s.chatStoreRead(sid); st.Model != "gpt-6.2-sol" {
						t.Fatalf("отвергнутый выбор изменил запись: %+v", st)
					}
				})
			}
		}
	}
}

func TestChatModelCatalogProjectsLegacyAssignments(t *testing.T) {
	e, _ := appEnv(t)
	const catalog = `{"harnesses":[{"name":"codextest","enabled":true,"default":true,"bin":"клиент-app","protocol":"codex",
"models":[{"tier":"mini","model":"gpt-6-luna"},{"tier":"base","model":"gpt-6.1-terra"},
{"tier":"pro","model":"gpt-6.1-sol","levels":["low","high"]},{"tier":"max","model":"gpt-6-astra"}],
"chat":["gpt-6-sol","gpt-6.1-astra","gpt-6.2-sol","gpt-6.2-astra"]}]}`
	writeScript(t, e.bin, "agentctl", `printf '%s' '`+catalog+`'`)
	opts := e.s.chatModelOpts()
	if len(opts) != 4 {
		t.Fatalf("после замены нужны четыре модели без повторов: %+v", opts)
	}
	want := []string{"gpt-6-luna", "gpt-6.1-terra", "gpt-6.2-sol", "gpt-6.2-astra"}
	for i, opt := range opts {
		if opt.Model != want[i] || opt.Harness != "codextest" || opt.Default != (i == 2) {
			t.Fatalf("проекция потеряла модель, подписку или умолчание: %+v", opts)
		}
	}
	if opts[2].Tier != "pro" || strings.Join(opts[2].Levels, ",") != "low,high" || opts[3].Tier != "max" {
		t.Fatalf("проекция потеряла ярусы или глубину: %+v", opts)
	}
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		if !e.s.chatPairKnown(model, "codextest") || e.s.chatHarnessOf(model, "codextest").Name != "codextest" {
			t.Fatalf("модель нового выбора не сопоставилась прежнему назначению: %s", model)
		}
	}
	raw := e.s.harnesses().Harnesses[0].Models
	if raw[2].Model != "gpt-6.1-sol" || raw[3].Model != "gpt-6-astra" {
		t.Fatalf("проекция чата переписала назначения фоновых агентов: %+v", raw)
	}
}

func TestChatLegacyModelPreservedUntilExplicitChange(t *testing.T) {
	for _, old := range []string{"gpt-6.1-sol", "gpt-6-astra"} {
		t.Run(old, func(t *testing.T) {
			e, c := appEnv(t)
			catalog := strings.Replace(appHarnessJSON, `"protocol": "codex", "models":`,
				`"chat": ["gpt-6.2-sol", "gpt-6.2-astra"], "protocol": "codex", "models":`, 1)
			writeScript(t, e.bin, "agentctl", `case "$1" in
  exec) shift 4; exec "$@" ;;
  *) printf '%s' '`+catalog+`' ;;
esac`)
			sid := appStartChat(t, e, c, "сохранённый разговор")
			appWaitFeed(t, e, sid, "ответ на реплику")
			appWaitBusy(t, e, c, sid, false)
			st := e.s.chatStoreRead(sid)
			st.Model = old
			if err := e.s.chatStoreWrite(sid, st); err != nil {
				t.Fatal(err)
			}
			before := readFile(t, blankPath(e.home, sid))
			row := blankRow(blankList(t, e, c, "?all=1"), sid)
			if row == nil || row.Model != old {
				t.Fatalf("подпись прежней модели потеряна: %+v", row)
			}
			if after := readFile(t, blankPath(e.home, sid)); after != before {
				t.Fatal("чтение списка переписало исторический ID")
			}
			logPath := filepath.Join(e.proj, "fake-app-log")
			logBefore := readFile(t, logPath)
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"новый ход старой моделью"}`)
			got := body(t, resp)
			if resp.StatusCode != http.StatusConflict || !strings.Contains(got, "перед продолжением выберите") {
				t.Fatalf("продолжение без актуального выбора: %d %s", resp.StatusCode, got)
			}
			if readFile(t, logPath) != logBefore || readFile(t, blankPath(e.home, sid)) != before {
				t.Fatal("отклонённое продолжение изменило запись или отправило ход клиенту")
			}
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model", `{"model":"gpt-6.2-sol","harness":"codextest"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("смена на актуальную модель: %d %s", resp.StatusCode, body(t, resp))
			}
			resp.Body.Close()
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{"drop":true}`)
			body(t, resp)
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжение после смены"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("продолжение после смены: %d %s", resp.StatusCode, body(t, resp))
			}
			resp.Body.Close()
			appWaitFeed(t, e, sid, "продолжение после смены")
			if log := readFile(t, logPath); !strings.Contains(log, "gpt-6.2-sol") {
				t.Fatalf("точный актуальный ID не дошёл клиенту: %s", log)
			}
		})
	}
}

func TestChatModelCatalogPreservesOperationalTask(t *testing.T) {
	e, c := appTaskEnv(t, "task-done")
	catalog := strings.ReplaceAll(appHarnessJSON, "модель-codex", "gpt-6.1-sol")
	writeScript(t, e.bin, "agentctl", `case "$1" in
  exec) shift 4; exec "$@" ;;
  *) printf '%s' '`+catalog+`' ;;
esac`)
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK || got["model"] != "gpt-6.1-sol" {
		t.Fatalf("каталог чата изменил запуск фоновой задачи: %d %+v", code, got)
	}
	waitTaskStopped(t, e, got["session"])
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); !strings.Contains(log, "start gpt-6.1-sol\n") {
		t.Fatalf("фоновая задача не получила прежний operational ID: %s", log)
	}
}

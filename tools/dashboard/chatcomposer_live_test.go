//go:build dashboard_live_acceptance

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Этот прогон требует явного включения и авторизованного клиента машины.
func TestChatComposerRealCodex(t *testing.T) {
	if os.Getenv("DASHBOARD_REAL_CODEX") != "1" {
		t.Skip("живая приёмка: DASHBOARD_REAL_CODEX=1")
	}
	agent, err := exec.LookPath("agentctl")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEnv(t)
	t.Setenv("DEVKIT_HOME", checkout)
	writeScript(t, e.bin, "agentctl", "exec "+shQuote(agent)+" \"$@\"")
	proxy := filepath.Join(e.bin, "codex-proxy.py")
	capture := filepath.Join(e.home, "codex-wire.jsonl")
	code := `import json, subprocess, sys, threading
p=subprocess.Popen([` + quoteJSON(codex) + `]+sys.argv[1:],stdin=subprocess.PIPE,stdout=subprocess.PIPE)
def forward():
 for line in sys.stdin.buffer:
  try:
   x=json.loads(line)
   if x.get("method")=="turn/start":
    params=x.get("params",{})
    with open(` + quoteJSON(capture) + `,"a") as f:
     f.write(json.dumps({k:params.get(k) for k in ("threadId","model","effort")})+"\n")
  except Exception: pass
  p.stdin.write(line);p.stdin.flush()
threading.Thread(target=forward,daemon=True).start()
for line in p.stdout:
 try:
  x=json.loads(line)
  if "plan" in x.get("method","").lower() or x.get("method")=="turn/started" or x.get("method", "").startswith("item/"):
   with open(` + quoteJSON(capture) + `,"a") as f: f.write(json.dumps(x)+"\n")
 except Exception: pass
 sys.stdout.buffer.write(line);sys.stdout.buffer.flush()
`
	if err := os.WriteFile(proxy, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	writeScript(t, e.bin, "codex", "exec python3 "+shQuote(proxy)+" \"$@\"")
	client := e.loggedClient(t)
	t.Cleanup(func() {
		e.srv.Close()
		e.s.appMu.Lock()
		var ids []string
		for sid := range e.s.appChats {
			ids = append(ids, sid)
		}
		e.s.appMu.Unlock()
		for _, sid := range ids {
			e.s.appDrop(sid, "конец живой приёмки DK-1741")
		}
		e.s.appWorkers.Wait()
	})
	start := func(marker string) string {
		prompt := "Составь содержательный план и реши задачу. Нужно независимо проверить сумму квадратов целых чисел от 1 до 40 двумя способами, прямым подсчётом и формулой. Сначала обязательно вызови инструмент update_plan с двумя шагами: «Факты " + marker + "» и «Проверка " + marker + "». Покажи последовательный переход pending -> in_progress -> completed вызовами инструмента. Выполни вычисления через python3 в shell, без чтения и изменения файлов. После проверки оба шага отметь completed. Последним ответом напиши только " + marker + "."
		data, _ := json.Marshal(map[string]string{"text": prompt, "model": "gpt-6.2-sol", "harness": "codex", "effort": "low"})
		resp := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats", string(data))
		raw := body(t, resp)
		if resp.StatusCode != 200 {
			t.Fatalf("живой запуск: %d %s", resp.StatusCode, raw)
		}
		var got map[string]string
		json.Unmarshal([]byte(raw), &got)
		return got["session"]
	}
	wait := func(sid, marker string) {
		until := time.After(240 * time.Second)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			if live := e.s.appLive(sid); live != nil && !live.busyNow() {
				feed := e.s.appFeedRead(sid)
				found := false
				for _, it := range feed {
					if it.Role == "assistant" && strings.Contains(it.Text, marker) {
						found = true
					}
				}
				if found {
					return
				}
			}
			select {
			case <-until:
				t.Fatalf("живой ответ не завершён: %+v", e.s.appFeedRead(sid))
			case <-tick.C:
			}
		}
	}
	sid := start("LIVE_A")
	live := e.s.appLive(sid)
	if live == nil || !live.busyNow() {
		t.Fatal("нет живого первого хода")
	}
	resp := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model", `{"model":"gpt-6.2-astra","harness":"codex"}`)
	raw := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("живой выбор: %d %s", resp.StatusCode, raw)
	}
	if e.s.appLive(sid) != live || live.proc.gone() {
		t.Fatal("выбор снял живого клиента")
	}
	wait(sid, "LIVE_A")
	plan := e.s.appPlanRead(sid)
	if len(plan) != 2 || plan[0].Text != "Факты LIVE_A" || plan[0].State != "completed" || plan[1].State != "completed" {
		captureData, _ := os.ReadFile(capture)
		t.Fatalf("живой план: %+v; feed=%+v; wire=%s", plan, e.s.appFeedRead(sid), captureData)
	}
	resp = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"Ответь только LIVE_NEXT. Не вызывай инструменты.","effort":"high"}`)
	raw = body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("живой следующий ход: %d %s", resp.StatusCode, raw)
	}
	wait(sid, "LIVE_NEXT")
	other := start("LIVE_B")
	wait(other, "LIVE_B")
	otherPlan := e.s.appPlanRead(other)
	if len(otherPlan) != 2 || otherPlan[0].Text != "Факты LIVE_B" {
		t.Fatalf("второй живой план: %+v", otherPlan)
	}
	if kept := e.s.appPlanRead(sid); len(kept) != 2 || kept[0].Text != "Факты LIVE_A" {
		t.Fatalf("планы двух чатов смешались: %+v", kept)
	}
	restarted := &server{cfg: e.cfg}
	if got := restarted.appPlanRead(sid); len(got) != 2 {
		t.Fatalf("план после рестарта: %+v", got)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var turns []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var v map[string]string
		if json.Unmarshal([]byte(line), &v) == nil && v["model"] != "" {
			turns = append(turns, v)
		}
	}
	if len(turns) != 3 || turns[0]["model"] != "gpt-6.2-sol" || turns[1]["model"] != "gpt-6.2-astra" || turns[1]["effort"] != "high" || turns[1]["threadId"] != turns[0]["threadId"] {
		t.Fatalf("живые turn/start: %+v", turns)
	}
	t.Logf("DK-1741 live Codex: 2 чата, 3 turn/start; следующий ход model=%s effort=%s, прежняя нить сохранена; оба плана 2/2, состояния пережили чтение новым сервером", turns[1]["model"], turns[1]["effort"])
}

func quoteJSON(s string) string { data, _ := json.Marshal(s); return string(data) }

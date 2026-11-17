package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskBlankFirstMessageRoute(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node нужен для проверки маршрута панели")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_taskblankroute.mjs"), filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("первая реплика нового чата: %v\n%s", err, out)
	}
}

func TestTaskBlankStartRetriesOwnSession(t *testing.T) {
	e, c := chatEnv(t)
	log := filepath.Join(e.home, "tmux-route.log")
	flag := log + ".ready"
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	writeClientFakes(t, e.bin, "клиент-1", "клиент-2")
	writeScript(t, e.bin, "tmux", `echo "$@" >> '`+log+`'
case "$1" in
ls) printf 'chat-XR-4-1\t1\t1754770421\n'; if [ -f '`+flag+`' ]; then printf 'chat-XR-4-2\t1\t1754770422\n'; fi;;
new-session) if [ ! -f '`+flag+`' ]; then echo refused; exit 1; fi;;
esac
exit 0`)
	blank := blankMake(t, e, c, `{"model":"вторая-pro","harness":"втораяtest","id":"XR-4"}`)
	text := "Нужно завершить задачу.\nМакеты согласованы. Продолжай"
	data, _ := json.Marshal(map[string]string{"text": text, "model": "вторая-pro", "harness": "втораяtest", "id": "XR-4", "chat": blank})
	draft, _ := json.Marshal(map[string]string{"text": text})
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+blank+"/draft", string(draft))
	send := func(want int) map[string]string {
		t.Helper()
		r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats", string(data))
		b := body(t, r)
		if r.StatusCode != want {
			t.Fatalf("подъём: %d, хочу %d: %s", r.StatusCode, want, b)
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(b), &raw); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for key, value := range raw {
			if word, ok := value.(string); ok {
				got[key] = word
			}
		}
		return got
	}
	send(http.StatusBadGateway)
	if st := blankStore(t, e.home, blank); st.Draft != text || st.Tmux != "" {
		t.Fatalf("ошибка потеряла черновик или подтвердила запуск: %+v", st)
	}
	// Имя второго окна появляется только после запуска, а повтор видит его живым.
	writeScript(t, e.bin, "tmux", `echo "$@" >> '`+log+`'
case "$1" in
ls) printf 'chat-XR-4-1\t1\t1754770421\n'; if [ -f '`+flag+`.started' ]; then printf 'chat-XR-4-2\t1\t1754770422\n'; fi;;
new-session) touch '`+flag+`.started';;
esac
exit 0`)
	got := send(http.StatusOK)
	if got["tmux"] != "chat-XR-4-2" || got["model"] != "вторая-pro" || got["harness"] != "втораяtest" {
		t.Fatalf("не тот запуск: %+v", got)
	}
	before := readFile(t, log)
	if !strings.Contains(before, text) || !strings.Contains(before, "клиент-2") || strings.Contains(before, "send-keys") {
		t.Fatalf("реплика ушла не выбранному новому клиенту: %s", before)
	}
	send(http.StatusOK)
	after := readFile(t, log)
	if strings.Count(after, "new-session") != strings.Count(before, "new-session") {
		t.Fatalf("повтор поднял второй клиент: %s", after)
	}
	for key, value := range map[string]string{"text": "другая реплика", "model": "вторая-base", "effort": "high", "id": "XR-7"} {
		request := map[string]string{"text": text, "model": "вторая-pro", "harness": "втораяtest", "id": "XR-4", "chat": blank}
		request[key] = value
		data, _ = json.Marshal(request)
		send(http.StatusConflict)
	}
	data, _ = json.Marshal(map[string]string{"text": text, "model": "вторая-pro", "harness": "втораяtest", "chat": blank})
	writeTmuxFake(t, e.bin, log, "")
	send(http.StatusBadGateway)
	if after := readFile(t, log); strings.Count(after, "new-session") != strings.Count(before, "new-session") {
		t.Fatalf("повтор после смерти клиента поднял дубль: %s", after)
	}
	st := blankStore(t, e.home, blank)
	st.Grown = "aaaa4444-4444-4444-8444-444444444444"
	if err := e.s.chatStoreWrite(blank, st); err != nil {
		t.Fatal(err)
	}
	if got := send(http.StatusOK); got["session"] != st.Grown {
		t.Fatalf("повтор не назвал выросший разговор: %+v", got)
	}
	if st := blankStore(t, e.home, blank); st.Tmux != "chat-XR-4-2" || st.Draft != "" {
		t.Fatalf("запуск не сохранился: %+v", st)
	}
}

func TestTaskBlankConcurrentDifferentMessages(t *testing.T) {
	e, c := chatEnv(t)
	log := filepath.Join(e.home, "concurrent-start.log")
	writeTmuxFake(t, e.bin, log, "")
	writeScript(t, e.bin, "claude", "exit 0")
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	ready := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, text := range []string{"первая реплика", "другая реплика"} {
		go func(text string) {
			<-ready
			data, _ := json.Marshal(map[string]string{"text": text, "model": "opus", "chat": blank})
			r := httptest.NewRequest("POST", "/api/projects/demo/chats", strings.NewReader(string(data)))
			r.SetPathValue("p", "demo")
			w := httptest.NewRecorder()
			e.s.handleChatStart(w, r)
			results <- w
		}(text)
	}
	close(ready)
	statuses := map[int]int{}
	for i := 0; i < 2; i++ {
		w := <-results
		statuses[w.Code]++
		if w.Code != http.StatusOK && w.Code != http.StatusConflict {
			t.Errorf("неожиданный ответ %d: %s", w.Code, w.Body.String())
		}
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("новая реплика потерялась вместо явного отказа: %+v", statuses)
	}
	if got := strings.Count(readFile(t, log), "new-session"); got != 1 {
		t.Fatalf("поднято клиентов: %d, хочу 1", got)
	}
}

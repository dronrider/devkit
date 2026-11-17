package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func blankStartRequest(s *server, blank, text string) *httptest.ResponseRecorder {
	data, _ := json.Marshal(map[string]string{"chat": blank, "text": text, "model": "opus"})
	r := httptest.NewRequest("POST", "/api/projects/demo/chats", strings.NewReader(string(data)))
	r.SetPathValue("p", "demo")
	w := httptest.NewRecorder()
	s.handleChatStart(w, r)
	return w
}

func TestTaskBlankReceiptSurvivesFailedBlankWriteAndRestart(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	log := filepath.Join(e.home, "start.log")
	lock := filepath.Join(chatStoreDir(e.home), blank+".lock")
	writeScript(t, e.bin, "claude", "exit 0")
	writeScript(t, e.bin, "tmux", `echo "$@" >> '`+log+`'
case "$1" in
new-session) rm -f '`+lock+`'; mkdir '`+lock+`';;
ls) `+tmuxNoServer+`;;
esac
exit 0`)
	if w := blankStartRequest(e.s, blank, "первый текст"); w.Code != http.StatusOK {
		t.Fatalf("первый запуск %d: %s", w.Code, w.Body.String())
	}
	if st := blankStore(t, e.home, blank); st.Tmux != "" {
		t.Fatal("фикстура не сорвала запись заготовки")
	}
	fresh := &server{cfg: e.s.cfg, now: e.s.now}
	if w := blankStartRequest(fresh, blank, "первый текст"); w.Code != http.StatusBadGateway {
		t.Fatalf("повтор после рестарта %d: %s", w.Code, w.Body.String())
	}
	if n := strings.Count(readFile(t, log), "new-session"); n != 1 {
		t.Fatalf("запусков после потери blank write: %d", n)
	}
}

func TestTaskBlankReceiptSurvivesSweepAndMixedAPI(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	log := filepath.Join(e.home, "start.log")
	writeTmuxFake(t, e.bin, log, "")
	writeScript(t, e.bin, "claude", "exit 0")
	if w := blankStartRequest(e.s, blank, "первый текст"); w.Code != http.StatusOK {
		t.Fatalf("start %d: %s", w.Code, w.Body.String())
	}
	e.s.chatBlankList("demo")
	if _, err := os.Stat(blankPath(e.home, blank)); !os.IsNotExist(err) {
		t.Fatalf("фикстура не убрала умершую заготовку: %v", err)
	}
	fresh := &server{cfg: e.s.cfg, now: e.s.now}
	if w := blankStartRequest(fresh, blank, "первый текст"); w.Code != http.StatusBadGateway {
		t.Fatalf("POST после уборки %d: %s", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	fresh.chatRaiseSay(w, &Project{Name: "demo", Path: e.proj}, blank, "первый текст", "receipt-retry", "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("say после POST %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	fresh.chatRaiseSay(w, &Project{Name: "demo", Path: e.proj}, blank, "другая реплика", "receipt-new", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("другая say потерялась: %d %s", w.Code, w.Body.String())
	}
	if n := strings.Count(readFile(t, log), "new-session"); n != 1 {
		t.Fatalf("запусков через смешанные входы: %d", n)
	}
}

func TestTaskBlankUnknownStartRetainsReceipt(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	writeScript(t, e.bin, "claude", "exit 0")
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	old := startReservedFn
	calls := 0
	startReservedFn = func(root, tree, id, sess string, command func(string) string) error {
		calls++
		return errors.Join(errChatStartUnknown, errors.New("lost ack"))
	}
	defer func() { startReservedFn = old }()
	for i := 0; i < 2; i++ {
		fresh := &server{cfg: e.s.cfg, now: e.s.now}
		if w := blankStartRequest(fresh, blank, "первый текст"); w.Code != http.StatusBadGateway {
			t.Fatalf("unknown выдал доставку: %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("неизвестный исход запустили повторно: %d", calls)
	}
}

func TestTaskBlankReceiptFailurePreventsLaunch(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	log := filepath.Join(e.home, "start.log")
	writeTmuxFake(t, e.bin, log, "")
	if err := os.Mkdir(e.s.chatStartReceiptPath(blank), 0700); err != nil {
		t.Fatal(err)
	}
	if w := blankStartRequest(e.s, blank, "первый текст"); w.Code != http.StatusBadGateway {
		t.Fatalf("отказ квитанции выдал запуск: %d %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "new-session") {
		t.Fatal("клиент поднят без квитанции")
	}
}

func TestTaskBlankDirectSignalRetainsUnknownReceipt(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus"}`)
	log := filepath.Join(e.home, "signal.log")
	writeScript(t, e.bin, "claude", "exit 0")
	writeScript(t, e.bin, "tmux", `echo "$@" >> '`+log+`'
case "$1" in
new-session) kill -TERM $$;;
ls) `+tmuxNoServer+`;;
esac
exit 0`)
	for i := 0; i < 2; i++ {
		w := blankStartRequest(e.s, blank, "первый текст")
		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"unknown":true`) {
			t.Fatalf("неизвестный сигнал выдал доставку: %d %s", w.Code, w.Body.String())
		}
	}
	if n := strings.Count(readFile(t, log), "new-session"); n != 1 {
		t.Fatalf("после сигнала клиент повторён: %d", n)
	}
}

func TestTaskBlankForeignKeepsExistingStartRoute(t *testing.T) {
	e, c := appEnv(t)
	blank := blankMake(t, e, c, `{"model":"модель-codex","harness":"codextest"}`)
	for i := 0; i < 2; i++ {
		data, _ := json.Marshal(map[string]string{"chat": blank, "text": "первый текст", "model": "модель-codex", "harness": "codextest"})
		r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats", string(data))
		if r.StatusCode != http.StatusOK {
			t.Fatalf("app-start получил новый отказ: %d %s", r.StatusCode, body(t, r))
		}
		body(t, r)
	}
}

func TestTaskBlankMixedConcurrentAPIs(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	log := filepath.Join(e.home, "mixed.log")
	writeTmuxFake(t, e.bin, log, "")
	writeScript(t, e.bin, "claude", "exit 0")
	gate := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	go func() { <-gate; results <- blankStartRequest(e.s, blank, "первая реплика") }()
	go func() {
		<-gate
		r := httptest.NewRequest("POST", "/api/projects/demo/chats/"+blank+"/say", strings.NewReader(`{"text":"другая реплика","msg_id":"mixed"}`))
		r.SetPathValue("p", "demo")
		r.SetPathValue("sid", blank)
		w := httptest.NewRecorder()
		e.s.handleChatSay(w, r)
		results <- w
	}()
	close(gate)
	status := map[int]int{}
	for i := 0; i < 2; i++ {
		w := <-results
		status[w.Code]++
	}
	if status[http.StatusOK] != 1 || status[http.StatusConflict] != 1 {
		t.Fatalf("смешанные входы не разделили квитанцию: %+v", status)
	}
	if n := strings.Count(readFile(t, log), "new-session"); n != 1 {
		t.Fatalf("смешанные входы подняли %d клиентов", n)
	}
}

func TestTaskBlankReceiptDoesNotFollowReusedTmux(t *testing.T) {
	e, c := chatEnv(t)
	blank := blankMake(t, e, c, `{"model":"opus","id":"XR-4"}`)
	log := filepath.Join(e.home, "reuse.log")
	writeScript(t, e.bin, "claude", "exit 0")
	writeTmuxFake(t, e.bin, log, "")
	w := blankStartRequest(e.s, blank, "первый текст")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var reply map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	name := reply["tmux"]
	mem := e.s.chatStoreRead("tmux-" + name)
	mem.From = "blank-other"
	if err := e.s.chatStoreWrite("tmux-"+name, mem); err != nil {
		t.Fatal(err)
	}
	writeTmuxFake(t, e.bin, log, name+"\t1\t9999999999\n")
	if w := blankStartRequest(e.s, blank, "первый текст"); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"unknown":true`) {
		t.Fatalf("старый чат открыл чужое окно: %d %s", w.Code, w.Body.String())
	}
}

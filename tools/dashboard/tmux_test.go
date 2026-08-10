package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeTmuxLiveFake кладёт фикстуру tmux для экрана агента: ls отвечает
// строками формата с табами, capture-pane отдаёт снимок и пишет вызов в
// журнал, чужое имя сессии это ненулевой код, как у живого tmux.
func writeTmuxLiveFake(t *testing.T, bin, logPath string) {
	t.Helper()
	body := `echo "$@" >> "` + logPath + `"
case "$1" in
ls)
  printf 'goal-XR-9\t1\t1754770421\ntask-XR-5\t2\t1754770500\n';;
capture-pane)
  if [ "$4" != "=goal-XR-9:" ]; then
    echo "can't find session" >&2
    exit 1
  fi
  printf 'виток 3 поднят, ход витка ниже\n$ taskctl list\n';;
esac
exit 0`
	writeScript(t, bin, "tmux", body)
}

func tmuxEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e := newTestEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxLiveFake(t, e.bin, tmuxLog)
	return e, e.loggedClient(t), tmuxLog
}

// API tmux называет поднятые сессии с окнами и временем создания (шаг 4
// сценария DK-219).
func TestTmuxListNames(t *testing.T) {
	e, c, _ := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux", "")
	var got struct {
		Sessions []tmuxSession `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	want := []tmuxSession{
		{Name: "goal-XR-9", Windows: 1, Created: 1754770421},
		{Name: "task-XR-5", Windows: 2, Created: 1754770500},
	}
	if !reflect.DeepEqual(got.Sessions, want) {
		t.Errorf("сессии: %+v, ожидал %+v", got.Sessions, want)
	}
}

// tmux без единой сессии это штатно пустой список со словами «сессий нет»,
// а не ошибка и не молчание.
func TestTmuxListNoServer(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "tmux", "exit 1")
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tmux без сервера: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"sessions":[]`, "tmux-сессий нет"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
}

// Ненайденный tmux называется и здесь: «tmux нет» и «сессий нет» это разные
// ответы.
func TestTmuxListMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	if err := os.Remove(filepath.Join(e.bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	c := e.loggedClient(t)
	for _, path := range []string{"/api/tmux", "/api/tmux/goal-XR-9"} {
		resp := doReq(t, c, "GET", e.srv.URL+path, "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "tmux не нашёлся") {
			t.Errorf("%s без tmux: %d %s", path, resp.StatusCode, text)
		}
	}
}

// Снимок пейна идёт через capture-pane с точным именем сессии (=имя: с
// двоеточием цели-пейна): без знака равенства tmux взял бы сессию по
// префиксу и снимок пришёл бы от соседки.
func TestTmuxPaneSnapshot(t *testing.T) {
	e, c, tmuxLog := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/goal-XR-9", "")
	var got struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "goal-XR-9" || got.Text != "виток 3 поднят, ход витка ниже\n$ taskctl list\n" {
		t.Fatalf("снимок: %+v", got)
	}
	if log := readFile(t, tmuxLog); !strings.Contains(log, "capture-pane -p -t =goal-XR-9:") {
		t.Errorf("tmux позван не так: %s", log)
	}
}

// Сессии нет, и снимок отвечает 404 со словами, а не пустым экраном.
func TestTmuxPaneNotFound(t *testing.T) {
	e, c, _ := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/goal-XR-777", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "tmux-сессия goal-XR-777 не найдена") {
		t.Fatalf("снимок несуществующей сессии: %d %s", resp.StatusCode, text)
	}
}

// Имя сессии с чужими символами отбивается до подпроцесса.
func TestTmuxPaneBadName(t *testing.T) {
	e, c, tmuxLog := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/%3Brm%20-rf", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("кривое имя: %d %s", resp.StatusCode, text)
	}
	if log := readFile(t, tmuxLog); strings.Contains(log, "capture-pane") {
		t.Errorf("кривое имя дошло до tmux: %s", log)
	}
}

// Зависший capture-pane снимается по сроку, а не держит горутину запроса:
// снимок это подпроцесс со сроком, как все чужие программы сервера.
func TestTmuxPaneHungAnsweredWithError(t *testing.T) {
	e, c, _ := tmuxEnv(t)
	writeScript(t, e.bin, "tmux", "sleep 60")
	old := procTimeout
	procTimeout = 200 * time.Millisecond
	t.Cleanup(func() { procTimeout = old })

	start := time.Now()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/goal-XR-9", "")
	text := body(t, resp)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("ответ занял %v: срок подпроцесса не сработал", took)
	}
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "по сроку") {
		t.Fatalf("зависший снимок: %d %s", resp.StatusCode, text)
	}
}

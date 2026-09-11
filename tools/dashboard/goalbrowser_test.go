package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Браузерный стенд чата цели (DK-938). Сервер дашборда настоящий и стоит на
// синтетическом доме: цель XR-100 идёт циклом в tmux goal-XR-100, замок оболочки
// называет живой виток, виток скрыт из списка чатов (DK-847), а рядом лежит
// вчерашний груминг той же задачи. Страницу открывает headless-chrome, зонд
// testdata/goal_chat_probe.js жмёт кнопку чата в строке доски и на форме цели,
// каждую своим заходом с чистой страницы. Обе обязаны открыть чат витка с его
// заголовком и зелёной точкой работы, а не груминг с красной плашкой «разговор
// остановлен».
//
// Вход в дашборд делает прокси стенда: браузеру куку взять неоткуда, и прокси
// дописывает её к каждому запросу, тот же приём, что у testdata/poc_browser.py.
// Встроенный скрипт страница не исполнит (CSP default-src 'self'), поэтому зонд
// отдаётся своим адресом того же источника. Браузер стенду называет
// DASHBOARD_CHROME, без него шаг пропускается.
func TestBrowserGoalChatOpensLoop(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: браузерный стенд чата цели пропущен")
	}
	e, c, _ := runsEnv(t, `goal-XR-100\t1\t1000\n`)
	goalDoc := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")
	if err := os.MkdirAll(filepath.Dir(goalDoc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goalDoc, []byte("# XR-100: пробный цикл\n\n## Входящие\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const loopTitle = "Цикл цели XR-100: третий виток"
	writeSession(t, e.home, e.proj, "", goalTurnSID,
		`{"type":"summary","summary":"`+loopTitle+`"}`+"\n"+goalTurnTalk("XR-100"), time.Now())
	if err := e.s.chatStoreWrite(goalTurnSID, chatStore{Hidden: true}); err != nil {
		t.Fatal(err)
	}
	writePeerAged(t, e.home, goalTurnSID, "", "busy", time.Now().UnixMilli())
	goalLock(t, e.proj, "XR-100", os.Getpid(), goalTurnSID)

	groom := "ba1e0826-0826-4826-8826-082608260826"
	yesterday := time.Now().Add(-20 * time.Hour)
	writeSession(t, e.home, e.proj, "", groom,
		`{"type":"summary","summary":"Разбор и груминг задачи XR-100"}`+"\n"+
			saidLine("Проведи груминг XR-100", yesterday), yesterday)
	writeBinds(t, e.home, bindTmux(e.home, yesterday.Format("2006-01-02T15:04:05"), groom, "XR-100", "task-XR-100"))

	px := goalChatProxy(t, e, c)
	for _, one := range []struct{ where, hash string }{
		{"в строке доски", "#demo"},
		{"на форме цели", "#demo/XR-100"},
	} {
		v := goalChatLook(t, chrome, px+"/"+one.hash)
		if v.Title != loopTitle {
			t.Errorf("кнопка %s открыла чат %q, ждал чат витка %q", one.where, v.Title, loopTitle)
		}
		if v.Stop || !v.Green {
			t.Errorf("чат витка после кнопки %s не зелёный: %+v", one.where, v)
		}
		if !strings.Contains(v.Note, "Сообщение уйдёт агенту") {
			t.Errorf("над полем чата витка (кнопка %s) нет слов про доставку: %q", one.where, v.Note)
		}
		t.Logf("кнопка %s: %+v", one.where, v)
	}
}

// goalChatView это то, что зонд снял с панели после кнопки чата.
type goalChatView struct {
	Title string `json:"title"`
	Busy  bool   `json:"busy"`
	Stop  bool   `json:"stop"`
	Green bool   `json:"green"`
	Note  string `json:"note"`
}

// goalChatProxy поднимает прокси стенда: кука входа к каждому запросу, зонд
// своим адресом и вставкой в страницу, обрыв потоков событий.
func goalChatProxy(t *testing.T, e *testEnv, c *http.Client) string {
	t.Helper()
	target, err := url.Parse(e.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookies := c.Jar.Cookies(target)
	if len(cookies) == 0 {
		t.Fatal("вход в стенд не дал куки: браузер увидел бы страницу входа")
	}
	probe := readFile(t, filepath.Join("testdata", "goal_chat_probe.js"))
	rp := httputil.NewSingleHostReverseProxy(target)
	direct := rp.Director
	rp.Director = func(r *http.Request) {
		direct(r)
		for _, ck := range cookies {
			r.AddCookie(ck)
		}
		r.Header.Del("Accept-Encoding")
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		page := string(data)
		tag := `<script src="/__probe.js"></script>`
		if strings.Contains(page, "</body>") {
			page = strings.Replace(page, "</body>", tag+"</body>", 1)
		} else {
			page += tag
		}
		resp.Body = io.NopCloser(strings.NewReader(page))
		resp.ContentLength = int64(len(page))
		resp.Header.Set("Content-Length", strconv.Itoa(len(page)))
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/__probe.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, probe)
	})
	// Потоки событий обрываются нарочно: живут они вечно, и страница из-за них
	// не успокаивается, а chrome с --dump-dom висит до срока. Ответ 204
	// EventSource считает концом и не переподключается. Панель и доска
	// собираются обычными запросами, они и есть предмет стенда.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		rp.ServeHTTP(w, r)
	})
	px := httptest.NewServer(mux)
	t.Cleanup(px.Close)
	return px.URL
}

// goalChatLook открывает страницу своим заходом chrome и поднимает итог зонда
// из заголовка страницы.
func goalChatLook(t *testing.T, chrome, page string) goalChatView {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 120*time.Second)
	defer stop()
	cmd := exec.CommandContext(ctx, chrome, "--headless", "--disable-gpu", "--no-sandbox",
		"--hide-scrollbars", "--user-data-dir="+filepath.Join(t.TempDir(), "profile"),
		"--window-size=1400,900", "--virtual-time-budget=40000", "--dump-dom", page)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("chrome на %s: %v\n%s", page, err, out)
	}
	title := ""
	if at := strings.Index(string(out), "<title>DK938:"); at >= 0 {
		rest := string(out)[at+len("<title>DK938:"):]
		if end := strings.Index(rest, "</title>"); end >= 0 {
			title = rest[:end]
		}
	}
	var got struct {
		Look *goalChatView `json:"look"`
		Err  string        `json:"err"`
	}
	raw, err := url.PathUnescape(title)
	if title == "" || err != nil || json.Unmarshal([]byte(raw), &got) != nil {
		t.Fatalf("зонд не вернул итога из браузера на %s (заголовок %q)\n%s", page, title, out)
	}
	if got.Err != "" || got.Look == nil {
		t.Fatalf("зонд встал на %s: %s", page, got.Err)
	}
	return *got.Look
}

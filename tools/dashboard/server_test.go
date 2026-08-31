package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ответ taskctl list --json изображает исполняемая фикстура: доска с одной
// строкой в работе и одной в Backlog, префикс XR.
const boardFixtureJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[{"id":"XR-005","title":"Задача в работе","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"check","title":"Check","rows":[]},` +
	`{"key":"backlog","title":"Backlog","rows":[{"id":"XR-002","title":"Верхняя","type":"bug","p":"P1","r":55,"r_parts":[50,0,0,5,0],"cost":"-","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[]}]}`

// Фикстура утилиты зовётся настоящим подпроцессом, а свежий исполняемый файл
// macOS проверяет на первом запуске: проверка стоит от десятых долей секунды до
// нескольких секунд, повторный запуск того же файла идёт за миллисекунды. Своя
// фикстура у каждого теста складывала эту проверку сотни раз, и пакет тянулся
// шесть минут вместо одной, а под нагрузкой соседних прогонов срывал срок
// слияния (DK-649). Поэтому исполняемым остаётся один неизменный переходник на
// всю машину, а тело фикстуры ложится рядом обычным файлом: его не исполняют, и
// проверять в нём нечего.
const scriptShimBody = "#!/bin/sh\nexec /bin/sh \"$0.sh\" \"$@\"\n"

// scriptBody это имя файла с телом фикстуры рядом со ссылкой на переходник.
func scriptBody(name string) string { return name + ".sh" }

var (
	shimOnce sync.Once
	shimPath string
	shimErr  error
)

// scriptShim кладёт переходник в постоянный каталог временных файлов
// пользователя. Отпечаток тела стоит в имени каталога, поэтому правка тела
// берёт новый путь, а проверка macOS переживает не только тест, но и весь
// прогон вместе с соседними.
func scriptShim() (string, error) {
	shimOnce.Do(func() {
		sum := sha256.Sum256([]byte(scriptShimBody))
		dir := filepath.Join(os.TempDir(), "dashboard-test-shim-"+hex.EncodeToString(sum[:8]))
		if shimErr = os.MkdirAll(dir, 0o755); shimErr != nil {
			return
		}
		path := filepath.Join(dir, "shim")
		if fi, err := os.Stat(path); err == nil && fi.Mode()&0o111 != 0 && fi.Size() == int64(len(scriptShimBody)) {
			shimPath = path
			return
		}
		// Переходник кладётся переименованием: за тот же путь могут взяться
		// соседние прогоны пакета, и дописываемый файл они поймали бы на бегу.
		tmp, err := os.CreateTemp(dir, "shim-*")
		if err != nil {
			shimErr = err
			return
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(scriptShimBody); err != nil {
			tmp.Close()
			shimErr = err
			return
		}
		if err := tmp.Close(); err != nil {
			shimErr = err
			return
		}
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			shimErr = err
			return
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			shimErr = err
			return
		}
		shimPath = path
	})
	return shimPath, shimErr
}

func writeScript(t testing.TB, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, scriptBody(name)), []byte("#!/bin/sh\n"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, name)
	if _, err := os.Lstat(link); err == nil {
		return
	}
	shim, err := scriptShim()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shim, link); err != nil {
		t.Fatal(err)
	}
}

type testEnv struct {
	srv  *httptest.Server
	s    *server
	cfg  *Config
	home string
	proj string // путь синтетического проекта
	bin  string // каталог исполняемых фикстур в PATH
}

// newTestEnv поднимает сервер на временном доме с синтетическим проектом;
// taskctl и tmux подменены исполняемыми фикстурами в PATH.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	loginPause = 0
	home := t.TempDir()
	root := filepath.Join(home, "projects")
	proj := filepath.Join(root, "demo")
	mkProject(t, proj)

	bin := t.TempDir()
	writeScript(t, bin, "taskctl", fmt.Sprintf("echo '%s'", boardFixtureJSON))
	writeScript(t, bin, "tmux", "printf 'goal-XR-9\\ntask-XR-5\\nчужая-сессия\\n'")
	// Фикстура подписок стоит в стенде с самого начала: живой agentctl лежит в
	// PATH разработчика, сам ходит в git, и тесты, которые считают подпроцессы,
	// засчитывали его вызовы своим (DK-512). Тесту, которому нужна другая
	// раскладка, довольно переписать скрипт поверх.
	writeAgentctlFake(t, bin, harnessJSONFixture)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Своё дерево devkit у стенда синтетическое, и переменная разработчика сюда
	// не пускается: с ней сервер брал бы настоящую оболочку цикла вместо
	// фикстуры, и прогон отвечал бы по машине, а не по стенду. Тест, которому
	// своё дерево нужно, называет его сам.
	t.Setenv("DEVKIT_HOME", "")

	goals := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(goals, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := "goal = XR-112\nroot = " + proj + "\n"
	if err := os.WriteFile(filepath.Join(goals, "XR-112.watch"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Home: home, Roots: []string{root}, Port: defaultPort, Token: "test-token"}
	s := newServer(cfg, os.DirFS("static"), nil)
	// Зонд канала в тестах молчаливо здоров: настоящих сокетов у синтетических
	// сессий нет, и без шва каждый тест со старым транскриптом ловил бы клин.
	// Тесты детектора подставляют свой ответ сами.
	s.probe = func(string, time.Duration) error { return nil }
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, s: s, cfg: cfg, home: home, proj: proj, bin: bin}
}

// client без редиректов: ответы 302 проверяются как есть.
func plainClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// loggedClient входит с верным токеном и держит куку.
func (e *testEnv) loggedClient(t testing.TB) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	resp, err := c.Post(e.srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"token": "test-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход с верным токеном: %d", resp.StatusCode)
	}
	return c
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Без входа не отдаётся ни одна строка данных: API отвечает 401 без строк
// доски, страницы уводят на /login.
func TestNoAuthNoData(t *testing.T) {
	e := newTestEnv(t)
	c := plainClient()
	for _, path := range []string{"/api/projects", "/api/projects/demo/board",
		"/api/projects/demo/goals/XR-100/log", "/api/projects/demo/sessions",
		"/api/projects/demo/sessions/abc", "/api/notifications", "/api/tmux",
		"/api/tmux/goal-XR-9"} {
		resp, err := c.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		text := body(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s без входа: %d, ожидал 401", path, resp.StatusCode)
		}
		for _, leak := range []string{"XR-005", "XR-002", "demo"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s без входа отдал данные: %q в %q", path, leak, text)
			}
		}
	}
	for _, path := range []string{"/", "/assets/app.js"} {
		resp, err := c.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
			t.Errorf("%s без входа: %d -> %q, ожидал 302 /login", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

func TestHealthzOpen(t *testing.T) {
	e := newTestEnv(t)
	resp, err := plainClient().Get(e.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK       bool     `json:"ok"`
		Version  string   `json:"version"`
		Projects int      `json:"projects"`
		Errors   []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !got.OK {
		t.Fatalf("healthz: %d %+v", resp.StatusCode, got)
	}
	if !strings.HasPrefix(got.Version, "dashboard ") {
		t.Errorf("версия %q не в формате утилит devkit", got.Version)
	}
	if got.Projects != 1 || len(got.Errors) != 0 {
		t.Errorf("проектов %d, ошибки %v", got.Projects, got.Errors)
	}
}

func TestLoginWrongToken(t *testing.T) {
	e := newTestEnv(t)
	resp, err := plainClient().Post(e.srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"token": "не тот"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("чужой токен: %d, ожидал 401", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatal("провал входа не должен ставить куку")
	}
}

// Ротация секрета живым демоном (DK-481): dashboard secret --rotate меняет
// только файл конфига, а сервер, не перечитывая его, вечно сверяет вход и
// куки со стартовым токеном. Без перечитывания по mtime этот тест краснеет:
// вход по новому токену получает 403, старая кука продолжает пускать.
func TestSecretRotateLiveReload(t *testing.T) {
	loginPause = 0
	home := t.TempDir()
	writeConf(t, home, "root = /x\ntoken = old-secret\n")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(cfg, os.DirFS("static"), nil)
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login := func(token string) int {
		resp, err := client.Post(srv.URL+"/api/login", "application/json",
			strings.NewReader(`{"token": "`+token+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := login("old-secret"); got != http.StatusOK {
		t.Fatalf("вход старым секретом: %d, ожидал 200", got)
	}
	resp, err := client.Get(srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("кука сразу после входа не пускает: %d", resp.StatusCode)
	}

	if _, err := cmdSecret(home, true); err != nil {
		t.Fatal(err)
	}
	// mtime может совпасть с прежним на грубых файловых системах: явно
	// отодвигаем его в будущее, чтобы сравнение mtime увидело перемену
	// детерминированно, а не по случайности гонки часов.
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(cfg.Path, future, future); err != nil {
		t.Fatal(err)
	}

	resp, err = client.Get(srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("кука с доротационным секретом ещё жива после ротации: %d, ожидал 401", resp.StatusCode)
	}

	newToken, err := cmdSecret(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := login(newToken); got != http.StatusOK {
		t.Fatalf("вход новым токеном без рестарта демона: %d, ожидал 200", got)
	}
}

// Провал перечитывания (файл конфига пропал) не роняет вход и не открывает
// его посторонним: действует последний удачно прочитанный секрет, а причина
// видна в /healthz.
func TestSecretRereadFailureKeepsOldToken(t *testing.T) {
	loginPause = 0
	home := t.TempDir()
	path := writeConf(t, home, "root = /x\ntoken = keep-me\n")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(cfg, os.DirFS("static"), nil)
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	// Первый запрос читает файл и запоминает его mtime.
	if resp, err := http.Get(srv.URL + "/healthz"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"token": "keep-me"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("пропавший файл конфига не должен ронять вход прежним токеном: %d", resp.StatusCode)
	}

	hz, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer hz.Body.Close()
	var got struct {
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(hz.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range got.Errors {
		if strings.Contains(e, "secret") {
			found = true
		}
	}
	if !found {
		t.Fatalf("healthz не назвал причину провала перечитывания секрета: %v", got.Errors)
	}
}

// Проверка входа идёт на каждом запросе и должна выдерживать параллельные
// вызовы без гонки данных: тест ловится флагом -race, а не своей проверкой.
func TestSecretRefreshTokenConcurrent(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "root = /x\ntoken = concurrent-secret\n")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	s := newServer(cfg, os.DirFS("static"), nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.refreshToken()
		}()
	}
	wg.Wait()
}

func TestProjectsAfterLogin(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp, err := c.Get(e.srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Projects []struct {
			Name     string         `json:"name"`
			Sections map[string]int `json:"sections"`
			Works    []Work         `json:"works"`
		} `json:"projects"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(got.Projects) != 1 || got.Projects[0].Name != "demo" {
		t.Fatalf("проекты: %+v", got.Projects)
	}
	p := got.Projects[0]
	if p.Sections["in-progress"] != 1 || p.Sections["backlog"] != 1 || p.Sections["check"] != 0 {
		t.Errorf("счётчики секций: %v", p.Sections)
	}
	var works []string
	for _, w := range p.Works {
		works = append(works, w.Kind+"-"+w.ID+"/"+w.Via)
	}
	want := "goal-XR-9/tmux,task-XR-5/tmux,goal-XR-112/registry"
	if strings.Join(works, ",") != want {
		t.Errorf("работы: %v, ожидал %s", works, want)
	}
}

func TestBoardAfterLogin(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp, err := c.Get(e.srv.URL + "/api/projects/demo/board")
	if err != nil {
		t.Fatal(err)
	}
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("доска после входа: %d\n%s", resp.StatusCode, text)
	}
	for _, want := range []string{"XR-005", "Задача в работе", `"prefix":"XR"`} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе доски нет %q", want)
		}
	}
	resp, err = c.Get(e.srv.URL + "/api/projects/ghost/board")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("несуществующий проект: %d, ожидал 404", resp.StatusCode)
	}
}

// Экран доски отдаётся вшитой статикой и не ссылается на внешние хосты:
// внешних зависимостей у статики нет по решению LLD.
func TestStaticNoExternalHosts(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	pages := []string{"/", "/login", "/assets/style.css", "/assets/app.js", "/assets/login.js"}
	for _, page := range pages {
		resp, err := c.Get(e.srv.URL + page)
		if err != nil {
			t.Fatal(err)
		}
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", page, resp.StatusCode)
		}
		// Пространство имён svg это опознавательная строка, а не адрес: за ним
		// браузер никуда не ходит, и требовать его отсутствия значило бы
		// запретить статике рисовать векторную графику вовсе.
		text = strings.ReplaceAll(text, svgNS, "")
		if strings.Contains(text, "http://") || strings.Contains(text, "https://") {
			t.Errorf("%s ссылается на внешний хост", page)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("%s без CSP: %q", page, csp)
		}
	}
}

func TestLoginOriginCheck(t *testing.T) {
	e := newTestEnv(t)
	req, err := http.NewRequest("POST", e.srv.URL+"/api/login", strings.NewReader(`{"token": "test-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.example")
	resp, err := plainClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Origin: %d, ожидал 403", resp.StatusCode)
	}
}

// Заход извне: посредник переписал Host на адрес апстрима и назвал внешнее имя
// в X-Forwarded-Host, а браузер поставил Origin по внешнему имени. Сверка
// Origin обязана сойтись, иначе с телефона не пройти даже вход.
func TestLoginOriginForwardedHost(t *testing.T) {
	e := newTestEnv(t)
	req, err := http.NewRequest("POST", e.srv.URL+"/api/login", strings.NewReader(`{"token": "test-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "127.0.0.1:7112"
	req.Header.Set("X-Forwarded-Host", "dash.entry.example")
	req.Header.Set("Origin", "https://dash.entry.example")
	resp, err := plainClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход через заход извне: %d, ожидал 200", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("вход через заход извне прошёл без куки")
	}
}

// Внешнее имя не открывает дверь кому попало: чужой Origin остаётся чужим и за
// посредником.
func TestLoginForeignOriginBehindProxy(t *testing.T) {
	e := newTestEnv(t)
	req, err := http.NewRequest("POST", e.srv.URL+"/api/login", strings.NewReader(`{"token": "test-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-Host", "dash.entry.example")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := plainClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Origin за посредником: %d, ожидал 403", resp.StatusCode)
	}
}

func TestExternalHost(t *testing.T) {
	cases := []struct {
		name string
		host string
		fwd  string
		want string
	}{
		{"без посредника", "192.168.1.10:7112", "", "192.168.1.10:7112"},
		{"посредник назвал имя", "127.0.0.1:7112", "dash.entry.example", "dash.entry.example"},
		{"цепочка посредников", "127.0.0.1:7112", "dash.entry.example, inner.example", "dash.entry.example"},
		{"пробелы вокруг имени", "127.0.0.1:7112", "  dash.entry.example  ", "dash.entry.example"},
		{"пустой заголовок", "192.168.1.10:7112", "   ", "192.168.1.10:7112"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.Host = c.host
			if c.fwd != "" {
				r.Header.Set("X-Forwarded-Host", c.fwd)
			}
			if got := externalHost(r); got != c.want {
				t.Errorf("externalHost = %q, ожидал %q", got, c.want)
			}
		})
	}
}

func TestLogout(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp, err := c.Post(e.srv.URL+"/api/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = c.Get(e.srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("после выхода: %d, ожидал 401", resp.StatusCode)
	}
}

// Провал входа стоит паузу, она гасит перебор токена; верный вход не ждёт.
func TestLoginFailurePause(t *testing.T) {
	e := newTestEnv(t)
	loginPause = 200 * time.Millisecond
	t.Cleanup(func() { loginPause = 0 })
	start := time.Now()
	resp, err := plainClient().Post(e.srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"token": "не тот"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	took := time.Since(start)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("чужой токен: %d, ожидал 401", resp.StatusCode)
	}
	if took < loginPause {
		t.Fatalf("провал входа занял %v при паузе %v: перебор токена ничем не гасится", took, loginPause)
	}
	start = time.Now()
	e.loggedClient(t)
	if took := time.Since(start); took >= loginPause {
		t.Fatalf("верный вход занял %v: пауза должна стоять только на провале", took)
	}
}

// Зависший taskctl оборачивается ошибкой ответа по сроку, а не вечным
// ожиданием: подвисший подпроцесс не должен держать горутину запроса.
func TestHungTaskctlAnsweredWithError(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", "sleep 60")
	old := procTimeout
	procTimeout = 200 * time.Millisecond
	t.Cleanup(func() { procTimeout = old })

	c := e.loggedClient(t)
	start := time.Now()
	resp, err := c.Get(e.srv.URL + "/api/projects/demo/board")
	if err != nil {
		t.Fatal(err)
	}
	text := body(t, resp)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("ответ занял %v: срок подпроцесса не сработал", took)
	}
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "по сроку") {
		t.Fatalf("зависший taskctl: %d %s, ожидал 502 с ошибкой про срок", resp.StatusCode, text)
	}
}

// Ненайденный tmux это названная ошибка в /healthz, в списке проектов и в
// ответе доски, а не молча пустой список работ: пустота без причины
// неотличима от «агенты не работают». Реестр целей при этом жив, он
// читается с диска без подпроцесса.
func TestTmuxMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	if err := os.Remove(filepath.Join(e.bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)

	resp, err := plainClient().Get(e.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if text := body(t, resp); !strings.Contains(text, "tmux не нашёлся") {
		t.Errorf("healthz молчит про пропавший tmux: %s", text)
	}
	c := e.loggedClient(t)
	resp, err = c.Get(e.srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Projects []struct {
			Works []Work `json:"works"`
		} `json:"projects"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(strings.Join(got.Errors, "; "), "tmux не нашёлся") {
		t.Errorf("список проектов молчит про пропавший tmux: %v", got.Errors)
	}
	works := got.Projects[0].Works
	if len(works) != 1 || works[0].Via != "registry" {
		t.Errorf("ждал одну работу из реестра, получил %v", works)
	}
	resp, err = c.Get(e.srv.URL + "/api/projects/demo/board")
	if err != nil {
		t.Fatal(err)
	}
	if text := body(t, resp); !strings.Contains(text, "tmux не нашёлся") {
		t.Errorf("ответ доски молчит про пропавший tmux: %s", text)
	}
}

// tmux на месте, но без единой сессии его сервер не живёт и ls отвечает
// ненулевым кодом: это штатное «сессий нет», пустой список без ошибки.
func TestTmuxNoServerQuiet(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "tmux", "exit 1")

	resp, err := plainClient().Get(e.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if text := body(t, resp); strings.Contains(text, "tmux") {
		t.Errorf("healthz жалуется на живой tmux без сессий: %s", text)
	}
	c := e.loggedClient(t)
	resp, err = c.Get(e.srv.URL + "/api/projects/demo/board")
	if err != nil {
		t.Fatal(err)
	}
	text := body(t, resp)
	if strings.Contains(text, "tmux") {
		t.Errorf("ответ доски жалуется на живой tmux без сессий: %s", text)
	}
	if !strings.Contains(text, `"via":"registry"`) {
		t.Errorf("работа из реестра пропала: %s", text)
	}
}

// Из-под launchd PATH системный, и taskctl ищется рядом с собственным
// бинарём: фикстура в каталоге exeDir находится с пустым PATH. Дефект шага 5
// сценария: /healthz отвечал «taskctl не нашёлся», хотя бинарь стоял рядом с
// самим dashboard.
func TestTaskctlFoundNextToExecutable(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "taskctl", fmt.Sprintf("echo '%s'", boardFixtureJSON))
	oldExe := exeDir
	exeDir = func() string { return dir }
	t.Cleanup(func() { exeDir = oldExe })
	t.Setenv("PATH", t.TempDir())

	if m := taskctlMissing(); m != "" {
		t.Fatalf("taskctl лежит рядом с бинарём, а диагностика: %s", m)
	}
	raw, err := boardJSON(t.TempDir())
	if err != nil {
		t.Fatalf("доска не прочиталась соседом по каталогу: %v", err)
	}
	if !strings.Contains(string(raw), `"prefix":"XR"`) {
		t.Fatalf("ответ не от фикстуры: %s", raw)
	}
}

// Пропавший taskctl это названная ошибка в /healthz и в ответе доски, а не
// пустая доска: тихая деградация неотличима от «задач нет».
func TestTaskctlMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	old := taskctlBin
	taskctlBin = "no-such-taskctl-binary"
	t.Cleanup(func() { taskctlBin = old })

	resp, err := plainClient().Get(e.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	text := body(t, resp)
	if !strings.Contains(text, "taskctl") {
		t.Errorf("healthz молчит про пропавший taskctl: %s", text)
	}
	c := e.loggedClient(t)
	resp, err = c.Get(e.srv.URL + "/api/projects/demo/board")
	if err != nil {
		t.Fatal(err)
	}
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "taskctl") {
		t.Errorf("ответ доски без taskctl: %d %s", resp.StatusCode, text)
	}
}

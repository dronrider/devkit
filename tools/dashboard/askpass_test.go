package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// askpassSecretHeaderTest это тот же заголовок, каким предъявляет себя
// помощник; имя тут не импортируется из askpass.go нарочно, чтобы опечатка в
// одной из двух констант не осталась незамеченной обеими сторонами теста.
const askpassSecretHeaderTest = "X-Devkit-Askpass-Secret"

// waitForAskpass опрашивает GET .../ask, пока не встанет вопрос помощника
// пароля (или не выйдет срок): опрос демона в проде идёт из панели раз в
// три секунды, а тест не должен ждать так долго.
func waitForAskpass(t *testing.T, c *http.Client, base, sid string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp := doReq(t, c, "GET", base+"/api/projects/demo/chats/"+sid+"/ask", "")
		var parsed struct {
			Ask map[string]any `json:"ask"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &parsed); err == nil &&
			parsed.Ask != nil && parsed.Ask["kind"] == askKindPass {
			return parsed.Ask
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("вопрос помощника не встал в GET .../ask вовремя")
	return nil
}

// askpassPost зовёт /api/askpass как звал бы помощник: секрет заголовком,
// разговор и текст запроса телом.
func askpassPost(t *testing.T, base, chatSecret, sid, tmux, prompt string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("POST", base+"/api/askpass", strings.NewReader(
		`{"chat":"`+sid+`","tmux":"`+tmux+`","prompt":"`+prompt+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if chatSecret != "" {
		req.Header.Set(askpassSecretHeaderTest, chatSecret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp, body(t, resp)
}

// TestAskpassRequestAnswerRoundTrip проходит весь путь вопроса без подмены
// sudo: помощник ставит вопрос (POST /api/askpass), панель видит его в
// GET .../ask и отвечает POST .../askpass, а помощник получает пароль в теле
// ответа. Вопрос закрывается: второй GET его уже не находит.
func TestAskpassRequestAnswerRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00daaaa1111"
	c := e.loggedClient(t)
	sid := "aaaa9999-1111-4111-8111-111111111111"

	type askResult struct {
		code int
		body string
	}
	done := make(chan askResult, 1)
	go func() {
		resp, text := askpassPost(t, e.srv.URL, e.s.askpassSecret, sid, "chat-9", "[sudo] Password:")
		done <- askResult{resp.StatusCode, text}
	}()

	ask := waitForAskpass(t, c, e.srv.URL, sid)
	if ask["text"] != "[sudo] Password:" {
		t.Errorf("текст запроса не тот: %v", ask)
	}
	id, _ := ask["id"].(string)
	if id == "" {
		t.Fatal("у вопроса нет id")
	}

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/askpass",
		`{"id":"`+id+`","text":"пароль-панели-7"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ панели не принят: %d %s", resp.StatusCode, body(t, resp))
	}

	select {
	case r := <-done:
		if r.code != http.StatusOK {
			t.Fatalf("помощник получил не 200: %d %s", r.code, r.body)
		}
		if !strings.Contains(r.body, `"password":"пароль-панели-7"`) {
			t.Errorf("пароль не доехал до помощника: %s", r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("помощник не дождался ответа")
	}

	resp2 := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
	if strings.Contains(body(t, resp2), `"kind":"`+askKindPass+`"`) {
		t.Errorf("вопрос помощника остался висеть после ответа: %s", body(t, resp2))
	}
}

// TestAskpassCancelReturnsGone проверяет отмену: панель шлёт cancel, и
// помощник получает 410, а не пароль. Sudo на таком ответе отказывает, как
// при пустом пароле (askpass.py, fail на коде 410).
func TestAskpassCancelReturnsGone(t *testing.T) {
	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00dbbbb2222"
	c := e.loggedClient(t)
	sid := "bbbb9999-2222-4222-8222-222222222222"

	type askResult struct {
		code int
		body string
	}
	done := make(chan askResult, 1)
	go func() {
		resp, text := askpassPost(t, e.srv.URL, e.s.askpassSecret, sid, "chat-9", "[sudo] Password:")
		done <- askResult{resp.StatusCode, text}
	}()

	ask := waitForAskpass(t, c, e.srv.URL, sid)
	id, _ := ask["id"].(string)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/askpass",
		`{"id":"`+id+`","cancel":true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отмена не принята: %d %s", resp.StatusCode, body(t, resp))
	}

	select {
	case r := <-done:
		if r.code != http.StatusGone {
			t.Fatalf("отменённый вопрос отдал помощнику не 410: %d %s", r.code, r.body)
		}
		if strings.Contains(r.body, "password") {
			t.Errorf("отменённый ответ несёт поле password: %s", r.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("помощник не дождался отмены")
	}
}

// TestAskpassTimeoutClosesWait укорачивает срок ожидания и проверяет, что
// помощник получает отказ сам, без ответа панели: забытый вопрос не должен
// держать sudo подвешенным вечно.
func TestAskpassTimeoutClosesWait(t *testing.T) {
	was := askpassTimeout
	askpassTimeout = 60 * time.Millisecond
	t.Cleanup(func() { askpassTimeout = was })

	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00dcccc3333"
	sid := "cccc9999-3333-4333-8333-333333333333"

	resp, text := askpassPost(t, e.srv.URL, e.s.askpassSecret, sid, "chat-9", "[sudo] Password:")
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("вышедший срок отдал не 504: %d %s", resp.StatusCode, text)
	}

	c := e.loggedClient(t)
	r2 := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
	if strings.Contains(body(t, r2), `"kind":"`+askKindPass+`"`) {
		t.Errorf("протухший вопрос всё ещё виден панели: %s", body(t, r2))
	}
}

// TestAskpassWrongOrEmptySecretRejected: заголовок с чужим или пустым
// секретом получает 401 и не заводит запись ожидания вовсе. Кука панели тут
// ни при чём, демон узнаёт своего по локальному секрету.
func TestAskpassWrongOrEmptySecretRejected(t *testing.T) {
	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00ddddd4444"
	sid := "dddd9999-4444-4444-8444-444444444444"

	for _, tc := range []struct {
		name, secret string
	}{
		{"чужой", "не-тот-секрет"},
		{"пустой", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, text := askpassPost(t, e.srv.URL, tc.secret, sid, "chat-9", "[sudo] Password:")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s секрет не отбит 401: %d %s", tc.name, resp.StatusCode, text)
			}
		})
	}
	if _, ok := e.s.askpassPending(sid); ok {
		t.Error("неавторизованный запрос всё равно завёл запись ожидания")
	}
}

// TestAskpassPasswordNeverLogged проходит вопрос целиком и сверяет, что
// пароль не осел ни в журнале демона (logf), ни в каком-либо файле под
// ~/.devkit тестового дома: DoD задачи требует именно это, а не «на глаз».
func TestAskpassPasswordNeverLogged(t *testing.T) {
	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00deeee5555"
	var lines []string
	e.s.logf = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	c := e.loggedClient(t)
	sid := "eeee9999-5555-4555-8555-555555555555"

	const secretWord = "не-должен-попасть-в-журнал-42"
	done := make(chan struct{})
	go func() {
		askpassPost(t, e.srv.URL, e.s.askpassSecret, sid, "chat-9", "[sudo] Password:")
		close(done)
	}()
	ask := waitForAskpass(t, c, e.srv.URL, sid)
	id, _ := ask["id"].(string)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/askpass",
		`{"id":"`+id+`","text":"`+secretWord+`"}`)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("помощник не дождался ответа")
	}

	for _, ln := range lines {
		if strings.Contains(ln, secretWord) {
			t.Fatalf("пароль попал в журнал: %q", ln)
		}
	}
	if err := filepath.Walk(e.home, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if strings.Contains(string(data), secretWord) {
			t.Errorf("пароль осел в файле %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestLaunchEnvAskpassVars: SUDO_ASKPASS/SSH_ASKPASS/DISPLAY/DEVKIT_ADDR
// встают в окружение подъёма, когда помощник разложен на диск, DEVKIT_CHAT
// встаёт только при известном sid, а без файла помощника переменных нет
// вовсе и причина названа в журнале, а не молчит.
func TestLaunchEnvAskpassVars(t *testing.T) {
	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00dffff6666"
	var lines []string
	e.s.logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }

	home := t.TempDir()
	was := realHomeFn
	realHomeFn = func() string { return home }
	t.Cleanup(func() { realHomeFn = was })

	// Без помощника на диске: переменных askpass нет, а причина в журнале.
	env := e.s.launchEnv("XR-1", "chat-XR-1-1", "")
	if strings.Contains(env, "SUDO_ASKPASS=") {
		t.Errorf("SUDO_ASKPASS встал без разложенного помощника: %s", env)
	}
	found := false
	for _, ln := range lines {
		if strings.Contains(ln, "помощник пароля не разложен") {
			found = true
		}
	}
	if !found {
		t.Errorf("отсутствие помощника не названо в журнале: %v", lines)
	}

	// Помощник на диске, исполняемый: переменные встают все разом.
	helperPath := askpassHelperPath(home)
	if err := os.MkdirAll(filepath.Dir(helperPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, []byte("#!/usr/bin/env python3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	envWithID := e.s.launchEnv("XR-1", "chat-XR-1-1", "sid-known-1")
	for _, want := range []string{
		"SUDO_ASKPASS='" + helperPath + "'",
		"SSH_ASKPASS='" + helperPath + "'",
		"DISPLAY='" + askpassDisplay + "'",
		"DEVKIT_ADDR='" + e.s.cfg.ListenAddr() + "'",
		"DEVKIT_CHAT='sid-known-1'",
	} {
		if !strings.Contains(envWithID, want) {
			t.Errorf("в окружении подъёма нет %q: %s", want, envWithID)
		}
	}

	// Без известного sid (новый чат, конвейер, разбор, вход) DEVKIT_CHAT не
	// встаёт вовсе: помощник найдёт разговор обратным поиском по tmux.
	envNoID := e.s.launchEnv("XR-1", "chat-XR-1-1", "")
	if strings.Contains(envNoID, "DEVKIT_CHAT=") {
		t.Errorf("DEVKIT_CHAT встал без известного sid: %s", envNoID)
	}
	if !strings.Contains(envNoID, "SUDO_ASKPASS=") {
		t.Errorf("SUDO_ASKPASS пропал у дороги без sid: %s", envNoID)
	}
}

// TestAskpassChainWithFakeSudo это стенд цепочки целиком: поддельный sudo
// зовёт настоящего помощника (tools/askpass/askpass.py) в точности так, как
// звал бы его живой sudo, помощник ходит в настоящий HTTP-сервер демона, а
// панель отвечает через тот же API, что и человек. Дом на время теста свой
// (realHomeFn), иначе помощник и секрет легли бы в общий дом всего прогона
// пакета и красили бы соседние тесты чужим SUDO_ASKPASS.
func TestAskpassChainWithFakeSudo(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 не в PATH: помощнику нечем выполниться")
	}
	e := newTestEnv(t)
	e.s.askpassSecret = "cafef00dgggg7777"

	// Срок ожидания укорочен: сорвись цепочка, тест обязан упасть быстро, а
	// не повиснуть на боевые 120 секунд (python-клиент помощника ждёт больше
	// серверного срока и всегда получит от демона отказ первым).
	wasTimeout := askpassTimeout
	askpassTimeout = 5 * time.Second
	t.Cleanup(func() { askpassTimeout = wasTimeout })

	home := t.TempDir()
	was := realHomeFn
	realHomeFn = func() string { return home }
	t.Cleanup(func() { realHomeFn = was })

	helperSrc, err := os.ReadFile(filepath.Join("..", "askpass", "askpass.py"))
	if err != nil {
		t.Fatalf("исходник помощника не читается: %v", err)
	}
	helperPath := askpassHelperPath(home)
	if err := os.MkdirAll(filepath.Dir(helperPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	// Секрет тем же кодом, каким его пишет демон на старте (main.go): помощник
	// читает файл, а не переменную, и без него сверка заголовка не сойдётся.
	if err := writeAskpassSecret(home, e.s.askpassSecret); err != nil {
		t.Fatal(err)
	}

	// Демон слушает httptest на случайном порту, а не cfg.Port: адрес
	// помощнику назвать обязан тот же самый, иначе стучаться некуда.
	u, err := url.Parse(e.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	e.s.cfg.Addr, e.s.cfg.Port = host, port

	sid := "gggg9999-chain-4777-8777-777777777777"
	env := e.s.launchEnv("", "chat-chain-1", sid)
	if !strings.Contains(env, "SUDO_ASKPASS='"+helperPath+"'") {
		t.Fatalf("SUDO_ASKPASS не встал на разложенного помощника: %s", env)
	}

	sudoBin := t.TempDir()
	// Поддельный sudo зовёт помощника, как звал бы настоящий: текст запроса
	// первым аргументом, пароль ждёт строкой в stdout, отказ помощника (код
	// не ноль) sudo отбивает и не подаёт ничего дальше.
	writeScript(t, sudoBin, "fake-sudo", `pw="$("$SUDO_ASKPASS" "$1")" || { echo SUDO-FAIL; exit 1; }
printf 'SUDO-OK:%s\n' "$pw"`)

	// Подменный sudo идёт своей горутиной: t.Fatal вправе звать только
	// главная горутина теста, а ждущий exec.Command мог бы срываться по
	// таймауту раньше, чем панель успеет ответить.
	cmdLine := env + shQuote(filepath.Join(sudoBin, "fake-sudo")) + " " + shQuote("[sudo] Password:")
	sudoDone := make(chan []byte, 1)
	sudoErr := make(chan error, 1)
	go func() {
		out, err := exec.Command("sh", "-c", cmdLine).CombinedOutput()
		sudoDone <- out
		sudoErr <- err
	}()

	c := e.loggedClient(t)
	var ask map[string]any
	deadline := time.Now().Add(3 * time.Second)
pollLoop:
	for time.Now().Before(deadline) {
		select {
		case out := <-sudoDone:
			err := <-sudoErr
			t.Fatalf("подменный sudo кончился раньше вопроса (err=%v):\n%s", err, out)
		default:
		}
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
		text := body(t, resp)
		var parsed struct {
			Ask map[string]any `json:"ask"`
		}
		if json.Unmarshal([]byte(text), &parsed) == nil && parsed.Ask != nil && parsed.Ask["kind"] == askKindPass {
			ask = parsed.Ask
			break pollLoop
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ask == nil {
		t.Fatal("вопрос помощника не встал в GET .../ask вовремя")
	}
	id, _ := ask["id"].(string)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/askpass",
		`{"id":"`+id+`","text":"свежий-пароль-77"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ панели не принялся: %d %s", resp.StatusCode, body(t, resp))
	}

	select {
	case out := <-sudoDone:
		if err := <-sudoErr; err != nil {
			t.Fatalf("подменный sudo не прошёл: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "SUDO-OK:свежий-пароль-77") {
			t.Fatalf("пароль не доехал через настоящего помощника до подменного sudo: %s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("подменный sudo не завершился вовремя")
	}
}

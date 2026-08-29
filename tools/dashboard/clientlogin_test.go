package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Стенд входа клиента (DK-577). tmux подменён скриптом с памятью: send-keys
// меняет стадию панели, capture-pane печатает панель стадии, а пропажа файла
// стадии значит смерть сессии. Так прогоняется весь ход входа без настоящей
// tmux и клиента: подъём сессии, выбор способа, ссылка, код и три отказа.

// Курсор и рамка это знаки панели клиента; в исходнике они собираются рунами,
// чтобы текст файла оставался в раскладках en/ru (правило символов).
var (
	paneCursor = string(rune(0x276f))
	paneFrame  = string(rune(0x2500))
)

// Панели стадий входа: пустая панель разгона, вопрос доверия каталогу, REPL без
// входа и такой же видом REPL, но вычищающий ввод (клиент после ответа о
// доверии поднимает REPL заново и съедает поданное в момент пересборки),
// виджет выбора способа, ссылка с полем кода, сделанный вход и отказ кода.
// Вид взят с живой панели клиента.
func loginPaneOf(stage string) string {
	code := "Paste code here if prompted >" + "\n" + paneCursor + " \n"
	switch stage {
	case "boot":
		return ""
	case "repl", "init":
		return "Login expired. Please run /login\n\n" + paneCursor + " \n"
	case "trust":
		return strings.Join([]string{
			"", strings.Repeat(paneFrame, 60),
			" Accessing workspace:", "", " /Users/rider", "",
			" Quick safety check: is this a project you trust?",
			paneCursor + " No, exit",
			"  Yes, I trust this folder", "",
			" Enter to confirm, Esc to cancel",
			strings.Repeat(paneFrame, 60), "",
		}, "\n")
	case "ask":
		return strings.Join([]string{
			"", strings.Repeat(paneFrame, 60),
			" Select login method", "",
			paneCursor + " 1. Claude account with subscription",
			"  2. Anthropic Console account", "",
			" Enter to select", strings.Repeat(paneFrame, 60), "",
		}, "\n")
	case "url":
		return strings.Join([]string{
			"Please visit the following URL to log in:", "",
			"https://claude.ai/oauth/authorize?client_id=test&state=abc123", "",
			code,
		}, "\n")
	case "ok":
		return strings.Join([]string{"Welcome back!", "", paneCursor + " \n"}, "\n")
	case "again":
		return strings.Join([]string{
			"Invalid authorization code. Try again.", "", code,
		}, "\n")
	}
	return ""
}

// fakeTmuxLogin ставит скрипт tmux с панелью входа и возвращает каталог его
// памяти. Стадии: boot (клиент разгоняется, панель пуста и на третьем снимке
// рисует REPL), trust (виджет доверия каталогу), init (REPL вида готового, но
// пересборка вычищает ввод, и со второго снимка он готов по-настоящему), repl
// (REPL без входа), ask (виджет выбора способа), url (ссылка и поле кода), ok
// (вход сделан), again (код не принят); пропажа файла стадии это смерть
// сессии. Хвостовая черта в строке фикстуры значит перенос строки пейна:
// capture-pane с -J склеивает такие строки, как живой tmux. Нажатие на пустой
// панели оставляет метку early: в ненарисовавшегося клиента нажатия не уходят.
func fakeTmuxLogin(t *testing.T, e *testEnv) string {
	t.Helper()
	d := t.TempDir()
	for _, stage := range []string{"boot", "repl", "init", "trust", "ask", "url", "ok", "again"} {
		if err := os.WriteFile(filepath.Join(d, "pane-"+stage),
			[]byte(loginPaneOf(stage)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeScript(t, e.bin, "tmux", `D="`+d+`"
echo "$1" >>"$D/calls"
case "$1" in
ls) printf 'goal-XR-9\n';;
new-session)
  if [ -f "$D/first" ]; then cat "$D/first" >"$D/stage"; else echo repl >"$D/stage"; fi;;
kill-session) rm -f "$D/stage";;
capture-pane)
  [ -f "$D/stage" ] || exit 3
  st=$(cat "$D/stage")
  if [ "$st" = "boot" ]; then
    n=$(($(cat "$D/bootN" 2>/dev/null || echo 0)+1)); echo "$n" >"$D/bootN"
    [ "$n" -ge 3 ] && echo repl >"$D/stage"; st=$(cat "$D/stage")
  fi
  if [ "$st" = "init" ]; then
    n=$(($(cat "$D/initN" 2>/dev/null || echo 0)+1)); echo "$n" >"$D/initN"
    [ "$n" -ge 4 ] && echo repl >"$D/stage"; st=$(cat "$D/stage")
  fi
  pane="$D/pane-$st"
  case " $* " in
  *" -J "*) awk '{ if (sub(/\\$/,"")) printf "%s", $0; else print }' "$pane";;
  *) awk '{ sub(/\\$/,""); print }' "$pane";;
  esac
  ;;
send-keys)
  st=$(cat "$D/stage" 2>/dev/null || echo gone)
  if [ "$4" = "-l" ] && [ "$st" != "init" ]; then printf '%s' "$5" >"$D/last"
  elif [ "$4" = "Enter" ]; then
    last=$(cat "$D/last" 2>/dev/null || true)
    if [ "$st" = "repl" ]; then echo ask >"$D/stage"
    elif [ "$st" = "boot" ]; then touch "$D/early"
    elif [ "$st" = "init" ]; then :
    elif [ "$st" = "trust" ]; then echo init >"$D/stage"
    elif [ "$st" = "ask" ]; then echo url >"$D/stage"
    elif [ "$st" = "gone" ]; then :
    else
      case "$last" in
      GOOD) echo ok >"$D/stage";;
      *) echo again >"$D/stage";;
      esac
    fi
  fi
  ;;
esac
exit 0`)
	// Клиент нужен лишь проверкой PATH: сессию в стенде поднимает скрипт tmux,
	// а не настоящий клиент.
	writeScript(t, e.bin, defaultClient, "exit 0")
	t.Setenv("PATH", e.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return d
}

// fastLoginWait сжимает ожидания входа до тестовых. Прогон со стендом занимает
// миллисекунды, а боевые двадцать секунд превращали бы отказ в вечность.
func fastLoginWait(t *testing.T, link time.Duration) {
	t.Helper()
	was := []time.Duration{loginPollEvery, loginSettleWait, loginLinkWait, loginCodeWait}
	loginPollEvery, loginSettleWait = 3*time.Millisecond, 3*time.Millisecond
	loginLinkWait, loginCodeWait = link, 2*time.Second
	t.Cleanup(func() {
		loginPollEvery, loginSettleWait, loginLinkWait, loginCodeWait =
			was[0], was[1], was[2], was[3]
	})
}

func loginPost(t *testing.T, c *http.Client, url, path, body string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Post(url+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(data)
}

// loginCalls считает вызовы tmux стенда с последнего раза. Хвост нужен потому,
// что подъём входа сам шлёт нажатия.
func loginCalls(t *testing.T, d, name string) func(string) int {
	t.Helper()
	file := filepath.Join(d, name)
	last := 0
	return func(what string) int {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		got := 0
		for _, ln := range lines[last:] {
			if ln == what {
				got++
			}
		}
		last = len(lines)
		return got
	}
}

// Подъём входа проходит выбор способа и отдаёт ссылку, а повторный экран
// получает ту же ссылку, не поднимая соседнюю сессию.
func TestClientLoginStartGivesLink(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём входа: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "https://claude.ai/oauth/authorize") {
		t.Fatalf("ссылка авторизации не приехала: %s", text)
	}
	if !strings.Contains(text, `"tmux":"login-1"`) {
		t.Fatalf("имя сессии входа не названо: %s", text)
	}
	if got := sent("new-session"); got != 1 {
		t.Fatalf("сессий входа поднято %d, ожидалась одна", got)
	}
	// Вторая вкладка получает ту же ссылку: вход на машине один.
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "https://claude.ai/oauth/authorize") {
		t.Fatalf("повторный экран входа не получил ссылку: %d, %s", resp.StatusCode, text)
	}
	if got := sent("new-session"); got != 0 {
		t.Fatalf("повторный экран поднял вторую сессию входа: %d", got)
	}
}

// Код принят: сессия входа снята, слова говорят про перезапуск разговоров, а
// сам код не остался ни в журнале дашборда, ни в ответе.
func TestClientLoginCodeAccepted(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	var journal strings.Builder
	e.s.logf = func(format string, args ...any) { journal.WriteString(fmt.Sprintf(format, args...) + "\n") }
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	_, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"ok":true`) {
		t.Fatalf("код не принят: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(strings.ToLower(text), "перезапустите") {
		t.Fatalf("слова успеха не зовут перезапускать разговоры: %s", text)
	}
	if strings.Contains(text, "GOOD") || strings.Contains(journal.String(), "GOOD") {
		t.Fatalf("код авторизации остался в ответе или журнале: %s | %s", text, journal.String())
	}
	if got := sent("kill-session"); got != 1 {
		t.Fatalf("одноразовая сессия входа не снята после успеха: kill-session %d", got)
	}
	// После исхода состояние входа забыто: код повторно принять некуда.
	resp, _ = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("код принят вне поднятого входа: %d", resp.StatusCode)
	}
}

// Вопрос доверия каталогу проходится сам: на машине, где клиент ещё не доверял
// дом, виджет доверия встаёт до REPL, съедая команду /login, а пункт на курсоре
// это выход. Вход отвечает пунктом доверия и подаёт /login заново.
func TestClientLoginTrustAskedAndPassed(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	if err := os.WriteFile(filepath.Join(d, "first"), []byte("trust"), 0o644); err != nil {
		t.Fatal(err)
	}
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём входа не прошёл вопрос доверия: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "https://claude.ai/oauth/authorize") {
		t.Fatalf("ссылка авторизации не приехала после доверия: %s", text)
	}
}

// Разгон клиента не получает нажатий вслепую: пока панель пуста, клиент не
// нарисовал ни REPL, ни виджета, и нажатия достались бы первому вставшему
// экрану, где Enter подтверждает пункт на курсоре. Вход ждёт отрисовки и лишь
// потом подаёт команду.
func TestClientLoginNoKeysIntoBlankPane(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	if err := os.WriteFile(filepath.Join(d, "first"), []byte("boot"), 0o644); err != nil {
		t.Fatal(err)
	}
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "https://") {
		t.Fatalf("подъём входа на разгоняющемся клиенте не прошёл: %d, %s", resp.StatusCode, text)
	}
	if _, err := os.Stat(filepath.Join(d, "early")); err == nil {
		t.Fatal("нажатия ушли в пустую панель до отрисовки клиента")
	}
}

// Код не принят: отказ назван словами, поле остаётся, сессия входа жива.
func TestClientLoginCodeRejected(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"WRONG"}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "код не принят") {
		t.Fatalf("отказ кода не назван словами: %d, %s", resp.StatusCode, text)
	}
	if got := sent("kill-session"); got != 0 {
		t.Fatalf("после неверного кода снята сессия входа: ввести другой нечем (%d)", got)
	}
	// Другой код в той же сессии доезжает до успеха.
	resp, _ = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повторный код в живой сессии не принят: %d", resp.StatusCode)
	}
}

// Ссылка не нашлась: отказ назван словами, сессия снята.
func TestClientLoginLinkMissingSaysWords(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	// Клиент выбирается, но ссылку не печатает: после выбора способа панель
	// возвращается на виджет.
	if err := os.WriteFile(filepath.Join(d, "pane-url"),
		[]byte(loginPaneOf("ask")), 0o644); err != nil {
		t.Fatal(err)
	}
	fastLoginWait(t, 40*time.Millisecond)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "не напечатал ссылку") {
		t.Fatalf("пропажа ссылки не названа словами: %d, %s", resp.StatusCode, text)
	}
	if got := sent("kill-session"); got != 1 {
		t.Fatalf("сессия без ссылки не снята: kill-session %d", got)
	}
}

// Сессия входа умерла на середине: отказ назван словами и не считается
// принятым кодом.
func TestClientLoginGoneMidWay(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	// Сессия умирает между ссылкой и кодом: файл стадии исчезает, снимок
	// панели отвечает ошибкой.
	if err := os.Remove(filepath.Join(d, "stage")); err != nil {
		t.Fatal(err)
	}
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "умерла") {
		t.Fatalf("смерть сессии входа не названа словами: %d, %s", resp.StatusCode, text)
	}
}

// Пустой код не уезжает в сессию вовсе.
func TestClientLoginEmptyCode(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	sent("send-keys")
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"  "}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("пустой код уехал в сессию: %d, %s", resp.StatusCode, text)
	}
	if got := sent("send-keys"); got != 0 {
		t.Fatalf("пустой код подался нажатиями: send-keys ещё %d", got)
	}
}

// Ссылка, разорванная переносом по ширине пейна, отдаётся целой. Фикстура
// несёт ссылку кусками без пробела на стыке, как её режет панель в 80 колонок:
// усечённый обрывок с телефона не открывается, а разбор молчит о подмене.
func TestClientLoginWrappedLinkJoins(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	full := "https://claude.ai/oauth/authorize?client_id=9dWzLkXA3287fGhQ2vNbT4cR1sY6uBe5HnJmZp" +
		"&redirect_uri=https%3A%2F%2Fclaude.ai%2Foauth%2Fcallback&state=abc123def456"
	wrapped := strings.Join([]string{
		"Please visit the following URL to log in:", "",
		"https://claude.ai/oauth/authorize?client_id=9dWzLkXA3\\",
		"287fGhQ2vNbT4cR1sY6uBe5HnJmZp&redirect_uri=https%3A%2F%\\",
		"2Fclaude.ai%2Foauth%2Fcallback&state=abc123def456", "",
		"Paste the authorization code below:", "", paneCursor + " ",
	}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-url"), []byte(wrapped), 0o644); err != nil {
		t.Fatal(err)
	}
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём входа: %d, %s", resp.StatusCode, text)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ входа не разобран: %s", text)
	}
	if got.URL != full {
		t.Fatalf("ссылка отдалась не целой: %s", got.URL)
	}
}

// Ссылка, порванная своими переводами строк клиента, отдаётся целой. Живой
// клиент режет ссылку сам по ширине пейна: это не мягкий перенос терминала,
// capture-pane их не клеит, и обрывки собирает разбор. Усечённый обрывок с
// телефона не открывается, а разбор молчал бы о подмене.
func TestClientLoginHardWrappedLinkJoins(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	full := "https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88" +
		"ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.co" +
		"m%2Foauth%2Fcode%2Fcallback&scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainf" +
		"erence+user%3Asessions%3Aclaude_code+user%3Amcp_servers+user%3Afile_upload&code_" +
		"challenge=15xLrGK-0V-FtkmtNIPsRLMJvVmBZgQnLA-BFDCqxyE&code_challenge_method=S256" +
		"&state=SN8he0RxdleeKwFkRqWs48b2_kWeqEliF5H2gKXHr4w"
	torn := strings.Join([]string{
		"Please visit the following URL to log in:", "",
		"https://claude.com/cai/oauth/authorize?code=true&client_id=9d1c250a-e61b-44d9-88",
		"ed-5944d1962f5e&response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.co",
		"m%2Foauth%2Fcode%2Fcallback&scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainf",
		"erence+user%3Asessions%3Aclaude_code+user%3Amcp_servers+user%3Afile_upload&code_",
		"challenge=15xLrGK-0V-FtkmtNIPsRLMJvVmBZgQnLA-BFDCqxyE&code_challenge_method=S256",
		"&state=SN8he0RxdleeKwFkRqWs48b2_kWeqEliF5H2gKXHr4w", "",
		"Hold Shift (Option in iTerm2, Fn in Terminal.app) while selecting to use",
		"your terminal's native copy", "", "Paste code here if prompted >", "",
		paneCursor + " ",
	}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-url"), []byte(torn), 0o644); err != nil {
		t.Fatal(err)
	}
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём входа: %d, %s", resp.StatusCode, text)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ входа не разобран: %s", text)
	}
	if got.URL != full {
		t.Fatalf("ссылка из обрывков не собрана: %s", got.URL)
	}
}

// Просроченный вход снимается фоновым кругом сам, без нового нажатия «Войти»:
// человек открыл ссылку и не вернулся, и сессия с живым клиентом не стоит
// бессрочно.
func TestClientLoginSweepDropsExpired(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("kill-session")
	base := e.s.now()
	e.s.now = func() time.Time { return base.Add(loginRunTTL + time.Minute) }
	e.s.loginSweep()
	if got := sent("kill-session"); got != 1 {
		t.Fatalf("просроченная сессия входа не снята уборкой: kill-session %d", got)
	}
	// Состояние забыто: следующий вход поднимает свежую сессию, а не конфликт.
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "https://") {
		t.Fatalf("после уборки вход не поднялся заново: %d, %s", resp.StatusCode, text)
	}
	if got := sent("new-session"); got != 1 {
		t.Fatalf("после уборки не поднята свежая сессия входа: new-session %d", got)
	}
}

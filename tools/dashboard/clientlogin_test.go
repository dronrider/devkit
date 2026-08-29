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
// ls называет и живую сессию входа с моментом рождения: по списку служба
// узнаёт сессии, оставшиеся после перезапуска, и подбирает свободное имя.
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
ls)
  printf 'goal-XR-9\n'
  [ -f "$D/sess" ] && cat "$D/sess";;
new-session)
  if [ -f "$D/first" ]; then cat "$D/first" >"$D/stage"; else echo repl >"$D/stage"; fi
  printf '%s|1|%s\n' "$4" "$(date +%s)" >"$D/sess";;
set-environment) printf '%s=%s\n' "$4" "$5" >"$D/env-${3#=}";;
show-environment)
  [ -f "$D/env-${3#=}" ] || exit 1
  grep "^$4=" "$D/env-${3#=}" || exit 1;;
kill-session)
  echo "${3#=}" >>"$D/killed"
  rm -f "$D/stage" "$D/sess" "$D/env-${3#=}";;
capture-pane)
  for a in "$@"; do
    [ "$prev" = "-t" ] && tgt="$a"
    prev="$a"
  done
  nm=${tgt#=}; nm=${nm%:}
  if [ -f "$D/pane-for-$nm" ]; then cat "$D/pane-for-$nm"; exit 0; fi
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

// Соседняя ссылка не приклеивается к найденной. Панель с двумя разными
// https-ссылками подряд без пустой строки между ними сливалась в кашу: любая
// строка из URL-знаков глоталась как обрывок без проверки родства. Свой
// адрес с полной схемой внутри обрывка не встречается, вложенные адреса
// клиент кодирует процентами, и вторая ссылка это граница, а не кусок первой.
func TestClientLoginSiblingLinkNotGlued(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	first := "https://claude.ai/oauth/authorize?client_id=test&state=abc123"
	pane := strings.Join([]string{
		"Please visit the following URL to log in:", "",
		first,
		"https://console.anthropic.com/oauth/authorize?client_id=other",
		"",
		"Paste code here if prompted >", "", paneCursor + " ",
	}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-url"), []byte(pane), 0o644); err != nil {
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
	if got.URL != first {
		t.Fatalf("соседняя ссылка приклеилась к найденной: %s", got.URL)
	}
}

// Строка-хэш следом за ссылкой не приклеивается. Хэш целиком состоит из
// URL-знаков, и тихое поглощение любой похожей строки носило бы в ссылку
// чужой кусок: человек получил бы адрес, который не открывается, без единого
// признака подмены. Обрывок узнаётся по колонке разрыва, а не по похожести.
func TestClientLoginHashAfterLinkNotGlued(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	first := "https://claude.ai/oauth/authorize?client_id=test&state=abc123"
	pane := strings.Join([]string{
		"Please visit the following URL to log in:", "",
		first,
		"d41d8cd98f00b204e980",
		"Paste code here if prompted >", "", paneCursor + " ",
	}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-url"), []byte(pane), 0o644); err != nil {
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
	if got.URL != first {
		t.Fatalf("строка-хэш приклеилась к ссылке: %s", got.URL)
	}
}

// Возврат с телефона застаёт вход живым. Срок считался десятью минутами от
// подъёма, а дорога с ссылкой ведёт на телефон: страница входа, пароль из
// менеджера, второй фактор из другого приложения. Человек с верным кодом
// возвращался на одиннадцатой минуте и получал отказ «вход ещё не поднят»,
// хотя ничего не бросал.
func TestClientLoginSurvivesMobileOAuth(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("kill-session")
	// Столько занимает вход на телефоне со вторым фактором.
	base := e.s.now()
	e.s.now = func() time.Time { return base.Add(10*time.Minute + 5*time.Second) }
	e.s.loginSweep()
	if got := sent("kill-session"); got != 0 {
		t.Fatalf("уборка сорвала вход, пока человек проходил его на телефоне: kill-session %d", got)
	}
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"ok":true`) {
		t.Fatalf("верный код не принят после входа с телефона: %d, %s", resp.StatusCode, text)
	}
}

// Срок входа считается от последнего признака жизни, а не от подъёма.
// Повторный запрос ссылки это признак жизни: человек вернулся к плашке, взял
// ссылку заново и провозился дольше срока от подъёма, а верный код доезжает
// живому входу, а не снятому за старость.
func TestClientLoginAliveFromLastTouch(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("kill-session")
	// Человек вернулся к плашке под конец срока и спросил ссылку снова.
	base := e.s.now()
	e.s.now = func() time.Time { return base.Add(loginRunTTL - 4*time.Minute) }
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "https://") {
		t.Fatalf("повторный экран входа не получил ссылку: %d, %s", resp.StatusCode, text)
	}
	// Дальше он провозился с кодом: срок от подъёма уже вышел, от последнего
	// признака жизни ещё нет.
	e.s.now = func() time.Time { return base.Add(loginRunTTL + 2*time.Minute) }
	e.s.loginSweep()
	if got := sent("kill-session"); got != 0 {
		t.Fatalf("уборка сорвала вход, о котором только что шла речь: kill-session %d", got)
	}
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"ok":true`) {
		t.Fatalf("верный код не принят после долгого возврата: %d, %s", resp.StatusCode, text)
	}
}

// Перезапуск службы не оставляет сессию входа сиротой. Состояние подъёма жило
// в памяти процесса и умерло вместе с ним, а tmux-сессия входа осталась на
// машине: следующий вход узнаёт её по панели со ссылкой вместо того, чтобы
// поднять соседнюю и оставить первую ничьей.
func TestClientLoginAdoptedAfterRestart(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	_, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if !strings.Contains(text, `"tmux":"login-1"`) {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("new-session")
	// Перезапуск: память процесса пуста, машина не тронута.
	e.s.mu.Lock()
	e.s.loginRun = nil
	e.s.mu.Unlock()
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход после перезапуска не отвечен: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"tmux":"login-1"`) {
		t.Fatalf("живая сессия входа не узнана, поднята соседняя: %s", text)
	}
	if got := sent("new-session"); got != 0 {
		t.Fatalf("после перезапуска поднята вторая сессия входа: new-session %d", got)
	}
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"ok":true`) {
		t.Fatalf("код не принят узнанной сессии входа: %d, %s", resp.StatusCode, text)
	}
}

// Брошенная сессия входа снимается и после перезапуска службы: учёт умер
// вместе с процессом, и узнавание различает ждущий вход от забытого по
// возрасту сессии, а не подбирает всё подряд.
func TestClientLoginOrphanSweptAfterRestart(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("kill-session")
	e.s.mu.Lock()
	e.s.loginRun = nil
	e.s.mu.Unlock()
	base := e.s.now()
	e.s.now = func() time.Time { return base.Add(loginRunTTL + time.Minute) }
	e.s.loginSweep()
	if got := sent("kill-session"); got != 1 {
		t.Fatalf("сирота входа не снят после перезапуска: kill-session %d", got)
	}
}

// Склейка обрывков разбирается по форме панели. Проверка собранного адреса
// (loginLinkCheck) чужую строку не ловит: приклеенный кусок даёт синтаксически
// годный адрес, просто не тот, и человек узнал бы о подмене, только не сумев
// открыть ссылку. Опора тут на колонку разрыва и на пустую строку, которой
// клиент отбивает конец ссылки.
func TestLoginLinkOfGlue(t *testing.T) {
	const torn = 40
	head := "https://claude.com/x?a=" + strings.Repeat("1", torn-len("https://claude.com/x?a="))
	body := strings.Repeat("2", torn)
	tail := strings.Repeat("3", 20)
	alien := strings.Repeat("9", torn)
	prompt := "Paste code here if prompted >"
	cases := []struct {
		name string
		pane []string
		want string
	}{
		{"живой вид панели: обрывки по колонке и короткий хвост за ними",
			[]string{"Use the url below:", "", head, body, tail, "", prompt},
			head + body + tail},
		{"соседняя ссылка не приклеивается",
			[]string{head, "https://console.anthropic.com/oauth?b=2", "", prompt},
			head},
		{"строка-хэш без пустой строки за ней не приклеивается",
			[]string{head, tail, prompt, "", "> "},
			head},
		{"чужая строка той же ширины без пустой строки за ней рвёт склейку",
			[]string{head, body, alien, prompt, "", "> "},
			""},
		{"нерваная ссылка отдаётся, даже когда следом сразу проза",
			[]string{head, prompt, "", "> "},
			head},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := loginLinkOf(strings.Join(c.pane, "\n")); got != c.want {
				t.Fatalf("разбор панели дал %q, жду %q", got, c.want)
			}
		})
	}
}

// Соседняя сессия не выдаётся за свой вход. Узнавание после перезапуска шло по
// имени login-N, возрасту и любому адресу в панели, и сессия помоложе с
// посторонней ссылкой забирала себе учёт, а настоящий вход снимался сиротой.
// Цена ошибки не в путанице: в узнанную сессию уходит код авторизации
// нажатиями, то есть одноразовый ключ от учётной записи уехал бы чужому.
func TestClientLoginStrangerNotAdopted(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, `"tmux":"login-1"`) {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("new-session")
	// Рядом встала посторонняя сессия того же вида: имя login-N, панель со
	// ссылкой, рождение позже нашего. Метки дашборда у неё нет.
	born, err := os.ReadFile(filepath.Join(d, "sess"))
	if err != nil {
		t.Fatal(err)
	}
	list := string(born) + fmt.Sprintf("login-2|1|%d\n", time.Now().Add(time.Minute).Unix())
	if err := os.WriteFile(filepath.Join(d, "sess"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	alien := strings.Join([]string{"Смотри сюда:", "",
		"https://example.invalid/oauth/authorize?client_id=stranger", "", "> "}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-for-login-2"), []byte(alien), 0o644); err != nil {
		t.Fatal(err)
	}
	// Перезапуск: память процесса пуста, машина не тронута.
	e.s.mu.Lock()
	e.s.loginRun = nil
	e.s.mu.Unlock()
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход после перезапуска не отвечен: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"tmux":"login-1"`) {
		t.Fatalf("узнана посторонняя сессия вместо своей: %s", text)
	}
	if strings.Contains(text, "example.invalid") {
		t.Fatalf("отдана посторонняя ссылка: %s", text)
	}
	if got := sent("new-session"); got != 0 {
		t.Fatalf("после перезапуска поднята вторая сессия входа: new-session %d", got)
	}
	killed, _ := os.ReadFile(filepath.Join(d, "killed"))
	if strings.Contains(string(killed), "login-2") {
		t.Fatalf("снята посторонняя сессия, до которой дашборду дела нет: %s", killed)
	}
	if strings.Contains(string(killed), "login-1") {
		t.Fatalf("своя сессия входа снята сиротой: %s", killed)
	}
}

// Код авторизации не подаётся в сессию, занявшую имя после нашей. Имя login-N
// держится в памяти с подъёма, и подача шла по нему без единой проверки: умри
// наша сессия, имя достанется соседу, и одноразовый ключ от учётной записи
// уедет нажатиями в чужую панель.
func TestClientLoginCodeRefusedToStranger(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, `"tmux":"login-1"`) {
		t.Fatalf("вход не поднялся: %s", text)
	}
	sent("send-keys")
	// Имя осталось, метка ушла вместе с нашей сессией: под login-1 теперь
	// стоит сосед.
	if err := os.Remove(filepath.Join(d, "env-login-1")); err != nil {
		t.Fatal(err)
	}
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("код подан в чужую сессию: %d, %s", resp.StatusCode, text)
	}
	if got := sent("send-keys"); got != 0 {
		t.Fatalf("нажатия ушли в чужую панель: send-keys %d", got)
	}
	killed, _ := os.ReadFile(filepath.Join(d, "killed"))
	if strings.Contains(string(killed), "login-1") {
		t.Fatalf("снята чужая сессия под нашим именем: %s", killed)
	}
	// Плашка после отказа поднимает вход заново, а не упирается в конфликт.
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "https://") {
		t.Fatalf("после отказа вход не поднялся заново: %d, %s", resp.StatusCode, text)
	}
}

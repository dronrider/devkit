package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
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
	case "oauth":
		// Живой вид отказа, снятый с клиента: поля кода нет, слов про код нет,
		// и по уходу поля разбор звал это успехом.
		return strings.Join([]string{
			"", strings.Repeat(paneFrame, 60), "  Login", "",
			"  OAuth error: Request failed with status code 400", "", "",
			"  Press Enter to retry.", "", "  Esc to cancel", "",
		}, "\n")
	case "stuck":
		// Экран входа стоит, а что случилось, по нему не прочесть.
		return strings.Join([]string{
			"", strings.Repeat(paneFrame, 60), "  Login", "", "", "",
			"  Esc to cancel", "",
		}, "\n")
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
	for _, stage := range []string{"boot", "repl", "init", "trust", "ask", "url", "ok", "again", "oauth", "stuck"} {
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
list-panes) echo 4242;;
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
      if [ -f "$D/after" ]; then cat "$D/after" >"$D/stage"
      else
        case "$last" in
        GOOD) echo ok >"$D/stage";;
        *) echo again >"$D/stage";;
        esac
      fi
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

// slowDownTmuxOnce тормозит ровно первый send-keys фикстуры tmux после
// fakeTmuxLogin на delay, дальше она отвечает как обычно. send-keys, а не
// первый вызов вообще: тот раньше уходил на new-session, поднимающий сессию
// ещё до отсчёта deadline у loginAwaitLink, и заминка там бюджет ожидания
// ссылки не трогала. Так проверяется заминка внешнего процесса под живой
// нагрузкой (DK-677) внутри самого опроса панели, а не постоянно медленная
// машина: реальная заминка тоже разовая, не на каждый вызов.
func slowDownTmuxOnce(t *testing.T, e *testEnv, d string, delay time.Duration) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.bin, scriptBody("tmux")))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(string(raw), "#!/bin/sh\n"), "\n")
	guard := fmt.Sprintf("if [ \"$1\" = send-keys ] && [ ! -f \"%s/slowed\" ]; then "+
		"sleep %g; touch \"%s/slowed\"; fi\n", d, delay.Seconds(), d)
	writeScript(t, e.bin, "tmux", guard+body)
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

// Строка-хэш следом за ссылкой в ссылку не попадает. Хэш целиком состоит из
// знаков адреса, и тихое поглощение похожей строки носило бы в ссылку чужой
// кусок: человек получил бы адрес, который не открывается, без единого
// признака подмены. Отдать вместо этого первую строку разбор тоже не вправе:
// та же форма бывает у ссылки, порванной ровно по колонке, и первая строка
// оказалась бы обрезанной ссылкой. Поэтому исход тут отказ со словами.
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
	// Деадлайн тот же, что у соседних тестов входа (DK-677): 300ms не
	// переживали разбор под живой нагрузкой прогона слияния, цепочка
	// send-keys/capture-pane фикстуры не успевала дойти до стадии url.
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("подъём отдал ссылку там, где форма не та: %d, %s", resp.StatusCode, text)
	}
	if strings.Contains(text, "d41d8cd98f00b204e980"+"&") {
		t.Fatalf("строка-хэш приклеилась к ссылке: %s", text)
	}
	if !strings.Contains(text, "разобрать не вышло") {
		t.Fatalf("отказ разбора не назван словами: %s", text)
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

// Склейка обрывков разбирается узкой формой, а не догадкой. Родства у обрывка
// нет: строка из знаков адреса той же ширины выглядит продолжением, чем бы она
// ни была, и проверка собранного адреса тут бессильна, у каши один хост и одна
// схема. Три захода ревью по этому месту это подтвердили. Поэтому принимается
// одна форма (обрывки ровно по колонке, короткий хвост, пустая строка), а всё
// прочее отдаёт пустую ссылку и причину словами.
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
		why  bool
	}{
		{"живой вид панели: обрывки по колонке, короткий хвост, пустая строка",
			[]string{"Use the url below:", "", head, body, tail, "", prompt},
			head + body + tail, false},
		{"соседняя ссылка продолжением не считается",
			[]string{head, "https://console.anthropic.com/oauth?b=2", "", prompt},
			head, false},
		{"нерваная ссылка отдаётся, даже когда следом сразу проза",
			[]string{head, prompt, "", "> "},
			head, false},
		{"чужая строка той же ширины без пустой строки за ней",
			[]string{head, body, alien, prompt, "", "> "},
			"", true},
		{"чужая строка той же ширины, за которой пустая строка стоит",
			[]string{head, body, alien, "", prompt},
			"", true},
		{"ссылка легла по колонке ровно, короткого хвоста нет",
			[]string{head, body, "", prompt},
			"", true},
		{"строка-хэш на месте хвоста, а пустой строки за ней нет",
			[]string{head, tail, prompt, "", "> "},
			"", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := loginLinkOf(strings.Join(c.pane, "\n"))
			if got != c.want {
				t.Fatalf("разбор панели дал %q, жду %q", got, c.want)
			}
			if (why != "") != c.why {
				t.Fatalf("причина отказа %q при ожидании %v", why, c.why)
			}
		})
	}
}

// Отказ разбора едет человеку словами и кадром панели. Догадка про родство
// обрывков снята нарочно, и цена решения это отказ там, где форма не та.
// Молча отдать первый обрывок нельзя: неоткрывающаяся ссылка о подмене
// молчит, а человеку по кадру видно, что клиент нарисовал на самом деле.
func TestClientLoginLinkRefusalSaysFrame(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	first := "https://claude.ai/oauth/authorize?client_id=test&state=abc123"
	pane := strings.Join([]string{
		"Please visit the following URL to log in:", "",
		first,
		strings.Repeat("7", len(first)),
		"",
		"Paste code here if prompted >", "", paneCursor + " ",
	}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-url"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	// Деадлайн тот же, что у соседних тестов входа (DK-677): см. комментарий
	// у TestClientLoginHashAfterLinkNotGlued.
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("каша отдана за ссылку: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "разобрать не вышло") {
		t.Fatalf("отказ разбора не назван словами: %s", text)
	}
	if !strings.Contains(text, "Paste code here") {
		t.Fatalf("кадра панели в отказе нет: %s", text)
	}
}

// Заминка внешнего процесса на 300ms не переживала: единственная задержка в
// половину секунды (сравнимая с загрузкой машины при слиянии, а не с холодным
// стартом macOS, тот гасит общий переходник DK-649) уводила разбор в общий
// «клиент не напечатал ссылку» вместо разобранной причины, хотя панель со
// ссылкой в итоге дошла (DK-677).
func TestClientLoginLinkRefusalSurvivesASlowTmuxCall(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	first := "https://claude.ai/oauth/authorize?client_id=test&state=abc123"
	pane := strings.Join([]string{
		"Please visit the following URL to log in:", "",
		first,
		strings.Repeat("7", len(first)),
		"",
		"Paste code here if prompted >", "", paneCursor + " ",
	}, "\n")
	if err := os.WriteFile(filepath.Join(d, "pane-url"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}
	slowDownTmuxOnce(t, e, d, 500*time.Millisecond)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("каша отдана за ссылку: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "разобрать не вышло") {
		t.Fatalf("одна заминка внешнего процесса увела разбор в общий таймаут: %s", text)
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

// Отказ клиента не выдаётся за успех. Исход кода читался одним признаком:
// ушло поле кода, значит вход сделан. Живой отказ клиента поле тоже убирает,
// и дашборд докладывал «вход сделан», снимал сессию и оставлял человека со
// свежей ссылкой вместо слов о том, что случилось. Проверено живьём на
// экземпляре 7131: негодный код дал 200 и «вход сделан», а клиент в это время
// писал «OAuth error: Request failed with status code 400».
func TestClientLoginCodeErrorNotSuccess(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	if err := os.WriteFile(filepath.Join(d, "after"), []byte("oauth\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"nosuchcode"}`)
	if resp.StatusCode == http.StatusOK || strings.Contains(text, `"ok":true`) {
		t.Fatalf("отказ клиента выдан за сделанный вход: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "status code 400") {
		t.Fatalf("слова клиента про отказ до человека не доехали: %s", text)
	}
}

// Неузнанный исход тоже не успех. Клиент мог остаться на своём экране входа с
// чем угодно на нём, и назвать это входом дашборду не по чему. Тогда он
// говорит последними словами клиента и сессию не снимает: свидетельство нужно
// человеку целым.
func TestClientLoginCodeStuckNotSuccess(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	if _, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}"); !strings.Contains(text, "https://") {
		t.Fatalf("вход не поднялся: %s", text)
	}
	if err := os.WriteFile(filepath.Join(d, "after"), []byte("stuck\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"nosuchcode"}`)
	if resp.StatusCode == http.StatusOK || strings.Contains(text, `"ok":true`) {
		t.Fatalf("неузнанный исход выдан за сделанный вход: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "Вход не сделан") {
		t.Fatalf("исход назван невнятно: %s", text)
	}
	killed, _ := os.ReadFile(filepath.Join(d, "killed"))
	if strings.Contains(string(killed), "login-1") {
		t.Fatalf("сессия снята вместе со свидетельством: %s", killed)
	}
}

// Откуда пришёл запрос, оттуда и дорога входа. Код руками нужен ровно тогда,
// когда браузер и клиент живут на разных машинах, и одного адреса соединения
// для этого мало: заход извне идёт через клиента шар на ту же машину, и адрес
// у него петлевой (README пакета, «Заход извне»). Телефон получил бы режим без
// кода и ссылку на localhost, которого у него нет.
func TestLoginFromMachine(t *testing.T) {
	cases := []struct {
		name string
		addr string
		host string
		head map[string]string
		want bool
	}{
		{"браузер машины по петле", "127.0.0.1:54321", "127.0.0.1:7112", nil, true},
		{"он же по имени localhost", "[::1]:54321", "localhost:7112", nil, true},
		{"браузер соседа по сети", "192.168.1.14:51000", "192.168.1.9:7112", nil, false},
		{"телефон через фронт входа", "127.0.0.1:54321", "127.0.0.1:7112",
			map[string]string{"X-Forwarded-Host": "dash.example"}, false},
		{"он же с цепочкой посредников", "127.0.0.1:54321", "127.0.0.1:7112",
			map[string]string{"X-Forwarded-For": "203.0.113.7"}, false},
		{"посредник старого образца", "127.0.0.1:54321", "127.0.0.1:7112",
			map[string]string{"Forwarded": "for=203.0.113.7;host=dash.example"}, false},
		{"посредник переписал только имя", "127.0.0.1:54321", "dash.example", nil, false},
		{"адреса нет вовсе", "", "", nil, false},
	}
	for _, c := range cases {
		r := &http.Request{RemoteAddr: c.addr, Host: c.host, Header: http.Header{}}
		for k, v := range c.head {
			r.Header.Set(k, v)
		}
		if got := loginFromMachine(r); got != c.want {
			t.Fatalf("%s: признан %v, жду %v", c.name, got, c.want)
		}
	}
}

// Петлевая ссылка это та же ссылка с другим адресом возврата. Всё, чем вход
// связан (client_id, state, code_challenge), клиентово и остаётся нетронутым:
// подменяется один адрес, и признак ручного кода снимается.
func TestLoginLoopURL(t *testing.T) {
	raw := "https://claude.com/cai/oauth/authorize?code=true&client_id=abc&" +
		"response_type=code&redirect_uri=https%3A%2F%2Fplatform.claude.com%2Foauth%2Fcode%2Fcallback&" +
		"code_challenge=zzz&code_challenge_method=S256&state=sss"
	got := loginLoopURL(raw, 53535)
	u, err := neturl.Parse(got)
	if err != nil {
		t.Fatalf("петлевая ссылка не разобралась: %s", got)
	}
	q := u.Query()
	if q.Get("redirect_uri") != "http://localhost:53535/callback" {
		t.Fatalf("адрес возврата не переложен на клиент: %s", q.Get("redirect_uri"))
	}
	if q.Has("code") {
		t.Fatalf("признак ручного кода остался в петлевой ссылке: %s", got)
	}
	for _, k := range []string{"client_id", "state", "code_challenge", "code_challenge_method"} {
		if q.Get(k) == "" {
			t.Fatalf("петлевая ссылка потеряла %s: %s", k, got)
		}
	}
	if loginLoopURL(raw, 0) != "" {
		t.Fatalf("без петлевого порта ссылка обязана быть пустой")
	}
}

// Вход с самой машины идёт без кода. Браузер тут живёт на той же машине, что
// клиент, клиент держит петлевой слушатель и ловит код сам, и поле кода
// человеку показывать незачем: шаг остаётся один, открыть ссылку.
func TestClientLoginLocalWayNoCode(t *testing.T) {
	e := newTestEnv(t)
	fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	was := loginLsof
	loginLsof = []string{"lsof"}
	t.Cleanup(func() { loginLsof = was })
	writeScript(t, e.bin, "lsof", `echo "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME"
echo "2.1.251 4242 rider 19u IPv4 0x1b31 0t0 TCP 127.0.0.1:53535 (LISTEN)"`)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход не поднялся: %d, %s", resp.StatusCode, text)
	}
	var got struct {
		Way string `json:"way"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ входа не разобран: %s", text)
	}
	if got.Way != "local" {
		t.Fatalf("вход с самой машины пошёл дорогой кода: %s", text)
	}
	if !strings.Contains(got.URL, "localhost%3A53535%2Fcallback") &&
		!strings.Contains(got.URL, "localhost:53535/callback") {
		t.Fatalf("ссылка не ведёт возврат в клиент: %s", got.URL)
	}
}

// Вход через фронт входа идёт кодом. Это боевой путь снаружи: телефон приходит
// по HTTPS на фронт, тот через relay достаёт клиента шар, а шар проксирует на
// петлевой порт машины (README пакета, «Заход извне»). Адрес соединения тут
// петлевой, и по нему одному дашборд прятал поле кода и вёл человека по ссылке
// на localhost, которого на телефоне нет.
func TestClientLoginProxiedWayKeepsCode(t *testing.T) {
	e := newTestEnv(t)
	fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	was := loginLsof
	loginLsof = []string{"lsof"}
	t.Cleanup(func() { loginLsof = was })
	writeScript(t, e.bin, "lsof", `echo "2.1.251 4242 rider 19u IPv4 0x1b31 0t0 TCP 127.0.0.1:53535 (LISTEN)"`)
	c := e.loggedClient(t)
	req, err := http.NewRequest(http.MethodPost,
		e.srv.URL+"/api/projects/demo/chats/login", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	// Так заголовки кладёт клиент шар: внешнее имя в X-Forwarded-Host, Origin
	// по нему же, Host переписан на апстрим.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Host", "dash.example")
	req.Header.Set("Origin", "https://dash.example")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход через фронт не поднялся: %d, %s", resp.StatusCode, body)
	}
	var got struct {
		Way string `json:"way"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("ответ входа не разобран: %s", body)
	}
	if got.Way != "code" {
		t.Fatalf("заход через фронт пошёл дорогой без кода: %s", body)
	}
	if strings.Contains(got.URL, "localhost") || strings.Contains(got.URL, "127.0.0.1") {
		t.Fatalf("телефону отдана ссылка на петлевой адрес машины: %s", got.URL)
	}
}

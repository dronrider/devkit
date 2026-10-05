package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ключ выбора модели двухчастный «модель+подписка» (DK-1281): имя повторяется
// у двух подписок, и разговор поднимается квотой выбранной строки. Стенды тут
// говорят ручками и сырыми файлами памяти записи, без новых полей и сигнатур:
// такой тест собирается и на старом коде, и краснота его доказуема regcheck.

// pairLayout это раскладка с честным повтором имени: «sonnet» домашняя ступень
// у двух подписок сразу, и владельца по одному имени не найти. «третьяtest»
// стоит второй нарочно: порядок харнесов зовёт владельцем первую, и без пары
// разговор поднимался бы чужой квотой.
const pairLayout = `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
     "models": [{"tier": "base", "model": "sonnet"}, {"tier": "pro", "model": "opus"}]},
    {"name": "третьяtest", "enabled": true, "default": false, "bin": "клиент-3",
     "models": [{"tier": "base", "model": "sonnet"}]}
  ]
}`

// pairEnv поднимает окружение с повтором имени и журналом tmux: по журналу
// видно, чьей обвязкой поднялся разговор, а ls без сервера делает всякую
// записанную сессию кончившейся, чтобы реплика шла резюмом.
func pairEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c := chatEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) `+tmuxNoServer+`;;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, pairLayout)
	return e, c, tmuxLog
}

// Первая реплика поднимает разговор квотой выбранной строки повтора: обёртка
// agentctl exec называет подписку, чьими парами окружения платится клиент.
// Выбор без подписки живёт по-старому: владельца ищет порядок харнесов, и
// домашняя подписка по умолчанию обходится без обёртки вовсе.
func TestChatRaiseOnNamedHarness(t *testing.T) {
	e, c, tmuxLog := pairEnv(t)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "привет", "model": "sonnet", "harness": "третьяtest"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём на названной подписке: %d %s", resp.StatusCode, body(t, resp))
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "exec --harness 'третьяtest'") {
		t.Fatalf("разговор поднялся не названной квотой: %s", log)
	}
	if !strings.Contains(log, "--model 'sonnet'") {
		t.Fatalf("подъём не назвал выбранную модель: %s", log)
	}

	was := len(log)
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "ещё раз", "model": "sonnet"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём без подписки: %d %s", resp.StatusCode, body(t, resp))
	}
	tail := readFile(t, tmuxLog)[was:]
	if strings.Contains(tail, "exec --harness") {
		t.Fatalf("домашняя подписка поднялась с чужой обёрткой: %s", tail)
	}
	if !strings.Contains(tail, "--model 'sonnet'") {
		t.Fatalf("подъём без подписки не назвал модель: %s", tail)
	}
}

// Продолжение кончившегося разговора берёт квоту из памяти записи: резюм
// зовёт обёртку сохранённой подписки, а не первой, у которой имя домашнее.
// Память пишется сырым файлом, каким её пишет сам сервер, чтобы стенд собрался
// и на коде без поля подписки.
func TestChatResumeRaisesSavedPair(t *testing.T) {
	e, c, tmuxLog := pairEnv(t)
	sid := "cccc1281-1111-4111-8111-111111111111"
	name := "chat-1281-1"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-time.Minute))
	writeBinds(t, e.home, "2026-10-05T00:35:12 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+
		" транскрипт "+standTranscript(e.home, sid)+" источник заказ повод startup tmux "+name+"\n")
	if err := os.MkdirAll(chatStoreDir(e.home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatStoreDir(e.home), sid+".json"),
		[]byte(`{"model": "sonnet", "harness": "третьяtest"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в кончившийся разговор: %d %s", resp.StatusCode, body(t, resp))
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "exec --harness 'третьяtest'") {
		t.Fatalf("резюм поднялся не квотой сохранённого выбора: %s", log)
	}
}

// Смена модели пишет в память записи пару целиком: разделитель ленты называет
// квоту, ведь смена бывает сменой квоты при том же имени, а повтор той же пары
// второго разделителя не заводит.
func TestChatModelKeepsPair(t *testing.T) {
	e, c, _ := pairEnv(t)
	sid := "aaaa-1111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/model"
	resp := doReq(t, c, "POST", at, `{"model":"sonnet","harness":"третьяtest"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("смена модели с подпиской: %d %s", resp.StatusCode, body(t, resp))
	}
	var marks []string
	for _, it := range saidFeed(t, c, e, sid) {
		if it.Role == roleMark {
			marks = append(marks, it.Text)
		}
	}
	if len(marks) != 1 || marks[0] != "модель изменена: opus -> sonnet (третьяtest)" {
		t.Fatalf("разделитель не назвал квоту выбранной строки: %q", marks)
	}
	raw, err := os.ReadFile(filepath.Join(chatStoreDir(e.home), sid+".json"))
	if err != nil {
		t.Fatal(err)
	}
	// Числа и логические поля записи мешают строгому разбору, а стенду нужны
	// только строки выбора.
	var kept map[string]any
	if err := json.Unmarshal(raw, &kept); err != nil {
		t.Fatal(err)
	}
	if kept["model"] != "sonnet" || kept["harness"] != "третьяtest" {
		t.Fatalf("память записи не держит пару: %s", raw)
	}

	// Повтор той же пары это не смена: рубеж остаётся один.
	if resp := doReq(t, c, "POST", at, `{"model":"sonnet","harness":"третьяtest"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("повтор выбора: %d", resp.StatusCode)
	}
	again := 0
	for _, it := range saidFeed(t, c, e, sid) {
		if it.Role == roleMark {
			again++
		}
	}
	if again != 1 {
		t.Fatalf("повтор пары завёл второй разделитель: %d", again)
	}
}

// Рассохшийся список моделей честно отказывает: вход сверяет пару с раскладкой
// подписок, и выбор с чужой ступенью не пишется в память молча, а называет
// причину словами.
func TestChatModelRefusesStalePair(t *testing.T) {
	e, c, _ := pairEnv(t)
	sid := "aaaa-1111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model",
		`{"model":"opus","harness":"третьяtest"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("чужая ступень подписки: %d %s, жду 409", resp.StatusCode, body(t, resp))
	}
	if !strings.Contains(body(t, resp), "не ступень подписки третьяtest") {
		t.Fatalf("отказ не назвал причину: %s", body(t, resp))
	}
}

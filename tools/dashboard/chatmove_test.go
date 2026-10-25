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

// Перевод разговора между подписками (DK-1455): смена модели живого чата на
// подписку чужого харнеса продолжала резюм со старым sid, транскрипт оставался
// в дереве писателя, целевой клиент его не находил, и разговор умирал молча.
// Стенды ниже прогоняют весь путь: переключатель модели, реплика резюмом и
// продолжение работы, и все смотрят файлы на диске, а не слово сервера.

// moveLayout это раскладка с повтором имени и домом у второй подписки: у
// «sonnet» два владельца, и разговор со сменой модели уезжает в дерево
// выбранной строки, чьи журналы лежат в её доме, а не в ~/.claude (DK-362).
func moveLayout(home string) string {
	return `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
     "models": [{"tier": "base", "model": "sonnet"}]},
    {"name": "третьяtest", "enabled": true, "default": false, "bin": "клиент-3",
     "home": "` + filepath.Join(home, "h3") + `", "env": ["CLAUDE_CONFIG_DIR"],
     "models": [{"tier": "base", "model": "sonnet"}]}
  ]
}`
}

// moveEnv поднимает окружение перевода: журналируемый tmux без живых сессий
// (всякая записанная сессия кончилась, и реплика идёт резюмом), раскладка с
// домом второй подписки и болванки клиентов обеих сторон.
func moveEnv(t *testing.T) (*testEnv, *http.Client, string, string) {
	t.Helper()
	e, c := chatEnv(t)
	t.Setenv("PATH", e.bin+string(os.PathListSeparator)+"/bin")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) `+tmuxNoServer+`;;
esac
exit 0`)
	writeAgentctlFake(t, e.bin, moveLayout(e.home))
	writeClientFakes(t, e.bin, "клиент-1", "клиент-3")
	return e, c, tmuxLog, filepath.Join(e.home, "h3", "projects")
}

// moveCase кладёт кончившийся разговор первой подписки с сайдкарами: без
// каталога <sid>/ перевод был бы неполным, субагенты живут в нём (DK-900).
// task называет привязку реестра, прочерк значит разговор без задачи.
func moveCase(t *testing.T, e *testEnv, sid, task string) string {
	t.Helper()
	src := writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-time.Minute))
	side := filepath.Join(filepath.Dir(src), sid, "subagents")
	if err := os.MkdirAll(side, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(side, "sub-1.jsonl"), []byte(`{"say":"хвост захода"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBinds(t, e.home, "2026-10-25T10:00:00 сессия "+sid+" задача "+task+" проект demo дерево "+e.proj+
		" транскрипт "+standTranscript(e.home, sid)+" источник заказ повод startup tmux chat-9\n")
	return src
}

// switchModel переводит разговор на вторую подписку самим переключателем:
// поле model записи он правит уже сейчас, и стенд не пишет запись руками.
func switchModel(t *testing.T, c *http.Client, e *testEnv, sid string) {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model",
		`{"model":"sonnet","harness":"третьяtest"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("смена модели на чужую подписку: %d %s", resp.StatusCode, body(t, resp))
	}
}

// Реплика в кончившийся разговор после смены модели поднимает резюм целевой
// подпиской, а транскрипт и сайдкары переезжают в её дерево до подъёма:
// оригинал не остаётся, и список не получает второй строки разговора.
func TestChatSayMovesTranscriptToTargetHarness(t *testing.T) {
	e, c, tmuxLog, h3root := moveEnv(t)
	sid := "dddd1455-1111-4111-8111-111111111111"
	src := moveCase(t, e, sid, "-")
	switchModel(t, c, e, sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в переведённый разговор: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `"way":"resume"`) {
		t.Fatalf("переведённый разговор продолжен не резюмом: %s", got)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("оригинал транскрипта остался в дереве писателя: %v", err)
	}
	dst := filepath.Join(h3root, claudeDirName(e.proj), sid+".jsonl")
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("транскрипт не переехал в дерево подписки подъёма: %v", err)
	}
	if !strings.Contains(string(data), "работа идёт") {
		t.Fatalf("в цель приехал не тот разговор: %s", data)
	}
	if _, err := os.Stat(filepath.Join(h3root, claudeDirName(e.proj), sid, "subagents", "sub-1.jsonl")); err != nil {
		t.Fatalf("сайдкары не переехали вместе с транскриптом: %v", err)
	}
	said := readFile(t, tmuxLog)
	if !strings.Contains(said, "exec --harness 'третьяtest'") {
		t.Fatalf("резюм поднялся не подпиской выбора: %s", said)
	}
	if !strings.Contains(said, "--resume '"+sid+"'") {
		t.Fatalf("резюм потерял сессию разговора: %s", said)
	}
	// Одна строка разговора в списке: переезд, а не копия, и подписка строки
	// сменилась на владельца журналов.
	list := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?days=0", "")
	var rows struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, list)), &rows); err != nil {
		t.Fatal(err)
	}
	same := 0
	for _, row := range rows.Chats {
		if row.ID == sid {
			same++
			if row.Harness != "третьяtest" {
				t.Fatalf("строка разговора не назвала подписку журналов: %+v", row)
			}
		}
	}
	if same != 1 {
		t.Fatalf("разговор стоит в списке не одной строкой: %d", same)
	}
}

// Неудача перевода не убивает разговор: подъём откатывается на свежую сессию
// с подкладкой состояния (LLD DK-1422, решение 5), а причина называется в
// ленте. Целевое дерево, занятое файлом, изображает отказ переезда.
func TestChatSayTranslateFailureFallsFresh(t *testing.T) {
	e, c, tmuxLog, h3root := moveEnv(t)
	sid := "eeee1455-1111-4111-8111-111111111111"
	src := moveCase(t, e, sid, "-")
	if err := os.MkdirAll(filepath.Dir(h3root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h3root, []byte("занято"), 0o644); err != nil {
		t.Fatal(err)
	}
	switchModel(t, c, e, sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика при сорванном переводе: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `"way":"fresh"`) {
		t.Fatalf("сорванный перевод не откатился на свежую сессию: %s", got)
	}
	if said := readFile(t, tmuxLog); strings.Contains(said, "--resume") {
		t.Fatalf("сорванный перевод всё-таки поднял резюм: %s", said)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("транскрипт пропал при сорванном переводе: %v", err)
	}
	var marks []string
	for _, it := range saidFeed(t, c, e, sid) {
		if it.Role == roleMark {
			marks = append(marks, it.Text)
		}
	}
	found := false
	for _, m := range marks {
		if strings.Contains(m, "перевод разговора в дерево подписки подъёма не удался") {
			found = true
		}
	}
	if !found {
		t.Fatalf("лента не назвала причину свежей сессии: %q", marks)
	}
}

// Продолжение работы задачи переводит разговор той же дорогой: смена модели
// главе записи уезжает резюмом целевой подписки с перенесённым транскриптом.
func TestContinueMovesTranscriptToTargetHarness(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeClientFakes(t, e.bin, "клиент-1", "клиент-3")
	writeAgentctlFake(t, e.bin, moveLayout(e.home))
	writeTaskRunFake(t, filepath.Dir(e.proj))
	useHeadHome(t, e)
	sid := "ffff1455-1111-4111-8111-111111111111"
	src := moveCase(t, e, sid, "XR-004")
	switchModel(t, c, e, sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/continue", ``)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("продолжение переведённого разговора: %d %s", resp.StatusCode, got)
	}
	if !strings.Contains(got, `"way":"resume"`) {
		t.Fatalf("продолжение переведённого разговора пошло не резюмом: %s", got)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("оригинал транскрипта остался в дереве писателя: %v", err)
	}
	h3root := filepath.Join(e.home, "h3", "projects")
	if _, err := os.Stat(filepath.Join(h3root, claudeDirName(e.proj), sid+".jsonl")); err != nil {
		t.Fatalf("транскрипт не переехал в дерево подписки продолжения: %v", err)
	}
	said := readFile(t, tmuxLog)
	if !strings.Contains(said, "exec --harness 'третьяtest'") || !strings.Contains(said, "--resume '"+sid+"'") {
		t.Fatalf("продолжение поднято не подпиской выбора и не резюмом: %s", said)
	}
}

// tmuxDeadFake это tmux после смерти переведённого резюма: опрос сервера
// успешен и пуст, сессий нет, панель мёртвого окна не отдаёт. Все вызовы
// пишутся в журнал, и смена видна в нём целиком.
func tmuxDeadFake(t *testing.T, e *testEnv, log string) {
	t.Helper()
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+log+`"
case "$1" in
capture-pane) exit 1;;
esac
exit 0`)
}

// newSessionLines собирает из журнала tmux вызовы подъёма окон: их число
// отличает смену умершего резюма от повторного резюма.
func newSessionLines(t *testing.T, log string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readFile(t, log), "\n") {
		if strings.Contains(line, "new-session") {
			out = append(out, line)
		}
	}
	return out
}

// movedMarks считает в ленте разговора строки о поднятии смены.
func movedMarks(t *testing.T, home, key, word string) int {
	t.Helper()
	n := 0
	for _, m := range saidMarks(t, home, key) {
		if strings.Contains(m, word) {
			n++
		}
	}
	return n
}

// Мгновенная смерть переведённого резюма поднимает смену (DoD): целевой клиент
// не прочитал перенесённый транскрипт и вышел, а разговор продолжается свежей
// сессией, и реплика человека уезжает в неё снова. Смена, умершая тоже,
// второй смены не поднимает: одна смерть одну смену и зовёт.
func TestChatMovedResumeDeathLiftsFresh(t *testing.T) {
	e, c, tmuxLog, h3root := moveEnv(t)
	sid := "dead1455-1111-4111-8111-111111111111"
	src := moveCase(t, e, sid, "-")
	switchModel(t, c, e, sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай с места обрыва"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в переведённый разговор: %d %s", resp.StatusCode, body(t, resp))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("оригинал транскрипта остался в дереве писателя: %v", err)
	}
	tmuxDeadFake(t, e, tmuxLog)
	e.s.chatWatchTick()
	if n := movedMarks(t, e.home, "sess-"+sid, "резюм умер, не прочитав перенесённый транскрипт"); n != 1 {
		t.Fatalf("смена после смерти переведённого резюма не названа в ленте: %q",
			saidMarks(t, e.home, "sess-"+sid))
	}
	lines := newSessionLines(t, tmuxLog)
	if len(lines) != 2 {
		t.Fatalf("окон поднято не два: %q", lines)
	}
	if strings.Contains(lines[1], "--resume") || !strings.Contains(lines[1], "продолжай с места обрыва") {
		t.Fatalf("смена поднята не свежей сессией с репликой человека: %s", lines[1])
	}
	if _, err := os.Stat(filepath.Join(h3root, claudeDirName(e.proj), sid+".jsonl")); err != nil {
		t.Fatalf("смерть тронула перенесённый транскрипт: %v", err)
	}
	e.s.chatWatchTick()
	if got := newSessionLines(t, tmuxLog); len(got) != 2 {
		t.Fatalf("смерть смены подняла вторую смену: %q", got)
	}
	if n := movedMarks(t, e.home, "sess-"+sid, "поднят свежей сессией"); n != 1 {
		t.Fatalf("лента повторила смену: %q", saidMarks(t, e.home, "sess-"+sid))
	}
}

// Сорванная смена не молчит: tmux, не поднявший окно, отдаёт причину строкой
// в ленту разговора, и человек знает, почему разговор стоит.
func TestMovedReliftFailureSaysWhy(t *testing.T) {
	e, c, tmuxLog, _ := moveEnv(t)
	sid := "fail1455-1111-4111-8111-111111111111"
	moveCase(t, e, sid, "-")
	switchModel(t, c, e, sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в переведённый разговор: %d %s", resp.StatusCode, body(t, resp))
	}
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
new-session) exit 1;;
capture-pane) exit 1;;
esac
exit 0`)
	e.s.chatWatchTick()
	found := false
	for _, m := range saidMarks(t, e.home, "sess-"+sid) {
		if strings.Contains(m, "переведённый резюм умер, смену поднять не удалось") {
			found = true
		}
	}
	if !found {
		t.Fatalf("сорванная смена не назвала причину: %q", saidMarks(t, e.home, "sess-"+sid))
	}
}

// Продолжение работы умирает той же смертью, и смену ему поднимает заказ
// продолжения: свежая сессия с подкладкой состояния, без резюма.
func TestContinueMovedDeathLiftsFresh(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeClientFakes(t, e.bin, "клиент-1", "клиент-3")
	writeAgentctlFake(t, e.bin, moveLayout(e.home))
	writeTaskRunFake(t, filepath.Dir(e.proj))
	useHeadHome(t, e)
	sid := "cead1455-1111-4111-8111-111111111111"
	src := moveCase(t, e, sid, "XR-004")
	switchModel(t, c, e, sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/continue", ``)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body(t, resp), `"way":"resume"`) {
		t.Fatalf("продолжение переведённого разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("оригинал транскрипта остался в дереве писателя: %v", err)
	}
	tmuxDeadFake(t, e, tmuxLog)
	e.s.chatWatchTick()
	if n := movedMarks(t, e.home, "sess-"+sid, "продолжение поднято свежей сессией"); n != 1 {
		t.Fatalf("смена продолжения не названа в ленте: %q", saidMarks(t, e.home, "sess-"+sid))
	}
	lines := newSessionLines(t, tmuxLog)
	if len(lines) != 2 {
		t.Fatalf("окон поднято не два: %q", lines)
	}
	if strings.Contains(lines[1], "--resume") || !strings.Contains(lines[1], "Продолжай работу по XR-004") {
		t.Fatalf("смена продолжения поднята не своим заказом: %s", lines[1])
	}
	h3root := filepath.Join(e.home, "h3", "projects")
	if _, err := os.Stat(filepath.Join(h3root, claudeDirName(e.proj), sid+".jsonl")); err != nil {
		t.Fatalf("смерть тронула перенесённый транскрипт: %v", err)
	}
}

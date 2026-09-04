package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Чужое окно под своим именем (DK-673). Имя tmux-сессии попадает в реестр из
// переменной DEVKIT_TMUX, а её наследует всякий, кто поднялся изнутри окна:
// печатный `claude -p --resume` чужой сессии записал этим именем себя, и
// хозяином имени стал мёртвый разговор. Реплика из его карточки уехала
// клавишами в окно диспетчера цели, уборка того разговора сняла бы его живую
// работу целиком, а панель всё это время рисовала мёртвый чат живым.
//
// Сверка хозяина по реестру тут не помогает: испорченная запись принадлежит
// тому самому разговору, и сверка её пропускает. Поэтому слово о том, кто
// сейчас идёт в окне, берётся у самих живых клиентов: реестр клиента называет
// tmux-сессию, в которой процесс поднят, и живёт эта запись, пока жив процесс.

// heldEnv поднимает разговор sid, чья запись реестра называет окно chat-9, и
// сажает в это же окно живой разговор alien. tmuxLog собирает всё, что дашборд
// сказал tmux: по нему видно, поехали ли клавиши.
func heldEnv(t *testing.T, sid, alien string) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, saidLine("работа идёт", time.Now().Add(-time.Hour)),
		time.Now().Add(-time.Minute))
	writeBinds(t, e.home, "2026-08-31T21:09:00 сессия "+sid+
		" задача - проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
		"источник заказ повод startup tmux chat-9\n")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) printf 'chat-9\t1\t1754770421\n';;
capture-pane) printf '';;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")
	writePeerWindow(t, e.home, alien, "chat-9")
	return e, c, tmuxLog
}

// writePeerWindow кладёт запись живого клиента, назвавшего своё окно. Поле
// tmux у клиента идёт с окном и панелью, и разбор этого хвоста тоже под
// проверкой. Pid берётся свой: мёртвую запись реестр клиента отбрасывает, и
// без живого процесса собеседника не вышло бы вовсе.
func writePeerWindow(t *testing.T, home, sid, tmux string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	rec := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"devkit-2","kind":"interactive","entrypoint":"cli","tmux":"%s:@997.%%997"}`,
		pid, sid, tmux)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Реплика в разговор, за которым не стоит своя живая сессия, уезжает резюмом.
// До правки она уходила клавишами в чужое окно: человек писал в пустоту, а его
// текст читал посторонний агент.
func TestChatSayDoesNotTypeIntoAHeldWindow(t *testing.T) {
	sid := "dddd6666-1111-4111-8111-111111111111"
	alien := "eeee7777-2222-4222-8222-222222222222"
	e, c, tmuxLog := heldEnv(t, sid, alien)
	said := ""
	e.s.logf = func(format string, args ...any) { said += fmt.Sprintf(format, args...) + "\n" }

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "Довёл?"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика не прошла: %d %s", resp.StatusCode, got)
	}
	if strings.Contains(got, `"send-keys"`) {
		t.Fatalf("реплика уехала клавишами в чужое окно: %s", got)
	}
	if !strings.Contains(got, `"resume"`) {
		t.Fatalf("реплика поехала не резюмом: %s", got)
	}
	if keys := strings.Count(readFile(t, tmuxLog), "send-keys"); keys != 0 {
		t.Fatalf("в чужое окно ушли клавиши: %d посылок, %s", keys, readFile(t, tmuxLog))
	}
	// Причина остаётся в журнале дашборда: молчаливый уход на резюм читался бы
	// как обычная мёртвая сессия, а тут у имени живой хозяин.
	if !strings.Contains(said, "в окне chat-9 идёт разговор "+alien) {
		t.Fatalf("журнал не назвал хозяина окна: %s", said)
	}
}

// Уборка в архив снимает сессию, только когда та ведёт убираемый чат. Это
// самая дорогая половина беды: убери человек мёртвый разговор, чьё имя увёл
// печатный подъём, и вместе с ним ушёл бы весь цикл цели соседа.
func TestChatArchiveKeepsAHeldWindowAlive(t *testing.T) {
	sid := "dddd6666-3333-4333-8333-333333333333"
	alien := "eeee7777-4444-4444-8444-444444444444"
	e, c, _ := heldEnv(t, sid, alien)

	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive",
		`{"archived": true}`)
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка в архив: %d %s", resp.StatusCode, said)
	}
	if killed != "" {
		t.Fatalf("уборка сняла чужую живую сессию %q", killed)
	}
	if !strings.Contains(said, "идёт разговор "+alien) {
		t.Fatalf("ответ не сказал, чьим стало имя: %s", said)
	}
	if !strings.Contains(said, `"archived":true`) {
		t.Fatalf("признак архива не лёг: %s", said)
	}
	// Снятие под перезапуск идёт тем же правилом: чужую работу не трогают и
	// там, иначе кнопка смены модели убивала бы соседа.
	stop := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop",
		`{"drop": true}`)
	if stop.StatusCode != http.StatusConflict {
		t.Fatalf("снятие под перезапуск полезло в чужое окно: %d %s",
			stop.StatusCode, body(t, stop))
	}
	if killed != "" {
		t.Fatalf("снятие под перезапуск сняло чужую сессию %q", killed)
	}
}

// Панель показывает такой разговор снятым и называет, кто занял имя: живым он
// выглядел ровно до тех пор, пока мерой живости было имя окна из реестра.
func TestChatListNamesTheLiveWindowHolder(t *testing.T) {
	sid := "dddd6666-5555-4555-8555-555555555555"
	alien := "eeee7777-6666-4666-8666-666666666666"
	e, c, _ := heldEnv(t, sid, alien)

	list, _ := chatsWindow(t, e, c, "")
	got := chatOne(list, sid)
	if got == nil {
		t.Fatalf("разговор пропал из списка: %+v", list)
	}
	if got.State == chatLive {
		t.Fatalf("разговор с чужим окном показан живым: %+v", got)
	}
	if got.Gone == "" || got.GoneTo != alien {
		t.Fatalf("панель не назвала занявший имя разговор: %+v", got)
	}
	if got.Tmux != "" {
		t.Fatalf("чужое окно осталось адресом разговора: %+v", got)
	}
}

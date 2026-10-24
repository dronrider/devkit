package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/mainchat"
)

// Передача основного чата и подъём носителя (LLD DK-1422, решения 3-5). Тесты
// закрывают порядок строки и стопа, адресность по голове, ввод назначенной
// записи у дозора и выбор способа подъёма: живые прогоны дашборда проверяют
// то же руками по сценарию задачи.

// Строка передачи пишется в журнал до заказа стопа: реплика, вошедшая следом,
// ложится безадресно во вход записи, а не уходящей голове. Порядок доказывает
// снимок журнала, который поддельный tmux прикладывает к своему журналу вызовов
// в момент kill-session: снимок в момент стопа уже несёт строку передачи.
// Подъём нового носителя встаёт после стопа, назначение остаётся одно, и
// второй строки основного чата передача не заводит.
func TestHandoverMoveLineBeforeStop(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")
	writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-004", Chat: "tmux-task-XR-004", Project: "demo", Source: mainchat.ByWork}))
	writeScript(t, e.bin, "tmux", fmt.Sprintf(`echo "$@" >> %q
case "$1" in
ls) printf 'task-XR-004\t1\t1754770421\n';;
kill-session)
  printf 'журнал-при-стопе\n' >> %q
  cat %q >> %q 2>/dev/null
  ;;
new-session)
  for last; do :; done
  case "$last" in
  *task-run.py*)
    /bin/sh -c "$last" >/dev/null 2>&1 &
    echo '%%9';;
  esac;;
esac
exit 0`, tmuxLog, tmuxLog, mainchat.Path(e.home), tmuxLog))
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/handover", `{}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("передача не прошла: %d %s", resp.StatusCode, text)
	}
	log := readFile(t, tmuxLog)
	kill := strings.Index(log, "kill-session -t =task-XR-004")
	if kill < 0 {
		t.Fatalf("стоп конвейеру не заказан:\n%s", log)
	}
	shot := log[kill:]
	if !strings.Contains(shot, "журнал-при-стопе") ||
		!strings.Contains(shot, "источник передача прежде tmux-task-XR-004") {
		t.Fatalf("в момент стопа строки передачи в журнале не было:\n%s", shot)
	}
	lift := strings.LastIndex(log, "new-session")
	if lift < kill {
		t.Fatalf("подъём нового носителя встал раньше стопа:\n%s", log)
	}
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-004/chat")
	if code != http.StatusOK || got.Chat != "tmux-task-XR-004" || got.Source != mainchat.ByMove {
		t.Fatalf("адрес записи после передачи не тот: %d %+v", code, got)
	}
	if len(got.Segments) != 1 {
		t.Fatalf("передача завела второй отрезок основного чата: %+v", got.Segments)
	}
	data := readFile(t, mainchat.Path(e.home))
	if n := strings.Count(data, " назначение XR-004 "); n != 2 {
		t.Fatalf("у записи %d строк журнала, жду работу и передачу:\n%s", n, data)
	}
}

// Передавать нечего и нечем: записи без назначения ручка отказывает в пользу
// запуска, а цель живёт витками, и передача витка ждёт своего писателя.
func TestHandoverRefuses(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/handover", `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("передача без назначения ответила %d, жду 409", resp.StatusCode)
	}
	if said := body(t, resp); !strings.Contains(said, "нет назначенного разговора") {
		t.Fatalf("отказ не назвал причину: %s", said)
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-100/handover", `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("передача цели ответила %d, жду 409", resp.StatusCode)
	}
	if said := body(t, resp); !strings.Contains(said, "виток") {
		t.Fatalf("отказ цели не назвал виток: %s", said)
	}
}

// Адресность по голове (решение 4): живой голове текущего носителя реплика
// идёт адресно, снятая строкой передачи голова адресатом не считается, хотя
// её окно ещё живо и ход доходит до конца, а ввод назначенной записи не
// гаснет и у записи без строки доски.
func TestChatReplyAddressByHead(t *testing.T) {
	e := newTestEnv(t)
	const sid = "aaaa1111-2222-4222-8222-333333333333"
	writeBinds(t, e.home, "2026-09-01T10:00:00 сессия "+sid+" задача XR-004"+
		" проект demo дерево "+e.proj+" транскрипт "+standTranscript(e.home, "t")+
		" источник заказ повод startup tmux task-XR-004\n")
	onBoard := map[string]boardRow{"XR-004": {Sect: "in-progress"}}
	offBoard := map[string]boardRow{}
	alive := func(string) bool { return true }
	dead := func(string) bool { return false }
	info := sessionInfo{ID: sid, Task: "XR-004"}

	// Живая голова текущего носителя: реплика идёт адресно, в её дерево.
	writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-004", Chat: "tmux-task-XR-004", Project: "demo", Source: mainchat.ByWork}))
	if reply, _ := e.s.chatReply(e.proj, info, onBoard, alive); reply != replyToSession {
		t.Fatalf("живой голове выбрана ручка %q, жду адресную", reply)
	}

	// Голова снята передачей: назначение смещено строкой, окно ещё стоит, но
	// адресат уже новый носитель, и реплика уходит задаче.
	writeMainChat(t, e.home,
		mainchat.Line(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			mainchat.Bind{Record: "XR-004", Chat: "tmux-task-XR-004", Project: "demo", Source: mainchat.ByWork})+
			mainchat.Line(time.Now(),
				mainchat.Bind{Record: "XR-004", Chat: "tmux-task-XR-004", Prior: "tmux-task-XR-004",
					Project: "demo", Source: mainchat.ByMove}))
	reply, note := e.s.chatReply(e.proj, info, onBoard, alive)
	if reply != replyToTask {
		t.Fatalf("снятой голове выбрана ручка %q, жду ручку задачи", reply)
	}
	if !strings.Contains(note, "передано") {
		t.Fatalf("причина не назвала передачу: %s", note)
	}

	// Записи на доске нет, а назначение есть: ввод не гаснет, реплика ждёт
	// во входе записи следующего носителя. Разговоровый ключ записи проверяется
	// той же мерой, что и окно конвейера.
	writeMainChat(t, e.home, mainchat.Line(time.Now(),
		mainchat.Bind{Record: "XR-777", Chat: sid, Prior: sid, Project: "demo", Source: mainchat.ByMove}))
	far := sessionInfo{ID: sid, Task: "XR-777"}
	if reply, note := e.s.chatReply(e.proj, far, offBoard, alive); reply != replyToTask ||
		!strings.Contains(note, "следующий носитель") {
		t.Fatalf("ввод назначенной записи погас: %q %s", reply, note)
	}

	// Назначения нет: кончившийся разговор без записи гаснет, как и раньше.
	writeMainChat(t, e.home, mainchat.Line(time.Now(),
		mainchat.Bind{Record: "XR-778", Source: mainchat.ByOff}))
	gone := sessionInfo{ID: sid, Task: "XR-778"}
	if reply, _ := e.s.chatReply(e.proj, gone, offBoard, dead); reply != "" {
		t.Fatalf("неназначенной записи обещан адресат: %q", reply)
	}
}

// Ввод назначенной записи дозор не подметает (решение 4): строка Backlog с
// назначенным разговором ждёт следующего носителя, и закрытая запись с
// назначением остаётся адресатом доставки.
func TestChatHoldKeepsAssignedInput(t *testing.T) {
	e, calls := holdEnv(t)
	deadFile := holdTaskFile(t, e.proj, "XR-2")
	holdPutLine(t, e.proj, "XR-2", chat.TaskLine(time.Now(), "возьми эту задачу"))
	writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-2", Chat: "tmux-task-XR-2", Project: "demo", Source: mainchat.ByWork}))
	e.s.chatHoldSweep()
	if src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-2.in")); !strings.Contains(src, "возьми эту задачу") {
		t.Fatalf("дозор подмел ввод назначенной записи:\n%s", src)
	}
	if got := readFile(t, deadFile); strings.Contains(got, "возьми эту задачу") {
		t.Fatalf("реплика назначенной записи уехала в файл задачи:\n%s", got)
	}
	if said := readFile(t, calls); strings.Contains(said, "XR-2") {
		t.Fatalf("дозор позвал человека по назначенной записи:\n%s", said)
	}
	// Закрытая запись мера читает тем же образом: без назначения отказ, с
	// назначением адресат, и ручка задачи не гаснет на разборе архива.
	arch := "# Архив (префикс XR)\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n" +
		"| XR-9 | Закрытая | bug | P1 | 2026-08-13 | [tasks/archive/2026/XR-9.md](tasks/archive/2026/XR-9.md) |\n"
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "TASKS-archive.md"), []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := map[string]boardRow{}
	if why := e.s.deadAddresseeWhy(e.proj, "XR-9", rows); why == "" {
		t.Fatal("закрытая запись без назначения названа адресатом")
	}
	writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 24, 12, 1, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-9", Chat: "tmux-task-XR-9", Project: "demo", Source: mainchat.ByWork}))
	if why := e.s.deadAddresseeWhy(e.proj, "XR-9", rows); why != "" {
		t.Fatalf("закрытая запись с назначением не адресат: %s", why)
	}
}

// Выбор способа подъёма (решение 5): харнес с продолжением поднимает резюм в
// то же имя окна, харнес без продолжения поднимает свежую сессию того же
// разговора, и доступное состояние записи подкладывается в первую реплику.
// Назначение в обоих случаях ведётся за поднятым заходом строкой передачи.
func TestContinueChoosesLiftWay(t *testing.T) {
	const sid = "aaaa1111-2222-4222-8222-333333333333"

	t.Run("резюм в то же окно", func(t *testing.T) {
		e, c, _ := tasksEnv(t)
		tmuxLog := filepath.Join(e.home, "tmux.log")
		writeTmuxFake(t, e.bin, tmuxLog, "")
		writeClientFakes(t, e.bin, "клиент-1", "клиент-2")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
		writeTaskRunFake(t, filepath.Dir(e.proj))
		useHeadHome(t, e)
		endedChat(t, e, sid, "XR-004", "chat-7")
		storeModel(t, e, sid, "модель-pro")
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/continue", ``)
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("продолжение не прошло: %d %s", resp.StatusCode, text)
		}
		if !strings.Contains(text, `"way":"resume"`) {
			t.Fatalf("харнес с продолжением поднял не резюм: %s", text)
		}
		said := readFile(t, tmuxLog)
		if !strings.Contains(said, "new-session -d -s chat-7 ") {
			t.Fatalf("резюм встал не в то же имя окна:\n%s", said)
		}
		if !strings.Contains(said, "--resume '"+sid+"'") {
			t.Fatalf("резюм без продолжения сессии:\n%s", said)
		}
	})

	t.Run("свежая сессия на харнесе без продолжения", func(t *testing.T) {
		e, c, _ := tasksEnv(t)
		tmuxLog := filepath.Join(e.home, "tmux.log")
		writeTmuxFake(t, e.bin, tmuxLog, "")
		writeClientFakes(t, e.bin, "клиент-1", "клиент-2")
		writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
		writeTaskRunFake(t, filepath.Dir(e.proj))
		useHeadHome(t, e)
		endedChat(t, e, sid, "XR-004", "chat-7")
		storeModel(t, e, sid, "вторая-pro")
		writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
			mainchat.Bind{Record: "XR-004", Chat: "tmux-task-XR-004", Project: "demo", Source: mainchat.ByWork}))
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/continue", ``)
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("продолжение не прошло: %d %s", resp.StatusCode, text)
		}
		if !strings.Contains(text, `"way":"fresh"`) {
			t.Fatalf("харнес без продолжения поднял резюм: %s", text)
		}
		said := readFile(t, tmuxLog)
		if !strings.Contains(said, "new-session -d -s chat-7 ") {
			t.Fatalf("свежая сессия встала не в имя окна разговора:\n%s", said)
		}
		if strings.Contains(said, "--resume") {
			t.Fatalf("свежая сессия поднята резюмом:\n%s", said)
		}
		if !strings.Contains(said, "docs/tasks/XR-004.md") || !strings.Contains(said, "tmux-task-XR-004") {
			t.Fatalf("в первую реплику не подложено состояние записи:\n%s", said)
		}
		// Назначение ведётся за заходом: носителем становится окно свежей
		// сессии, прежний конвейер уезжает полем «прежде».
		code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-004/chat")
		if code != http.StatusOK || got.Chat != "tmux-chat-7" || got.Source != mainchat.ByMove {
			t.Fatalf("назначение не ушло за поднятым заходом: %d %+v", code, got)
		}
		data := readFile(t, mainchat.Path(e.home))
		if !strings.Contains(data, "прежде tmux-task-XR-004") {
			t.Fatalf("строка передачи не назвала прежнего носителя:\n%s", data)
		}
	})
}

// Панель зовёт передачу с экрана записи: кнопка стоит в полосе действий
// идущей работы, рядом со стопом. Ввод записи по мёртвой голове носителя не
// гаснет: ключ носителя, не разрешавшийся разговором, остаётся адресом записи,
// и реплика уходит во вход записи ручкой задачи (решение 4).
func TestStaticHandoverWay(t *testing.T) {
	app, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"/handover"`,
		`"Передать"`,
		`handoverTask(project, id)`,
		`st.carrier`,
		`во вход записи `,
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("в app.js нет %s", want)
		}
	}
}

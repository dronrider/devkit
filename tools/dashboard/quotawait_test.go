package main

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ретрай кончившейся подписки (DK-647). Харнес молчит транскриптом и отвечает
// сокетом как живой. Единственный след это его собственная строка ретрая в
// пейне («Weekly limit reached, Retrying in 1h (3pm), attempt 1/300», материал
// разбора 480 сессий за 10 дней). Правка, от которой стенды краснеют: разбор
// этой строки (quotaRetryLine, quotaRetryWeekly), сцепка со снимком квоты
// (quotaResetOf) и вынос причины со сроком в список разговоров и в ответ
// ручки say вместо часовой тишины.

const quotaRetryPane = "Weekly limit reached, Retrying in 1h (3pm), attempt 1/300\n"

// quotaTaskFileQuote это копия строки docs/tasks/DK-647.md, которая привела
// живой случай ревью: агент вывел файл этой же задачи в свой терминал, файл
// цитирует строку ретрая дословно, и прежняя мера (пара подстрок где угодно в
// пейне) сочла живую сессию кончившейся подпиской. Строка не читается из
// самого файла: он переедет в архив после закрытия задачи, а регрессия
// обязана ловиться и после переезда.
const quotaTaskFileQuote = "- Чат «Груминг DK-640 с вопросами в чате», сессия 22fb4dce в " +
	"~/.claude/projects/-Users-rider-projects-devkit/. Ход закрыт в 12:04, в 12:09 " +
	"сообщение с панели принято очередью (queue-operation dequeue), после него в " +
	"транскрипте нет ни одной записи: модель не запустилась, недельная квота " +
	"исчерпана. Единственный след на всей машине это статус-строка окна tmux " +
	"task-DK-640: «Weekly limit reached, Retrying in 1h (3pm), attempt 1/300». " +
	"Второе сообщение от 12:22 лежит в очереди за первым.\n"

func TestQuotaRetryLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		pane string
		want bool
	}{
		{"строка ретрая недельного лимита", quotaRetryPane, true},
		{"обычный вывод агента", "работаю над задачей\nчитаю файл\n", false},
		{"пусто", "", false},
		{"одно из двух слов без второго", "Weekly limit reached\n", false},
		{"регистр не важен", "WEEKLY LIMIT REACHED, RETRYING IN 62S, ATTEMPT 2/300\n", true},
		{"пятичасовое окно тоже своя строка", "5-hour limit reached, Retrying in 62s, attempt 1/300\n", true},
		// Живой случай ревью: обе подстроки есть, а строки ретрая нет, цитата
		// стоит посреди чужой прозы файла задачи с обеих сторон.
		{"цитата строки ретрая внутри чужого текста", quotaTaskFileQuote, false},
		{"вывод файла задачи целиком в ленте", quotaTaskFileQuote + quotaRetryPane + "лишний текст ниже\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, got := quotaRetryLine(tc.pane)
			if got != tc.want {
				t.Errorf("quotaRetryLine(%q) = %v, жду %v", tc.pane, got, tc.want)
			}
		})
	}
}

func TestQuotaRetryWeekly(t *testing.T) {
	line, ok := quotaRetryLine(quotaRetryPane)
	if !ok || !quotaRetryWeekly(line) {
		t.Error("недельный ретрай не узнан")
	}
	other, ok := quotaRetryLine("5-hour limit reached, Retrying in 62s, attempt 1/300\n")
	if !ok || quotaRetryWeekly(other) {
		t.Error("пятичасовой ретрай ошибочно назван недельным")
	}
}

func TestQuotaResetHuman(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.Local)
	if got := quotaResetHuman("2026-09-03T15:00", now); got != "15:00" {
		t.Errorf("сброс сегодняшним днём: %q, жду 15:00", got)
	}
	if got := quotaResetHuman("2026-09-07T15:00", now); got != "07.09 15:00" {
		t.Errorf("сброс другим днём: %q, жду 07.09 15:00", got)
	}
	if got := quotaResetHuman("не время", now); got != "" {
		t.Errorf("битая дата разобралась во что-то: %q", got)
	}
}

func TestQuotaWaitNote(t *testing.T) {
	if got := quotaWaitNote("15:00"); !strings.Contains(got, quotaWaitWord) || !strings.Contains(got, "15:00") {
		t.Errorf("причина и срок не в одной фразе: %q", got)
	}
	if got := quotaWaitNote(""); !strings.Contains(got, "неизвестен") {
		t.Errorf("неизвестный срок не назван честно: %q", got)
	}
}

// Живой статус. Ретрай на экране виден списком разговоров причиной и сроком
// сброса, взятым из снимка квоты, а не из самой строки экрана (ей нельзя
// верить, часа и пояса в ней не названо).
func TestChatListShowsQuotaWait(t *testing.T) {
	sid := "aaaa9999-1111-4111-8111-111111111111"
	e, c, _ := termSayEnv(t, sid, quotaRetryPane)
	e.s.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.Local) }
	writeQuota(t, e.home, "перваяtest",
		"taken = 2026-09-07T11:00\nweek_all = 100% сброс 2026-09-07T15:00\n")

	list := chatsOf(t, e, c)
	if len(list) != 1 {
		t.Fatalf("список разговора: %+v", list)
	}
	if list[0].Quota == "" {
		t.Fatal("ретрай квоты не назван в списке: чат выглядит обычным живым разговором")
	}
	if !strings.Contains(list[0].Quota, quotaWaitWord) {
		t.Errorf("в статусе нет причины: %q", list[0].Quota)
	}
	if !strings.Contains(list[0].Quota, "15:00") {
		t.Errorf("в статусе нет срока сброса снимка квоты: %q", list[0].Quota)
	}
}

// Обычная живая панель (никакого ретрая) не путается с кончившейся подпиской.
func TestChatListNoQuotaOnOrdinaryPane(t *testing.T) {
	sid := "bbbb9999-2222-4222-8222-222222222222"
	e, c, _ := termSayEnv(t, sid, "работаю над задачей, читаю файл\n")

	list := chatsOf(t, e, c)
	if len(list) != 1 {
		t.Fatalf("список разговора: %+v", list)
	}
	if list[0].Quota != "" {
		t.Errorf("живой разговор ошибочно назван кончившейся подпиской: %q", list[0].Quota)
	}
}

// Ретрая нет снимка квоты (harness не назван раскладкой машины либо файла
// нет). Срок сброса называется неизвестным, а не выдумывается по строке
// экрана.
func TestChatListQuotaWaitUnknownResetHonest(t *testing.T) {
	sid := "cccc9999-3333-4333-8333-333333333333"
	e, c, _ := termSayEnv(t, sid, quotaRetryPane)
	// Снимка квоты в ~/.devkit/quota нет вовсе.

	list := chatsOf(t, e, c)
	if len(list) != 1 {
		t.Fatalf("список разговора: %+v", list)
	}
	if !strings.Contains(list[0].Quota, quotaWaitWord) {
		t.Fatalf("причина не названа без снимка: %q", list[0].Quota)
	}
	if !strings.Contains(list[0].Quota, "неизвестен") {
		t.Errorf("отсутствие снимка не названо честно, а промолчано: %q", list[0].Quota)
	}
}

// Сердце DK-647. На реплику в чат кончившейся подписки приходит статус вместо
// часовой тишины, реплика уходит в терминал (доставлена харнесу, а не
// потеряна), а причина со сроком остаются в ленте разговора пометкой.
func TestChatSayQuotaWaitTellsStatusInsteadOfSilence(t *testing.T) {
	sid := "dddd9999-4444-4444-8444-444444444444"
	e, c, sent := termSayEnv(t, sid, quotaRetryPane)
	e.s.now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.Local) }
	writeQuota(t, e.home, "перваяtest",
		"taken = 2026-09-07T11:00\nweek_all = 100% сброс 2026-09-07T15:00\n")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай"}`)
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в очередь квоты отбита: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("реплика не поехала терминалом: %s", said)
	}
	if !strings.Contains(said, quotaWaitWord) || !strings.Contains(said, "15:00") {
		t.Fatalf("ответ ручки не назвал причину и срок вместо тишины: %s", said)
	}
	if keys := readFile(t, sent); !strings.Contains(keys, "продолжай") {
		t.Fatalf("реплика не подалась клавишами, осталась не в очереди харнеса: %q", keys)
	}
	marks := saidMarks(t, e.home, "sess-"+sid)
	if len(marks) == 0 {
		t.Fatal("в ленте разговора нет пометки о ретрае квоты")
	}
	last := marks[len(marks)-1]
	if !strings.Contains(last, quotaWaitWord) || !strings.Contains(last, "15:00") {
		t.Errorf("пометка ленты не называет причину и срок: %q", last)
	}
}

// Панельная сторона. Причина и срок стоят живым статусом над лентой открытого
// разговора и чипом в выпадающем списке, до его открытия. Сторожит стенд
// testdata/poc_quotawait.mjs. Настоящий static/app.js поднимается в node с
// заглушкой DOM, и проверяется собранная разметка, а не текст исходника. Без
// node шаг пропускается, как у остальных стендов статики.
func TestStaticChatQuotaWaitTellsThePanel(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд ретрая квоты пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_quotawait.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("ретрай квоты в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

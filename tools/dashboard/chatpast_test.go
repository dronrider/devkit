package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Склейка заходов одного окна (DK-723). Конвейер задачи идёт живой сессией, но
// остановы у неё прежние: потолок проходов, воронка молчания, вышедший клиент,
// перезагрузка машины. После каждого кнопка поднимает новую сессию со своим ID
// под тем же именем окна, и список, ключующий строку по ID сессии, показывал
// такие заходы отдельными строками с одинаковыми заголовками.

// passTalk рисует транскрипт захода: одна реплика с названным временем.
func passTalk(text, at string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text +
		`"},"timestamp":"` + at + `","gitBranch":"main"}` + "\n"
}

// passBind это строка реестра про заход, поднятый дашбордом под именем окна.
func passBind(stamp, sid, task, proj, tmux string) string {
	return stamp + " сессия " + sid + " задача " + task + " проект demo дерево " + proj +
		" транскрипт /tmp/" + sid + ".jsonl источник заказ повод startup tmux " + tmux + "\n"
}

// Три захода одного окна стоят в списке одной строкой: голова это тот заход,
// за которым закреплено имя, прошлые уходят полем past старшими первыми, а их
// задачи достаются строке. Прежде каждый перезапуск вставал своей строкой с
// тем же заголовком, и живую среди них человек искал глазами по свежести.
func TestChatListGluesWindowPasses(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	base := time.Now().Add(-3 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaaa-0001", passTalk("первый заход", "2026-09-04T10:00:00.000Z"), base)
	writeSession(t, e.home, e.proj, "", "bbbb-0002", passTalk("второй заход", "2026-09-04T11:00:00.000Z"), base.Add(time.Hour))
	writeSession(t, e.home, e.proj, "", "cccc-0003", passTalk("третий заход", "2026-09-04T12:00:00.000Z"), base.Add(2*time.Hour))
	writeBinds(t, e.home,
		passBind("2026-09-04T10:00:00", "aaaa-0001", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T11:00:00", "bbbb-0002", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T12:00:00", "cccc-0003", "XR-4", e.proj, "task-XR-4"))

	list := chatsOf(t, e, c)
	if len(list) != 1 {
		t.Fatalf("строк списка %d, а заходы одного окна стоят одной: %+v", len(list), list)
	}
	got := list[0]
	if got.ID != "cccc-0003" {
		t.Errorf("головой строки встал не тот заход, за которым имя окна: %+v", got)
	}
	if got.State != chatLive || got.Tmux != "task-XR-4" {
		t.Errorf("живой заход потерял окно и живость: %+v", got)
	}
	if len(got.Past) != 2 || got.Past[0].ID != "aaaa-0001" || got.Past[1].ID != "bbbb-0002" {
		t.Errorf("прошлые заходы не приехали строкой старшими первыми: %+v", got.Past)
	}
	if !hasTask(got.Tasks, "XR-4") {
		t.Errorf("задача заходов до склеенной строки не доехала: %+v", got.Tasks)
	}
}

// Голова находится и тогда, когда свежий заход стоит в списке ниже мёртвого
// соседа: имя окна закреплено за ним, и реплика уходит живому заходу, а не в
// кончившийся. Свежесть тут не мера, живой заход бывает тише мёртвого.
func TestChatListGlueHeadIsWindowHolder(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	base := time.Now().Add(-3 * time.Hour)
	// Мёртвый заход говорил позже живого: список ставит его выше по времени
	// последней реплики, а имя окна держит всё равно живой.
	writeSession(t, e.home, e.proj, "", "aaaa-0001", passTalk("мёртвый заход", "2026-09-04T13:00:00.000Z"), base)
	writeSession(t, e.home, e.proj, "", "bbbb-0002", passTalk("живой заход", "2026-09-04T12:00:00.000Z"), base.Add(time.Hour))
	writeBinds(t, e.home,
		passBind("2026-09-04T10:00:00", "aaaa-0001", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T11:00:00", "bbbb-0002", "XR-4", e.proj, "task-XR-4"))

	list := chatsOf(t, e, c)
	if len(list) != 1 {
		t.Fatalf("строк списка %d, ждал одну: %+v", len(list), list)
	}
	got := list[0]
	if got.ID != "bbbb-0002" || got.State != chatLive {
		t.Fatalf("головой строки встал не живой заход: %+v", got)
	}
	if got.Gone != "" {
		t.Errorf("живая голова помечена снятой: %q", got.Gone)
	}
	if len(got.Past) != 1 || got.Past[0].ID != "aaaa-0001" {
		t.Errorf("мёртвый сосед не ушёл в прошлые заходы: %+v", got.Past)
	}
}

// Разговор человека о задаче в склейку не попадает: имя окна у него своё
// (chat-<ID>-<n>), и лента у него своя. Склеивать по рабочей задаче значило бы
// свести конвейер, доводящий чат и окно человека в одну строку.
func TestChatListKeepsOwnWindowApart(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	base := time.Now().Add(-2 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaaa-0001", passTalk("прошлый заход конвейера", "2026-09-04T10:00:00.000Z"), base)
	writeSession(t, e.home, e.proj, "", "bbbb-0002", passTalk("живой заход конвейера", "2026-09-04T11:00:00.000Z"), base.Add(time.Hour))
	writeSession(t, e.home, e.proj, "", "dddd-0004", passTalk("разговор о задаче", "2026-09-04T11:30:00.000Z"), base.Add(90*time.Minute))
	writeBinds(t, e.home,
		passBind("2026-09-04T10:00:00", "aaaa-0001", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T11:00:00", "bbbb-0002", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T11:30:00", "dddd-0004", "XR-4", e.proj, "chat-XR-4-1"))

	byID := map[string]chatEntry{}
	for _, ch := range chatsOf(t, e, c) {
		byID[ch.ID] = ch
	}
	if len(byID) != 2 {
		t.Fatalf("строк списка %d, ждал две (конвейер и разговор): %+v", len(byID), byID)
	}
	if got, ok := byID["dddd-0004"]; !ok || len(got.Past) != 0 {
		t.Errorf("разговор человека втянулся в склейку конвейера: %+v", got)
	}
	if got := byID["bbbb-0002"]; len(got.Past) != 1 || got.Past[0].ID != "aaaa-0001" {
		t.Errorf("заходы конвейера не склеились: %+v", got.Past)
	}
}

// Лента склеенной строки идёт сверху вниз: прошлые заходы стоят перед нынешним,
// а границы заходов названы разделителем. История задачи читается тут одним
// разговором, без перехода в соседнюю запись списка.
func TestChatFeedGluesPastPasses(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	base := time.Now().Add(-3 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaaa-0001", passTalk("первый заход разобрал постановку", "2026-09-04T10:00:00.000Z"), base)
	writeSession(t, e.home, e.proj, "", "bbbb-0002", passTalk("второй заход написал тесты", "2026-09-04T11:00:00.000Z"), base.Add(time.Hour))
	writeSession(t, e.home, e.proj, "", "cccc-0003", passTalk("этот заход дописывает доку", "2026-09-04T12:00:00.000Z"), base.Add(2*time.Hour))
	writeBinds(t, e.home,
		passBind("2026-09-04T10:00:00", "aaaa-0001", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T11:00:00", "bbbb-0002", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T12:00:00", "cccc-0003", "XR-4", e.proj, "task-XR-4"))

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/cccc-0003", "")
	if resp.StatusCode != 200 {
		t.Fatalf("лента разговора: %d", resp.StatusCode)
	}
	text := body(t, resp)
	var got struct {
		Items []reply `json:"items"`
		Start bool    `json:"start"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался (%v): %s", err, text)
	}
	var order []string
	for _, it := range got.Items {
		order = append(order, it.Role+"|"+it.Text)
	}
	want := []string{
		"mark|" + passMarkWord(1, 3, "aaaa-0001"),
		"user|первый заход разобрал постановку",
		"mark|" + passMarkWord(2, 3, "bbbb-0002"),
		"user|второй заход написал тесты",
		"mark|" + passMarkWord(3, 3, "cccc-0003"),
		"user|этот заход дописывает доку",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Fatalf("лента склеена не по порядку:\nполучил:\n%s\nждал:\n%s",
			strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
	if !got.Start {
		t.Errorf("склеенная лента не считает себя начатой с начала: %s", text)
	}
}

// Разговор без прошлых заходов лентой не меняется: разделителю тут взяться
// неоткуда, и лишняя черта в начале читалась бы как пропавшая история.
func TestChatFeedSinglePassHasNoMark(t *testing.T) {
	e, c := chatEnv(t)
	base := time.Now().Add(-time.Hour)
	writeSession(t, e.home, e.proj, "", "cccc-0003", passTalk("один заход", "2026-09-04T12:00:00.000Z"), base)
	writeBinds(t, e.home, passBind("2026-09-04T12:00:00", "cccc-0003", "XR-4", e.proj, "task-XR-4"))

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/cccc-0003", "")
	text := body(t, resp)
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался (%v): %s", err, text)
	}
	for _, it := range got.Items {
		if it.Role == roleMark {
			t.Fatalf("в ленте одного захода встал разделитель: %s", text)
		}
	}
}

// Панельная часть склейки: строка списка говорит числом, из скольких заходов
// она склеена, а границы заходов в ленте нарисованы чертой, как граница дня.
// Стенд гоняет настоящий static/app.js синтетическим DOM.
func TestStaticChatPastGlued(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд склейки заходов пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatpast.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("склейка заходов в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Ключ записи в склеенной ленте уникален на всю склейку. Ключ транскрипта это
// смещение в своём файле, и у первой записи каждого захода он один и тот же
// («m:0.0»). Панель отсеивает повторы по ключу, и из трёх заходов до ленты
// доезжал один, а страница истории просилась по чужому ключу (замечание ревью
// 1). Заход стоит в приставке ключа, и записи расходятся.
func TestChatFeedPassKeysAreUnique(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	base := time.Now().Add(-3 * time.Hour)
	two := func(first, second, at1, at2 string) string {
		return passTalk(first, at1) + passTalk(second, at2)
	}
	writeSession(t, e.home, e.proj, "", "aaaa-0001",
		two("первый заход начал", "первый заход кончил", "2026-09-04T10:00:00.000Z", "2026-09-04T10:05:00.000Z"), base)
	writeSession(t, e.home, e.proj, "", "bbbb-0002",
		two("второй заход начал", "второй заход кончил", "2026-09-04T11:00:00.000Z", "2026-09-04T11:05:00.000Z"), base.Add(time.Hour))
	writeSession(t, e.home, e.proj, "", "cccc-0003",
		two("этот заход начал", "этот заход идёт", "2026-09-04T12:00:00.000Z", "2026-09-04T12:05:00.000Z"), base.Add(2*time.Hour))
	writeBinds(t, e.home,
		passBind("2026-09-04T10:00:00", "aaaa-0001", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T11:00:00", "bbbb-0002", "XR-4", e.proj, "task-XR-4"),
		passBind("2026-09-04T12:00:00", "cccc-0003", "XR-4", e.proj, "task-XR-4"))

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/cccc-0003", "")
	text := body(t, resp)
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался (%v): %s", err, text)
	}
	seen := map[string]string{}
	for _, it := range got.Items {
		if it.Key == "" {
			t.Errorf("запись ленты без ключа: %+v", it)
			continue
		}
		if had, dup := seen[it.Key]; dup {
			t.Errorf("ключ %q носят две записи (%q и %q): панель оставит одну",
				it.Key, had, it.Text)
		}
		seen[it.Key] = it.Text
	}
	// Записи всех заходов доезжают до ленты, и меряется это ключами: число
	// разделителей ничего про них не говорит.
	for _, want := range []string{"первый заход начал", "первый заход кончил",
		"второй заход начал", "второй заход кончил", "этот заход начал", "этот заход идёт"} {
		found := false
		for _, it := range got.Items {
			if it.Text == want {
				found = true
			}
		}
		if !found {
			t.Errorf("записи %q в склеенной ленте нет: %s", want, text)
		}
	}
}

// Заход старше потолка склейки остаётся своей строкой списка. Прежде поле
// прошлых заходов потолка не знало: чип считал девять, лента клеила шесть, и
// дороги к остальным трём не оставалось (замечание ревью 2).
func TestChatListGlueCapKeepsOlderRows(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-XR-4|1|123";;
esac
exit 0`)
	base := time.Now().Add(-24 * time.Hour)
	ids := []string{"aaaa-0001", "bbbb-0002", "cccc-0003", "dddd-0004", "eeee-0005",
		"ffff-0006", "gggg-0007", "hhhh-0008", "iiii-0009"}
	var binds []string
	for i, id := range ids {
		at := fmt.Sprintf("2026-09-04T%02d:00:00", 10+i)
		writeSession(t, e.home, e.proj, "", id, passTalk("заход "+id, at+".000Z"),
			base.Add(time.Duration(i)*time.Hour))
		binds = append(binds, passBind(at, id, "XR-4", e.proj, "task-XR-4"))
	}
	writeBinds(t, e.home, binds...)

	list := chatsOf(t, e, c)
	if len(list) != len(ids)-chatPassMax {
		t.Fatalf("строк списка %d, ждал голову плюс заходы за потолком: %+v", len(list), list)
	}
	var head chatEntry
	rest := map[string]chatEntry{}
	for _, ch := range list {
		if ch.ID == "iiii-0009" {
			head = ch
			continue
		}
		rest[ch.ID] = ch
	}
	if len(head.Past) != chatPassMax {
		t.Errorf("склеено %d заходов при потолке %d: %+v", len(head.Past), chatPassMax, head.Past)
	}
	for _, id := range []string{"aaaa-0001", "bbbb-0002"} {
		got, ok := rest[id]
		if !ok {
			t.Errorf("заход %s за потолком пропал из списка молча", id)
			continue
		}
		if got.Gone == "" {
			t.Errorf("заход %s за потолком стоит строкой без слов о снятии: %+v", id, got)
		}
	}
	// Лента головы держит тот же потолок: склеенное в строке и склеенное в
	// ленте это одно и то же число.
	if got := len(e.s.chatPasses("iiii-0009")); got != chatPassMax {
		t.Errorf("лента тянет %d заходов при потолке %d", got, chatPassMax)
	}
}

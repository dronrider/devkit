package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// Список диалогов растёт вместе с машиной: транскрипты не исчезают, и на живой
// машине в общем списке лежало сто сорок пять строк при сорока одной живой.
// Ручка отдаёт окно свежести, а не всё подряд, и говорит, что раньше есть ещё.

// saidLine это запись транскрипта с заданным временем реплики: время разговора
// считается по ней, а не по касанию файла.
func saidLine(text string, at time.Time) string {
	return fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":%q},"timestamp":%q,"gitBranch":"main"}`,
		text, at.UTC().Format(time.RFC3339)) + "\n"
}

// chatsWindow спрашивает общий список машины и отдаёт строки вместе с
// признаком «раньше есть ещё».
func chatsWindow(t *testing.T, e *testEnv, c *http.Client, query string) ([]chatEntry, bool) {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?all=1"+query, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("общий список (%s): %d", query, resp.StatusCode)
	}
	var got struct {
		Chats []chatEntry `json:"chats"`
		Older bool        `json:"older"`
		Days  int         `json:"days"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	return got.Chats, got.Older
}

func chatIn(list []chatEntry, sid string) bool {
	for _, c := range list {
		if c.ID == sid {
			return true
		}
	}
	return false
}

// Окно списка: трое суток плюс живые любого возраста. Старый разговор в список
// не едет и достаётся ключом days, а признак older говорит панели, что кнопке
// «показать раньше» есть что показывать.
func TestChatListWindowKeepsFreshAndLive(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Now()
	fresh := "aaaa1111-1111-4111-8111-111111111111"
	stale := "bbbb2222-2222-4222-8222-222222222222"
	liveOld := "cccc3333-3333-4333-8333-333333333333"
	touched := "dddd4444-4444-4444-8444-444444444444"
	writeSession(t, e.home, e.proj, "", fresh, saidLine("сегодняшний разговор", now.Add(-time.Hour)), now.Add(-time.Hour))
	writeSession(t, e.home, e.proj, "", stale, saidLine("давняя беседа", now.AddDate(0, 0, -10)), now.AddDate(0, 0, -10))
	writeSession(t, e.home, e.proj, "", liveOld, saidLine("старый, но живой", now.AddDate(0, 0, -10)), now.AddDate(0, 0, -10))
	// Служебное касание файла: транскрипт тронут сегодня, а последняя реплика
	// в нём десятидневной давности. Мерить окно временем правки значило бы
	// пускать такую беседу в сегодняшний список.
	writeSession(t, e.home, e.proj, "", touched, saidLine("тронутый служебщиной", now.AddDate(0, 0, -10)), now)
	// Живой разговор узнаётся записью реестра клиента: свой процесс тут и есть
	// доказательство живости.
	writePeer(t, e.home, liveOld, os.Getpid())

	list, older := chatsWindow(t, e, c, "")
	if !chatIn(list, fresh) {
		t.Errorf("сегодняшний разговор выпал из окна: %+v", list)
	}
	if !chatIn(list, liveOld) {
		t.Errorf("живой разговор старше окна выпал из списка: к нему идут отвечать")
	}
	if chatIn(list, stale) {
		t.Errorf("разговор десятидневной давности стоит в окне трёх суток: список копится дальше")
	}
	if chatIn(list, touched) {
		t.Errorf("беседа, тронутая служебщиной, пролезла в окно по времени правки файла")
	}
	if !older {
		t.Error("ручка не сказала, что раньше есть ещё: кнопке «показать раньше» неоткуда взяться")
	}

	// Ключ days двигает окно, а days=0 снимает его вовсе: этим списком живут
	// поиск по всей машине и последняя ступень «показать раньше».
	all, older := chatsWindow(t, e, c, "&days=0")
	for _, sid := range []string{fresh, stale, liveOld, touched} {
		if !chatIn(all, sid) {
			t.Errorf("список без окна недосчитался разговора %s: %+v", sid, all)
		}
	}
	if older {
		t.Error("список без окна всё ещё обещает что-то раньше")
	}

	// Открытый панелью разговор остаётся в списке любого возраста: иначе адрес
	// старой беседы выпадал бы из выпадашки прямо под руками.
	kept, _ := chatsWindow(t, e, c, "&keep="+stale)
	if !chatIn(kept, stale) {
		t.Error("названный ключом keep разговор не пережил окно: панель стоит на нём")
	}
}

// Разговор без единой содержательной реплики (поднялся и умер, служебный
// подъём) в списке не строка, а помеха. Отсев идёт по уже прочитанной шапке:
// второго чтения транскрипта он не стоит.
func TestChatListSkipsBlank(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Now()
	said := "aaaa1111-1111-4111-8111-111111111111"
	empty := "bbbb2222-2222-4222-8222-222222222222"
	service := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", said, saidLine("живые слова", now.Add(-time.Hour)), now.Add(-time.Hour))
	// Сессия поднялась и умерла: в транскрипте одна служебная запись клиента.
	writeSession(t, e.home, e.proj, "", empty,
		`{"type":"system","subtype":"init","timestamp":"`+now.Add(-time.Hour).UTC().Format(time.RFC3339)+`"}`+"\n",
		now.Add(-time.Hour))
	// Служебный подъём: единственная реплика целиком состоит из вставки
	// харнеса, и лента такой пузырь не рисует вовсе.
	writeSession(t, e.home, e.proj, "", service,
		saidLine("<command-name>/clear</command-name>", now.Add(-time.Hour)), now.Add(-time.Hour))

	list, _ := chatsWindow(t, e, c, "")
	if !chatIn(list, said) {
		t.Fatalf("разговор со словами пропал из списка: %+v", list)
	}
	if chatIn(list, empty) {
		t.Error("разговор, где никто ничего не сказал, стоит в списке")
	}
	if chatIn(list, service) {
		t.Error("служебный подъём стоит в списке разговором")
	}
}

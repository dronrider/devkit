package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Незачатый разговор (жалоба пользователя: «новый чат создаётся по сути после
// первой реплики»). Разговор и сессия клиента были одним и тем же, поэтому до
// первой реплики разговора не существовало нигде, кроме адреса вкладки: в
// списке его было не видно, набранный текст оставался в следующем новом чате, а
// завести рядом второй было нечем. Стенды тут про то, что запись живёт своей
// жизнью: заводится без реплики, стоит в списке, помнит свой черновик,
// пришивается к поднявшейся сессии и не копится мусором.

// blankPath это файл памяти диалога.
func blankPath(home, id string) string {
	return filepath.Join(chatStoreDir(home), id+".json")
}

// blankMake заводит запись кнопкой «+» и отдаёт её ID.
func blankMake(t *testing.T, e *testEnv, c *http.Client, order string) string {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/blank", order)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запись чата не завелась: %d %s", resp.StatusCode, text)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatalf("ручка не назвала ID заведённого чата: %s", text)
	}
	return got.ID
}

// blankStore читает память диалога с диска.
func blankStore(t *testing.T, home, id string) chatStore {
	t.Helper()
	data, err := os.ReadFile(blankPath(home, id))
	if err != nil {
		t.Fatalf("памяти диалога %s нет: %v", id, err)
	}
	var st chatStore
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// blankList спрашивает общий список машины и отдаёт строки.
func blankList(t *testing.T, e *testEnv, c *http.Client, query string) []chatEntry {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats"+query, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список чатов (%s): %d %s", query, resp.StatusCode, text)
	}
	var got struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	return got.Chats
}

func blankRow(list []chatEntry, id string) *chatEntry {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// Кнопка «+» заводит разговор сразу, без единой реплики, и он виден в списке.
// Сессию тут никто не поднимает: клиент стоит квоты, а человеку в эту минуту
// нужна строка в списке и поле, в которое можно писать.
func TestChatBlankStandsInListWithoutSay(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", "exit 0")
	id := blankMake(t, e, c, `{"model": "sonnet"}`)

	st := blankStore(t, e.home, id)
	if !st.Blank || st.Born == 0 || st.Project != "demo" || st.Model != "sonnet" {
		t.Fatalf("память диалога не помнит незачатый разговор: %+v", st)
	}
	row := blankRow(blankList(t, e, c, "?all=1"), id)
	if row == nil {
		t.Fatalf("заведённого разговора нет в общем списке машины")
	}
	if !row.Blank || row.State != chatNotStarted {
		t.Errorf("строка не назвалась незачатой: %+v", row)
	}
	if row.Model != "sonnet" || row.Mtime == "" || row.Project != "demo" {
		t.Errorf("строке незачатого разговора нечем подписаться: %+v", row)
	}
	// Проектный список тот же: панель ходит в общий, а точечные вопросы идут
	// без ключа, и пропадать там разговору не за чем.
	if blankRow(blankList(t, e, c, ""), id) == nil {
		t.Errorf("в проектном списке заведённого разговора нет")
	}
	// Сессия не поднималась: подъём это дело первой реплики.
	if _, err := os.Stat(filepath.Join(e.home, "tmux.log")); err == nil {
		t.Errorf("заведение чата подняло клиента, а поднимать его должна первая реплика")
	}
}

// Записей заводится сколько угодно, и каждая живёт своей строкой со своим
// набранным текстом. Прежде адрес нового чата был один на вкладку, и текст,
// набранный в одном новом чате, встречал человека в следующем.
func TestChatBlankKeepsOwnDraft(t *testing.T) {
	e, c := chatEnv(t)
	first := blankMake(t, e, c, `{"model": "opus"}`)
	second := blankMake(t, e, c, `{"model": "opus"}`)
	if first == second {
		t.Fatalf("две записи получили один ID: %s", first)
	}
	draft := func(id, text string) {
		t.Helper()
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+id+"/draft",
			`{"text": `+strconv.Quote(text)+`}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("черновик %s не записался: %d %s", id, resp.StatusCode, body(t, resp))
		}
	}
	draft(first, "разберись с расходом подписки")
	draft(second, "посмотри поезд слияния")

	list := blankList(t, e, c, "?all=1")
	one, two := blankRow(list, first), blankRow(list, second)
	if one == nil || two == nil {
		t.Fatalf("в списке не обе записи: %+v", list)
	}
	if one.Draft != "разберись с расходом подписки" {
		t.Errorf("первая запись потеряла свой черновик: %q", one.Draft)
	}
	if two.Draft != "посмотри поезд слияния" {
		t.Errorf("вторая запись взяла чужой черновик: %q", two.Draft)
	}
	// Черновик живёт при записи, а не во вкладке: он же говорит уборке, что
	// запись человеку нужна.
	if blankStore(t, e.home, first).Draft != "разберись с расходом подписки" {
		t.Errorf("черновик не лёг в память диалога")
	}
}

// Первая реплика поднимает сессию, и запись помнит, какую: сначала имя
// tmux-сессии, а как клиент назовётся в реестре, так и её ID. По этому полю
// панель, стоящая на старом адресе, переезжает на живой разговор.
func TestChatBlankGrowsIntoSession(t *testing.T) {
	e, c := chatEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) exit 1;;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")
	id := blankMake(t, e, c, `{"model": "opus", "id": "XR-4"}`)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+id+"/draft",
		`{"text": "недописанное"}`)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "почему поезд встал", "model": "opus", "chat": `+strconv.Quote(id)+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика не подняла сессию: %d %s", resp.StatusCode, body(t, resp))
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "почему поезд встал") {
		t.Fatalf("сессия поднята не репликой человека: %s", log)
	}
	// Задачу называет сама запись: заказ её не передавал, а разговор заводили
	// с экрана задачи, и сессия обязана подняться с её привязкой.
	if !strings.Contains(log, "DEVKIT_TASK='XR-4'") {
		t.Errorf("подъём потерял задачу записи: %s", log)
	}
	st := blankStore(t, e.home, id)
	if st.Tmux != "chat-XR-4-1" {
		t.Fatalf("запись не запомнила поднятую сессию: %+v", st)
	}
	if st.Draft != "" {
		t.Errorf("отправленная реплика осталась черновиком: %q", st.Draft)
	}
	if st.Grown != "" {
		t.Errorf("сессия ещё не назвалась в реестре, а запись уже пришита: %+v", st)
	}
	// Поиск по имени tmux ищет родившийся разговор, и подсовывать ему саму
	// запись нельзя: панель пришилась бы к тому, на чём и так стоит.
	if len(blankList(t, e, c, "?tmux=chat-XR-4-1")) != 0 {
		t.Errorf("незачатая запись отозвалась на поиск по имени tmux")
	}

	// Клиент назвался в реестре: имя tmux достаётся живой сессии, и запись
	// узнаёт своего наследника.
	born := "aaaa1111-2222-4333-8444-555566667777"
	writeBinds(t, e.home, "2026-08-18T12:03:11 сессия "+born+" задача XR-4 проект demo "+
		"дерево "+e.proj+" транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-4-1\n")
	row := blankRow(blankList(t, e, c, "?all=1"), id)
	if row == nil {
		t.Fatalf("запись пропала из списка раньше времени")
	}
	if row.Grown != born {
		t.Errorf("запись не пришилась к поднявшейся сессии: %+v", row)
	}
	if blankStore(t, e.home, id).Grown != born {
		t.Errorf("пришивание не пережило заход: память диалога о сессии молчит")
	}
}

// Брошенные записи не копятся мусором. Пустая, без единой буквы и без сессии,
// уходит через час: хранить в ней нечего, а копилась она сроком окна списка, и
// за день их набиралась горсть (замечание пользователя, замер дал пять штук за
// два с половиной часа). Начатое человеком живёт сколько угодно, а выросшая
// запись доживает дорожным знаком свои трое суток.
func TestChatBlankSweepsAbandoned(t *testing.T) {
	e, c := chatEnv(t)
	old := time.Now().Add(-(chatBlankLife + time.Hour)).Unix()
	// Час это срок пустой записи, а не всей уборки: дорожный знак выросшей
	// живёт своим сроком, и час его не трогает.
	if chatBlankLife != time.Hour {
		t.Fatalf("срок пустой записи %v, а решением он час", chatBlankLife)
	}
	stale := time.Now().Add(-(chatGrownLife + time.Hour)).Unix()
	put := func(id string, st chatStore) {
		t.Helper()
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(chatStoreDir(e.home), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blankPath(e.home, id), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("blank-forgotten", chatStore{Blank: true, Born: old, Project: "demo"})
	put("blank-written", chatStore{Blank: true, Born: old, Project: "demo", Draft: "недописанное"})
	put("blank-grown", chatStore{Blank: true, Born: stale, Project: "demo", Grown: "aaaa-0001"})
	put("blank-fresh-grown", chatStore{Blank: true, Born: old, Project: "demo", Grown: "aaaa-0002"})
	put("blank-today", chatStore{Blank: true, Born: time.Now().Unix(), Project: "demo"})

	list := blankList(t, e, c, "?all=1")
	if blankRow(list, "blank-forgotten") != nil {
		t.Errorf("брошенная пустая запись осталась в списке")
	}
	if _, err := os.Stat(blankPath(e.home, "blank-forgotten")); !os.IsNotExist(err) {
		t.Errorf("брошенная пустая запись осталась на диске: %v", err)
	}
	if blankRow(list, "blank-written") == nil {
		t.Errorf("запись с набранным текстом смело уборкой, а её человек писал")
	}
	if blankRow(list, "blank-grown") != nil {
		t.Errorf("выросшая запись осталась строкой рядом со своим разговором")
	}
	if _, err := os.Stat(blankPath(e.home, "blank-grown")); !os.IsNotExist(err) {
		t.Errorf("выросшая запись осталась на диске: %v", err)
	}
	// Свежая выросшая запись стоит дорожным знаком: панель соседней вкладки
	// переезжает по ней на живой разговор, и торопить её незачем.
	if _, err := os.Stat(blankPath(e.home, "blank-fresh-grown")); err != nil {
		t.Errorf("свежая выросшая запись стёрта, и панели на её адресе некуда идти: %v", err)
	}
	if blankRow(list, "blank-today") == nil {
		t.Errorf("сегодняшняя пустая запись смело уборкой: человек только что её завёл")
	}
}

// Запись закрывается рукой, и ждать часа для этого не надо: мусор человек видит
// сейчас («закрыть их я не могу, просто нет такой возможности», замечание
// пользователя). Живой сессии закрытие не касается: запись, за которой стоит
// сессия, ручка не трогает и говорит, куда идти.
func TestChatBlankDropsByHand(t *testing.T) {
	e, c := chatEnv(t)
	put := func(id string, st chatStore) {
		t.Helper()
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(chatStoreDir(e.home), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blankPath(e.home, id), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Unix()
	put("blank-empty", chatStore{Blank: true, Born: now, Project: "demo"})
	put("blank-typed", chatStore{Blank: true, Born: now, Project: "demo", Draft: "недописанное"})
	put("blank-live", chatStore{Blank: true, Born: now, Project: "demo", Tmux: "devkit-demo-1"})
	put("blank-owned", chatStore{Blank: true, Born: now, Project: "demo", Grown: "aaaa-0001"})

	drop := func(id string) (int, string) {
		t.Helper()
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+id+"/drop", "{}")
		return resp.StatusCode, body(t, resp)
	}

	// Пустая уходит первым же движением и с диска тоже: ждать её часа человеку
	// незачем.
	if code, said := drop("blank-empty"); code != http.StatusOK {
		t.Fatalf("пустая запись не закрылась рукой: %d %s", code, said)
	}
	if _, err := os.Stat(blankPath(e.home, "blank-empty")); !os.IsNotExist(err) {
		t.Errorf("закрытая запись осталась на диске: %v", err)
	}

	// Набранный текст закрытию не помеха: спрашивает о нём экран, а решение
	// человека ручка исполняет.
	if code, said := drop("blank-typed"); code != http.StatusOK {
		t.Fatalf("запись с текстом не закрылась рукой: %d %s", code, said)
	}

	// Запись с поднятой сессией остаётся на месте: снимать сессию походя
	// закрытие не вправе.
	code, said := drop("blank-live")
	if code != http.StatusConflict {
		t.Errorf("запись с живой сессией закрылась: %d %s", code, said)
	}
	if !strings.Contains(said, "devkit-demo-1") {
		t.Errorf("отказ не назвал сессию, из-за которой запись осталась: %s", said)
	}
	if _, err := os.Stat(blankPath(e.home, "blank-live")); err != nil {
		t.Errorf("запись с живой сессией всё же стёрлась: %v", err)
	}

	// Выросшая запись это дорожный знак к разговору, и закрывают сам разговор.
	code, said = drop("blank-owned")
	if code != http.StatusConflict {
		t.Errorf("выросшая запись закрылась мимо своего разговора: %d %s", code, said)
	}
	if !strings.Contains(said, "aaaa-0001") {
		t.Errorf("отказ не назвал разговор, в который выросла запись: %s", said)
	}

	// Повторное закрытие это не поломка: человек просил ровно того, что уже
	// сошлось.
	if code, said := drop("blank-empty"); code != http.StatusOK {
		t.Errorf("повторное закрытие отбито: %d %s", code, said)
	}
}

// Панель: предмет проверки тут собранная разметка и обработчики, поэтому
// статика поднимается в node с заглушкой DOM (стенд testdata/poc_chatblank.mjs).
// Он проходит весь путь человека: нажатие «+» заводит разговор на сервере, два
// разговора живут порознь со своими черновиками, первая реплика называет свою
// запись, а поднявшаяся сессия забирает разговор себе. Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticChatBlank(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд незачатого разговора пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatblank.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("незачатый разговор: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Пустой выбор моделей называет себя словами. Живой случай: «нельзя поменять
// модель в новом чате» (замечание пользователя). Список моделей дашборд не
// сочиняет, он целиком приезжает от agentctl, и когда лестницы ярусов в ответе
// нет, выпадающий список схлопывается в одну строку с текущей моделью. Молчание
// тут читается как «модель тут одна», хотя чинится это машинным слоем харнесов.
func TestChatModelsNoteWhenLadderEmpty(t *testing.T) {
	e, c := chatEnv(t)

	// Лестницы в ответе нет: подписки есть, ярусов нет.
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?all=1&days=0", "")
	text := body(t, resp)
	var got struct {
		Models []chatModelOpt `json:"models"`
		Note   string         `json:"models_note"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 0 {
		t.Fatalf("лестницы в фикстуре нет, а модели взялись: %+v", got.Models)
	}
	if got.Note == "" {
		t.Fatal("пустой выбор моделей молчит: человек читает его как «модель тут одна»")
	}
	if !strings.Contains(got.Note, "ярус") {
		t.Fatalf("причина пустого выбора не названа: %q", got.Note)
	}

	// Лестница приехала: причине взяться неоткуда, и её в ответе нет.
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	e.s.harn, e.s.harnBorn = nil, time.Time{}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?all=1&days=0", "")
	text = body(t, resp)
	got.Models, got.Note = nil, ""
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) == 0 {
		t.Fatal("лестница приехала, а моделей в ответе нет")
	}
	if got.Note != "" {
		t.Fatalf("выбор есть, а причина его пустоты всё равно приехала: %q", got.Note)
	}
}

package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Вход в Claude Design с плашки дашборда (DK-920). Токен обычного входа доступа
// к макетам не несёт: api.anthropic.com отвечает на него 403 и просит
// /design-login. Команды этой из дашборда не подать, и работа с макетами вставала
// до терминала на машине, а с телефона не шла вовсе.
//
// Стенд тот же, что у обычного входа (fakeTmuxLogin): скрипт tmux с памятью
// стадий, стадии диалога макетов durl, dok и dagain. Сам вход требует учётной
// записи человека и кода из браузера, и его закрывает прогон человеком; тут
// проверяются подъём своим диалогом, разбор отказа кода и два вида входа подряд.

// designStage читает стадию панели из памяти стенда: по ней видно, куда диалог
// ушёл после нажатия.
func designStage(t *testing.T, d string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(d, "stage"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// designSaid читает последний текст, поданный в сессию входа: команду вида или
// код.
func designSaid(t *testing.T, d string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(d, "last"))
	if err != nil {
		t.Fatalf("в сессию входа ничего не подавалось: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// Кнопка входа в Claude Design поднимает свой диалог: в сессию уходит
// /design-login, а не /login, ссылка приезжает человеку, и вид назван в ответе.
// Этим закрыты кейсы «человек с телефона входит в Claude Design кнопкой на
// плашке» и «тот же вход с машины из панели вместо терминала»: дорога у них одна
// и та же ручка, а расходятся они дорогой ответа (код или петля), которую
// сторожит стенд обычного входа.
func TestDesignLoginRaisesOwnDialog(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)

	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login",
		`{"kind":"design"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход в Claude Design не поднялся: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "https://claude.ai/oauth/authorize") {
		t.Fatalf("ссылка авторизации не приехала: %s", text)
	}
	if !strings.Contains(text, `"kind":"design"`) {
		t.Fatalf("вид входа в ответе не назван: %s", text)
	}
	if said := designSaid(t, d); said != "/design-login" {
		t.Fatalf("в сессию входа подана команда %q, ждал /design-login", said)
	}
	// Метка вида живёт при самой сессии: состояние подъёма умирает перезапуском
	// службы, а по виду решается, чьему диалогу достанется код авторизации.
	env, err := os.ReadFile(filepath.Join(d, "env-login-1"))
	if err != nil {
		t.Fatalf("метки сессии входа не нашлось: %v", err)
	}
	if !strings.Contains(string(env), loginWayVar+"=design") {
		t.Fatalf("вид входа не помечен при сессии: %s", env)
	}
}

// Код не принят: человеку сказано словами, сессия входа жива, а диалог макетов
// вернулся к полю кода, и вторая попытка идёт в ту же сессию. Без своего
// узнавателя тут был бы окончательный провал: поля кода на такой панели нет
// вовсе, и общий loginSaysFailed по слову invalid снял бы живую сессию входа.
func TestDesignLoginCodeRetriesWithoutLosingSession(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	sent := loginCalls(t, d, "calls")
	c := e.loggedClient(t)

	if resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login",
		`{"kind":"design"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("вход в Claude Design не поднялся: %d, %s", resp.StatusCode, text)
	}
	sent("kill-session")

	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"kind":"design","code":"BAD"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("отказ кода назван не отказом: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "код не принят") {
		t.Fatalf("человеку не сказано, что код не принят: %s", text)
	}
	if got := sent("kill-session"); got != 0 {
		t.Fatalf("сессия входа снята на повторяемом отказе кода: kill-session %d", got)
	}
	if stage := designStage(t, d); stage != "durl" {
		t.Fatalf("диалог не вернулся к полю кода: стадия %q, ждал durl", stage)
	}

	// Вторая попытка идёт той же ссылкой и кончается входом.
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"kind":"design","code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"ok":true`) {
		t.Fatalf("вторая попытка кода не прошла: %d, %s", resp.StatusCode, text)
	}
	if got := sent("kill-session"); got != 1 {
		t.Fatalf("одноразовая сессия входа не снята после успеха: kill-session %d", got)
	}
}

// Обычный вход и вход в Claude Design, поднятые подряд, друг другу не мешают:
// второй поднимает свою сессию вместо первой, а код первого вида в стоящий
// диалог второго не уедет. Цена промаха тут дорогая: код авторизации это
// одноразовый ключ учётной записи, и нажатия в чужой диалог ему дорога в один
// конец.
func TestDesignLoginAfterClientLoginRaisesAnew(t *testing.T) {
	e := newTestEnv(t)
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	// Счётчик у каждого слова свой: заход счётчика съедает хвост журнала
	// вызовов, и один на три слова считал бы пустоту.
	raised, dropped, keys := loginCalls(t, d, "calls"), loginCalls(t, d, "calls"),
		loginCalls(t, d, "calls")
	c := e.loggedClient(t)

	if resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login",
		"{}"); resp.StatusCode != http.StatusOK {
		t.Fatalf("обычный вход не поднялся: %d, %s", resp.StatusCode, text)
	}
	if said := designSaid(t, d); said != "/login" {
		t.Fatalf("обычный вход подал команду %q, ждал /login", said)
	}
	raised("new-session")
	dropped("kill-session")

	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login",
		`{"kind":"design"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"kind":"design"`) {
		t.Fatalf("вход в Claude Design не поднялся вторым: %d, %s", resp.StatusCode, text)
	}
	if got := raised("new-session"); got != 1 {
		t.Fatalf("сессий входа поднято %d, ждал одну новую", got)
	}
	if got := dropped("kill-session"); got != 1 {
		t.Fatalf("сессия обычного входа не снята: kill-session %d", got)
	}
	if said := designSaid(t, d); said != "/design-login" {
		t.Fatalf("второй вход подал команду %q, ждал /design-login", said)
	}

	// Экран обычного входа кода в этот диалог не отправит: вид не тот.
	keys("send-keys")
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"code":"GOOD"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("код обычного входа уехал в диалог макетов: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "другой вход") {
		t.Fatalf("отказ не называет причину видом входа: %s", text)
	}
	if got := keys("send-keys"); got != 0 {
		t.Fatalf("код уехал нажатиями мимо своего вида: send-keys %d", got)
	}

	// А своему виду ключ доезжает.
	resp, text = loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login/code",
		`{"kind":"design","code":"GOOD"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"ok":true`) {
		t.Fatalf("код входа в Claude Design не принят: %d, %s", resp.StatusCode, text)
	}
}

// Незнакомый вид входа отбивается словами, а не сводится молча к обычному:
// молчаливое сведение подало бы код в диалог, которого человек не просил.
func TestDesignLoginUnknownWayRefused(t *testing.T) {
	e := newTestEnv(t)
	fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)

	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login",
		`{"kind":"figma"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("незнакомый вид входа принят: %d, %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "figma") {
		t.Fatalf("отказ не называет вид, которого не знает: %s", text)
	}
}

// designAttach это запись транскрипта, которой клиент говорит об отказе
// подключения серверов MCP: её дашборд и так читает, и признак берётся из неё, а
// не из прогона claude mcp list и не из слов панели tmux.
func designAttach(failed, at string) string {
	return `{"type":"attachment","attachment":{"pendingMcpServers":[],` +
		`"failedMcpServers":` + failed + `},"timestamp":"` + at + `"}` + "\n"
}

// designFailed это отказ сервера макетов в том виде, в каком его пишет клиент.
const designFailed = `[{"name":"claude-design","errorCode":"FIRST_PARTY_AUTH_REJECTED",` +
	`"error":"api.anthropic.com rejected your claude.ai login for Claude Design (HTTP 403). Run /design-login and retry"}]`

// Строка списка называет отказ сервера макетов словами, и от разлогина он
// отличается: вход в клиента жив, молчит одна работа с макетами. Ответ агента
// признак не гасит (сервер молчит до конца жизни процесса), а свежий заход без
// отказа снимает его сам. Этим закрыт кейс «сессия с отказом claude-design
// поднимает плашку сама»: поле разговора и есть признак подъёма блока.
func TestChatEntryDesignGone(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd9200-9200-4200-8200-920092009200"
	said := func(text, at string) string {
		return `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4",` +
			`"content":[{"type":"text","text":"` + text + `"}]},"timestamp":"` + at + `"}` + "\n"
	}
	talk := designAttach(designFailed, "2026-09-15T20:32:46.138Z") + plainTalk
	writeSession(t, e.home, e.proj, "", sid, talk, time.Now())

	got := chatsOf(t, e, c)
	if len(got) != 1 {
		t.Fatalf("чатов в списке %d, ждал один: %+v", len(got), got)
	}
	if got[0].Design != designGoneWord {
		t.Fatalf("отказ сервера макетов не назван словами: design=%q, ждал %q",
			got[0].Design, designGoneWord)
	}
	if got[0].Login != "" {
		t.Errorf("отказ макетов выдан за разлогин: login=%q, а вход в клиента тут жив", got[0].Login)
	}

	// Агент ответил по делу: сервер макетов от этого не подключился.
	live := talk + said("макет не собрать, сервер не отвечает", "2026-09-15T20:40:00.000Z")
	writeSession(t, e.home, e.proj, "", sid, live, time.Now().Add(time.Second))
	if got := chatsOf(t, e, c); got[0].Design != designGoneWord {
		t.Errorf("ответ агента погасил отказ макетов: design=%q", got[0].Design)
	}

	// Вход сделан, разговор поднят заново: свежая запись о серверах отказа не
	// несёт, и признак гаснет.
	fresh := live + designAttach("[]", "2026-09-15T20:45:00.000Z")
	writeSession(t, e.home, e.proj, "", sid, fresh, time.Now().Add(2*time.Second))
	if got := chatsOf(t, e, c); got[0].Design != "" {
		t.Errorf("признак не погас после входа в макеты: design=%q", got[0].Design)
	}

}

// Продолженный разговор: процесс поднялся посреди файла, и запись о серверах
// лежит под работой захода. Ни шапкой, ни хвостом такую запись не достать, и
// строка списка про отказ молчала, а снятый отказ возвращался, едва после входа
// набегала четверть мегабайта (находка ревью).
//
// Каждый случай берёт своё окружение и свой транскрипт: шапку дочитанного файла
// процесс помнит по headTTL, и вторая правка того же файла доехала бы до списка
// только сроком памяти, а предмет тут не память, а сам разбор.
func TestChatEntryDesignGoneMidFile(t *testing.T) {
	pad := designPad(metaScanLimit + 32*1024)
	fail := designAttach(designFailed, "2026-09-15T21:10:00.000Z")
	ok := designAttach("[]", "2026-09-15T21:20:00.000Z")
	cases := []struct {
		name string
		talk string
		want string
	}{
		{"отказ под работой захода", plainTalk + pad + fail + pad, designGoneWord},
		{"вход сделан под работой захода", plainTalk + fail + pad + ok + pad, ""},
	}
	for _, c := range cases {
		e, cl := chatEnv(t)
		sid := "dddd9201-9201-4201-8201-920192019201"
		writeSession(t, e.home, e.proj, "", sid, c.talk, time.Now())
		got := chatsOf(t, e, cl)
		if len(got) != 1 {
			t.Fatalf("%s: чатов в списке %d, ждал один", c.name, len(got))
		}
		if got[0].Design != c.want {
			t.Errorf("%s: design=%q, ждал %q", c.name, got[0].Design, c.want)
		}
	}
}

// Признак отказа едет и в ответе о состоянии разговора. Панель собирает блок
// входа один раз, а отказ приходит и позже: разговор умер и поднялся заново уже
// без сервера макетов. Этим ответом блок встаёт при открытой панели, не дожидаясь
// переоткрытия разговора (замечание ревью).
func TestChatStatusTellsDesignGone(t *testing.T) {
	e := newTestEnv(t)
	sid := "dddd9202-9202-4202-8202-920292029202"
	talk := designAttach(designFailed, "2026-09-15T20:32:46.138Z") + plainTalk
	writeSession(t, e.home, e.proj, "", sid, talk, time.Now())

	// Процесса у разговора нет: он и умер, а признак человеку нужен тем более.
	got := chatStatus(t, e, sid)
	if !got.Design {
		t.Fatalf("отказ сервера макетов в состоянии разговора не назван: %+v", got)
	}
	if got.Login {
		t.Errorf("отказ макетов выдан за разлогин: %+v", got)
	}

	// Вход сделан, свежий заход отказа не несёт: признак гаснет и тут.
	fresh := "dddd9203-9203-4203-8203-920392039203"
	writeSession(t, e.home, e.proj, "", fresh,
		talk+designAttach("[]", "2026-09-15T20:45:00.000Z"), time.Now())
	if got := chatStatus(t, e, fresh); got.Design {
		t.Errorf("признак не погас после входа в макеты: %+v", got)
	}
}

// Разбор записи о серверах: отказ узнаётся по имени сервера, запись без поля
// перечня в расчёт не идёт вовсе, а считается последняя запись, потому что свою
// пишет каждый заход процесса.
func TestDesignOfReadsLastRecord(t *testing.T) {
	fail := designAttach(designFailed, "2026-09-15T20:32:46.138Z")
	ok := designAttach("[]", "2026-09-15T20:45:00.000Z")
	other := designAttach(`[{"name":"claude.ai Gmail","errorCode":"CONNECT_FAILED"}]`,
		"2026-09-15T20:33:00.000Z")
	// Запись attachment без перечня серверов: их у клиента много, и о серверах
	// говорит только та, где поле стоит.
	empty := `{"type":"attachment","attachment":{"type":"deferred_tools_delta"},` +
		`"timestamp":"2026-09-15T20:50:00.000Z"}` + "\n"

	cases := []struct {
		name string
		data string
		want designSeen
	}{
		{"записи нет", plainTalk, designUnseen},
		{"перечня нет", plainTalk + empty, designUnseen},
		{"отказ макетов", plainTalk + fail, designOff},
		{"чужой отказ", plainTalk + other, designUp},
		{"вход сделан после отказа", fail + ok, designUp},
		{"отказ после успеха", ok + fail, designOff},
		{"запись без перечня отказ не снимает", fail + empty, designOff},
	}
	for _, c := range cases {
		if got := designOf([]byte(c.data)); got != c.want {
			t.Errorf("%s: разбор дал %d, ждал %d", c.name, got, c.want)
		}
	}
}

// designPad это работа разговора между записями о серверах: столько байт
// записей, сколько сказано, и ни байтом больше. Длина тут точная нарочно: по
// ней стенд ставит границу обратного чтения ровно посреди записи.
func designPad(n int) string {
	line := func(size int) string {
		head := `{"type":"user","message":{"role":"user","content":"`
		tail := `"},"timestamp":"2026-09-15T20:35:00.000Z"}` + "\n"
		fill := size - len(head) - len(tail)
		if fill < 0 {
			return strings.Repeat("x", size-1) + "\n"
		}
		return head + strings.Repeat("x", fill) + tail
	}
	var b strings.Builder
	for n-b.Len() >= 512 {
		b.WriteString(line(512))
	}
	if rest := n - b.Len(); rest > 0 {
		b.WriteString(line(rest))
	}
	return b.String()
}

// Запись о серверах ищется обратным чтением по всему файлу, а не в двух
// форточках по концам. Прежде смотрели шапку и хвост по четверти мегабайта, и
// запись середины не видел никто: у живого транскрипта в 43 МБ записи стояли на
// 35-37 МБ, отказ продолженного разговора блока не поднимал, а снятый отказ
// возвращался, едва после входа набегала четверть мегабайта (находка ревью).
func TestDesignLastFindsRecordMidFile(t *testing.T) {
	fail := designAttach(designFailed, "2026-09-15T20:32:46.138Z")
	ok := designAttach("[]", "2026-09-15T20:45:00.000Z")
	// Форточка была в metaScanLimit с каждой стороны, и работы тут больше неё:
	// запись лежит там, где её не видит ни шапка, ни хвост.
	pad := designPad(metaScanLimit + 32*1024)
	cases := []struct {
		name string
		data string
		want designSeen
	}{
		{"отказ посреди файла", pad + fail + pad, designOff},
		{"вход сделан посреди файла, отказ в шапке", fail + pad + ok + pad, designUp},
		{"отказ посреди файла, успех в шапке", ok + pad + fail + pad, designOff},
		{"записи нет вовсе", pad + pad, designUnseen},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "talk.jsonl")
		if err := os.WriteFile(path, []byte(c.data), 0o644); err != nil {
			t.Fatalf("%s: транскрипт стенда не записался: %v", c.name, err)
		}
		if got := designLast(path); got != c.want {
			t.Errorf("%s: разбор дал %d, ждал %d", c.name, got, c.want)
		}
	}
}

// Запись на стыке кусков обратного чтения: читается файл с конца шагами по
// designScanChunk, и строка, разорванная границей, склеивается хвостом. Без
// склейки такая запись терялась бы целиком, а стоит она в живом файле где
// придётся.
func TestDesignLastGluesRecordOnChunkEdge(t *testing.T) {
	rec := designAttach(designFailed, "2026-09-15T20:32:46.138Z")
	// Хвост после записи короче куска ровно на половину её длины: граница
	// первого куска тогда ложится посреди самой записи.
	data := designPad(64*1024) + rec + designPad(designScanChunk-len(rec)/2)
	path := filepath.Join(t.TempDir(), "talk.jsonl")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("транскрипт стенда не записался: %v", err)
	}
	if got := designLast(path); got != designOff {
		t.Fatalf("запись на стыке кусков потеряна: разбор дал %d, ждал %d", got, designOff)
	}
}

// Панель: предмет проверки тут собранная разметка и тело запросов, поэтому
// статика поднимается в node с заглушкой DOM (стенд
// testdata/poc_designlogin.mjs). Он проходит путь человека: отказ сервера
// макетов поднимает блок сам, кнопка называет свой вход, вид уезжает в теле всех
// трёх ручек, ответ агента блок не гасит, а разлогин клиента остался прежним
// входом. Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticDesignLoginTalk(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд входа в Claude Design пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_designlogin.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("вход в Claude Design в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

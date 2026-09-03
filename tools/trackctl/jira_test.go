package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Адаптер проверяется по образцам ответов из testdata/jira, разложенным на
// подставном сервере: в сеть тесты не ходят, а мимо подставного сервера
// адаптеру ходить нечем, за этим следит hostGuard.

type jiraReq struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   string
}

type jiraResp struct {
	code     int
	file     string
	body     string
	redirect string
}

type jiraStub struct {
	t    *testing.T
	srv  *httptest.Server
	reqs []jiraReq
	resp map[string]jiraResp
}

// hostGuard рвёт любой запрос мимо подставного сервера: адаптер с прошитым в
// коде адресом (или с походом в настоящий Atlassian) обязан упасть в тесте, а
// не молча уехать в сеть.
type hostGuard struct {
	allow string
	inner http.RoundTripper
}

func (g *hostGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != g.allow {
		return nil, fmt.Errorf("тест: адаптер полез на %s, а разрешён только %s", r.URL.Host, g.allow)
	}
	return g.inner.RoundTrip(r)
}

func newJiraStub(t *testing.T) (*jiraStub, *jiraAdapter) {
	t.Helper()
	return newJiraStubVer(t, "")
}

// newJiraStubVer собирает стенд на контуре с названной версией API: пустая
// это умолчание v3, «2» это контур Server/DC. Хост-страж стоит тот же: мимо
// стенда адаптеру ходить нечем при любой версии.
func newJiraStubVer(t *testing.T, api string) (*jiraStub, *jiraAdapter) {
	t.Helper()
	st := &jiraStub{t: t, resp: map[string]jiraResp{}}
	st.srv = httptest.NewServer(http.HandlerFunc(st.serve))
	t.Cleanup(st.srv.Close)
	t.Setenv("JIRA_TOKEN", "s3cr3t-token")
	c := &contour{
		Name:      "corp",
		Adapter:   "jira",
		BaseURL:   st.srv.URL,
		TokenEnv:  "JIRA_TOKEN",
		User:      "5b10ac8d82e05b22cc7d4ef5",
		RankField: "customfield_100",
		API:       api,
	}
	a, err := newAdapter(c)
	if err != nil {
		t.Fatal(err)
	}
	j, ok := a.(*jiraAdapter)
	if !ok {
		t.Fatalf("таблица отдала по имени jira не тот адаптер: %T", a)
	}
	j.client.Transport = &hostGuard{allow: st.srv.Listener.Addr().String(), inner: http.DefaultTransport}
	return st, j
}

func (s *jiraStub) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	s.reqs = append(s.reqs, jiraReq{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Auth:   r.Header.Get("Authorization"),
		Body:   string(raw),
	})
	resp, ok := s.resp[r.Method+" "+r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	code := resp.code
	if code == 0 {
		code = http.StatusOK
	}
	if resp.redirect != "" {
		w.Header().Set("Location", resp.redirect)
	}
	data := []byte(resp.body)
	if resp.file != "" {
		var err error
		data, err = os.ReadFile(filepath.Join("testdata", "jira", resp.file))
		if err != nil {
			s.t.Fatal(err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func (s *jiraStub) on(method, path string, resp jiraResp) {
	s.resp[method+" "+path] = resp
}

func (s *jiraStub) last() jiraReq {
	s.t.Helper()
	if len(s.reqs) == 0 {
		s.t.Fatal("адаптер не сделал ни одного запроса")
	}
	return s.reqs[len(s.reqs)-1]
}

// bodyMap разбирает тело запроса обратно в дерево: сравнивать тела строками
// значит завязаться на порядок ключей, которого у JSON нет.
func bodyMap(t *testing.T, req jiraReq) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(req.Body), &m); err != nil {
		t.Fatalf("тело %s %s не разобралось: %v (%s)", req.Method, req.Path, err, req.Body)
	}
	return m
}

func TestJiraInAdapterTable(t *testing.T) {
	found := false
	for _, n := range adapterNames() {
		if n == "jira" {
			found = true
		}
	}
	if !found {
		t.Fatalf("адаптера jira нет в таблице: %v", adapterNames())
	}
	_, j := newJiraStub(t)
	if miss := missingOptional(j); len(miss) != 0 {
		t.Fatalf("у jira должны быть обе необязательные операции, нет: %v", miss)
	}
}

func TestJiraFetch(t *testing.T) {
	st, j := newJiraStub(t)
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1", jiraResp{file: "issue.json"})
	got, err := j.fetch("ED-1")
	if err != nil {
		t.Fatal(err)
	}
	want := ticket{Key: "ED-1", Status: "In Progress", Type: "Bug", Title: "My first example issue", Estimate: "10m", URL: st.srv.URL + "/browse/ED-1"}
	if got != want {
		t.Fatalf("тикет из образца разобран не так:\nхочу %+v\nимею %+v", want, got)
	}
	if q := st.last().Query; !strings.Contains(q, "timetracking") || !strings.Contains(q, "description") {
		t.Fatalf("fetch не спросил нужные поля: %q", q)
	}
}

// Description приезжает по-разному на v2 (строка) и v3 (документ ADF): без
// разбора обоих форматов файл задачи строки ревью остался бы без постановки
// на одной из версий Jira молча.
func TestJiraFetchDescriptionV2String(t *testing.T) {
	st, j := newJiraStubVer(t, "2")
	st.on(http.MethodGet, "/rest/api/2/issue/ED-1", jiraResp{file: "issue-v2-desc.json"})
	got, err := j.fetch("ED-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "просто строка описания" {
		t.Fatalf("описание v2 разобрано не так: %q", got.Description)
	}
}

func TestJiraFetchDescriptionV3ADF(t *testing.T) {
	st, j := newJiraStub(t)
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1", jiraResp{file: "issue-v3-desc.json"})
	got, err := j.fetch("ED-1")
	if err != nil {
		t.Fatal(err)
	}
	want := "первый абзац\nвторой абзац"
	if got.Description != want {
		t.Fatalf("описание v3 (ADF) разобрано не так:\nхочу %q\nимею %q", want, got.Description)
	}
}

func TestJiraTransitionCarriesSectionFields(t *testing.T) {
	st, j := newJiraStub(t)
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1/transitions", jiraResp{file: "transitions.json"})
	fields := map[string]string{
		"reason":     "плановая работа",
		"resolution": `{"name": "Fixed"}`,
	}
	if err := j.transition("ED-1", "In Progress", fields); err != nil {
		t.Fatal(err)
	}
	if len(st.reqs) != 2 {
		t.Fatalf("хочу список переходов и сам переход, имею %d запросов", len(st.reqs))
	}
	body := bodyMap(t, st.last())
	tr, _ := body["transition"].(map[string]any)
	if tr == nil || tr["id"] != "2" {
		t.Fatalf("переход выбран не по списку образца: %v", body["transition"])
	}
	sent, _ := body["fields"].(map[string]any)
	if sent == nil {
		t.Fatalf("поля секции [fields_*] не уехали в тело перехода: %s", st.last().Body)
	}
	if sent["reason"] != "плановая работа" {
		t.Fatalf("строковое поле секции потерялось: %v", sent["reason"])
	}
	res, _ := sent["resolution"].(map[string]any)
	if res == nil || res["name"] != "Fixed" {
		t.Fatalf("поле-объект уехало не объектом: %v", sent["resolution"])
	}
}

func TestJiraTransitionRefusedListsAvailable(t *testing.T) {
	st, j := newJiraStub(t)
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1/transitions", jiraResp{file: "transitions.json"})
	err := j.transition("ED-1", "Review", nil)
	var refused *transitionRefused
	if !errors.As(err, &refused) {
		t.Fatalf("хочу отказ воркфлоу, имею %v", err)
	}
	if strings.Join(refused.Available, ",") != "In Progress,Closed" {
		t.Fatalf("отказ перечислил доступные переходы не по образцу: %v", refused.Available)
	}
	for _, want := range []string{"In Progress", "Closed", "Review"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в тексте отказа нет %q: %s", want, err)
		}
	}
	if len(st.reqs) != 1 {
		t.Fatalf("после отказа адаптер полез делать переход: %d запросов", len(st.reqs))
	}
}

func TestJiraAssignEstimateWorklogComment(t *testing.T) {
	st, j := newJiraStub(t)
	if err := j.assign("ED-1", "5b10ac8d82e05b22cc7d4ef5"); err != nil {
		t.Fatal(err)
	}
	if req := st.last(); req.Method != http.MethodPut || req.Path != "/rest/api/3/issue/ED-1/assignee" {
		t.Fatalf("assign ушёл не туда: %s %s", req.Method, req.Path)
	}
	if got := bodyMap(t, st.last())["accountId"]; got != "5b10ac8d82e05b22cc7d4ef5" {
		t.Fatalf("assign не передал пользователя контура: %v", got)
	}

	if err := j.estimate("ED-1", "4h"); err != nil {
		t.Fatal(err)
	}
	est := bodyMap(t, st.last())
	fields, _ := est["fields"].(map[string]any)
	tt, _ := fields["timetracking"].(map[string]any)
	if tt == nil || tt["originalEstimate"] != "4h" {
		t.Fatalf("оценка ушла не в timetracking.originalEstimate: %s", st.last().Body)
	}

	st.on(http.MethodPost, "/rest/api/3/issue/ED-1/worklog", jiraResp{code: http.StatusCreated, file: "worklog-created.json"})
	if err := j.worklog("ED-1", "2026-08-04", "3h"); err != nil {
		t.Fatal(err)
	}
	wl := bodyMap(t, st.last())
	if wl["started"] != "2026-08-04T12:00:00.000+0000" || wl["timeSpent"] != "3h" {
		t.Fatalf("ворклог собран не по контракту: %s", st.last().Body)
	}

	before := len(st.reqs)
	if err := j.worklog("ED-1", "вчера", "3h"); err == nil {
		t.Fatal("хочу отказ на дате не того формата")
	}
	if len(st.reqs) != before {
		t.Fatal("адаптер отправил ворклог с негодной датой")
	}

	st.on(http.MethodPost, "/rest/api/3/issue/ED-1/comment", jiraResp{code: http.StatusCreated, file: "comment-created.json"})
	if err := j.comment("ED-1", "текст"); err != nil {
		t.Fatal(err)
	}
	cm := bodyMap(t, st.last())
	doc, _ := cm["body"].(map[string]any)
	if doc == nil || doc["type"] != "doc" {
		t.Fatalf("комментарий ушёл не документом ADF: %s", st.last().Body)
	}
}

// Имя поля ранга приезжает из контура, и прошить его в коде нельзя: у каждой
// компании свои customfield, и это чувствительное. Двумя разными именами
// подряд ловится ровно такая мутация.
func TestJiraRankFieldFromContour(t *testing.T) {
	st, j := newJiraStub(t)
	for _, field := range []string{"customfield_20021", "priority_score"} {
		if err := j.rank("ED-1", field, 42); err != nil {
			t.Fatal(err)
		}
		fields, _ := bodyMap(t, st.last())["fields"].(map[string]any)
		if fields == nil || len(fields) != 1 {
			t.Fatalf("ранг ушёл не одним полем: %s", st.last().Body)
		}
		v, ok := fields[field]
		if !ok {
			t.Fatalf("ранг ушёл не в поле %s: %s", field, st.last().Body)
		}
		if n, _ := v.(float64); int(n) != 42 {
			t.Fatalf("ранг ушёл не числом: %v", v)
		}
	}
	if err := j.rank("ED-1", "", 42); err == nil {
		t.Fatal("хочу отказ, когда контур поля не назвал")
	}
}

// Отказ трекера доносится как есть: свой текст «что-то пошло не так» скрыл бы
// и код ответа, и то самое обязательное поле, из-за которого Jira отказала.
func TestJiraErrorCarriesCodeAndText(t *testing.T) {
	st, j := newJiraStub(t)
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1/transitions", jiraResp{file: "transitions.json"})
	st.on(http.MethodPost, "/rest/api/3/issue/ED-1/transitions", jiraResp{code: http.StatusBadRequest, file: "error-required-field.json"})
	err := j.transition("ED-1", "In Progress", nil)
	if err == nil {
		t.Fatal("хочу отказ трекера")
	}
	for _, want := range []string{"400", "Field 'priority' is required", "/rest/api/3/issue/ED-1/transitions"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %s", want, err)
		}
	}

	st.on(http.MethodPut, "/rest/api/3/issue/ED-1", jiraResp{code: http.StatusBadRequest, file: "error-fields.json"})
	err = j.estimate("ED-1", "4h")
	if err == nil {
		t.Fatal("хочу отказ трекера")
	}
	for _, want := range []string{"issuetype: The issue type selected is invalid.", "project: Sub-tasks"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %s", want, err)
		}
	}

	st.on(http.MethodPut, "/rest/api/3/issue/ED-1/assignee", jiraResp{code: http.StatusForbidden})
	err = j.assign("ED-1", "5b10ac8d82e05b22cc7d4ef5")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("пустой отказ должен доносить хотя бы код: %v", err)
	}
}

// Токен живёт только в заголовке Authorization: в теле запроса, в пути и в
// тексте ошибки ему делать нечего, а ошибка уезжает в вывод команды и в глаза
// диспетчеру.
func TestJiraTokenStaysInHeader(t *testing.T) {
	st, j := newJiraStub(t)
	const token = "s3cr3t-token"
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1", jiraResp{file: "issue.json"})
	st.on(http.MethodGet, "/rest/api/3/issue/ED-1/transitions", jiraResp{file: "transitions.json"})
	if _, err := j.fetch("ED-1"); err != nil {
		t.Fatal(err)
	}
	if err := j.transition("ED-1", "In Progress", map[string]string{"assignee": "{user}"}); err != nil {
		t.Fatal(err)
	}
	if err := j.worklog("ED-1", "2026-08-04", "3h"); err != nil {
		t.Fatal(err)
	}
	for _, req := range st.reqs {
		if req.Auth != "Bearer "+token {
			t.Fatalf("запрос %s %s ушёл без токена в заголовке: %q", req.Method, req.Path, req.Auth)
		}
		if strings.Contains(req.Body, token) || strings.Contains(req.Path, token) || strings.Contains(req.Query, token) {
			t.Fatalf("токен уехал в запрос %s %s: %s?%s %s", req.Method, req.Path, req.Path, req.Query, req.Body)
		}
	}
	st.on(http.MethodPut, "/rest/api/3/issue/ED-1", jiraResp{code: http.StatusUnauthorized, body: `{"errorMessages":["Client must be authenticated"],"errors":{}}`})
	err := j.estimate("ED-1", "4h")
	if err == nil {
		t.Fatal("хочу отказ трекера")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("токен всплыл в тексте ошибки: %s", err)
	}
}

// Токен читается на запросе, а не при сборке адаптера: без этого status в
// проекте без токена не сказал бы про адаптер вовсе, а он должен называть его
// и перечень отсутствующих операций.
func TestJiraStatusWithoutToken(t *testing.T) {
	contourText := strings.Replace(contourFile, `adapter = "fake"`, `adapter = "jira"`, 1)
	contourText = strings.Replace(contourText, `token_env = "FAKE_TOKEN"`, `token_env = "JIRA_NO_SUCH_TOKEN"`, 1)
	root := setupEnv(t, contourText, bindingFile)
	out, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "адаптер:\tjira") {
		t.Fatalf("status не назвал адаптер: %s", out)
	}
	if !strings.Contains(out, "операции:\tвсе на месте") {
		t.Fatalf("status не увидел необязательные операции jira: %s", out)
	}
	if !strings.Contains(out, "JIRA_NO_SUCH_TOKEN") {
		t.Fatalf("status промолчал про отсутствующий токен: %s", out)
	}
}

// Версия API приезжает из контура, а не из кода. Неизвестное значение
// отбивается на сборке адаптера, до первого запроса: превратившись в путь,
// оно умерло бы посреди работы 404 или перенаправлением в веб-логин.
func TestJiraAPIVersionFromContour(t *testing.T) {
	for _, bad := range []string{"4", "v9", "cloud"} {
		c := &contour{Name: "corp", Adapter: "jira", BaseURL: "https://tracker.example", User: "u", API: bad, Path: "/конфиг/корп.local"}
		_, err := newAdapter(c)
		if err == nil {
			t.Fatalf("версия %q принята молча", bad)
		}
		for _, want := range []string{"api_version", bad, "2 или 3"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("в отказе по версии %q нет %q: %s", bad, want, err)
			}
		}
	}
}

// Контур Server/DC на v2: все оси адаптера ходят путями второй версии, а
// тела расходятся с v3 ровно в двух местах, исполнителе и комментарии.
func TestJiraV2Axes(t *testing.T) {
	st, j := newJiraStubVer(t, "2")
	st.on(http.MethodGet, "/rest/api/2/issue/ED-1", jiraResp{file: "issue.json"})
	got, err := j.fetch("ED-1")
	if err != nil {
		t.Fatal(err)
	}
	// Ключ сверяется по полям образца, а не по запрошенному: fetch молча
	// подставляет запрошенный ключ в пустой ответ, и проверка по нему
	// срабатывала бы всегда.
	if got.Status != "In Progress" || got.Title != "My first example issue" {
		t.Fatalf("чтение по v2 не собралось из образца: %+v", got)
	}

	st.on(http.MethodGet, "/rest/api/2/issue/ED-1/transitions", jiraResp{file: "transitions.json"})
	if err := j.transition("ED-1", "In Progress", nil); err != nil {
		t.Fatal(err)
	}
	if req := st.last(); req.Path != "/rest/api/2/issue/ED-1/transitions" {
		t.Fatalf("переход ушёл не путём v2: %s %s", req.Method, req.Path)
	}

	if err := j.assign("ED-1", "elena.vnukova"); err != nil {
		t.Fatal(err)
	}
	if req := st.last(); req.Path != "/rest/api/2/issue/ED-1/assignee" {
		t.Fatalf("assign ушёл не путём v2: %s %s", req.Method, req.Path)
	}
	body := bodyMap(t, st.last())
	if got := body["name"]; got != "elena.vnukova" {
		t.Fatalf("исполнитель на v2 зовётся name, а не accountId: %s", st.last().Body)
	}
	if _, has := body["accountId"]; has {
		t.Fatalf("ключ accountId уехал на v2: %s", st.last().Body)
	}

	st.on(http.MethodPost, "/rest/api/2/issue/ED-1/comment", jiraResp{code: http.StatusCreated})
	if err := j.comment("ED-1", "текст"); err != nil {
		t.Fatal(err)
	}
	if got := bodyMap(t, st.last())["body"]; got != "текст" {
		t.Fatalf("комментарий на v2 уехал не строкой: %s", st.last().Body)
	}

	if err := j.estimate("ED-1", "4h"); err != nil {
		t.Fatal(err)
	}
	if req := st.last(); req.Path != "/rest/api/2/issue/ED-1" {
		t.Fatalf("оценка ушла не путём v2: %s %s", req.Method, req.Path)
	}

	st.on(http.MethodPost, "/rest/api/2/issue/ED-1/worklog", jiraResp{code: http.StatusCreated, file: "worklog-created.json"})
	if err := j.worklog("ED-1", "2026-08-04", "3h"); err != nil {
		t.Fatal(err)
	}
	if req := st.last(); req.Path != "/rest/api/2/issue/ED-1/worklog" {
		t.Fatalf("ворклог ушёл не путём v2: %s %s", req.Method, req.Path)
	}

	if err := j.rank("ED-1", "customfield_100", 42); err != nil {
		t.Fatal(err)
	}
	for _, req := range st.reqs {
		if strings.HasPrefix(req.Path, "/rest/api/3") {
			t.Fatalf("на контуре v2 уехал путь третьей версии: %s %s", req.Method, req.Path)
		}
	}
}

// v3 без контурного ключа остаётся на ADF и accountId: умолчание не меняет
// поведения стоящих контуров Cloud.
func TestJiraV3DefaultBodies(t *testing.T) {
	st, j := newJiraStub(t)
	st.on(http.MethodPost, "/rest/api/3/issue/ED-1/comment", jiraResp{code: http.StatusCreated})
	if err := j.comment("ED-1", "текст"); err != nil {
		t.Fatal(err)
	}
	if _, isDoc := bodyMap(t, st.last())["body"].(map[string]any); !isDoc {
		t.Fatalf("комментарий умолчания уехал не документом ADF: %s", st.last().Body)
	}
	if err := j.assign("ED-1", "5b10ac8d82e05b22cc7d4ef5"); err != nil {
		t.Fatal(err)
	}
	if got := bodyMap(t, st.last())["accountId"]; got != "5b10ac8d82e05b22cc7d4ef5" {
		t.Fatalf("исполнитель умолчания уехал не по accountId: %s", st.last().Body)
	}
}

// Живой Server на путь отсутствующей версии API отвечает 302 на веб-логин:
// тихое следование за ним превращало запись в молчаливый успех, потому
// перенаправление не проходят, а отдаются отказом с адресом и ключом контура.
func TestJiraRedirectRefused(t *testing.T) {
	st, j := newJiraStub(t)
	login := st.srv.URL + "/login.jsp?permissionViolation=true"
	st.on(http.MethodPut, "/rest/api/3/issue/ED-1/assignee", jiraResp{code: http.StatusFound, redirect: login})
	err := j.assign("ED-1", "5b10ac8d82e05b22cc7d4ef5")
	if err == nil {
		t.Fatal("перенаправление в веб-логин сошло за успех")
	}
	for _, want := range []string{"302", "login.jsp", "api_version"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе по перенаправлению нет %q: %s", want, err)
		}
	}
	if len(st.reqs) != 1 {
		t.Fatalf("адаптер пошёл дальше за перенаправлением: %d запросов", len(st.reqs))
	}
}

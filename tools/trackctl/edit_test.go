package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Правка проверяется с двух сторон: команда на подложном адаптере (след
// вызовов, порядок осей, отказ до первого запроса) и каждая ось против стенда
// jira (ожидаемый запрос). Ожидания запросов выведены по контракту REST API,
// а не сняты с адаптера: иначе тест сходился бы при любом его поведении.

const contourFullFile = `adapter = "fake-full"
base_url = "https://tracker.example"
token_env = "FAKE_TOKEN"
user = "ivanov"
cost_s = "4h"
cost_m = "1d"
cost_l = "3d"
rank_field = "customfield_100"

[status]
backlog = ["Open", "Backlog"]
in_progress = ["In Progress", "Development"]
check = ["Review", "Testing"]
blocked = ["Blocked", "On Hold"]
done = ["Done", "Closed", "Rejected"]

[fields_in_progress]
assignee = "{user}"
reason = "плановая работа"

[fields_check]
reviewer = "{user}"
`

// Без ключей команда не делает ничего: ни одного вызова адаптера, отказ
// называет ключи, которыми ось называется.
func TestEditWithoutAxesRefused(t *testing.T) {
	root := setupEnv(t, contourFullFile, bindingFile)
	_, err := cmdEdit(root, "ABC-12", editAxes{})
	if err == nil || !strings.Contains(err.Error(), "не названо ни одной оси") {
		t.Fatalf("жду отказ без осей, имею %v", err)
	}
	if len(fakeState.calls) != 0 {
		t.Fatalf("без осей не должно быть ни одного вызова: %v", fakeState.calls)
	}
}

// Названная ось уезжает, неназванная не трогается, порядок фиксированный:
// исполнитель, оценка, поля, переход, комментарий.
func TestEditEveryAxisFixedOrder(t *testing.T) {
	root := setupEnv(t, contourFullFile, bindingFile)
	ax := editAxes{
		assignee: "petrov",
		estimate: "6h",
		title:    "новый заголовок",
		kind:     "Bug",
		fields:   fieldList{`labels=["a"]`},
		status:   "On Hold",
		comment:  "текст",
	}
	msg, err := cmdEdit(root, "ABC-12", ax)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"assign ABC-12 petrov",
		"estimate ABC-12 6h",
		`update ABC-12 [issuetype=Bug labels=["a"] summary=новый заголовок]`,
		"transition ABC-12 On Hold []",
		"comment ABC-12 текст",
	}
	if !reflect.DeepEqual(fakeState.calls, want) {
		t.Fatalf("след вызовов:\n%v\nжду:\n%v", fakeState.calls, want)
	}
	for _, w := range []string{"petrov", "6h", "новый заголовок", "On Hold"} {
		if !strings.Contains(msg, w) {
			t.Fatalf("в выводе нет %q:\n%s", w, msg)
		}
	}
}

// Ось одна: уезжает ровно один вызов, остальных осей команда не касается.
func TestEditSingleAxis(t *testing.T) {
	for name, ax := range map[string]editAxes{
		"исполнитель": {assignee: "petrov"},
		"оценка":      {estimate: "4h"},
		"заголовок":   {title: "т"},
		"тип":         {kind: "Bug"},
		"поле":        {fields: fieldList{"labels=a"}},
		"статус":      {status: "On Hold"},
		"комментарий": {comment: "текст"},
	} {
		t.Run(name, func(t *testing.T) {
			root := setupEnv(t, contourFullFile, bindingFile)
			if _, err := cmdEdit(root, "ABC-12", ax); err != nil {
				t.Fatal(err)
			}
			if len(fakeState.calls) != 1 {
				t.Fatalf("жду один вызов на одну ось, имею %v", fakeState.calls)
			}
		})
	}
}

// Статус называется именем трекера. Расписанному в [status] достаются поля
// его секции тем же порядком, что у обрядов, нерасписанному и конечному
// ничего, и вывод говорит, каким статус получился.
func TestEditStatusNaming(t *testing.T) {
	for _, tc := range []struct {
		status string
		call   string
		want   string
	}{
		{"In Progress", "transition ABC-12 In Progress [assignee=ivanov reason=плановая работа]", "секция доски: In progress"},
		{"On Hold", "transition ABC-12 On Hold []", "секция доски: Blocked"},
		{"Done", "transition ABC-12 Done []", "конечный"},
		{"Paused", "transition ABC-12 Paused []", "в таблице [status] контура его нет"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			root := setupEnv(t, contourFullFile, bindingFile)
			msg, err := cmdEdit(root, "ABC-12", editAxes{status: tc.status})
			if err != nil {
				t.Fatal(err)
			}
			if !hasCall(fakeState, tc.call) {
				t.Fatalf("жду вызов %q, имею %v", tc.call, fakeState.calls)
			}
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("в выводе нет %q:\n%s", tc.want, msg)
			}
		})
	}
}

// Ось, которой у адаптера нет, отказывает до первого запроса: половина
// правки уехать не должна, а отказ называет операцию и оси.
func TestEditRefusesAxesAdapterLacks(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile) // fake: ни update, ни comment
	for name, ax := range map[string]editAxes{
		"заголовок":   {title: "т"},
		"тип":         {kind: "Bug"},
		"поле":        {fields: fieldList{"labels=a"}},
		"комментарий": {comment: "текст"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cmdEdit(root, "ABC-12", ax)
			if err == nil {
				t.Fatal("жду отказ на отсутствующей оси")
			}
			if len(fakeState.calls) != 0 {
				t.Fatalf("отказ обязан прийти до первого вызова: %v", fakeState.calls)
			}
		})
	}
}

// --field разбирается по первому «=», значение бывает пустым (снять метку),
// имя нет; обрывок без «=» отбивается с именем ключа в отказе.
func TestEditFieldParsing(t *testing.T) {
	root := setupEnv(t, contourFullFile, bindingFile)
	if _, err := cmdEdit(root, "ABC-12", editAxes{fields: fieldList{"labels="}}); err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "update ABC-12 [labels=]") {
		t.Fatalf("пустое значение легально: %v", fakeState.calls)
	}

	root = setupEnv(t, contourFullFile, bindingFile)
	_, err := cmdEdit(root, "ABC-12", editAxes{fields: fieldList{"labels"}})
	if err == nil || !strings.Contains(err.Error(), "--field") {
		t.Fatalf("жду отказ с именем ключа, имею %v", err)
	}
	if len(fakeState.calls) != 0 {
		t.Fatalf("за обрывком не должно быть вызовов: %v", fakeState.calls)
	}
}

// setupJiraCmdEnv поднимает стенд jira и раскладывает на нём окружение
// команды: контур в файле, привязка, таблица статусов и поле секции check.
func setupJiraCmdEnv(t *testing.T, api string) (string, *jiraStub) {
	t.Helper()
	st := &jiraStub{t: t, resp: map[string]jiraResp{}}
	st.srv = httptest.NewServer(http.HandlerFunc(st.serve))
	t.Cleanup(st.srv.Close)
	t.Setenv("JIRA_TOKEN", "s3cr3t-token")
	contourText := fmt.Sprintf(`adapter = "jira"
base_url = %q
api_version = "%s"
token_env = "JIRA_TOKEN"
user = "5b10ac8d82e05b22cc7d4ef5"

[status]
backlog = ["Open", "Backlog"]
in_progress = ["In Progress", "Development"]
check = ["Review", "Testing"]
blocked = ["Blocked", "On Hold"]
done = ["Done", "Closed", "Rejected"]

[fields_check]
reviewer = "{user}"
`, st.srv.URL, api)
	root := setupEnv(t, contourText, bindingFile)
	return root, st
}

// Список переходов стенда: id различимы, чтобы выбор перехода сверялся по
// списку, а не по единственному варианту.
const editTransitions = `{"transitions":[
 {"id":"11","name":"Начать","to":{"name":"In Progress"}},
 {"id":"12","name":"Пауза","to":{"name":"On Hold"}},
 {"id":"14","name":"Ревью","to":{"name":"Review"}},
 {"id":"13","name":"Закрыть","to":{"name":"Done"}}
]}`

func TestEditJiraEstimateIsOneRequest(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	if _, err := cmdEdit(root, "ABC-12", editAxes{estimate: "6h"}); err != nil {
		t.Fatal(err)
	}
	if len(st.reqs) != 1 {
		t.Fatalf("оценка это ровно один запрос, имею %d", len(st.reqs))
	}
	req := st.last()
	if req.Method != http.MethodPut || req.Path != "/rest/api/3/issue/ABC-12" {
		t.Fatalf("оценка ушла не туда: %s %s", req.Method, req.Path)
	}
	fields, _ := bodyMap(t, req)["fields"].(map[string]any)
	tt, _ := fields["timetracking"].(map[string]any)
	if tt == nil || tt["originalEstimate"] != "6h" {
		t.Fatalf("оценка уехала не в timetracking.originalEstimate: %s", req.Body)
	}
}

func TestEditJiraAssignee(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	if _, err := cmdEdit(root, "ABC-12", editAxes{assignee: "5b10ac8d82e05b22cc7d4ef5"}); err != nil {
		t.Fatal(err)
	}
	req := st.last()
	if req.Method != http.MethodPut || req.Path != "/rest/api/3/issue/ABC-12/assignee" {
		t.Fatalf("исполнитель ушёл не туда: %s %s", req.Method, req.Path)
	}
	if got := bodyMap(t, req)["accountId"]; got != "5b10ac8d82e05b22cc7d4ef5" {
		t.Fatalf("исполнитель уехал не по accountId: %s", req.Body)
	}
}

// Заголовок, тип и произвольные поля уезжают одним запросом update: строка
// типа сворачивается в объект с именем, значения-JSON разбираются.
func TestEditJiraUpdateAxes(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	ax := editAxes{
		title:  "новый заголовок",
		kind:   "Bug",
		fields: fieldList{`labels=["a","b"]`, `customfield_1={"name": "Q"}`},
	}
	if _, err := cmdEdit(root, "ABC-12", ax); err != nil {
		t.Fatal(err)
	}
	if len(st.reqs) != 1 {
		t.Fatalf("поля правятся одним запросом, имею %d", len(st.reqs))
	}
	req := st.last()
	if req.Method != http.MethodPut || req.Path != "/rest/api/3/issue/ABC-12" {
		t.Fatalf("правка полей ушла не туда: %s %s", req.Method, req.Path)
	}
	fields, _ := bodyMap(t, req)["fields"].(map[string]any)
	if fields["summary"] != "новый заголовок" {
		t.Fatalf("заголовок потерялся: %s", req.Body)
	}
	it, _ := fields["issuetype"].(map[string]any)
	if it == nil || it["name"] != "Bug" {
		t.Fatalf("тип-строка не свёрнута в объект с именем: %s", req.Body)
	}
	labels, _ := fields["labels"].([]any)
	if len(labels) != 2 || labels[0] != "a" {
		t.Fatalf("поле-JSON уехало не разобранным: %s", req.Body)
	}
	cf, _ := fields["customfield_1"].(map[string]any)
	if cf == nil || cf["name"] != "Q" {
		t.Fatalf("произвольное поле-объект уехало не объектом: %s", req.Body)
	}
}

func TestEditJiraStatusCarriesSectionFields(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	st.on(http.MethodGet, "/rest/api/3/issue/ABC-12/transitions", jiraResp{body: editTransitions})
	if _, err := cmdEdit(root, "ABC-12", editAxes{status: "Review"}); err != nil {
		t.Fatal(err)
	}
	if len(st.reqs) != 2 {
		t.Fatalf("жду список переходов и переход, имею %d", len(st.reqs))
	}
	body := bodyMap(t, st.last())
	tr, _ := body["transition"].(map[string]any)
	if tr == nil || tr["id"] != "14" {
		t.Fatalf("переход выбран не по списку стенда: %v", body["transition"])
	}
	sent, _ := body["fields"].(map[string]any)
	if sent == nil || sent["reviewer"] != "5b10ac8d82e05b22cc7d4ef5" {
		t.Fatalf("поле секции [fields_check] не уехало с переходом: %s", st.last().Body)
	}
}

// Отказ трекера доносится текстом трекера и работа останавливается: оси
// после отказавшей не уезжают, а уехавшие до неё видны в выводе команды.
func TestEditJiraRefusalStops(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	st.on(http.MethodPut, "/rest/api/3/issue/ABC-12", jiraResp{code: http.StatusBadRequest, file: "error-required-field.json"})
	ax := editAxes{assignee: "5b10ac8d82e05b22cc7d4ef5", estimate: "6h", title: "новый"}
	msg, err := cmdEdit(root, "ABC-12", ax)
	if err == nil {
		t.Fatal("жду отказ трекера")
	}
	for _, want := range []string{"400", "priority"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %s", want, err)
		}
	}
	if len(st.reqs) != 2 {
		t.Fatalf("после отказа работа не останавливается: %d запросов", len(st.reqs))
	}
	if !strings.Contains(msg, "исполнитель: 5b10ac8d82e05b22cc7d4ef5") {
		t.Fatalf("уехавшая до отказа ось не видна в выводе:\n%s", msg)
	}
	for _, gone := range []string{"оценка:", "поля:"} {
		if strings.Contains(msg, gone) {
			t.Fatalf("ось после отказа отмечена сделанной:\n%s", msg)
		}
	}
}

func TestEditJiraComment(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	if _, err := cmdEdit(root, "ABC-12", editAxes{comment: "текст"}); err != nil {
		t.Fatal(err)
	}
	req := st.last()
	if req.Method != http.MethodPost || req.Path != "/rest/api/3/issue/ABC-12/comment" {
		t.Fatalf("комментарий ушёл не туда: %s %s", req.Method, req.Path)
	}
	doc, _ := bodyMap(t, req)["body"].(map[string]any)
	if doc == nil || doc["type"] != "doc" {
		t.Fatalf("комментарий ушёл не документом ADF: %s", req.Body)
	}
}

// Без ключей тикет не трогается вовсе: к трекеру не уходит ни одного запроса.
func TestEditJiraNoAxesZeroRequests(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "")
	if _, err := cmdEdit(root, "ABC-12", editAxes{}); err == nil {
		t.Fatal("жду отказ без осей")
	}
	if len(st.reqs) != 0 {
		t.Fatalf("без осей не должно быть ни одного запроса: %d", len(st.reqs))
	}
}

// Контур Server/DC на v2: правка полей и комментарий ходят путями второй
// версии, комментарий уезжает строкой.
func TestEditJiraV2Paths(t *testing.T) {
	root, st := setupJiraCmdEnv(t, "2")
	ax := editAxes{title: "новый", kind: "Bug", comment: "текст"}
	if _, err := cmdEdit(root, "ABC-12", ax); err != nil {
		t.Fatal(err)
	}
	if len(st.reqs) != 2 {
		t.Fatalf("жду правку полей и комментарий, имею %d", len(st.reqs))
	}
	for _, req := range st.reqs {
		if strings.HasPrefix(req.Path, "/rest/api/3") {
			t.Fatalf("на контуре v2 уехал путь третьей версии: %s", req.Path)
		}
	}
	if got := bodyMap(t, st.reqs[1])["body"]; got != "текст" {
		t.Fatalf("комментарий на v2 уехал не строкой: %s", st.reqs[1].Body)
	}
}

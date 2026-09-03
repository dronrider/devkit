package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Адаптер Jira: REST API v2 и v3 поверх stdlib, как соседние утилиты. Версия
// называется контуром ключом api_version и без него это v3. Токен в код и
// в конфиг не попадает, его называет контур (token_env либо token_file) и
// читается он на каждом запросе: команда status обязана работать и там, где
// токена на машине нет, иначе про адаптер она не скажет ничего.
const (
	jiraTimeout = 30 * time.Second
	// Время ворклога внутри дня. Факты работы дают дату, а Jira ждёт момент;
	// полдень по UTC ложится в тот же календарный день в любой зоне.
	jiraWorklogTime = "T12:00:00.000+0000"
)

func init() {
	registerAdapter("jira", newJiraAdapter)
}

type jiraAdapter struct {
	base string
	// api это префикс пути, /rest/api/2 или /rest/api/3: у Cloud его нет в
	// выборе, а Server и Data Center третьей версии не имеют вовсе. v2
	// переворачивает и формат тел: исполнитель зовётся name, а не accountId,
	// комментарий строкой, а не документом ADF.
	api     string
	v2      bool
	contour *contour
	client  *http.Client
}

func newJiraAdapter(c *contour) (adapter, error) {
	api, v2, err := jiraAPIVersion(c)
	if err != nil {
		return nil, err
	}
	return &jiraAdapter{
		base:    strings.TrimRight(c.BaseURL, "/"),
		api:     api,
		v2:      v2,
		contour: c,
		client: &http.Client{
			Timeout: jiraTimeout,
			// Перенаправления не проходят вовсе: живой Server на путь
			// отсутствующей версии API отвечает 302 на веб-логин, и тихое
			// следование за ним превращало бы запись в молчаливый успех
			// (GET с HTML-страницей в ответ), а чтение в панику разбора.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// jiraAPIVersion сворачивает ключ контура api_version в префикс пути.
// Пустой ключ это v3, сегодняшнее поведение стоящих контуров Cloud.
func jiraAPIVersion(c *contour) (string, bool, error) {
	raw := strings.ToLower(strings.TrimSpace(c.API))
	raw = strings.TrimPrefix(raw, "v")
	switch raw {
	case "", "3":
		return "/rest/api/3", false, nil
	case "2":
		return "/rest/api/2", true, nil
	}
	return "", false, fmt.Errorf("%s: api_version %q не понимаю, жду 2 или 3: у Cloud это 3, у Server и Data Center 2", c.Path, c.API)
}

// jiraError доносит отказ трекера как есть: код ответа и текст Jira. Своего
// «что-то пошло не так» тут нет, разбираться с обязательным полем или чужими
// правами всё равно человеку, и по своему тексту он этого не сделает.
type jiraError struct {
	Status   int
	Method   string
	Path     string
	Messages []string
	Fields   map[string]string
	Raw      string
}

func (e *jiraError) Error() string {
	var parts []string
	parts = append(parts, e.Messages...)
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, e.Fields[k]))
	}
	if len(parts) == 0 {
		if e.Raw != "" {
			parts = append(parts, e.Raw)
		} else {
			parts = append(parts, "тело ответа пустое")
		}
	}
	return fmt.Sprintf("Jira ответила %d на %s %s: %s", e.Status, e.Method, e.Path, strings.Join(parts, "; "))
}

// do это один запрос к Jira. Тело запроса и заголовки нигде не печатаются:
// токен уезжает только в Authorization и не должен всплыть ни в ошибке, ни в
// журнале.
func (j *jiraAdapter) do(method, path string, body, out any) error {
	if j.base == "" {
		return fmt.Errorf("контур %s не назвал base_url, адаптеру jira некуда ходить", j.contour.Name)
	}
	token, err := j.contour.token()
	if err != nil {
		return err
	}
	var buf io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, j.base+path, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := j.client.Do(req)
	if err != nil {
		return fmt.Errorf("не достучался до Jira (%s %s): %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// До сюда доходит только ответ без следования за ним (CheckRedirect
		// стоит в клиенте): живой Server именно так отвечает на путь версии,
		// которой у него нет, отсылая в веб-логин.
		where := strings.TrimSpace(resp.Header.Get("Location"))
		if where == "" {
			where = "не названо"
		}
		return fmt.Errorf("Jira ответила %d на %s %s, перенаправление в %s: у сервера нет этого пути, и если это про версию API, сверьте api_version контура %s", resp.StatusCode, method, path, where, j.contour.Name)
	}
	if resp.StatusCode >= 400 {
		return jiraFail(method, path, resp.StatusCode, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("не разобрал ответ Jira (%s %s): %v", method, path, err)
	}
	return nil
}

// jiraFail разбирает тело отказа. Схема ErrorCollection это errorMessages и
// errors, но отдают её не всегда: 401 и 403 у Jira часто приходят пустыми или
// куском HTML, и тогда в ошибку идёт начало тела как есть.
func jiraFail(method, path string, status int, data []byte) error {
	e := &jiraError{Status: status, Method: method, Path: path}
	var body struct {
		Messages []string          `json:"errorMessages"`
		Fields   map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(data, &body); err == nil {
		e.Messages = body.Messages
		e.Fields = body.Fields
	}
	e.Raw = shorten(strings.TrimSpace(string(data)), 200)
	return e
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type jiraIssue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			Name string `json:"name"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		TimeTracking struct {
			OriginalEstimate string `json:"originalEstimate"`
		} `json:"timetracking"`
		// Description приходит строкой на v2 и документом ADF на v3, поэтому
		// сырой JSON разбирает jiraDescription, а не эта структура.
		Description json.RawMessage `json:"description"`
	} `json:"fields"`
}

func (j *jiraAdapter) fetch(key string) (ticket, error) {
	var issue jiraIssue
	path := j.api + "/issue/" + key + "?fields=summary,status,issuetype,timetracking,description"
	if err := j.do(http.MethodGet, path, nil, &issue); err != nil {
		return ticket{}, err
	}
	t := ticket{
		Key:         issue.Key,
		Status:      issue.Fields.Status.Name,
		Type:        issue.Fields.IssueType.Name,
		Title:       issue.Fields.Summary,
		Estimate:    issue.Fields.TimeTracking.OriginalEstimate,
		Description: jiraDescription(issue.Fields.Description),
	}
	if t.Key == "" {
		t.Key = key
	}
	t.URL = j.base + "/browse/" + t.Key
	return t, nil
}

// jiraDescription достаёт текст описания из ответа Jira: v2 отдаёт его
// строкой, v3 документом ADF (Atlassian Document Format). Без разбора формата
// `trackctl review` не мог бы вписать постановку тикета в файл задачи,
// поэтому разбор минимальный: текстовые узлы собираются подряд, абзац
// заканчивается переводом строки, разметка (жирный, ссылки) не переживает,
// она тут не нужна.
func jiraDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var doc struct {
		Content []jiraADFNode `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	for _, n := range doc.Content {
		writeJiraADFText(&b, n)
	}
	return strings.TrimSpace(b.String())
}

type jiraADFNode struct {
	Type    string        `json:"type"`
	Text    string        `json:"text"`
	Content []jiraADFNode `json:"content"`
}

func writeJiraADFText(b *strings.Builder, n jiraADFNode) {
	if n.Text != "" {
		b.WriteString(n.Text)
	}
	for _, c := range n.Content {
		writeJiraADFText(b, c)
	}
	if n.Type == "paragraph" {
		b.WriteString("\n")
	}
}

type jiraTransitions struct {
	Transitions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		To   struct {
			Name string `json:"name"`
		} `json:"to"`
	} `json:"transitions"`
}

// transition делает один шаг в целевой статус. Пути через промежуточные
// статусы адаптер не ищет: нужного перехода в списке нет, значит отказ
// перечисляет доступные, а разруливает это человек строчкой в таблице контура.
func (j *jiraAdapter) transition(key, status string, fields map[string]string) error {
	var list jiraTransitions
	if err := j.do(http.MethodGet, j.api+"/issue/"+key+"/transitions", nil, &list); err != nil {
		return err
	}
	id := ""
	var available []string
	for _, tr := range list.Transitions {
		if strings.EqualFold(strings.TrimSpace(tr.To.Name), strings.TrimSpace(status)) {
			id = tr.ID
		}
		available = append(available, tr.To.Name)
	}
	if id == "" {
		return &transitionRefused{Key: key, Target: status, Available: available}
	}
	body := map[string]any{"transition": map[string]string{"id": id}}
	if len(fields) > 0 {
		body["fields"] = jiraFields(fields)
	}
	return j.do(http.MethodPost, j.api+"/issue/"+key+"/transitions", body, nil)
}

// jiraFields перекладывает поля секции [fields_*] в тело перехода. Значение
// уезжает как есть, но у Jira половина полей это объекты ({"name": "Fixed"}),
// поэтому значение, которое само по себе валидный JSON, отправляется разобранным:
// иначе секцией нельзя было бы задать ни resolution, ни assignee.
func jiraFields(fields map[string]string) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		out[k] = jiraValue(v)
	}
	return out
}

func jiraValue(v string) any {
	s := strings.TrimSpace(v)
	if s == "" {
		return v
	}
	switch s[0] {
	case '{', '[':
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed
		}
	}
	return v
}

// assign ставит исполнителя. Ключ тела зависит от версии: v3 Cloud ждёт
// accountId, v2 Server и DC зовут пользователя по name.
func (j *jiraAdapter) assign(key, user string) error {
	who := "accountId"
	if j.v2 {
		who = "name"
	}
	return j.do(http.MethodPut, j.api+"/issue/"+key+"/assignee", map[string]string{who: user}, nil)
}

func (j *jiraAdapter) estimate(key, value string) error {
	body := map[string]any{"fields": map[string]any{
		"timetracking": map[string]string{"originalEstimate": value},
	}}
	return j.do(http.MethodPut, j.api+"/issue/"+key, body, nil)
}

func (j *jiraAdapter) worklog(key, date, spent string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("ворклог %s: дата %q не в формате 2006-01-02", key, date)
	}
	body := map[string]string{
		"started":   date + jiraWorklogTime,
		"timeSpent": spent,
	}
	return j.do(http.MethodPost, j.api+"/issue/"+key+"/worklog", body, nil)
}

// update правит поля тикета: заголовок, тип и произвольные поля команды
// edit. Значения уезжают как есть с оговоркой jiraValue: валидный JSON
// отправляется разобранным. issuetype исключение вдвойне: Jira ждёт объект
// с именем типа, поэтому строка («Bug») сворачивается в {"name": "Bug"},
// а явный JSON уезжает как записан.
func (j *jiraAdapter) update(key string, fields map[string]string) error {
	sent := jiraFields(fields)
	if raw, ok := sent["issuetype"].(string); ok {
		sent["issuetype"] = map[string]string{"name": raw}
	}
	body := map[string]any{"fields": sent}
	return j.do(http.MethodPut, j.api+"/issue/"+key, body, nil)
}

// rank пишет числовое поле приоритета. Имя поля приезжает из контура и в коде
// не прошито: у каждой компании свои customfield, и это чувствительное.
func (j *jiraAdapter) rank(key, field string, value int) error {
	if strings.TrimSpace(field) == "" {
		return fmt.Errorf("приоритет тикета %s: контур не назвал rank_field, писать некуда", key)
	}
	body := map[string]any{"fields": map[string]any{field: value}}
	return j.do(http.MethodPut, j.api+"/issue/"+key, body, nil)
}

// comment пишет комментарий: v3 принимает только тело Atlassian Document
// Format, v2 ждёт обычную строку wiki-разметки.
func (j *jiraAdapter) comment(key, text string) error {
	var body map[string]any
	if j.v2 {
		body = map[string]any{"body": text}
	} else {
		body = map[string]any{"body": map[string]any{
			"type":    "doc",
			"version": 1,
			"content": []any{map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": text}},
			}},
		}}
	}
	return j.do(http.MethodPost, j.api+"/issue/"+key+"/comment", body, nil)
}

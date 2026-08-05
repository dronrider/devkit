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

// Адаптер Jira: REST API v3 поверх stdlib, как соседние утилиты. Токен в код и
// в конфиг не попадает, его называет контур (token_env либо token_file) и
// читается он на каждом запросе: команда status обязана работать и там, где
// токена на машине нет, иначе про адаптер она не скажет ничего.
const (
	jiraAPI     = "/rest/api/3"
	jiraTimeout = 30 * time.Second
	// Время ворклога внутри дня. Факты работы дают дату, а Jira ждёт момент;
	// полдень по UTC ложится в тот же календарный день в любой зоне.
	jiraWorklogTime = "T12:00:00.000+0000"
)

func init() {
	registerAdapter("jira", newJiraAdapter)
}

type jiraAdapter struct {
	base    string
	contour *contour
	client  *http.Client
}

func newJiraAdapter(c *contour) (adapter, error) {
	return &jiraAdapter{
		base:    strings.TrimRight(c.BaseURL, "/"),
		contour: c,
		client:  &http.Client{Timeout: jiraTimeout},
	}, nil
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
	} `json:"fields"`
}

func (j *jiraAdapter) fetch(key string) (ticket, error) {
	var issue jiraIssue
	path := jiraAPI + "/issue/" + key + "?fields=summary,status,issuetype,timetracking"
	if err := j.do(http.MethodGet, path, nil, &issue); err != nil {
		return ticket{}, err
	}
	t := ticket{
		Key:      issue.Key,
		Status:   issue.Fields.Status.Name,
		Type:     issue.Fields.IssueType.Name,
		Title:    issue.Fields.Summary,
		Estimate: issue.Fields.TimeTracking.OriginalEstimate,
	}
	if t.Key == "" {
		t.Key = key
	}
	return t, nil
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
	if err := j.do(http.MethodGet, jiraAPI+"/issue/"+key+"/transitions", nil, &list); err != nil {
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
	return j.do(http.MethodPost, jiraAPI+"/issue/"+key+"/transitions", body, nil)
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

func (j *jiraAdapter) assign(key, user string) error {
	return j.do(http.MethodPut, jiraAPI+"/issue/"+key+"/assignee", map[string]string{"accountId": user}, nil)
}

func (j *jiraAdapter) estimate(key, value string) error {
	body := map[string]any{"fields": map[string]any{
		"timetracking": map[string]string{"originalEstimate": value},
	}}
	return j.do(http.MethodPut, jiraAPI+"/issue/"+key, body, nil)
}

func (j *jiraAdapter) worklog(key, date, spent string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("ворклог %s: дата %q не в формате 2006-01-02", key, date)
	}
	body := map[string]string{
		"started":   date + jiraWorklogTime,
		"timeSpent": spent,
	}
	return j.do(http.MethodPost, jiraAPI+"/issue/"+key+"/worklog", body, nil)
}

// rank пишет числовое поле приоритета. Имя поля приезжает из контура и в коде
// не прошито: у каждой компании свои customfield, и это чувствительное.
func (j *jiraAdapter) rank(key, field string, value int) error {
	if strings.TrimSpace(field) == "" {
		return fmt.Errorf("приоритет тикета %s: контур не назвал rank_field, писать некуда", key)
	}
	body := map[string]any{"fields": map[string]any{field: value}}
	return j.do(http.MethodPut, jiraAPI+"/issue/"+key, body, nil)
}

// comment пишет комментарий телом Atlassian Document Format: обычной строки
// API v3 не принимает.
func (j *jiraAdapter) comment(key, text string) error {
	body := map[string]any{"body": map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": text}},
		}},
	}}
	return j.do(http.MethodPost, jiraAPI+"/issue/"+key+"/comment", body, nil)
}

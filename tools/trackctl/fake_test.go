package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Подложный адаптер живёт в таблице наравне с живыми и записывает вызовы:
// проверять команды иначе нечем, живой адаптер приезжает своей задачей, а в
// сеть тесты не ходят. Записанный след это и есть ожидание тестов: по нему
// видно, что take не забыл assign, а шага по отсутствующей оси никто не делал.
type fake struct {
	calls     []string
	ticket    ticket
	byKey     map[string]ticket
	fetchErr  error
	available []string
	failStep  string
	baseURL   string
	// mrURLs и mrErr это ответ mergeRequests: она есть только у fakeFull,
	// обычный fake про GitLab ничего не знает, как и живой jira.go сегодня.
	mrURLs []string
	mrErr  error
}

// fakeState это адаптер, который отдаст таблица следующему newAdapter.
// Прогон однопоточный, глобальная переменная тут дешевле лишнего слоя.
var fakeState *fake

func (f *fake) log(format string, args ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fake) fetch(key string) (ticket, error) {
	f.log("fetch %s", key)
	if f.fetchErr != nil {
		return ticket{}, f.fetchErr
	}
	t, ok := f.byKey[key]
	if !ok {
		t = f.ticket
	}
	t.Key = key
	if t.URL == "" {
		t.URL = strings.TrimRight(f.baseURL, "/") + "/browse/" + key
	}
	return t, nil
}

func (f *fake) transition(key, status string, fields map[string]string) error {
	f.log("transition %s %s [%s]", key, status, strings.Join(fieldPairs(fields), " "))
	if len(f.available) > 0 {
		return &transitionRefused{Key: key, Target: status, Available: f.available}
	}
	if f.failStep == "transition" {
		return fmt.Errorf("трекер отказал")
	}
	return nil
}

func (f *fake) assign(key, user string) error {
	f.log("assign %s %s", key, user)
	if f.failStep == "assign" {
		return fmt.Errorf("трекер отказал")
	}
	return nil
}

func (f *fake) estimate(key, value string) error {
	f.log("estimate %s %s", key, value)
	return nil
}

func (f *fake) worklog(key, date, spent string) error {
	f.log("worklog %s %s %s", key, date, spent)
	return nil
}

// fieldPairs сворачивает поля в сортированный след «ключ=значение»: у
// перехода так же, и след правки сравнивается со списком целиком.
func fieldPairs(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k+"="+fields[k])
	}
	sort.Strings(keys)
	return keys
}

// fakeFull это тот же адаптер, но с необязательными операциями: им проверяется
// вторая половина оси, когда операция есть, а команда всё равно по ней не
// ходит.
type fakeFull struct{ *fake }

func (f fakeFull) rank(key, field string, value int) error {
	f.log("rank %s %s %d", key, field, value)
	return nil
}

func (f fakeFull) comment(key, text string) error {
	f.log("comment %s %s", key, text)
	return nil
}

func (f fakeFull) update(key string, fields map[string]string) error {
	f.log("update %s [%s]", key, strings.Join(fieldPairs(fields), " "))
	return nil
}

// mergeRequests живёт только у fakeFull: обычный fake про поиск MR ничего не
// знает, как и живой jira.go сегодня, review в этом случае заводит строку без
// MR и просит ссылку флагом --mr.
func (f fakeFull) mergeRequests(branchPrefix string) ([]string, error) {
	f.log("mr %s", branchPrefix)
	if f.mrErr != nil {
		return nil, f.mrErr
	}
	return f.mrURLs, nil
}

func init() {
	registerAdapter("fake", func(c *contour) (adapter, error) { fakeState.baseURL = c.BaseURL; return fakeState, nil })
	registerAdapter("fake-full", func(c *contour) (adapter, error) { fakeState.baseURL = c.BaseURL; return fakeFull{fakeState}, nil })
}

const contourFile = `adapter = "fake"
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

const bindingFile = `contour = corp
key = ABC
branch = "{key}-{slug}"
repo = ../corp-repo
`

// setupEnv раскладывает временное окружение команды: корень с .devkit,
// привязку проекта и контур в домашней директории.
func setupEnv(t *testing.T, contourText, bindText string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FAKE_TOKEN", "secret")
	dir := filepath.Join(home, ".devkit", "tracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "corp.local"), []byte(contourText), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte("# доска\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, bindingPath), []byte(bindText), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeState = &fake{ticket: ticket{Status: "Open", Type: "Task", Title: "заголовок", Estimate: "1d"}}
	t.Cleanup(func() { fakeState = nil })
	return root
}

func hasCall(f *fake, prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const readyTaskBody = `## Что происходит

Пользователь не может сохранить описание при создании задачи из формы.

## Чего хотим

Форма сохраняет введённое описание вместе с новой строкой доски.

## DoD

- После сохранения файл новой задачи содержит введённое описание.

## Ранг

- Серьёзность: без описания приходится возвращаться к постановке.
- Ценность: описание нужно при каждом создании задачи.
- Неопределённость: путь сохранения проверен запросом к серверу.
- Поправка на баг: этот стенд проверяет новую возможность.
- Рычаг: исполнитель получает описание до начала работы.
`

func taskBodyFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "task-body.md")
	if err := os.WriteFile(path, []byte(readyTaskBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func taskCreateJSON(t *testing.T, text string) string {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	value["body"] = readyTaskBody
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTaskCreateNeedsDescription(t *testing.T) {
	e, c, gitLog := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)
	for _, description := range []string{"", "## Что происходит\n\n<!-- заполнить -->\n\n## Чего хотим\n\n- \n\n## DoD\n\n## Ранг\n"} {
		data, err := json.Marshal(map[string]any{"title": "Без постановки", "rank": "25+1+1+0+0", "body": description})
		if err != nil {
			t.Fatal(err)
		}
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks", string(data))
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("описание %q: %d %s", description, resp.StatusCode, text)
		}
		if after := readFile(t, boardPath); after != before {
			t.Fatal("неполное описание изменило доску")
		}
		if isFile(filepath.Join(e.proj, "docs", "tasks", "XR-005.md")) {
			t.Fatal("создан файл неполной задачи")
		}
	}
	if strings.Contains(readFile(t, gitLog), "строка и файл задачи заведены") {
		t.Fatal("неполная постановка дошла до коммита")
	}
}

func TestTaskCreateKeepsDescription(t *testing.T) {
	e, c, _ := tasksEnv(t)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks", taskCreateJSON(t, `{"title":"С описанием", "rank":"25+1+1+0+0"}`))
	result := newResp(t, resp, "создание с описанием")
	id, _ := result["id"].(string)
	text := readFile(t, filepath.Join(e.proj, "docs", "tasks", id+".md"))
	for _, want := range []string{"Пользователь не может сохранить описание", "Форма сохраняет введённое описание", "После сохранения файл новой задачи", "Рычаг: исполнитель"} {
		if !strings.Contains(text, want) {
			t.Errorf("описание потеряло %q: %s", want, text)
		}
	}
}

func TestTaskFormIncludesDescription(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	form := funcBody(t, text, "function renderNew(")
	for _, want := range []string{"afterChips: [bodyField]", "body: newForm.body", "draft ? DRAFT_TEMPLATE : TASK_TEMPLATE", `"описание задачи"`} {
		if !strings.Contains(form, want) {
			t.Errorf("форма не сохраняет описание: нет %q", want)
		}
	}
}

func TestTaskCreatePreservesAcceptanceExample(t *testing.T) {
	for _, example := range []string{"```md\n## Приёмка\n\n- барьер «доступ»:\n```\n\n", "<!--\n## Приёмка\n\n- барьер «доступ»:\n-->\n\n"} {
		t.Run(example[:3], func(t *testing.T) {
			e, c, _ := tasksEnv(t)
			description := strings.Replace(readyTaskBody, "## Чего хотим", example+"## Чего хотим", 1)
			data, _ := json.Marshal(map[string]any{"title": "С примером", "rank": "25+1+1+0+0", "body": description, "accept": "mixed", "barrier": "доступ", "reason": "Нет доступа к внешней системе"})
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks", string(data))
			result := newResp(t, resp, "создание с примером")
			id, _ := result["id"].(string)
			text := readFile(t, filepath.Join(e.proj, "docs", "tasks", id+".md"))
			if !strings.Contains(text, example) {
				t.Fatalf("изменён пример в описании: %s", text)
			}
			if !strings.Contains(text, "- барьер «доступ»: Нет доступа к внешней системе") {
				t.Fatal("причина не записана в настоящем разделе: " + text)
			}
		})
	}
}

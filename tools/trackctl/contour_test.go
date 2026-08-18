package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestBindingRead(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	b, err := loadBinding(root)
	if err != nil {
		t.Fatal(err)
	}
	if b.Contour != "corp" || b.Key != "ABC" || b.Repo != "../corp-repo" {
		t.Fatalf("привязка разобрана не так: %+v", b)
	}
	if got := b.branchName("ABC-12", "trackctl"); got != "ABC-12-trackctl" {
		t.Fatalf("шаблон ветки: %q", got)
	}
}

// Привязку читает и shipctl, у которого парсера TOML нет, поэтому формат
// остаётся плоским: секция в файле это не привязка, а чужой формат.
func TestBindingRequiresKeys(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"без контура", "key = ABC\n", "не задан contour"},
		{"без ключа", "contour = corp\n", "не задан key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupEnv(t, contourFile, tc.text)
			_, err := loadBinding(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("жду отказ %q, вижу %v", tc.want, err)
			}
		})
	}
}

func TestContourRead(t *testing.T) {
	setupEnv(t, contourFile, bindingFile)
	c, err := loadContour("corp")
	if err != nil {
		t.Fatal(err)
	}
	if c.Adapter != "fake" || c.User != "ivanov" || c.BaseURL != "https://tracker.example" {
		t.Fatalf("верхний уровень разобран не так: %+v", c)
	}
	if c.CostS != "4h" || c.CostM != "1d" || c.CostL != "3d" || c.RankField != "customfield_100" {
		t.Fatalf("коэффициенты и поле ранга разобраны не так: %+v", c)
	}
	if want := []string{"Review", "Testing"}; !reflect.DeepEqual(c.Status[sectCheck], want) {
		t.Fatalf("секция check: %v", c.Status[sectCheck])
	}
	if got := c.Fields[sectInProgress]["assignee"]; got != "{user}" {
		t.Fatalf("поля перехода читаются как есть, подстановка идёт при отправке: %q", got)
	}
	tok, err := c.token()
	if err != nil || tok != "secret" {
		t.Fatalf("токен берётся из переменной окружения: %q %v", tok, err)
	}
}

func TestContourRejectsUnknownSections(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"чужая секция статусов", strings.Replace(contourFile, "check = ", "review = ", 1), "не из доски"},
		{"поля конечной секции", contourFile + "\n[fields_done]\nresolution = \"Fixed\"\n", "не про секцию доски"},
		{"пустой список", strings.Replace(contourFile, `check = ["Review", "Testing"]`, "check = []", 1), "пуст"},
		{"без адаптера", strings.Replace(contourFile, `adapter = "fake"`, "", 1), "не задан adapter"},
		{"без пользователя", strings.Replace(contourFile, `user = "ivanov"`, "", 1), "не задан user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupEnv(t, tc.text, bindingFile)
			if _, err := loadContour("corp"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("жду отказ %q, вижу %v", tc.want, err)
			}
		})
	}
}

// Версия API выглядит числом и так её легко записать без кавычек: молчаливое
// умолчание тогда вернуло бы Server-контуру исходный баг, поэтому расхождение
// типов отбивается на чтении контура.
func TestContourAPIVersionQuoted(t *testing.T) {
	bad := strings.Replace(contourFile, `adapter = "fake"`, "api_version = 2\nadapter = \"fake\"", 1)
	setupEnv(t, bad, bindingFile)
	_, err := loadContour("corp")
	if err == nil {
		t.Fatal("числовая версия принята молча")
	}
	for _, want := range []string{"api_version", "кавычках"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %s", want, err)
		}
	}
}

func TestContourMissingFile(t *testing.T) {
	setupEnv(t, contourFile, bindingFile)
	_, err := loadContour("other")
	if err == nil || !strings.Contains(err.Error(), "other.local") {
		t.Fatalf("отказ обязан назвать путь ожидаемого файла: %v", err)
	}
}

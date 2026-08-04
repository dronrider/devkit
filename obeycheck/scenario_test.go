package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const wholeScenario = `# закоммитить правку

конец: субагент

## Промпт

Поправь README и закоммить.

Второй абзац промпта.

## Подготовка

` + "```sh" + `
echo подготовка
` + "```" + `

## Проверка

` + "```sh" + `
git log -1 --pretty=%s | grep -q OB-002
` + "```" + `
`

func TestParseScenarioWhole(t *testing.T) {
	s, err := parseScenario("docs/03-commit-style.md", wholeScenario)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "03-commit-style" || s.Title != "закоммитить правку" || s.End != endSub {
		t.Fatalf("шапка разобрана как %+v", s)
	}
	if s.Prompt != "Поправь README и закоммить.\n\nВторой абзац промпта." {
		t.Fatalf("промпт: %q", s.Prompt)
	}
	if s.Setup != "echo подготовка" {
		t.Fatalf("подготовка: %q", s.Setup)
	}
	if s.Check != "git log -1 --pretty=%s | grep -q OB-002" {
		t.Fatalf("проверка: %q", s.Check)
	}
}

func TestParseScenarioDefaults(t *testing.T) {
	s, err := parseScenario("press.md", "# нажать кнопку\n\n## Промпт\n\nжми\n\n## Проверка\n\ntest -f done.txt\n")
	if err != nil {
		t.Fatal(err)
	}
	if s.End != endAny {
		t.Fatalf("конец по умолчанию должен быть %s, получил %s", endAny, s.End)
	}
	if s.Setup != "" {
		t.Fatalf("подготовки не было, получил %q", s.Setup)
	}
	if !s.runsOn(endSession) || !s.runsOn(endSub) {
		t.Fatal("сценарий без объявленного конца гоняется на обоих")
	}
}

func TestScenarioRunsOn(t *testing.T) {
	s := Scenario{End: endSession}
	if !s.runsOn(endSession) || s.runsOn(endSub) {
		t.Fatal("сессионный сценарий на субагентском конце гоняться не должен")
	}
}

func TestParseScenarioErrors(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"нет заголовка", "## Промпт\n\nжми\n", "жду заголовок"},
		{"пустой заголовок", "# \n\n## Промпт\n\nжми\n", "пустой заголовок"},
		{"нет промпта", "# сценарий\n\n## Проверка\n\ntrue\n", "Промпт"},
		{"нет проверки", "# сценарий\n\n## Промпт\n\nжми\n", "Проверка"},
		{"чужая секция", "# сценарий\n\n## Ожидание\n\nжми\n", "неизвестная секция"},
		{"секция дважды", "# c\n\n## Промпт\n\nа\n\n## Промпт\n\nб\n", "уже была"},
		{"чужой ключ", "# c\n\nмодель: opus\n\n## Промпт\n\nа\n\n## Проверка\n\ntrue\n", "неизвестный ключ"},
		{"чужой конец", "# c\n\nконец: оба\n\n## Промпт\n\nа\n\n## Проверка\n\ntrue\n", "конец"},
		{"мусор до секций", "# c\n\nпросто строка\n\n## Промпт\n\nа\n\n## Проверка\n\ntrue\n", "ключ: значение"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseScenario("c.md", c.text)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ждал %q, получил: %v", c.want, err)
			}
		})
	}
}

// Сценарий без проверки стендом не гоняется: иначе прогон меряет впечатление, а
// не поведение, и любая раскладка выходит зелёной.
func TestScenarioWithoutCheckRejected(t *testing.T) {
	_, err := parseScenario("c.md", "# c\n\n## Промпт\n\nа\n\n## Проверка\n\n\n")
	if err == nil || !strings.Contains(err.Error(), "объективной") {
		t.Fatalf("ждал отказ, получил: %v", err)
	}
}

func TestLoadScenariosOrderAndFilter(t *testing.T) {
	dir := filepath.Join("testdata", "scenarios")
	all, err := loadScenarios(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range all {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "env,press,session-only" {
		t.Fatalf("порядок сценариев: %v", ids)
	}
	one, err := loadScenarios(dir, []string{"press"})
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].ID != "press" {
		t.Fatalf("фильтр вернул %v", one)
	}
	if _, err := loadScenarios(dir, []string{"нет-такого"}); err == nil {
		t.Fatal("ждал ошибку про несуществующий сценарий")
	}
}

// Боевые сценарии лежат в репозитории и разбираются тем же парсером: файл,
// который стенд не прочитает, обнаружится на прогоне в двести сессий, а не тут.
func TestFirstWaveScenariosParse(t *testing.T) {
	root := devkitRoot(t)
	list, err := loadScenarios(filepath.Join(root, "obeycheck", "scenarios"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 10 {
		t.Fatalf("сценариев первой очереди десять, вижу %d", len(list))
	}
	dir := t.TempDir()
	for _, s := range list {
		if s.Title == "" || s.Prompt == "" || s.Check == "" {
			t.Errorf("сценарий %s разобран наполовину: %+v", s.ID, s)
		}
		path := filepath.Join(dir, s.ID+".sh")
		if err := os.WriteFile(path, []byte(s.Check+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
			t.Errorf("проверка сценария %s не разбирается shell: %v (%s)", s.ID, err, out)
		}
	}
}

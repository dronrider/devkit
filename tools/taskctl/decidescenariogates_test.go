package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

type scenarioGateCLI struct {
	t         *testing.T
	root, bin string
}

func (c scenarioGateCLI) run(ok bool, args ...string) string {
	c.t.Helper()
	out, err := exec.Command(c.bin, append([]string{"-C", c.root, "decide", "XR-005"}, args...)...).CombinedOutput()
	if (err == nil) != ok {
		c.t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}
func (c scenarioGateCLI) fail(word string, args ...string) {
	c.t.Helper()
	before := readTask(c.t, c.root, "XR-005")
	ask := askInMain(c.t, c.root, "XR-005")
	waiting, _ := os.ReadFile(ask)
	out := c.run(false, args...)
	after, _ := os.ReadFile(ask)
	if !strings.Contains(out, word) || readTask(c.t, c.root, "XR-005") != before || string(after) != string(waiting) {
		c.t.Fatalf("отказ %q изменил запись/ожидание или потерял причину:\n%s", word, out)
	}
}
func (c scenarioGateCLI) snapshot() string {
	c.t.Helper()
	out := c.run(true, "сценарии", "--chat")
	key := regexp.MustCompile(`снимок сценариев: (sc1-[a-f0-9]{64})`).FindStringSubmatch(out)
	if key == nil {
		c.t.Fatal(out)
	}
	return key[1]
}
func (c scenarioGateCLI) write(doc string) {
	c.t.Helper()
	if err := os.WriteFile(taskFilePath(c.root, "XR-005"), []byte(doc), 0o644); err != nil {
		c.t.Fatal(err)
	}
}
func (c scenarioGateCLI) create(id string, given bool) {
	args := []string{"--ask", "сценарии", "--scenarios", "--hint", id + ": путь", "--option", id + " (слово пользователя): путь"}
	if given {
		args = append(args, "--given", id, "--said", "путь задан человеком")
	}
	c.run(true, append(args, "что включаем?")...)
}

func TestScenarioCLIGates(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка: %v\n%s", err, out)
	}
	fresh := func(t *testing.T) scenarioGateCLI {
		root := setup(t)
		t.Setenv("DEVKIT_TMUX", "")
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		t.Setenv("DEVKIT_HEADLESS", "1")
		return scenarioGateCLI{t, root, bin}
	}
	max := int(^uint(0) >> 1)
	last := fmt.Sprintf("СЦ-%d", max-2)
	exhausted := fmt.Sprintf("СЦ-%d", max-1)
	t.Run("создание и последняя граница", func(t *testing.T) {
		c := fresh(t)
		c.fail("диапазон исчерпан", "--ask", "сценарии", "--scenarios", "--hint", exhausted+": путь", "--option", exhausted+" (слово пользователя): путь", "что включаем?")
		addScenarioTestCards(t, c.root, "XR-005", last)
		c.create(last, true)
		c.snapshot()
		f, _ := taskform.FindFork(readTask(t, c.root, "XR-005"), "сценарии")
		if f.NextID != max-1 || f.Answer != last {
			t.Fatalf("последняя граница: %+v", f)
		}
	})
	t.Run("option и последняя граница", func(t *testing.T) {
		c := fresh(t)
		c.create("СЦ-1", false)
		c.fail("диапазон исчерпан", "сценарии", "--option", exhausted+" (слово пользователя): путь")
		c.run(true, "сценарии", "--option", last+" (слово пользователя): путь")
		addScenarioTestCards(t, c.root, "XR-005", "СЦ-1", last)
		c.snapshot()
		f, _ := taskform.FindFork(readTask(t, c.root, "XR-005"), "сценарии")
		if f.NextID != max-1 {
			t.Fatal(f.NextID)
		}
	})
	t.Run("свой сценарий и последняя граница", func(t *testing.T) {
		c := fresh(t)
		addScenarioTestCards(t, c.root, "XR-005", "СЦ-1")
		c.create("СЦ-1", false)
		doc, err := taskform.SetForkField(readTask(t, c.root, "XR-005"), "сценарии", "следующий ID", exhausted)
		if err != nil {
			t.Fatal(err)
		}
		c.write(doc)
		key := c.snapshot()
		c.fail("диапазон исчерпан", "сценарии", "--snapshot", key, "--answer", "сценарии 1; плюс новый")
		doc, _ = taskform.SetForkField(readTask(t, c.root, "XR-005"), "сценарии", "следующий ID", last)
		c.write(doc)
		key = c.snapshot()
		out := c.run(true, "сценарии", "--snapshot", key, "--answer", "плюс последний")
		if !strings.Contains(out, "добавлено: "+last) {
			t.Fatal(out)
		}
		addScenarioTestCards(t, c.root, "XR-005", last)
		c.snapshot()
		f, _ := taskform.FindFork(readTask(t, c.root, "XR-005"), "сценарии")
		if f.NextID != max-1 {
			t.Fatal(f.NextID)
		}
	})
	t.Run("экранирование и дословный ответ", func(t *testing.T) {
		c := fresh(t)
		addScenarioTestCards(t, c.root, "XR-005", "СЦ-1")
		c.create("СЦ-1", false)
		key := c.snapshot()
		for _, text := range []string{`сценарии 1; плюс "проверить путь C:\tmp"`, `плюс "буква \u0041"`, `плюс "буква \x41"`} {
			c.fail("неподдержанное экранирование", "сценарии", "--snapshot", key, "--answer", text)
		}
		text := ` плюс "путь C:\\tmp и \"кавычка\"" `
		c.run(true, "сценарии", "--snapshot", key, "--answer", text)
		f, _ := taskform.FindFork(readTask(t, c.root, "XR-005"), "сценарии")
		replies, err := f.ScenarioReplies()
		if err != nil || replies[0].Text != text || !strings.Contains(f.Options[1], `путь C:\tmp и "кавычка"`) {
			t.Fatalf("дословность потеряна: %+v %v", f, err)
		}
	})
	good := scenarioTestCards("СЦ-1")
	for _, bad := range []struct{ name, cards, reason string }{
		{"нет карточки", "", "СЦ-1"},
		{"нет поля", strings.Replace(good, "польза: сверить выбор\n", "", 1), "Польза"},
		{"поле снаружи", strings.Replace(good, "польза: сверить выбор\n", "", 1) + "\n## Другое\nПольза: снаружи\n", "Польза"},
		{"поле другого ID", strings.Replace(good, "польза: сверить выбор\n", "", 1) + "\n### СЦ-2\nПольза: чужая\n", "Польза"},
		{"поле в примере", strings.Replace(good, "польза: сверить выбор\n", "```text\nПольза: пример\n```\n", 1), "Польза"},
		{"карточка в примере", "\n## Пользовательские сценарии\n\n```text\n" + scenarioTestCard("СЦ-1", "пример") + "```\n", "СЦ-1"},
	} {
		for _, entry := range []string{"chat", "given"} {
			t.Run(bad.name+"/"+entry, func(t *testing.T) {
				c := fresh(t)
				base := readTask(t, c.root, "XR-005")
				if entry == "chat" {
					c.create("СЦ-1", false)
					base = readTask(t, c.root, "XR-005")
					c.write(base + bad.cards)
					c.fail(bad.reason, "сценарии", "--chat")
				} else {
					c.write(base + bad.cards)
					c.fail(bad.reason, "--ask", "сценарии", "--scenarios", "--hint", "СЦ-1: путь", "--option", "СЦ-1 (слово пользователя): путь", "--given", "СЦ-1", "--said", "путь задан", "что включаем?")
				}
			})
		}
	}
	t.Run("готовые карточки и последующее дополнение", func(t *testing.T) {
		c := fresh(t)
		addScenarioTestCards(t, c.root, "XR-005", "СЦ-1")
		c.create("СЦ-1", true)
		key := c.snapshot()
		f, _ := taskform.FindFork(readTask(t, c.root, "XR-005"), "сценарии")
		for _, sub := range f.Sub {
			if strings.HasPrefix(sub, "снимок: ") {
				_, raw, _ := strings.Cut(strings.TrimPrefix(sub, "снимок: "), " ")
				var snapshot taskform.ScenarioSnapshot
				if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || !strings.Contains(snapshot.Cards, "источник: слово пользователя") {
					t.Fatal("снимок не содержит подготовленных карточек")
				}
			}
		}
		text := "плюс свой сценарий"
		result := c.run(true, "сценарии", "--snapshot", key, "--answer", text)
		c.fail("СЦ-2", "сценарии", "--chat")
		before := readTask(t, c.root, "XR-005")
		if retry := c.run(true, "сценарии", "--snapshot", key, "--answer", text); retry != result || readTask(t, c.root, "XR-005") != before {
			t.Fatal("принятый свой сценарий нельзя повторить до карточки")
		}
		addScenarioTestCards(t, c.root, "XR-005", "СЦ-2")
		c.snapshot()
	})
}

func scenarioTestCard(id, situation string) string {
	return fmt.Sprintf("### %s\nситуация: %s\nдействие: назвать пункты\nрезультат: выбранные ID\nпольза: сверить выбор\nрекомендация: включить, нужен результат\nограничение: показанный блок\nисточник: слово пользователя\n", id, situation)
}

func scenarioTestCards(ids ...string) string {
	text := "\n## Пользовательские сценарии\n\n"
	for _, id := range ids {
		situation := "выбор"
		if id == "СЦ-2" {
			situation = "пример"
		}
		if id == "СЦ-3" {
			situation = "перерыв"
		}
		text += scenarioTestCard(id, situation) + "\n"
	}
	return text
}

func addScenarioTestCards(t *testing.T, root, id string, ids ...string) {
	t.Helper()
	path := taskFilePath(root, id)
	doc := readTask(t, root, id)
	cards := scenarioTestCards(ids...)
	if strings.Contains(doc, "## Пользовательские сценарии") {
		cards = strings.TrimPrefix(cards, "\n## Пользовательские сценарии\n\n")
	}
	if err := os.WriteFile(path, []byte(doc+"\n"+cards), 0o644); err != nil {
		t.Fatal(err)
	}
}

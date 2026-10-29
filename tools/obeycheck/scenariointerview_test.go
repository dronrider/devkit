package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const interviewNarrowActualFinal = `[OB-042 подготовлен к обсуждению](docs/tasks/drafts/OB-042.md).

- Падение воспроизведено, дублей нет.
- Сохранён заданный сценарий: ` + "`total`" + ` считает None нулём.
- Границы ограничены ` + "`total`" + ` и тестами. Чтение файла и ночной запуск отсутствуют в проекте.
- Новых вопросов человеку не выявлено.

Черновик оставлен в накопителе. Вычитка и ` + "`taskctl lint`" + ` пройдены. Коммит ` + "`6ed0c93`" + ` отправлен в origin.`

func TestInterviewScenarioChecks(t *testing.T) {
	devkit := devkitRoot(t)
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join(devkit, "tools", "taskctl")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("taskctl: %v\n%s", err, out)
	}
	ids := []string{"42-interview-cases-narrow", "43-interview-cases-wide"}
	list, err := loadScenarios(filepath.Join(devkit, "tools", "obeycheck", "scenarios"), devkit, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		section := "Заданный состав и продолжение"
		if strings.HasPrefix(s.ID, "43-") {
			section = "Создание и ответ"
		}
		support := false
		for _, subject := range s.Subjects {
			if subject.Path == "kit/skills/interview/scenarios.md" && subject.Section == section {
				support = true
			}
		}
		if !support {
			t.Fatalf("%s не покрывает раздел вспомогательного файла «%s»", s.ID, section)
		}
		states := []string{"исходная", "пустая", "работает", "журнал", "без команд", "без реплики", "старые составы", "потеряно поле", "поле снаружи", "чужая карточка", "поле в примере", "пустые cards"}
		if strings.HasPrefix(s.ID, "43-") {
			states = append(states, "карточки не доставлены", "тело не доставлено")
		} else {
			states = append(states, "итог без ID", "итог без None")
		}
		for _, state := range states {
			t.Run(s.ID+"/"+state, func(t *testing.T) {
				project := t.TempDir()
				transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
				env := append(os.Environ(), "HOME="+t.TempDir(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "DEVKIT_HEADLESS=1", "DEVKIT_TMUX=", "CLAUDE_CODE_SESSION_ID=", "OBEY_PROJECT="+project, "OBEY_TRANSCRIPT="+transcript, "OBEY_DEVKIT="+devkit)
				var commands []string
				run := func(args ...string) string {
					t.Helper()
					c := exec.Command(bin, args...)
					c.Dir = project
					c.Env = env
					out, err := c.CombinedOutput()
					if err != nil {
						t.Fatalf("%v: %v\n%s", args, err, out)
					}
					commands = append(commands, "taskctl "+strings.Join(args, " "))
					return string(out)
				}
				run("init", "--prefix", "OB")
				setup := exec.Command("sh", "-eu", "-c", s.Setup)
				setup.Dir = project
				setup.Env = env
				if out, err := setup.CombinedOutput(); err != nil {
					t.Fatalf("подготовка: %v\n%s", err, out)
				}
				record := "OB-043"
				count := 3
				if strings.HasPrefix(s.ID, "42-") {
					record = "OB-042"
					count = 1
				}
				path := filepath.Join(project, "docs", "tasks", "drafts", record+".md")
				final := "СЦ-1. total считает None нулём. Основание: запись. Выбор сохранён."
				if state == "пустая" {
					if err := os.WriteFile(path, nil, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if state != "исходная" && state != "пустая" {
					doc, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					cards := "\n## Пользовательские сценарии\n\n"
					origins := []string{"слово пользователя", "вывод из примера", "предложение агента"}
					titles := []string{"total считает None нулём", "отчёт по расписанию", "соседняя панель получает доли"}
					if count == 3 {
						titles[0] = "человек читает доли в терминале"
					}
					for i := 0; i < count; i++ {
						cards += fmt.Sprintf("### СЦ-%d\nСитуация: нужен результат.\nДействие: получить доли.\nРезультат: видны числа.\nПольза: сверить расчёт.\nРекомендация: нужен путь.\nОграничение: один потребитель.\nИсточник: запись.\n\n", i+1)
					}
					if err := os.WriteFile(path, append(doc, []byte(cards)...), 0o644); err != nil {
						t.Fatal(err)
					}
					if state == "старые составы" {
						run("decide", record, "--ask", "кейсы", "--hint", "сбалансированный состав, 2 кейса: (1) первый; (2) второй", "--option", "узкий состав, 1 кейс: (1) первый", "--option", "широкий состав, 3 кейса: (1) первый; (2) второй; (3) третий", "что включаем?")
						final = run("decide", record, "--chat")
					} else {
						hint := "СЦ-1: путь задан"
						if count == 3 {
							hint = "СЦ-1, СЦ-3: нужны чтение и панель"
						}
						args := []string{"decide", record, "--ask", "сценарии", "--scenarios", "--hint", hint}
						for i := 0; i < count; i++ {
							args = append(args, "--option", fmt.Sprintf("СЦ-%d (%s): %s", i+1, origins[i], titles[i]))
						}
						if count == 1 {
							args = append(args, "--given", "СЦ-1", "--said", "из записи: total считает None нулём")
						}
						args = append(args, "что включаем?")
						run(args...)
						if count == 1 {
							final = "СЦ-1 сохранён в записи. Основание: прямой текст записи OB-042, total считает None нулём."
						} else {
							final = run("decide", record, "сценарии", "--chat")
						}
					}
				}
				expectedReason := ""
				if state == "потеряно поле" || state == "поле снаружи" || state == "поле в примере" || state == "чужая карточка" {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					doc := string(data)
					switch state {
					case "потеряно поле":
						doc = strings.Replace(doc, "Польза: сверить расчёт.\n", "", 1)
						expectedReason = "польза"
					case "поле снаружи":
						doc = strings.Replace(doc, "Польза: сверить расчёт.\n", "", 1) + "\n## Другое\nПольза: сверить расчёт.\n"
						expectedReason = "польза"
					case "поле в примере":
						doc = strings.Replace(doc, "Польза: сверить расчёт.\n", "```text\nПольза: сверить расчёт.\n```\n", 1)
						expectedReason = "польза"
					case "чужая карточка":
						cardRe := regexp.MustCompile(`(?ms)^### СЦ-1\n.*?(?:\n\n|$)`)
						card := cardRe.FindString(doc)
						if card == "" {
							t.Fatal("нет исходной карточки фикстуры")
						}
						doc = strings.Replace(doc, card, "", 1) + "\n## Другое\n" + card
						expectedReason = "нет карточки СЦ-1"
					}
					if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if state == "пустые cards" {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					doc := string(data)
					snapshotRe := regexp.MustCompile(`(?m)^  - снимок: (sc1-[a-f0-9]{64}) (.+)$`)
					match := snapshotRe.FindStringSubmatch(doc)
					if match == nil {
						t.Fatal("нет исходного снимка фикстуры")
					}
					var snapshot map[string]any
					if err := json.Unmarshal([]byte(match[2]), &snapshot); err != nil {
						t.Fatal(err)
					}
					snapshot["cards"] = ""
					raw, _ := json.Marshal(snapshot)
					sum := sha256.Sum256(raw)
					key := fmt.Sprintf("sc1-%x", sum)
					doc = strings.Replace(doc, match[0], "  - снимок: "+match[1]+" "+string(raw), 1)
					doc = strings.ReplaceAll(doc, match[1], key)
					final = strings.ReplaceAll(final, match[1], key)
					if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
						t.Fatal(err)
					}
					expectedReason = "нет карточки СЦ-1"
				}
				if state == "тело не доставлено" {
					final = strings.Replace(final, "Польза: сверить расчёт.\n", "", 1)
					expectedReason = "польза"
				}
				if state == "карточки не доставлены" {
					final = strings.ReplaceAll(final, "### СЦ-", "Карточка СЦ-")
				}
				if state == "итог без ID" {
					final = interviewNarrowActualFinal
				}
				if state == "итог без None" {
					final = "Заданный сценарий сохранён в записи."
				}
				if state == "без реплики" {
					final = ""
				}
				var trace strings.Builder
				if state == "журнал" {
					var log strings.Builder
					for i, command := range commands {
						fmt.Fprintf(&log, "start\t%d\t%s\nend\t%d\t0\n", i, command, i)
					}
					if err := os.WriteFile(filepath.Join(project, ".obey-taskctl.log"), []byte(log.String()), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if state == "журнал" || state == "без команд" {
					commands = nil
				}
				for _, command := range commands {
					data, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "command_execution", "command": command}})
					trace.Write(data)
					trace.WriteByte('\n')
				}
				data, _ := json.Marshal(map[string]any{"type": "result", "result": final})
				trace.Write(data)
				trace.WriteByte('\n')
				if err := os.WriteFile(transcript, []byte(trace.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				check := exec.Command("sh", "-eu", "-c", s.Check)
				check.Dir = project
				check.Env = env
				out, err := check.CombinedOutput()
				if (err == nil) != (state == "работает" || state == "журнал" || state == "итог без ID" || state == "итог без None") {
					t.Fatalf("состояние %s: %v\n%s", state, err, out)
				}
				if err != nil && expectedReason != "" && !strings.Contains(string(out), expectedReason) {
					t.Fatalf("отказ не проверил нужную причину %q: %s", expectedReason, out)
				}
				if err == nil && !strings.Contains(string(out), "сохран") {
					t.Fatalf("успех без положительного результата: %s", out)
				}
			})
		}
	}
}

func TestInterviewDecideBlockHeadless(t *testing.T) {
	devkit := devkitRoot(t)
	list, err := loadScenarios(filepath.Join(devkit, "tools", "obeycheck", "scenarios"), devkit, []string{"43-interview-decide-block"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := list[0]
	for _, name := range []string{"DEVKIT_HEADLESS", "DEVKIT_RUN_DEPTH", "DEVKIT_TMUX"} {
		t.Setenv(name, "parent-session")
	}
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-py")
	e := &runEnv{Home: t.TempDir(), Project: t.TempDir()}
	env := append(e.environ(devkit, "fixture", s.ID, 1), s.Env...)
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join(devkit, "tools", "taskctl")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("taskctl: %v\n%s", err, out)
	}
	env = append(env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir, cmd.Env = e.Project, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "--prefix", "OB")
	setup := exec.Command("sh", "-eu", "-c", s.Setup)
	setup.Dir, setup.Env = e.Project, env
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("подготовка: %v\n%s", err, out)
	}
	out := run("decide", "OB-004", "--chat")
	for _, want := range []string{
		"заход поднят автоматикой, DEVKIT_HEADLESS=obeycheck",
		"«формат»: Каким форматом уходит отчёт смежнику?",
		"«срок»: Когда отчёт уходит?",
		"ответ строкой:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("нет %q в печатном блоке:\n%s", want, out)
		}
	}
	for _, n := range []string{"1. ", "2. ", "3. "} {
		if count := strings.Count(out, "\n"+n); count != 2 {
			t.Errorf("позиция %s встретилась %d раз вместо двух:\n%s", n, count, out)
		}
	}
	if strings.Contains(out, "у экрана человек") || strings.Contains(out, "те же варианты идут диалогом") {
		t.Fatalf("сценарий запросил диалог вместо печатного блока:\n%s", out)
	}
}

func writeShownProposal(t *testing.T, project, rawCards, mode string) {
	t.Helper()
	var cards []map[string]any
	if err := json.Unmarshal([]byte(rawCards), &cards); err != nil {
		t.Fatal(err)
	}
	var active []map[string]any
	for _, card := range cards {
		if card["status"] == nil || card["status"] == "" {
			active = append(active, card)
		}
	}
	basis := "basis-shown"
	tree, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "устаревший basis" {
		basis = "other"
	}
	if mode == "другая редакция" && len(active) > 0 {
		active[0]["revision"] = 99
	}
	question := map[string]any{"questions": []any{map[string]any{"text": "выбор", "proposal": map[string]any{"version": 1, "tree": tree, "anchor": "OB-069", "sources": []any{map[string]any{"source": "OB-069", "basis": basis, "proposals": active}}}}}}
	data, err := json.Marshal(question)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, ".devkit", "chat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-OB-069.ask"), append([]byte("-\nзадача OB-069\n"), append(data, '\n')...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGroomProposalScenarioChecks(t *testing.T) {
	devkit := devkitRoot(t)
	list, err := loadScenarios(filepath.Join(devkit, "tools", "obeycheck", "scenarios"), devkit, []string{"69-groom-proposals"}, nil)
	if err != nil || len(list) != 1 {
		t.Fatalf("сценарий: %v, найдено %d", err, len(list))
	}
	for _, tc := range []struct {
		name, log, proposals string
		pass                 bool
	}{
		{"порядок", "taskctl draft proposals save --help\ntaskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, true},
		{"ошибка до интервью", "taskctl draft proposals save OB-069 --base abc [exit=1]\ntaskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, true},
		{"только ошибка", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc [exit=1]\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"наоборот", "taskctl draft proposals save OB-069 --base abc\ntaskctl decide OB-069 --scenarios\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"нет вызовов", "", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"нет редакции", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1"},{"id":"p2","revision":1}]`, false},
		{"нет вопроса", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"нет ask вызова", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"устаревший basis", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"другая редакция", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"без сессии", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"применение answer", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\ntaskctl draft proposals answer OB-069\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"прямое применение", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\ntaskctl draft drop OB-069\n", `[{"id":"p1","revision":1},{"id":"p2","revision":1}]`, false},
		{"применённая карточка", "taskctl decide OB-069 --scenarios\ntaskctl draft proposals save OB-069 --base abc\n", `[{"id":"p1","revision":1,"status":"accepted"},{"id":"p2","revision":1}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			binDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(project, "docs", "tasks", "drafts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "docs", "tasks", "drafts", "OB-069.md"), []byte("выбор: сценарии/1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			logText := tc.log
			if tc.name != "нет ask вызова" {
				logText += "taskctl draft proposals ask OB-069\n"
			}
			if tc.name != "нет вопроса" {
				writeShownProposal(t, project, tc.proposals, tc.name)
			}
			transcript := filepath.Join(project, "transcript")
			trace := "{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"готово\"}}\n{\"type\":\"turn.completed\"}\n"
			if tc.name == "без сессии" {
				trace = ""
			}
			if err := os.WriteFile(transcript, []byte(trace), 0o644); err != nil {
				t.Fatal(err)
			}
			var log strings.Builder
			for i, command := range strings.Split(strings.TrimSpace(logText), "\n") {
				if command != "" {
					status := 0
					if strings.HasSuffix(command, " [exit=1]") {
						status = 1
						command = strings.TrimSuffix(command, " [exit=1]")
					}
					fmt.Fprintf(&log, "start\t%d\t%s\nend\t%d\t%d\n", i, command, i, status)
				}
			}
			if err := os.WriteFile(filepath.Join(project, ".obey-taskctl.log"), []byte(log.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			fake := "#!/bin/sh\nprintf '%s\\n' '{\"basis\":\"basis-shown\",\"state\":{\"proposals\":" + tc.proposals + "}}'\n"
			if err := os.WriteFile(filepath.Join(binDir, "taskctl"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			check := exec.Command("sh", "-eu", "-c", list[0].Check)
			check.Dir = project
			check.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "OBEY_TRANSCRIPT="+transcript)
			out, err := check.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("проверка: %v\n%s", err, out)
			}
		})
	}
}

func TestGroomPrivateDuplicateScenarioChecks(t *testing.T) {
	devkit := devkitRoot(t)
	list, err := loadScenarios(filepath.Join(devkit, "tools", "obeycheck", "scenarios"), devkit, []string{"70-groom-private-duplicates"}, nil)
	if err != nil || len(list) != 1 {
		t.Fatalf("сценарий: %v, найдено %d", err, len(list))
	}
	for _, tc := range []struct {
		name, trace, output, log, proposals string
		pass                                bool
	}{
		{"работает", "session", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, true},
		{"нет сессии", "empty", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, false},
		{"закрытый активный заголовок", "session", "active-title", "safe", `[{"id":"p1","revision":1}]`, false},
		{"закрытое архивное тело", "session", "archive-body", "safe", `[{"id":"p1","revision":1}]`, false},
		{"нет безопасного списка", "session", "обычный вывод", "missing", `[{"id":"p1","revision":1}]`, false},
		{"нет интервью", "session", "обычный вывод", "nointerview", `[{"id":"p1","revision":1}]`, false},
		{"нет сценариев", "session", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, false},
		{"save параллельно интервью", "session", "обычный вывод", "parallel-save", `[{"id":"p1","revision":1}]`, false},
		{"ask параллельно save", "session", "обычный вывод", "parallel-ask", `[{"id":"p1","revision":1}]`, false},
		{"нет публичного результата", "session", "обычный вывод", "safe", `[]`, false},
		{"совпадение маркеров", "session", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, false},
		{"нет вопроса", "session", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, false},
		{"устаревший basis", "session", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, false},
		{"другая редакция", "session", "обычный вывод", "safe", `[{"id":"p1","revision":1}]`, false},
		{"нет ask вызова", "session", "обычный вывод", "noask", `[{"id":"p1","revision":1}]`, false},
		{"применение answer", "session", "обычный вывод", "answer", `[{"id":"p1","revision":1}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, home, binDir := t.TempDir(), t.TempDir(), t.TempDir()
			markers := []string{"fixture-" + strings.Repeat("a", 24), "fixture-" + strings.Repeat("b", 24), "fixture-" + strings.Repeat("c", 24), "fixture-" + strings.Repeat("d", 24)}
			if tc.name == "совпадение маркеров" {
				markers[1] = markers[0]
			}
			data, err := json.Marshal(markers)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".obey-private-markers.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{"docs/TASKS.md": markers[0], "docs/TASKS-archive.md": markers[2], "docs/tasks/OB-070.md": markers[0] + "\n" + markers[1], "docs/tasks/archive/2026/OB-071.md": markers[2] + "\n" + markers[3]}
			for name, content := range files {
				path := filepath.Join(project, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(project, "docs", "tasks", "drafts"), 0o755); err != nil {
				t.Fatal(err)
			}
			source := "публичная запись\nвыбор: сценарии/1\n"
			if tc.name == "нет сценариев" {
				source = "публичная запись\n"
			}
			if err := os.WriteFile(filepath.Join(project, "docs", "tasks", "drafts", "OB-069.md"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			log := "start\t1\ttaskctl guard OB-069\nend\t1\t0\nstart\t2\ttaskctl list --safe\nend\t2\t0\nstart\t3\ttaskctl draft list --safe\nend\t3\t0\nstart\t9\ttaskctl decide OB-069 --scenarios\nend\t9\t0\nstart\t4\ttaskctl draft proposals save OB-069 --base abc\nend\t4\t0\nstart\t5\ttaskctl draft proposals ask OB-069\nend\t5\t0\n"
			if tc.log == "missing" {
				log = "start\t1\ttaskctl guard OB-069\nend\t1\t0\n"
			}
			if tc.log == "nointerview" {
				log = strings.Replace(log, "start\t9\ttaskctl decide OB-069 --scenarios\nend\t9\t0\n", "", 1)
			}
			if tc.log == "parallel-save" {
				log = strings.Replace(log, "end\t9\t0\nstart\t4\ttaskctl draft proposals save OB-069 --base abc\n", "start\t4\ttaskctl draft proposals save OB-069 --base abc\nend\t9\t0\n", 1)
			}
			if tc.log == "parallel-ask" {
				log = strings.Replace(log, "end\t4\t0\nstart\t5\ttaskctl draft proposals ask OB-069\n", "start\t5\ttaskctl draft proposals ask OB-069\nend\t4\t0\n", 1)
			}
			if tc.log == "noask" {
				log = strings.Replace(log, "start\t5\ttaskctl draft proposals ask OB-069\nend\t5\t0\n", "", 1)
			}
			if tc.log == "answer" {
				log += "start\t6\ttaskctl draft proposals answer OB-069\nend\t6\t0\n"
			}
			if tc.name != "нет вопроса" {
				writeShownProposal(t, project, tc.proposals, tc.name)
			}
			if err := os.WriteFile(filepath.Join(project, ".obey-taskctl.log"), []byte(log), 0o644); err != nil {
				t.Fatal(err)
			}
			transcript := filepath.Join(project, "transcript")
			trace := ""
			if tc.trace == "session" {
				trace = "{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"готово\"}}\n{\"type\":\"turn.completed\"}\n"
			}
			if err := os.WriteFile(transcript, []byte(trace), 0o644); err != nil {
				t.Fatal(err)
			}
			sessions := filepath.Join(home, ".codex", "sessions")
			if err := os.MkdirAll(sessions, 0o755); err != nil {
				t.Fatal(err)
			}
			output := tc.output
			if output == "active-title" {
				output = markers[0]
			}
			if output == "archive-body" {
				output = markers[3]
			}
			entry, _ := json.Marshal(map[string]any{"payload": map[string]any{"type": "custom_tool_call_output", "output": output}})
			if err := os.WriteFile(filepath.Join(sessions, "rollout-test.jsonl"), append(entry, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			fake := "#!/bin/sh\nprintf '%s\\n' '{\"basis\":\"basis-shown\",\"state\":{\"proposals\":" + tc.proposals + "}}'\n"
			if err := os.WriteFile(filepath.Join(binDir, "taskctl"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			check := exec.Command("sh", "-eu", "-c", list[0].Check)
			check.Dir = project
			check.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "OBEY_HOME="+home, "OBEY_TRANSCRIPT="+transcript)
			out, err := check.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("проверка: %v\n%s", err, out)
			}
		})
	}
}

func TestGroomPrivateDuplicateFixture(t *testing.T) {
	devkit := devkitRoot(t)
	list, err := loadScenarios(filepath.Join(devkit, "tools", "obeycheck", "scenarios"), devkit, []string{"70-groom-private-duplicates"}, nil)
	if err != nil || len(list) != 1 {
		t.Fatalf("сценарий: %v, найдено %d", err, len(list))
	}
	project := t.TempDir()
	for _, name := range []string{"README.md", "docs/TASKS.md", "docs/TASKS-archive.md"} {
		data, err := os.ReadFile(filepath.Join(devkit, "tools", "obeycheck", "testdata", "project", name))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(project, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setup := exec.Command("sh", "-eu", "-c", list[0].Setup)
	setup.Dir = project
	home := t.TempDir()
	setup.Env = append(os.Environ(), "OBEY_HOME="+home)
	if out, err := setup.CombinedOutput(); err != nil {
		t.Fatalf("подготовка: %v\n%s", err, out)
	}
	var markers []string
	data, err := os.ReadFile(filepath.Join(home, ".obey-private-markers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &markers); err != nil || len(markers) != 4 {
		t.Fatalf("маркеры: %v, %v", markers, err)
	}
	for name, needles := range map[string][]string{
		"README.md":                         {"python3 server.py", "old-help"},
		"server.py":                         {"HTTPServer", "serve_forever"},
		"docs/tasks/OB-070.md":              {"защита: locked", markers[0], markers[1]},
		"docs/tasks/archive/2026/OB-071.md": {"защита: locked", markers[2], markers[3]},
		"docs/TASKS.md":                     {markers[0]}, "docs/TASKS-archive.md": {markers[2]},
	} {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range needles {
			if !strings.Contains(string(data), needle) {
				t.Errorf("%s: нет %q", name, needle)
			}
		}
	}
}

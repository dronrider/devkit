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
		states := []string{"исходная", "пустая", "работает", "без реплики", "старые составы", "потеряно поле", "поле снаружи", "чужая карточка", "поле в примере", "пустые cards"}
		if strings.HasPrefix(s.ID, "43-") {
			states = append(states, "карточки не доставлены", "тело не доставлено")
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
							final = "СЦ-1: total считает None нулём. Основание: прямой текст записи OB-042."
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
				if state == "без реплики" {
					final = ""
				}
				var trace strings.Builder
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
				if (err == nil) != (state == "работает") {
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

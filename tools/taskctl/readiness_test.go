package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureRankReasons = "- Серьёзность 0: работа продолжается.\n- Ценность 1: касается одного сценария.\n- Неопределённость 1: место правки известно.\n- Поправка на баг 0: новая работа.\n- Рычаг 1: убирает повторный разбор.\n"
const fixtureTaskBody = "\n## Что происходит\n\nТестовый запуск обнаружил пропуск проверки.\n\n## Чего хотим\n\nПроверка выполняется до записи.\n\n## DoD\n\n- Неполная запись отклоняется.\n\n## Ранг\n\n`0+1+1+0+1 = 3`, P3.\n\n" + fixtureRankReasons

func fixtureAddBody(t *testing.T, root, id string) string {
	t.Helper()
	if id != "" {
		if _, err := os.Stat(draftPath(root, id)); err == nil {
			return ""
		}
	}
	if _, err := os.Stat(taskFileAbs(root, id)); id != "" && err == nil {
		return taskFileAbs(root, id)
	}
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(fixtureTaskBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddBodyFileKeepsDescription(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		t.Run(map[bool]string{false: "файл", true: "stdin"}[stdin], func(t *testing.T) {
			root := setup(t)
			body := strings.Replace(fixtureTaskBody, "`0+1+1+0+1 = 3`, P3.\n\n", "", 1)
			body += "\n## Контекст\n\nУсловие: публикацию подтверждает человек.\n\n```markdown\n## DoD\nпример чужой карточки\n```\n"
			source := filepath.Join(t.TempDir(), "body.md")
			if err := os.WriteFile(source, []byte("# Старый заголовок\n"+body), 0o644); err != nil {
				t.Fatal(err)
			}
			if stdin {
				f, err := os.Open(source)
				if err != nil {
					t.Fatal(err)
				}
				original := os.Stdin
				os.Stdin = f
				t.Cleanup(func() { os.Stdin = original; f.Close() })
				source = "-"
			}
			_, err := cmdAdd(root, AddParams{Title: "Готовая постановка", Type: "task", Rank: "25+4+2+0+2", Accept: "agent", BodyFile: source})
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(taskFileAbs(root, "XR-008"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"# XR-008: Готовая постановка", "`25+4+2+0+2 = 33`, P2.", "Условие: публикацию подтверждает человек.", "```markdown\n## DoD\nпример чужой карточки\n```"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("нет %q: %s", want, data)
				}
			}
			if strings.Contains(string(data), "Старый заголовок") {
				t.Fatal("H1 не обновлён")
			}
		})
	}
}

func TestAddIgnoresCommentedSections(t *testing.T) {
	for _, promote := range []bool{false, true} {
		t.Run(map[bool]string{false: "прямо", true: "черновик"}[promote], func(t *testing.T) {
			root := setup(t)
			hidden := "<!--\n## Ранг\nПример ранга.\n## Приёмка\n\n- вид: mixed\n- барьер «доступ»:\n## Проверка\n-->\n"
			p := AddParams{Title: "Постановка с примером", Type: "task", Rank: "0+1+1+0+1", Accept: "mixed", Barrier: "глаза"}
			if promote {
				if _, err := cmdDraft(root, draftSCQA, "mid", CommitOpts{}); err != nil {
					t.Fatal(err)
				}
				giveDraftDoD(t, root, "XR-008")
				data, err := os.ReadFile(draftPath(root, "XR-008"))
				if err != nil {
					t.Fatal(err)
				}
				text := strings.Replace(string(data), "## Черновик\n", "## Черновик\n\n"+hidden, 1)
				if err := os.WriteFile(draftPath(root, "XR-008"), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
				p.ID = "XR-008"
			} else {
				p.BodyFile = filepath.Join(t.TempDir(), "body.md")
				text := strings.Replace(fixtureTaskBody, "## Что происходит\n", "## Что происходит\n\n"+hidden, 1)
				if err := os.WriteFile(p.BodyFile, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := cmdAdd(root, p); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(taskFileAbs(root, "XR-008"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), hidden) {
				t.Fatal("скрытый пример потерян")
			}
			section, found, _ := readTaskSection(root, "XR-008", acceptanceHeading)
			if !found || !strings.Contains(section, "барьер «глаза»") || strings.Contains(section, "барьер «доступ»") {
				t.Fatalf("приёмка подменена скрытым примером: %s", section)
			}
		})
	}
}

func TestPromotedDraftPreservesProtection(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	path := draftPath(root, "XR-008")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "## ", "тип: task\nисточник: mail\nзамок: locked\n\n## ", 1) + fixtureTaskBody)
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	body, _, err := prepareTaskBody(root, "XR-008", AddParams{Title: "Задача", Type: "task"}, "`0+1+1+0+1 = 3`, P3.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "защита: locked") || !strings.Contains(body, "Тип task, источник mail") {
		t.Fatalf("provenance: %s", body)
	}
}

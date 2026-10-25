package taskform

import (
	"strings"
	"testing"
)

const readyRank = "`25+4+2+5+2 = 38`, P2.\n\n- Серьёзность 25: работа замедляется.\n- Ценность 4: затронуты частые запуски.\n- Неопределённость 2: нужен разбор соседнего вызова.\n- Поправка на баг 5: исправление дефекта.\n- Рычаг 2: снимает повторную диагностику.\n"
const readyTask = "## Что происходит\n\nКоманда записывает пустую задачу.\n\n## Чего хотим\n\nКоманда отказывает до записи.\n\n## DoD\n\n- Пустая постановка отклонена.\n\n## Ранг\n\n" + readyRank

func TestValidateTask(t *testing.T) {
	cases := []struct{ name, doc, missing string }{
		{"готовая", readyTask, ""},
		{"код в наблюдении", strings.Replace(readyTask, "Команда записывает пустую задачу.", "```sh\ntaskctl add --title x\n```", 1), ""},
		{"заголовки в коде", "```markdown\n" + readyTask + "```\n", "Что происходит"},
		{"комментарий", strings.Replace(readyTask, "Команда записывает пустую задачу.", "<!-- факт\nне записан -->", 1), "Что происходит"},
		{"заголовок в комментарии", "<!--\n" + readyTask + "-->\n", "Что происходит"},
		{"пустые пункты", strings.Replace(readyTask, "- Пустая постановка отклонена.", "- [ ]\n-\n1.\n*", 1), "DoD"},
		{"HTML inline", strings.Replace(readyTask, "Команда записывает пустую задачу.", "Парсер теряет текст после литерала `<!--` в описании.", 1), ""},
		{"HTML inline с backtick", strings.Replace(readyTask, "Команда записывает пустую задачу.", "Парсер теряет литерал `` `<!--` ``.", 1), ""},
		{"препроцессор в коде", strings.Replace(readyTask, "Команда записывает пустую задачу.", "```c\n#include <stdio.h>\n```", 1), ""},
		{"HTML в примере", strings.Replace(readyTask, "Команда записывает пустую задачу.", "```html\n<!-- проверяемый HTML-комментарий -->\n```", 1), ""},
		{"пустой код", strings.Replace(readyTask, "Команда записывает пустую задачу.", "```sh\n```", 1), "Что происходит"},
		{"подраздел", strings.Replace(readyTask, "Команда отказывает до записи.", "### Решение", 1), "Чего хотим"},
		{"заглушка формы", strings.Replace(readyTask, "- Пустая постановка отклонена.", "(конец работы сверх общего DoD)", 1), "DoD"},
		{"нет причины", strings.Replace(readyTask, "- Рычаг 2: снимает повторную диагностику.", "- Рычаг 2: <!-- причина -->", 1), "Рычаг"},
		{"причины в коде", strings.Replace(readyTask, readyRank, "```\n"+readyRank+"```", 1), "причина"},
		{"соседний раздел", strings.Replace(readyTask, "## Ранг\n\n", "## Ранг\n\n`25+4+2+5+2 = 38`, P2.\n\n## Ранг прошлой задачи\n\n", 1), "причина"},
		{"только формула", strings.Replace(readyTask, readyRank, "`25+4+2+5+2 = 38`, P2.", 1), "причина"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTask(c.doc)
			if c.missing == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.missing) {
				t.Fatalf("ждали %s, получили %v", c.missing, err)
			}
		})
	}
}

func TestWithRankFormula(t *testing.T) {
	next := "`0+1+1+0+1 = 3`, P3."
	got := WithRankFormula(readyTask, next)
	if strings.Count(got, next) != 1 || strings.Contains(got, "25+4+2+5+2") || !strings.Contains(got, "- Рычаг 2:") {
		t.Fatal(got)
	}
	if got != WithRankFormula(got, next) {
		t.Fatal("повторная нормализация меняет текст")
	}
	if err := ValidateTask(got); err != nil {
		t.Fatal(err)
	}
}

func TestWithRankFormulaIgnoresCommentHeadings(t *testing.T) {
	doc := "<!--\n## Ранг\n```\n-->\n" + readyTask
	got := WithRankFormula(doc, "`0+1+1+0+1 = 3`, P3.")
	if err := ValidateTask(got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "25+4+2+5+2") {
		t.Fatal("формула настоящего раздела не обновлена")
	}
}

func TestWithRankFormulaPreservesExplanation(t *testing.T) {
	explanation := "- Серьёзность 25: прежний расчёт `25+4+2+5+2 = 38`, P2."
	doc := strings.Replace(readyTask, "- Серьёзность 25: работа замедляется.", explanation, 1)
	got := WithRankFormula(doc, "`0+1+1+0+1 = 3`, P3.")
	if !strings.Contains(got, explanation) {
		t.Fatal("причина со старой формулой потеряна")
	}
}

func TestWithRankFormulaKeepsCommentedExample(t *testing.T) {
	hidden := "<!--\n`0+1+1+0+1 = 3`, P3.\n-->"
	doc := strings.Replace(readyTask, "- Серьёзность", hidden+"\n\n- Серьёзность", 1)
	got := WithRankFormula(doc, "`50+4+2+5+2 = 63`, P1.")
	if !strings.Contains(got, hidden) {
		t.Fatal("скрытый пример формулы изменился")
	}
	if err := ValidateTask(got); err != nil {
		t.Fatal(err)
	}
}

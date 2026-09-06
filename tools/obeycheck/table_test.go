package main

import (
	"strings"
	"testing"
)

func cellOf(green, red int) cell {
	var c cell
	for i := 0; i < green; i++ {
		c.Attempts = append(c.Attempts, attempt{Green: true, Repeat: len(c.Attempts) + 1})
	}
	for i := 0; i < red; i++ {
		c.Attempts = append(c.Attempts, attempt{Repeat: len(c.Attempts) + 1, Note: "проверка: exit status 1"})
	}
	return c
}

// Пять слов вердикта. Разницу считает значимой точный тест Фишера, поэтому
// клетка туда-сюда при k=5 это «не хуже», а не просадка: на сценарии с
// вероятностью зелёной клетки 0.8 кандидат отстаёт на клетку в трети прогонов
// при одинаковом тексте.
func TestVerdictWords(t *testing.T) {
	cases := []struct {
		name         string
		candG, baseG int
		repeats      int
		want         string
	}{
		{"зелёный на обеих", 5, 5, 5, verdictBothGreen},
		{"красный на обеих", 0, 0, 5, verdictBothRed},
		{"польза", 5, 1, 5, verdictGain},
		{"просадка", 1, 5, 5, verdictDrop},
		{"клетка туда-сюда это не хуже", 4, 5, 5, verdictNoWorse},
		{"четыре против двух незначимы", 4, 2, 5, verdictNoWorse},
		{"полный провал кандидата", 0, 5, 5, verdictDrop},
		{"тройка против нуля при k 3", 3, 0, 3, verdictGain},
		{"двойка против нуля при k 3", 2, 0, 3, verdictNoWorse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := verdictOf(cellOf(c.candG, c.repeats-c.candG), cellOf(c.baseG, c.repeats-c.baseG), c.repeats)
			if got != c.want {
				t.Fatalf("вердикт %q, ждал %q", got, c.want)
			}
		})
	}
}

// Точный тест Фишера считается гипергеометрикой, и значение сверяется с
// таблицей решения LLD DK-805: при k=5 значима разница 5/5 против 1/5 и 4/5
// против 0/5, а 4/5 против 2/5 нет.
func TestFisherExact(t *testing.T) {
	cases := []struct {
		name        string
		a, b, c, d  int
		want        float64
		significant bool
	}{
		{"5/5 против 1/5", 5, 0, 1, 4, 6.0 / 252, true},
		{"4/5 против 0/5", 4, 1, 0, 5, 6.0 / 252, true},
		{"4/5 против 2/5", 4, 1, 2, 3, 66.0 / 252, false},
		{"5/5 против 2/5", 5, 0, 2, 3, 21.0 / 252, false},
		{"3/3 против 0/3", 3, 0, 0, 3, 1.0 / 20, true},
		{"10/10 против 6/10", 10, 0, 6, 4, 8008.0 / 184756, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fisherP(c.a, c.b, c.c, c.d)
			if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("p = %g, ждал %g", got, c.want)
			}
			if (got <= alpha) != c.significant {
				t.Fatalf("значимость при p = %g вышла %v", got, got <= alpha)
			}
		})
	}
}

// Зачёт зависит от базы: с базой «старый» зачтены три вердикта, с базой
// «пусто» только польза. Новый текст, без которого агент ведёт себя так же, в
// резидент не едет.
func TestBaseCounting(t *testing.T) {
	cases := []struct {
		verdict, base string
		want          bool
	}{
		{verdictGain, baseOld, true},
		{verdictNoWorse, baseOld, true},
		{verdictBothGreen, baseOld, true},
		{verdictDrop, baseOld, false},
		{verdictBothRed, baseOld, false},
		{verdictGain, baseEmpty, true},
		{verdictNoWorse, baseEmpty, false},
		{verdictBothGreen, baseEmpty, false},
		{verdictBothRed, baseEmpty, false},
	}
	for _, c := range cases {
		t.Run(c.verdict+" при базе "+c.base, func(t *testing.T) {
			if got := counted(c.verdict, c.base); got != c.want {
				t.Fatalf("зачёт %v, ждал %v", got, c.want)
			}
		})
	}
	if err := checkBase("никакая"); err == nil {
		t.Fatal("ждал отказ на неизвестной базе")
	}
}

func TestRenderTable(t *testing.T) {
	rows := []row{
		{
			Scenario: Scenario{ID: "01-doc", Title: "написать абзац документации"},
			Cells:    []cell{cellOf(5, 0), cellOf(5, 0)},
			Verdict:  verdictBothGreen,
		},
		{
			Scenario: Scenario{ID: "02-bug", Title: "починить баг"},
			Cells:    []cell{cellOf(1, 4), cellOf(5, 0)},
			Verdict:  verdictDrop,
		},
		{
			Scenario: Scenario{ID: "10-delegate", Title: "отдать задачу исполнителю", End: endSession},
			Skipped:  true,
			Verdict:  "пропущен, конец сессия",
		},
	}
	got := render(rows, []string{"layouts/кандидат", "layouts/старый"}, 5, baseOld)
	// Ширины колонок это дело таблицы: строки сверяются со схлопнутыми
	// пробелами, иначе тест ломается от лишнего знака в имени раскладки.
	flatGot := flat(got)
	for _, want := range []string{
		"написать абзац документации 5/5 5/5 зелёный на обеих",
		"починить баг 1/5 5/5 просадка",
		"отдать задачу исполнителю - - пропущен, конец сессия",
	} {
		if !strings.Contains(flatGot, want) {
			t.Errorf("в таблице нет %q:\n%s", want, got)
		}
	}
	for _, want := range []string{
		"сценарий",
		"кандидат",
		"старый",
		"итог: сценариев 3, не зачтено 1, база старый",
		"02-bug: просадка, у кандидата значимо меньше зелёных",
		"первый красный прогон каждого угла:",
		"02-bug / кандидат / повтор 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в таблице нет %q:\n%s", want, got)
		}
	}
}

// Тот же прогон с базой «пусто» не зачтён: текст ничего не меняет.
func TestRenderEmptyBase(t *testing.T) {
	rows := []row{{
		Scenario: Scenario{ID: "01-doc", Title: "написать абзац"},
		Cells:    []cell{cellOf(5, 0), cellOf(5, 0)},
		Verdict:  verdictBothGreen,
	}}
	if got := render(rows, []string{"кандидат", "пусто"}, 5, baseEmpty); !strings.Contains(got,
		"итог: сценариев 1, не зачтено 1, база пусто") || !strings.Contains(got, "текст ничего не меняет") {
		t.Fatalf("итог с базой «пусто»:\n%s", got)
	}
}

func TestRenderCleanRun(t *testing.T) {
	rows := []row{{
		Scenario: Scenario{ID: "01-doc", Title: "написать абзац"},
		Cells:    []cell{cellOf(5, 0), cellOf(1, 4)},
		Verdict:  verdictGain,
	}}
	got := render(rows, []string{"кандидат", "старый"}, 5, baseOld)
	if !strings.Contains(got, "итог: сценариев 1, все зачтены, база старый") {
		t.Fatalf("итог чистого прогона:\n%s", got)
	}
}

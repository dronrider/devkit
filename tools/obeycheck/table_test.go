package main

import (
	"os/exec"
	"path/filepath"
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

// runSymbolsHook гоняет боевой hooks/check-symbols.py на тексте и отдаёт код
// возврата: 0 текст чист, 1 хук нашёл запрещённый символ. Код рун в образцах
// ниже собран числом через rune(0x...), а не буквенным символом: живой символ
// в исходнике сам не прошёл бы тот же рубеж.
func runSymbolsHook(t *testing.T, text string) int {
	t.Helper()
	path := filepath.Join(devkitRoot(t), "hooks", "check-symbols.py")
	cmd := exec.Command("python3", path, "--stdin")
	cmd.Stdin = strings.NewReader(text)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	t.Fatalf("хук символов: %v", err)
	return -1
}

// TestToKeyboardMatchesHook гоняет по случаю каждый класс BAD из
// hooks/check-symbols.py через сам хук: сырой символ хук обязан ловить,
// приведённый к клавиатуре текст обязан пропускать. Так таблица toKeyboard
// не расходится с хуком на следующей правке одного без другого (DK-1056).
func TestToKeyboardMatchesHook(t *testing.T) {
	r := func(code int) string { return string(rune(code)) }
	cases := []struct{ name, dirty, want string }{
		{"длинное тире", "слово" + r(0x2014) + "слово", "слово - слово"},
		{"стрелка вправо", "было" + r(0x2192) + "стало", "было->стало"},
		{"многоточие", "ждать" + r(0x2026), "ждать..."},
		{"двойная кавычка-лапка", r(0x201c) + "цитата" + r(0x201d), "«цитата»"},
		{"одинарная кавычка-лапка", r(0x2018) + "цитата" + r(0x2019), "«цитата»"},
		{"неразрывный пробел", "два" + r(0x00a0) + "слова", "два слова"},
		{"эмодзи", "готово" + r(0x2705), "готово"},
		{"символ вне раскладок", "caf" + r(0x00e9), "caf?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runSymbolsHook(t, c.dirty); code == 0 {
				t.Fatalf("образец %q не ловится текущим хуком, случай устарел", c.dirty)
			}
			if got := toKeyboard(c.dirty); got != c.want {
				t.Fatalf("toKeyboard(%q) = %q, ждал %q", c.dirty, got, c.want)
			} else if code := runSymbolsHook(t, got); code != 0 {
				t.Fatalf("после toKeyboard хук всё ещё против %q (код %d)", got, code)
			}
		})
	}
}

// TestToKeyboardApostropheBetweenLetters: U+2019 между двумя буквами это
// апостроф внутри слова, а не закрывающая кавычка (ревью DK-1056, замечание
// 1). Обычная замена на «»» ломает английское слово, «don»t» вместо «don't»;
// прямой апостроф с клавиатуры хук пропускает и слово остаётся собой.
func TestToKeyboardApostropheBetweenLetters(t *testing.T) {
	r := func(code int) string { return string(rune(code)) }
	cases := []struct{ name, dirty, want string }{
		{"апостроф внутри английского слова", "don" + r(0x2019) + "t", "don't"},
		{"апостроф во втором слове фразы", "it" + r(0x2019) + "s ok", "it's ok"},
		{"закрывающая лапка после буквы, а не апостроф", r(0x2018) + "цитата" + r(0x2019), "«цитата»"},
		{"закрывающая лапка перед знаком препинания", "фраза" + r(0x2019) + ", дальше", "фраза», дальше"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runSymbolsHook(t, c.dirty); code == 0 {
				t.Fatalf("образец %q не ловится текущим хуком, случай устарел", c.dirty)
			}
			if got := toKeyboard(c.dirty); got != c.want {
				t.Fatalf("toKeyboard(%q) = %q, ждал %q", c.dirty, got, c.want)
			} else if code := runSymbolsHook(t, got); code != 0 {
				t.Fatalf("после toKeyboard хук всё ещё против %q (код %d)", got, code)
			}
		})
	}
}

// TestToKeyboardRemovesZWJFromEmoji: соединитель ZWJ (U+200D) между эмодзи
// уходит вместе с ними, а не в default-ветку «?» (ревью DK-1056, замечание
// 2). Без этого составная эмодзи (семья, тон кожи) оставляет в тексте мусор
// из вопросительных знаков на месте соединителя.
func TestToKeyboardRemovesZWJFromEmoji(t *testing.T) {
	r := func(code int) string { return string(rune(code)) }
	cases := []struct{ name, dirty, want string }{
		{
			"семейная эмодзи с двумя соединителями",
			"семья " + r(0x1f468) + r(0x200d) + r(0x1f469) + r(0x200d) + r(0x1f466),
			"семья ",
		},
		{
			"эмодзи с модификатором тона кожи",
			"жест " + r(0x1f44d) + r(0x1f3fc),
			"жест ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := runSymbolsHook(t, c.dirty); code == 0 {
				t.Fatalf("образец %q не ловится текущим хуком, случай устарел", c.dirty)
			}
			if got := toKeyboard(c.dirty); got != c.want {
				t.Fatalf("toKeyboard(%q) = %q, ждал %q", c.dirty, got, c.want)
			} else if code := runSymbolsHook(t, got); code != 0 {
				t.Fatalf("после toKeyboard хук всё ещё против %q (код %d)", got, code)
			}
		})
	}
}

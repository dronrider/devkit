package taskform

import (
	"strings"
	"testing"
)

// listDoc это файл задачи с перечнем на все случаи разбора: развилка со всеми
// четырьмя подстроками, оставленная исполнителю, решённая дважды, проза при
// развилке, голова вне раздела и голова внутри ограждённого блока.
const listDoc = `# T-001

## Что происходит

- «мимо»: голова вне раздела развилкой не считается

## Развилки

- «хранение»: раздел файла или своя команда?
  - решает: человек
  - рекомендация: команда, штампы дешевле ставить машиной
  - см. docs/lld/DK-552-forks-interview.md
  - решено человеком 2026-09-05: раздел, читается руками
  - решено человеком 2026-09-06: команда, раздел остаётся читаемым
- «имя поля»: звать поле «решает» или «кто»?
  - решает: исполнитель
  - рекомендация: «решает», читается ответом на вопрос
- «публичность»: релизы видны без токена?
  - решает: человек

## Проверка

` + "```" + `
- «цитата»: вывод чужой команды развилок не заводит
  - решает: человек
` + "```" + `
`

// TestForksReadsList: перечень читается формой, а не глазами. Четыре
// подстрочные формы, три состояния из двух полей, второе «решено» под той же
// головой, проза при развилке, голова вне раздела и голова в ограждённом
// блоке.
func TestForksReadsList(t *testing.T) {
	forks := ParseForks(listDoc)
	if len(forks) != 3 {
		var got []string
		for _, f := range forks {
			got = append(got, f.Name)
		}
		t.Fatalf("в перечне ждали три развилки, вышло %d: %v", len(forks), got)
	}

	t.Run("четыре формы", func(t *testing.T) {
		f := forks[0]
		if f.Name != "хранение" || f.Question != "раздел файла или своя команда?" {
			t.Fatalf("голова разобрана не так: %q, %q", f.Name, f.Question)
		}
		if f.Who != ForkHuman {
			t.Fatalf("поле «решает» разобрано как %q", f.Who)
		}
		if !strings.HasPrefix(f.Hint, "команда, штампы") {
			t.Fatalf("рекомендация разобрана как %q", f.Hint)
		}
		if f.By != ByHuman || f.Date != "2026-09-06" {
			t.Fatalf("автор и дата решения: %q, %q", f.By, f.Date)
		}
		if f.Line != 9 {
			t.Fatalf("номер строки головы %d, а голова стоит девятой", f.Line)
		}
	})

	t.Run("повторное решено", func(t *testing.T) {
		if got := forks[0].Answer; got != "команда, раздел остаётся читаемым" {
			t.Fatalf("побеждает не последнее решение: %q", got)
		}
	})

	t.Run("три состояния", func(t *testing.T) {
		want := []string{StateDecided, StateLeft, StateOpen}
		hold := []bool{false, false, true}
		for i, f := range forks {
			if f.State() != want[i] {
				t.Fatalf("%s: состояние %q, а ждали %q", f.Name, f.State(), want[i])
			}
			if f.HoldsStart() != hold[i] {
				t.Fatalf("%s: держит старт %v, а ждали %v", f.Name, f.HoldsStart(), hold[i])
			}
		}
	})

	t.Run("проза при развилке", func(t *testing.T) {
		// Строка со ссылкой на документ стоит между рекомендацией и решением:
		// разбор её пропускает, а поля под ней читает.
		if !forks[0].Decided() {
			t.Fatal("проза при развилке оборвала разбор подстрок")
		}
	})

	t.Run("голова вне раздела", func(t *testing.T) {
		for _, f := range forks {
			if f.Name == "мимо" {
				t.Fatal("голова из «Что происходит» попала в перечень")
			}
		}
	})

	t.Run("ограждённый блок", func(t *testing.T) {
		for _, f := range forks {
			if f.Name == "цитата" {
				t.Fatal("голова из вложенного вывода попала в перечень")
			}
		}
		// Тот же вывод, вложенный в сам раздел «Развилки», развилок не заводит.
		doc := "# T-002\n\n## Развилки\n\n```\n- «цитата»: вывод команды\n```\n"
		if got := ParseForks(doc); len(got) != 0 {
			t.Fatalf("цитата внутри раздела принята за развилку: %+v", got)
		}
	})
}

// TestForksDefaultWho: развилка без поля «решает» считается человеческой и
// держит старт. Умолчание выбрано по цене ошибки: забытое поле видно отказом
// ворот, а обратное умолчание пропускало бы вопрос человека молча.
func TestForksDefaultWho(t *testing.T) {
	doc := "# T-003\n\n## Развилки\n\n- «доступ»: токен положен?\n"
	forks := ParseForks(doc)
	if len(forks) != 1 || !forks[0].HoldsStart() || forks[0].State() != StateOpen {
		t.Fatalf("развилка без поля «решает» разобрана как %+v", forks)
	}
}

// TestForksFinds: сторож формы называет подстроку не по форме, повтор имени и
// строку «оставлена» при «решает: человек», а чистый перечень оставляет без
// находок.
func TestForksFinds(t *testing.T) {
	if got := ForkFinds(listDoc); len(got) != 0 {
		t.Fatalf("на чистом перечне находки: %+v", got)
	}
	doc := `# T-004

## Развилки

- «доступ»: токен положен?
  - решает: человек
  - решено вчера: положил
- «доступ»: тот же вопрос второй раз
  - решает: человек
- «поле»: как звать?
  - решает: человек
  - оставлена человеком 2026-09-06: решит исполнитель
`
	finds := ForkFinds(doc)
	if len(finds) != 3 {
		t.Fatalf("ждали три находки, вышло %d: %+v", len(finds), finds)
	}
	if !strings.Contains(finds[0].Text, "решено") || finds[0].Line != 7 {
		t.Fatalf("подстрока не по форме названа как %+v", finds[0])
	}
	if !strings.Contains(finds[1].Text, "второй раз") || finds[1].Line != 8 {
		t.Fatalf("повтор имени назван как %+v", finds[1])
	}
	if !strings.Contains(finds[2].Text, "решает: человек") {
		t.Fatalf("противоречие передачи названо как %+v", finds[2])
	}
	// Проза без слова формы находкой не считается.
	fine := "# T-005\n\n## Развилки\n\n- «а»: вопрос?\n  - решает: человек\n  - см. соседнюю задачу\n"
	if got := ForkFinds(fine); len(got) != 0 {
		t.Fatalf("проза при развилке принята за ошибку формы: %+v", got)
	}
}

// TestForksWrite: заведение кладёт голову с полями на место раздела по форме,
// занятое имя отказывает, а подстрока дописывается под свою голову, а не в
// конец перечня.
func TestForksWrite(t *testing.T) {
	doc := "# T-006\n\n## Что происходит\n\nтекст\n\n## DoD\n\nконец\n"
	got, err := AddFork(doc, "доступ", "токен положен?", ForkHuman, "считать приватным")
	if err != nil {
		t.Fatal(err)
	}
	want := "# T-006\n\n## Что происходит\n\nтекст\n\n## Развилки\n\n- «доступ»: токен положен?\n  - решает: человек\n  - рекомендация: считать приватным\n\n## DoD\n\nконец\n"
	if got != want {
		t.Fatalf("раздел встал не по форме:\n%s", got)
	}
	if _, err := AddFork(got, "доступ", "второй раз", ForkHuman, ""); err == nil {
		t.Fatal("занятое имя завелось второй раз")
	} else if !strings.Contains(err.Error(), "доступ") {
		t.Fatalf("отказ не назвал имени: %v", err)
	}
	if _, err := AddFork(got, "поле", "как звать?", "кто угодно", ""); err == nil {
		t.Fatal("чужое значение поля «решает» принято")
	}

	got, err = AddFork(got, "поле", "как звать?", ForkExecutor, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err = AppendToFork(got, "доступ", ForkDecisionLine(ByHuman, "2026-09-06", "положил в secretctl"))
	if err != nil {
		t.Fatal(err)
	}
	forks := ParseForks(got)
	if len(forks) != 2 || !forks[0].Decided() || forks[1].Decided() {
		t.Fatalf("решение легло не под свою голову:\n%s", got)
	}
	if forks[1].Name != "поле" || forks[1].State() != StateLeft {
		t.Fatalf("вторая развилка разобрана как %+v", forks[1])
	}
	if _, err := AppendToFork(got, "нет такой", "  - решено человеком 2026-09-06: ответ"); err == nil {
		t.Fatal("ответ закрыл незаведённую развилку")
	}
	// Передача исполнителю дописывает след, а состояние правит поле «решает».
	got, err = AppendToFork(got, "доступ", ForkLeaveLine(ByAgent, "2026-09-06", ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "оставлена агентом 2026-09-06\n") {
		t.Fatalf("след передачи без причины не записан:\n%s", got)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

// decideDay это дата прогона: штампы команды сверяются по ней, а не по
// сегодняшнему числу, иначе тест жил бы ровно сутки.
var decideDay = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func decideRun(t *testing.T, root string, p DecideParams) string {
	t.Helper()
	p.Now = decideDay
	msg, err := cmdDecide(root, p)
	if err != nil {
		t.Fatalf("decide %+v: %v", p, err)
	}
	return msg
}

func readTask(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestDecideWritesList: четыре входа команды на файле задачи. Заведение
// кладёт голову с полем «решает», закрытие дописывает штамп с автором и
// датой, передача правит поле и оставляет след, печать называет состояния.
func TestDecideWritesList(t *testing.T) {
	root := setup(t)
	msg := decideRun(t, root, DecideParams{ID: "XR-005", Ask: "«хранение»", Who: taskform.ForkHuman, Hint: "команда, штампы дешевле машиной", Text: "раздел файла или своя команда?"})
	if !strings.Contains(msg, "держит старт") {
		t.Fatalf("ответ заведения не сказал про старт: %s", msg)
	}
	doc := readTask(t, root, "XR-005")
	if !strings.Contains(doc, "## Развилки\n\n- «хранение»: раздел файла или своя команда?\n  - решает: человек\n  - рекомендация: команда, штампы дешевле машиной") {
		t.Fatalf("развилка записана не по форме:\n%s", doc)
	}
	if strings.Index(doc, "## Развилки") > strings.Index(doc, "## Сценарий проверки") {
		t.Fatal("раздел встал не на своё место по форме")
	}

	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "поле", Who: taskform.ForkExecutor, Hint: "«решает», читается ответом", Text: "как звать поле?"})
	out := decideRun(t, root, DecideParams{ID: "XR-005", Open: true})
	if !strings.Contains(out, "«хранение» (открыта, держит старт)") || !strings.Contains(out, "«поле» (оставлена исполнителю)") {
		t.Fatalf("печать открытых не назвала состояний:\n%s", out)
	}

	decideRun(t, root, DecideParams{ID: "XR-005", Name: "«хранение»", By: "человек", Text: "команда, раздел остаётся читаемым"})
	doc = readTask(t, root, "XR-005")
	if !strings.Contains(doc, "  - решено человеком 2026-09-06: команда, раздел остаётся читаемым") {
		t.Fatalf("штамп решения не записан:\n%s", doc)
	}
	out = decideRun(t, root, DecideParams{ID: "XR-005", Open: true})
	if strings.Contains(out, "«хранение»") {
		t.Fatalf("решённая развилка печатается открытой:\n%s", out)
	}
	if !strings.Contains(decideRun(t, root, DecideParams{ID: "XR-005"}), "«хранение» (решена)") {
		t.Fatal("полная печать потеряла решённую развилку")
	}

	// Передача правит поле «решает», а не только дописывает след: состояние
	// читается полем, и без правки развилка держала бы старт после передачи.
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "публичность", Text: "релизы видны без токена?"})
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "публичность", Leave: true, By: "агент", Text: "проверить по факту"})
	f, ok := taskform.FindFork(readTask(t, root, "XR-005"), "публичность")
	if !ok || f.HoldsStart() || f.State() != taskform.StateLeft {
		t.Fatalf("после передачи развилка разобрана как %+v", f)
	}
	if !strings.Contains(readTask(t, root, "XR-005"), "оставлена агентом 2026-09-06: проверить по факту") {
		t.Fatal("след передачи не записан")
	}
}

// TestDecideRefusals: отказы команды. Повтор имени, закрытие незаведённого
// имени, архивный ID, чужой автор и заведение без вопроса.
func TestDecideRefusals(t *testing.T) {
	root := setup(t)
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "доступ", Text: "токен положен?"})

	t.Run("повтор имени", func(t *testing.T) {
		_, err := cmdDecide(root, DecideParams{ID: "XR-005", Ask: "доступ", Text: "второй раз", Now: decideDay})
		if err == nil || !strings.Contains(err.Error(), "доступ") {
			t.Fatalf("занятое имя завелось второй раз: %v", err)
		}
	})
	t.Run("незаведённое имя", func(t *testing.T) {
		_, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "секрет", By: "человек", Text: "ответ", Now: decideDay})
		if err == nil || !strings.Contains(err.Error(), "секрет") {
			t.Fatalf("ответ закрыл незаведённую развилку: %v", err)
		}
	})
	t.Run("архивный ID", func(t *testing.T) {
		dir := filepath.Join(root, "docs", "tasks", "archive", "2026")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "XR-007.md"), []byte("# XR-007\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := cmdDecide(root, DecideParams{ID: "XR-007", Ask: "поздно", Text: "вопрос?", Now: decideDay})
		if err == nil || !strings.Contains(err.Error(), "XR-007") {
			t.Fatalf("развилка завелась в архивной записи: %v", err)
		}
	})
	t.Run("чужой автор", func(t *testing.T) {
		if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "доступ", By: "ревьювер", Text: "ответ", Now: decideDay}); err == nil {
			t.Fatal("автор не из трёх принят")
		}
		if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "доступ", Leave: true, By: "исполнитель", Now: decideDay}); err == nil {
			t.Fatal("развилку оставил себе сам исполнитель")
		}
	})
	t.Run("нет вопроса", func(t *testing.T) {
		if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Ask: "пусто", Now: decideDay}); err == nil {
			t.Fatal("развилка завелась без вопроса")
		}
	})
	t.Run("записи нет", func(t *testing.T) {
		if _, err := cmdDecide(root, DecideParams{ID: "XR-404", Ask: "нет", Text: "вопрос?", Now: decideDay}); err == nil {
			t.Fatal("развилка завелась у записи, которой нет")
		}
	})
	t.Run("решённую не оставляют", func(t *testing.T) {
		decideRun(t, root, DecideParams{ID: "XR-005", Name: "доступ", By: "человек", Text: "положил в secretctl"})
		if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "доступ", Leave: true, Now: decideDay}); err == nil {
			t.Fatal("решённая развилка ушла исполнителю")
		}
	})
}

// TestDecideFindsRecord: запись команда ищет сама, как её ищет ask, и правит
// перечень черновика накопителя так же, как перечень файла задачи.
func TestDecideFindsRecord(t *testing.T) {
	root := setup(t)
	drafts := filepath.Join(root, "docs", "tasks", "drafts")
	if err := os.MkdirAll(drafts, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(drafts, "XR-050.md")
	if err := os.WriteFile(path, []byte("# XR-050: черновик\n\n## Черновик\n\n### Ситуация\n\nтекст\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := decideRun(t, root, DecideParams{ID: "XR-050", Ask: "хранение", Text: "где держать?"})
	if !strings.Contains(msg, "черновик") {
		t.Fatalf("ответ не назвал вид записи: %s", msg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if forks := taskform.ParseForks(string(data)); len(forks) != 1 || forks[0].Name != "хранение" {
		t.Fatalf("перечень черновика разобран как %+v", forks)
	}
}

// TestDecideLintFinds: сторож формы называет находки перечня, правленного
// руками, с путём файла и номером строки.
func TestDecideLintFinds(t *testing.T) {
	root := setup(t)
	doc := readTask(t, root, "XR-002") + "\n## Развилки\n\n- «доступ»: токен положен?\n  - решает: человек\n  - решено вчера: положил\n"
	if err := os.WriteFile(taskFilePath(root, "XR-002"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	hit := ""
	for _, f := range finds {
		if strings.Contains(f, "доступ") {
			hit = f
		}
	}
	if hit == "" {
		t.Fatalf("сторож не назвал подстроку не по форме: %v", finds)
	}
	if !strings.Contains(hit, "docs/tasks/XR-002.md:") || !strings.Contains(hit, "решено") {
		t.Fatalf("находка без адреса и формы: %s", hit)
	}
}

// TestAddLinkPrintsGoalForks: заведение наследной строки печатает открытые
// развилки файла цели, чтобы тот, кто режет цель, видел, что переносить.
// Решённая развилка не печатается: ответ лежит в родителе.
func TestAddLinkPrintsGoalForks(t *testing.T) {
	root := setup(t)
	goal := "# XR-002: цель\n\n## Развилки\n\n" +
		"- «публичность»: релизы видны без токена?\n  - решает: человек\n  - рекомендация: считать приватными\n" +
		"- «имя поля»: как звать поле?\n  - решает: исполнитель\n" +
		"- «хранение»: где держать перечень?\n  - решает: человек\n  - решено человеком 2026-09-05: разделом файла\n"
	if err := os.WriteFile(taskFilePath(root, "XR-002"), []byte(goal), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdAdd(root, AddParams{ID: "XR-051", Title: "Наследная", Type: "task", Rank: "25+2+1+0+2", Link: "tasks/XR-002.md", Accept: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "«публичность»") || !strings.Contains(msg, "«имя поля»") {
		t.Fatalf("открытые развилки цели не напечатаны:\n%s", msg)
	}
	if strings.Contains(msg, "«хранение»") {
		t.Fatalf("решённая развилка цели зовёт к переносу:\n%s", msg)
	}
	if !strings.Contains(msg, "decide --ask") {
		t.Fatalf("печать не сказала, чем переносить:\n%s", msg)
	}
	// Строка без цели печать не заводит, и своих развилок у неё пока нет.
	msg, err = cmdAdd(root, AddParams{ID: "XR-052", Title: "Сама по себе", Type: "task", Rank: "25+2+1+0+2", Accept: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "развилки цели") {
		t.Fatalf("строка без цели получила чужой перечень:\n%s", msg)
	}
}

// TestDecideAddsOptionToOpenFork: вариант дописывается в заведённую развилку,
// и блок вопроса печатает его своим номером. Вопрос, заведённый без вариантов,
// печатался человеку одной рекомендацией, выбирать было не из чего, а
// повторное --ask на занятое имя отказывает.
func TestDecideAddsOptionToOpenFork(t *testing.T) {
	root := setup(t)
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "область", Hint: "послабление только боковой директории контура", Text: "любой вложенный docs/TASKS.md или доска контура?"})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	if strings.Contains(got, "\n2. ") {
		t.Fatalf("у развилки без вариантов взялся второй пункт:\n%s", got)
	}

	msg := decideRun(t, root, DecideParams{ID: "XR-005", Name: "область", Opts: []string{"правило по суффиксу пути, одинаковое всем репозиториям"}})
	if !strings.Contains(msg, "дописано вариантов 1") {
		t.Fatalf("ответ не назвал, сколько вариантов дописано: %s", msg)
	}
	doc := readTask(t, root, "XR-005")
	if !strings.Contains(doc, "  - рекомендация: послабление только боковой директории контура\n  - вариант: правило по суффиксу пути, одинаковое всем репозиториям") {
		t.Fatalf("вариант встал не после рекомендации:\n%s", doc)
	}
	got = chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	if !strings.Contains(got, "\n2. правило по суффиксу пути, одинаковое всем репозиториям") {
		t.Fatalf("дописанный вариант не попал в блок вопроса:\n%s", got)
	}

	// Дубль не отбивает пачку: команду зовут списком ключей, и повтор одного
	// варианта не повод потерять остальные. В файле дубля при этом нет.
	msg = decideRun(t, root, DecideParams{ID: "XR-005", Name: "область", Opts: []string{"правило по суффиксу пути, одинаковое всем репозиториям", "признак корп-контура у проекта-подкаталога"}})
	if !strings.Contains(msg, "дописано вариантов 1") || !strings.Contains(msg, "уже были в перечне 1") {
		t.Fatalf("ответ не отделил дубль от дописанного: %s", msg)
	}
	f, ok := taskform.FindFork(readTask(t, root, "XR-005"), "область")
	if !ok || len(f.Options) != 2 {
		t.Fatalf("дубль лёг в перечень вторым разом: %+v", f)
	}
}

// TestDecideOptionRefusals: отказы дописывания. Решённой развилке вариант уже
// не нужен, незаведённое имя отказывает, как у остальных входов, а пустой ключ
// зовёт форму команды.
func TestDecideOptionRefusals(t *testing.T) {
	root := setup(t)
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "хранение", Hint: "команда", Text: "раздел или команда?"})
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "хранение", By: "человек", Text: "команда, штампы дешевле машиной"})
	if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "хранение", Opts: []string{"раздел файла задачи"}, Now: decideDay}); err == nil {
		t.Fatal("решённая развилка приняла вариант")
	}
	if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "секрет", Opts: []string{"из secretctl"}, Now: decideDay}); err == nil {
		t.Fatal("вариант лёг в незаведённую развилку")
	}
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "доступ", Text: "токен положен?"})
	if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Name: "доступ", Opts: []string{"   "}, Now: decideDay}); err == nil {
		t.Fatal("пустой вариант принят")
	}
}

// TestDecideOptionAllDuplicates: пачка из одних дублей файла не трогает и на
// коммит не идёт. Записать файл тем же текстом мало: следом зовётся коммит, и
// git падает на пустом диффе вместо внятного ответа (замечание ревью DK-882).
func TestDecideOptionAllDuplicates(t *testing.T) {
	root := setup(t)
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "область", Hint: "боковая директория контура", Text: "любая доска или контур?"})
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "область", Opts: []string{"правило по суффиксу пути"}})
	before := readTask(t, root, "XR-005")

	msg := decideRun(t, root, DecideParams{ID: "XR-005", Name: "область", Opts: []string{"правило по суффиксу пути"}, Commit: CommitOpts{Msg: "второй раз тот же вариант"}})
	if !strings.Contains(msg, "дописывать нечего") || !strings.Contains(msg, "уже были в перечне 1") {
		t.Fatalf("ответ на пачку дублей не назвал, что делать нечего: %s", msg)
	}
	if got := readTask(t, root, "XR-005"); got != before {
		t.Fatalf("файл переписан пачкой дублей:\n%s", got)
	}
}

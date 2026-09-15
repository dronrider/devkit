package main

import (
	"strings"
	"testing"
)

// Первый раунд интервью спрашивает про кейсы записи тремя составами, узким,
// сбалансированным и широким (DK-969). Раньше все раскопанные кейсы уезжали
// человеку одной рекомендацией сплошным списком: выбирать было не из чего, а
// снять кейс по номеру нечем. Тесты ниже держат обе половины правки, печать
// блока с раскладкой и разбор ответа «состав 2, минус 4, плюс свой».

const (
	lineupMid  = "сбалансированный состав, 3 кейса: (1) человек видит три состава; (2) кейсы правятся по номерам; (3) широкий несёт находки обхода"
	lineupNar  = "узкий состав, 2 кейса: (1) человек видит три состава; (2) кейсы правятся по номерам"
	lineupWide = "широкий состав, 4 кейса: (1) человек видит три состава; (2) кейсы правятся по номерам; (3) широкий несёт находки обхода; (4) панель вешает галочки на составы"
)

// caseFork заводит развилку кейсов тремя составами: рекомендованный идёт
// рекомендацией, два других вариантами.
func caseFork(t *testing.T, root, id string) {
	t.Helper()
	askForks(t, root, id, [][]string{
		{"кейсы", lineupMid, "какие пользовательские кейсы у записи? рекомендую сбалансированный: узкий теряет кейсы (3) и (4)", lineupNar, lineupWide},
	})
}

// TestChatBlockShortensLineups: в строке варианта стоит только голова состава,
// ширина и счёт. Весь список кейсов в строке с номером не читается, и править
// его по номерам человеку не с чего.
func TestChatBlockShortensLineups(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	want := []string{
		"1. рекомендую: сбалансированный состав, 3 кейса",
		"2. узкий состав, 2 кейса",
		"3. широкий состав, 4 кейса",
	}
	for _, w := range want {
		if !strings.Contains(got, "\n"+w+"\n") {
			t.Fatalf("в блоке нет строки состава %q:\n%s", w, got)
		}
	}
	head, _, ok := strings.Cut(got, chatAnswerHint)
	if !ok {
		t.Fatalf("в блоке нет последней строки:\n%s", got)
	}
	if strings.Contains(head, "(1) человек видит три состава") {
		t.Fatalf("список кейсов уехал в строку варианта:\n%s", head)
	}
}

// TestChatBlockRollsCases: раскладка кейсов стоит под блоком, по кейсу на
// строку и по составу блоком. Ниже последней строки блока она затем, чтобы
// галочки панели вставали на составы, а не на кейсы.
func TestChatBlockRollsCases(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	_, roll, ok := strings.Cut(got, chatAnswerHint)
	if !ok {
		t.Fatalf("в блоке нет последней строки:\n%s", got)
	}
	want := []string{
		"раскладка кейсов развилки «кейсы», по кейсу на строку:",
		"состав 1, сбалансированный:",
		"1. человек видит три состава",
		"3. широкий несёт находки обхода",
		"состав 2, узкий:",
		"состав 3, широкий:",
		"4. панель вешает галочки на составы",
		chatLineupHint,
	}
	for _, w := range want {
		if !strings.Contains(roll, "\n"+w) {
			t.Fatalf("в раскладке нет строки %q:\n%s", w, roll)
		}
	}
}

// TestChatBlockKeepsPlainForks: развилка с обычными вариантами печатается
// по-прежнему, целиком и без раскладки. Грамматика составов к ней не лезет.
func TestChatBlockKeepsPlainForks(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катит смежник, чужая команда дорога", "релиз катится сам или его катит смежник?", "катится сам"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	if !strings.Contains(got, "\n1. рекомендую: катит смежник, чужая команда дорога\n") {
		t.Fatalf("обычный вариант укоротился:\n%s", got)
	}
	if strings.Contains(got, "раскладка кейсов") {
		t.Fatalf("раскладка встала при развилке без составов:\n%s", got)
	}
}

// TestAnswerPicksLineupAndEdits: ответ «состав 2, минус 1, плюс свой» берёт
// названный состав, снимает кейс по номеру раскладки и дописывает свой.
// Развилка после этого закрыта, а номера в файле идут сплошняком.
func TestAnswerPicksLineupAndEdits(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "состав 2, минус 1, плюс панель вешает галочки на составы"}, nil, nil)
	if !strings.Contains(got, "закрыто развилок 1") {
		t.Fatalf("ответ составом развилку не закрыл:\n%s", got)
	}
	want := "узкий состав, 2 кейса: (1) кейсы правятся по номерам; (2) панель вешает галочки на составы"
	doc := readTask(t, root, "XR-005")
	if !strings.Contains(doc, "решено человеком 2026-09-06: "+want) {
		t.Fatalf("в файле не тот состав, жду %q:\n%s", want, doc)
	}
	if strings.Contains(got, "не разобрано") {
		t.Fatalf("куски правки состава ушли агенту:\n%s", got)
	}
}

// TestAnswerEditsRecommendedLineup: «минус 3» без номера состава правит
// рекомендованный. Человек согласился с составом и снял из него один кейс,
// называть номер тут нечего.
func TestAnswerEditsRecommendedLineup(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	chatRun(t, root, DecideParams{ID: "XR-005", Answer: "минус 3"}, nil, nil)
	doc := readTask(t, root, "XR-005")
	want := "сбалансированный состав, 2 кейса: (1) человек видит три состава; (2) кейсы правятся по номерам"
	if !strings.Contains(doc, want) {
		t.Fatalf("правка рекомендованного состава не легла, жду %q:\n%s", want, doc)
	}
}

// TestAnswerLineupByForkName: имя развилки с номером работает по-прежнему, а
// правка по номерам кейсов ложится на тот же ответ, а не заводит второй.
func TestAnswerLineupByForkName(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "кейсы 3, минус 2"}, nil, nil)
	if !strings.Contains(got, "закрыто развилок 1") {
		t.Fatalf("развилка закрыта не одним решением:\n%s", got)
	}
	doc := readTask(t, root, "XR-005")
	want := "широкий состав, 3 кейса: (1) человек видит три состава; (2) широкий несёт находки обхода; (3) панель вешает галочки на составы"
	if !strings.Contains(doc, want) {
		t.Fatalf("правка не легла на названный состав, жду %q:\n%s", want, doc)
	}
	if strings.Count(doc, "решено человеком") != 1 {
		t.Fatalf("решений у развилки больше одного:\n%s", doc)
	}
}

// TestAnswerLineupRefusesUnknownNumbers: номер состава и номер кейса за
// пределами списка валят команду целиком. Человек назвал то, чего в блоке не
// было, и закрыть развилку соседним значит соврать про его ответ.
func TestAnswerLineupRefusesUnknownNumbers(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	for _, c := range []struct{ answer, want string }{
		{"состав 7", "составов 3"},
		{"состав 2, минус 5", "минус 5"},
	} {
		_, err := cmdDecide(root, DecideParams{ID: "XR-005", Answer: c.answer, Now: decideDay})
		if err == nil {
			t.Fatalf("ответ %q прошёл молча", c.answer)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("отказ на %q не называет промах: %v", c.answer, err)
		}
	}
	if strings.Contains(readTask(t, root, "XR-005"), "решено человеком") {
		t.Fatal("отказ по номеру всё же правил файл")
	}
}

// TestAnswerLineupWordsStayUnparsed: слова состава при развилке без составов
// остаются неразобранным куском. Своей грамматики у них тут нет, и закрывать
// ими чужую развилку команда не берётся.
func TestAnswerLineupWordsStayUnparsed(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катит смежник", "релиз катится сам или его катит смежник?", "катится сам"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "состав 2, минус 4"}, nil, nil)
	if !strings.Contains(got, "не разобрано словами: состав 2; минус 4") {
		t.Fatalf("слова состава разобрались при развилке без составов:\n%s", got)
	}
}

// TestAnswerLineupWithRecommendation: «по рекомендации» рядом с правкой
// состава закрывает соседние развилки рекомендациями, а состав приходит с
// правкой человека, не дублируясь вторым решением.
func TestAnswerLineupWithRecommendation(t *testing.T) {
	root := setup(t)
	caseFork(t, root, "XR-005")
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катит смежник", "релиз катится сам или его катит смежник?", "катится сам"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "минус 2, по рекомендации"}, nil, nil)
	if !strings.Contains(got, "закрыто развилок 2") {
		t.Fatalf("пачка закрылась не целиком:\n%s", got)
	}
	doc := readTask(t, root, "XR-005")
	if !strings.Contains(doc, "сбалансированный состав, 2 кейса: (1) человек видит три состава; (2) широкий несёт находки обхода") {
		t.Fatalf("состав пришёл без правки человека:\n%s", doc)
	}
	if strings.Contains(doc, "решено человеком 2026-09-06: "+lineupMid) {
		t.Fatalf("состав закрылся ещё и рекомендацией без правки:\n%s", doc)
	}
}

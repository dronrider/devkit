package main

import (
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskform"
)

// chatEnv это окружение хода для runDecide: сессия панели узнаётся по
// DEVKIT_TMUX, и тест ставит переменную сам, а не полагается на машину.
func chatEnv(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// askForks заводит в записи развилки с рекомендацией и вариантами.
func askForks(t *testing.T, root, id string, specs [][]string) {
	t.Helper()
	for _, s := range specs {
		p := DecideParams{ID: id, Ask: s[0], Who: taskform.ForkHuman, Hint: s[1], Text: s[2], Now: decideDay}
		p.Opts = append(p.Opts, s[3:]...)
		if _, err := cmdDecide(root, p); err != nil {
			t.Fatalf("развилка «%s» не завелась: %v", s[0], err)
		}
	}
}

func chatRun(t *testing.T, root string, p DecideParams, d *askDeps, env map[string]string) string {
	t.Helper()
	p.Now = decideDay
	msg, err := runDecide(root, p, d, chatEnv(env))
	if err != nil {
		t.Fatalf("decide %+v: %v", p, err)
	}
	return msg
}

// TestChatBlockNumbersOptions: блок вопроса печатается из перечня развилок.
// Имя развилки стоит в голове, варианты пронумерованы с единицы,
// рекомендованный идёт первым и назван словом, а последняя строка называет три
// вида ответа. Номер стоит в начале строки: по нему панель находит вариант в
// реплике и вешает галочку слева.
func TestChatBlockNumbersOptions(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"печать", "утилита печатает блок, штампы дешевле машиной", "кто собирает текст вопроса?", "агент собирает по шаблону скилла"},
		{"ответ", "имя развилки и номер варианта", "какой формы ответ человека?", "только словами", "кнопкой панели"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	want := []string{
		"«печать»: кто собирает текст вопроса?",
		"1. рекомендую: утилита печатает блок, штампы дешевле машиной",
		"2. агент собирает по шаблону скилла",
		"«ответ»: какой формы ответ человека?",
		"1. рекомендую: имя развилки и номер варианта",
		"2. только словами",
		"3. кнопкой панели",
		chatAnswerHint,
	}
	for _, w := range want {
		if !strings.Contains(got, "\n"+w) {
			t.Fatalf("в блоке нет строки %q:\n%s", w, got)
		}
	}
	if strings.Contains(got, "решает: человек") {
		t.Fatalf("в блок уехала машинная подстрока записи:\n%s", got)
	}
}

// TestChatBlockHoldsPackLimit: в пачку идут четыре развилки, пятая ждёт
// следующей. Потолок тот же, что у признака ожидания (chat.PackLimit): человек
// закрывает четыре вопроса одним заходом, пятый это уже простыня.
func TestChatBlockHoldsPackLimit(t *testing.T) {
	root := setup(t)
	var specs [][]string
	for _, name := range []string{"первая", "вторая", "третья", "четвёртая", "пятая"} {
		specs = append(specs, []string{name, "рекомендация " + name, "вопрос " + name})
	}
	askForks(t, root, "XR-005", specs)
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, nil)
	if !strings.Contains(got, "в пачке развилок 4") || !strings.Contains(got, "ещё 1 уйдут следующей пачкой") {
		t.Fatalf("шапка не назвала размер пачки и остаток:\n%s", got)
	}
	if strings.Contains(got, "«пятая»") {
		t.Fatalf("пятая развилка уехала в ту же пачку:\n%s", got)
	}
	if n := strings.Count(got, "1. рекомендую: "); n != chat.PackLimit {
		t.Fatalf("развилок в блоке %d, потолок %d:\n%s", n, chat.PackLimit, got)
	}
}

// TestChatBlockWithoutForks: спрашивать нечего, значит команда говорит это
// прямо и признака не заводит. Молчание тут неотличимо от поломки, а парковка
// строки под несуществующий вопрос увела бы задачу в blocked.
func TestChatBlockWithoutForks(t *testing.T) {
	root := setup(t)
	st := newAskStand(t)
	st.deps.Main = root
	got := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &st.deps,
		map[string]string{askTmuxEnv: "chat-XR-005-1"})
	if !strings.Contains(got, "открытых развилок нет") {
		t.Fatalf("пустой перечень не назван словом:\n%s", got)
	}
	if len(st.parked) != 0 {
		t.Fatalf("строка припаркована без вопроса: %v", st.parked)
	}
}

// TestChatPanelWritesAsk: в сессии панели та же команда кладёт признак
// ожидания, зовёт уведомитель и паркует строку (DK-864, решение «печать»).
// Раньше это делал хук ask-panel.py на вызове виджета вопроса. Блок при этом
// один и тот же: обычный терминал получает те же строки без признака.
func TestChatPanelWritesAsk(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"печать", "утилита печатает блок", "кто собирает текст?", "агент по шаблону"},
	})
	st := newAskStand(t)
	st.deps.Main = root
	panel := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &st.deps,
		map[string]string{askTmuxEnv: "chat-XR-005-1", askSessionEnv: "aaa-1"})
	if len(st.parked) != 1 || !strings.Contains(st.parked[0], "вопрос: ") {
		t.Fatalf("строка не припаркована вопросом: %v", st.parked)
	}
	if len(st.notes) != 1 || !strings.Contains(st.notes[0], reasonAsk) {
		t.Fatalf("уведомитель не позван: %v", st.notes)
	}
	ask, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-005")))
	if !ok {
		t.Fatal("признака ожидания нет")
	}
	if ask.Session != "aaa-1" || len(ask.Questions) != 1 {
		t.Fatalf("признак лёг без адреса или без вопроса: %+v", ask)
	}
	if len(ask.Questions[0].Options) != 2 || !ask.Questions[0].Options[0].Recommended {
		t.Fatalf("варианты не доехали до признака: %+v", ask.Questions[0])
	}

	plain := newAskStand(t)
	plain.deps.Main = root
	term := chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &plain.deps, nil)
	if len(plain.parked) != 0 || len(plain.notes) != 0 {
		t.Fatalf("обычный терминал завёл признак: %v %v", plain.parked, plain.notes)
	}
	if blockOf(panel) != blockOf(term) {
		t.Fatalf("блок разошёлся между панелью и терминалом:\n%s\n---\n%s", panel, term)
	}
}

// blockOf отрезает служебную шапку: блок начинается после первой пустой
// строки, и в реплику человеку идёт только он.
func blockOf(out string) string {
	if i := strings.Index(out, "\n\n"); i >= 0 {
		return out[i+2:]
	}
	return out
}

// TestAnswerClosesByNameAndNumber: ответ «имя номер» закрывает названные
// развилки, автор решения человек, а текст решения это текст варианта.
func TestAnswerClosesByNameAndNumber(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катим поездом", "как катим?", "катим по одной"},
		{"правило", "правило в ядре", "где живёт правило?", "правило в скилле"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат 2, правило 1"}, nil, nil)
	if !strings.Contains(got, "закрыто развилок 2") {
		t.Fatalf("ответ не закрыл обе развилки:\n%s", got)
	}
	doc := readTask(t, root, "XR-005")
	if !strings.Contains(doc, "решено человеком 2026-09-06: катим по одной") {
		t.Fatalf("номер варианта не стал текстом решения:\n%s", doc)
	}
	if !strings.Contains(doc, "решено человеком 2026-09-06: правило в ядре") {
		t.Fatalf("первый номер не взял рекомендацию:\n%s", doc)
	}
}

// TestAnswerByRecommendation: «по рекомендации» закрывает всю пачку
// рекомендованными вариантами, и второго ключа на это не нужно.
func TestAnswerByRecommendation(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катим поездом", "как катим?", "катим по одной"},
		{"правило", "правило в ядре", "где живёт правило?"},
	})
	chatRun(t, root, DecideParams{ID: "XR-005", Answer: chatRecommended}, nil, nil)
	doc := readTask(t, root, "XR-005")
	for _, want := range []string{"решено человеком 2026-09-06: катим поездом", "решено человеком 2026-09-06: правило в ядре"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("нет строки %q:\n%s", want, doc)
		}
	}
}

// TestAnswerLeavesWordsToAgent: слова своей речью команда не разбирает и врать
// об этом не должна. Неразобранный кусок она называет, развилку оставляет
// открытой и печатает команду, которой её закрывает агент.
func TestAnswerLeavesWordsToAgent(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катим поездом", "как катим?", "катим по одной"},
		{"правило", "правило в ядре", "где живёт правило?", "правило в скилле"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат 1, а правило давай обсудим отдельно"}, nil, nil)
	if !strings.Contains(got, "не разобрано словами: а правило давай обсудим отдельно") {
		t.Fatalf("неразобранный кусок не назван:\n%s", got)
	}
	if !strings.Contains(got, "--by человек") {
		t.Fatalf("команда закрытия агентом не показана:\n%s", got)
	}
	doc := readTask(t, root, "XR-005")
	if strings.Contains(doc, "правило в ядре\n  - решено") {
		t.Fatalf("развилка закрыта по словам, которых команда не понимает:\n%s", doc)
	}
	f, _ := taskform.FindFork(doc, "правило")
	if f.Decided() {
		t.Fatalf("развилка «правило» закрыта, а разобрана не была: %+v", f)
	}
}

// TestAnswerRefusesUnknownNumber: номер за пределами списка это отказ.
// Человек назвал вариант, которого в блоке не было, и закрыть развилку
// соседним значило бы соврать про его ответ.
func TestAnswerRefusesUnknownNumber(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{{"выкат", "катим поездом", "как катим?", "катим по одной"}})
	_, err := runDecide(root, DecideParams{ID: "XR-005", Answer: "выкат 7", Now: decideDay}, nil, chatEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "номер 7") {
		t.Fatalf("номер за пределами списка прошёл: %v", err)
	}
	doc := readTask(t, root, "XR-005")
	if strings.Contains(doc, "решено") {
		t.Fatalf("файл правился при отказе:\n%s", doc)
	}
}

// TestAnswerHoldsRecommendationNextToWords: «по рекомендации» рядом со словами
// остаток пачки не закрывает. Что человек исключил словами, команде не видно,
// и закрыть остальное значит подписать ему ответ, которого он не давал.
func TestAnswerHoldsRecommendationNextToWords(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катим поездом", "как катим?"},
		{"правило", "правило в ядре", "где живёт правило?"},
	})
	got := chatRun(t, root, DecideParams{ID: "XR-005", Answer: "по рекомендации, кроме правила"}, nil, nil)
	if !strings.Contains(got, "остаток пачки не закрыт") {
		t.Fatalf("остаток закрыт молча:\n%s", got)
	}
	doc := readTask(t, root, "XR-005")
	if strings.Contains(doc, "решено человеком") {
		t.Fatalf("развилки закрыты рекомендацией рядом со словами:\n%s", doc)
	}
}

// TestAnswerNamesLongestFork: имена «выкат» и «выкат правил» различаются только
// хвостом, и короткое имя не должно съедать оба куска ответа.
func TestAnswerNamesLongestFork(t *testing.T) {
	root := setup(t)
	askForks(t, root, "XR-005", [][]string{
		{"выкат", "катим поездом", "как катим?", "катим по одной"},
		{"выкат правил", "правила едут отдельно", "как катим правила?", "правила едут вместе"},
	})
	chatRun(t, root, DecideParams{ID: "XR-005", Answer: "выкат правил 2, выкат 2"}, nil, nil)
	doc := readTask(t, root, "XR-005")
	if !strings.Contains(doc, "решено человеком 2026-09-06: правила едут вместе") {
		t.Fatalf("длинное имя не выиграло у короткого:\n%s", doc)
	}
	if !strings.Contains(doc, "решено человеком 2026-09-06: катим по одной") {
		t.Fatalf("короткое имя осталось без решения:\n%s", doc)
	}
}

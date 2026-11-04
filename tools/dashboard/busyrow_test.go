package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Блеск слова действия возвращён к макету DK-919 (DK-1586). Прежняя правка
// держала градиент на месте и дышала прозрачностью: слово мигало целиком, а
// макет гоняет узкую полосу по буквам. Тест краснеет на старой строке, где
// наклон 100deg, ширина фона 100% и кадры меняют opacity, а не положение фона,
// на обратном направлении полосы и на ходе за пределы 0..100%, где фон уходит
// из слова.
func TestStaticBusyWordShineFollowsMockup(t *testing.T) {
	css := readStatic(t, "style.css")
	body := cssRuleText(css, ".bword")
	if body == "" {
		t.Fatal("правила .bword в style.css нет: блеск слова смотреть негде")
	}
	for _, want := range []string{
		"linear-gradient(105deg",
		"var(--tx2) 30%",
		"var(--act) 46%",
		"#fff 52%",
		"var(--act) 58%",
		"var(--tx2) 70%",
		"background-size:300% 100%",
		"animation:bshine 3.2s linear infinite",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("блеск слова не по макету: в .bword нет %q", want)
		}
	}
	shine, ok := keyframeBodies(css)["bshine"]
	if !ok {
		t.Fatal("дорожки bshine в style.css нет: слово стоит на месте")
	}
	from, to, ok := shineSpan(shine)
	if !ok {
		t.Fatalf("положение фона в дорожке блеска не разобрать: кадры %q", strings.TrimSpace(shine))
	}
	if from != 100 || to != 0 {
		t.Errorf("полоса ходит от %g%% до %g%%, а макет ведёт её слева направо: начало хода это 100%%, конец 0%%",
			from, to)
	}
	// Направление отдельной сверкой: при фоне шире слова рост процента уводит
	// картинку влево, поэтому обратный ход гнал бы полосу справа налево.
	if from <= to {
		t.Errorf("ход идёт от %g%% к %g%%: полоса бежит справа налево против макета", from, to)
	}
	// Покрытие: фон шириной 300% должен накрывать слово на всём ходе. При
	// положении за пределами 0..100% картинка шире элемента уезжает из него
	// целиком, а текст с color:transparent остаётся без краски.
	size := cssDecl(body, "background-size")
	if !strings.HasPrefix(size, "300%") {
		t.Fatalf("ширина фона %q: покрытие слова считается не по ней", size)
	}
	if !coversWord(300, from) || !coversWord(300, to) {
		t.Errorf("фон 300%% при ходе %g%%..%g%% не накрывает слово: часть цикла слово невидимо", from, to)
	}
	if props := frameProps(shine); len(props) != 1 || props[0] != "background-position" {
		t.Errorf("дорожка блеска меняет %v, а макет двигает только положение фона", props)
	}
	// Модификаторы гасят блеск там, где хода нет: без хода и на остановке слово
	// читается спокойным цветом, а не переливается.
	for _, sel := range []string{".busyrow.note .bword", ".busyrow.stop .bword"} {
		rule := cssRuleText(css, sel)
		if rule == "" {
			t.Errorf("правила %s нет: блеск не гаснет в этом состоянии строки", sel)
			continue
		}
		if !strings.Contains(rule, "animation:none") {
			t.Errorf("%s не гасит блеск: слово переливается без хода", sel)
		}
	}
	if !strings.Contains(css, ".busyrow .bword{animation:none;background-image:none;color:var(--tx2)}") {
		t.Error("prefers-reduced-motion не гасит блеск слова: без движения слово остаётся градиентом")
	}
}

// Строка активности стоит тем же левым краем, что записи ленты и поле ввода
// (DK-1586). Прежде у .busyrow стояло margin:6px 0 0, и рамка выступала влево
// на 17 точек против .cbox и .frow. Тест краснеет на старом правиле: тогда у
// строки в чате нет левого отступа, а опоры разговора стоят на 17 точках.
func TestStaticBusyRowAlignedWithChat(t *testing.T) {
	css := readStatic(t, "style.css")
	cbox := cssRuleText(css, ".cbox")
	frow := cssRuleText(css, ".frow")
	if cbox == "" || frow == "" {
		t.Fatal("опор выравнивания чата в style.css нет: с чем сравнивать строку, непонятно")
	}
	cboxLeft := cssDecl(cbox, "margin-left")
	frowLeft := cssDecl(frow, "padding-left")
	if cboxLeft != "17px" || frowLeft != "17px" {
		t.Fatalf("край разговора сдвинулся: .cbox margin-left=%q, .frow padding-left=%q, а строка равняется на них",
			cboxLeft, frowLeft)
	}
	row := cssRuleText(css, ".chatwrap .busyrow")
	if row == "" {
		t.Fatal("у строки активности нет левого отступа в чате: рамка торчит наружу против ленты и поля ввода")
	}
	if got := cssDecl(row, "margin-left"); got != cboxLeft {
		t.Errorf("левый край строки активности %q против %q у поля ввода", got, cboxLeft)
	}
	// Правый край совпадает сам: строка блочная и ширину берёт по контейнеру,
	// как поле ввода, а правого отступа у обоих нет. Положительный отступ
	// справа сдвинул бы её внутрь и порвал совпадение.
	if got := cssDecl(row, "margin-right"); got != "" {
		t.Errorf("у строки появился правый отступ %q: её край разошёлся с полем ввода", got)
	}
	if base := cssRuleText(css, ".busyrow"); base != "" && strings.Contains(base, "margin-right") {
		t.Error("правый отступ у .busyrow задан в общем правиле: край строки разойдётся с полем ввода")
	}
}

// cssRuleText отдаёт тело правила, объявленного селектором sel с начала
// строки. Разбор скобками, а не поиском строки целиком: тело .bword занимает
// несколько строк, а имя селектора встречается и в чужих правилах.
func cssRuleText(css, sel string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + `\{`)
	at := re.FindStringIndex(css)
	if at == nil {
		return ""
	}
	i := at[1]
	depth := 1
	start := i
	for i < len(css) && depth > 0 {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	if depth != 0 {
		return ""
	}
	return css[start : i-1]
}

// cssDecl отдаёт значение объявления prop из тела правила без имени свойства:
// cssValue отдаёт строку целиком, а сравнивать надо число.
func cssDecl(rule, prop string) string {
	v := cssValue(rule, prop)
	if v == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(v, prop+":"))
}

// shineSpan достаёт из дорожки блеска начало и конец хода в процентах.
func shineSpan(shine string) (from, to float64, ok bool) {
	m := regexp.MustCompile(`background-position:\s*(-?[0-9.]+)%`).FindAllStringSubmatch(shine, -1)
	if len(m) != 2 {
		return 0, 0, false
	}
	f, err := strconv.ParseFloat(m[0][1], 64)
	if err != nil {
		return 0, 0, false
	}
	t, err := strconv.ParseFloat(m[1][1], 64)
	if err != nil {
		return 0, 0, false
	}
	return f, t, true
}

// coversWord отвечает, накрывает ли фон шириной size процентов слово при
// положении pos процентов. Смещение фона в долях слова это (1-size)*pos/100,
// и картинка от смещения до смещения+size должна накрыть отрезок 0..1.
func coversWord(size, pos float64) bool {
	s := size / 100
	off := (1 - s) * pos / 100
	return off <= 0 && off+s >= 1
}

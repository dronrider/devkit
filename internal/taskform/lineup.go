package taskform

import (
	"fmt"
	"regexp"
	"strings"
)

// Состав кейсов это вариант ответа развилки первого раунда интервью (DK-969).
// Первым кругом груминга и постановки цели человек выбирает не один список
// кейсов, а один из трёх составов: узкий, сбалансированный и широкий. Составы
// едут обычными вариантами развилки, а кейсы нумеруются внутри текста
// варианта. Новой сущности «кейс» в перечне не заводится: вложенность
// «состав -> кейс» уже отложило решение «состав» DK-916 из-за цены в разборе.
// Форма живёт тут, потому что читателей у неё трое: печать блока вопроса,
// разбор ответа человека и сторож формы.

// Ширина состава. Слов ровно три, и по ним же человек узнаёт состав в блоке.
const (
	LineupNarrow  = "узкий"
	LineupMiddle  = "сбалансированный"
	LineupWide    = "широкий"
	lineupTailSep = "; "
)

// Lineup это разобранный состав кейсов.
type Lineup struct {
	Width string   // узкий, сбалансированный или широкий
	Cases []string // кейсы в порядке номеров, по одному на элемент
}

var (
	lineupHeadRe = regexp.MustCompile(`^(узкий|сбалансированный|широкий)\s+состав,\s*\d{1,2}\s+кейс\S*:\s*(\S.*)$`)
	lineupCaseRe = regexp.MustCompile(`\(\d{1,2}\)\s*`)
)

// ParseLineup разбирает текст варианта в состав. Текст не той формы это
// обычный вариант ответа, и разбор говорит об этом вторым значением: развилка
// с такими вариантами спрашивается и отвечается по-старому.
func ParseLineup(text string) (Lineup, bool) {
	m := lineupHeadRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return Lineup{}, false
	}
	tail := strings.TrimSpace(m[2])
	marks := lineupCaseRe.FindAllStringIndex(tail, -1)
	if len(marks) == 0 || marks[0][0] != 0 {
		return Lineup{}, false
	}
	l := Lineup{Width: m[1]}
	for i, mark := range marks {
		end := len(tail)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		piece := strings.TrimSpace(tail[mark[1]:end])
		piece = strings.TrimRight(piece, "; ")
		if piece == "" {
			return Lineup{}, false
		}
		l.Cases = append(l.Cases, piece)
	}
	return l, true
}

// Head это короткая строка состава: ширина и счёт кейсов. Ею состав идёт
// строкой варианта в блоке вопроса, а сами кейсы стоят ниже раскладкой по
// одному на строку. Длинный текст варианта в строке с номером не читается, и
// править его по номерам кейсов человеку не с чего.
func (l Lineup) Head() string {
	return fmt.Sprintf("%s состав, %d %s", l.Width, len(l.Cases), casesWord(len(l.Cases)))
}

// String собирает текст варианта обратно. Номера кейсов считаются заново, и
// состав после правки человека приходит в файл сплошной нумерацией.
func (l Lineup) String() string {
	parts := make([]string, 0, len(l.Cases))
	for i, c := range l.Cases {
		parts = append(parts, fmt.Sprintf("(%d) %s", i+1, strings.TrimSpace(c)))
	}
	return l.Head() + ": " + strings.Join(parts, lineupTailSep)
}

// Drop снимает кейсы по номерам, какими они стояли в раскладке. Номер за
// пределами состава это отказ: человек назвал кейс, которого в раскладке не
// было, и снять вместо него соседний значит соврать про его ответ.
func (l Lineup) Drop(nums []int) (Lineup, error) {
	gone := map[int]bool{}
	for _, n := range nums {
		if n < 1 || n > len(l.Cases) {
			return Lineup{}, fmt.Errorf("в составе кейсов %d, а в ответе «минус %d»: кейсы нумерует раскладка под блоком", len(l.Cases), n)
		}
		gone[n] = true
	}
	out := Lineup{Width: l.Width}
	for i, c := range l.Cases {
		if !gone[i+1] {
			out.Cases = append(out.Cases, c)
		}
	}
	return out, nil
}

// Add дописывает кейсы человека в хвост состава.
func (l Lineup) Add(texts []string) (Lineup, error) {
	out := Lineup{Width: l.Width, Cases: append([]string{}, l.Cases...)}
	for _, t := range texts {
		if t = strings.TrimSpace(t); t == "" {
			return Lineup{}, fmt.Errorf("у «плюс» нет текста кейса: жду «плюс <кейс одной строкой>»")
		}
		out.Cases = append(out.Cases, t)
	}
	if len(out.Cases) == 0 {
		return Lineup{}, fmt.Errorf("состав остался без кейсов: снимать весь список нечем, откажитесь от состава словами")
	}
	return out, nil
}

// Lineups разбирает варианты развилки в составы. Вариант не той формы валит
// весь разбор: развилка составов это все три варианта разом, а половина
// составов и половина обычных ответов означала бы, что номерами правится то
// один вариант, то другой.
func (f Fork) Lineups() ([]Lineup, bool) {
	choices := f.Choices()
	if len(choices) < 2 {
		return nil, false
	}
	var out []Lineup
	for _, c := range choices {
		l, ok := ParseLineup(c.Text)
		if !ok {
			return nil, false
		}
		out = append(out, l)
	}
	return out, true
}

// casesWord склоняет слово «кейс» при счёте.
func casesWord(n int) string {
	if n%100 >= 11 && n%100 <= 14 {
		return "кейсов"
	}
	switch n % 10 {
	case 1:
		return "кейс"
	case 2, 3, 4:
		return "кейса"
	}
	return "кейсов"
}

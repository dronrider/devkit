package taskform

import (
	"strings"
	"testing"
)

const narrow = "узкий состав, 2 кейса: (1) человек видит три состава; (2) кейсы правятся по номерам"

// TestParseLineupReadsCases: состав разбирается на ширину и кейсы по номерам,
// а собранный обратно текст равен исходному. Через эту пару текст варианта
// ездит в файл записи и обратно на каждой правке по номерам.
func TestParseLineupReadsCases(t *testing.T) {
	l, ok := ParseLineup(narrow)
	if !ok {
		t.Fatalf("состав не разобрался: %q", narrow)
	}
	if l.Width != LineupNarrow {
		t.Fatalf("ширина состава %q, жду %q", l.Width, LineupNarrow)
	}
	if len(l.Cases) != 2 || l.Cases[0] != "человек видит три состава" || l.Cases[1] != "кейсы правятся по номерам" {
		t.Fatalf("кейсы разобрались не так: %#v", l.Cases)
	}
	if got := l.String(); got != narrow {
		t.Fatalf("собранный состав разошёлся с исходным:\n%s\n%s", got, narrow)
	}
	if got := l.Head(); got != "узкий состав, 2 кейса" {
		t.Fatalf("голова состава %q", got)
	}
}

// TestParseLineupSkipsOtherOptions: обычный вариант ответа составом не
// считается. Развилка с такими вариантами спрашивается и отвечается
// по-старому, и грамматика «состав N» к ней не лезет.
func TestParseLineupSkipsOtherOptions(t *testing.T) {
	for _, text := range []string{
		"катит смежник",
		"узкий состав, 2 кейса: без номеров совсем",
		"средний состав, 2 кейса: (1) а; (2) б",
		"узкий состав: (1) а; (2) б",
	} {
		if _, ok := ParseLineup(text); ok {
			t.Fatalf("составом признан обычный вариант: %q", text)
		}
	}
}

// TestLineupDropRenumbers: снятый кейс уносит свой номер, а оставшиеся
// нумеруются заново сплошняком. Иначе следующая правка по номерам целилась бы
// в дырку.
func TestLineupDropRenumbers(t *testing.T) {
	l, _ := ParseLineup("широкий состав, 3 кейса: (1) первый; (2) второй; (3) третий")
	got, err := l.Drop([]int{2})
	if err != nil {
		t.Fatalf("кейс не снялся: %v", err)
	}
	want := "широкий состав, 2 кейса: (1) первый; (2) третий"
	if got.String() != want {
		t.Fatalf("после снятия состав %q, жду %q", got.String(), want)
	}
}

// TestLineupDropOutOfRange: номер за пределами состава это отказ. Человек
// назвал кейс, которого в раскладке не было, и снять соседний значит соврать
// про его ответ.
func TestLineupDropOutOfRange(t *testing.T) {
	l, _ := ParseLineup(narrow)
	if _, err := l.Drop([]int{7}); err == nil {
		t.Fatal("снятие кейса за пределами состава прошло молча")
	} else if !strings.Contains(err.Error(), "минус 7") {
		t.Fatalf("отказ не называет номер: %v", err)
	}
}

// TestLineupAddCountsWord: дописанный кейс встаёт в хвост, счёт в голове
// пересчитывается, а слово «кейс» склоняется под него.
func TestLineupAddCountsWord(t *testing.T) {
	l, _ := ParseLineup(narrow)
	got, err := l.Add([]string{"панель вешает галочки на составы"})
	if err != nil {
		t.Fatalf("кейс не дописался: %v", err)
	}
	want := "узкий состав, 3 кейса: (1) человек видит три состава; (2) кейсы правятся по номерам; (3) панель вешает галочки на составы"
	if got.String() != want {
		t.Fatalf("после правки состав %q, жду %q", got.String(), want)
	}
	for n, word := range map[int]string{1: "кейс", 2: "кейса", 5: "кейсов", 11: "кейсов", 21: "кейс"} {
		if casesWord(n) != word {
			t.Fatalf("при счёте %d слово %q, жду %q", n, casesWord(n), word)
		}
	}
}

// TestLineupAddEmpty: «плюс» без текста это отказ, а не пустой кейс в хвосте
// состава.
func TestLineupAddEmpty(t *testing.T) {
	l, _ := ParseLineup(narrow)
	if _, err := l.Add([]string{"  "}); err == nil {
		t.Fatal("пустой кейс дописался молча")
	}
}

// TestForkLineupsAllOrNothing: развилка составов это все варианты разом.
// Половина составов и половина обычных ответов означала бы, что номерами
// правится то один вариант, то другой.
func TestForkLineupsAllOrNothing(t *testing.T) {
	f := Fork{Hint: narrow, Options: []string{"сбалансированный состав, 1 кейс: (1) а"}}
	if _, ok := f.Lineups(); !ok {
		t.Fatal("развилка из двух составов составами не признана")
	}
	f.Options = append(f.Options, "давай обсудим словами")
	if _, ok := f.Lineups(); ok {
		t.Fatal("развилка с обычным вариантом признана развилкой составов")
	}
	one := Fork{Hint: narrow}
	if _, ok := one.Lineups(); ok {
		t.Fatal("одинокая рекомендация признана развилкой составов: выбирать не из чего")
	}
}

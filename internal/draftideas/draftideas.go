// Package draftideas читает раздел «Идеи» записи черновика и отличает
// разобранные мысли от неразобранных (DK-1564). Судьбу пункту приписывает
// грумер явной отметкой: сегментом после стрелки «->» в строке пункта либо
// строкой судьбы под пунктом, начинающейся словом судьбы или пометкой
// «судьба:». Слово судьбы внутри свободного текста мысли, цитаты или примера
// кода отметкой не считается: иначе новая мысль гасила бы себя сама и молча
// теряла повод разбора. Мысль без судьбы это неразобранная мысль, и только
// она даёт повод для повторного разбора.
package draftideas

import (
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// Heading это заголовок раздела идей записи.
const Heading = "## Идеи"

// GroomHeading это заголовок раздела груминга записи: его пишет откладывание
// и удаление, и по нему видно, что разбор записи уже шёл.
const GroomHeading = "## Грумминг"

// fateMarkers узнают приписанную судьбу. Слова те же, которыми судьбу
// приписывает скилл разбора.
var fateMarkers = []string{"поднята в задаче", "влита в материал", "отклонена:"}

// fateLead это словесная пометка строки судьбы под пунктом.
const fateLead = "судьба:"

// line это строка записи с признаком примера кода: заголовки и пункты внутри
// ограждённого блока разметки остаются текстом примера, а не устройством
// записи.
type line struct {
	text string
	code bool
}

// splitLines делит запись на строки и помечает строки ограждённых блоков.
// Ограждение считает общий FenceMask: волна ~~~ ограждает наравне с тройной
// кавычкой, короткая кавычка внутри длинного ограждения его не закрывает.
func splitLines(record []byte) []line {
	raw := strings.Split(string(record), "\n")
	mask, _ := taskform.FenceMask(raw)
	out := make([]line, len(raw))
	for i, ln := range raw {
		out[i] = line{text: ln, code: mask[i]}
	}
	return out
}

// idea это пункт раздела целиком, со строками продолжения, и признак
// приписанной судьбы.
type idea struct {
	text    string
	decided bool
}

// ideas собирает пункты раздела «Идеи»: пунктом считается строка с маркера
// списка, продолжением строки под ней без маркера. Раздела нет, значит
// мыслей нет.
func ideas(record []byte) []idea {
	var out []idea
	var text []string
	decided := false
	flush := func() {
		if len(text) == 0 {
			return
		}
		out = append(out, idea{text: strings.Join(text, " "), decided: decided})
	}
	inSection := false
	for _, ln := range splitLines(record) {
		if ln.code {
			continue
		}
		trim := strings.TrimSpace(ln.text)
		if strings.HasPrefix(trim, "## ") {
			if inSection {
				break
			}
			inSection = trim == Heading
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(trim, "- ") {
			flush()
			text = []string{trim}
			decided = fateMarked(trim)
			continue
		}
		if trim == "" || len(text) == 0 {
			continue
		}
		text = append(text, trim)
		// Строка судьбы узнаётся от начала: пометка «судьба:» или само слово
		// судьбы открывают строку, продолжение прозы судьбой не становится.
		rest := trim
		if strings.HasPrefix(rest, fateLead) {
			rest = strings.TrimSpace(rest[len(fateLead):])
		}
		if fateMarked(rest) {
			decided = true
		}
	}
	flush()
	return out
}

// Undecided возвращает пункты раздела без приписанной судьбы.
func Undecided(record []byte) []string {
	out := []string{}
	for _, i := range ideas(record) {
		if !i.decided {
			out = append(out, i.text)
		}
	}
	return out
}

// Count отвечает, сколько в записи мыслей всего и сколько из них без судьбы.
// Спрашивает его экран записи: по паре чисел он отличает первый разбор от
// повторного повода, не разбирая текст сам.
func Count(record []byte) (total, undecided int) {
	for _, i := range ideas(record) {
		total++
		if !i.decided {
			undecided++
		}
	}
	return total, undecided
}

// GroomSection отвечает, есть ли в записи раздел «Грумминг»: его пишет
// откладывание и удаление, и по нему видно, что разбор записи уже шёл.
// Заголовок ищется своей строкой вне примеров кода.
func GroomSection(record []byte) bool {
	for _, ln := range splitLines(record) {
		if !ln.code && strings.TrimSpace(ln.text) == GroomHeading {
			return true
		}
	}
	return false
}

// fateMarked решает, стоит ли в строке явная отметка судьбы: слово судьбы
// открывает строку или сегмент строки после запятой либо стрелки. Запятая
// читается по совместимости с записями, где судьба была приписана в конец
// пункта без стрелки. Слово судьбы внутри цитаты в «ёлочках» отметкой не
// считается: автор процитировал чужую надпись, а не назначил судьбу.
func fateMarked(s string) bool {
	for _, m := range fateMarkers {
		from := 0
		for from <= len(s) {
			i := strings.Index(s[from:], m)
			if i < 0 {
				break
			}
			at := from + i
			from = at + 1
			if quotedFate(s[:at]) {
				continue
			}
			if at == 0 ||
				(at >= 2 && s[at-2:at] == ", ") ||
				(at >= 2 && s[at-2:at] == "->") ||
				(at >= 3 && s[at-3:at] == "-> ") {
				return true
			}
		}
	}
	return false
}

// quotedFate отвечает, стоит ли позиция внутри цитаты или inline-примера:
// до неё не закрылась пара «ёлочек», прямых кавычек либо бэктиков. Подпись
// интерфейса цитируется и прямыми кавычками, и кодовым спаном, и слово
// судьбы внутри них остаётся цитатой, а не отметкой (замер QA 2026-11-06).
func quotedFate(prefix string) bool {
	delimiter, depth := 0, 0
	ascii := false
	for i := 0; i < len(prefix); {
		if delimiter > 0 {
			if prefix[i] != '`' {
				i++
				continue
			}
			end := i + 1
			for end < len(prefix) && prefix[end] == '`' {
				end++
			}
			if end-i == delimiter {
				delimiter = 0
			}
			i = end
			continue
		}
		if ascii {
			if prefix[i] == '"' {
				ascii = false
			}
			i++
			continue
		}
		if strings.HasPrefix(prefix[i:], "«") {
			depth++
			i += len("«")
			continue
		}
		if depth > 0 {
			if strings.HasPrefix(prefix[i:], "»") {
				depth--
				i += len("»")
			} else {
				i++
			}
			continue
		}
		if prefix[i] == '"' {
			ascii = true
			i++
			continue
		}
		if prefix[i] == '`' {
			end := i + 1
			for end < len(prefix) && prefix[end] == '`' {
				end++
			}
			delimiter = end - i
			i = end
			continue
		}
		i++
	}
	return delimiter > 0 || depth > 0 || ascii
}

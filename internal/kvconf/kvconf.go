// Package kvconf читает плоские конфиги devkit в .devkit: строка вида
// «ключ = значение», решётка это комментарий, пустые строки пропускаются.
//
// Такой формат носят и обвязка выката deploy.local, и бюджеты ревью
// review.conf, и разбор у них был бы одинаковым до буквы. Копия разошлась бы с
// оригиналом на первой же правке (обрезка пробелов, кавычки вокруг значения),
// и файл, который читают две утилиты, читался бы ими по-разному.
package kvconf

import (
	"bufio"
	"os"
	"strings"
)

// Pair это разобранная строка конфига. Порядок пар сохраняется: у повторённого
// ключа читатель волен взять хоть первое значение, хоть последнее.
type Pair struct {
	Key   string
	Value string
}

// Read разбирает файл в пары. Отсутствие файла не ошибка: конфига может не
// быть вовсе, и умолчания за него отвечает вызывающий. Строка без знака
// равенства пропускается молча: это проза, забытая в конфиге, а не ключ.
func Read(path string) ([]Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var pairs []Pair
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		pairs = append(pairs, Pair{Key: strings.TrimSpace(key), Value: Unquote(strings.TrimSpace(val))})
	}
	return pairs, sc.Err()
}

// Unquote снимает одну окружающую пару кавычек, если значение целиком в них
// завёрнуто. Кавычки внутри значения (ssh host 'systemctl restart foo') не
// трогаются: снимается ровно внешняя пара, не все подряд.
func Unquote(s string) string {
	if len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}
	return s
}

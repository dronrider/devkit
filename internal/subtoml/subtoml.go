// Package subtoml читает подмножество TOML, которым написаны профили харнесов
// (kit/harness) и шаблоны планов (kit/plans). Пакет заведён DK-1142: парсер жил
// в agentctl, а шаблоны планов читают ещё и другие утилиты, и вторая копия
// разошлась бы с первой на первой же правке.
package subtoml

import (
	"fmt"
	"strconv"
	"strings"
)

// Парсер подмножества TOML, которым написаны профили харнесов и слои
// конфигурации (docs/lld/DK-033-universal-kit.md). Подмножество: секции одного
// уровня, ключи key = value, значения это строки в двойных кавычках, целые,
// true/false и массивы строк в одну строку, комментарии с решётки. Вложенных
// таблиц, дат, многострочного и literal-строк нет, и за границу подмножества
// парсер не выпускает: иначе профиль однажды написали бы полным TOML, а вторая
// реализация его бы не прочитала.
//
// Реализаций у формата две, вторая в tools/devkitctl/harness.py. Тексты ошибок и
// канонический дамп у них общие, сверяются фикстурами kit/harness/testdata.

const (
	KindStr  = "str"
	KindInt  = "int"
	KindBool = "bool"
	KindArr  = "arr"
)

// Имена типов для сообщений об ошибках, одни и те же в обеих реализациях.
var KindNames = map[string]string{
	KindStr:  "строку",
	KindInt:  "целое",
	KindBool: "true/false",
	KindArr:  "массив строк",
}

type Value struct {
	Kind string
	Str  string
	Int  int
	Bool bool
	Arr  []string
	Line int
}

// Table это секция. Порядок ключей сохраняется: по нему идёт дамп и порядок
// предупреждений, а он входит в контракт фикстур.
type Table struct {
	Name string
	Keys []string
	Vals map[string]Value
}

func (t *Table) Get(key string) (Value, bool) {
	if t == nil {
		return Value{}, false
	}
	v, ok := t.Vals[key]
	return v, ok
}

func (t *Table) Str(key string) string {
	v, ok := t.Get(key)
	if !ok || v.Kind != KindStr {
		return ""
	}
	return v.Str
}

func (t *Table) Arr(key string) []string {
	v, ok := t.Get(key)
	if !ok || v.Kind != KindArr {
		return nil
	}
	return v.Arr
}

func (t *Table) Empty() bool { return t == nil || len(t.Keys) == 0 }

// Doc это разобранный файл. Верхний уровень лежит таблицей с пустым именем:
// у машинного конфига там default и enabled.
type Doc struct {
	Name   string
	Order  []string
	Tables map[string]*Table
}

func (d *Doc) Table(name string) *Table {
	if d == nil {
		return nil
	}
	return d.Tables[name]
}

func (d *Doc) Has(name string) bool {
	_, ok := d.Tables[name]
	return ok
}

// Quote это единственный способ показать строку в сообщении: %q в Go и repr
// в Python экранируют по-разному, а тексты ошибок у двух реализаций общие.
func Quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func BareKey(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func isInt(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func scanString(s string) (string, string, error) {
	var b strings.Builder
	for i := 1; i < len(s); {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return "", "", fmt.Errorf("строка не закрыта")
			}
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", "", fmt.Errorf("неизвестная escape-последовательность \\%c", s[i+1])
			}
			i += 2
			continue
		}
		if c == '"' {
			return b.String(), s[i+1:], nil
		}
		b.WriteByte(c)
		i++
	}
	return "", "", fmt.Errorf("строка не закрыта")
}

// trailer проверяет хвост после значения: пусто либо комментарий.
func trailer(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return nil
	}
	return fmt.Errorf("после значения лишнее: %s", Quote(rest))
}

func parseArray(s string) (Value, error) {
	var items []string
	rest := s[1:]
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return Value{}, fmt.Errorf("массив не закрыт: многострочные массивы вне подмножества")
		}
		if rest[0] == ']' {
			rest = rest[1:]
			break
		}
		if rest[0] != '"' {
			return Value{}, fmt.Errorf("в массиве допустимы только строки в кавычках, вижу %s", Quote(rest))
		}
		v, r, err := scanString(rest)
		if err != nil {
			return Value{}, err
		}
		items = append(items, v)
		rest = strings.TrimLeft(r, " \t")
		if rest == "" {
			return Value{}, fmt.Errorf("массив не закрыт: многострочные массивы вне подмножества")
		}
		switch rest[0] {
		case ',':
			rest = rest[1:]
		case ']':
			rest = rest[1:]
			return Value{Kind: KindArr, Arr: items}, trailer(rest)
		default:
			return Value{}, fmt.Errorf("жду запятую или ] после элемента массива, вижу %s", Quote(rest))
		}
	}
	return Value{Kind: KindArr, Arr: items}, trailer(rest)
}

func parseValue(s string) (Value, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Value{}, fmt.Errorf("нет значения")
	}
	switch s[0] {
	case '"':
		v, rest, err := scanString(s)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindStr, Str: v}, trailer(rest)
	case '\'':
		return Value{}, fmt.Errorf("literal-строки вне подмножества, значение пишется в двойных кавычках")
	case '[':
		return parseArray(s)
	}
	tok := s
	if i := strings.Index(tok, "#"); i >= 0 {
		tok = tok[:i]
	}
	tok = strings.TrimSpace(tok)
	switch tok {
	case "true":
		return Value{Kind: KindBool, Bool: true}, nil
	case "false":
		return Value{Kind: KindBool, Bool: false}, nil
	}
	if isInt(tok) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			return Value{}, fmt.Errorf("целое %s не разобрано", Quote(tok))
		}
		return Value{Kind: KindInt, Int: n}, nil
	}
	return Value{}, fmt.Errorf("значение %s вне подмножества: строка в кавычках, целое, true/false, массив строк", Quote(tok))
}

// Parse разбирает текст. Имя идёт в сообщения об ошибках и берётся как
// есть: тесты подставляют базовое имя файла, чтобы сообщения не зависели от
// того, из какой директории запущен прогон.
func Parse(name, text string) (*Doc, error) {
	d := &Doc{Name: name, Tables: map[string]*Table{}}
	cur := &Table{Vals: map[string]Value{}}
	d.Tables[""] = cur
	d.Order = append(d.Order, "")
	fail := func(line int, err error) error {
		return fmt.Errorf("%s:%d: %v", name, line, err)
	}
	for i, raw := range strings.Split(text, "\n") {
		ln := i + 1
		s := strings.TrimSpace(raw)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if strings.HasPrefix(s, "[") {
			if !strings.HasSuffix(s, "]") {
				return nil, fail(ln, fmt.Errorf("секция не закрыта: %s", Quote(s)))
			}
			name := strings.TrimSpace(s[1 : len(s)-1])
			if !BareKey(name) {
				return nil, fail(ln, fmt.Errorf("имя секции %s вне подмножества: вложенные таблицы и массивы таблиц не поддержаны", Quote(name)))
			}
			if _, ok := d.Tables[name]; ok {
				return nil, fail(ln, fmt.Errorf("секция [%s] уже была", name))
			}
			cur = &Table{Name: name, Vals: map[string]Value{}}
			d.Tables[name] = cur
			d.Order = append(d.Order, name)
			continue
		}
		key, rest, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fail(ln, fmt.Errorf("строка %s не разобрана: жду key = value", Quote(s)))
		}
		key = strings.TrimSpace(key)
		if !BareKey(key) {
			return nil, fail(ln, fmt.Errorf("ключ %s вне подмножества: допустимы буквы, цифры, дефис и подчёркивание", Quote(key)))
		}
		if _, ok := cur.Vals[key]; ok {
			where := "на верхнем уровне"
			if cur.Name != "" {
				where = "в секции [" + cur.Name + "]"
			}
			return nil, fail(ln, fmt.Errorf("ключ %s %s уже был", key, where))
		}
		v, err := parseValue(rest)
		if err != nil {
			return nil, fail(ln, fmt.Errorf("ключ %s: %v", key, err))
		}
		v.Line = ln
		cur.Keys = append(cur.Keys, key)
		cur.Vals[key] = v
		continue
	}
	return d, nil
}

func (v Value) Render() string {
	switch v.Kind {
	case KindStr:
		return Quote(v.Str)
	case KindInt:
		return strconv.Itoa(v.Int)
	case KindBool:
		if v.Bool {
			return "true"
		}
		return "false"
	default:
		parts := make([]string, 0, len(v.Arr))
		for _, s := range v.Arr {
			parts = append(parts, Quote(s))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
}

// dump это канонический вид разобранного файла: тот же порядок, комментарии
// убраны, значения перепечатаны из разобранных. Им обе реализации доказывают,
// что прочитали вход одинаково.
func (d *Doc) Dump() string {
	var b strings.Builder
	for _, name := range d.Order {
		t := d.Tables[name]
		if name != "" {
			fmt.Fprintf(&b, "[%s]\n", name)
		} else if len(t.Keys) == 0 {
			continue
		}
		for _, k := range t.Keys {
			fmt.Fprintf(&b, "%s = %s\n", k, t.Vals[k].Render())
		}
	}
	return b.String()
}

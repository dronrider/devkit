package main

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
//
// Здесь лежит копия tools/agentctl/toml.go: у каждой утилиты свой модуль, общего
// пакета у них нет, и конфиг контура пишется тем же подмножеством, что профили
// харнесов. Правка формата едет в обе копии сразу.

const (
	tomlStr  = "str"
	tomlInt  = "int"
	tomlBool = "bool"
	tomlArr  = "arr"
)

// Имена типов для сообщений об ошибках, одни и те же в обеих реализациях.
var tomlKindNames = map[string]string{
	tomlStr:  "строку",
	tomlInt:  "целое",
	tomlBool: "true/false",
	tomlArr:  "массив строк",
}

type tomlValue struct {
	Kind string
	Str  string
	Int  int
	Bool bool
	Arr  []string
	Line int
}

// tomlTable это секция. Порядок ключей сохраняется: по нему идёт дамп и порядок
// предупреждений, а он входит в контракт фикстур.
type tomlTable struct {
	Name string
	Keys []string
	Vals map[string]tomlValue
}

func (t *tomlTable) get(key string) (tomlValue, bool) {
	if t == nil {
		return tomlValue{}, false
	}
	v, ok := t.Vals[key]
	return v, ok
}

func (t *tomlTable) str(key string) string {
	v, ok := t.get(key)
	if !ok || v.Kind != tomlStr {
		return ""
	}
	return v.Str
}

func (t *tomlTable) arr(key string) []string {
	v, ok := t.get(key)
	if !ok || v.Kind != tomlArr {
		return nil
	}
	return v.Arr
}

func (t *tomlTable) empty() bool { return t == nil || len(t.Keys) == 0 }

// tomlDoc это разобранный файл. Верхний уровень лежит таблицей с пустым именем:
// у машинного конфига там default и enabled.
type tomlDoc struct {
	Name   string
	Order  []string
	Tables map[string]*tomlTable
}

func (d *tomlDoc) table(name string) *tomlTable {
	if d == nil {
		return nil
	}
	return d.Tables[name]
}

func (d *tomlDoc) has(name string) bool {
	_, ok := d.Tables[name]
	return ok
}

// quoteTOML это единственный способ показать строку в сообщении: %q в Go и repr
// в Python экранируют по-разному, а тексты ошибок у двух реализаций общие.
func quoteTOML(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func bareTOMLKey(s string) bool {
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

func tomlIsInt(s string) bool {
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

func scanTOMLString(s string) (string, string, error) {
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

// tomlTrailer проверяет хвост после значения: пусто либо комментарий.
func tomlTrailer(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return nil
	}
	return fmt.Errorf("после значения лишнее: %s", quoteTOML(rest))
}

func parseTOMLArray(s string) (tomlValue, error) {
	var items []string
	rest := s[1:]
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return tomlValue{}, fmt.Errorf("массив не закрыт: многострочные массивы вне подмножества")
		}
		if rest[0] == ']' {
			rest = rest[1:]
			break
		}
		if rest[0] != '"' {
			return tomlValue{}, fmt.Errorf("в массиве допустимы только строки в кавычках, вижу %s", quoteTOML(rest))
		}
		v, r, err := scanTOMLString(rest)
		if err != nil {
			return tomlValue{}, err
		}
		items = append(items, v)
		rest = strings.TrimLeft(r, " \t")
		if rest == "" {
			return tomlValue{}, fmt.Errorf("массив не закрыт: многострочные массивы вне подмножества")
		}
		switch rest[0] {
		case ',':
			rest = rest[1:]
		case ']':
			rest = rest[1:]
			return tomlValue{Kind: tomlArr, Arr: items}, tomlTrailer(rest)
		default:
			return tomlValue{}, fmt.Errorf("жду запятую или ] после элемента массива, вижу %s", quoteTOML(rest))
		}
	}
	return tomlValue{Kind: tomlArr, Arr: items}, tomlTrailer(rest)
}

func parseTOMLValue(s string) (tomlValue, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return tomlValue{}, fmt.Errorf("нет значения")
	}
	switch s[0] {
	case '"':
		v, rest, err := scanTOMLString(s)
		if err != nil {
			return tomlValue{}, err
		}
		return tomlValue{Kind: tomlStr, Str: v}, tomlTrailer(rest)
	case '\'':
		return tomlValue{}, fmt.Errorf("literal-строки вне подмножества, значение пишется в двойных кавычках")
	case '[':
		return parseTOMLArray(s)
	}
	tok := s
	if i := strings.Index(tok, "#"); i >= 0 {
		tok = tok[:i]
	}
	tok = strings.TrimSpace(tok)
	switch tok {
	case "true":
		return tomlValue{Kind: tomlBool, Bool: true}, nil
	case "false":
		return tomlValue{Kind: tomlBool, Bool: false}, nil
	}
	if tomlIsInt(tok) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			return tomlValue{}, fmt.Errorf("целое %s не разобрано", quoteTOML(tok))
		}
		return tomlValue{Kind: tomlInt, Int: n}, nil
	}
	return tomlValue{}, fmt.Errorf("значение %s вне подмножества: строка в кавычках, целое, true/false, массив строк", quoteTOML(tok))
}

// parseTOML разбирает текст. Имя идёт в сообщения об ошибках и берётся как
// есть: тесты подставляют базовое имя файла, чтобы сообщения не зависели от
// того, из какой директории запущен прогон.
func parseTOML(name, text string) (*tomlDoc, error) {
	d := &tomlDoc{Name: name, Tables: map[string]*tomlTable{}}
	cur := &tomlTable{Vals: map[string]tomlValue{}}
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
				return nil, fail(ln, fmt.Errorf("секция не закрыта: %s", quoteTOML(s)))
			}
			name := strings.TrimSpace(s[1 : len(s)-1])
			if !bareTOMLKey(name) {
				return nil, fail(ln, fmt.Errorf("имя секции %s вне подмножества: вложенные таблицы и массивы таблиц не поддержаны", quoteTOML(name)))
			}
			if _, ok := d.Tables[name]; ok {
				return nil, fail(ln, fmt.Errorf("секция [%s] уже была", name))
			}
			cur = &tomlTable{Name: name, Vals: map[string]tomlValue{}}
			d.Tables[name] = cur
			d.Order = append(d.Order, name)
			continue
		}
		key, rest, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fail(ln, fmt.Errorf("строка %s не разобрана: жду key = value", quoteTOML(s)))
		}
		key = strings.TrimSpace(key)
		if !bareTOMLKey(key) {
			return nil, fail(ln, fmt.Errorf("ключ %s вне подмножества: допустимы буквы, цифры, дефис и подчёркивание", quoteTOML(key)))
		}
		if _, ok := cur.Vals[key]; ok {
			where := "на верхнем уровне"
			if cur.Name != "" {
				where = "в секции [" + cur.Name + "]"
			}
			return nil, fail(ln, fmt.Errorf("ключ %s %s уже был", key, where))
		}
		v, err := parseTOMLValue(rest)
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

func (v tomlValue) render() string {
	switch v.Kind {
	case tomlStr:
		return quoteTOML(v.Str)
	case tomlInt:
		return strconv.Itoa(v.Int)
	case tomlBool:
		if v.Bool {
			return "true"
		}
		return "false"
	default:
		parts := make([]string, 0, len(v.Arr))
		for _, s := range v.Arr {
			parts = append(parts, quoteTOML(s))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
}

// dump это канонический вид разобранного файла: тот же порядок, комментарии
// убраны, значения перепечатаны из разобранных. Им обе реализации доказывают,
// что прочитали вход одинаково.
func (d *tomlDoc) dump() string {
	var b strings.Builder
	for _, name := range d.Order {
		t := d.Tables[name]
		if name != "" {
			fmt.Fprintf(&b, "[%s]\n", name)
		} else if len(t.Keys) == 0 {
			continue
		}
		for _, k := range t.Keys {
			fmt.Fprintf(&b, "%s = %s\n", k, t.Vals[k].render())
		}
	}
	return b.String()
}

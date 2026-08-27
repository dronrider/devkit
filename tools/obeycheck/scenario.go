package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Сценарий это промпт плюс проверка. Проверка это команда с кодом возврата, а
// не чтение вывода глазами: иначе стенд меряет впечатление, а не поведение.
type Scenario struct {
	ID     string
	Title  string
	End    string // на каком конце гоняется: сессия, субагент или любой
	Prompt string
	Setup  string
	Check  string
	Path   string
}

const (
	endAny     = "любой"
	endSession = "сессия"
	endSub     = "субагент"
)

const (
	sectPrompt = "Промпт"
	sectSetup  = "Подготовка"
	sectCheck  = "Проверка"
)

// runsOn отвечает, гоняется ли сценарий на этом конце. Сценарий про сам спавн
// исполнителя субагентским концом бессмысленен, поэтому конец объявляется в
// файле сценария, а не выводится флагом на всех разом.
func (s Scenario) runsOn(end string) bool {
	return s.End == endAny || s.End == end
}

// unfence снимает обрамление ```sh вокруг тела секции: в файле сценария
// команды читаются кодом, а стенду достаётся голый текст для sh -c.
func unfence(lines []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") &&
		strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[1 : len(lines)-1]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// fence следит за оградами блоков кода. Внутри блока строка «## ...» это
// текст примера, а не заголовок секции: сценарий про вычитку постановки везёт
// в подготовке heredoc с чужими заголовками, и разбор на них спотыкался.
type fence struct {
	open int // длина открытой ограды, ноль когда блока нет
}

// backticks считает обратные апострофы в начале строки, разрешая отступ.
// Ограда это три апострофа и больше, после открывающей может стоять язык.
func backticks(l string) int {
	t := strings.TrimLeft(l, " \t")
	n := 0
	for n < len(t) && t[n] == '`' {
		n++
	}
	if n < 3 {
		return 0
	}
	return n
}

// step прогоняет строку и отвечает, лежит ли она внутри блока кода. Сами
// ограды считаются частью блока. Закрывает блок только ограда не короче
// открывающей и без хвоста, поэтому вложенный блок с более длинной оградой
// внешний не рвёт.
func (f *fence) step(l string) bool {
	n := backticks(l)
	if f.open == 0 {
		if n > 0 {
			f.open = n
		}
		return n > 0
	}
	if n >= f.open && strings.TrimSpace(strings.TrimLeft(l, " \t`")) == "" {
		f.open = 0
	}
	return true
}

func parseScenario(path, text string) (Scenario, error) {
	s := Scenario{
		ID:   strings.TrimSuffix(filepath.Base(path), ".md"),
		End:  endAny,
		Path: path,
	}
	fail := func(line int, format string, a ...any) (Scenario, error) {
		return Scenario{}, fmt.Errorf("%s:%d: %s", filepath.Base(path), line, fmt.Sprintf(format, a...))
	}
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || !strings.HasPrefix(lines[i], "# ") {
		return fail(i+1, "первой строкой жду заголовок сценария «# ...»")
	}
	s.Title = strings.TrimSpace(lines[i][2:])
	if s.Title == "" {
		return fail(i+1, "пустой заголовок сценария")
	}
	i++

	body := map[string][]string{}
	sect := ""
	var f fence
	for ; i < len(lines); i++ {
		ln := i + 1
		l := lines[i]
		inCode := f.step(l)
		if !inCode && strings.HasPrefix(l, "## ") {
			sect = strings.TrimSpace(l[3:])
			switch sect {
			case sectPrompt, sectSetup, sectCheck:
			default:
				return fail(ln, "неизвестная секция «%s»: жду %s, %s или %s", sect, sectPrompt, sectSetup, sectCheck)
			}
			if _, ok := body[sect]; ok {
				return fail(ln, "секция «%s» уже была", sect)
			}
			body[sect] = nil
			continue
		}
		if sect == "" {
			t := strings.TrimSpace(l)
			if t == "" {
				continue
			}
			key, val, ok := strings.Cut(t, ":")
			if !ok {
				return fail(ln, "до первой секции жду «ключ: значение», вижу %q", t)
			}
			key, val = strings.TrimSpace(key), strings.TrimSpace(val)
			if key != "конец" {
				return fail(ln, "неизвестный ключ %q: у сценария есть только «конец»", key)
			}
			switch val {
			case endAny, endSession, endSub:
				s.End = val
			default:
				return fail(ln, "конец %q неизвестен: %s, %s или %s", val, endAny, endSession, endSub)
			}
			continue
		}
		body[sect] = append(body[sect], l)
	}

	s.Prompt = unfence(body[sectPrompt])
	s.Setup = unfence(body[sectSetup])
	s.Check = unfence(body[sectCheck])
	if s.Prompt == "" {
		return Scenario{}, fmt.Errorf("%s: нет секции «## %s» или она пуста", filepath.Base(path), sectPrompt)
	}
	if s.Check == "" {
		return Scenario{}, fmt.Errorf("%s: нет секции «## %s» или она пуста; сценарий без объективной "+
			"проверки стендом не гоняется", filepath.Base(path), sectCheck)
	}
	return s, nil
}

func readScenario(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	return parseScenario(path, string(data))
}

// loadScenarios читает директорию сценариев целиком, в порядке имён файлов:
// порядок строк таблицы должен быть один и тот же от прогона к прогону.
func loadScenarios(dir string, only []string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("директория сценариев: %v", err)
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []Scenario
	seen := map[string]bool{}
	for _, name := range names {
		s, err := readScenario(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if len(want) > 0 && !want[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	for _, id := range only {
		if !seen[id] {
			return nil, fmt.Errorf("сценария %q в %s нет", id, dir)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("в %s не нашлось ни одного сценария", dir)
	}
	return out, nil
}

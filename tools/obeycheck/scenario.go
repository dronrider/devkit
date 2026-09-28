package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/obey"
)

// Сценарий это промпт плюс проверка. Проверка это команда с кодом возврата, а
// не чтение вывода глазами: иначе стенд меряет впечатление, а не поведение.
type Scenario struct {
	ID       string
	Title    string
	End      string // на каком конце гоняется: сессия, субагент или любой
	Subjects []obey.Subject
	Env      []string // пары ИМЯ=значение из ключа «окружение», едут в окружение прогона поверх стенда
	Prompt   string
	Reply    string // вторая реплика человека тем же ходом --resume; пусто, если реплика одна
	Setup    string
	Check    string
	Judge    *Judge // судейская секция, необязательная
	Path     string
}

// Subject собирает предмет сценария обратно в строку ключа: так он печатается
// в `--list` и в отказах.
func (s Scenario) Subject() string { return obey.Join(s.Subjects) }

const (
	endAny     = "любой"
	endSession = "сессия"
	endSub     = "субагент"
)

// envKey это ключ шапки с переменными окружения прогона. Сценарий им называет
// заход, который стенд сам не отличает: печатный вызов стенда для команд
// devkit выглядит живым окном, а признак автоматики (DEVKIT_HEADLESS) ставит
// подъёмщик, которого у стенда нет (DK-1169). Пары разделяются «;», как у
// предмета, и заданный так признак стенд не вычищает.
const envKey = "окружение"

// envPairRe это одна пара ключа «окружение»: имя переменной оболочки и
// значение после знака равенства, пустое значение законно.
var envPairRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=.*$`)

// parseEnvPairs разбирает значение ключа «окружение».
func parseEnvPairs(val string) ([]string, error) {
	var out []string
	for _, kv := range strings.Split(val, ";") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		if !envPairRe.MatchString(kv) {
			return nil, fmt.Errorf("окружение %q: жду пары ИМЯ=значение через «;»", kv)
		}
		out = append(out, kv)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ключ «%s» пуст: жду пары ИМЯ=значение через «;»", envKey)
	}
	return out, nil
}

const (
	sectPrompt = "Промпт"
	sectReply  = "Ответ"
	sectSetup  = "Подготовка"
	sectCheck  = "Проверка"
)

// sectNames это секции сценария в порядке, в котором они перечисляются в
// отказах разбора.
var sectNames = []string{sectPrompt, sectReply, sectSetup, sectCheck, sectJudge}

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
	start := map[string]int{} // номер строки заголовка секции, для отказов разбора
	sect := ""
	var f obey.Fence
	for ; i < len(lines); i++ {
		ln := i + 1
		l := lines[i]
		inCode := f.Step(l)
		if !inCode && strings.HasPrefix(l, "## ") {
			sect = strings.TrimSpace(l[3:])
			switch sect {
			case sectPrompt, sectReply, sectSetup, sectCheck, sectJudge:
			default:
				return fail(ln, "неизвестная секция «%s»: жду %s", sect, strings.Join(sectNames, ", "))
			}
			if _, ok := body[sect]; ok {
				return fail(ln, "секция «%s» уже была", sect)
			}
			body[sect] = nil
			start[sect] = ln
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
			switch key {
			case "конец":
				switch val {
				case endAny, endSession, endSub:
					s.End = val
				default:
					return fail(ln, "конец %q неизвестен: %s, %s или %s", val, endAny, endSession, endSub)
				}
			case obey.Key:
				subs, err := obey.ParseSubjects(val)
				if err != nil {
					return fail(ln, "%v", err)
				}
				s.Subjects = append(s.Subjects, subs...)
			case envKey:
				pairs, err := parseEnvPairs(val)
				if err != nil {
					return fail(ln, "%v", err)
				}
				s.Env = append(s.Env, pairs...)
			default:
				return fail(ln, "неизвестный ключ %q: у сценария есть «конец», «%s» и «%s»", key, obey.Key, envKey)
			}
			continue
		}
		body[sect] = append(body[sect], l)
	}

	s.Prompt = unfence(body[sectPrompt])
	s.Reply = unfence(body[sectReply])
	s.Setup = unfence(body[sectSetup])
	s.Check = unfence(body[sectCheck])
	if raw, ok := body[sectJudge]; ok {
		j, err := parseJudge(raw, func(i int, format string, a ...any) error {
			return fmt.Errorf("%s:%d: %s", filepath.Base(path), start[sectJudge]+1+i, fmt.Sprintf(format, a...))
		})
		if err != nil {
			return Scenario{}, err
		}
		s.Judge = j
	}
	if s.Prompt == "" {
		return Scenario{}, fmt.Errorf("%s: нет секции «## %s» или она пуста", filepath.Base(path), sectPrompt)
	}
	if s.Check == "" {
		return Scenario{}, fmt.Errorf("%s: нет секции «## %s» или она пуста; сценарий без объективной "+
			"проверки стендом не гоняется", filepath.Base(path), sectCheck)
	}
	if len(s.Subjects) == 0 {
		return Scenario{}, fmt.Errorf("%s: нет ключа «%s»: сценарий обязан назвать текст, который он "+
			"меряет, иначе привязку к правилу проверить нечем", filepath.Base(path), obey.Key)
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
// порядок строк таблицы должен быть один и тот же от прогона к прогону. Заодно
// проверяется привязка каждого сценария к дереву devkit: осиротевший предмет
// это переименованный раздел или уехавший скилл, и молча гонять такой сценарий
// значит мерять текст, которого больше нет.
func loadScenarios(dir, root string, only, forFiles []string) ([]Scenario, error) {
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
		if err := obey.VerifyAll(root, s.Subjects); err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if len(want) > 0 && !want[s.ID] {
			continue
		}
		seen[s.ID] = true
		if len(forFiles) > 0 && !coversAnyFile(s, forFiles) {
			continue
		}
		out = append(out, s)
	}
	for _, id := range only {
		if !seen[id] {
			return nil, fmt.Errorf("сценария %q в %s нет", id, dir)
		}
	}
	if len(out) == 0 {
		if len(forFiles) > 0 {
			return nil, fmt.Errorf("сценария с предметом на %s в %s нет: завести сценарий с ключом «%s»",
				strings.Join(forFiles, ", "), dir, obey.Key)
		}
		return nil, fmt.Errorf("в %s не нашлось ни одного сценария", dir)
	}
	return out, nil
}

// coversAnyFile отвечает, покрывает ли предмет сценария хоть один из файлов
// отбора `--for`. Раздел тут не спрашивается: автор правки называет файлы, а
// какой раздел тронут, знают ворота слияния по диффу.
func coversAnyFile(s Scenario, files []string) bool {
	for _, f := range files {
		if obey.CoversAny(s.Subjects, f, "") {
			return true
		}
	}
	return false
}

// relToDevkit приводит путь отбора к виду «от корня devkit»: автор зовёт стенд
// из дерева задачи и пишет путь так, как его показал git.
func relToDevkit(root, p string) string {
	if filepath.IsAbs(p) {
		if rel, err := filepath.Rel(root, p); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

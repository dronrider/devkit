package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// План работ сессии живёт файлом ~/.devkit/plans/<ID сессии>.json, и читает
// его дашборд: кольцо в шапке разговора и блок «План агента» на экране задачи.
// До DK-613 формат держался текстом правила в пяти копиях, каждая сессия
// собирала JSON руками, и файлы расходились видом. Здесь формат держит
// утилита: агент называет пункты словами, а имя файла, состояния и запись
// целиком считает команда.

// planItem это пункт плана в том виде, в каком он лежит в файле.
type planItem struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

// planEnvSession, planEnvChild и planEnvTmux это окружение, из которого
// считается адрес файла. Своё имя сессия Claude Code кладёт сама, признак
// субагента приезжает от харнеса, а имя tmux-сессии это заказ поднявшего:
// в контуре второй подписки CLAUDE_CODE_SESSION_ID пуст, и без запасного
// адреса план такой сессии писать некуда (DK-269).
const (
	planEnvSession = "CLAUDE_CODE_SESSION_ID"
	planEnvChild   = "CLAUDE_CODE_CHILD_SESSION"
	planEnvTmux    = "DEVKIT_TMUX"
	planEnvLabel   = "DEVKIT_PLAN_LABEL"
)

// planAddr это адрес файла плана: чей план и как называется файл.
type planAddr struct {
	sid   string
	label string
	file  string
}

// planDir это каталог планов машины.
func planDir(home string) string {
	return filepath.Join(home, ".devkit", "plans")
}

// planLabelOK бережёт имя файла от метки, которая уедет за пределы каталога
// или расклеит маску дашборда (<sid>-sub-<метка>.json).
func planLabelOK(label string) bool {
	if label == "" || len(label) > 40 {
		return false
	}
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return !strings.HasPrefix(label, ".")
}

// planResolve считает адрес плана по окружению и флагам. Субагент пишет свой
// файл с меткой: CLAUDE_CODE_SESSION_ID у субагентов одной пачки общий, своего
// признака окружение им не даёт, и без метки они писали план поверх соседского
// (DK-527).
func planResolve(home, sid, label string, env func(string) string) (planAddr, error) {
	var a planAddr
	a.sid = sid
	if a.sid == "" {
		a.sid = strings.TrimSpace(env(planEnvSession))
	}
	if a.sid == "" {
		a.sid = strings.TrimSpace(env(planEnvTmux))
	}
	if a.sid == "" {
		return a, fmt.Errorf("не видно, чей это план: пусты %s и %s, назови сессию флагом --sid",
			planEnvSession, planEnvTmux)
	}
	a.label = label
	if a.label == "" {
		a.label = strings.TrimSpace(env(planEnvLabel))
	}
	if a.label == "" && strings.TrimSpace(env(planEnvChild)) == "1" {
		return a, fmt.Errorf("ты субагент (%s=1), ID сессии у тебя от внешней сессии: назови свою метку флагом --label, иначе план ляжет поверх чужого",
			planEnvChild)
	}
	name := a.sid
	if a.label != "" {
		if !planLabelOK(a.label) {
			return a, fmt.Errorf("метка %q не годится в имя файла: жду буквы, цифры, дефис, подчёркивание и точку, до сорока знаков", a.label)
		}
		name = a.sid + "-sub-" + a.label
	}
	a.file = filepath.Join(planDir(home), name+".json")
	return a, nil
}

// planRead читает план из файла. Пропавший файл это пустой план, а не ошибка:
// первая команда сессии кладёт план на пустое место.
func planRead(file string) ([]planItem, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []planItem
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("файл плана %s не разобран: %v", file, err)
	}
	for i := range out {
		if out[i].State == "" {
			out[i].State = "pending"
		}
	}
	return out, nil
}

// planWrite кладёт план целиком. Запись идёт через временный файл: дашборд
// читает каталог каждым тиком, и половина записи попадалась бы ему нечитаемым
// файлом.
func planWrite(file string, plan []planItem) error {
	if plan == nil {
		plan = []planItem{}
	}
	data, err := json.MarshalIndent(plan, "", " ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// planPick находит пункт по номеру (с единицы) или по куску текста. Номер
// удобен команде, текст живой сессии: она держит в голове слова, а не счёт.
func planPick(plan []planItem, arg string) (int, error) {
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(plan) {
			return 0, fmt.Errorf("пункта %d в плане нет, их %d", n, len(plan))
		}
		return n - 1, nil
	}
	needle := strings.ToLower(strings.TrimSpace(arg))
	hit := -1
	for i, it := range plan {
		if strings.Contains(strings.ToLower(it.Text), needle) {
			if hit >= 0 {
				return 0, fmt.Errorf("под %q подходит и пункт %d, и пункт %d: назови номером", arg, hit+1, i+1)
			}
			hit = i
		}
	}
	if hit < 0 {
		return 0, fmt.Errorf("пункта по %q в плане нет", arg)
	}
	return hit, nil
}

// planItems режет позиционные аргументы на пункты: строка с переносами это
// список, а не один пункт длиной в экран.
func planItems(args []string) []planItem {
	var out []planItem
	for _, a := range args {
		for _, line := range strings.Split(a, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			if line == "" {
				continue
			}
			out = append(out, planItem{Text: line, State: "pending"})
		}
	}
	return out
}

// planMark это значок состояния в печати плана.
func planMark(state string) string {
	switch state {
	case "completed":
		return "[x]"
	case "in_progress":
		return "[>]"
	}
	return "[ ]"
}

// planPrint печатает план человеку: тем же видом, что и файл, но читаемым.
func planPrint(head string, plan []planItem) string {
	var b strings.Builder
	running := 0
	done := 0
	for i, it := range plan {
		if it.State == "in_progress" {
			running = i + 1
		}
		if it.State == "completed" {
			done++
		}
	}
	fmt.Fprintf(&b, "%s: пунктов %d, закрыто %d", head, len(plan), done)
	if running > 0 {
		fmt.Fprintf(&b, ", идёт %d", running)
	}
	for i, it := range plan {
		fmt.Fprintf(&b, "\n %2d %s %s", i+1, planMark(it.State), it.Text)
	}
	return b.String()
}

// planSubFiles это планы субагентов сессии: их пишут делегаты, и человеку в
// терминале они нужны рядом с планом самой сессии, как в кольце дашборда.
func planSubFiles(home, sid string) []string {
	ents, err := os.ReadDir(planDir(home))
	if err != nil {
		return nil
	}
	pre := sid + "-sub"
	var out []string
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, pre) || !strings.HasSuffix(name, ".json") {
			continue
		}
		rest := strings.TrimSuffix(name[len(pre):], ".json")
		if rest != "" && !strings.HasPrefix(rest, "-") {
			continue
		}
		out = append(out, filepath.Join(planDir(home), name))
	}
	sort.Strings(out)
	return out
}

// cmdPlan это все четыре действия над планом одной командой: положить, начать
// пункт, закрыть пункт, показать.
func cmdPlan(home, op string, args []string, sid, label string, env func(string) string) (string, error) {
	a, err := planResolve(home, sid, label, env)
	if err != nil {
		return "", err
	}
	plan, err := planRead(a.file)
	if err != nil {
		return "", err
	}
	head := "план сессии " + a.sid
	if a.label != "" {
		head = "план субагента " + a.label + " (сессия " + a.sid + ")"
	}
	switch op {
	case "show":
		out := planPrint(head, plan)
		if a.label == "" {
			for _, f := range planSubFiles(home, a.sid) {
				sub, err := planRead(f)
				if err != nil || len(sub) == 0 {
					continue
				}
				mark := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), a.sid+"-sub-"), ".json")
				out += "\n" + planPrint("план субагента "+mark, sub)
			}
		}
		if len(plan) == 0 {
			out += "\nплана нет, положи его командой agentctl plan set"
		}
		return out, nil
	case "set":
		items := planItems(args)
		if len(items) == 0 {
			return "", fmt.Errorf("жду пункты плана: plan set <пункт> [<пункт>...]")
		}
		if err := planWrite(a.file, items); err != nil {
			return "", err
		}
		return planPrint(head, items), nil
	case "step", "done":
		if len(plan) == 0 {
			return "", fmt.Errorf("плана нет, класть его команде нечем: сперва plan set")
		}
		if err := planStep(plan, op, args); err != nil {
			return "", err
		}
		if err := planWrite(a.file, plan); err != nil {
			return "", err
		}
		return planPrint(head, plan), nil
	}
	return "", fmt.Errorf("неизвестное действие %q, жду set, step, done или show", op)
}

// planStep двигает состояния на месте. Начатый пункт закрывает предыдущий
// идущий: план ведётся по одному шагу, и два идущих пункта в кольце
// неотличимы от плана, который забыли закрыть.
func planStep(plan []planItem, op string, args []string) error {
	idx := -1
	switch {
	case len(args) > 0:
		n, err := planPick(plan, args[0])
		if err != nil {
			return err
		}
		idx = n
	case op == "done":
		for i, it := range plan {
			if it.State == "in_progress" {
				idx = i
			}
		}
		if idx < 0 {
			return fmt.Errorf("идущего пункта в плане нет, назови номером или текстом")
		}
	default:
		for i, it := range plan {
			if it.State == "pending" {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("ждущих пунктов в плане не осталось")
		}
	}
	if op == "done" {
		plan[idx].State = "completed"
		return nil
	}
	for i := range plan {
		if plan[i].State == "in_progress" {
			plan[i].State = "completed"
		}
	}
	plan[idx].State = "in_progress"
	return nil
}

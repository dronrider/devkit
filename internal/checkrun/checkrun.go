// Package checkrun это отбор подъёма проверяющего после выката (DK-718,
// DK-947): нужен ли строке Check прогон агентской части сценария, кто вёл
// разработку и какой моделью поднимать того, кто сценарий прогонит.
//
// Зовущих у отбора трое, и правила у них одни. Выкат без человека в окне
// (shipctl merge и ship при autonomous = true) поднимает проверяющего сразу,
// тик devkitctl watch страхует то, что мимо выката проехало, командой
// `shipctl check-run`, а `dashboard check` остаётся обёрткой для экрана. Сам
// подъём у каждого свой носитель: shipctl зовёт `taskctl run <ID> --model`,
// дашборд ту же лестницу internal/taskhead своим окружением окна. До DK-947
// правила жили в дашборде, и без его бинаря подъёма не было вовсе.
//
// Независимость проверяющего несёт отдельная сессия (DK-1360): прогон идёт в
// свежем разговоре, не участвовавшем в круге разработки, и совпадение модели
// с моделью разработки помехой не служит. Ярус даёт вердикт
// `agentctl pick --role review`, у роли ревью пол base, а разработку задач
// ценой M ведёт тот же base. Ступень вверх по лестнице подписки отбор берёт
// только когда модель яруса недоступна: раскладка её не назвала либо подписку
// снял отбор допущенных. Сессии участников круга и прогона хранит запись
// этапов, а сверяет их ворота taskctl close. Если первая лестница не дала
// модели, отбор пробует другие включённые лестницы с того же яруса. Отказ
// остаётся, когда ни одна из них модели яруса не дала.
package checkrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/deployconf"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// Виды приёмки, по которым отбор разводит строки (LLD DK-292).
const (
	AcceptMixed = "mixed"
	AcceptUser  = "user"
)

// RoleReview это роль вердикта, которой назначается проверяющий.
const RoleReview = "review"

// FallbackTier это ярус, когда вердикт не ответил: тот же pro, что у
// груминга и у кнопки запуска без вердикта.
const FallbackTier = "pro"

// OrderHead это начало заказа проверяющему. По нему оболочка конвейера
// task-run.py узнаёт голову проверки: строку mixed без отметки smoke голова
// разработки сдаёт проверяющему, а голова проверки, кончившая проход без
// отметки, это уже поломка, и её оболочка называет вслух (DK-947).
const OrderHead = "Прогони агентскую часть сценария проверки "

// Row это строка доски глазами отбора. Accept пуст у агентского вида: он
// умолчание и суффикса в заголовке не носит.
type Row struct {
	ID, Title, Sect, Section, Accept, Fail string
	// Control это число дней контроля строки (DK-1470): -1 у строки без
	// контроля. Дни и срок приезжают полями, а не разбором заголовка: у
	// shipctl строка с суффиксами, у дашборда снятая taskctl list --json, и
	// разбор на месте разошёлся бы с taskctl на первой правке.
	Control int
	// Due это срок строки датой 2006-01-02, пусто без срока.
	Due string
}

// DocPath это файл задачи в корне проекта.
func DocPath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// Need решает, нужен ли строке прогон. Первым спрашивается проект: подъём
// заводит только конвейер, доверенный агенту целиком (`autonomous = true` в
// обвязке выката). Проект с выкатом за пользователем проверяющего не поднимает
// вовсе: там человек в окне, до Check строка доходит с его рук, и сессия
// поверх его работы вставала бы каждым тиком сторожка. Дальше идут те же
// признаки, по которым taskctl отбирает строки Check в закрытие автоматикой:
// секция, вид приёмки, непогашенный провал и отметка smoke на последний выкат.
// Пустой ответ значит «прогон нужен», непустой это причина, по которой
// поднимать нечего.
func Need(root string, row Row) string {
	if !deployconf.Autonomous(root) {
		return "выкат за пользователем (autonomous = false в " + deployconf.Rel +
			"): проверяющего поднимает человек"
	}
	if row.Sect != "check" {
		return "строка не в Check (" + row.Section + ")"
	}
	if row.Accept == AcceptUser {
		return "приёмка за человеком (вид user): агентской половины у сценария нет"
	}
	if row.Fail != "" {
		return "непогашенный провал проверки: сначала чинится прод"
	}
	doc, err := os.ReadFile(DocPath(root, row.ID))
	if err != nil {
		return "файла задачи нет: сценарий прогонять не по чему"
	}
	if taskform.SmokeCovers(string(doc)) {
		return "отметка «" + taskform.SmokeNote + "» стоит: сценарий после выката прогнан"
	}
	return ""
}

// DevExecutor называет модель, которая вела разработку задачи: незакрытый
// пакет этапов и раздел «Ход работы» файла задачи, оба разбирает
// internal/stage, тот же код, которым ворота закрытия узнают модель участника
// круга. Имя едет в заказе и отчёте, а независимость по нему не судится.
func DevExecutor(root, id string) (string, bool) {
	var lines []string
	if doc, err := os.ReadFile(DocPath(root, id)); err == nil {
		lines = taskform.SectionLines(string(doc), taskform.Stages)
	}
	var pending []stage.Stage
	if rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id)); err == nil {
		pending = rec.Stages
	}
	return stage.LastExecutor(lines, pending)
}

// Order это заказ поднятой сессии: прогнать агентскую часть сценария на
// выкаченном коде и довести строку, насколько её вид позволяет. Слова тут
// дословные, как у заказов экрана: по ним скиллы доски разводят работу. Имя
// исполнителя разработки едет в заказе для сведения: независимость несёт
// сама свежесть сессии проверяющего, а ворота закрытия сверяют сессии, и
// сказать это до прогона дешевле, чем получить отказ после. Хвост долга
// сдачи ложится в тот же заказ (DK-1356): проверяющего поднимают все
// конвейерные маршруты, и разбор долга уезжает с ним, а не отдельным
// маршрутом.
func Order(id, dev, accept string) string {
	out := OrderHead + id +
		" на выкаченном коде: вывод прогона в раздел «Проверка» файла задачи," +
		" прогонявшего отметь `agentctl stage " + id + " проверка --by <модель>`," +
		" выкат отметь `shipctl smoke " + id + "`."
	if accept == AcceptUser || accept == AcceptMixed {
		out += " Вид приёмки " + accept + ": твоя половина агентская, шаг человека остаётся ему," +
			" строку из Check не закрывай."
	} else {
		out += " Дальше закрой строку (`taskctl close " + id + "`)."
	}
	// Хвост долга у конвейерной сдачи разбирает проверяющий (DK-1356, развилка
	// «разбор»): его подъём уже стоит на всех конвейерных маршрутах, и шаг
	// ложится в заказ, а не в ещё один маршрут. Ручную сдачу ведёт исполнитель
	// и закрывает отметку сам, а шаг в заказе проверяющего для неё перестраховка.
	out += " Хвост долга этой сдачи: пройди скилл board-debt по диффу круга и закрой" +
		" отметку `taskctl debt-done " + id + " \"пересечений нет\"` либо \"взята <ID долга>\"."
	if dev != "" {
		out += " Разработку вёл " + dev + ": прогон веди в этой отдельной сессии," +
			" ворота закрытия сверяют сессии, и совпадение модели помехой не служит."
	}
	out += " Сценарий провалился, значит прод сломан: `taskctl fail " + id +
		" --reason \"чем сломан прод\"` и разбор по скиллу board-ship, а не закрытие."
	return out
}

// Again это заказ следующих проходов: строку к тому времени уже двигали.
func Again(id string) string { return "Продолжай выполнение " + id }

// Step это ступень лестницы подписки: ярус и модель, в которую он развёрнут.
type Step struct {
	Tier  string `json:"tier"`
	Model string `json:"model"`
}

// Ladder это целая включённая лестница; первая в списке штатная для запуска.
type Ladder struct {
	Name  string
	Steps []Step
	Via   map[string]string
}

// Choice это исход выбора модели проверяющего. Model пуста, когда лестница
// яруса не назвала: у одиночной лестницы решение за зовущим, а отбор
// допущенных подписок отвечает отказом, чтобы подписку не выбирал носитель.
type Choice struct {
	Tier, Model string
	Harness     string
	// From это ярус вердикта, когда модель взята ступенью выше.
	From    string
	Stepped bool
	// Refusal непуст, когда поднять некем: разрешённым автоматике подпискам
	// модели яруса и выше не досталось, либо ярус раскладкой не назван.
	// Модель разработки выбор не режет: независимость несёт отдельная сессия
	// прогона, и та же модель разрешена (DK-1360).
	Refusal string
}

// Choose разворачивает ярус вердикта в модель по лестнице подписки. Лестница
// идёт в порядке раскладки машины, снизу вверх (agentctl harness --json отдаёт
// её так же).
func Choose(tier string, ladder []Step) Choice {
	for _, s := range ladder {
		if s.Tier != tier {
			continue
		}
		if s.Model == "" {
			return Choice{Tier: tier}
		}
		return Choice{Tier: tier, Model: s.Model}
	}
	return Choice{Tier: tier}
}

// ChooseAvailable меняет целую лестницу лишь когда модель яруса недоступна:
// раскладка её не назвала либо подписку снял отбор допущенных. Поиск идёт
// ярусами снаружи: сперва точный ярус по всем лестницам, и соседняя допущенная
// подписка дешевле ступени вверх, потому что ярус вердикта это требование
// достаточности, а не нижняя граница. Ступени выше обходят тем же порядком,
// от яруса к ярусу. Без разрешённой модели ответ отказ, а не пустой выбор:
// пустой выбор оставлял носителю подписку исходного via, и подъём шёл мимо
// запрета (DK-1453).
func ChooseAvailable(tier string, ladders []Ladder) Choice {
	if len(ladders) == 0 {
		return Choice{Tier: tier, Refusal: "в раскладке машины нет подписок, разрешённых" +
			" автоматическим назначениям (ключ auto машинного конфига)"}
	}
	admitted := map[string]bool{}
	for _, ladder := range ladders {
		admitted[ladder.Name] = true
	}
	selectFrom := func(tier string, ladder Ladder) Choice {
		steps := make([]Step, 0, len(ladder.Steps))
		for _, step := range ladder.Steps {
			owner := ladder.Via[step.Tier]
			if owner == "" || admitted[owner] {
				steps = append(steps, step)
			}
		}
		ch := Choose(tier, steps)
		ch.Harness = ladder.Via[ch.Tier]
		if ch.Harness == "" {
			ch.Harness = ladder.Name
		}
		return ch
	}
	first := selectFrom(tier, ladders[0])
	if first.Refusal == "" && first.Model != "" {
		return first
	}
	ranks := map[string]int{"mini": 0, "base": 1, "pro": 2, "max": 3}
	if minimum, valid := ranks[tier]; valid {
		for _, candidate := range []string{"mini", "base", "pro", "max"} {
			if ranks[candidate] < minimum {
				continue
			}
			for _, ladder := range ladders {
				ch := selectFrom(candidate, ladder)
				if ch.Refusal == "" && ch.Model != "" {
					if ch.Tier != tier {
						ch.From, ch.Stepped = tier, true
					}
					return ch
				}
			}
		}
	}
	if first.Refusal != "" {
		return first
	}
	return Choice{Tier: tier, Refusal: fmt.Sprintf(
		"среди подписок, разрешённых автоматическим назначениям, нет модели яруса %s и ступеней"+
			" выше: ступени запрещённых подписок отбор снял, а разрешённым раскладка модели не"+
			" назвала; разрешить подписку ключом auto машинного конфига либо назвать модель"+
			" яруса в раскладке машины", tier)}
}

// ParseTier достаёт ярус из машинных строк вердикта `agentctl pick`.
func ParseTier(out string) string { return machineLine(out, "tier:") }

// ParseModel достаёт модель из тех же машинных строк: их печатают и вердикт
// `agentctl pick`, и расчёт проверяющего `agentctl check`. Прочерк это ответ
// «разворачивать нечем», и модели в нём нет.
func ParseModel(out string) string {
	if m := machineLine(out, "model:"); m != "-" {
		return m
	}
	return ""
}

func machineLine(out, key string) string {
	for _, ln := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), key); ok {
			if v := strings.TrimSpace(rest); v != "" {
				return v
			}
		}
	}
	return ""
}

// Plan это решённый подъём одной строки: кого, какой моделью и с каким
// заказом. Носитель поднимает его своей дорогой.
type Plan struct {
	Row      Row
	Dev      string
	DevKnown bool
	Choice   Choice
	TierWhy  string
	Order    string
	Again    string
}

// Raised собирает строку отчёта о поднятом прогоне. where это слова носителя
// про то, где встала голова.
func (p Plan) Raised(where string) string {
	line := fmt.Sprintf("%s: прогон сценария поднят %s, %s", p.Row.ID, where, p.TierWhy)
	if p.Choice.Harness != "" {
		line += ", подписка " + p.Choice.Harness
	}
	if p.Choice.Stepped {
		line += fmt.Sprintf(", ярус %s поднят ступенью до %s: модель яруса недоступна",
			p.Choice.From, p.Choice.Tier)
	}
	switch {
	case p.DevKnown && p.Choice.Model != "":
		line += fmt.Sprintf(", модель %s, разработку вёл %s; независимость несёт отдельная сессия прогона, не имя модели", p.Choice.Model, p.Dev)
	case p.DevKnown:
		line += fmt.Sprintf(", разработку вёл %s (модель яруса раскладка не назвала, независимость сторожат ворота закрытия)", p.Dev)
	default:
		line += ", исполнителя разработки записи не назвали, независимость сторожат ворота закрытия"
	}
	return line
}

// Report это исход по одной строке: слова для журнала зовущего и признаки.
// Поломкой считается только то, где подъём был нужен и не вышел: строка без
// нужды в прогоне (отметка стоит, приёмка за человеком, идёт своя сессия) это
// штатное «поднимать нечего».
type Report struct {
	ID     string `json:"id"`
	Line   string `json:"line"`
	Failed bool   `json:"failed"`
	Raised bool   `json:"raised"`
}

// NoNeed это слова строки, которой подъём не нужен. По ним тик отличает
// норму от события, и менять их без правки тика нельзя.
const NoNeed = "подъём не нужен"

// NotRaised это слова отказа нужного подъёма.
const NotRaised = "прогон не поднят"

// Carrier это носитель подъёма: у дашборда своё окружение окна и свои
// проверки tmux, у shipctl команда taskctl run.
type Carrier interface {
	// Ready это предполётная проверка носителя: пусто значит «поднимать
	// можно», иначе слова отчёта и признак поломки.
	Ready(id string) (string, bool)
	// Tier спрашивает ярус проверяющего и называет, откуда он взялся.
	Tier(id string) (string, string)
	// Ladder это лестница подписки, на которой встанет голова.
	Ladder() []Step
	// Raise поднимает решённый прогон.
	Raise(p Plan) Report
}

// Run проходит по названным строкам, а без имён по всей секции Check.
// Пустых исходов не бывает: на каждую строку приходится свой отчёт, а на
// пустую секцию один общий.
func Run(c Carrier, root string, rows map[string]Row, ids []string) []Report {
	if len(ids) == 0 {
		for id, row := range rows {
			if row.Sect == "check" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return []Report{{Line: "в Check пусто: поднимать нечего"}}
		}
	}
	var out []Report
	for _, id := range ids {
		out = append(out, one(c, root, rows, id))
	}
	return out
}

func one(c Carrier, root string, rows map[string]Row, id string) Report {
	row, ok := rows[id]
	if !ok {
		return Report{ID: id, Line: id + ": строки нет на доске проекта " + filepath.Base(root), Failed: true}
	}
	if why := Need(root, row); why != "" {
		return Report{ID: id, Line: id + ": " + NoNeed + ", " + why}
	}
	if line, failed := c.Ready(id); line != "" {
		return Report{ID: id, Line: id + ": " + line, Failed: failed}
	}
	tier, why := c.Tier(id)
	dev, known := DevExecutor(root, id)
	ch := Choose(tier, c.Ladder())
	if multi, ok := c.(interface{ Ladders() []Ladder }); ok {
		ch = ChooseAvailable(tier, multi.Ladders())
	}
	if ch.Refusal != "" {
		return Report{ID: id, Line: id + ": " + NotRaised + ", " + ch.Refusal, Failed: true}
	}
	p := Plan{Row: row, Dev: dev, DevKnown: known, Choice: ch, TierWhy: why,
		Order: Order(id, dev, row.Accept), Again: Again(id)}
	rep := c.Raise(p)
	rep.ID = id
	return rep
}

// Failed отвечает, провалился ли хоть один нужный подъём.
func Failed(reps []Report) bool {
	for _, r := range reps {
		if r.Failed {
			return true
		}
	}
	return false
}

// askWait ограничивает вопросы утилитам: подъём зовут после необратимого
// слияния, и висеть отчёту заложником подпроцесса нельзя.
const askWait = 30 * time.Second

func ask(dir string, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), askWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return out, fmt.Errorf("%s %s не ответил за %s", bin, args[0], askWait)
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return out, err
}

// AskTier спрашивает ярус проверяющего у `agentctl pick <ID> --role review`.
// Вторая строка это слова про то, откуда ярус взялся: молчащий вердикт
// откатывает на FallbackTier, и молчать об откате нельзя.
func AskTier(root, id string) (string, string) {
	bin, err := exec.LookPath("agentctl")
	if err != nil {
		return FallbackTier, "agentctl не нашёлся в PATH: ярус " + FallbackTier
	}
	out, err := ask(root, bin, "pick", id, "--role", RoleReview)
	if err != nil {
		return FallbackTier, fmt.Sprintf("вердикт agentctl pick не ответил (%v): ярус %s", err, FallbackTier)
	}
	if tier := ParseTier(string(out)); tier != "" {
		return tier, "ярус " + tier + " по вердикту agentctl pick"
	}
	return FallbackTier, "вердикт agentctl pick яруса не назвал: ярус " + FallbackTier
}

// harnessView это машинный вид `agentctl harness --json`, нужные поля.
type harnessView struct {
	Harnesses []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Default bool   `json:"default"`
		// Auto это допуск к автоматическим назначениям: печатает agentctl из
		// ключа auto машинного конфига (DK-1453). Старый бинарь поля не знает,
		// и пустой указатель значит допуск подписки по умолчанию, как в
		// политике без настройки.
		Auto   *bool `json:"auto"`
		Models []struct {
			Tier  string `json:"tier"`
			Model string `json:"model"`
			Via   string `json:"via"`
		} `json:"models"`
	} `json:"harnesses"`
}

// admitted это допуск подписки к автоматическим назначениям. Пустой флаг
// значит ответ старого agentctl без ключа auto: допущена подписка по
// умолчанию, как велит политика без настройки.
func admitted(auto *bool, def bool) bool {
	if auto == nil {
		return def
	}
	return *auto
}

// AskLadder называет подписку по умолчанию и её лестницу ярусов. Третья
// строка это причина, когда лестницы нет: утилиты нет, ответ не разобрался,
// включённых подписок нет.
func AskLadder() (string, []Step, string) {
	bin, err := exec.LookPath("agentctl")
	if err != nil {
		return "", nil, "agentctl не нашёлся в PATH"
	}
	out, err := ask("", bin, "harness", "--json")
	if err != nil {
		return "", nil, "agentctl harness --json не ответил: " + err.Error()
	}
	return ParseLadder(out)
}

// AskLadders отдаёт лестницы, допущенные автоматическим назначениям, начиная
// с текущей подписки либо default.
func AskLadders() ([]Ladder, string) {
	bin, err := exec.LookPath("agentctl")
	if err != nil {
		return nil, "agentctl не нашёлся в PATH"
	}
	out, err := ask("", bin, "harness", "--json")
	if err != nil {
		return nil, "agentctl harness --json не ответил: " + err.Error()
	}
	return ParseLadders(out, os.Getenv("DEVKIT_HARNESS"))
}

// ParseLadders оставляет только включённые и допущенные автоматике (ключ auto
// машинного конфига, DK-1453); preferred меняет порядок, но не состав.
func ParseLadders(raw []byte, preferred string) ([]Ladder, string) {
	var v harnessView
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, "ответ agentctl harness --json не разобрался: " + err.Error()
	}
	var out []Ladder
	primary := ""
	enabledSeen := false
	for _, h := range v.Harnesses {
		if !h.Enabled {
			continue
		}
		enabledSeen = true
		if !admitted(h.Auto, h.Default) {
			continue
		}
		ladder := Ladder{Name: h.Name, Via: map[string]string{}}
		for _, m := range h.Models {
			ladder.Steps = append(ladder.Steps, Step{Tier: m.Tier, Model: m.Model})
			ladder.Via[m.Tier] = m.Via
		}
		out = append(out, ladder)
		if h.Default {
			primary = h.Name
		}
	}
	if len(out) == 0 {
		if enabledSeen {
			return nil, "включённые подписки автоматическим назначениям не разрешены (ключ auto машинного конфига)"
		}
		return nil, "включённых подписок agentctl не назвал"
	}
	for _, h := range out {
		if h.Name == preferred {
			primary = preferred
			break
		}
	}
	for i, h := range out {
		if h.Name == primary {
			out = append([]Ladder{h}, append(out[:i], out[i+1:]...)...)
			break
		}
	}
	return out, ""
}

// ParseLadder разбирает ответ `agentctl harness --json`: подписка по
// умолчанию, а без признака первая включённая, как делает экран.
func ParseLadder(raw []byte) (string, []Step, string) {
	var v harnessView
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil, "ответ agentctl harness --json не разобрался: " + err.Error()
	}
	pick := -1
	for i, h := range v.Harnesses {
		if !h.Enabled {
			continue
		}
		if h.Default {
			pick = i
			break
		}
		if pick < 0 {
			pick = i
		}
	}
	if pick < 0 {
		return "", nil, "включённых подписок agentctl не назвал"
	}
	h := v.Harnesses[pick]
	var steps []Step
	for _, m := range h.Models {
		steps = append(steps, Step{Tier: m.Tier, Model: m.Model})
	}
	return h.Name, steps, ""
}

// PermsRel это перечень прав машинного контура внутри чекаута devkit.
const PermsRel = "tools/devkitctl/perms.py"

// Perms это предполётная проверка прав, тот же барьер, каким виток цели закрыт
// от старта без прав. Прогон идёт в окне без человека, одобрить запрос
// разрешения в нём некому, и сессия без прав вставала на первом же вызове
// (DK-739). Пусто значит «поднимать можно», непустое это слова отказа.
func Perms(devkit, home string) string {
	p := filepath.Join(devkit, filepath.FromSlash(PermsRel))
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "перечня прав машинного контура нет (" + p + "): проверить их нечем, а без них сессия" +
			" без человека встаёт на первом же запросе разрешения"
	}
	ctx, cancel := context.WithTimeout(context.Background(), askWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", p)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return msg
	}
	return "перечень прав " + p + " не ответил: " + err.Error()
}

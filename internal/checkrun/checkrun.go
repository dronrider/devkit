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
// Независимость проверяющего держит ступень яруса. Ярус даёт вердикт
// `agentctl pick --role review`, у роли ревью пол base, а разработку задач
// ценой M ведёт тот же base. Когда модель яруса совпала с моделью разработки,
// ярус берётся ступенью выше по лестнице подписки. Правило «сценарий
// прогоняет не автор правки» и ворота taskctl close сравнивают имена моделей,
// и ступень держит их без правки. Отказ остаётся одному случаю: выше по
// лестнице модели, отличной от автора, нет.
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
// internal/stage, тот же код, которым ворота закрытия ловят прогон под именем
// автора правки.
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
// исполнителя разработки едет в заказе потому, что ворота закрытия сверяют его
// с прогонявшим и на совпадении отказывают: сказать это до прогона дешевле,
// чем получить отказ после.
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
	if dev != "" {
		out += " Разработку вёл " + dev + ", ему прогон не отдавай: ворота закрытия сверяют имена."
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

// Choice это исход выбора модели проверяющего. Model пуста, когда лестница
// яруса не назвала: зовущий решает сам, поднимать ли тогда вовсе.
type Choice struct {
	Tier, Model string
	// From это ярус вердикта, когда модель взята ступенью выше.
	From    string
	Stepped bool
	// Refusal непуст, когда поднять некем: модель яруса вела разработку, а
	// выше по лестнице другой модели нет.
	Refusal string
}

// Choose разворачивает ярус вердикта в модель по лестнице подписки и держит
// независимость ступенью: модель, совпавшая с моделью разработки, заменяется
// первой ступенью выше с другой моделью. Лестница идёт в порядке раскладки
// машины, снизу вверх (agentctl harness --json отдаёт её так же).
func Choose(tier string, ladder []Step, dev string, known bool) Choice {
	at := -1
	for i, s := range ladder {
		if s.Tier == tier {
			at = i
			break
		}
	}
	if at < 0 || ladder[at].Model == "" {
		return Choice{Tier: tier}
	}
	model := ladder[at].Model
	if !known || !strings.EqualFold(model, dev) {
		return Choice{Tier: tier, Model: model}
	}
	for _, s := range ladder[at+1:] {
		if s.Model != "" && !strings.EqualFold(s.Model, dev) {
			return Choice{Tier: s.Tier, Model: s.Model, From: tier, Stepped: true}
		}
	}
	return Choice{Tier: tier, Model: model, Refusal: fmt.Sprintf(
		"ярусом %s он достался бы модели %s, а она вела разработку, и ступени выше с другой моделью"+
			" в раскладке машины нет; сценарий прогоняет не автор правки, поднять другой моделью руками"+
			" либо развести ярусы в раскладке машины", tier, model)}
}

// ParseTier достаёт ярус из машинных строк вердикта `agentctl pick`.
func ParseTier(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "tier:"); ok {
			if tier := strings.TrimSpace(rest); tier != "" {
				return tier
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
	if p.Choice.Stepped {
		line += fmt.Sprintf(", ярус %s поднят ступенью до %s: модель яруса %s вела разработку",
			p.Choice.From, p.Choice.Tier, p.Choice.From)
	}
	switch {
	case p.DevKnown && p.Choice.Model != "":
		line += fmt.Sprintf(", модель %s, разработку вёл %s", p.Choice.Model, p.Dev)
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
	ch := Choose(tier, c.Ladder(), dev, known)
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
		Models  []Step `json:"models"`
	} `json:"harnesses"`
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
	return h.Name, h.Models, ""
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

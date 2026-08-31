package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Харнес это инструмент, в котором живёт сессия агента. Что он умеет,
// объявляет профиль kit/harness/<имя>.toml, что на машине включено и как ярусы
// разворачиваются в модели, говорит ~/.devkit/harness.local, а проектный
// .devkit/harness.local машинный список только сужает. Дизайн в
// docs/lld/DK-033-universal-kit.md, разделы «Профиль харнеса», «Слои
// конфигурации» и «Детект активного харнеса».

const (
	machineConfigName = "harness.local"
	profileDirGroup   = "kit"
	profileDirName    = "harness"
)

// Лестница ярусов. Порядок значим: корректор и спуск на роль ревьювера двигают
// индекс по этому списку, а маппинг активного харнеса разворачивает ярус в
// модель. Ступень добавляется строкой сюда и строкой в таблицу трат, формулы не
// меняются.
const (
	tierMini = "mini"
	tierBase = "base"
	tierPro  = "pro"
	tierMax  = "max"
)

var tierNames = []string{tierMini, tierBase, tierPro, tierMax}

// Что стоит в строке model, когда разворачивать ярус нечем. Прочерк, а не
// пустое место: молчание тут неотличимо от штатной работы. Строка via идёт тем
// же прочерком и по той же причине.
const unmappedModel = "-"

// assignment это назначение яруса: харнес, чьей подпиской платит ступень, и
// модель этого харнеса. Разбор в docs/lld/DK-090-heterogeneous-ladder.md,
// раздел «Назначение яруса в машинном слое».
type assignment struct {
	Harness string
	Model   string
	// Профиля такого харнеса в kit/harness/ нет: ступень не разворачивается, а
	// поломка сказана предупреждением слияния.
	Broken bool
}

// parseAssignment разбирает значение ключа-яруса. Разделитель это первое
// двоеточие: слева каноничное имя харнеса, справа всё остальное как имя модели.
// Второй разделитель отнимал бы у пользователя часть пространства имён, а
// двоеточия в именах моделей чужих инструментов бывают. Значение без двоеточия
// это домашняя ступень, то есть харнес той секции, в которой оно стоит.
func parseAssignment(file, section, tier, value string) (assignment, error) {
	i := strings.Index(value, ":")
	if i < 0 {
		return assignment{Harness: section, Model: value}, nil
	}
	name, model := value[:i], value[i+1:]
	bad := func(why string) (assignment, error) {
		return assignment{}, fmt.Errorf("%s: [%s] %s = %s: %s", file, section, tier, quoteTOML(value), why)
	}
	switch {
	case name == "":
		return bad("до двоеточия пусто, а там стоит имя харнеса назначения; домашняя ступень пишется без двоеточия вовсе")
	case model == "":
		return bad("после двоеточия пусто, а там стоит имя модели")
	}
	return assignment{Harness: name, Model: model}, nil
}

// away отвечает, уехала ли ступень в чужой харнес. Префикс, совпавший с
// активным харнесом, это домашняя ступень: запись избыточна, но законна, и
// отдельного случая не требует, сравнение имён даёт ответ само.
func (a assignment) away(active string) bool {
	return !a.Broken && a.Harness != "" && a.Harness != active
}

// shown это значение яруса тем же синтаксисом, каким оно пишется в машинном
// конфиге: у домашней ступени голая модель, у уехавшей харнес с моделью.
func (a assignment) shown(active string) string {
	switch {
	case a.Broken || a.Model == "":
		return unmappedModel
	case a.away(active):
		return a.Harness + ":" + a.Model
	default:
		return a.Model
	}
}

type keySpec struct{ Key, Kind string }

type sectionSpec struct {
	Name string
	Keys []keySpec
}

// Свод профиля: пять обязательных секций и известные ключи каждой. Порядок
// фиксирован, по нему идут и проверки типов, и сообщения, а они входят в
// контракт фикстур.
var profileSchema = []sectionSpec{
	{"detect", []keySpec{{"env", tomlStr}, {"value", tomlStr}, {"bin", tomlStr}}},
	{"rules", []keySpec{{"mode", tomlStr}, {"file", tomlStr}, {"import_line", tomlStr},
		{"config", tomlStr}, {"dir", tomlStr}, {"global_file", tomlStr}}},
	{"delegate", []keySpec{{"mode", tomlStr}, {"command", tomlArr}, {"agents_dir", tomlStr},
		{"agents_format", tomlStr}, {"map_mini", tomlStr}, {"map_base", tomlStr},
		{"map_pro", tomlStr}, {"map_max", tomlStr}}},
	{"hooks", []keySpec{{"protocol", tomlStr}, {"config", tomlStr}, {"events", tomlArr},
		{"memory_index", tomlStr}}},
	{"quota", []keySpec{{"snap", tomlStr}, {"script", tomlStr}, {"buckets", tomlArr},
		{"required", tomlStr}, {"spend_mini", tomlArr}, {"spend_base", tomlArr},
		{"spend_pro", tomlArr}, {"spend_max", tomlArr}, {"budget_based", tomlBool}}},
}

// Шестая секция, ось скиллов (docs/lld/DK-100-context-tree.md, раздел «Харнесы
// без скиллов»). Обязательной её сделать нельзя: профиль, написанный до этой
// оси, обязан читаться и дальше, а отсутствие секции значит сегодняшнюю полную
// вклейку правил.
var optionalSchema = []sectionSpec{
	{"skills", []keySpec{{"dir", tomlStr}, {"format", tomlStr}, {"discovery", tomlStr}}},
}

var discoveryValues = []string{"auto", "manual"}

var knownEvents = []string{"write", "session-start", "notify", "subagent-done", "turn-done", "turn-failed", "prompt-submit", "tool-done"}

// Свод машинного слоя. Ярусы в секции харнеса обязательны все четыре: сложенные
// в одну модель соседние ярусы пишутся повторением значения, а пропуск ключа
// неотличим от забытого.
var machineRootKeys = []keySpec{{"default", tomlStr}, {"enabled", tomlArr}}

var machineHarnessKeys = []keySpec{{"mini", tomlStr}, {"base", tomlStr}, {"pro", tomlStr},
	{"max", tomlStr}, {"budget", tomlInt}, {"bin", tomlStr}, {"home", tomlStr}, {"env", tomlArr}}

// Плейсхолдер каталога харнеса в значениях env. Тот же, что понимает генератор
// раскладки в путях профиля (tools/devkitctl/rules.py): каталог на машине один,
// и записан он один раз ключом home.
const homeMark = "{home}"

// Приставка, закрытая для имён в env. Своё пространство имён devkit держит за
// собой целиком, а не перечисляет имена поимённо: острый случай тут
// DEVKIT_RUN_DEPTH, молча перебитый ограничитель вложенности разворачивается в
// воронку сессий, то есть в потраченную подписку.
const devkitEnvPrefix = "DEVKIT_"

// envPair это переменная окружения подпроцесса: имя печатается командами,
// значение нет. Массив env общий, завтра туда положат токен, а вывод команд
// уезжает в логи и в контекст агента.
type envPair struct{ Name, Value string }

// expandTilde разворачивает ведущий ~/ в домашний каталог. Пути в машинных
// конфигах devkit пишут с тильдой везде, а буквальный ~/.claude-glm завёл бы
// клиенту каталог с именем ~ в рабочем дереве, причём один раз и навсегда.
func expandTilde(s string) string {
	if s != "~" && !strings.HasPrefix(s, "~/") {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(s, "~"), "/"))
}

// parseHarnessEnv разбирает ключ env секции харнеса: массив строк ИМЯ=значение с
// плейсхолдером {home} в значении. Проверки тут жёсткие все до одной, потому что
// тихо пропущенная переменная это окно, которое молча ушло на дорогую подписку.
// Значение элемента в сообщениях не печатается, элемент называется номером и
// именем: в env кладут и токены, а текст ошибки уезжает в те же логи, что и всё
// остальное.
func parseHarnessEnv(file, section string, items []string, home string) ([]envPair, error) {
	var out []envPair
	for i, item := range items {
		bad := func(what, why string) error {
			return fmt.Errorf("%s: [%s] env, элемент %d%s: %s", file, section, i+1, what, why)
		}
		j := strings.Index(item, "=")
		if j < 0 {
			return nil, bad("", "нет знака =, а пара пишется как ИМЯ=значение (сам элемент не печатаю, в env попадают и токены)")
		}
		name, value := item[:j], item[j+1:]
		switch {
		case name == "":
			return nil, bad("", "слева от = пусто, а там стоит имя переменной")
		case strings.HasPrefix(name, devkitEnvPrefix):
			return nil, bad(" ("+name+")", "приставка "+devkitEnvPrefix+" закрыта, это пространство имён devkit: им ставятся ограничитель вложенности "+
				runDepthEnv+" и харнес сессии, и перебить их снаружи нельзя")
		}
		value = expandTilde(value)
		if strings.Contains(value, homeMark) {
			if home == "" {
				return nil, bad(" ("+name+")", "в значении стоит "+homeMark+", а каталога в машинном слое нет: вписать home в секцию ["+
					section+"] файла "+file+" (это каталог конфигурации, которым поднимается сам инструмент)")
			}
			value = strings.ReplaceAll(value, homeMark, home)
		}
		out = append(out, envPair{Name: name, Value: value})
	}
	return out, nil
}

// envNames это имена переменных для вывода команд. Значения не отдаёт никто:
// печатать их некому по решению дизайна.
func envNames(pairs []envPair) []string {
	var names []string
	for _, p := range pairs {
		names = append(names, p.Name)
	}
	return names
}

func specIndex(list []keySpec) map[string]string {
	m := map[string]string{}
	for _, s := range list {
		m[s.Key] = s.Kind
	}
	return m
}

func inList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// checkTypes проверяет типы известных ключей таблицы в порядке свода.
func checkTypes(name string, t *tomlTable, keys []keySpec) error {
	for _, spec := range keys {
		v, ok := t.get(spec.Key)
		if !ok || v.Kind == spec.Kind {
			continue
		}
		return fmt.Errorf("%s: [%s] %s: жду %s, вижу %s", name, t.Name, spec.Key,
			tomlKindNames[spec.Kind], tomlKindNames[v.Kind])
	}
	return nil
}

// unknownWarns собирает незнакомые секции и ключи в порядке появления в файле.
// Это предупреждение, а не ошибка: старый бинарь обязан переживать профиль из
// более свежего devkit, иначе обновление devkit ломало бы работу до пересборки.
func unknownWarns(d *tomlDoc, known map[string][]keySpec) []string {
	var warns []string
	for _, sect := range d.Order {
		t := d.Tables[sect]
		keys, ok := known[sect]
		if !ok {
			if sect == "" {
				for _, k := range t.Keys {
					warns = append(warns, fmt.Sprintf("%s: незнакомый ключ %s на верхнем уровне, пропущен", d.Name, k))
				}
				continue
			}
			warns = append(warns, fmt.Sprintf("%s: незнакомая секция [%s], пропущена", d.Name, sect))
			continue
		}
		idx := specIndex(keys)
		for _, k := range t.Keys {
			if _, ok := idx[k]; !ok {
				warns = append(warns, fmt.Sprintf("%s: [%s] незнакомый ключ %s, пропущен", d.Name, sect, k))
			}
		}
	}
	return warns
}

func requireKey(name string, t *tomlTable, key, why string) error {
	if _, ok := t.get(key); ok {
		return nil
	}
	return fmt.Errorf("%s: [%s] нет ключа %s (%s)", name, t.Name, key, why)
}

func requireOneOf(name string, t *tomlTable, key string, allowed []string) error {
	v := t.str(key)
	if inList(allowed, v) {
		return nil
	}
	return fmt.Errorf("%s: [%s] %s = %s, допустимы %s", name, t.Name, key, quoteTOML(v),
		strings.Join(allowed, ", "))
}

// validateProfile гоняет свод из раздела «Валидация»: пять секций на месте,
// обязательные ключи при выбранных режимах, значения режимов из перечня,
// spend_* и required ссылаются только на бакеты из buckets. Нарушение это
// жёсткая ошибка с именем файла и ключа: битый профиль хуже отсутствующего,
// потому что молча выключил бы ось.
func validateProfile(d *tomlDoc) ([]string, error) {
	known := map[string][]keySpec{}
	for _, s := range append(append([]sectionSpec{}, profileSchema...), optionalSchema...) {
		known[s.Name] = s.Keys
	}
	for _, s := range profileSchema {
		if !d.has(s.Name) {
			return nil, fmt.Errorf("%s: нет секции [%s], в профиле обязаны быть все пять (detect, rules, delegate, hooks, quota)", d.Name, s.Name)
		}
	}
	for _, s := range append(append([]sectionSpec{}, profileSchema...), optionalSchema...) {
		if !d.has(s.Name) {
			continue
		}
		if err := checkTypes(d.Name, d.table(s.Name), s.Keys); err != nil {
			return nil, err
		}
	}
	if err := validateDetect(d); err != nil {
		return nil, err
	}
	if err := validateRules(d); err != nil {
		return nil, err
	}
	if err := validateDelegate(d); err != nil {
		return nil, err
	}
	if err := validateHooks(d); err != nil {
		return nil, err
	}
	if err := validateQuota(d); err != nil {
		return nil, err
	}
	if err := validateSkills(d); err != nil {
		return nil, err
	}
	warns := unknownWarns(d, known)
	// Незнакомое событие тоже предупреждение: оси, которой этот бинарь не
	// знает, у него всё равно нет, а отказывать из-за неё нечестно.
	for _, e := range d.table("hooks").arr("events") {
		if !inList(knownEvents, e) {
			warns = append(warns, fmt.Sprintf("%s: [hooks] events: незнакомое событие %s, пропущено", d.Name, quoteTOML(e)))
		}
	}
	return warns, nil
}

func validateDetect(d *tomlDoc) error {
	t := d.table("detect")
	if t.empty() {
		return nil
	}
	for _, key := range []string{"env", "bin"} {
		if err := requireKey(d.Name, t, key, "секция непуста"); err != nil {
			return err
		}
	}
	return nil
}

func validateRules(d *tomlDoc) error {
	t := d.table("rules")
	if t.empty() {
		return fmt.Errorf("%s: секция [rules] пуста, а правила обязаны доезжать до каждого харнеса", d.Name)
	}
	if err := requireKey(d.Name, t, "mode", "обязателен всегда"); err != nil {
		return err
	}
	if err := requireOneOf(d.Name, t, "mode", []string{"import", "embed", "render"}); err != nil {
		return err
	}
	switch t.str("mode") {
	case "import":
		for _, key := range []string{"file", "import_line"} {
			if err := requireKey(d.Name, t, key, `при mode = "import"`); err != nil {
				return err
			}
		}
	case "render":
		if err := requireKey(d.Name, t, "dir", `при mode = "render"`); err != nil {
			return err
		}
	}
	return nil
}

func validateDelegate(d *tomlDoc) error {
	t := d.table("delegate")
	if err := requireKey(d.Name, t, "mode", "обязателен всегда, у инструмента без делегирования это none"); err != nil {
		return err
	}
	if err := requireOneOf(d.Name, t, "mode", []string{"native", "cli", "none"}); err != nil {
		return err
	}
	if t.str("mode") == "cli" {
		if err := requireKey(d.Name, t, "command", `при mode = "cli"`); err != nil {
			return err
		}
	}
	return nil
}

func validateHooks(d *tomlDoc) error {
	t := d.table("hooks")
	if t.empty() {
		return nil
	}
	return requireKey(d.Name, t, "protocol", "секция непуста")
}

func validateQuota(d *tomlDoc) error {
	t := d.table("quota")
	if t.empty() {
		return nil
	}
	if err := requireKey(d.Name, t, "snap", "секция непуста"); err != nil {
		return err
	}
	if err := requireOneOf(d.Name, t, "snap", []string{"usage-pane", "script"}); err != nil {
		return err
	}
	if t.str("snap") == "script" {
		if err := requireKey(d.Name, t, "script", `при snap = "script"`); err != nil {
			return err
		}
	}
	for _, key := range []string{"buckets", "required"} {
		if err := requireKey(d.Name, t, key, "секция непуста"); err != nil {
			return err
		}
	}
	spend := []string{}
	for _, tier := range tierNames {
		spend = append(spend, "spend_"+tier)
	}
	for _, key := range spend {
		if err := requireKey(d.Name, t, key, "секция непуста, ярусы объявляются все четыре"); err != nil {
			return err
		}
	}
	buckets := t.arr("buckets")
	// Окно бакета берётся из префикса имени, и имя без известного префикса
	// молча считалось бы недельным: у месячного бюджета pace тогда врёт всемеро.
	for _, b := range buckets {
		if bucketPrefix(b) == "" {
			var prefixes []string
			for _, w := range bucketWindows {
				prefixes = append(prefixes, w.Prefix)
			}
			return fmt.Errorf("%s: [quota] buckets: имя бакета %s без известного префикса, из него берётся окно расчёта; годятся %s",
				d.Name, quoteTOML(b), strings.Join(prefixes, ", "))
		}
	}
	if req := t.str("required"); !inList(buckets, req) {
		return fmt.Errorf("%s: [quota] required = %s, такого бакета нет в buckets", d.Name, quoteTOML(req))
	}
	for _, key := range spend {
		for _, b := range t.arr(key) {
			if !inList(buckets, b) {
				return fmt.Errorf("%s: [quota] %s ссылается на бакет %s, которого нет в buckets", d.Name, key, quoteTOML(b))
			}
		}
	}
	return nil
}

// validateSkills проверяет ось скиллов. Секции нет вовсе, значит про ось ещё не
// разбирались, и правила поедут полным текстом; пустая секция это разобранное
// «скиллов у инструмента нет». Исход у обоих случаев один, а смысл разный, и
// различает их генератор правил.
func validateSkills(d *tomlDoc) error {
	t := d.table("skills")
	if t.empty() {
		return nil
	}
	if err := requireKey(d.Name, t, "discovery", "секция непуста, иначе непонятно, доезжают скиллы сами или по указателю"); err != nil {
		return err
	}
	if err := requireOneOf(d.Name, t, "discovery", discoveryValues); err != nil {
		return err
	}
	if t.str("discovery") == "auto" {
		return requireKey(d.Name, t, "dir", `при discovery = "auto" скиллы надо куда-то раскладывать`)
	}
	return nil
}

// profileReport это общий с devkitctl отчёт по одному файлу: либо отказ, либо
// канонический дамп с предупреждениями. Им сверяются фикстуры kit/harness/testdata.
func profileReport(name, text string, validate bool) string {
	d, err := parseTOML(name, text)
	if err != nil {
		return "error: " + err.Error() + "\n"
	}
	var warns []string
	if validate {
		warns, err = validateProfile(d)
		if err != nil {
			return "error: " + err.Error() + "\n"
		}
	}
	out := d.dump()
	for _, w := range warns {
		out += "warn: " + w + "\n"
	}
	return out
}

type profile struct {
	Name  string
	Path  string
	Doc   *tomlDoc
	Warns []string
}

func (p *profile) section(name string) *tomlTable { return p.Doc.table(name) }

// loadProfile читает и тут же валидирует: отдельной команды проверки нет,
// профиль проверяется на каждой загрузке.
func loadProfile(dir, name string) (*profile, error) {
	path := filepath.Join(dir, name+".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("нет профиля харнеса %s", path)
		}
		return nil, err
	}
	d, err := parseTOML(filepath.Base(path), string(data))
	if err != nil {
		return nil, err
	}
	warns, err := validateProfile(d)
	if err != nil {
		return nil, err
	}
	return &profile{Name: name, Path: path, Doc: d, Warns: warns}, nil
}

func listProfiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	sort.Strings(names)
	return names, nil
}

// harnessDir ищет директорию профилей. Бинарь лежит в ~/go/bin и про свой
// devkit ничего не знает, поэтому кандидатов несколько: явная переменная,
// корень проекта (в самом devkit профили лежат в kit/), соседний с проектом
// клон и путь из README.
func harnessDir(start string) (string, error) {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, filepath.Join(v, profileDirGroup, profileDirName))
	}
	if root, err := findRoot(start); err == nil {
		cands = append(cands, filepath.Join(root, profileDirGroup, profileDirName),
			filepath.Join(filepath.Dir(root), "devkit", profileDirGroup, profileDirName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "projects", "devkit", profileDirGroup, profileDirName))
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("не нашёл директорию профилей харнесов, искал: %s; указать: DEVKIT_HOME=<путь к devkit>",
		strings.Join(cands, ", "))
}

func machineConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".devkit", machineConfigName)
}

func projectConfigPath(start string) string {
	root, err := findRoot(start)
	if err != nil {
		return ""
	}
	return filepath.Join(root, ".devkit", machineConfigName)
}

// setup это машинная часть про один харнес: маппинг ярусов в модели плюс
// необязательные бюджет и путь к бинарю вне PATH.
type setup struct {
	Map map[string]assignment
	// Маппинг взят из map_* профиля, а не с машины: так выходит, пока мастер
	// настройки не прогнан, и в выводе это надо различать.
	Suggested bool
	// Секция харнеса в машинном конфиге есть. Без ярусов она законна (харнес
	// назван обвязкой или назначением соседа), и хинт про ненастроенную
	// лестницу зовёт дописать ярусы, а не вписать стоящую секцию.
	Section bool
	Budget  int
	Bin     string
	// Каталог конфигурации, которым поднимается сам инструмент, и переменные,
	// которые получает подпроцесс делегирования. Разбор в
	// docs/lld/DK-090-heterogeneous-ladder.md, раздел «Каталог конфигурации и
	// окружение подпроцесса».
	Home string
	Env  []envPair
}

func (s *setup) mapped() bool { return s != nil && len(s.Map) == len(tierNames) }

// layers это результат слияния трёх слоёв.
type layers struct {
	Dir     string
	Enabled []string
	Default string
	// Чем объясняется default в резолве: машинным конфигом или его отсутствием.
	DefaultHow string
	Setup      map[string]*setup
	Profiles   map[string]*profile
	Source     string // откуда взялся список включённых
	// ExecRotateTokens это порог ротации исполнителя-субагента у
	// чата-диспетчера: верхнеуровневый ключ exec_rotate_tokens машинного
	// конфига. Ноль значит ключ не задан или бит: умолчание называет
	// потребитель (дашборд), слой тут только транспорт.
	ExecRotateTokens int
	Warns            []string
}

func readConfig(path string) (*tomlDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseTOML(path, string(data))
}

// mergeLayers складывает слои сверху вниз: профиль объявляет возможности,
// машинный конфиг включает и маппит, проектный сужает. Машинного конфига нет
// вовсе, значит включён claude-code с маппингом-предложением из его профиля:
// это сегодняшнее поведение один в один, до первого прогона мастера настройки.
func mergeLayers(dir, machinePath, projectPath string) (*layers, error) {
	l := &layers{Dir: dir, Setup: map[string]*setup{}, Profiles: map[string]*profile{}}
	mdoc, err := readConfig(machinePath)
	if err != nil {
		return nil, err
	}
	if mdoc == nil {
		l.Source = fmt.Sprintf("машинного конфига %s нет, умолчание", machinePath)
		l.Enabled = []string{"claude-code"}
		l.Default, l.DefaultHow = "claude-code", "умолчание, машинного конфига нет"
	} else {
		l.Source = machinePath
		if err := checkTypes(machinePath, mdoc.table(""), machineRootKeys); err != nil {
			return nil, err
		}
		l.Default, l.DefaultHow = mdoc.table("").str("default"), "default машинного конфига"
		l.Enabled = append(l.Enabled, mdoc.table("").arr("enabled")...)
		// Ключ читается мягко, а не через machineRootKeys: битое значение не
		// должно ронять раскладку целиком, диспетчер обойдётся умолчанием, а
		// причина уезжает предупреждением.
		if v, ok := mdoc.table("").get("exec_rotate_tokens"); ok {
			if v.Kind == tomlInt && v.Int > 0 {
				l.ExecRotateTokens = v.Int
			} else {
				l.Warns = append(l.Warns, fmt.Sprintf("%s: ключ exec_rotate_tokens ждёт целое больше нуля, значение пропущено", machinePath))
			}
		}
		for _, name := range mdoc.Order {
			if name == "" {
				continue
			}
			t := mdoc.table(name)
			if err := checkTypes(machinePath, t, machineHarnessKeys); err != nil {
				return nil, err
			}
			s := &setup{Map: map[string]assignment{}, Section: true}
			var missing []string
			for _, tier := range tierNames {
				if _, ok := t.get(tier); !ok {
					missing = append(missing, tier)
					continue
				}
				a, err := parseAssignment(machinePath, name, tier, t.str(tier))
				if err != nil {
					return nil, err
				}
				s.Map[tier] = a
			}
			// Ярусов либо все четыре, либо ни одного: секция без ярусов это
			// харнес, который в этом конфиге присутствует только назначением
			// соседей и своей машинной обвязкой, а лестницу такому активному
			// разворачивает предложение из профиля (ниже, DK-189). Пропуск одного
			// яруса из четырёх неотличим от забытого, и это ошибка.
			if len(missing) > 0 && len(missing) < len(tierNames) {
				return nil, fmt.Errorf("%s: [%s] нет ключа %s (ярусы задаются все четыре либо ни одного, сложенные соседние пишутся повторением модели)",
					machinePath, name, missing[0])
			}
			if v, ok := t.get("budget"); ok {
				s.Budget = v.Int
			}
			s.Bin = t.str("bin")
			s.Home = expandTilde(t.str("home"))
			env, err := parseHarnessEnv(machinePath, name, t.arr("env"), s.Home)
			if err != nil {
				return nil, err
			}
			s.Env = env
			l.Setup[name] = s
		}
	}
	// Имя без профиля в kit/harness/ выбрасывается: включать нечего, а молчать
	// нельзя, иначе опечатка в enabled выглядела бы как выключенный инструмент.
	var enabled []string
	for _, name := range l.Enabled {
		p, err := loadProfile(dir, name)
		if err != nil {
			if strings.HasPrefix(err.Error(), "нет профиля") {
				l.Warns = append(l.Warns, fmt.Sprintf("%s включён, а профиля %s нет, пропущен", name, filepath.Join(dir, name+".toml")))
				continue
			}
			return nil, err
		}
		l.Profiles[name] = p
		l.Warns = append(l.Warns, p.Warns...)
		enabled = append(enabled, name)
	}
	l.Enabled = enabled
	// Маппинг-предложение из профиля: ярусы разворачиваются его же `map_*`, и
	// вердикт не остаётся без модели. Случаев два. Машинного конфига нет вовсе,
	// тогда включён один claude-code и мастер настройки не прогнан ни разу.
	// Либо секция харнеса стоит одной машинной обвязкой, без ярусов, что законно
	// по DK-177: раскладку машина назвала, лестницу забыла, и предложение тут
	// уместно ровно так же. Имя, которое только в enabled, предложения не
	// получает: секции нет, машина про этот харнес не сказала ничего, и прочерк
	// в вердикте это честный сигнал прогнать мастер.
	if mdoc == nil {
		suggestMap(l, "claude-code")
	} else {
		for _, name := range l.Enabled {
			if s := l.Setup[name]; s != nil && len(s.Map) == 0 {
				suggestMap(l, name)
			}
		}
	}
	if err := checkAssignments(l, machinePath); err != nil {
		return nil, err
	}
	if err := narrowByProject(l, projectPath); err != nil {
		return nil, err
	}
	return l, nil
}

// suggestMap разворачивает ярусы харнеса его собственным `map_*` из профиля и
// метит их предложением. Машинную обвязку секции предложение не трогает: путь к
// бинарю, каталог и окружение остаются машинными, предложением едет только
// лестница.
func suggestMap(l *layers, name string) {
	p := l.Profiles[name]
	if p == nil {
		return
	}
	m := map[string]assignment{}
	for _, tier := range tierNames {
		if v := p.section("delegate").str("map_" + tier); v != "" {
			m[tier] = assignment{Harness: name, Model: v}
		}
	}
	if len(m) == 0 {
		return
	}
	s := l.Setup[name]
	if s == nil {
		s = &setup{}
		l.Setup[name] = s
	}
	s.Map, s.Suggested = m, true
}

// unmapHint это хвост хинта про ненастроенную лестницу: чинится она по-разному,
// смотря стоит ли секция харнеса в машинном конфиге.
func unmapHint(l *layers, name string) string {
	s := l.Setup[name]
	if s == nil || !s.Section {
		return fmt.Sprintf("вписать секцию [%s] в %s", name, machineConfigPath())
	}
	var missing []string
	for _, tier := range tierNames {
		if _, ok := s.Map[tier]; !ok {
			missing = append(missing, tier)
		}
	}
	return fmt.Sprintf("в секции [%s] файла %s нет ярусов: %s", name, machineConfigPath(), strings.Join(missing, ", "))
}

// checkAssignments дочитывает профили, названные секцией машинного слоя или
// назначением яруса, и метит битые назначения. Грузить их приходится сверх
// enabled: у уехавшей ступени иначе неоткуда взять ни [delegate], ни [quota], а
// про имя без профиля devkit не узнал бы вовсе. Присутствия в enabled формат от
// назначения не требует, это законная, хотя и неполная раскладка, и говорит о
// ней команда harness.
func checkAssignments(l *layers, machinePath string) error {
	var names []string
	for name := range l.Setup {
		names = append(names, name)
	}
	sort.Strings(names)
	missing := map[string]bool{}
	// Отвечает, есть ли у имени профиль, и заодно кладёт его в слои.
	have := func(name string) (bool, error) {
		if name == "" || missing[name] {
			return false, nil
		}
		if _, ok := l.Profiles[name]; ok {
			return true, nil
		}
		p, err := loadProfile(l.Dir, name)
		if err != nil {
			if strings.HasPrefix(err.Error(), "нет профиля") {
				missing[name] = true
				return false, nil
			}
			return false, err
		}
		l.Profiles[name] = p
		return true, nil
	}
	for _, name := range names {
		if _, err := have(name); err != nil {
			return err
		}
		s := l.Setup[name]
		for _, tier := range tierNames {
			a, ok := s.Map[tier]
			if !ok {
				continue
			}
			okp, err := have(a.Harness)
			if err != nil {
				return err
			}
			if okp {
				continue
			}
			a.Broken = true
			s.Map[tier] = a
			l.Warns = append(l.Warns, fmt.Sprintf("%s: [%s] %s уезжает в %s, а профиля %s нет: назначение битое, ступень не разворачивается",
				machinePath, name, tier, a.Harness, filepath.Join(l.Dir, a.Harness+".toml")))
		}
	}
	return nil
}

// narrowByProject сужает список проектным слоем. Понимает он только enabled:
// default остаётся машинным, а любой другой ключ это находка doctor.
func narrowByProject(l *layers, path string) error {
	if path == "" {
		return nil
	}
	pdoc, err := readConfig(path)
	if err != nil || pdoc == nil {
		return err
	}
	root := pdoc.table("")
	if err := checkTypes(path, root, []keySpec{{"enabled", tomlArr}}); err != nil {
		return err
	}
	for _, k := range root.Keys {
		if k != "enabled" {
			l.Warns = append(l.Warns, fmt.Sprintf("%s: ключ %s проектному слою не положен, понимается только enabled", path, k))
		}
	}
	for _, name := range pdoc.Order {
		if name != "" {
			l.Warns = append(l.Warns, fmt.Sprintf("%s: секция [%s] проектному слою не положена, маппинг ярусов машинный", path, name))
		}
	}
	if _, ok := root.get("enabled"); !ok {
		return nil
	}
	var kept []string
	for _, name := range root.arr("enabled") {
		if inList(l.Enabled, name) {
			kept = append(kept, name)
			continue
		}
		l.Warns = append(l.Warns, fmt.Sprintf("%s: %s сужением не включить, в машинном слое его нет, пропущен", path, name))
	}
	for _, name := range l.Enabled {
		if !inList(kept, name) {
			l.Warns = append(l.Warns, fmt.Sprintf("%s сужен проектным слоем %s", name, path))
		}
	}
	l.Enabled = kept
	l.Source += " с сужением " + path
	return nil
}

// resolution это ответ на вопрос «какой харнес активен и чем определён».
type resolution struct {
	Name  string
	How   string
	Notes []string
}

// resolveHarness идёт по пяти шагам детекта, первое сработавшее решает:
// явное имя, переменная, детект по окружению, default машинного конфига, а
// иначе не определилось. DEVKIT_HARNESS стоит выше детекта не для красоты:
// подпроцесс наследует окружение родителя, и внутри инструмента, поднятого из
// сессии Claude Code, переменные родителя останутся видимыми рядом с чужими.
func resolveHarness(l *layers, want string, env func(string) string) (*resolution, error) {
	r := &resolution{}
	explicit := func(name, how string) (*resolution, error) {
		if _, err := loadProfile(l.Dir, name); err != nil {
			return nil, err
		}
		r.Name, r.How = name, how
		if !inList(l.Enabled, name) {
			r.Notes = append(r.Notes, fmt.Sprintf("%s задан явно, в списке включённых его нет", name))
		}
		return r, nil
	}
	if want != "" {
		return explicit(want, "флаг --harness")
	}
	if v := strings.TrimSpace(env(harnessEnv)); v != "" {
		return explicit(v, "переменная "+harnessEnv)
	}
	names, err := listProfiles(l.Dir)
	if err != nil {
		return nil, err
	}
	var hits, outside []string
	for _, name := range names {
		p := l.Profiles[name]
		if p == nil {
			// Детект смотрит и профили вне включённого списка: сессия внутри
			// установленного, но не включённого инструмента не должна молча
			// уезжать в default.
			p, err = loadProfile(l.Dir, name)
			if err != nil {
				return nil, err
			}
		}
		t := p.section("detect")
		key := t.str("env")
		if key == "" {
			continue
		}
		got := env(key)
		if got == "" {
			continue
		}
		if need := t.str("value"); need != "" && need != got {
			continue
		}
		if inList(l.Enabled, name) {
			hits = append(hits, name)
		} else {
			outside = append(outside, name)
		}
	}
	for _, name := range outside {
		r.Notes = append(r.Notes, fmt.Sprintf("сессия внутри %s, а он не включён", name))
	}
	switch {
	case len(hits) == 1:
		p := l.Profiles[hits[0]]
		r.Name = hits[0]
		r.How = fmt.Sprintf("детект по переменной %s", p.section("detect").str("env"))
		return r, nil
	case len(hits) > 1:
		// Гадать тут нельзя: переменные родителя видны подпроцессу, и
		// совпадений в гнезде сессий бывает больше одного.
		r.Notes = append(r.Notes, fmt.Sprintf("детект по окружению неоднозначен (%s), идём дальше по шагам", strings.Join(hits, ", ")))
	}
	switch {
	case l.Default == "":
	case inList(l.Enabled, l.Default):
		r.Name, r.How = l.Default, l.DefaultHow
		return r, nil
	default:
		r.Notes = append(r.Notes, fmt.Sprintf("default %s не в списке включённых, пропущен", l.Default))
	}
	return r, nil
}

// tierModels разворачивает ярус в модель активного харнеса. Ярусная половина
// вердикта от инструмента не зависит по построению, поэтому неопределённый,
// ненастроенный или битый контур это не отказ pick, а прочерк в строке model и
// причина хвостом: молчащая ось хуже честного прочерка.
type tierModels struct {
	Map map[string]assignment
	// Активный харнес: относительно него ступень домашняя или уехавшая.
	Active string
	// Режим делегирования по харнесам назначения: им выбирается слово записи в
	// файле задачи, субагент или подпроцесс.
	Delegate map[string]string
	Note     string
}

// assign отдаёт назначение яруса, если разворачивать его есть чем. Битое
// назначение и пустая модель тут равны отсутствию: и то и другое даёт прочерк.
func (m tierModels) assign(tier string) (assignment, bool) {
	a, ok := m.Map[tier]
	if !ok || a.Broken || a.Model == "" {
		return assignment{}, false
	}
	return a, true
}

func (m tierModels) model(tier string) string {
	if a, ok := m.assign(tier); ok {
		return a.Model
	}
	return unmappedModel
}

// via это харнес, чьей подпиской платит ступень. Печатается он всегда, в том
// числе на однородной лестнице: строка, появляющаяся через раз, заставляет
// потребителя читать вывод условно, а «дома» это такой же ответ на вопрос
// «чьей подпиской», как и любой другой.
func (m tierModels) via(tier string) string {
	a, ok := m.assign(tier)
	switch {
	case !ok:
		return unmappedModel
	case a.Harness != "":
		return a.Harness
	default:
		return m.Active
	}
}

func (m tierModels) away(tier string) bool {
	a, ok := m.assign(tier)
	return ok && a.away(m.Active)
}

// named это исполнитель тем же синтаксисом «харнес:модель», каким назначение
// пишется в машинном конфиге: у домашней ступени имя харнеса не повторяется, и
// записи в файлах задач, сделанные до гетерогенной лестницы, читаются одинаково.
func (m tierModels) named(tier string) string {
	a, ok := m.assign(tier)
	if !ok {
		return unmappedModel
	}
	return a.shown(m.Active)
}

// brokenWarn это причина прочерка, когда ярус назначен в харнес без профиля.
// Слияние слоёв про это уже сказало, но предупреждений слияния pick не печатает,
// а молчащий прочерк неотличим от ненастроенной машины.
func (m tierModels) brokenWarn(tier string) string {
	if a, ok := m.Map[tier]; ok && a.Broken {
		return fmt.Sprintf("ярус %s назначен в %s, а профиля такого харнеса нет: ступень не разворачивается; разобраться: agentctl harness", tier, a.Harness)
	}
	return ""
}

// shiftBlock отвечает, почему корректору нельзя двигать вердикт на эту ступень.
// Уехавшая ступень платит чужой подпиской, и мерить её домашним снимком нечем;
// битое назначение не разворачивается вовсе, и сдвиг на него это потерянный
// вердикт, прочерк вместо модели.
func (m tierModels) shiftBlock(tier string) string {
	switch a, ok := m.Map[tier]; {
	case ok && a.Broken:
		return fmt.Sprintf("ступень %s назначена в %s, а профиля такого харнеса нет, сдвига нет", tier, a.Harness)
	case m.away(tier):
		return fmt.Sprintf("ступень %s уезжает в %s, чужую подписку мерить нечем, сдвига нет", tier, m.via(tier))
	}
	return ""
}

// word это слово записи в файле задачи: режим делегирования харнеса-исполнителя
// говорит, субагентом работа уходит или подпроцессом.
func (m tierModels) word(tier string) string {
	if a, ok := m.assign(tier); ok && m.Delegate[a.Harness] == "cli" {
		return "подпроцесс"
	}
	return "субагент"
}

// quotaSpecOf собирает объявление квоты активного харнеса: бакеты, лестницу
// трат и путь снимка. Пустая секция [quota] это штатно выключенный корректор, а
// не поломка: у инструмента, которым остаток снять нечем, снимку взяться
// неоткуда.
func quotaSpecOf(l *layers, name string) *quotaSpec {
	p := l.Profiles[name]
	if p == nil {
		return nil
	}
	t := p.section("quota")
	if t.empty() {
		return nil
	}
	q := &quotaSpec{
		Harness:  name,
		Dir:      l.Dir,
		Snap:     t.str("snap"),
		Script:   t.str("script"),
		Buckets:  t.arr("buckets"),
		Required: t.str("required"),
		Spend:    map[string][]string{},
	}
	for _, tier := range tierNames {
		q.Spend[tier] = t.arr("spend_" + tier)
	}
	if v, ok := t.get("budget_based"); ok {
		q.BudgetBased = v.Bool
	}
	if s := l.Setup[name]; s != nil {
		q.Budget = s.Budget
		q.Home = s.Home
	}
	q.Path, q.From = snapshotSource(name)
	return q
}

// harnessContext это всё, что вердикту нужно от активного харнеса: маппинг
// ярусов в модели и объявление квоты. Резолв тут один на вызов, иначе pick
// читал бы три конфига дважды, а сказать про поломку контура мог бы дважды
// разными словами.
type harnessContext struct {
	Name   string
	Models tierModels
	Quota  *quotaSpec
	// Слои целиком: run поднимает подпроцесс в харнесе назначения, а его профиль
	// и его объявление квоты лежат тут же. Слои не прочитались, значит поле
	// пустое, и это тот же случай, что прочерк в строке model.
	L *layers
	// Почему квоты нет, когда с остальным контуром всё в порядке. Поломка
	// контура объясняется один раз, причиной в Models.Note: следствий у неё два,
	// и повторять их порознь значило бы удваивать хвост вердикта.
	QuotaNote string
}

// want это явное имя харнеса от флага команды: пустая строка значит резолв по
// шагам детекта. Командам quota имя нужно отдельным входом, потому что снимок
// чужой подписки снимается из любой сессии, а активной вторая подписка бывает
// только внутри собственного подпроцесса.
func resolveHarnessContext(start, want string) harnessContext {
	off := func(note string) harnessContext { return harnessContext{Models: tierModels{Note: note}} }
	dir, err := harnessDir(start)
	if err != nil {
		return off(fmt.Sprintf("ярус разворачивать нечем и остаток лимитов брать неоткуда: %v", err))
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return off(fmt.Sprintf("слои харнесов не прочитаны (%v), ярус разворачивать нечем и корректор без профиля выключен; разобраться: agentctl harness", err))
	}
	r, err := resolveHarness(l, want, os.Getenv)
	if err != nil {
		return off(fmt.Sprintf("харнес не определился (%v), ярус разворачивать нечем и корректор без профиля выключен; разобраться: agentctl harness", err))
	}
	if r.Name == "" {
		return off(fmt.Sprintf("харнес не определён, ярус разворачивать нечем и корректор без профиля выключен; включить и смаппить: %s (разобраться: agentctl harness)", machineConfigPath()))
	}
	hc := harnessContext{Name: r.Name, Quota: quotaSpecOf(l, r.Name), L: l}
	if hc.Quota == nil {
		hc.QuotaNote = fmt.Sprintf("у харнеса %s секция [quota] пуста, снимать остаток нечем, вердикт идёт без корректора", r.Name)
	}
	if s := l.Setup[r.Name]; s.mapped() {
		modes := map[string]string{}
		for name, p := range l.Profiles {
			modes[name] = p.section("delegate").str("mode")
		}
		hc.Models = tierModels{Map: s.Map, Active: r.Name, Delegate: modes}
	} else {
		hc.Models = tierModels{Note: fmt.Sprintf("харнес %s не настроен, маппинга ярусов нет; %s", r.Name, unmapHint(l, r.Name))}
	}
	return hc
}

// quotaWhy это причина, по которой квоты нет: либо своя, либо поломка контура.
func (hc harnessContext) quotaWhy() string {
	if hc.QuotaNote != "" {
		return hc.QuotaNote
	}
	return hc.Models.Note
}

// quotaSpecFor это точка входа команд quota: они читают и пишут снимок, и без
// объявления харнеса им работать не с чем, поэтому тут отказ, а не прочерк.
func quotaSpecFor(start, want string) (*quotaSpec, error) {
	hc := resolveHarnessContext(start, want)
	if hc.Quota == nil {
		return nil, fmt.Errorf("%s", hc.quotaWhy())
	}
	return hc.Quota, nil
}

// quotaSpecsAll собирает объявления квоты всех включённых харнесов машины: по
// ним периодический съём (quota refresh --all) освежает каждый снимок, не
// спрашивая, какой харнес активен, у тика сторожка и демона дашборда сессии
// нет. Харнес с пустой секцией [quota] пропускается молча: снимать у него
// нечего по построению, и отказ тут был бы только шумом. Пустой итог это
// отказ: на машине без единой объявленной квоты периодическому съёму делать
// нечего, и молчание выглядело бы работой.
func quotaSpecsAll(start string) ([]*quotaSpec, error) {
	dir, err := harnessDir(start)
	if err != nil {
		return nil, err
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return nil, err
	}
	var specs []*quotaSpec
	for _, name := range l.Enabled {
		if q := quotaSpecOf(l, name); q != nil {
			specs = append(specs, q)
		}
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("ни у одного включённого харнеса не объявлена секция [quota], снимать нечего")
	}
	return specs, nil
}

// harnessJSON это одна подписка машинным видом. Значений окружения тут нет
// никогда, только имена: в env лежат каталог конфигурации, base URL и токен, а
// ответ уезжает по HTTP в дашборд и в логи.
type harnessJSON struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Default bool   `json:"default"`
	// Bin это чем поднимается клиент инструмента: путь из машинного слоя, иначе
	// [detect] bin профиля, иначе первое слово [delegate] command.
	Bin string `json:"bin,omitempty"`
	// Home это каталог хозяйства подписки (ключ home машинного слоя): под ним
	// лежат и настройки клиента, и журналы его разговоров. Секрета в пути нет,
	// а без него читателю не найти транскрипты второй подписки: они уезжают в
	// свой каталог вместе с CLAUDE_CONFIG_DIR, а не в ~/.claude (DK-362).
	Home string   `json:"home,omitempty"`
	Env  []string `json:"env,omitempty"`
	// Models это лестница ярусов харнеса моделями: ярус и то, во что он
	// разворачивается на этой машине. Список нужен всякому, кто даёт человеку
	// выбрать модель руками (панель разговора дашборда): захардкоженный
	// перечень имён там разошёлся бы с лестницей на первой же смене поставщика,
	// а имён харнесов в чужом коде не должно быть вовсе.
	Models []harnessModelJSON `json:"models,omitempty"`
}

// harnessModelJSON это одна ступень лестницы: ярус, модель и признак яруса по
// умолчанию для роли исполнителя.
type harnessModelJSON struct {
	Tier  string `json:"tier"`
	Model string `json:"model"`
}

// harnessesJSON это ответ команды: машинная раскладка подписок целиком.
type harnessesJSON struct {
	Default   string        `json:"default,omitempty"`
	Source    string        `json:"source"`
	Harnesses []harnessJSON `json:"harnesses"`
	// ExecRotateTokens это порог ротации исполнителя-субагента из машинного
	// конфига; нет ключа, нет и поля, умолчание ставит потребитель.
	ExecRotateTokens int      `json:"exec_rotate_tokens,omitempty"`
	Note             string   `json:"note,omitempty"`
	Warns            []string `json:"warns,omitempty"`
}

// clientBin отвечает, чем поднимается клиент харнеса. Порядок тот же, каким
// его ищет сам devkit: путь машинного слоя (инструмент вне PATH), потом
// [detect] bin профиля, потом первое слово шаблона команды делегирования.
func clientBin(p *profile, s *setup) string {
	if s != nil && s.Bin != "" {
		return s.Bin
	}
	if p == nil {
		return ""
	}
	if bin := p.section("detect").str("bin"); bin != "" {
		return bin
	}
	if cmd := p.section("delegate").arr("command"); len(cmd) > 0 {
		return cmd[0]
	}
	return ""
}

// cmdHarnessJSON печатает машинную раскладку подписок для чужой программы.
// Человеческий вывод cmdHarness для этого не годится: он живой текст, меняется
// от любой правки формулировки, и разбирать его регекспом хрупко.
//
// Список идёт полным, включённые и невключённые вперемешку с признаком
// enabled: потребитель решает сам, кого показывать (дашборд оставляет одних
// включённых), а команда, которая молча прячет половину раскладки, отвечала бы
// на вопрос «какие подписки на машине» неправдой. Активный харнес тут не
// считается вовсе: резолв зависит от окружения того, кто зовёт, а признак «по
// умолчанию» приезжает из машинного слоя и одинаков у всех.
func cmdHarnessJSON(start string) (string, error) {
	dir, err := harnessDir(start)
	if err != nil {
		return "", err
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return "", err
	}
	v := harnessesJSON{Default: l.Default, Source: l.Source, Harnesses: []harnessJSON{},
		ExecRotateTokens: l.ExecRotateTokens, Warns: l.Warns}
	var names []string
	for name := range l.Setup {
		names = append(names, name)
	}
	for _, name := range l.Enabled {
		if _, ok := l.Setup[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		h := harnessJSON{Name: name, Enabled: inList(l.Enabled, name), Default: name == l.Default}
		h.Bin = clientBin(l.Profiles[name], l.Setup[name])
		h.Home = l.Setup[name].homeOf()
		h.Env = envNames(l.Setup[name].envOf())
		for _, tier := range tierNames {
			if m := l.Setup[name].mapOf(tier); m != "" {
				h.Models = append(h.Models, harnessModelJSON{Tier: tier, Model: m})
			}
		}
		v.Harnesses = append(v.Harnesses, h)
	}
	if len(l.Enabled) == 0 {
		v.Note = fmt.Sprintf("включённых харнесов нет (%s); включить: вписать enabled в %s", l.Source, machineConfigPath())
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// envOf отдаёт пары окружения секции, переживая её отсутствие: харнес бывает
// включён без единой строки в машинном слое.
// mapOf отдаёт модель яруса из секции харнеса. Пусто значит ярус не назначен:
// лестница бывает неполной, и врать про неё нечем.
func (s *setup) mapOf(tier string) string {
	if s == nil {
		return ""
	}
	a, ok := s.Map[tier]
	if !ok {
		return ""
	}
	return a.Model
}

func (s *setup) envOf() []envPair {
	if s == nil {
		return nil
	}
	return s.Env
}

func (s *setup) homeOf() string {
	if s == nil {
		return ""
	}
	return s.Home
}

// cmdHarness это окно в резолв: любой сдвиг поведения между машинами
// объясняется этой командой, а не чтением трёх конфигов руками.
func cmdHarness(start, want string) (string, error) {
	dir, err := harnessDir(start)
	if err != nil {
		return "", err
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return "", err
	}
	r, err := resolveHarness(l, want, os.Getenv)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if r.Name == "" {
		fmt.Fprintf(&b, "харнес: не определён; ярусная часть вердикта работает, модель разворачивать нечем (model: -)\n")
	} else {
		fmt.Fprintf(&b, "харнес: %s (%s)\n", r.Name, r.How)
	}
	if len(l.Enabled) == 0 {
		fmt.Fprintf(&b, "включены: никого (%s); включить: вписать enabled в %s\n", l.Source, machineConfigPath())
	} else {
		fmt.Fprintf(&b, "включены: %s (%s)\n", strings.Join(l.Enabled, ", "), l.Source)
	}
	if r.Name != "" {
		s := l.Setup[r.Name]
		if s.mapped() {
			var parts []string
			for _, tier := range tierNames {
				parts = append(parts, tier+" = "+s.Map[tier].shown(r.Name))
			}
			src := "машинный конфиг"
			if s.Suggested {
				src = "предложение профиля, машина не настроена"
				if s.Section {
					src = fmt.Sprintf("предложение профиля, в секции [%s] ярусов нет", r.Name)
				}
			}
			fmt.Fprintf(&b, "маппинг ярусов: %s (%s)\n", strings.Join(parts, ", "), src)
			// Уехавшая ступень называется отдельной строкой: подписку перепутать
			// легко, а замечается это по счёту, то есть поздно.
			for _, tier := range tierNames {
				a := s.Map[tier]
				if !a.away(r.Name) {
					continue
				}
				line := fmt.Sprintf("назначение: ярус %s уезжает в %s", tier, a.Harness)
				if !inList(l.Enabled, a.Harness) {
					line += ", а он не в списке включённых: правила, хуки и скиллы devkit ему не раскладываются"
				}
				fmt.Fprintf(&b, "%s\n", line)
			}
		} else {
			fmt.Fprintf(&b, "маппинг ярусов: не задан, харнес ненастроен; %s\n", unmapHint(l, r.Name))
		}
		if p := l.Profiles[r.Name]; p != nil {
			mode := p.section("delegate").str("mode")
			fmt.Fprintf(&b, "делегирование: %s (профиль %s)\n", mode, p.Path)
		}
	}
	// Каталог и окружение называются по всем харнесам машинного слоя, а не по
	// одному активному: уехавшая ступень поднимается каталогом своего харнеса, и
	// без этой строки на вопрос «куда смотреть» ответа нет. Имена переменных
	// печатаются, значения нет: в env кладут и токены.
	var named []string
	for name := range l.Setup {
		named = append(named, name)
	}
	sort.Strings(named)
	for _, name := range named {
		s := l.Setup[name]
		var parts []string
		if s.Home != "" {
			parts = append(parts, "каталог "+s.Home)
		}
		if len(s.Env) > 0 {
			parts = append(parts, "env: "+strings.Join(envNames(s.Env), ", "))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "машинное хозяйство %s: %s\n", name, strings.Join(parts, ", "))
		}
	}
	q := quotaSpecOf(l, r.Name)
	switch {
	case q == nil && r.Name == "":
		b.WriteString("снимок квоты: харнес не определён, чем снимать остаток, объявляет его профиль\n")
	case q == nil:
		fmt.Fprintf(&b, "снимок квоты: у %s секция [quota] пуста, корректор для него выключен\n", r.Name)
	default:
		s, err := q.read()
		switch {
		case err != nil:
			fmt.Fprintf(&b, "снимок квоты: %s не прочитан (%v)\n", q.From, err)
		case s.empty():
			fmt.Fprintf(&b, "снимок квоты: %s, снимка нет; снять: agentctl quota refresh\n", q.From)
		case s.Taken.IsZero():
			fmt.Fprintf(&b, "снимок квоты: %s, момента снятия в нём нет\n", q.From)
		default:
			fmt.Fprintf(&b, "снимок квоты: %s, возраст %s\n", q.From, humanAge(timeNow().Sub(s.Taken)))
		}
		if w := q.legacyWarn(); w != "" {
			fmt.Fprintf(&b, "заметка: %s\n", w)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "заметка: %s\n", n)
	}
	for _, w := range l.Warns {
		fmt.Fprintf(&b, "предупреждение: %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Харнес это инструмент, в котором живёт сессия агента. Что он умеет,
// объявляет профиль harness/<имя>.toml, что на машине включено и как ярусы
// разворачиваются в модели, говорит ~/.devkit/harness.local, а проектный
// .devkit/harness.local машинный список только сужает. Дизайн в
// docs/lld/DK-033-universal-kit.md, разделы «Профиль харнеса», «Слои
// конфигурации» и «Детект активного харнеса».

const (
	machineConfigName = "harness.local"
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
// пустое место: молчание тут неотличимо от штатной работы.
const unmappedModel = "-"

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

var knownEvents = []string{"write", "session-start", "notify", "subagent-done", "turn-done", "prompt-submit"}

// Свод машинного слоя. Ярусы в секции харнеса обязательны все четыре: сложенные
// в одну модель соседние ярусы пишутся повторением значения, а пропуск ключа
// неотличим от забытого.
var machineRootKeys = []keySpec{{"default", tomlStr}, {"enabled", tomlArr}}

var machineHarnessKeys = []keySpec{{"mini", tomlStr}, {"base", tomlStr}, {"pro", tomlStr},
	{"max", tomlStr}, {"budget", tomlInt}, {"bin", tomlStr}}

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
// канонический дамп с предупреждениями. Им сверяются фикстуры harness/testdata.
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
// корень проекта (в самом devkit профили лежат прямо там), соседний с проектом
// клон и путь из README.
func harnessDir(start string) (string, error) {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, filepath.Join(v, profileDirName))
	}
	if root, err := findRoot(start); err == nil {
		cands = append(cands, filepath.Join(root, profileDirName),
			filepath.Join(filepath.Dir(root), "devkit", profileDirName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "projects", "devkit", profileDirName))
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
	Map map[string]string
	// Маппинг взят из map_* профиля, а не с машины: так выходит, пока мастер
	// настройки не прогнан, и в выводе это надо различать.
	Suggested bool
	Budget    int
	Bin       string
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
	Warns      []string
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
		for _, name := range mdoc.Order {
			if name == "" {
				continue
			}
			t := mdoc.table(name)
			if err := checkTypes(machinePath, t, machineHarnessKeys); err != nil {
				return nil, err
			}
			s := &setup{Map: map[string]string{}}
			for _, tier := range tierNames {
				if _, ok := t.get(tier); !ok {
					return nil, fmt.Errorf("%s: [%s] нет ключа %s (ярусы задаются все четыре, сложенные соседние пишутся повторением модели)",
						machinePath, name, tier)
				}
				s.Map[tier] = t.str(tier)
			}
			if v, ok := t.get("budget"); ok {
				s.Budget = v.Int
			}
			s.Bin = t.str("bin")
			l.Setup[name] = s
		}
	}
	// Имя без профиля в harness/ выбрасывается: включать нечего, а молчать
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
	if mdoc == nil {
		// Маппинг-предложение из профиля: до прогона мастера ярусы всё равно
		// разворачиваются, и вердикт не остаётся без модели.
		if p := l.Profiles["claude-code"]; p != nil {
			s := &setup{Map: map[string]string{}, Suggested: true}
			for _, tier := range tierNames {
				if v := p.section("delegate").str("map_" + tier); v != "" {
					s.Map[tier] = v
				}
			}
			l.Setup["claude-code"] = s
		}
	}
	if err := narrowByProject(l, projectPath); err != nil {
		return nil, err
	}
	return l, nil
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
	if v := strings.TrimSpace(env("DEVKIT_HARNESS")); v != "" {
		return explicit(v, "переменная DEVKIT_HARNESS")
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
	Map  map[string]string
	Note string
}

func (m tierModels) model(tier string) string {
	if v := m.Map[tier]; v != "" {
		return v
	}
	return unmappedModel
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
	// Почему квоты нет, когда с остальным контуром всё в порядке. Поломка
	// контура объясняется один раз, причиной в Models.Note: следствий у неё два,
	// и повторять их порознь значило бы удваивать хвост вердикта.
	QuotaNote string
}

func resolveHarnessContext(start string) harnessContext {
	off := func(note string) harnessContext { return harnessContext{Models: tierModels{Note: note}} }
	dir, err := harnessDir(start)
	if err != nil {
		return off(fmt.Sprintf("ярус разворачивать нечем и остаток лимитов брать неоткуда: %v", err))
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return off(fmt.Sprintf("слои харнесов не прочитаны (%v), ярус разворачивать нечем и корректор без профиля выключен; разобраться: agentctl harness", err))
	}
	r, err := resolveHarness(l, "", os.Getenv)
	if err != nil {
		return off(fmt.Sprintf("харнес не определился (%v), ярус разворачивать нечем и корректор без профиля выключен; разобраться: agentctl harness", err))
	}
	if r.Name == "" {
		return off(fmt.Sprintf("харнес не определён, ярус разворачивать нечем и корректор без профиля выключен; включить и смаппить: %s (разобраться: agentctl harness)", machineConfigPath()))
	}
	hc := harnessContext{Name: r.Name, Quota: quotaSpecOf(l, r.Name)}
	if hc.Quota == nil {
		hc.QuotaNote = fmt.Sprintf("у харнеса %s секция [quota] пуста, снимать остаток нечем, вердикт идёт без корректора", r.Name)
	}
	if s := l.Setup[r.Name]; s.mapped() {
		hc.Models = tierModels{Map: s.Map}
	} else {
		hc.Models = tierModels{Note: fmt.Sprintf("харнес %s не настроен, маппинга ярусов нет; вписать секцию [%s] в %s", r.Name, r.Name, machineConfigPath())}
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
func quotaSpecFor(start string) (*quotaSpec, error) {
	hc := resolveHarnessContext(start)
	if hc.Quota == nil {
		return nil, fmt.Errorf("%s", hc.quotaWhy())
	}
	return hc.Quota, nil
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
				parts = append(parts, tier+" = "+s.Map[tier])
			}
			src := "машинный конфиг"
			if s.Suggested {
				src = "предложение профиля, машина не настроена"
			}
			fmt.Fprintf(&b, "маппинг ярусов: %s (%s)\n", strings.Join(parts, ", "), src)
		} else {
			fmt.Fprintf(&b, "маппинг ярусов: не задан, харнес ненастроен; вписать секцию [%s] в %s\n", r.Name, machineConfigPath())
		}
		if p := l.Profiles[r.Name]; p != nil {
			mode := p.section("delegate").str("mode")
			fmt.Fprintf(&b, "делегирование: %s (профиль %s)\n", mode, p.Path)
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

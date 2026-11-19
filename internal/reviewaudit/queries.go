package reviewaudit

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

const queryRule = "common-architecture:ARCH-018"

type QueryCalls struct {
	Paths     []QueryPath `json:"paths"`
	KeysParam string      `json:"keys_param,omitempty"`
	One       *int        `json:"one"`
	Two       *int        `json:"two,omitempty"`
	Repeat    *int        `json:"repeat,omitempty"`
	Probe     *QueryProbe `json:"probe,omitempty"`
}

type QueryPath struct {
	Steps    []QueryStep `json:"steps"`
	DriverAt string      `json:"driver_at"`
	LoopAt   string      `json:"loop_at,omitempty"`
}

type QueryStep struct {
	At         string `json:"at"`
	To         string `json:"to"`
	ReceiverAt string `json:"receiver_at,omitempty"`
}

type QueryProbe struct {
	Command string `json:"command,omitempty"`
	Counter string `json:"counter,omitempty"`
	File    string `json:"file,omitempty"`
}

type queryProbeLog struct {
	CodeHash string          `json:"code_hash"`
	Object   string          `json:"object"`
	Command  string          `json:"command"`
	Runs     []queryProbeRun `json:"runs"`
}

type queryProbeRun struct {
	Case  string   `json:"case"`
	IDs   []string `json:"ids"`
	Calls []string `json:"calls"`
}

type QueryInputs struct {
	One         []string           `json:"one"`
	Two         []string           `json:"two,omitempty"`
	Repeat      []string           `json:"repeat,omitempty"`
	Locations   []string           `json:"locations"`
	TwoLimit    string             `json:"two_limit,omitempty"`
	TwoLimits   []QueryInputLimit  `json:"two_limits,omitempty"`
	TwoScenario *QueryTwoScenario  `json:"two_scenario,omitempty"`
	Entries     []QueryEntryReview `json:"entries,omitempty"`
	Gap         *QueryInputGap     `json:"gap,omitempty"`
}

type QueryTwoScenario struct {
	Entry      string            `json:"entry"`
	Parameters map[string]string `json:"parameters"`
	Validation ReviewFact        `json:"validation"`
	Selection  ReviewFact        `json:"selection"`
	Adapter    ReviewFact        `json:"adapter"`
	IDs        []string          `json:"ids"`
}

type QueryInputLimit struct {
	Entry     string     `json:"entry"`
	Condition ReviewFact `json:"condition"`
}

type QueryEntryReview struct {
	Entry            string `json:"entry"`
	Result           string `json:"result"`
	CallAt           string `json:"call_at"`
	ValidationAt     string `json:"validation_at,omitempty"`
	ValidationCallAt string `json:"validation_call_at,omitempty"`
	Validator        string `json:"validator,omitempty"`
	Observation      string `json:"observation"`
}

type QueryInputGap struct {
	Kind     string `json:"kind"`
	LastAt   string `json:"last_at"`
	Question string `json:"question"`
}

func checkQueryEvidence(root, path, output, auditPath string, report Report) error {
	for _, rule := range report.Rules {
		if rule.ID != queryRule || len(rule.Objects) == 0 {
			continue
		}
		files, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		defer files.Close()
		source := &evidenceSource{root: root, reportPath: path, output: output, base: report.BaseSHA, files: files, cache: make(map[string][]string)}
		return checkQueryRule(rule, source, auditPath, report.CodeHash)
	}
	return nil
}

func checkQueryRule(rule Rule, source *evidenceSource, auditPath, codeHash string) error {
	objects := make(map[string]map[string]Evidence)
	for _, entry := range rule.Evidence {
		if objects[entry.Object] == nil {
			objects[entry.Object] = make(map[string]Evidence)
		}
		objects[entry.Object][entry.Check] = entry
	}
	goSource := newQueryGoSource(source)
	var issues []error
	for _, object := range rule.Objects {
		if err := checkQueryObject(object, objects[object], rule.QueryEntries[object], source, goSource, auditPath, codeHash); err != nil {
			issues = append(issues, fmt.Errorf("%s / %s: %w", queryRule, object, err))
		}
	}
	return errors.Join(issues...)
}

func checkQueryObject(object string, entries map[string]Evidence, candidates []QueryEntryTarget, source *evidenceSource, goSource *queryGoSource, auditPath, codeHash string) error {
	calls, inputs, batch := entries["calls"], entries["inputs"], entries["batch"]
	if calls.Result == "not_applicable" || inputs.Result == "not_applicable" || batch.Result == "not_applicable" {
		if calls.Result == "not_applicable" && inputs.Result == "not_applicable" && batch.Result == "not_applicable" {
			var issues []error
			for _, e := range []Evidence{calls, inputs, batch} {
				_, err := source.line(e.Location)
				issues = append(issues, err)
			}
			return errors.Join(issues...)
		}
		return fmt.Errorf("у операции смешаны применимые и неприменимые подпроверки")
	}
	_, locationErr := source.line(object)
	var issues []error
	issues = append(issues, locationErr)
	if inputs.Result == "checked" {
		issues = append(issues, checkQueryInputs(object, inputs.QueryInputs, source, goSource))
	}
	if inputs.Result == "pending" || inputs.Result == "checked" {
		issues = append(issues, checkQueryEntries(inputs, candidates, goSource))
	}
	if inputs.Result == "pending" {
		issues = append(issues, checkQueryInputGap(inputs, source))
	}
	if calls.Result == "checked" {
		issues = append(issues, checkQueryCalls(object, calls.QueryCalls, inputs.QueryInputs, inputs.Result == "checked", source, goSource, auditPath, codeHash))
	}
	if calls.Result == "pending" {
		questions := calls.Facts["question"]
		if len(questions) != 1 {
			issues = append(issues, fmt.Errorf("calls: для pending заполните facts.question с недостающей проверкой"))
		} else {
			issues = append(issues, checkReviewFact("question", questions[0], source))
		}
	}
	if batch.Result == "checked" || batch.Result == "violated" {
		if calls.Result != "checked" || inputs.Result != "checked" {
			issues = append(issues, fmt.Errorf("batch требует завершённых calls и inputs"))
		}
		if batch.Result == "checked" && calls.QueryCalls != nil &&
			calls.QueryCalls.One != nil && calls.QueryCalls.Two != nil &&
			*calls.QueryCalls.Two > *calls.QueryCalls.One && strings.TrimSpace(batch.QueryJustification) == "" {
			issues = append(issues, fmt.Errorf("batch: укажите обоснование поштучных запросов"))
		}
	}
	return errors.Join(issues...)
}

func checkQueryInputGap(entry Evidence, source *evidenceSource) error {
	in := entry.QueryInputs
	if in == nil || in.Gap == nil || strings.TrimSpace(in.Gap.Question) == "" {
		return fmt.Errorf("inputs: для pending заполните query_inputs.gap с причиной и вопросом")
	}
	gap := in.Gap
	switch gap.Kind {
	case "missing_file":
		if entry.MissingFile == nil {
			return fmt.Errorf("inputs: причина missing_file требует путь и ревизию")
		}
	case "contract", "chain":
		if entry.MissingFile != nil {
			return fmt.Errorf("inputs: для отсутствующего файла используйте причину missing_file")
		}
	default:
		return fmt.Errorf("inputs: gap.kind должен быть missing_file, contract или chain")
	}
	for _, loc := range append([]string{gap.LastAt}, in.Locations...) {
		if _, err := source.line(loc); err != nil {
			return fmt.Errorf("inputs: %w", err)
		}
	}
	return nil
}

func checkQueryEntries(entry Evidence, candidates []QueryEntryTarget, goSource *queryGoSource) error {
	remaining := make(map[string][]string)
	for _, candidate := range candidates {
		remaining[candidate.Location] = candidate.Calls
	}
	var issues []error
	if entry.QueryInputs != nil {
		for _, check := range entry.QueryInputs.Entries {
			calls, found := remaining[check.Entry]
			if !found {
				issues = append(issues, fmt.Errorf("inputs: неизвестный или повторный кандидат %s", check.Entry))
				continue
			}
			delete(remaining, check.Entry)
			if err := goSource.checkEntry(check, calls); err != nil {
				issues = append(issues, fmt.Errorf("inputs: %s: %w", check.Entry, err))
			}
		}
	}
	for _, candidate := range candidates {
		if _, found := remaining[candidate.Location]; found {
			issues = append(issues, fmt.Errorf("inputs: разберите штатный вход %s (%s) в query_inputs.entries", candidate.Location, candidate.Symbol))
		}
	}
	return errors.Join(issues...)
}

func checkQueryInputs(object string, in *QueryInputs, source *evidenceSource, goSource *queryGoSource) error {
	if in == nil || len(in.Locations) == 0 || len(in.One) != 1 || strings.TrimSpace(in.One[0]) == "" {
		return fmt.Errorf("inputs: заполните query_inputs.one и locations")
	}
	if in.Gap != nil {
		return fmt.Errorf("inputs: query_inputs.gap допустим только для pending")
	}
	for _, loc := range in.Locations {
		if _, err := source.line(loc); err != nil {
			return fmt.Errorf("inputs: %w", err)
		}
	}
	if len(in.Two) == 0 {
		if err := checkQueryLimits(in, source); err != nil {
			return err
		}
	} else {
		if in.TwoLimit != "" || len(in.TwoLimits) != 0 || len(in.Two) != 2 || strings.TrimSpace(in.Two[0]) == "" ||
			strings.TrimSpace(in.Two[1]) == "" || strings.TrimSpace(in.Two[0]) == strings.TrimSpace(in.Two[1]) {
			return fmt.Errorf("inputs: two должно содержать два разных ID без ограничений two_limits")
		}
		if err := checkQueryTwoScenario(object, in, source, goSource); err != nil {
			return err
		}
	}
	if len(in.Repeat) > 2 || (len(in.Repeat) > 0 && strings.TrimSpace(in.Repeat[0]) == "") ||
		(len(in.Repeat) == 2 && in.Repeat[0] != in.Repeat[1]) {
		return fmt.Errorf("inputs: repeat должен содержать один ID после дедупликации или два одинаковых ID")
	}
	return nil
}

func checkQueryTwoScenario(object string, in *QueryInputs, source *evidenceSource, goSource *queryGoSource) error {
	scenario := in.TwoScenario
	if scenario == nil || len(scenario.Parameters) == 0 {
		return fmt.Errorf("inputs: заполните two_scenario с конкретным штатным входом на два ID")
	}
	for name, value := range scenario.Parameters {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("inputs: two_scenario.parameters требует имена и конкретные значения")
		}
	}
	if !slices.Equal(in.Two, scenario.IDs) {
		return fmt.Errorf("inputs: ID на входе адаптера должны совпадать с query_inputs.two")
	}
	if _, err := source.line(scenario.Entry); err != nil {
		return fmt.Errorf("inputs: two_scenario.entry: %w", err)
	}
	if isGoRef(scenario.Entry) {
		if _, err := goSource.declaration(scenario.Entry); err != nil {
			return fmt.Errorf("inputs: two_scenario.entry: %w", err)
		}
	}
	if len(in.Entries) > 0 && !slices.ContainsFunc(in.Entries, func(e QueryEntryReview) bool {
		return e.Entry == scenario.Entry && e.Result == "inspected" && e.ValidationAt == scenario.Validation.Location
	}) {
		return fmt.Errorf("inputs: two_scenario должен ссылаться на связанный штатный вход и его проверку")
	}
	if scenario.Validation.Location == scenario.Selection.Location || scenario.Selection.Location == scenario.Adapter.Location ||
		scenario.Validation.Location == scenario.Adapter.Location {
		return fmt.Errorf("inputs: укажите отдельные места валидации, отбора и вызова адаптера")
	}
	for _, step := range []struct {
		role string
		fact ReviewFact
	}{{"validation", scenario.Validation}, {"selection", scenario.Selection}, {"adapter", scenario.Adapter}} {
		if err := checkReviewFact("statement", step.fact, source); err != nil {
			return fmt.Errorf("inputs: two_scenario.%s: %w", step.role, err)
		}
	}
	if isGoRef(object) {
		if err := goSource.checkAdapterCall(object, scenario.Adapter.Location); err != nil {
			return fmt.Errorf("inputs: two_scenario.adapter: %w", err)
		}
	}
	return nil
}

func checkQueryLimits(in *QueryInputs, source *evidenceSource) error {
	if in.TwoScenario != nil || len(in.TwoLimits) == 0 {
		return fmt.Errorf("inputs: без двух ID укажите two_limits с ограничением каждого связанного входа")
	}
	related := make(map[string]bool)
	for _, e := range in.Entries {
		if e.Result == "inspected" {
			related[e.Entry] = true
		}
	}
	seen := make(map[string]bool)
	for _, limit := range in.TwoLimits {
		if seen[limit.Entry] || (len(in.Entries) > 0 && !related[limit.Entry]) {
			return fmt.Errorf("inputs: two_limits: повторный или несвязанный вход %s", limit.Entry)
		}
		seen[limit.Entry] = true
		if _, err := source.line(limit.Entry); err != nil {
			return fmt.Errorf("inputs: two_limits.entry: %w", err)
		}
		if err := checkReviewFact("statement", limit.Condition, source); err != nil {
			return fmt.Errorf("inputs: two_limits.condition: %w", err)
		}
	}
	for _, e := range in.Entries {
		if related[e.Entry] && !seen[e.Entry] {
			return fmt.Errorf("inputs: two_limits: укажите ограничение для %s", e.Entry)
		}
	}
	return nil
}

func checkQueryCalls(object string, calls *QueryCalls, in *QueryInputs, inputsClosed bool, source *evidenceSource, goSource *queryGoSource, auditPath, codeHash string) error {
	if calls == nil || len(calls.Paths) == 0 || calls.One == nil {
		return fmt.Errorf("calls: заполните query_calls.paths и one")
	}
	if calls.Probe != nil && calls.Probe.File == "" && (strings.TrimSpace(calls.Probe.Command) == "" || strings.TrimSpace(calls.Probe.Counter) == "") {
		return fmt.Errorf("calls: для замера укажите команду и счётчик вызовов драйвера")
	}
	once, perKey := 0, 0
	seen := make(map[string]bool)
	for _, path := range calls.Paths {
		var chain strings.Builder
		for _, step := range path.Steps {
			chain.WriteString(step.At + "\x00" + step.To + "\x00")
		}
		chain.WriteString(path.DriverAt)
		key := chain.String()
		if seen[key] {
			return fmt.Errorf("calls: повтор цепочки к %s", path.DriverAt)
		}
		seen[key] = true
		repeats := path.LoopAt != ""
		if isGoRef(object) {
			var err error
			repeats, err = goSource.path(object, path, calls.KeysParam, calls.Probe == nil)
			if err != nil {
				return fmt.Errorf("calls: %w", err)
			}
		} else {
			if err := checkQueryLocations(path, source); err != nil {
				return err
			}
			if calls.Probe == nil && repeats && strings.TrimSpace(calls.KeysParam) == "" {
				return fmt.Errorf("calls: для цикла укажите keys_param или приложите замер")
			}
		}
		if repeats {
			perKey++
		} else {
			once++
		}
	}
	if inputsClosed && in != nil && ((len(in.Two) > 0) != (calls.Two != nil) || (len(in.Repeat) > 0) != (calls.Repeat != nil)) {
		return fmt.Errorf("calls: числа two и repeat должны соответствовать допустимым входам")
	}
	repeatKeys := 2
	if in != nil && len(in.Repeat) > 0 {
		repeatKeys = len(in.Repeat)
	}
	for _, c := range []struct {
		label string
		count *int
		keys  int
	}{{"one", calls.One, 1}, {"two", calls.Two, 2}, {"repeat", calls.Repeat, repeatKeys}} {
		if c.count == nil {
			continue
		}
		if *c.count < 0 {
			return fmt.Errorf("calls: %s не может быть отрицательным", c.label)
		}
		if want := once + c.keys*perKey; calls.Probe == nil && *c.count != want {
			return fmt.Errorf("calls: для %s цепочка даёт %d обращений к драйверу, записано %d", c.label, want, *c.count)
		}
	}
	hasProbeFile := calls.Probe != nil && calls.Probe.File != ""
	if perKey > 0 && !hasProbeFile && (calls.Two != nil || (inputsClosed && in != nil && len(in.Two) == 2)) {
		return fmt.Errorf("calls: для цикла на двух ID нужен файл замера на драйвере в query_calls.probe.file")
	}
	if hasProbeFile {
		return checkQueryProbe(object, calls, in, auditPath, codeHash)
	}
	return nil
}

func checkQueryProbe(object string, calls *QueryCalls, in *QueryInputs, auditPath, codeHash string) error {
	var log queryProbeLog
	if err := readAuditProbe(auditPath, ".queries", calls.Probe.File, &log); err != nil {
		return fmt.Errorf("calls: probe.file: %w", err)
	}
	if log.CodeHash == "" || log.CodeHash != codeHash || log.Object != object || strings.TrimSpace(log.Command) == "" {
		return fmt.Errorf("calls: замер должен относиться к этому снимку и методу и содержать команду")
	}
	if in == nil {
		return fmt.Errorf("calls: для замера нужны ключи из query_inputs")
	}
	runs := make(map[string]queryProbeRun, len(log.Runs))
	for _, run := range log.Runs {
		if _, exists := runs[run.Case]; exists {
			return fmt.Errorf("calls: повтор замера %s", run.Case)
		}
		runs[run.Case] = run
	}
	for _, scenario := range []struct {
		name  string
		ids   []string
		count *int
	}{{"one", in.One, calls.One}, {"two", in.Two, calls.Two}, {"repeat", in.Repeat, calls.Repeat}} {
		if scenario.count == nil {
			continue
		}
		run, ok := runs[scenario.name]
		if !ok || len(scenario.ids) == 0 || !slices.Equal(run.IDs, scenario.ids) || run.Calls == nil {
			return fmt.Errorf("calls: замер %s должен содержать те же ID и массив событий драйвера", scenario.name)
		}
		for _, at := range run.Calls {
			if !slices.ContainsFunc(calls.Paths, func(p QueryPath) bool { return p.DriverAt == at }) {
				return fmt.Errorf("calls: замер %s содержит обращение вне цепочки метода: %s", scenario.name, at)
			}
		}
		if len(run.Calls) != *scenario.count {
			return fmt.Errorf("calls: замер %s содержит %d обращений, записано %d", scenario.name, len(run.Calls), *scenario.count)
		}
		delete(runs, scenario.name)
	}
	if len(runs) > 0 {
		return fmt.Errorf("calls: замер содержит входы без счётчиков в query_calls")
	}
	return nil
}

func checkQueryLocations(path QueryPath, source *evidenceSource) error {
	refs := []string{path.DriverAt}
	if path.LoopAt != "" {
		refs = append(refs, path.LoopAt)
	}
	for _, step := range path.Steps {
		refs = append(refs, step.At, step.To)
		if step.ReceiverAt != "" {
			refs = append(refs, step.ReceiverAt)
		}
	}
	for _, ref := range refs {
		if _, err := source.line(ref); err != nil {
			return fmt.Errorf("calls: %w", err)
		}
	}
	return nil
}

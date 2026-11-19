package reviewaudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

type SQLBindingProposal struct {
	SQL       string               `json:"sql"`
	Style     string               `json:"style"`
	Arguments []SQLBindingArgument `json:"arguments"`
	Expected  []SQLBindingResult   `json:"expected"`
	Evidence  ReviewFact           `json:"evidence"`
	ProbeFile string               `json:"probe_file,omitempty"`
}

type SQLBindingArgument struct {
	Parameter string   `json:"parameter"`
	Value     string   `json:"value,omitempty"`
	IDs       []string `json:"ids,omitempty"`
}

type SQLBindingResult struct {
	ID   string `json:"id"`
	Rows string `json:"rows"`
}

type sqlBindingProbe struct {
	CodeHash  string            `json:"code_hash"`
	Object    string            `json:"object"`
	Location  string            `json:"location"`
	Command   string            `json:"command"`
	SQL       string            `json:"sql"`
	Arguments []json.RawMessage `json:"arguments"`
}

func checkOptionAndBindingPairs(rule Rule, source *evidenceSource, auditPath, codeHash string) error {
	if rule.ID != configOptionRule && rule.ID != dataBoundaryRule {
		return nil
	}
	byObject := make(map[string]map[string]Evidence)
	for _, entry := range rule.Evidence {
		if byObject[entry.Object] == nil {
			byObject[entry.Object] = make(map[string]Evidence)
		}
		byObject[entry.Object][entry.Check] = entry
	}
	var issues []error
	for _, object := range rule.Objects {
		entries := byObject[object]
		var err error
		if rule.ID == configOptionRule {
			err = checkListDecision(entries["behavior"], entries["basis"], source.taskText)
			if err == nil {
				err = checkDerivedOption(entries["behavior"], entries["basis"], source)
			}
		} else {
			err = checkBindingProposal(entries["parameters"], entries["proposed_binding"], source)
			if err == nil && entries["proposed_binding"].Result == "checked" {
				err = checkSQLBindingProbe(entries["proposed_binding"], auditPath, codeHash)
			}
		}
		if err != nil {
			issues = append(issues, fmt.Errorf("%s / %s: %w", rule.ID, object, err))
		}
	}
	return errors.Join(issues...)
}

func checkDerivedOption(behavior, basis Evidence, source *evidenceSource) error {
	if behavior.Kind != "derived" || behavior.Result == "pending" || behavior.Result == "not_applicable" {
		return nil
	}
	facts := behavior.Facts
	derived, omitted, incompatible := facts["derive"], facts["omitted"], facts["incompatible"]
	if len(derived) != 1 || len(omitted) != 1 || len(incompatible) != 1 ||
		strings.TrimSpace(derived[0].Value) == "" || strings.TrimSpace(omitted[0].Value) == "" ||
		strings.TrimSpace(incompatible[0].Value) == "" || incompatible[0].Value == derived[0].Value {
		return fmt.Errorf("behavior: для derived нужны значение из источника, результат удаления и другое проверенное значение (derive, omitted, incompatible)")
	}
	if basis.Result != "checked" {
		return nil
	}
	for _, fact := range basis.Facts["necessity"] {
		if containsIdentifier(fact.Quote, basis.Object) && !fromTaskImplementationOptions(fact.Location, source.taskText) &&
			checkContractFact(fact, source) == nil {
			return nil
		}
	}
	return fmt.Errorf("basis: для derived укажите facts.necessity с согласованным требованием обязательности поля %s", basis.Object)
}

func checkListDecision(behavior, basis Evidence, task string) error {
	if behavior.Kind != "list" {
		if basis.ListDecision != "" {
			return fmt.Errorf("basis: list_decision относится к behavior.kind: list")
		}
		return nil
	}
	switch basis.ListDecision {
	case "undecided":
		if basis.Result != "pending" {
			return fmt.Errorf("basis: для list_decision: undecided нужен результат pending и вопрос")
		}
		return nil
	case "automatic", "explicit":
		if basis.Result != "checked" && basis.Result != "violated" {
			return fmt.Errorf("basis: для согласованного list_decision нужен результат checked или violated")
		}
	default:
		return fmt.Errorf("basis: укажите list_decision: automatic, explicit или undecided")
	}
	for _, fact := range basis.Facts["contract"] {
		if !fromTaskImplementationOptions(fact.Location, task) {
			return nil
		}
	}
	return fmt.Errorf("basis: варианты реализации не подтверждают list_decision, укажите согласованное требование")
}

func fromTaskImplementationOptions(location, task string) bool {
	number, ok := strings.CutPrefix(location, "task:")
	if !ok {
		return false
	}
	line, err := strconv.Atoi(number)
	lines := strings.Split(task, "\n")
	if err != nil || line < 1 || line > len(lines) {
		return false
	}
	mask, _ := taskform.FenceMask(lines[:line])
	optionsLevel := 0
	for i, text := range lines[:line] {
		if mask[i] {
			continue
		}
		text = strings.TrimSpace(text)
		title := strings.TrimLeft(text, "#")
		level := len(text) - len(title)
		if level == 0 || level > 6 || !strings.HasPrefix(title, " ") {
			continue
		}
		if level <= optionsLevel {
			optionsLevel = 0
		}
		switch strings.ToLower(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(title), "#"))) {
		case "варианты реализации", "варианты решения", "implementation options", "implementation alternatives":
			optionsLevel = level
		}
	}
	return optionsLevel != 0
}

var proposalParameter = regexp.MustCompile(`\{\{([1-9][0-9]*):([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)
var parameterName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func checkBindingProposal(parameters, proposal Evidence, source *evidenceSource) error {
	if parameters.Result == "" {
		return nil
	}
	literals, err := checkSQLFilterLiterals(parameters, source)
	if err != nil {
		return err
	}
	var changed []string
	for _, filter := range parameters.Facts["filter"] {
		if filter.Parameter == "" {
			continue
		}
		if !parameterName.MatchString(filter.Parameter) {
			return fmt.Errorf("parameters: недопустимое имя нового параметра %q", filter.Parameter)
		}
		changed = append(changed, filter.Parameter)
	}
	if parameters.BindingChange == nil && (parameters.Result == "checked" || parameters.Result == "violated") {
		return fmt.Errorf("parameters: укажите binding_change")
	}
	change := parameters.BindingChange != nil && *parameters.BindingChange
	if !change && (len(changed) > 0 || parameters.Result == "violated") {
		return fmt.Errorf("parameters: для параметризации фильтра нужен binding_change: true")
	}
	if literals.unresolved && proposal.Result != "pending" {
		return fmt.Errorf("proposed_binding: при открытой проверке литерала нужен pending")
	}
	if !change {
		if proposal.Result != "not_applicable" && !(parameters.Result == "pending" && proposal.Result == "pending") {
			return fmt.Errorf("proposed_binding: без изменения привязки укажите not_applicable, при открытых parameters допустим pending")
		}
		if proposal.SQLProposal != nil {
			return fmt.Errorf("proposed_binding: sql_proposal требует binding_change: true")
		}
		return nil
	}
	if parameters.Result == "violated" && len(changed) == 0 {
		return fmt.Errorf("parameters: укажите имя нового параметра в facts.filter.parameter")
	}
	if parameters.Kind != "collection" && parameters.Kind != "scalar" {
		return fmt.Errorf("parameters: при изменении привязки укажите kind: collection или scalar")
	}
	switch proposal.Result {
	case "checked", "violated":
		return checkSQLProposal(proposal.SQLProposal, changed, parameters.Kind == "collection", source, literals.values)
	case "pending":
		if proposal.SQLProposal != nil {
			return checkSQLProposal(proposal.SQLProposal, changed, parameters.Kind == "collection", source, literals.values)
		}
		return nil
	default:
		return fmt.Errorf("proposed_binding: при binding_change: true нужны отдельный разбор или pending с вопросом")
	}
}

func checkSQLProposal(proposal *SQLBindingProposal, changed []string, collection bool, source *evidenceSource, literalValues map[string]string) error {
	if proposal == nil {
		return fmt.Errorf("proposed_binding: заполните sql_proposal")
	}
	if proposal.Style != "positional" && proposal.Style != "numbered" && proposal.Style != "sqlite" {
		return fmt.Errorf("proposed_binding: укажите style: positional, numbered или sqlite")
	}
	remainder := proposalParameter.ReplaceAllString(proposal.SQL, "")
	if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
		return fmt.Errorf("proposed_binding: используйте пометки {{позиция:имя}}")
	}
	slots := make([]string, len(proposal.Arguments))
	for i, match := range proposalParameter.FindAllStringSubmatch(proposal.SQL, -1) {
		position, err := strconv.Atoi(match[1])
		if err != nil || position > len(slots) || (proposal.Style == "positional" && position != i+1) {
			return fmt.Errorf("proposed_binding: неверная позиция плейсхолдера %s", match[0])
		}
		if slots[position-1] != "" && slots[position-1] != match[2] {
			return fmt.Errorf("proposed_binding: позиция %d обозначает разные параметры", position)
		}
		slots[position-1] = match[2]
	}
	if len(slots) == 0 {
		return fmt.Errorf("proposed_binding: нужны плейсхолдеры {{позиция:имя}} и по одному аргументу на позицию")
	}
	ids := make(map[string]bool)
	for i, argument := range proposal.Arguments {
		hasValue, hasIDs := strings.TrimSpace(argument.Value) != "", len(argument.IDs) != 0
		if slots[i] == "" || argument.Parameter != slots[i] || hasValue == hasIDs {
			return fmt.Errorf("proposed_binding: аргумент %d должен задавать значение для %s", i+1, slots[i])
		}
		if value, ok := literalValues[argument.Parameter]; ok && (!hasValue || argument.Value != value) {
			return fmt.Errorf("proposed_binding: параметр %s должен получить значение литерала %q", argument.Parameter, value)
		}
		for _, id := range argument.IDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("proposed_binding: пустой ID в аргументе %d", i+1)
			}
			ids[strings.TrimSpace(id)] = true
		}
	}
	collection = collection || len(ids) > 0 || len(proposal.Expected) > 1
	for _, name := range changed {
		if !slices.Contains(slots, name) {
			return fmt.Errorf("proposed_binding: в SQL отсутствует новый параметр %s", name)
		}
	}
	if len(proposal.Expected) == 0 {
		return fmt.Errorf("proposed_binding: укажите ожидаемые строки для входа")
	}
	seen := make(map[string]bool)
	for _, result := range proposal.Expected {
		if strings.TrimSpace(result.Rows) == "" || seen[result.ID] || (collection && !ids[result.ID]) {
			return fmt.Errorf("proposed_binding: для каждого входного ID нужны отдельные ожидаемые строки")
		}
		seen[result.ID] = true
	}
	if collection && (len(ids) < 2 || len(seen) != len(ids)) {
		return fmt.Errorf("proposed_binding: укажите два разных ID в аргументах и ожидаемые строки для каждого")
	}
	if err := checkReviewFact("evidence", proposal.Evidence, source); err != nil {
		return fmt.Errorf("proposed_binding: sql_proposal.evidence: %w", err)
	}
	return nil
}

func checkSQLBindingProbe(entry Evidence, auditPath, codeHash string) error {
	proposal := entry.SQLProposal
	if proposal == nil || !safeID.MatchString(proposal.ProbeFile) || !strings.HasSuffix(proposal.ProbeFile, ".json") {
		return fmt.Errorf("proposed_binding: для checked укажите probe_file с фактическим SQL и порядком аргументов драйвера")
	}
	var probe sqlBindingProbe
	err := readAuditProbe(auditPath, ".bindings", proposal.ProbeFile, &probe)
	if err != nil {
		return fmt.Errorf("proposed_binding: probe_file: %w", err)
	}
	if probe.CodeHash != codeHash || probe.Object != entry.Object || probe.Location != entry.Location {
		return fmt.Errorf("proposed_binding: проба должна относиться к этому снимку и запросу")
	}
	if strings.TrimSpace(probe.Command) == "" {
		return fmt.Errorf("proposed_binding: укажите команду пробы")
	}
	if !matchesDriverSQL(proposal, probe.SQL) {
		return fmt.Errorf("proposed_binding: SQL на границе драйвера не совпадает с предложением")
	}
	if len(probe.Arguments) != len(proposal.Arguments) {
		return fmt.Errorf("proposed_binding: число аргументов драйвера не совпадает с предложением")
	}
	for i, argument := range proposal.Arguments {
		if !matchesDriverArgument(argument, probe.Arguments[i]) {
			return fmt.Errorf("proposed_binding: аргумент драйвера %d не совпадает с предложением для %s", i+1, argument.Parameter)
		}
	}
	return nil
}

func readAuditProbe(auditPath, suffix, name string, probe any) error {
	if !safeID.MatchString(name) || !strings.HasSuffix(name, ".json") {
		return fmt.Errorf("укажите имя JSON-файла пробы")
	}
	files, err := os.OpenRoot(filepath.Dir(auditPath))
	if err != nil {
		return err
	}
	defer files.Close()
	path := filepath.Join(filepath.Base(auditPath)+suffix, name)
	info, err := files.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("проба должна быть обычным файлом")
	}
	file, err := files.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(probe); err != nil {
		return err
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return fmt.Errorf("после пробы есть лишние данные")
	}
	return nil
}

func matchesDriverSQL(proposal *SQLBindingProposal, observed string) bool {
	sql := strings.TrimSpace(proposal.SQL)
	var pattern strings.Builder
	pattern.WriteString("^")
	end := 0
	var positions []int
	for _, match := range proposalParameter.FindAllStringSubmatchIndex(sql, -1) {
		pattern.WriteString(regexp.QuoteMeta(sql[end:match[0]]))
		switch proposal.Style {
		case "sqlite":
			pattern.WriteString(`(\?[0-9]*)`)
			position, err := strconv.Atoi(sql[match[2]:match[3]])
			if err != nil {
				return false
			}
			positions = append(positions, position)
		case "positional":
			pattern.WriteString(`(?:\?|%s)`)
		default:
			pattern.WriteString(`(?:\$|\?|:|@p)`)
			pattern.WriteString(sql[match[2]:match[3]])
		}
		end = match[1]
	}
	pattern.WriteString(regexp.QuoteMeta(sql[end:]))
	pattern.WriteString("$")
	re, err := regexp.Compile(pattern.String())
	if err != nil {
		return false
	}
	observed = strings.TrimSpace(observed)
	match := re.FindStringSubmatchIndex(observed)
	if match == nil {
		return false
	}
	if proposal.Style != "sqlite" {
		return true
	}
	tokens := sqliteParameterPositions(observed)
	if len(tokens) != len(positions) {
		return false
	}
	for i, position := range positions {
		if tokens[match[2+2*i]] != position {
			return false
		}
	}
	return true
}

// SQLite gives a bare ? the next slot after the largest slot already assigned.
func sqliteParameterPositions(sql string) map[int]int {
	positions := make(map[int]int)
	largest := 0
	for i := 0; i < len(sql); {
		switch {
		case strings.HasPrefix(sql[i:], "--"):
			end := strings.IndexByte(sql[i:], '\n')
			if end < 0 {
				return positions
			}
			i += end + 1
		case strings.HasPrefix(sql[i:], "/*"):
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil
			}
			i += end + 4
		case strings.ContainsRune("'\"`[", rune(sql[i])):
			quote := sql[i]
			if quote == '[' {
				quote = ']'
			}
			i++
			end := strings.IndexByte(sql[i:], quote)
			if end < 0 {
				return nil
			}
			i += end + 1
		case sql[i] == '?':
			start := i
			i++
			for i < len(sql) && sql[i] >= '0' && sql[i] <= '9' {
				i++
			}
			slot := largest + 1
			if i > start+1 {
				var err error
				slot, err = strconv.Atoi(sql[start+1 : i])
				if err != nil || slot < 1 {
					return nil
				}
			}
			positions[start] = slot
			largest = max(largest, slot)
		case sql[i] == ':' || sql[i] == '@' || sql[i] == '$':
			if i+1 < len(sql) && sqliteNameStart(sql[i+1]) {
				return nil
			}
			i++
		default:
			i++
		}
	}
	return positions
}

func sqliteNameStart(ch byte) bool {
	return ch == '_' || ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= 0x80
}

func matchesDriverArgument(expected SQLBindingArgument, actual json.RawMessage) bool {
	var scalar *string
	if json.Unmarshal(actual, &scalar) == nil && scalar != nil {
		if len(expected.IDs) == 1 {
			return *scalar == expected.IDs[0]
		}
		return len(expected.IDs) == 0 && *scalar == expected.Value
	}
	if len(expected.IDs) == 0 {
		var number json.Number
		if json.Unmarshal(actual, &number) == nil && number != "" {
			return number.String() == expected.Value
		}
		return (string(actual) == "true" || string(actual) == "false") && string(actual) == expected.Value
	}
	var ids []string
	return json.Unmarshal(actual, &ids) == nil && slices.Equal(ids, expected.IDs)
}

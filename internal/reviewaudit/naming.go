package reviewaudit

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const namingRule = "common-style:STYLE-001"

type nameConclusionIssue struct {
	code string
	err  error
}

func (i *nameConclusionIssue) Error() string { return i.err.Error() }
func (i *nameConclusionIssue) Unwrap() error { return i.err }

func nameConclusionError(code, format string, args ...any) error {
	return &nameConclusionIssue{code: code, err: fmt.Errorf(format, args...)}
}

type WordCheck struct {
	Word     string `json:"word"`
	Meaning  string `json:"meaning"`
	Fact     string `json:"fact"`
	Location string `json:"location"`
	Result   string `json:"result,omitempty"`
}

type NameObjectComparison struct {
	Expected            []string         `json:"expected"`
	NoExpectationReason string           `json:"no_expectation_reason,omitempty"`
	Actual              string           `json:"actual"`
	ActualAt            string           `json:"actual_at"`
	EquivalentAt        string           `json:"equivalent_at,omitempty"`
	EquivalentFlow      *NameResultFlow  `json:"equivalent_flow,omitempty"`
	Proof               *NameObjectProof `json:"proof,omitempty"`
}

type NameResultFlow struct {
	Caller        string          `json:"caller"`
	CallAt        string          `json:"call_at"`
	Value         string          `json:"value"`
	Consumer      string          `json:"consumer"`
	ReceiverAt    string          `json:"receiver_at,omitempty"`
	Forward       []NameResultHop `json:"forward,omitempty"`
	Projection    *NameProjection `json:"projection,omitempty"`
	UseExpression string          `json:"use_expression,omitempty"`
	UseKind       string          `json:"use_kind,omitempty"`
	UseField      string          `json:"use_field,omitempty"`
}

type NameResultHop struct {
	ReturnAt   string `json:"return_at"`
	Caller     string `json:"caller"`
	CallAt     string `json:"call_at"`
	Value      string `json:"value"`
	ReceiverAt string `json:"receiver_at,omitempty"`
}

type NameProjection struct {
	RangeAt  string `json:"range_at"`
	Field    string `json:"field"`
	FilterAt string `json:"filter_at,omitempty"`
}

type ResultEvidence struct {
	Implementation string          `json:"implementation,omitempty"`
	Word           *string         `json:"word"`
	Returned       string          `json:"returned"`
	ReturnAt       string          `json:"return_at"`
	Consumer       string          `json:"consumer"`
	ConsumerAt     string          `json:"consumer_at"`
	Flow           *NameResultFlow `json:"flow,omitempty"`
	Facts          []ReviewFact    `json:"facts,omitempty"`
}

type WriteEvidence struct {
	Word    *string         `json:"word"`
	Target  string          `json:"target"`
	Value   string          `json:"value"`
	WriteAt string          `json:"write_at"`
	File    *NameFileWrite  `json:"file,omitempty"`
	Calls   []NameWriteCall `json:"calls,omitempty"`
}

type NameFileWrite struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	PayloadAt string `json:"payload_at,omitempty"`
	EncodeAt  string `json:"encode_at,omitempty"`
}

func checkNames(root, reportPath, output string, report Report) error {
	for _, rule := range report.Rules {
		if rule.ID == namingRule && len(rule.Objects) != 0 {
			return checkNameEvidence(root, reportPath, output, report.BaseSHA, rule)
		}
	}
	return nil
}

func checkNameEvidence(root, reportPath, output, base string, rule Rule) error {
	files, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer files.Close()
	source := evidenceSource{root: root, reportPath: reportPath, output: output, base: base, files: files, cache: make(map[string][]string)}
	targets := make(map[string]Target, len(rule.Targets))
	for _, target := range rule.Targets {
		targets[target.Object] = target
	}
	words := make(map[string][]string)
	meanings := make(map[string]Evidence)
	for _, e := range rule.Evidence {
		if e.Check == "meaning" {
			meanings[e.Object] = e
			if strings.TrimSpace(e.Symbol) != "" {
				words[e.Object] = identifierWords(e.Symbol)
			}
		}
	}
	var issues []error
	for _, e := range rule.Evidence {
		key := evidenceKey{e.Object, e.Check}
		if (e.Check == "result" || e.Check == "writes") && e.Result == "violated" && meanings[e.Object].Result != "violated" {
			issues = append(issues, newObjectIssue(namingRule, key, "нарушение %s должно быть отражено в meaning", e.Check))
		}
		target, required := targets[e.Object]
		if required && e.Result == "not_applicable" {
			exactWords := identifierWords(target.Symbol)
			if namesResultOrWrite(e.Check, nameBeforeSource(exactWords)) {
				issues = append(issues, newObjectIssue(namingRule, key, "имя %q обещает объект для %s; сопоставьте его с кодом", strings.Join(exactWords, " "), e.Check))
				continue
			}
		}
		if required && e.Check == "meaning" {
			if e.Symbol != target.Symbol || e.Location != target.Location {
				issues = append(issues, newObjectIssue(namingRule, key, "в meaning укажите symbol %q и location %s", target.Symbol, target.Location))
				continue
			}
			if e.Result != "pending" {
				for _, word := range e.WordChecks {
					if word.Result == "" {
						issues = append(issues, newObjectIssue(namingRule, key, "укажите result для слова %q", word.Word))
					}
				}
			}
		}
		exactWords, declaration := words[e.Object], meanings[e.Object].Location
		if required {
			exactWords, declaration = identifierWords(target.Symbol), target.Location
		}
		if err := checkNameEntry(e, declaration, exactWords, &source); err != nil {
			issues = append(issues, newObjectIssue(namingRule, key, "%w", err))
		}
	}
	return errors.Join(issues...)
}

func checkNameEntry(e Evidence, declaration string, words []string, source *evidenceSource) error {
	if e.ResultEvidence != nil && e.ResultEvidence.Implementation != "" {
		var err error
		if e.Kind == "source" {
			declaration, err = checkNameSourceImplementation(e, declaration, source)
		} else {
			declaration, err = checkNameImplementation(declaration, e.ResultEvidence.Implementation, source)
		}
		if err != nil {
			return err
		}
	}
	switch e.Result {
	case "pending":
		return checkPendingName(e, declaration, source)
	case "not_applicable":
		if e.Check == "writes" && isGoRef(declaration) {
			if err := checkNameObjectNoWrites(e, declaration, words, source); err != nil {
				return err
			}
		}
		if e.Check == "meaning" {
			return fmt.Errorf("для заявленного символа требуется проверка смысла имени")
		}
		_, err := source.line(e.Location)
		return err
	}
	switch e.Check {
	case "meaning":
		return checkNameMeaning(e, words, source)
	case "result":
		d := e.ResultEvidence
		if d == nil || strings.TrimSpace(d.Returned) == "" || strings.TrimSpace(d.Consumer) == "" {
			return fmt.Errorf("заполните result_evidence: возвращаемый объект и его использование")
		}
		if e.Kind == "source" && (d.Implementation == "" || d.Flow == nil || e.ObjectComparison == nil || e.ObjectComparison.Proof == nil) {
			return fmt.Errorf("source: укажите реализацию, доказательство результата и цепочку до потребителя")
		}
		_, returnErr := source.line(d.ReturnAt)
		_, consumerErr := source.line(d.ConsumerAt)
		for i, fact := range d.Facts {
			if err := checkReviewFact("statement", fact, source); err != nil {
				return fmt.Errorf("result_evidence.facts[%d]: %w", i, err)
			}
		}
		return errors.Join(checkNameObject(e, d.Word, words, d.Returned, declaration, source), returnErr, consumerErr)
	case "writes":
		d := e.WriteEvidence
		if d == nil || strings.TrimSpace(d.Target) == "" || strings.TrimSpace(d.Value) == "" {
			return fmt.Errorf("заполните write_evidence: изменяемая запись и значение")
		}
		_, writeErr := source.line(d.WriteAt)
		observed := d.Target
		if d.File != nil && d.File.Role == "content" {
			observed = d.Value
		}
		return errors.Join(checkNameObject(e, d.Word, words, observed, declaration, source), writeErr)
	}
	return nil
}

// nameNextSteps folds errors from source, inventory and summary checks together.
func nameNextSteps(report Report, coverageErr error) ([]string, error) {
	step := map[string]string{
		"":        "дополните список объектов",
		"meaning": "сопоставьте слова имени с назначением по коду",
		"result":  "проследите возвращаемое значение до потребителя",
		"writes":  "найдите объект записи и присваиваемое значение",
	}
	type action struct{ check, next string }
	type actions struct {
		fixes   map[action][]string
		order   []action
		pending []string
	}
	byObject := map[string]*actions{}
	var order []string
	forObject := func(object string) *actions {
		entry := byObject[object]
		if entry == nil {
			order = append(order, object)
			entry = &actions{fixes: make(map[action][]string)}
			byObject[object] = entry
		}
		return entry
	}
	var other []error
	eachAuditIssue(coverageErr, func(err error) {
		switch e := err.(type) {
		case *objectIssue:
			if e.rule != namingRule {
				other = append(other, e)
				return
			}
			entry := forObject(e.key.object)
			key := action{e.key.check, e.next}
			if _, seen := entry.fixes[key]; !seen {
				entry.order = append(entry.order, key)
			}
			detail := oneLine(e.err.Error())
			if !slices.Contains(entry.fixes[key], detail) {
				entry.fixes[key] = append(entry.fixes[key], detail)
			}
		default:
			other = append(other, e)
		}
	})
	for _, rule := range report.Rules {
		if rule.ID != namingRule {
			continue
		}
		for _, e := range rule.Evidence {
			if e.Result == "pending" && len(e.Facts["question"]) == 1 {
				entry := forObject(e.Object)
				if !slices.ContainsFunc(entry.order, func(a action) bool { return a.check == e.Check }) {
					fact := e.Facts["question"][0]
					entry.pending = append(entry.pending, fmt.Sprintf("%s: выясните факт из вопроса по %s (%s)",
						e.Check, oneLine(fact.Location), oneLine(fact.Text)))
				}
			}
		}
	}
	var pending []string
	for _, object := range order {
		a := byObject[object]
		var nextSteps []string
		for _, key := range a.order {
			next := key.next
			if next == "" {
				next = step[key.check]
			}
			if next == "" {
				next = "сверьте название подпроверки"
			}
			if key.check != "" {
				next = oneLine(key.check) + ": " + next
			}
			detail := strings.Join(a.fixes[key], " / ")
			nextSteps = append(nextSteps, fmt.Sprintf("%s (%s)", next, detail))
		}
		line := fmt.Sprintf("%s / %s -> %s", namingRule, oneLine(object), strings.Join(append(nextSteps, a.pending...), " | "))
		if len(a.fixes) != 0 {
			other = append(other, errors.New(line))
		} else {
			pending = append(pending, line)
		}
	}
	return pending, errors.Join(other...)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func checkPendingName(e Evidence, declaration string, source *evidenceSource) error {
	questions := e.Facts["question"]
	if len(questions) != 1 || !slices.Contains([]string{"contract", "flow", "missing_file"}, e.Kind) {
		return fmt.Errorf("для pending укажите недостающий факт в facts.question и kind: contract, flow или missing_file")
	}
	if (e.Kind == "missing_file") != (e.MissingFile != nil) {
		return fmt.Errorf("для причины missing_file укажите путь и ревизию")
	}
	fact := questions[0]
	question := strings.ToLower(compactComment(fact.Text))
	if strings.Contains(question, "equivalent_at") || strings.Contains(question, "equivalent_flow") ||
		strings.Contains(question, "object_comparison") || strings.Contains(question, "формальное доказательство") ||
		strings.Contains(question, "формальное соответствие") {
		return fmt.Errorf("в facts.question назовите недостающий факт о коде или контракте")
	}
	if err := checkReviewFact("question", fact, source); err != nil {
		return err
	}
	if e.Kind != "flow" {
		return nil
	}
	line, err := source.line(fact.Location)
	if err != nil {
		return err
	}
	symbol := strings.TrimSpace(fact.Value)
	if fact.Location == declaration || symbol == "" ||
		!containsIdentifier(line, symbol) || !containsIdentifier(fact.Text, symbol) {
		return fmt.Errorf("для pending flow в facts.question укажите последний подтверждённый переход, его идентификатор в value и назовите его в вопросе")
	}
	if !strings.HasSuffix(question, "?") {
		return fmt.Errorf("для pending flow задайте конкретный вопрос после последнего подтверждённого перехода")
	}
	if isGoRef(declaration) {
		return newQueryGoSource(source).checkNameGap(declaration, e.Check, fact, symbol)
	}
	return nil
}

func (g *queryGoSource) checkNameGap(declaration, check string, fact ReviewFact, symbol string) error {
	fn, err := g.declaration(declaration)
	if err != nil {
		return err
	}
	if fact.Via != "" {
		helper, err := g.declaration(fact.Via)
		if err != nil {
			return err
		}
		call, _, err := g.call(fn, fact.Related, func(fun ast.Expr) bool {
			return calledName(fun) == helper.node.Name.Name
		})
		if err != nil {
			return fmt.Errorf("переход к %s: %w", fact.Via, err)
		}
		if err := g.receiver(fn, helper, call, ""); err != nil {
			return err
		}
		fn = helper
	}
	file, line, base, err := evidenceLocation(fact.Location)
	if err != nil {
		return err
	}
	if file != fn.file || base != fn.base || line < g.fset.Position(fn.node.Body.Lbrace).Line ||
		line > g.fset.Position(fn.node.Body.Rbrace).Line {
		return fmt.Errorf("%s: последний переход вне тела %s и его подтверждённого вызова", fact.Location, fn.node.Name.Name)
	}
	if check == "meaning" {
		return nil
	}
	for node := range ast.Preorder(fn.node.Body) {
		if g.fset.Position(node.Pos()).Line != line {
			continue
		}
		switch n := node.(type) {
		case *ast.ReturnStmt:
			if check == "result" && slices.ContainsFunc(n.Results, func(expr ast.Expr) bool { return calledName(expr) == symbol }) {
				return nil
			}
		case *ast.AssignStmt:
			if check == "writes" && slices.ContainsFunc(n.Lhs, func(expr ast.Expr) bool { return calledName(expr) == symbol }) {
				return nil
			}
		case *ast.CallExpr:
			if calledName(n.Fun) == symbol || slices.ContainsFunc(n.Args, func(expr ast.Expr) bool { return calledName(expr) == symbol }) {
				return nil
			}
		}
	}
	return fmt.Errorf("%s: покажите возврат, запись или вызов с %q, после которого остаётся вопрос", fact.Location, symbol)
}

// A scalar result can have no identifier. For non-Go code the stable
// evidence is the literal return and a caller that checks that same value.
func checkScalarReturn(e Evidence, decl, line string, source *evidenceSource) error {
	pair, result := e.ObjectComparison, e.ResultEvidence
	file, declLine, base, err := evidenceLocation(decl)
	resultFile, resultLine, resultBase, resultErr := evidenceLocation(pair.ActualAt)
	consumer, readErr := source.line(result.ConsumerAt)
	if err != nil || resultErr != nil || readErr != nil {
		return errors.Join(err, resultErr, readErr)
	}
	value := pair.Actual
	if file != resultFile || base != resultBase || resultLine <= declLine ||
		result.ReturnAt != pair.ActualAt || result.Returned != value ||
		strings.TrimSuffix(strings.TrimSpace(line), ";") != "return "+value {
		return fmt.Errorf("actual_at: укажите строку возврата литерала %q и его точное значение", value)
	}
	_, symbol, ok := strings.Cut(e.Object, "#")
	if !ok || symbol == "" || !containsIdentifier(result.Consumer, symbol) ||
		!containsIdentifier(result.Consumer, value) || !strings.Contains(consumer, result.Consumer) ||
		result.ConsumerAt == pair.ActualAt {
		return fmt.Errorf("consumer_at: покажите использование %s с проверкой значения %s", symbol, value)
	}
	return nil
}

func checkNameObject(e Evidence, word *string, words []string, observed, declaration string, source *evidenceSource) error {
	if err := checkNameWord(word, words); err != nil {
		return err
	}
	pair := e.ObjectComparison
	if pair == nil {
		return fmt.Errorf("заполните object_comparison: ожидаемый объект по имени и фактический по коду")
	}
	if err := checkExpectedObject(pair.Expected, *word, words); err != nil {
		return err
	}
	if len(pair.Expected) == 0 {
		if strings.TrimSpace(pair.NoExpectationReason) == "" {
			return fmt.Errorf("expected: объясните в no_expectation_reason, почему имя не называет объект")
		}
	}
	line, err := source.line(pair.ActualAt)
	if err != nil {
		return fmt.Errorf("actual_at: %w", err)
	}
	if pair.Actual == "" && e.Result == "violated" && len(pair.Expected) > 0 &&
		pair.EquivalentAt == "" && pair.EquivalentFlow == nil && pair.Proof == nil {
		if (e.ResultEvidence != nil && e.ResultEvidence.Flow != nil) || (e.WriteEvidence != nil && e.WriteEvidence.File != nil) {
			return fmt.Errorf("укажите actual для проверяемой цепочки")
		}
		return nil
	}
	if e.Check == "result" && !isGoRef(declaration) && e.ResultEvidence != nil &&
		e.ResultEvidence.Flow == nil && pair.Proof == nil && pair.EquivalentFlow == nil && pair.EquivalentAt == "" {
		_, numberErr := strconv.ParseFloat(pair.Actual, 64)
		_, stringErr := strconv.Unquote(pair.Actual)
		if (numberErr == nil && strings.ContainsAny(pair.Actual, "0123456789")) || stringErr == nil {
			return checkScalarReturn(e, declaration, line, source)
		}
	}
	if pair.Actual == "" || !strings.ContainsFunc(pair.Actual, unicode.IsLetter) ||
		strings.ContainsFunc(pair.Actual, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) && r != '_' && r != '$'
		}) {
		return fmt.Errorf("actual: укажите точный идентификатор фактического объекта")
	}
	if !containsIdentifier(line, pair.Actual) {
		return fmt.Errorf("actual_at: идентификатор %q должен быть виден в %s", pair.Actual, pair.ActualAt)
	}
	structuredWrite := e.Check == "writes" && e.WriteEvidence != nil && (e.WriteEvidence.File != nil || pair.Proof != nil)
	if !structuredWrite && !containsIdentifier(observed, pair.Actual) {
		return fmt.Errorf("actual: идентификатор %q должен быть виден в %s и в описании факта", pair.Actual, pair.ActualAt)
	}
	created := e.Check == "result" && e.Result == "checked" && e.ResultEvidence != nil &&
		namesResultOrWrite(e.Check, nameBeforeSource(words)) && isGoRef(declaration)
	if e.Check == "writes" && e.WriteEvidence != nil && e.WriteEvidence.File != nil {
		if p := pair.Proof; p != nil && (p.Assignment != nil || p.API != nil || p.CLI != nil || p.Reference != nil || p.SQL != nil || p.Setting != nil) {
			return fmt.Errorf("writes: выберите один способ доказательства: write_evidence.file или object_comparison.proof")
		}
		return checkNameFileWrite(e, declaration, source)
	}
	if pair.Proof != nil {
		if pair.EquivalentAt != "" || pair.EquivalentFlow != nil {
			return fmt.Errorf("proof: используйте одну форму соответствия; цепочку запишите в result_evidence.flow")
		}
		if created {
			return errors.Join(checkCreatedResult(e, declaration, source), checkNameObjectProof(e, declaration, source))
		}
		return checkNameObjectProof(e, declaration, source)
	}
	if e.Check == "result" && e.ResultEvidence != nil && e.ResultEvidence.Flow != nil {
		if pair.EquivalentFlow != nil {
			return fmt.Errorf("укажите путь результата один раз в result_evidence.flow")
		}
		if err := checkNameResultFlow(e, declaration, source, e.ResultEvidence.Flow, !created && e.Result == "checked" && len(pair.Expected) != 0 && pair.EquivalentAt == ""); err != nil {
			return err
		}
		if created {
			return checkCreatedResult(e, declaration, source)
		}
		if e.Result == "checked" && len(pair.Expected) != 0 && pair.EquivalentAt != "" {
			return checkNameEquivalence(pair, source)
		}
		return nil
	}
	if pair.EquivalentFlow != nil {
		if e.Result != "checked" || e.Check != "result" || len(pair.Expected) == 0 || pair.EquivalentAt != "" {
			return fmt.Errorf("equivalent_flow требует checked по result и expected")
		}
		if err := checkNameResultFlow(e, declaration, source, pair.EquivalentFlow, !created); err != nil {
			return err
		}
		if created {
			return checkCreatedResult(e, declaration, source)
		}
		return nil
	}
	if created {
		return checkCreatedResult(e, declaration, source)
	}
	if e.Result == "checked" && len(pair.Expected) > 0 &&
		!sameObjectName(pair.Expected, identifierWords(pair.Actual)) {
		return checkNameEquivalence(pair, source)
	}
	return nil
}

func checkExpectedObject(expected []string, word string, words []string) error {
	complete := expectedNameWords(words)
	if !slices.Equal(expected, complete) {
		return fmt.Errorf("expected: имя %q называет объект для проверки; укажите полный состав %q без сокращений", strings.Join(words, " "), strings.Join(complete, " "))
	}
	if word == "" && len(expected) == 0 {
		return nil
	}
	if word == "" || !slices.Contains(expected, word) {
		return fmt.Errorf("expected должен содержать выбранное слово %q", word)
	}
	return nil
}

func expectedNameWords(words []string) []string {
	words = nameBeforeSource(words)
	if len(words) == 0 {
		return words
	}
	switch strings.ToLower(words[0]) {
	case "add", "build", "create", "delete", "export", "fetch", "find", "format",
		"get", "init", "initialize", "insert", "list", "load", "lookup", "make", "mark",
		"new", "parse", "put", "read", "remove", "render", "save", "set",
		"sign", "start", "stop", "update", "validate", "verify", "write":
		return words[1:]
	default:
		return words
	}
}

func nameBeforeSource(words []string) []string {
	for i := 1; i < len(words)-1; i++ {
		if strings.EqualFold(words[i], "from") || strings.EqualFold(words[i], "by") {
			return words[:i]
		}
	}
	return words
}

func namesResultOrWrite(check string, words []string) bool {
	if len(words) < 2 {
		return false
	}
	switch strings.ToLower(words[1]) {
	case "if", "when", "unless":
		return false
	}
	switch strings.ToLower(words[0]) {
	case "create":
		return check == "result"
	case "add", "set":
		return check == "writes"
	default:
		return false
	}
}

func sameObjectName(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	last := len(expected) - 1
	for i, word := range expected {
		if strings.EqualFold(word, actual[i]) {
			continue
		}
		if i == last && (strings.EqualFold(word+"s", actual[i]) || strings.EqualFold(word, actual[i]+"s")) {
			continue
		}
		return false
	}
	return true
}

func checkNameEquivalence(pair *NameObjectComparison, source *evidenceSource) error {
	expected := strings.Join(pair.Expected, " ")
	if pair.EquivalentAt == "" {
		return fmt.Errorf("для checked укажите equivalent_at или equivalent_flow: где %q и %q обозначают один объект", expected, pair.Actual)
	}
	line, err := source.line(pair.EquivalentAt)
	if err != nil {
		return fmt.Errorf("equivalent_at: %w", err)
	}
	line = strings.ToLower(line)
	actual := strings.ToLower(pair.Actual)
	for _, sep := range []string{"", " ", "_", "-"} {
		term := strings.ToLower(strings.Join(pair.Expected, sep))
		independent := strings.ReplaceAll(strings.ReplaceAll(line, term+"."+actual, " "), actual+"."+term, " ")
		if containsIdentifier(independent, term) && containsIdentifier(independent, actual) {
			return nil
		}
	}
	return fmt.Errorf("equivalent_at %s: строка должна называть %q и %q", pair.EquivalentAt, expected, pair.Actual)
}

func checkNameResultFlow(e Evidence, declaration string, source *evidenceSource, flow *NameResultFlow, checkMeaning bool) error {
	pair, result := e.ObjectComparison, e.ResultEvidence
	if !isGoRef(declaration) {
		return fmt.Errorf("equivalent_flow: для этого языка укажите связь в коде или контракте через equivalent_at")
	}
	g := newQueryGoSource(source)
	producer, err := g.declaration(declaration)
	if err != nil {
		return err
	}
	slot, err := g.nameReturnSlot(producer, result.ReturnAt, pair.Actual)
	if err != nil {
		return err
	}
	caller, call, parents, err := g.nameResultCall(producer, flow.Caller, flow.CallAt, flow.ReceiverAt)
	if err != nil {
		return err
	}
	value := flow.Value
	for _, hop := range flow.Forward {
		ret, block, err := g.nameForwardReturn(caller, hop.ReturnAt)
		if err != nil {
			return err
		}
		slot = nameFlowIndex(call, parents, ret, block, ret.Results, value, slot)
		if slot < 0 {
			return fmt.Errorf("flow: %s должен возвращать то же значение из %s", hop.ReturnAt, caller.node.Name.Name)
		}
		caller, call, parents, err = g.nameResultCall(caller, hop.Caller, hop.CallAt, hop.ReceiverAt)
		if err != nil {
			return err
		}
		value = hop.Value
	}
	if flow.UseExpression != "" {
		if flow.UseKind != "" || flow.UseField != "" || flow.Consumer != "" || flow.Projection != nil {
			return fmt.Errorf("use_expression: выберите выражение использования без use_kind, use_field, consumer и projection")
		}
		if err := g.checkNameUseExpression(caller, call, parents, value, slot, NameCodeUse{At: result.ConsumerAt, Expression: flow.UseExpression}); err != nil {
			return err
		}
		if checkMeaning && !sameObjectName(pair.Expected, identifierWords(pair.Actual)) {
			return checkNameEquivalence(pair, source)
		}
		return nil
	}
	if flow.UseKind != "" || flow.UseField != "" {
		if err := g.checkLocalNameUse(caller, call, parents, value, slot, result.ConsumerAt, flow); err != nil {
			return err
		}
		if checkMeaning && !sameObjectName(pair.Expected, identifierWords(pair.Actual)) {
			return checkNameEquivalence(pair, source)
		}
		return nil
	}
	consumer, err := g.declaration(flow.Consumer)
	if err != nil {
		return err
	}
	use, useParents, err := g.call(caller, result.ConsumerAt, func(expr ast.Expr) bool {
		return calledName(expr) == consumer.node.Name.Name
	})
	if err != nil {
		return err
	}
	if flow.Projection != nil {
		return g.checkNameProjection(producer, caller, consumer, call, parents, use, useParents, value, slot, flow.Projection, pair.Expected, checkMeaning)
	}
	if err := g.receiver(caller, consumer, use, ""); err != nil {
		return err
	}
	i := nameConsumerArgument(call, parents, use, useParents, value, slot)
	if i < 0 {
		return fmt.Errorf("flow: вызов найден, передачу значения от %s к %s подтвердить не удалось; покажите, как результат поступает в вызов, начиная с %s", producer.node.Name.Name, consumer.node.Name.Name, result.ConsumerAt)
	}
	if !checkMeaning {
		return nil
	}
	t := nameParameterType(consumer, i)
	if !sameObjectName(pair.Expected, identifierWords(t)) {
		return fmt.Errorf("equivalent_flow: тип параметра потребителя %q не подтверждает %q", t, strings.Join(pair.Expected, " "))
	}
	return nil
}

func (g *queryGoSource) nameReturnSlot(fn queryFunc, ref, actual string) (int, error) {
	ret, _, err := g.nameForwardReturn(fn, ref)
	if err != nil {
		return -1, err
	}
	if slot := slices.IndexFunc(ret.Results, func(expr ast.Expr) bool { return nameReturnedObject(expr) == actual }); slot >= 0 {
		return slot, nil
	}
	return -1, fmt.Errorf("equivalent_flow: %s должен возвращать %q из проверяемой функции", ref, actual)
}

func nameConsumerArgument(call *ast.CallExpr, parents []ast.Node, use *ast.CallExpr, useParents []ast.Node, value string, slot int) int {
	var block ast.Node
	if len(useParents) >= 2 {
		block = useParents[len(useParents)-2]
	}
	return nameFlowIndex(call, parents, use, block, use.Args, value, slot)
}

func nameFlowIndex(call *ast.CallExpr, parents []ast.Node, use ast.Node, useBlock ast.Node, operands []ast.Expr, value string, slot int) int {
	if value == "" {
		if _, returns := use.(*ast.ReturnStmt); returns && len(operands) == 1 && operands[0] == call {
			return slot
		}
		if slot == 0 {
			return slices.Index(operands, ast.Expr(call))
		}
		return -1
	}
	if !nameBoundResult(call, parents, use, useBlock, value, slot) {
		return -1
	}
	return slices.IndexFunc(operands, func(arg ast.Expr) bool { return nameIdentifier(arg) == value })
}

func nameBoundResult(call *ast.CallExpr, parents []ast.Node, use ast.Node, useBlock ast.Node, value string, slot int) bool {
	if value == "_" || len(parents) < 2 {
		return false
	}
	assign, ok := parents[len(parents)-1].(*ast.AssignStmt)
	if !ok || (assign.Tok != token.DEFINE && assign.Tok != token.ASSIGN) || len(assign.Rhs) != 1 || assign.Rhs[0] != call ||
		slot >= len(assign.Lhs) || nameIdentifier(assign.Lhs[slot]) != value {
		return false
	}
	block := parents[len(parents)-2]
	if block == useBlock {
		return nameValueUnchanged(block, assign, use, value)
	}
	if block.Pos() <= use.Pos() && use.End() <= block.End() {
		return nameValueUnchangedWithin(block, assign, use, value)
	}
	return nameAssignmentThroughBranch(parents, assign, useBlock, use, value)
}

func nameValueUnchanged(block ast.Node, from, to ast.Node, value string) bool {
	if from.End() >= to.Pos() {
		return false
	}
	statements := nameStatements(block)
	if statements == nil {
		return false
	}
	for _, stmt := range statements {
		if stmt.Pos() <= from.Pos() || stmt.Pos() >= to.Pos() {
			continue
		}
		for node := range ast.Preorder(stmt) {
			switch n := node.(type) {
			case *ast.AssignStmt:
				if slices.ContainsFunc(n.Lhs, func(expr ast.Expr) bool { return nameIdentifier(expr) == value }) {
					return false
				}
			case *ast.DeclStmt:
				if nameDeclaresLocal(n, value) {
					return false
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND && nameIdentifier(n.X) == value {
					return false
				}
			}
		}
	}
	return true
}

func nameStatements(block ast.Node) []ast.Stmt {
	switch n := block.(type) {
	case *ast.BlockStmt:
		return n.List
	case *ast.CaseClause:
		return n.Body
	}
	return nil
}

func nameIdentifier(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func nameParameterType(fn queryFunc, index int) string {
	for _, field := range fn.node.Type.Params.List {
		count := max(1, len(field.Names))
		if index >= 0 && index < count {
			expr := field.Type
			if ptr, ok := expr.(*ast.StarExpr); ok {
				expr = ptr.X
			}
			return calledName(expr)
		}
		index -= count
	}
	return ""
}

func checkNameWord(word *string, words []string) error {
	if word == nil || len(words) == 0 {
		return fmt.Errorf("укажите слово точного имени или пустую строку, если имя его не содержит")
	}
	if *word != "" && !slices.Contains(words, *word) {
		return fmt.Errorf("слово %q отсутствует в имени", *word)
	}
	return nil
}

func checkNameMeaning(e Evidence, words []string, source *evidenceSource) error {
	if len(words) == 0 {
		return fmt.Errorf("укажите точное имя в symbol")
	}
	line, err := source.line(e.Location)
	if err != nil {
		return err
	}
	location := strings.TrimPrefix(e.Location, "base@")
	file := location[:strings.LastIndexByte(location, ':')]
	if !containsIdentifier(line, e.Symbol) &&
		e.Symbol != strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)) &&
		e.Symbol != filepath.Base(filepath.Dir(file)) {
		return fmt.Errorf("имя %q отсутствует в %s и имени модуля", e.Symbol, e.Location)
	}
	unmatched := slices.Clone(words)
	for _, check := range e.WordChecks {
		i := slices.Index(unmatched, check.Word)
		if i < 0 {
			return fmt.Errorf("слово %q лишнее или повторено в word_checks", check.Word)
		}
		unmatched = slices.Delete(unmatched, i, i+1)
		if strings.TrimSpace(check.Meaning) == "" || strings.TrimSpace(check.Fact) == "" {
			return fmt.Errorf("для слова %q укажите смысл и факт", check.Word)
		}
		if check.Result != "" && check.Result != "checked" && (check.Result != "violated" || e.Result != "violated") {
			return fmt.Errorf("статус слова %q не согласуется с результатом meaning", check.Word)
		}
		if _, err := source.line(check.Location); err != nil {
			return fmt.Errorf("слово %q: %w", check.Word, err)
		}
	}
	if len(unmatched) != 0 {
		return fmt.Errorf("в word_checks пропущены слова %s", strings.Join(unmatched, ", "))
	}
	return nil
}

func identifierWords(name string) []string {
	runes := []rune(name)
	var words []string
	start := 0
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) {
			if i > start {
				words = append(words, string(runes[start:i]))
			}
			start = i + 1
			continue
		}
		if i > start && unicode.IsUpper(r) &&
			(unicode.IsLower(runes[i-1]) || unicode.IsNumber(runes[i-1]) ||
				(unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) &&
					!pluralInitialismEnd(runes[i+1:]))) {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	if start < len(runes) {
		words = append(words, string(runes[start:]))
	}
	if len(words) == 0 && name != "" {
		return []string{name}
	}
	return words
}

func pluralInitialismEnd(suffix []rune) bool {
	return suffix[0] == 's' &&
		(len(suffix) == 1 || (!unicode.IsLower(suffix[1]) && !unicode.IsMark(suffix[1])))
}

func containsIdentifier(line, name string) bool {
	isPart := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_' || r == '$'
	}
	for from := 0; from < len(line); {
		i := strings.Index(line[from:], name)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(name)
		left, _ := utf8.DecodeLastRuneInString(line[:start])
		right, _ := utf8.DecodeRuneInString(line[end:])
		if !isPart(left) && !isPart(right) {
			return true
		}
		from = end
	}
	return false
}

type evidenceSource struct {
	root       string
	reportPath string
	output     string
	base       string
	taskText   string
	files      *os.Root
	cache      map[string][]string
	names      map[string]bool
}

func evidenceLocation(ref string) (path string, line int, fromBase bool, err error) {
	location := strings.TrimPrefix(ref, "base@")
	at := strings.LastIndexByte(location, ':')
	if at < 0 {
		return "", 0, false, fmt.Errorf("укажите ссылку файл:строка, получено %q", ref)
	}
	path = location[:at]
	line, err = strconv.Atoi(location[at+1:])
	if err != nil || line < 1 || !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return "", 0, false, fmt.Errorf("некорректная ссылка на код %q", ref)
	}
	return path, line, location != ref, nil
}

func (s *evidenceSource) line(ref string) (string, error) {
	path, line, fromBase, err := evidenceLocation(ref)
	if err != nil {
		return "", err
	}
	key := ref[:strings.LastIndexByte(ref, ':')]
	lines, ok := s.cache[key]
	if !ok {
		body, err := s.read(path, fromBase)
		if err != nil {
			return "", fmt.Errorf("%s: %w", ref, err)
		}
		lines = strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		if body == "" {
			lines = nil
		}
		s.cache[key] = lines
	}
	if line > len(lines) {
		return "", fmt.Errorf("строка %d отсутствует в %s", line, key)
	}
	return lines[line-1], nil
}

func (s *evidenceSource) read(path string, fromBase bool) (string, error) {
	if path == s.output || excluded(s.root, s.reportPath, path) {
		return "", fmt.Errorf("файл не входит в снимок кода")
	}
	if fromBase {
		return gitRaw(s.root, "cat-file", "blob", s.base+":"+path)
	}
	if s.names == nil {
		out, err := gitRaw(s.root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
		if err != nil {
			return "", err
		}
		s.names = make(map[string]bool)
		for _, name := range strings.Split(out, "\x00") {
			s.names[name] = true
		}
	}
	if !s.names[path] {
		return "", fmt.Errorf("файл отсутствует в снимке кода")
	}
	info, err := s.files.Lstat(path)
	if os.IsNotExist(err) {
		flags, e := gitRaw(s.root, "ls-files", "-v", "--", ":(literal)"+path)
		if e == nil && (strings.HasPrefix(flags, "S ") || strings.HasPrefix(flags, "s ")) {
			return gitRaw(s.root, "cat-file", "blob", ":"+path)
		}
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("ссылка должна вести в обычный файл")
	}
	body, err := s.files.ReadFile(path)
	return string(body), err
}

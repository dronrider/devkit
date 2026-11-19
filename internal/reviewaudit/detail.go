package reviewaudit

import (
	"errors"
	"fmt"
	"go/ast"
	"slices"
	"strings"
)

func checkDetailedFacts(id string, entry Evidence, source *evidenceSource) error {
	switch id {
	case moduleRule:
		return checkModuleFacts(entry, source)
	case testStrengthRule:
		if entry.Check == "overlap" {
			return checkTestComparison(entry, source)
		}
	case flowRule:
		return checkFlowFacts(entry)
	case failureRule:
		return checkFailureFacts(entry, source)
	}
	return nil
}

func checkModuleFacts(entry Evidence, source *evidenceSource) error {
	facts := entry.Facts
	implementation, placement := facts["implementation"], facts["placement"]
	if len(implementation) != 1 || len(placement) != 1 || strings.TrimSpace(entry.Symbol) == "" {
		return fmt.Errorf("укажите symbol, одну реализацию и одно решение facts.placement для этой операции")
	}
	decl := implementation[0]
	if strings.TrimSpace(decl.Quote) == "" || !containsIdentifier(decl.Quote, entry.Symbol) {
		return fmt.Errorf("facts.implementation: процитируйте объявление symbol %q", entry.Symbol)
	}
	if isGoRef(decl.Location) {
		fn, err := newQueryGoSource(source).declaration(decl.Location)
		if err != nil || fn.node.Name.Name != entry.Symbol {
			return fmt.Errorf("facts.implementation: %s должна указывать на объявление Go-функции %s", decl.Location, entry.Symbol)
		}
	}
	for _, dep := range facts["dependency"] {
		if !slices.Contains([]string{"domain", "shared"}, dep.Verdict) {
			return fmt.Errorf("facts.dependency: укажите verdict: domain или shared и приведите зависимость или её отсутствие")
		}
	}
	for _, caller := range facts["caller"] {
		line, err := source.line(caller.Location)
		if err != nil {
			return err
		}
		if caller.Location == decl.Location || (!containsIdentifier(line, entry.Symbol) && caller.Verdict != "searched") {
			return fmt.Errorf("facts.caller: нужна отдельная строка использования %s либо место поиска, если вызовов нет", entry.Symbol)
		}
	}
	if strings.TrimSpace(placement[0].Value) == "" {
		return fmt.Errorf("facts.placement: укажите модуль и решение о размещении в value и text")
	}
	return nil
}

func checkTestComparison(entry Evidence, source *evidenceSource) error {
	facts := entry.Facts
	for _, role := range []string{"first", "second"} {
		for _, assertion := range facts[role] {
			if err := checkTestAssertion(assertion, source); err != nil {
				return fmt.Errorf("facts.%s: %w", role, err)
			}
		}
	}
	level := func(role string) (string, error) {
		rows := facts[role]
		if len(rows) == 1 && slices.Contains([]string{"unit", "component", "integration", "system"}, rows[0].Value) {
			return rows[0].Value, nil
		}
		return "", fmt.Errorf("facts.%s: укажите уровень unit, component, integration или system и фактическую зависимость", role)
	}
	first, err := level("first_level")
	if err != nil {
		return err
	}
	if entry.Kind == "cross_level" || entry.Kind == "pair" {
		second, err := level("second_level")
		if err != nil {
			return err
		}
		if (entry.Kind == "cross_level") == (first == second) {
			return fmt.Errorf("kind: cross_level нужен для пары на разных уровнях, kind: pair для одного уровня")
		}
		if len(facts["first"]) != 1 || len(facts["second"]) != 1 || facts["first"][0].Related == facts["second"][0].Related ||
			facts["first"][0].Location == facts["second"][0].Location {
			return fmt.Errorf("укажите два разных теста и два конкретных утверждения")
		}
		if entry.Kind == "cross_level" {
			return nil
		}
	}
	if entry.Kind == "single" && len(facts["first"]) != 1 {
		return fmt.Errorf("facts.first: укажите одно конкретное утверждение по проверяемому свойству")
	}
	for _, search := range facts["search"] {
		if search.Value != first && slices.Contains([]string{"unit", "component", "integration", "system"}, search.Value) &&
			search.Location != facts["first"][0].Related && search.Location != facts["first"][0].Location {
			return nil
		}
	}
	return fmt.Errorf("facts.search: укажите, где искали то же свойство на другом уровне")
}

func checkTestAssertion(assertion ReviewFact, source *evidenceSource) error {
	path, line, base, err := evidenceLocation(assertion.Location)
	if err != nil {
		return err
	}
	testPath, testLine, testBase, err := evidenceLocation(assertion.Related)
	if err != nil {
		return err
	}
	if assertion.Via == "" && (path != testPath || base != testBase || line < testLine) {
		return fmt.Errorf("утверждение должно находиться в указанном тесте или в его вспомогательной функции")
	}
	if !isGoRef(assertion.Related) {
		if assertion.Via == "" {
			return nil
		}
		_, err := source.line(assertion.Via)
		return err
	}
	goSource := newQueryGoSource(source)
	test, err := goSource.declaration(assertion.Related)
	if err != nil {
		return err
	}
	owner := test
	if assertion.Via != "" {
		owner, err = goSource.declaration(assertion.Via)
		if err != nil {
			return err
		}
		callsHelper := false
		ast.Inspect(test.node.Body, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok && calledName(call.Fun) == owner.node.Name.Name {
				callsHelper = true
			}
			return !callsHelper
		})
		if !callsHelper {
			return fmt.Errorf("тест должен вызывать вспомогательную функцию с утверждением")
		}
	}
	if owner.file != path || owner.base != base || goSource.fset.Position(owner.node.Pos()).Line > line ||
		goSource.fset.Position(owner.node.End()).Line < line {
		return fmt.Errorf("утверждение должно находиться в теле указанного теста или его вспомогательной функции")
	}
	return nil
}

func checkFlowFacts(entry Evidence) error {
	steps := make(map[string]bool)
	for _, step := range entry.Facts["step"] {
		steps[step.Location] = false
	}
	for _, use := range entry.Facts["use"] {
		if _, ok := steps[use.Related]; !ok {
			return fmt.Errorf("facts.use: related должен указывать на шаг этой цепочки")
		}
		if strings.TrimSpace(use.Value) == "" || !slices.Contains([]string{"used", "unused", "effect"}, use.Verdict) {
			return fmt.Errorf("facts.use: назовите значение или эффект и укажите verdict: used, unused или effect")
		}
		steps[use.Related] = true
	}
	for _, step := range entry.Facts["step"] {
		if !steps[step.Location] {
			return fmt.Errorf("facts.use: для шага %s укажите потребителя, возврат или объясните неиспользуемый результат", step.Location)
		}
	}
	for _, role := range []string{"return_value", "return_error"} {
		for _, fact := range entry.Facts[role] {
			if strings.TrimSpace(fact.Value) == "" {
				return fmt.Errorf("facts.%s: назовите возвращаемое значение или исход отказа в value", role)
			}
		}
	}
	return nil
}

func checkFailureFacts(entry Evidence, source *evidenceSource) error {
	facts := entry.Facts
	classification, definitions := facts["classification"], facts["classification_definition"]
	if len(classification) != 1 || len(definitions) != 1 {
		return fmt.Errorf("укажите первую классификацию и строку её определения")
	}
	first, definition := classification[0], definitions[0]
	line, err := source.line(first.Location)
	if err != nil {
		return err
	}
	if err := checkFactQuote(first.Quote, line); err != nil {
		return fmt.Errorf("facts.classification: %w", err)
	}
	switch first.Verdict {
	case "match", "mismatch", "forwarded":
	default:
		return fmt.Errorf("facts.classification: укажите verdict: match, mismatch или forwarded")
	}
	if first.Verdict != "forwarded" &&
		(first.Location == entry.Object || first.Location == definition.Location || strings.TrimSpace(first.Value) == "" ||
			!containsIdentifier(first.Quote, first.Value) || !containsIdentifier(definition.Quote, first.Value)) {
		return fmt.Errorf("facts.classification: укажите строку присвоения после вызова и определение той же классификации")
	}
	for _, boundary := range facts["boundary"] {
		if strings.TrimSpace(boundary.Value) == "" {
			return fmt.Errorf("facts.boundary: назовите итоговый публичный код или исход для вызывающего в value")
		}
	}
	bad := first.Verdict == "mismatch"
	for _, cause := range facts["cause"] {
		if !slices.Contains([]string{"preserved", "lost", "absent"}, cause.Verdict) {
			return fmt.Errorf("facts.cause: укажите verdict: preserved, lost или absent")
		}
		bad = bad || cause.Verdict == "lost"
	}
	for _, message := range facts["message"] {
		if !slices.Contains([]string{"once", "repeated"}, message.Verdict) {
			return fmt.Errorf("facts.message: укажите verdict: once или repeated и путь формирования текста")
		}
		bad = bad || message.Verdict == "repeated"
	}
	if bad && entry.Result == "checked" {
		return fmt.Errorf("checked противоречит выводу о классификации, причине или повторе сообщения")
	}
	return nil
}

func checkContractCases(rule Rule, source *evidenceSource) error {
	if rule.ID != contractRule {
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
		if err := checkContractCaseSet(byObject[object], source); err != nil {
			issues = append(issues, fmt.Errorf("%s / %s: %w", rule.ID, object, err))
		}
	}
	return errors.Join(issues...)
}

func checkContractCaseSet(entries map[string]Evidence, source *evidenceSource) error {
	schema, output, consumer := entries["schema"], entries["output"], entries["consumer"]
	if output.Result != "checked" && output.Result != "violated" {
		return nil
	}
	inputs := make(map[string]bool)
	for _, fact := range output.Facts["input"] {
		if strings.TrimSpace(fact.Case) == "" || inputs[fact.Case] {
			return fmt.Errorf("output: у каждого входа нужен отдельный case")
		}
		inputs[fact.Case] = true
	}
	declared := make(map[string]ReviewFact)
	if schema.Result == "checked" || schema.Result == "violated" {
		for _, fact := range schema.Facts["schema"] {
			if strings.TrimSpace(fact.Case) == "" || len(fact.Forms) == 0 || declared[fact.Case].Case != "" {
				return fmt.Errorf("schema: у каждого case перечислите допустимые forms")
			}
			declared[fact.Case] = fact
		}
	}
	actual := make(map[string]bool)
	for _, fact := range output.Facts["output"] {
		if !inputs[fact.Case] || actual[fact.Case] || len(fact.Forms) != 1 || strings.TrimSpace(fact.Forms[0]) == "" {
			return fmt.Errorf("output: для каждого входного case укажите одну фактическую форму")
		}
		actual[fact.Case] = true
		if len(declared) == 0 {
			continue
		}
		def, ok := declared[fact.Case]
		if !ok {
			return fmt.Errorf("schema: отсутствует case %s из output", fact.Case)
		}
		if !slices.Contains(def.Forms, fact.Forms[0]) && schema.Result == "checked" && !hasContractException(schema.Facts["exception"], def, fact, source) {
			return fmt.Errorf("schema: case %s выдаёт %s вне forms, укажите ссылку из объявления на исключение либо отметьте расхождение", fact.Case, fact.Forms[0])
		}
	}
	if len(inputs) != len(actual) || (len(declared) > 0 && len(declared) != len(actual)) {
		return fmt.Errorf("schema и output: разберите одинаковые case в объявлении, входе и результате")
	}
	open := make(map[string]bool)
	for _, question := range consumer.Facts["question"] {
		if !actual[question.Case] || open[question.Case] {
			return fmt.Errorf("consumer: привяжите facts.question к тому же case, что и output")
		}
		open[question.Case] = true
	}
	for _, fact := range output.Facts["compare"] {
		if !actual[fact.Case] || strings.TrimSpace(fact.Value) == "" || !slices.Contains([]string{"same", "different"}, fact.Verdict) {
			return fmt.Errorf("output: facts.compare требует тот же case, результат второй операции и verdict: same или different")
		}
		if fact.Verdict == "different" && consumer.Result != "violated" && !open[fact.Case] {
			return fmt.Errorf("consumer: для расхождения в case %s укажите нарушение или вопрос о контракте", fact.Case)
		}
	}
	for _, fact := range consumer.Facts["contract"] {
		if !actual[fact.Case] {
			return fmt.Errorf("consumer: привяжите facts.contract к тому же case, что и output")
		}
		delete(actual, fact.Case)
	}
	for name := range open {
		delete(actual, name)
	}
	if len(actual) > 0 && consumer.Result != "not_applicable" {
		return fmt.Errorf("consumer: нужен вывод по каждому case из output")
	}
	return nil
}

func hasContractException(exceptions []ReviewFact, schema, output ReviewFact, source *evidenceSource) bool {
	for _, exception := range exceptions {
		if exception.Case != output.Case || exception.Related != schema.Location || exception.Location == schema.Location ||
			!slices.Contains(exception.Forms, output.Forms[0]) || strings.TrimSpace(exception.Value) == "" {
			continue
		}
		line, err := source.line(schema.Location)
		if err == nil && strings.Contains(line, exception.Value) {
			return true
		}
	}
	return false
}

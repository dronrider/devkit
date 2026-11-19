package reviewaudit

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
)

const (
	configOptionRule = "common-architecture:ARCH-023"
	moduleRule       = "common-architecture:ARCH-003"
	contractRule     = "common-architecture:ARCH-005"
	selectionRule    = "common-architecture:ARCH-015"
	readRule         = "common-architecture:ARCH-016"
	dataBoundaryRule = "common-architecture:ARCH-019"
	failureRule      = "common-architecture:ARCH-014"
	flowRule         = "common-style:STYLE-011"
	descriptionRule  = "common-style:STYLE-016"
	testLevelRule    = "common-tests:TEST-004"
	testStrengthRule = "common-tests:TEST-005"
)

type ReviewFact struct {
	Location   string            `json:"location"`
	Text       string            `json:"text"`
	Related    string            `json:"related,omitempty"`
	Quote      string            `json:"quote,omitempty"`
	Failure    *bool             `json:"failure,omitempty"`
	Parameter  string            `json:"parameter,omitempty"`
	SQLLiteral *SQLFilterLiteral `json:"sql_literal,omitempty"`
	Case       string            `json:"case,omitempty"`
	Value      string            `json:"value,omitempty"`
	Forms      []string          `json:"forms,omitempty"`
	Verdict    string            `json:"verdict,omitempty"`
	Via        string            `json:"via,omitempty"`
}

// A role names the evidence the reviewer must provide, independently of language.
var reviewFactRoles = map[string]map[string][]string{
	configOptionRule: {
		"behavior": {"input", "before", "after", "activation"},
		"basis":    {"contract"},
	},
	moduleRule: {"": {"implementation", "caller", "dependency", "change", "placement"}},
	contractRule: {
		"schema":   {"schema"},
		"output":   {"input", "output"},
		"consumer": {"contract"},
	},
	selectionRule: {
		"usage": {"query", "consumer"},
		"order": {"order", "effect"},
	},
	readRule: {"with_consumer": nil, "without_consumer": nil},
	dataBoundaryRule: {
		"parameters":       {"filter"},
		"binding":          {"sql", "input", "argument"},
		"proposed_binding": nil,
	},
	failureRule:     {"": {"origin", "classification", "classification_definition", "boundary", "cause", "message"}},
	flowRule:        {"": {"entry", "condition", "step", "use", "outcome", "control", "return_value", "return_error"}},
	commentRule:     {"": nil},
	descriptionRule: {"": {"statement", "implementation"}},
	testLevelRule:   {"": {"test", "dependency", "unit_selection", "integration_selection"}},
	testStrengthRule: {
		"strength": {"assertion", "boundary", "failure_case"},
		"overlap":  {"first", "first_level"},
	},
}

func requiresReviewFacts(id, check string) bool {
	_, ok := reviewFactRoles[id][check]
	return ok
}

func checkReviewFacts(root, reportPath, output, auditPath string, report Report, taskText string) error {
	files, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: reportPath, output: output, base: report.BaseSHA, taskText: taskText, files: files, cache: make(map[string][]string)}
	var issues []error
	failures := make(map[string]bool)
	var flowFailures []string
	for _, rule := range report.Rules {
		issues = append(issues, checkOptionAndBindingPairs(rule, source, auditPath, report.CodeHash))
		issues = append(issues, checkContractCases(rule, source))
		issues = append(issues, checkReadInputs(rule))
		for _, entry := range rule.Evidence {
			if !requiresReviewFacts(rule.ID, entry.Check) {
				continue
			}
			if rule.ID == failureRule && entry.Result != "not_applicable" {
				failures[entry.Object] = true
			}
			if err := checkReviewEntry(rule.ID, entry, source); err != nil {
				issues = append(issues, fmt.Errorf("%s / %s: %w", rule.ID, evidenceKey{entry.Object, entry.Check}, err))
				continue
			}
			if rule.ID == flowRule && entry.Result != "not_applicable" {
				for _, step := range entry.Facts["step"] {
					if step.Failure != nil && *step.Failure {
						flowFailures = append(flowFailures, step.Location)
					}
				}
			}
		}
	}
	seen := make(map[string]bool)
	for _, loc := range flowFailures {
		if !seen[loc] && !failures[loc] {
			issues = append(issues, fmt.Errorf("%s / %s: для отказа из STYLE-011 нужна отдельная запись", failureRule, loc))
		}
		seen[loc] = true
	}
	return errors.Join(issues...)
}

func checkReviewEntry(id string, entry Evidence, source *evidenceSource) error {
	if id == readRule {
		if err := checkReadScenario(entry, source); err != nil {
			return err
		}
	}
	if entry.Result == "not_applicable" {
		if id == configOptionRule && entry.Check == "basis" {
			return fmt.Errorf("для основания выбора укажите checked, violated или pending")
		}
		if id == testStrengthRule && entry.Check == "overlap" {
			return fmt.Errorf("для сравнения без пары укажите kind: single и места поиска")
		}
		_, err := source.line(entry.Location)
		return err
	}
	roles, err := reviewEntryRoles(id, entry)
	if err != nil {
		return err
	}
	required := roles
	if entry.Result == "pending" {
		required = []string{"question"}
		roles = append(slices.Clone(roles), "question")
		if id == failureRule {
			required = append(required, "origin")
		}
		if id == commentRule {
			required = append(required, "claim")
		}
	}
	if id == contractRule && entry.Check == "schema" {
		roles = append(slices.Clone(roles), "exception")
	}
	if id == contractRule && entry.Check == "output" {
		roles = append(slices.Clone(roles), "compare")
	}
	if id == configOptionRule && entry.Check == "basis" {
		roles = append(slices.Clone(roles), "necessity")
	}
	for _, role := range required {
		if len(entry.Facts[role]) == 0 {
			return fmt.Errorf("заполните facts.%s", role)
		}
	}
	var issues []error
	for _, role := range roles {
		for _, fact := range entry.Facts[role] {
			if err := checkReviewFact(role, fact, source); err != nil {
				issues = append(issues, fmt.Errorf("facts.%s: %w", role, err))
			}
		}
	}
	if id == failureRule && (len(entry.Facts["origin"]) != 1 || entry.Facts["origin"][0].Location != entry.Object) {
		issues = append(issues, fmt.Errorf("object должен совпадать с одной строкой вызова из facts.origin"))
	}
	if entry.Result != "pending" {
		if err := checkDetailedFacts(id, entry, source); err != nil {
			issues = append(issues, err)
		}
	}
	if id == commentRule && entry.Kind == "repeat" {
		for _, fact := range entry.Facts["repeat"] {
			if fact.Location == entry.Object {
				issues = append(issues, fmt.Errorf("facts.repeat должен ссылаться на второй комментарий"))
			}
		}
	}
	var unknown []string
	for role := range entry.Facts {
		if !slices.Contains(roles, role) {
			unknown = append(unknown, role)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		issues = append(issues, fmt.Errorf("неизвестные роли в facts: %s", strings.Join(unknown, ", ")))
	}
	return errors.Join(issues...)
}

func reviewEntryRoles(id string, entry Evidence) ([]string, error) {
	roles := reviewFactRoles[id][entry.Check]
	switch {
	case id == configOptionRule && entry.Check == "behavior":
		switch entry.Kind {
		case "list":
			return append(slices.Clone(roles), "disabled", "absent"), nil
		case "derived":
			return append(slices.Clone(roles), "derive", "omitted", "incompatible"), nil
		case "option":
			return roles, nil
		}
		if entry.Result != "pending" {
			return nil, fmt.Errorf("укажите kind: list, option или derived")
		}
	case id == testStrengthRule && entry.Check == "overlap":
		switch entry.Kind {
		case "pair", "cross_level":
			roles = append(slices.Clone(roles), "second", "second_level", "first_command", "second_command", "first_input", "second_input", "common_failure", "guarantees")
			if entry.Kind == "pair" {
				roles = append(roles, "search")
			}
			return roles, nil
		case "single":
			return append(slices.Clone(roles), "search"), nil
		}
		if entry.Result != "pending" {
			return nil, fmt.Errorf("укажите kind: pair, cross_level или single")
		}
	case id == commentRule && entry.Result == "checked":
		return []string{"purpose", "claim"}, nil
	case id == commentRule && entry.Result == "pending":
		return []string{"claim"}, nil
	case id == commentRule && entry.Result == "violated":
		switch entry.Kind {
		case "contradiction", "restatement", "repeat":
			return []string{entry.Kind, "claim"}, nil
		}
		return nil, fmt.Errorf("укажите kind: contradiction, restatement или repeat")
	}
	return roles, nil
}

func checkReviewFact(role string, fact ReviewFact, source *evidenceSource) error {
	if strings.TrimSpace(fact.Text) == "" {
		return fmt.Errorf("укажите наблюдение или вывод в text")
	}
	if role == "claim" && fact.Verdict == "pending" && fact.Location == "" {
		return nil
	}
	if role == "step" && fact.Failure == nil {
		return fmt.Errorf("укажите failure: влияет ли отказ этого шага на дальнейшее выполнение или ответ")
	}
	filterNeedsSource := role == "filter" && (fact.SQLLiteral == nil || fact.SQLLiteral.Source != "pending")
	if (filterNeedsSource || role == "argument" || role == "first" || role == "second") && fact.Related == "" {
		return fmt.Errorf("укажите связанную строку в related")
	}
	taskFilterSource := role == "filter" && fact.SQLLiteral != nil && fact.SQLLiteral.Source == "fixed" && strings.HasPrefix(fact.Related, "task:")
	if fact.Related != "" && !taskFilterSource {
		if _, err := source.line(fact.Related); err != nil {
			return fmt.Errorf("related: %w", err)
		}
	}
	if role == "contract" || role == "necessity" {
		return checkContractFact(fact, source)
	}
	line, err := source.line(fact.Location)
	if err != nil {
		return err
	}
	switch role {
	case "statement", "repeat", "assertion", "first", "second", "schema", "exception", "classification_definition", "derive", "implementation":
		if role == "implementation" && fact.Quote == "" {
			return nil
		}
		return checkFactQuote(fact.Quote, line)
	}
	return nil
}

func checkContractFact(fact ReviewFact, source *evidenceSource) error {
	switch {
	case strings.HasPrefix(fact.Location, "base@"):
		line, err := source.line(fact.Location)
		if err != nil {
			return err
		}
		return checkFactQuote(fact.Quote, line)
	case strings.HasPrefix(fact.Location, "task:"):
		number, err := strconv.Atoi(strings.TrimPrefix(fact.Location, "task:"))
		if err != nil || number < 1 || source.taskText == "" {
			return fmt.Errorf("укажите существующую строку постановки task:номер")
		}
		lines := strings.Split(source.taskText, "\n")
		if number > len(lines) {
			return fmt.Errorf("строка %d отсутствует в постановке", number)
		}
		return checkFactQuote(fact.Quote, lines[number-1])
	default:
		reference, err := url.Parse(fact.Location)
		if err == nil && reference.Scheme == "https" && reference.Hostname() != "" && strings.TrimSpace(fact.Quote) != "" {
			return nil
		}
		return fmt.Errorf("укажите цитату и независимое основание в base@файл:строка, task:номер или по ссылке HTTPS")
	}
}

func checkFactQuote(quote, line string) error {
	if strings.TrimSpace(quote) == "" || !strings.Contains(line, quote) {
		return fmt.Errorf("quote должна точно цитировать указанную строку")
	}
	return nil
}

package reviewaudit

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// EvidenceTemplates returns examples that use the same types as the audit reader.
// Empty fields are slots for evidence, not completed checks.
func EvidenceTemplates(dir, id, check, object, result, kind string) ([]Evidence, error) {
	if !strings.Contains(id, ":") {
		switch {
		case strings.HasPrefix(id, "ARCH-"):
			id = "common-architecture:" + id
		case strings.HasPrefix(id, "STYLE-"):
			id = "common-style:" + id
		case strings.HasPrefix(id, "TEST-"):
			id = "common-tests:" + id
		}
	}
	checks := objectChecks(id)
	if len(checks) == 0 {
		if _, known := reviewFactRoles[id]; !known && !tracksObjects(id) {
			return nil, fmt.Errorf("для %s нет шаблона доказательств", id)
		}
		checks = []string{""}
	}
	if check != "" {
		if !slices.Contains(checks, check) {
			return nil, fmt.Errorf("у %s нет подпроверки %s", id, check)
		}
		checks = []string{check}
	}
	if !slices.Contains([]string{"checked", "violated", "pending", "not_applicable"}, result) {
		return nil, fmt.Errorf("укажите checked, violated, pending или not_applicable")
	}
	var templates []Evidence
	for _, name := range checks {
		entry := Evidence{Object: object, Check: name, Result: result}
		entryKind := kind
		if id == configOptionRule && check == "" && entryKind == "" {
			entryKind = "list"
		}
		if result == "violated" || result == "pending" {
			entry.Heading = "### "
		}
		if result != "not_applicable" && requiresReviewFacts(id, name) {
			if err := fillFactTemplate(id, &entry, entryKind); err != nil {
				return nil, err
			}
		}
		fillEvidenceTemplate(id, &entry, entryKind)
		if id == namingRule && entry.ObjectComparison != nil {
			fillNameExpectationTemplate(&entry)
		}
		if id == commentRule && object != "" && result != "not_applicable" {
			if err := fillCommentTemplate(dir, &entry); err != nil {
				return nil, err
			}
		}
		templates = append(templates, entry)
	}
	return templates, nil
}

func fillNameExpectationTemplate(entry *Evidence) {
	symbol := nameObjectSymbol(entry.Object)
	if symbol == "" {
		entry.ObjectComparison.Expected = []string{"полный состав слов по symbol; укажите --object с #именем"}
		return
	}
	expected := expectedNameWords(identifierWords(symbol))
	entry.ObjectComparison.Expected = append([]string{}, expected...)
	if len(expected) == 0 {
		return
	}
	entry.ObjectComparison.NoExpectationReason = ""
	word := expected[len(expected)-1]
	if entry.ResultEvidence != nil {
		entry.ResultEvidence.Word = &word
	}
	if entry.WriteEvidence != nil {
		entry.WriteEvidence.Word = &word
	}
	if proof := entry.ObjectComparison.Proof; proof != nil {
		proof.Words = nil
		for _, word := range expected {
			proof.Words = append(proof.Words, NameProofWord{Word: word, Role: "object", Fact: ReviewFact{Value: "выражение из цитаты"}})
		}
	}
}

func fillCommentTemplate(dir string, entry *Evidence) error {
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	files, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer files.Close()
	source := &evidenceSource{root: root, files: files, cache: make(map[string][]string)}
	block, err := readCommentBlock(source, entry.Object, make(map[string]goCommentEvidence))
	if err != nil {
		return err
	}
	entry.CommentQuote = block.fragments[0]
	claims := make([]ReviewFact, len(block.fragments))
	for i, fragment := range block.fragments {
		claims[i].Quote = fragment
		claims[i].Verdict = "вывод"
		claims[i].Value = "фрагмент строки реализации"
		if commentNamedSource.MatchString(fragment) {
			claims[i].Case = "конкретный вход"
			claims[i].Via = "файл:строка чтения источника"
		}
	}
	entry.Facts["claim"] = claims
	return nil
}

func fillFactTemplate(id string, entry *Evidence, kind string) error {
	switch {
	case id == configOptionRule && entry.Check == "behavior":
		entry.Kind = "list"
	case id == testStrengthRule && entry.Check == "overlap":
		entry.Kind = "pair"
	case id == commentRule && entry.Result == "violated":
		entry.Kind = "contradiction"
	case id == configOptionRule && entry.Check == "basis" && kind == "derived":
		entry.Kind = "derived"
	}
	if entry.Kind != "" && kind != "" {
		entry.Kind = kind
	}
	roles, err := reviewEntryRoles(id, *entry)
	if err != nil {
		return err
	}
	if entry.Result == "pending" {
		roles = []string{"question"}
		if id == failureRule {
			roles = append(roles, "origin")
		}
		if id == commentRule {
			roles = append(roles, "claim")
		}
	}
	if id == configOptionRule && entry.Check == "basis" && entry.Kind == "derived" && entry.Result == "checked" {
		roles = append(slices.Clone(roles), "necessity")
	}
	if len(roles) == 0 {
		return nil
	}
	entry.Facts = make(map[string][]ReviewFact, len(roles))
	for _, role := range roles {
		fact := ReviewFact{}
		switch role {
		case "filter", "argument", "first", "second", "use":
			fact.Related = "файл:строка"
		}
		switch role {
		case "contract", "statement", "repeat", "assertion", "first", "second", "schema", "claim",
			"implementation", "classification", "classification_definition", "derive", "necessity":
			fact.Quote = "точная цитата"
		case "step":
			failure := false
			fact.Failure = &failure
		}
		switch role {
		case "schema", "input", "output", "contract", "question":
			if id == contractRule {
				fact.Case = "проверяемый вход"
				if role == "schema" || role == "output" {
					fact.Forms = []string{"форма результата"}
				}
			}
		case "claim":
			fact.Verdict = entry.Result
		case "first_level", "second_level", "search", "placement", "derive", "omitted", "incompatible", "boundary", "return_value", "return_error":
			fact.Value = "значение"
		case "use", "classification":
			fact.Value, fact.Verdict = "значение", "вывод"
		case "cause", "message", "dependency":
			fact.Verdict = "вывод"
		}
		entry.Facts[role] = []ReviewFact{fact}
	}
	return nil
}

func fillEvidenceTemplate(id string, entry *Evidence, kind string) {
	if id == readRule && entry.Result != "pending" {
		entry.ReadScenario = &ReadScenario{CallAt: entry.Object, Consumers: []string{}}
		if entry.Check == "with_consumer" || entry.Result == "not_applicable" {
			entry.ReadScenario.Consumers = []string{""}
		}
	}
	if entry.Result == "not_applicable" {
		return
	}
	if id == commentRule {
		entry.CommentQuote = "точная цитата"
	}
	if id == moduleRule {
		entry.Symbol = "точное_имя"
	}
	if entry.Result == "pending" && id != queryRule {
		if id == configOptionRule && entry.Check == "basis" && kind == "list" {
			entry.ListDecision = "undecided"
		}
		if id == namingRule {
			entry.Kind = "flow"
			if kind == "contract" || kind == "missing_file" {
				entry.Kind = kind
			}
			if kind == "missing_file" {
				entry.MissingFile = &MissingFileEvidence{Revision: "head"}
			}
			entry.Facts = map[string][]ReviewFact{"question": {{}}}
			if entry.Kind == "flow" {
				entry.Facts["question"][0].Value = "идентификатор последнего перехода"
			}
		}
		return
	}
	switch id {
	case configOptionRule:
		if entry.Check == "basis" && kind == "list" {
			entry.ListDecision = "explicit"
		}
	case dataBoundaryRule:
		switch entry.Check {
		case "parameters":
			change := entry.Result == "violated" || kind == "collection" || kind == "scalar"
			entry.BindingChange = &change
			if change {
				entry.Kind = "collection"
				if kind == "scalar" {
					entry.Kind = kind
				}
				entry.Facts["filter"][0].Parameter = "имя_параметра"
			}
			if entry.Result == "violated" {
				entry.Facts["filter"][0].Quote = "'значение'"
				entry.Facts["filter"][0].SQLLiteral = &SQLFilterLiteral{
					Source: "constant", SourceQuote: "объявление константы",
				}
			}
		case "proposed_binding":
			entry.SQLProposal = &SQLBindingProposal{
				SQL: "... {{1:parameter}} ...", Style: "positional",
				Arguments: []SQLBindingArgument{{Parameter: "parameter", Value: "значение"}},
				Expected:  []SQLBindingResult{{}},
			}
			if entry.Result == "checked" {
				entry.SQLProposal.ProbeFile = "binding-probe.json"
			}
			if kind == "collection" {
				entry.SQLProposal.SQL = "... {{1:status}} ... IN ({{2:id}}, {{3:id}})"
				entry.SQLProposal.Arguments = []SQLBindingArgument{
					{Parameter: "status", Value: "значение"},
					{Parameter: "id", IDs: []string{"A"}},
					{Parameter: "id", IDs: []string{"B"}},
				}
				entry.SQLProposal.Expected = []SQLBindingResult{{ID: "A"}, {ID: "B"}}
			}
		}
	case namingRule:
		word := ""
		switch entry.Check {
		case "meaning":
			entry.Symbol = "точное_имя"
			entry.WordChecks = []WordCheck{{Result: "checked"}}
		case "result":
			entry.ResultEvidence = &ResultEvidence{Word: &word}
			switch kind {
			case "implementation":
				entry.ResultEvidence.Implementation = "файл:строка объявления реализации"
			case "source":
				entry.Kind = "source"
				entry.ResultEvidence.Implementation = "файл:строка одноимённой реализации на Go"
				entry.ResultEvidence.Facts = []ReviewFact{{Location: "файл:строка исходного объявления"}}
				entry.ResultEvidence.Flow = &NameResultFlow{UseExpression: "выражение использования"}
			case "use":
				entry.ResultEvidence.Flow = &NameResultFlow{UseExpression: "точное выражение использования"}
			case "boolean":
				entry.ResultEvidence.Flow = &NameResultFlow{UseExpression: "полное условие, которое использует результат"}
			case "assignment", "field_read":
				entry.ResultEvidence.Flow = &NameResultFlow{UseKind: kind}
			case "facts":
				entry.ResultEvidence.Facts = []ReviewFact{{}}
			case "forward":
				entry.ResultEvidence.Flow = &NameResultFlow{Forward: []NameResultHop{{}}}
			case "projection":
				entry.ResultEvidence.Flow = &NameResultFlow{Projection: &NameProjection{}}
			}
		case "writes":
			entry.WriteEvidence = &WriteEvidence{Word: &word}
			if kind == "callback" {
				entry.WriteEvidence.Callback = &NameCallbackWrite{}
			}
			if kind == "destination" || kind == "content" {
				entry.WriteEvidence.File = &NameFileWrite{Role: kind}
				if kind == "content" {
					entry.WriteEvidence.File.PayloadAt = "файл:строка сериализации"
					entry.WriteEvidence.File.EncodeAt = "файл:строка кодирования"
				}
			}
		}
		if entry.Check != "meaning" {
			entry.ObjectComparison = &NameObjectComparison{Expected: []string{}, NoExpectationReason: "основание пустого expected, если применимо"}
			if (entry.Check == "result" && slices.Contains([]string{"proof", "use", "guard", "implementation", "source", "boolean"}, kind)) ||
				(entry.Check == "writes" && slices.Contains([]string{"reference", "assignment", "api", "cli", "sql", "setting", "destination", "content", "callback"}, kind)) {
				entry.ObjectComparison.Proof = &NameObjectProof{Words: []NameProofWord{{Word: "слово из expected", Role: "object", Fact: ReviewFact{Value: "выражение из цитаты"}}}}
				p := entry.ObjectComparison.Proof
				switch kind {
				case "boolean":
					p.Type = "bool"
				case "guard":
					p.Guards = []NameCodeUse{{}}
					p.Type = "объявленный тип результата"
				case "proof", "use", "implementation", "source":
					entry.ObjectComparison.Proof.Type = "объявленный тип результата"
				case "setting":
					p.Setting = &NameSettingWrite{}
				case "sql":
					p.SQL = &NameSQLWrite{Arguments: []string{}}
				case "reference":
					p.Reference = &NameReferenceWrite{}
				case "assignment":
					p.Assignment = &NameAssignmentWrite{Writes: []NameAssignmentSite{{}}}
				case "api":
					p.API = &NameAPIWrite{}
				case "cli":
					p.CLI = &NameCLIWrite{
						Command:       []string{"буквальное слово подкоманды"},
						SuccessCodeAt: "файл:строка объявления, если код задан константой",
						CountAt:       "файл:строка проверки, если имя обещает один артефакт",
						Count:         "полный вызов проверки одного артефакта",
						ContentChecks: []NameCodeUse{{}},
					}
				}
			}
			if entry.Check == "result" && entry.Result == "checked" && kind == "flow" {
				entry.ObjectComparison.EquivalentFlow = &NameResultFlow{}
			}
		}
	case queryRule:
		switch entry.Check {
		case "calls":
			one := 1
			entry.QueryCalls = &QueryCalls{One: &one, Paths: []QueryPath{{Steps: []QueryStep{{}}}}}
			if entry.Result == "pending" {
				entry.Facts = map[string][]ReviewFact{"question": {{}}}
			} else if kind == "loop" {
				two := 2
				entry.QueryCalls.Two, entry.QueryCalls.Repeat = &two, &one
				entry.QueryCalls.Probe = &QueryProbe{File: "driver-calls.json"}
			}
		case "inputs":
			entry.QueryInputs = &QueryInputs{Locations: []string{}, Entries: []QueryEntryReview{{Result: "inspected"}}}
			if entry.Result == "pending" {
				entry.QueryInputs.Gap = &QueryInputGap{Kind: "contract"}
				if kind == "missing_file" {
					entry.QueryInputs.Gap.Kind = kind
					entry.MissingFile = &MissingFileEvidence{Revision: "head"}
				}
			} else {
				entry.QueryInputs.One = []string{""}
				entry.QueryInputs.Two = []string{"", ""}
				entry.QueryInputs.Repeat = []string{""}
				if kind == "two_limit" {
					entry.QueryInputs.Two = nil
					entry.QueryInputs.TwoLimits = []QueryInputLimit{{}}
				} else {
					entry.QueryInputs.TwoScenario = &QueryTwoScenario{
						Parameters: map[string]string{"parameter": "value"}, IDs: []string{"A", "B"},
					}
				}
			}
		case "batch":
			if entry.Result == "checked" {
				entry.QueryJustification = "основание поштучного чтения, если оно есть"
			}
		}
	}
}

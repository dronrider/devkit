package reviewaudit

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

type nameRuntimeResultRun struct {
	Command  nameRuntimeCommand     `json:"command"`
	Returned nameRuntimeObservation `json:"returned"`
	Read     nameRuntimeObservation `json:"read"`
}

type nameRuntimeResultRecord struct {
	nameRuntimeResultRun
	Version     int                  `json:"version"`
	Task        string               `json:"task"`
	Commit      string               `json:"commit"`
	Object      string               `json:"object"`
	Declaration string               `json:"declaration"`
	Actual      string               `json:"actual"`
	Expected    []string             `json:"expected"`
	Words       []NameProofWord      `json:"words"`
	Negative    nameRuntimeResultRun `json:"negative"`
}

func checkNameRuntimeResult(e Evidence, declaration string, words []string, source *evidenceSource) error {
	pair, result := e.ObjectComparison, e.ResultEvidence
	proof := pair.Proof
	if e.Result != "checked" || result == nil || e.WriteEvidence != nil || e.Kind == "source" ||
		result.Implementation != "" || result.Flow != nil || len(result.Facts) != 0 ||
		proof.Assignment != nil || proof.Reference != nil || proof.API != nil || proof.CLI != nil ||
		proof.Effect != nil || proof.Setting != nil || proof.SQL != nil || len(proof.Guards) != 0 ||
		proof.Type != "" || pair.EquivalentAt != "" || pair.EquivalentFlow != nil {
		return fmt.Errorf("proof.runtime: для JavaScript укажите checked для result и только подписанное доказательство результата")
	}
	path, declLine, base, err := evidenceLocation(declaration)
	if err != nil || base || !slices.Contains([]string{".js", ".mjs", ".cjs"}, filepath.Ext(path)) {
		return fmt.Errorf("proof.runtime: доказательство результата применяется к объявлению JavaScript в проверяемой ревизии")
	}
	if err := checkNameWord(result.Word, words); err != nil {
		return err
	}
	if err := checkExpectedObject(pair.Expected, *result.Word, words); err != nil {
		return err
	}
	if err := checkNameRuntimeSignature(proof.Runtime, source.root); err != nil {
		return err
	}
	var run nameRuntimeResultRecord
	if err := decodeNameRuntimePayload(proof.Runtime.Payload, &run); err != nil {
		return fmt.Errorf("proof.runtime.payload: %w", err)
	}
	if run.Version != 2 {
		return fmt.Errorf("proof.runtime.payload: для результата ожидается version 2")
	}
	if source.taskID == "" || run.Task != source.taskID || run.Object != e.Object || run.Declaration != declaration ||
		run.Actual != pair.Actual || !slices.Equal(run.Expected, pair.Expected) {
		return fmt.Errorf("proof.runtime.payload: задача, цель, объявление, actual и expected должны совпадать с проверкой")
	}
	if !reflect.DeepEqual(run.Words, proof.Words) {
		return fmt.Errorf("proof.runtime.payload.words: роли и факты должны совпадать с proof.words")
	}
	if !validNameRuntimeCommand(run.Command) || !validNameRuntimeCommand(run.Negative.Command) ||
		slices.EqualFunc(run.Command, run.Negative.Command, func(a, b *string) bool { return *a == *b }) {
		return fmt.Errorf("proof.runtime.payload: укажите команды прогона и отдельного контроля")
	}
	if strings.TrimSpace(pair.Actual) == "" || pair.ActualAt != result.ReturnAt || run.Returned.At != result.ReturnAt ||
		run.Returned.Expression != pair.Actual || result.Returned != pair.Actual || proof.Expression != pair.Actual {
		return fmt.Errorf("proof.runtime.payload.returned: выражение и точка возврата должны совпадать с result_evidence, actual и proof.expression")
	}
	if strings.TrimSpace(result.Consumer) == "" || run.Read.At != result.ConsumerAt || run.Read.At == run.Returned.At ||
		run.Read.Expression != result.Consumer {
		return fmt.Errorf("proof.runtime.payload.read: укажите отдельную точку и выражение потребителя из result_evidence")
	}
	if !sameNameRuntimeResult(run.nameRuntimeResultRun) {
		return fmt.Errorf("proof.runtime.payload: возврат и потребитель должны наблюдать один объект и одно значение")
	}
	control := run.Negative
	if control.Returned.nameRuntimeSite != run.Returned.nameRuntimeSite || control.Read.nameRuntimeSite != run.Read.nameRuntimeSite ||
		!sameNameRuntimeResult(control) || *control.Read.Value == *run.Read.Value {
		return fmt.Errorf("proof.runtime.payload.negative: контроль должен вернуть и прочитать другое значение в тех же точках")
	}
	if err := checkNameRuntimeTree(source.root, source.reportPath, run.Commit); err != nil {
		return fmt.Errorf("proof.runtime.payload.commit: %w", err)
	}
	returnPath, returnLine, returnBase, err := evidenceLocation(run.Returned.At)
	if err != nil || returnBase || returnPath != path || returnLine < declLine {
		return fmt.Errorf("proof.runtime.payload.returned.at: укажите возврат в файле проверяемого объявления")
	}
	if nameRuntimeReturnExpression(run.Returned.Quote) != run.Returned.Expression {
		return fmt.Errorf("proof.runtime.payload.returned.quote: процитируйте return с полным возвращаемым выражением")
	}
	lines := nameRuntimeSource{source: source, revision: run.Commit}
	if _, err := lines.line(declaration); err != nil {
		return fmt.Errorf("proof.runtime.payload.declaration: %w", err)
	}
	for _, site := range []struct {
		name string
		nameRuntimeSite
	}{{"returned", run.Returned.nameRuntimeSite}, {"read", run.Read.nameRuntimeSite}} {
		text, err := lines.lines(site.At, 1+strings.Count(site.Quote, "\n"))
		if err != nil {
			return fmt.Errorf("proof.runtime.payload.%s.at: %w", site.name, err)
		}
		if err := checkFactQuote(site.Quote, text); err != nil {
			return fmt.Errorf("proof.runtime.payload.%s: %w", site.name, err)
		}
		if site.name == "returned" {
			suffix, startsAtReturn := strings.CutPrefix(strings.TrimLeft(text, " \t"), strings.TrimLeft(site.Quote, " \t"))
			if !startsAtReturn {
				return fmt.Errorf("proof.runtime.payload.returned.quote: начните цитату с инструкции return в исходнике")
			}
			if tail := strings.TrimSpace(suffix); tail != "" && !strings.HasPrefix(tail, "//") {
				return fmt.Errorf("proof.runtime.payload.returned.quote: цитата должна охватывать всё выражение возврата")
			}
			if !strings.HasSuffix(strings.TrimSpace(site.Quote), ";") {
				next, err := lines.line(fmt.Sprintf("%s:%d", returnPath, returnLine+1+strings.Count(site.Quote, "\n")))
				if err != nil || !strings.HasPrefix(strings.TrimSpace(next), "}") {
					return fmt.Errorf("proof.runtime.payload.returned.quote: для return без ; покажите закрытие блока после выражения")
				}
			}
		}
		if err := checkFactQuote(site.Expression, site.Quote); err != nil {
			return fmt.Errorf("proof.runtime.payload.%s.expression: %w", site.name, err)
		}
	}
	if len(pair.Expected) == 0 {
		if len(proof.Words) != 0 || strings.TrimSpace(pair.NoExpectationReason) == "" {
			return fmt.Errorf("proof.words: для пустого expected укажите no_expectation_reason")
		}
	} else if err := checkNameProofWords(pair, source); err != nil {
		return err
	}
	for _, word := range proof.Words {
		fact := word.Fact
		if !nameRuntimeSiteCites(run.Returned.nameRuntimeSite, fact) && !nameRuntimeSiteCites(run.Read.nameRuntimeSite, fact) {
			return fmt.Errorf("proof.words %s: факт должен цитировать подписанный возврат или потребителя", word.Word)
		}
	}
	return nil
}

func nameRuntimeReturnExpression(quote string) string {
	tail, isReturn := strings.CutPrefix(strings.TrimSpace(quote), "return")
	if !isReturn || tail == "" || !strings.ContainsRune(" \t{[('\"`!~+-/.", rune(tail[0])) {
		return ""
	}
	expr := strings.TrimLeft(tail, " \t")
	if strings.HasPrefix(expr, "\n") || strings.HasPrefix(expr, "\r") ||
		strings.HasPrefix(expr, "\u2028") || strings.HasPrefix(expr, "\u2029") {
		return ""
	}
	expr = strings.TrimSpace(expr)
	if !strings.HasSuffix(expr, ";") {
		switch {
		case strings.HasPrefix(expr, "{") && !strings.HasSuffix(expr, "}"),
			strings.HasPrefix(expr, "[") && !strings.HasSuffix(expr, "]"),
			strings.HasPrefix(expr, "(") && !strings.HasSuffix(expr, ")"):
			return ""
		}
	}
	return strings.TrimSpace(strings.TrimSuffix(expr, ";"))
}

func nameRuntimeSiteCites(site nameRuntimeSite, fact ReviewFact) bool {
	path, first, _, _ := evidenceLocation(site.At)
	file, line, base, err := evidenceLocation(fact.Location)
	if err != nil || base || path != file || line < first {
		return false
	}
	lines := strings.Split(site.Quote, "\n")
	offset := line - first
	return offset < len(lines) && strings.Contains(lines[offset], fact.Quote)
}

func sameNameRuntimeResult(run nameRuntimeResultRun) bool {
	return strings.TrimSpace(run.Returned.Identity) != "" && run.Read.Identity == run.Returned.Identity &&
		run.Returned.Value != nil && run.Read.Value != nil && *run.Returned.Value == *run.Read.Value
}

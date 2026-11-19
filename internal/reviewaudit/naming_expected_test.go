package reviewaudit

import (
	"slices"
	"testing"
)

func TestNameExpectationRejectsShrinking(t *testing.T) {
	for _, tc := range []struct {
		name string
		full []string
	}{
		{"exportSingleToken", []string{"Single", "Token"}},
		{"readFeatureLimitValue", []string{"Feature", "Limit", "Value"}},
		{"tokenClaimValue", []string{"token", "Claim", "Value"}},
		{"newTokenClaimsError", []string{"Token", "Claims", "Error"}},
		{"setParentReceipt", []string{"Parent", "Receipt"}},
		{"createBatchReceiptFromRows", []string{"Batch", "Receipt"}},
		{"addBatchRowLabels", []string{"Batch", "Row", "Labels"}},
		{"readValueValue", []string{"Value", "Value"}},
		{"read_limit_value_by_id", []string{"limit", "value"}},
		{"читатьЗначениеЛимита", []string{"читать", "Значение", "Лимита"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			words := identifierWords(tc.name)
			head := tc.full[len(tc.full)-1]
			assertTargetCheck(t, checkExpectedObject(tc.full, head, words), "")
			for _, empty := range [][]string{nil, {}} {
				assertTargetCheck(t, checkExpectedObject(empty, "", words), "полный состав")
			}
			for i := range tc.full {
				short := slices.Delete(slices.Clone(tc.full), i, i+1)
				word := short[len(short)-1]
				assertTargetCheck(t, checkExpectedObject(short, word, words), "полный состав")
			}
			reversed := slices.Clone(tc.full)
			slices.Reverse(reversed)
			if !slices.Equal(reversed, tc.full) {
				assertTargetCheck(t, checkExpectedObject(reversed, head, words), "полный состав")
			}
		})
	}
}

func TestNameExpectationRequiresFullProof(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Item struct { Kind string; Value int }
func readLimitValue(item Item) int {
 if item.Kind != "limit" { return 0 }
 return item.Value
}
`)
	word := "Value"
	e := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{"Limit", "Value"}, Actual: "Value", ActualAt: at("return item.Value"),
			Proof: &NameObjectProof{Expression: "item.Value", Type: "int", Words: []NameProofWord{
				{Word: "Limit", Role: "condition", Fact: ReviewFact{Location: at("if item.Kind"), Value: "item.Kind", Quote: `if item.Kind != "limit"`, Text: "Проверяется вид значения."}},
				{Word: "Value", Role: "object", Fact: ReviewFact{Location: at("return item.Value"), Value: "item.Value", Quote: "return item.Value", Text: "Возвращается значение."}},
			}}},
		ResultEvidence: &ResultEvidence{Word: &word, Returned: "item.Value", ReturnAt: at("return item.Value"), Consumer: "caller", ConsumerAt: at("func readLimitValue")}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func readLimitValue"), identifierWords("readLimitValue"), source), want)
	}
	check("")
	proof := e.ObjectComparison.Proof
	e.ObjectComparison.Expected = []string{"Value"}
	e.ObjectComparison.Proof = nil
	check("полный состав")
	e.ObjectComparison.Expected = nil
	e.ObjectComparison.NoExpectationReason = "Имя описывает вычисление, а не результат."
	word = ""
	check("полный состав")
	e.ObjectComparison.Expected = []string{"Limit", "Value"}
	word = "Value"
	check("укажите equivalent_at")
	e.ObjectComparison.Proof = proof
	check("")
}

func TestNameExpectationTemplateIncludesAllWords(t *testing.T) {
	for _, check := range []string{"result", "writes"} {
		kind := "proof"
		if check == "writes" {
			kind = "cli"
		}
		entries, err := EvidenceTemplates(".", "STYLE-001", check, "sample.go:3#Service.exportSingleToken", "checked", kind)
		if err != nil {
			t.Fatal(err)
		}
		pair := entries[0].ObjectComparison
		if !slices.Equal(pair.Expected, []string{"Single", "Token"}) || len(pair.Proof.Words) != 2 ||
			pair.Proof.Words[0].Word != "Single" || pair.Proof.Words[1].Word != "Token" || pair.NoExpectationReason != "" {
			t.Fatalf("неполный шаблон: %+v", pair)
		}
	}
}

func TestNameExpectationUsesTargetInsteadOfEditedMeaning(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
func readLimitValue(item Item) int { return item.Value }
`)
	location := at("func readLimitValue")
	word := "Value"
	rule := Rule{Targets: []Target{{Object: "limit", Symbol: "readLimitValue", Location: location}}, Evidence: []Evidence{
		{Object: "limit", Check: "meaning", Result: "checked", Symbol: "readValue", Location: location},
		{Object: "limit", Check: "result", Result: "checked",
			ObjectComparison: &NameObjectComparison{Expected: []string{"Value"}, Actual: "Value", ActualAt: location},
			ResultEvidence:   &ResultEvidence{Word: &word, Returned: "item.Value", ReturnAt: location, Consumer: "caller", ConsumerAt: location}},
	}}
	assertTargetCheck(t, checkNameEvidence(source.root, source.reportPath, "", "", rule), "полный состав \"Limit Value\"")
}

func TestNameExpectationAllowsInputRoleAndActionOnlyName(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Invoice struct { Valid bool }
func validateInvoice(invoice Invoice) bool { return invoice.Valid }
func New() Invoice { return Invoice{} }
`)
	word := "Invoice"
	e := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{"Invoice"}, Actual: "Valid", ActualAt: at("func validateInvoice"),
			Proof: &NameObjectProof{Expression: "invoice.Valid", Type: "bool", Words: []NameProofWord{
				{Word: "Invoice", Role: "source", Fact: ReviewFact{Location: at("func validateInvoice"), Value: "invoice", Quote: "return invoice.Valid", Text: "Возвращается признак проверяемого счёта."}},
			}}},
		ResultEvidence: &ResultEvidence{Word: &word, Returned: "invoice.Valid", ReturnAt: at("func validateInvoice"), Consumer: "caller", ConsumerAt: at("func validateInvoice")}}
	assertTargetCheck(t, checkNameEntry(e, at("func validateInvoice"), identifierWords("validateInvoice"), source), "")
	word = ""
	e.ObjectComparison = &NameObjectComparison{Expected: []string{}, Actual: "Invoice", ActualAt: at("func New")}
	e.ResultEvidence.Returned = "Invoice{}"
	e.ResultEvidence.ReturnAt = at("func New")
	assertTargetCheck(t, checkNameEntry(e, at("func New"), identifierWords("New"), source), "no_expectation_reason")
	e.ObjectComparison.NoExpectationReason = "Имя состоит только из действия new."
	assertTargetCheck(t, checkNameEntry(e, at("func New"), identifierWords("New"), source), "")
}

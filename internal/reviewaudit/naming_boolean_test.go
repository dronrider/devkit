package reviewaudit

import (
	"strings"
	"testing"
)

func TestNameBooleanResultProof(t *testing.T) {
	const code = `package sample
func ownerValid(a, b, c int) bool {
  return a == b && b == c
}
func authorize(a, b, c int) bool {
  if !ownerValid(a, b, c) { return false }
  return true
}
`
	const expr = "a == b && b == c"
	for _, tc := range []struct{ name, use, codeUse, actual, want string }{
		{name: "whole expression", use: "!ownerValid(a, b, c)", codeUse: "!ownerValid(a, b, c)", actual: expr},
		{name: "partial expression", use: "!ownerValid(a, b, c)", codeUse: "!ownerValid(a, b, c)", actual: "a == b", want: "полным выражением return"},
		{name: "ignored call", use: "unrelated(ownerValid(a, b, c))", codeUse: "unrelated(ownerValid(a, b, c))", actual: expr, want: "условие вызывающей функции"},
		{name: "wrong condition evidence", use: "ownerValid(a, b, c)", codeUse: "!ownerValid(a, b, c)", actual: expr, want: "условие вызывающей функции"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, strings.Replace(code, "!ownerValid(a, b, c)", tc.codeUse, 1))
			location := at("return a == b")
			decl := at("func ownerValid")
			consumer := at("if " + tc.codeUse)
			actual := tc.actual
			word := "owner"
			e := Evidence{
				Object: decl + "#ownerValid", Check: "result", Result: "checked", Location: decl,
				ObjectComparison: &NameObjectComparison{Expected: []string{"owner", "Valid"}, Actual: actual, ActualAt: location,
					Proof: &NameObjectProof{Expression: actual, Type: "bool", Words: []NameProofWord{
						{Word: "owner", Role: "object", Fact: ReviewFact{Location: location, Quote: "a == b", Value: "a == b", Text: "Сравнение владельца."}},
						{Word: "Valid", Role: "condition", Fact: ReviewFact{Location: location, Quote: "b == c", Value: "b == c", Text: "Второе условие."}},
					}}},
				ResultEvidence: &ResultEvidence{Word: &word, Returned: actual, ReturnAt: location,
					Consumer: "authorize", ConsumerAt: consumer, Flow: &NameResultFlow{Caller: at("func authorize"), CallAt: consumer, UseExpression: tc.use}},
			}
			assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("ownerValid"), source), tc.want)
		})
	}
}

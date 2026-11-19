package reviewaudit

import (
	"path/filepath"
	"testing"
)

func TestNameWriteProofStartsAtBoundClosure(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Set struct{ items []string }
func prepare(set *Set) {
	addItem := func(item string) {
		set.items = append(set.items, item)
	}
	addItem("item")
	consume(set.items)
}
`)
	decl, writeAt, useAt := at("addItem :="), at("set.items ="), at("consume(set.items)")
	word := "Item"
	e := Evidence{Object: decl + "#addItem", Check: "writes", Result: "checked", Location: decl,
		ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "items", ActualAt: writeAt,
			Proof: &NameObjectProof{Expression: "set.items", Words: []NameProofWord{{Word: word, Role: "object", Fact: ReviewFact{
				Location: writeAt, Quote: "set.items = append(set.items, item)", Value: "item", Text: "Элемент добавляется в список."}}},
				Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: writeAt, Target: "set.items", Value: "append(set.items, item)"}},
					Use: NameCodeUse{Declaration: at("func prepare"), At: useAt, Expression: "set.items"}}}},
		WriteEvidence: &WriteEvidence{Word: &word, Target: "set.items", Value: "append(set.items, item)", WriteAt: writeAt}}
	words := identifierWords("addItem")
	assertTargetCheck(t, checkNameEntry(e, decl, words, source), "")
	e.Result = "not_applicable"
	assertTargetCheck(t, checkNameEntry(e, decl, words, source), "имя замыкания addItem обещает запись")
	e.Result, e.Object = "checked", decl+"#other"
	assertTargetCheck(t, checkNameEntry(e, decl, words, source), "строку объявления функции")
}

func TestNameParameterWritesAreScopedToTheParameter(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "os"
func writeFile(content string) {
	if true {
		content := "shadow"
		content = "other"
		_ = content
	}
	os.WriteFile("file", []byte(content), 0600)
}
func update(content []byte) {
	use(func() { content[0] = 'x' })
}
`)
	decl := at("func writeFile")
	e := Evidence{Object: decl + "#content", Check: "writes", Result: "not_applicable", Location: decl,
		ObjectComparison: &NameObjectComparison{Expected: []string{"content"}}}
	assertTargetCheck(t, checkNameEntry(e, decl, []string{"content"}, source), "")
	e.ObjectComparison.Expected = nil
	assertTargetCheck(t, checkNameEntry(e, decl, []string{"content"}, source), "сохраните полный список")
	e.Object, e.ObjectComparison = decl+"#writeFile", nil
	assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("writeFile"), source), "достижимая запись WriteFile")
	decl = at("func update")
	e.Object, e.Location = decl+"#content", decl
	assertTargetCheck(t, checkNameEntry(e, decl, []string{"content"}, source), "параметр content изменяется")
}

func TestNameResultProofConnectsSourceAndConsumer(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
func GetItems() []string {
	items := read()
	return items
}
func unrelated() []string { return nil }
func consume() {
	items := GetItems()
	use(items)
}
`)
	write(t, filepath.Join(source.root, "query.sql"), "-- name: GetItems\nSELECT item FROM inventory;\n")
	decl, word, returned := "query.sql:1", "Items", at("return items")
	e := Evidence{Object: decl + "#GetItems", Check: "result", Result: "checked", Kind: "source",
		ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "items", ActualAt: returned,
			Proof: &NameObjectProof{Expression: "items", Type: "[]string", Words: []NameProofWord{{Word: word, Role: "object", Fact: ReviewFact{
				Location: returned, Quote: "return items", Value: "items", Text: "Возвращаются элементы."}}}}},
		ResultEvidence: &ResultEvidence{Word: &word, Implementation: at("func GetItems"), Returned: "items", ReturnAt: returned, Consumer: "читает элементы", ConsumerAt: at("use(items)"),
			Facts: []ReviewFact{{Location: decl, Quote: "-- name: GetItems", Text: "Запрос из спецификации."}},
			Flow:  &NameResultFlow{Caller: at("func consume"), CallAt: at("items := GetItems"), Value: "items", UseExpression: "items"}}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("GetItems"), source), want)
	}
	check("")
	d := e.ResultEvidence
	d.Implementation = at("func unrelated")
	check("одноимённую реализацию GetItems")
	d.Implementation = at("func GetItems")
	d.Flow.UseExpression = "other"
	check("однозначно использовать результат")
	d.Flow.UseExpression = "items"
	d.Facts[0].Quote = "-- missing: GetItems"
	check("quote должна точно цитировать")
}

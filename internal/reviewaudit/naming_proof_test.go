package reviewaudit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func nameProofFixture(t *testing.T, code string) (*evidenceSource, func(string) string) {
	t.Helper()
	root, report := fixture(t)
	write(t, filepath.Join(root, "sample.go"), code)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	return &evidenceSource{root: root, reportPath: report, files: files, cache: map[string][]string{}}, func(text string) string {
		i := strings.Index(code, text)
		if i < 0 {
			t.Fatalf("no %q in fixture", text)
		}
		return "sample.go:" + strconv.Itoa(1+strings.Count(code[:i], "\n"))
	}
}

func TestNameProofKeepsCompoundExpectation(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Item struct { Kind string; Value int }
func readLimitValue(item *Item) int {
	unused := Item{Kind: "other"}
	if item.Kind != "limit" { return 0 }
	return item.Value
}
`)
	word := "Value"
	proof := &NameObjectProof{Expression: "item.Value", Type: "int", Words: []NameProofWord{
		{Word: "Limit", Role: "condition", Fact: ReviewFact{Location: at("if item.Kind"), Value: "item.Kind", Quote: `if item.Kind != "limit"`, Text: "Отбирается лимит."}},
		{Word: "Value", Role: "object", Fact: ReviewFact{Location: at("return item.Value"), Value: "item.Value", Quote: "return item.Value", Text: "Возвращается числовое значение."}},
	}}
	e := Evidence{Check: "result", Result: "checked", ObjectComparison: &NameObjectComparison{
		Expected: []string{"Limit", "Value"}, Actual: "Value", ActualAt: at("return item.Value"), Proof: proof},
		ResultEvidence: &ResultEvidence{Word: &word, Returned: "item.Value", ReturnAt: at("return item.Value"), Consumer: "caller", ConsumerAt: at("func readLimitValue")}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func readLimitValue"), identifierWords("readLimitValue"), source), want)
	}
	check("")
	e.ObjectComparison.Proof = nil
	check("укажите equivalent_at")
	e.ObjectComparison.Proof = proof
	proof.Words = proof.Words[:1]
	check("каждое слово expected")
	proof.Words = proof.Words[:2]
	saved := proof.Words[0].Fact
	proof.Words[0].Fact = ReviewFact{Location: at("unused :="), Value: "unused", Quote: "unused := Item", Text: "Посторонняя переменная."}
	check("не связано")
	proof.Words[0].Fact = saved
	proof.Words[0].Fact.Quote = `item.Kind == "limit"`
	check("точно цитировать")
	proof.Words[0].Fact = saved
	proof.Expression = "item"
	check("полное выражение")
	proof.Expression = "item.Value"
	proof.Type = "string"
	check("объявленный тип")
}

func TestNameProofChecksFinalCallAndSourceArgument(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Error struct{}
var ExportCode int
func newExportError(cause error) *Error {
	return New(ExportCode).Wrap(cause)
}
func readUpdateValue(input int) int {
	value := input
	return value
}
func use(row Record) { readUpdateValue(row.Update) }
`)
	word := "Error"
	e := Evidence{Check: "result", Result: "checked", ObjectComparison: &NameObjectComparison{
		Expected: []string{"Export", "Error"}, Actual: "Wrap", ActualAt: at("return New"), Proof: &NameObjectProof{
			Expression: "New(ExportCode).Wrap(cause)", Type: "*Error", Words: []NameProofWord{
				{Word: "Export", Role: "context", Fact: ReviewFact{Location: at("return New"), Value: "ExportCode", Quote: "New(ExportCode)", Text: "Ошибка экспорта."}},
				{Word: "Error", Role: "object", Fact: ReviewFact{Location: at("func newExportError"), Value: "*Error", Quote: "*Error", Text: "Возвращается Error."}},
			}}}, ResultEvidence: &ResultEvidence{Word: &word, Returned: "Wrap returns Error", ReturnAt: at("return New"), Consumer: "caller", ConsumerAt: at("func newExportError")}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func newExportError"), identifierWords("newExportError"), source), want)
	}
	check("")
	e.ObjectComparison.Actual, e.ResultEvidence.Returned = "New", "New returns Error"
	check("обозначает часть выражения")
	word = "Value"
	e.ObjectComparison = &NameObjectComparison{Expected: []string{"Update", "Value"}, Actual: "value", ActualAt: at("return value"), Proof: &NameObjectProof{
		Expression: "value", Type: "int", Words: []NameProofWord{
			{Word: "Update", Role: "source", Fact: ReviewFact{Location: at("func use"), Parameter: "input", Value: "row.Update", Quote: "readUpdateValue(row.Update)", Text: "Вход получает данные обновлений."}},
			{Word: "Value", Role: "object", Fact: ReviewFact{Location: at("return value"), Value: "value", Quote: "return value", Text: "Возвращается значение."}},
		}}}
	e.ResultEvidence = &ResultEvidence{Word: &word, Returned: "value", ReturnAt: at("return value"), Consumer: "use", ConsumerAt: at("func use")}
	assertTargetCheck(t, checkNameEntry(e, at("func readUpdateValue"), identifierWords("readUpdateValue"), source), "")
	e.ObjectComparison.Proof.Words[0].Fact.Parameter = "unrelated"
	assertTargetCheck(t, checkNameEntry(e, at("func readUpdateValue"), identifierWords("readUpdateValue"), source), "вход функции")
}

func TestNameProofPreservesCreatedObjectMismatch(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type File struct { Document string }
func createDocument() []File {
	files := []File{{Document: "contents"}}
	return files
}
`)
	word := "Document"
	e := Evidence{Check: "result", Result: "checked", ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "files", ActualAt: at("return files"), Proof: &NameObjectProof{
		Expression: "files", Type: "[]File", Words: []NameProofWord{{Word: word, Role: "object", Fact: ReviewFact{
			Location: at("files :="), Value: "files", Quote: "files := []File", Text: "Файлы содержат документ."}}}}},
		ResultEvidence: &ResultEvidence{Word: &word, Returned: "files", ReturnAt: at("return files"), Consumer: "caller", ConsumerAt: at("func createDocument")}}
	assertTargetCheck(t, checkNameEntry(e, at("func createDocument"), identifierWords("createDocument"), source), "фактический тип результата")
	e.Result = "violated"
	assertTargetCheck(t, checkNameEntry(e, at("func createDocument"), identifierWords("createDocument"), source), "")
}

func TestNameReferenceChecksArgumentsAndForeignKey(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
func setParent(db DB, id, parentID string) {
	_, err := db.ExecContext(ctx, "UPDATE children SET parent_id = $1 WHERE id = $2", parentID, id)
	check(err)
}
`)
	write(t, filepath.Join(source.root, "schema.sql"), "CREATE TABLE children (\n id TEXT, parent_id TEXT,\n FOREIGN KEY (parent_id) REFERENCES parents(id)\n);\n")
	word := "Parent"
	r := &NameReferenceWrite{Table: "children", Column: "parent_id", Value: "parentID", RowColumn: "id", RowValue: "id", TargetTable: "parents", TargetColumn: "id", SchemaAt: "schema.sql:3"}
	e := Evidence{Check: "writes", Result: "checked", ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "parent_id", ActualAt: at("_, err"), Proof: &NameObjectProof{
		Expression: `db.ExecContext(ctx, "UPDATE children SET parent_id = $1 WHERE id = $2", parentID, id)`, Reference: r,
		Words: []NameProofWord{{Word: word, Role: "object", Fact: ReviewFact{Location: "schema.sql:3", Value: "parents", Quote: "REFERENCES parents(id)", Text: "Ссылка на родителя."}}}}},
		WriteEvidence: &WriteEvidence{Word: &word, Target: "children.parent_id", Value: "parentID", WriteAt: at("_, err")}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func setParent"), identifierWords("setParent"), source), want)
	}
	check("")
	r.Value, r.RowValue = "id", "parentID"
	check("параметр $1 не получает id")
	r.Value, r.RowValue = "parentID", "id"
	r.TargetTable = "unrelated"
	check("не подтверждён внешний ключ")
	r.TargetTable = "parents"
	r.SchemaAt = "schema.sql:2"
	check("не подтверждён внешний ключ")
	r.SchemaAt = "schema.sql:3"
	e.ObjectComparison.Proof.Words[0].Fact.Location = "sample.go:1"
	e.ObjectComparison.Proof.Words[0].Fact.Value, e.ObjectComparison.Proof.Words[0].Fact.Quote = "sample", "package sample"
	check("с аргументом записи")
	e.ObjectComparison.Proof = nil
	e.ObjectComparison.Expected = nil
	e.ObjectComparison.NoExpectationReason = "Имя обозначает действие."
	word = ""
	check("называет объект")
}

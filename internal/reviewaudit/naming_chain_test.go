package reviewaudit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Decode the wire format so the regressions also compile against the old schema.
func namingWire(t *testing.T, text string, at func(string) string) Evidence {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	var expand func(any) any
	expand = func(v any) any {
		switch x := v.(type) {
		case string:
			if strings.HasPrefix(x, "@") {
				return at(x[1:])
			}
		case []any:
			for i := range x {
				x[i] = expand(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = expand(x[k])
			}
		}
		return v
	}
	body, err := json.Marshal(expand(value))
	if err != nil {
		t.Fatal(err)
	}
	var e Evidence
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNameChainUsageExpressionAndRebinding(t *testing.T) {
	for _, tc := range []struct {
		body, expr string
		good       bool
	}{
		{"consume(value)", "consume(value)", true},
		{"return value", "value", true},
		{"if enabled { consume(value) }", "consume(value)", true},
		{"saved := []int{value}", "[]int{value}", true},
		{"value = 7; consume(value)", "consume(value)", false},
		{"if enabled { value := 7; consume(value) }", "consume(value)", false},
		{"if enabled { value++; consume(value) }", "consume(value)", false},
		{"if enabled { consume(other) }", "consume(other)", false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			source, at := nameProofFixture(t, "package sample\nfunc readValue() int { value := 3; return (value) }\nfunc use() int {\n value := readValue()\n "+tc.body+"\n}\n")
			e := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["Value"],"actual":"value","actual_at":"@value := readValue()"},"result_evidence":{"word":"Value","returned":"value","return_at":"@func readValue","consumer":"use","consumer_at":"@`+strings.ReplaceAll(tc.body, `"`, `\"`)+`","flow":{"caller":"@func use","call_at":"@value := readValue()","value":"value","use_expression":`+strconvQuote(tc.expr)+`}}}`, at)
			err := checkNameEntry(e, at("func readValue"), identifierWords("readValue"), source)
			if tc.good {
				assertTargetCheck(t, err, "")
			} else {
				assertTargetCheck(t, err, "use_expression")
			}
		})
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestNameChainLiteralCaller(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
func readValue() int { value := 3; return value }
var handlers = map[string]func() int{
 "value": func() int {
  return readValue()
 },
}
`)
	e := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["Value"],"actual":"value","actual_at":"@func readValue"},"result_evidence":{"word":"Value","returned":"value","return_at":"@func readValue","consumer":"return","consumer_at":"@return readValue()","flow":{"caller":"@\"value\": func","call_at":"@return readValue()","value":"","use_expression":"readValue()"}}}`, at)
	assertTargetCheck(t, checkNameEntry(e, at("func readValue"), identifierWords("readValue"), source), "")
	e.ResultEvidence.Flow.Caller = at("return readValue()")
	assertTargetCheck(t, checkNameEntry(e, at("func readValue"), identifierWords("readValue"), source), "строку объявления")
}

func TestNameChainContextFileAndBindings(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "os"
func TestLoadInvoice(t T) {
 writeFixture(t, "invoice")
}
func writeFixture(t T, content string) {
 path := "input.json"
 os.WriteFile(path, []byte(content), 0600)
}
func unrelated(t T, content string) {
 os.WriteFile("other.json", []byte(content), 0600)
}
`)
	e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["Test","Load","Invoice"],"actual":"path","actual_at":"@path :=","proof":{"expression":"path","words":[
 {"word":"Test","role":"context","fact":{"location":"@writeFixture(t,","quote":"writeFixture(t, \"invoice\")","value":"t","text":"Подготовка данных сценария."}},
 {"word":"Load","role":"context","fact":{"location":"@writeFixture(t,","quote":"writeFixture(t, \"invoice\")","value":"t","text":"Подготовка входного файла загрузки."}},
 {"word":"Invoice","role":"context","fact":{"location":"@writeFixture(t,","quote":"writeFixture(t, \"invoice\")","value":"\"invoice\"","text":"Фикстура счёта."}}]}},
 "write_evidence":{"word":"Invoice","target":"path","value":"[]byte(content)","write_at":"@os.WriteFile(path","file":{"path":"path","role":"destination"},"calls":[{"at":"@writeFixture(t,","declaration":"@func writeFixture","bindings":[{"parameter":"t","argument":"t"},{"parameter":"content","argument":"\"invoice\""}]}]}}`, at)
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func TestLoadInvoice"), identifierWords("TestLoadInvoice"), source), want)
	}
	check("")
	raw, _ := json.Marshal(e)
	wrong := strings.Replace(string(raw), `"argument":"\"invoice\""`, `"argument":"\"other\""`, 1)
	json.Unmarshal([]byte(wrong), &e)
	check("bindings")
	json.Unmarshal(raw, &e)
	wrong = strings.Replace(string(raw), at("func writeFixture"), at("func unrelated"), 1)
	json.Unmarshal([]byte(wrong), &e)
	check("подходящий вызов")
	json.Unmarshal(raw, &e)
	e.ObjectComparison.Expected = e.ObjectComparison.Expected[1:]
	check("полный состав")
	json.Unmarshal(raw, &e)
	e.ObjectComparison.Proof.Words = e.ObjectComparison.Proof.Words[1:]
	check("каждое слово")
}

func TestNameChainPreservesPromisedWrite(t *testing.T) {
	const fixture = `package sample
type Item struct { Objects, Attributes int }
func addInvoiceAttributes(item *Item, other *Item, value int) {
 fill(item, value)
 marker := 1
 _ = marker
 consume(item)
}
func fill(item *Item, value int) {
 item.Objects = value
}
func consume(item *Item) int { return item.Objects }
var handlers = map[string]func(*Item) int{
 "objects": func(item *Item) int {
  return item.Objects
 },
}
`
	source, at := nameProofFixture(t, fixture)
	e := namingWire(t, `{"check":"writes","result":"violated","object_comparison":{"expected":["Invoice","Attributes"],"actual":"Objects","actual_at":"@item.Objects =","proof":{"expression":"item.Objects","words":[{"word":"Invoice","role":"context","fact":{"location":"@item.Objects =","quote":"item.Objects = value","value":"value","text":"Запись при обработке счёта."}},{"word":"Attributes","role":"context","fact":{"location":"@item.Objects =","quote":"item.Objects = value","value":"value","text":"Фактически меняются Objects."}}],"assignment":{"writes":[{"at":"@item.Objects =","target":"item.Objects","value":"value"}],"use":{"declaration":"@func consume","at":"@func consume","expression":"item.Objects"}}}},"write_evidence":{"word":"Attributes","target":"item.Objects","value":"value","write_at":"@item.Objects =","calls":[{"at":"@fill(item, value)","declaration":"@func fill","bindings":[{"parameter":"item","argument":"item"},{"parameter":"value","argument":"value"}]}]}}`, at)
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), source), want)
	}
	check("")
	use := e.ObjectComparison.Proof.Assignment.Use
	e.ObjectComparison.Proof.Assignment.Use = NameCodeUse{Declaration: at("\"objects\": func"), At: at("  return item.Objects"), Expression: "item.Objects"}
	check("читает Objects")
	e.ObjectComparison.Proof.Assignment.Use.Declaration = e.ObjectComparison.Proof.Assignment.Use.At
	check("строку объявления")
	assertNameRepairStage(t, checkNameEntry(e, at("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), source), "writes", "evidence", "missing_consumer", "Objects", "Продолжите поиск", "текущего поручения")
	e.ObjectComparison.Proof.Assignment.Use = use
	e.ObjectComparison.Proof.Assignment.Use.Expression = "item.Attributes"
	consumerErr := checkNameEntry(e, at("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), source)
	assertTargetCheck(t, consumerErr, "читает Objects")
	assertNameRepairStage(t, consumerErr, "writes", "evidence", "missing_consumer", "Objects", "proof.assignment.use", "повторите audit-progress")
	e.ObjectComparison.Proof.Assignment.Use = use
	check("")
	otherSource, otherAt := nameProofFixture(t, strings.Replace(fixture, "consume(item)", "consume(other)", 1))
	wrongInstance := checkNameEntry(e, otherAt("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), otherSource)
	assertTargetCheck(t, wrongInstance, "читает Objects")
	assertNameRepairStage(t, wrongInstance, "writes", "evidence", "missing_consumer", "того же экземпляра", "proof.assignment.use", "write_evidence.calls")
	changedSource, changedAt := nameProofFixture(t, strings.Replace(fixture, "marker := 1\n _ = marker", "item.Objects = 2\n _ = item", 1))
	assertTargetCheck(t, checkNameEntry(e, changedAt("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), changedSource), "читает Objects")
	alias := strings.NewReplacer("consume(item)", "consume(item, item)",
		"func consume(item *Item) int { return item.Objects }", "func consume(item, alias *Item) int { alias.Objects = 0; return item.Objects }")
	aliasedSource, aliasedAt := nameProofFixture(t, alias.Replace(fixture))
	assertTargetCheck(t, checkNameEntry(e, aliasedAt("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), aliasedSource), "читает Objects")
	localAlias := strings.Replace(fixture, "int { return item.Objects }", "int { alias := item; alias.Objects = 0; return item.Objects }", 1)
	localSource, localAt := nameProofFixture(t, localAlias)
	assertTargetCheck(t, checkNameEntry(e, localAt("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), localSource), "читает Objects")
	e.Result = "checked"
	check("не подтверждает объект")
	assertNameRepairStage(t, checkNameEntry(e, at("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), source), "writes", "conclusion", "write_object")
	e.ObjectComparison.Proof.Assignment.Use.Declaration = at("item.Objects =")
	err := checkNameEntry(e, at("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), source)
	assertTargetCheck(t, err, "не подтверждает объект")
	assertTargetCheck(t, err, "строку объявления")
	object := "code.go:1#addInvoiceAttributes"
	rule := Rule{Objects: []string{object}, Files: []FileReview{{Path: "code.go", Objects: []string{object}}}}
	state := describeNameProgress(rule, rule.Files, newObjectIssue(namingRule, evidenceKey{object, "writes"}, "%w", err))
	encoded, _ := json.Marshal(state.OpenFiles[0].Goals[0])
	var wire struct{ Blockers []struct{ Stage string } }
	json.Unmarshal(encoded, &wire)
	if len(wire.Blockers) != 2 || wire.Blockers[0].Stage != "conclusion" || wire.Blockers[1].Stage != "evidence" {
		t.Fatalf("ошибка доказательства скрыта за противоречием: %s", encoded)
	}
}

func TestNameChainSQLContextAndActualArguments(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
func TestInvoiceRefusal(db DB, id string) {
 db.ExecContext(ctx, "UPDATE customers SET name = '' WHERE id = $1", id)
}
`)
	e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["Test","Invoice","Refusal"],"actual":"customers","actual_at":"@db.ExecContext","proof":{"expression":"db.ExecContext(ctx, \"UPDATE customers SET name = '' WHERE id = $1\", id)","words":[],"sql":{"table":"customers","statement":"UPDATE customers SET name = '' WHERE id = $1","arguments":["id"]}}},"write_evidence":{"word":"Refusal","target":"customers","value":"пустое имя","write_at":"@db.ExecContext"}}`, at)
	for _, word := range e.ObjectComparison.Expected {
		e.ObjectComparison.Proof.Words = append(e.ObjectComparison.Proof.Words, NameProofWord{Word: word, Role: "context", Fact: ReviewFact{Location: at("db.ExecContext"), Quote: `db.ExecContext(ctx, "UPDATE customers SET name = '' WHERE id = $1", id)`, Value: "id", Text: "Подготовка записи для сценария отказа."}})
	}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func TestInvoiceRefusal"), identifierWords("TestInvoiceRefusal"), source), want)
	}
	check("")
	raw, _ := json.Marshal(e)
	wrong := strings.Replace(string(raw), `"arguments":["id"]`, `"arguments":["other"]`, 1)
	json.Unmarshal([]byte(wrong), &e)
	check("аргумент отличается")
	json.Unmarshal(raw, &e)
	e.ObjectComparison.Proof.Words[0].Role = "object"
	check("не подтверждает объект")
}

func TestNameChainCreatedObjectCannotBecomeContext(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Archive struct { Invoice string }
func createInvoice() []Archive {
 archives := []Archive{{Invoice: "invoice"}}
 return archives
}
func createInvoiceArchives() []Archive {
 packed := []Archive{{Invoice: "invoice"}}
 return packed
}
`)
	word := "Invoice"
	e := Evidence{Check: "result", Result: "checked", ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "archives", ActualAt: at("return archives"), Proof: &NameObjectProof{Expression: "archives", Type: "[]Archive", Words: []NameProofWord{{Word: word, Role: "context", Fact: ReviewFact{Location: at("archives :="), Quote: `archives := []Archive{{Invoice: "invoice"}}`, Value: "archives", Text: "Архив содержит счёт."}}}}}, ResultEvidence: &ResultEvidence{Word: &word, Returned: "archives", ReturnAt: at("return archives"), Consumer: "caller", ConsumerAt: at("func createInvoice()")}}
	assertTargetCheck(t, checkNameEntry(e, at("func createInvoice()"), identifierWords("createInvoice"), source), "object")
	e.ObjectComparison.Proof.Words[0].Role = "object"
	assertTargetCheck(t, checkNameEntry(e, at("func createInvoice()"), identifierWords("createInvoice"), source), "фактический тип результата")
	assertNameRepairStage(t, checkNameEntry(e, at("func createInvoice()"), identifierWords("createInvoice"), source), "result", "conclusion", "result_object")
	e.Result = "violated"
	assertTargetCheck(t, checkNameEntry(e, at("func createInvoice()"), identifierWords("createInvoice"), source), "")
	e.Result = "checked"
	word = "Archives"
	e.ObjectComparison.Expected = []string{"Invoice", "Archives"}
	e.ObjectComparison.Actual = "packed"
	e.ObjectComparison.ActualAt = at("return packed")
	e.ObjectComparison.Proof.Expression = "packed"
	e.ObjectComparison.Proof.Words = []NameProofWord{
		{Word: "Invoice", Role: "context", Fact: ReviewFact{Location: at("packed :="), Quote: `packed := []Archive{{Invoice: "invoice"}}`, Value: "packed", Text: "Содержимое архива относится к счёту."}},
		{Word: "Archives", Role: "object", Fact: ReviewFact{Location: at("packed :="), Quote: `packed := []Archive{{Invoice: "invoice"}}`, Value: `[]Archive{{Invoice: "invoice"}}`, Text: "Возвращается коллекция архивов."}},
	}
	e.ResultEvidence.Returned = "packed"
	e.ResultEvidence.ReturnAt = at("return packed")
	assertTargetCheck(t, checkNameEntry(e, at("func createInvoiceArchives"), identifierWords("createInvoiceArchives"), source), "")
}

func assertNameRepairStage(t *testing.T, err error, check, stage, code string, nextContains ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("ожидался отказ")
	}
	object := "code.go:1#operation"
	rule := Rule{Objects: []string{object}, Files: []FileReview{{Path: "code.go", Objects: []string{object}}}}
	state := describeNameProgress(rule, rule.Files, newObjectIssue(namingRule, evidenceKey{object, check}, "проверка: %w", err))
	encoded, _ := json.Marshal(state)
	var wire struct {
		OpenFiles []struct {
			Goals []struct {
				Blockers []struct{ Check, Stage, Code, Message, Next string }
			} `json:"goals"`
		} `json:"open_files"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.OpenFiles) != 1 || len(wire.OpenFiles[0].Goals) != 1 || len(wire.OpenFiles[0].Goals[0].Blockers) != 1 {
		t.Fatalf("нет структурированного отказа: %s", encoded)
	}
	blocker := wire.OpenFiles[0].Goals[0].Blockers[0]
	if blocker.Check != check || blocker.Stage != stage || blocker.Code != code || blocker.Next == "" || !strings.Contains(blocker.Message, err.Error()) {
		t.Fatalf("неверный следующий шаг: %+v", blocker)
	}
	for _, detail := range nextContains {
		if !strings.Contains(blocker.Next, detail) {
			t.Errorf("следующий шаг не содержит %q: %s", detail, blocker.Next)
		}
	}
	if (stage == "conclusion" || code == "missing_consumer") && !strings.Contains(state.String(), blocker.Next) {
		t.Fatalf("следующий шаг отсутствует в текстовом выводе: %s", state.String())
	}
}

func TestNameRepairDoesNotClassifyErrorsByWording(t *testing.T) {
	assertNameRepairStage(t, fmt.Errorf("имя обещает Invoice, но это ошибка заполнения"), "result", "evidence", "invalid_evidence")
}

func TestNameChainTemplatesExposeEvidence(t *testing.T) {
	for _, tc := range []struct{ check, kind, field string }{
		{"result", "use", "use_expression"}, {"writes", "sql", "statement"}, {"writes", "destination", "words"},
	} {
		entries, err := EvidenceTemplates(".", "STYLE-001", tc.check, "sample.go:1#TestLoadInvoice", "checked", tc.kind)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"`+tc.field+`"`) {
			t.Fatalf("шаблон %s не содержит %s: %s", tc.kind, tc.field, data)
		}
	}
}

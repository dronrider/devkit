package reviewaudit

import "testing"

func TestNameAssignmentFieldDeclarationAndGlobalArgument(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
type Address struct {
 file string
}
func resolve() Address {
 var a Address
 a.file = "path"
 return a
}
func consume() string {
 a := resolve()
 return a.file
}
var timeout int
func TestTimeout() {
 timeout = 0
}
func use() int {
 return add(timeout)
}
func add(n int) int { return n }
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func resolve"))
	writeAt := at("a.file =")
	proof := &NameObjectProof{Expression: "a.file", Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: writeAt, Target: "a.file", Value: `"path"`}}, Use: NameCodeUse{Declaration: at("func consume"), At: at("return a.file"), Expression: "a.file"}}}
	e := Evidence{ObjectComparison: &NameObjectComparison{Actual: "file", ActualAt: at(" file string"), Proof: proof}, WriteEvidence: &WriteEvidence{WriteAt: writeAt}}
	if err := g.checkNameAssignment(e, fn); err != nil {
		t.Fatal(err)
	}
	proof.Expression = `a.file = "other"`
	if err := g.checkNameAssignment(e, fn); err == nil {
		t.Fatal("подменённая правая часть принята")
	}
	proof.Expression = `a.file = "path"`
	if err := g.checkNameAssignment(e, fn); err != nil {
		t.Fatal(err)
	}
	e.ObjectComparison.ActualAt = at("var timeout")
	if err := g.checkNameAssignment(e, fn); err == nil {
		t.Fatal("чужое объявление принято")
	}
	fn, _ = g.declaration(at("func TestTimeout"))
	writeAt = at("timeout = 0")
	proof = &NameObjectProof{Expression: "timeout", Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: writeAt, Target: "timeout", Value: "0"}}, Use: NameCodeUse{Declaration: at("func use"), At: at("return add"), Expression: "timeout"}}}
	e = Evidence{ObjectComparison: &NameObjectComparison{Actual: "timeout", ActualAt: writeAt, Proof: proof}, WriteEvidence: &WriteEvidence{WriteAt: writeAt}}
	if err := g.checkNameAssignment(e, fn); err != nil {
		t.Fatal(err)
	}
}

func TestNameAssignmentRejectsShadowedGlobal(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
var timeout int
func TestTimeout() {
 timeout := 0
 _ = timeout
}

func use() int {
 return timeout
}
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func TestTimeout"))
	writeAt := at("timeout := 0")
	proof := &NameObjectProof{Expression: "timeout", Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: writeAt, Target: "timeout", Value: "0"}}, Use: NameCodeUse{Declaration: at("func use"), At: at("return timeout"), Expression: "timeout"}}}
	e := Evidence{ObjectComparison: &NameObjectComparison{Actual: "timeout", ActualAt: writeAt, Proof: proof}, WriteEvidence: &WriteEvidence{WriteAt: writeAt}}
	if err := g.checkNameAssignment(e, fn); err == nil {
		t.Fatal("локальное значение принято за глобальное")
	}
}

func TestNameAssignmentReadsGlobalArgument(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
var timeout int
func TestTimeout() {
 timeout = 0
}
func use() int {
 return add(timeout)
}
func add(n int) int { return n }
`)
	g := newQueryGoSource(source)
	fn, _ := g.declaration(at("func TestTimeout"))
	writeAt := at("timeout = 0")
	proof := &NameObjectProof{Expression: "timeout", Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: writeAt, Target: "timeout", Value: "0"}}, Use: NameCodeUse{Declaration: at("func use"), At: at("return add"), Expression: "timeout"}}}
	e := Evidence{ObjectComparison: &NameObjectComparison{Actual: "timeout", ActualAt: writeAt, Proof: proof}, WriteEvidence: &WriteEvidence{WriteAt: writeAt}}
	if err := g.checkNameAssignment(e, fn); err != nil {
		t.Fatal(err)
	}
}

func TestNameAssignmentChecksValueAndWrittenObject(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
 type Item struct { Attributes, Objects int }
 func addInvoiceAttributes(item *Item, value int) {
  item.Attributes = value
  item.Objects = value
 }
 func consume(item *Item) int { return item.Attributes }
 `)
	word := "Attributes"
	writeAt := at("item.Attributes = value")
	proof := &NameObjectProof{Expression: "item.Attributes", Assignment: &NameAssignmentWrite{
		Writes: []NameAssignmentSite{{At: writeAt, Target: "item.Attributes", Value: "value"}},
		Use:    NameCodeUse{Declaration: at("func consume"), At: at("func consume"), Expression: "item.Attributes"},
	}, Words: []NameProofWord{
		{Word: "Invoice", Role: "context", Fact: ReviewFact{Location: at("func consume"), Quote: "return item.Attributes", Value: "item.Attributes", Text: "Читается атрибут."}},
		{Word: word, Role: "object", Fact: ReviewFact{Location: writeAt, Quote: "item.Attributes = value", Value: "item.Attributes", Text: "Поле записывается."}},
	}}
	entry := Evidence{Check: "writes", Result: "checked", ObjectComparison: &NameObjectComparison{Expected: []string{"Invoice", word}, Actual: "Attributes", ActualAt: writeAt, Proof: proof},
		WriteEvidence: &WriteEvidence{Word: &word, Target: "item.Attributes", Value: "value", WriteAt: writeAt}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(entry, at("func addInvoiceAttributes"), identifierWords("addInvoiceAttributes"), source), want)
	}
	check("")
	proof.Assignment.Writes[0].Value = "other"
	check("должно получать правую часть")
	proof.Assignment.Writes[0].Value = "value"
	proof.Assignment.Use = NameCodeUse{Declaration: at("func addInvoiceAttributes"), At: writeAt, Expression: "item.Attributes"}
	check("читает Attributes")
	ref := at("item.Objects = value")
	entry.ObjectComparison.Actual, entry.ObjectComparison.ActualAt = "Objects", ref
	entry.WriteEvidence.Target, entry.WriteEvidence.WriteAt = "item.Objects", ref
	proof.Expression = "item.Objects"
	proof.Assignment.Writes = []NameAssignmentSite{{At: ref, Target: "item.Objects", Value: "value"}}
	check("не подтверждает объект")
}

func TestNameCLIUsesDestinationAndSelectedArtifact(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
 import "github.com/stretchr/testify/require"
 func exportSingleToken(t T, dest string) {
  run := Run("key", "export", dest)
  CheckCode(run, 0)
  unrelated := Read("somewhere")
  entries := Read(dest)
  require.Len(t, entries, 1)
  require.Len(t, entries, 2)
  require.Len(t, unrelated, 1)
  archive := entries["token.zip"]
  inspect(t, archive.body)
 }
 func inspect(t T, body []byte) {
  require.NotEmpty(t, body)
 }
 `)
	word := "Token"
	count := "require.Len(t, entries, 1)"
	c := &NameCLIWrite{Command: []string{"key", "export"}, Destination: "dest", SuccessAt: at("CheckCode"), Success: "CheckCode(run, 0)", ReadAt: at("entries :="), Read: "Read(dest)", CountAt: at(count), Count: count, ArtifactAt: at("archive :="), Artifact: "archive", Key: `"token.zip"`, ContentAt: at("inspect(t, archive"), Content: "inspect(t, archive.body)", ContentChecks: []NameCodeUse{{Declaration: at("func inspect"), At: at("require.NotEmpty"), Expression: "require.NotEmpty(t, body)"}}}
	entry := Evidence{Check: "writes", Result: "checked", ObjectComparison: &NameObjectComparison{Expected: []string{"Single", word}, Actual: "archive", ActualAt: c.ArtifactAt, Proof: &NameObjectProof{
		Expression: `Run("key", "export", dest)`, CLI: c, Words: []NameProofWord{
			{Word: "Single", Role: "condition", Fact: ReviewFact{Location: c.CountAt, Quote: count, Value: "entries", Text: "Проверяется один артефакт."}},
			{Word: word, Role: "object", Fact: ReviewFact{Location: c.ArtifactAt, Quote: `entries["token.zip"]`, Value: `"token.zip"`, Text: "Выбирается записанный архив токена."}},
		},
	}}, WriteEvidence: &WriteEvidence{Word: &word, Target: "archive", Value: "архив", WriteAt: at("run :=")}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(entry, at("func exportSingleToken"), identifierWords("exportSingleToken"), source), want)
	}
	check("")
	for _, wrong := range []string{"require.Len(t, entries, 2)", "require.Len(t, unrelated, 1)"} {
		c.CountAt, c.Count = at(wrong), wrong
		check("proof.cli.count: покажите проверку одного артефакта в прочитанном каталоге")
	}
	c.CountAt, c.Count = at(count), count
	c.ReadAt, c.Read = at("unrelated :="), `Read("somewhere")`
	check("чтение каталога, переданного команде")
	c.ReadAt, c.Read = at("entries :="), "Read(dest)"
	c.Key = `"other.zip"`
	check("по ожидаемому имени")
	c.Command = []string{"key", "export", "--flag"}
	c.Success = "0"
	c.CountAt, c.Count = "", ""
	err := checkNameEntry(entry, at("func exportSingleToken"), identifierWords("exportSingleToken"), source)
	for _, field := range []string{"command", "success", "count", "key"} {
		assertTargetCheck(t, err, "proof.cli."+field+":")
	}
}

func TestNameAPIOnlyUsesSchemasOfSuccessfulResponses(t *testing.T) {
	lines := []string{
		"paths:", "  /keys:", "    post:", "      responses:", "        '200':", "          schema:", "            $ref: '#/components/schemas/Success'",
		"        '404':", "          schema:", "            $ref: '#/components/schemas/Failure'",
		"components:", "  schemas:", "    Success:", "      allOf:", "        - $ref: '#/components/schemas/Result'",
		"    Result:", "      properties:", "        license_key: {}", "    Failure:", "      properties:", "        license_key: {}",
	}
	if !nameAPIResponseHasField(lines, 2, 17) || nameAPIResponseHasField(lines, 2, 20) {
		t.Fatal("поле должно быть достижимо только из схемы успешного ответа")
	}
}

func TestNameAssignmentRejectsUnrelatedFieldConsumer(t *testing.T) {
	for _, tc := range []struct {
		name, bindings string
	}{
		{"independent_instance", `a := Address{file: "different"}`},
		{"changed_field", "a := resolve()\n a.file = \"different\""},
		{"shadowed_call", "resolve := func() Address { return Address{} }\n a := resolve()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
type Address struct {
 file string
}
func resolve() Address {
 var a Address
 a.file = "written"
 return a
}
func consume() string {
 `+tc.bindings+`
 return a.file
}
`)
			g := newQueryGoSource(source)
			fn, _ := g.declaration(at("func resolve"))
			writeAt := at("a.file = \"written\"")
			proof := &NameObjectProof{Expression: "a.file", Assignment: &NameAssignmentWrite{
				Writes: []NameAssignmentSite{{At: writeAt, Target: "a.file", Value: `"written"`}},
				Use:    NameCodeUse{Declaration: at("func consume"), At: at("return a.file"), Expression: "a.file"},
			}}
			e := Evidence{
				ObjectComparison: &NameObjectComparison{Actual: "file", ActualAt: at("file string"), Proof: proof},
				WriteEvidence:    &WriteEvidence{WriteAt: writeAt},
			}
			if err := g.checkNameAssignment(e, fn); err == nil {
				t.Fatal("принято чтение поля другого значения")
			}
		})
	}
}

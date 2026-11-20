package reviewaudit

import "testing"

func TestNameAssignmentSynctestScenario(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "testing/synctest"
func TestTurn(t *testing.T) {
 t.Run("one", func(t *testing.T) {
  synctest.Test(t, func(t *testing.T) {
   turn := makeTurn()
   go func() { use(turn) }()
   func() { turn := makeTurn(); use(turn) }()
   unused := func() { use(turn) }; _ = unused
  })
  synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) })
 })
}
`)
	word := "Turn"
	write, use := at("turn := makeTurn()"), at("go func()")
	e := Evidence{Check: "writes", Result: "checked", ObjectComparison: &NameObjectComparison{Expected: []string{"Test", word}, Actual: "turn", ActualAt: write,
		Proof: &NameObjectProof{Expression: "turn", Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: write, Target: "turn", Value: "makeTurn()"}},
			Use: NameCodeUse{Declaration: at("func TestTurn"), At: use, Expression: "turn"}}, Words: []NameProofWord{
			{Word: "Test", Role: "context", Fact: ReviewFact{Location: use, Quote: "use(turn)", Value: "turn", Text: "Сценарий читает фикстуру."}},
			{Word: word, Role: "object", Fact: ReviewFact{Location: write, Quote: "turn := makeTurn()", Value: "turn", Text: "Создаётся фикстура."}},
		}}}, WriteEvidence: &WriteEvidence{Word: &word, Target: "turn", Value: "makeTurn()", WriteAt: write}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func TestTurn"), identifierWords("TestTurn"), source), want)
	}
	check("")
	for _, read := range []string{at("func() { turn :="), at("unused :="), at("synctest.Test(t, func(t *testing.T) { turn :=")} {
		e.ObjectComparison.Proof.Assignment.Use.At = read
		check("proof.assignment.use")
	}
}

func TestNameAssignmentSynctestRejectsForeignCallbacks(t *testing.T) {
	for _, tc := range []struct{ name, imports, body string }{
		{"foreign package", `import "example.com/synctest"`, `synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) })`},
		{"shadowed package", `import "testing/synctest"`, `synctest := fake; synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) })`},
		{"uninvoked closure", `import "testing/synctest"`, `f := func() { synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) }) }; _ = f`},
		{"unrelated callback", `import "testing/synctest"`, `other(func(t *testing.T) { turn := makeTurn(); use(turn) })`},
		{"foreign Run", `import "testing/synctest"`, `fake.Run("x", func(t *testing.T) { synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) }) })`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, "package sample\nimport \"testing\"\n"+tc.imports+"\nfunc TestTurn(t *testing.T) {\n"+tc.body+"\n}\n")
			g := newQueryGoSource(source)
			fn, err := g.declaration(at("func TestTurn"))
			if err != nil {
				t.Fatal(err)
			}
			if got := g.nameSynctestNode(fn, at("turn := makeTurn()"), "turn", true); got != nil {
				t.Fatal("accepted assignment in unrelated callback")
			}
		})
	}
}

package reviewaudit

import (
	"go/ast"
	"testing"
)

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

func TestNameAssignmentSynctestSubtestsAndNestedClosure(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "testing/synctest"
func TestTurn(t *testing.T) {
 synctest.Test(t, func(t *testing.T) {
  var turn int
  t.Run("a", func(t *testing.T) {
   turn = 1
   use(turn)
  })
  t.Run("b", func(t *testing.T) {
   use(turn)
  })
  remember(func() { use(turn) })
 })
}
`)
	g := newQueryGoSource(source)
	fn, err := g.declaration(at("func TestTurn"))
	if err != nil {
		t.Fatal(err)
	}
	write := g.nameSynctestNode(fn, at("turn = 1"), "turn", true)
	if write == nil {
		t.Fatal("subtest assignment not found")
	}
	same := g.nameSynctestNode(fn, at("   use(turn)"), "turn", false)
	if same == nil || same.scenario != write.scenario {
		t.Fatal("same subtest read not found")
	}
	// The second occurrence is deliberately addressed by its own line.
	otherAt := at(`t.Run("b"`)
	_, line, _, err := evidenceLocation(otherAt)
	if err != nil {
		t.Fatal(err)
	}
	other := g.nameSynctestNode(fn, nameRef(fn, line+1), "turn", false)
	if other == nil || other.scenario == write.scenario {
		t.Fatal("sibling subtest shares scenario")
	}
	closure := g.nameSynctestNode(fn, at("remember(func()"), "remember(func() { use(turn) })", false)
	if closure == nil {
		t.Fatal("consumer call not found")
	}
	stmt := write.node.(*ast.AssignStmt)
	if nameSynctestUsesBinding(closure.node, nameWriteRoot(stmt.Lhs[0]).Obj, "turn") {
		t.Fatal("uninvoked closure counted as read")
	}
}

func TestNameAssignmentSynctestFieldPaths(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "testing"
import "testing/synctest"
func TestField(t *testing.T) {
 synctest.Test(t, func(t *testing.T) {
  x := fixture()
  x.a.field = 1
  use(x.a.field)
  use(x.b.field)
 })
}
`)
	g := newQueryGoSource(source)
	fn, err := g.declaration(at("func TestField"))
	if err != nil {
		t.Fatal(err)
	}
	write := g.nameSynctestNode(fn, at("x.a.field ="), "x.a.field", true)
	if write == nil {
		t.Fatal("assignment not found")
	}
	root := nameWriteRoot(write.node.(*ast.AssignStmt).Lhs[0]).Obj
	for _, tc := range []struct {
		line, expression string
		want             bool
	}{
		{"use(x.a.field)", "use(x.a.field)", true},
		{"use(x.b.field)", "use(x.b.field)", false},
	} {
		read := g.nameSynctestNode(fn, at(tc.line), tc.expression, false)
		if read == nil || nameSynctestUsesBinding(read.node, root, "x.a.field") != tc.want {
			t.Errorf("incorrect proof for %s", tc.line)
		}
	}
}

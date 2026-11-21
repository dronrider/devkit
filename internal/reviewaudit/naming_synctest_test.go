package reviewaudit

import (
	"go/ast"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// synctestProofFixture supplies a single source file without invoking git.
func synctestProofFixture(t *testing.T, code string) (*evidenceSource, func(string) string) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "sample.go"), code)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	source := &evidenceSource{root: root, reportPath: filepath.Join(root, "report.json"), files: files, cache: map[string][]string{}, names: map[string]bool{"sample.go": true}}
	return source, func(text string) string {
		i := strings.Index(code, text)
		if i < 0 {
			t.Fatalf("no %q in fixture", text)
		}
		return "sample.go:" + strconv.Itoa(1+strings.Count(code[:i], "\n"))
	}
}

func TestNameAssignmentSynctestScenario(t *testing.T) {
	source, at := synctestProofFixture(t, `package sample
import "testing"
import "testing/synctest"
func TestTurn(t *testing.T) {
 t.Run("one", func(t *testing.T) {
  synctest.Test(t, func(t *testing.T) {
   turn := makeTurn()
   go func() { consume(func() { turn := makeTurn(); use(turn) }, turn) }()
   { turn := makeTurn(); use(turn) }
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
			Use: NameCodeUse{Declaration: at("func TestTurn"), At: use, Expression: "consume(func() { turn := makeTurn(); use(turn) }, turn)"}}, Words: []NameProofWord{
			{Word: "Test", Role: "context", Fact: ReviewFact{Location: use, Quote: "use(turn)", Value: "turn", Text: "Сценарий читает фикстуру."}},
			{Word: word, Role: "object", Fact: ReviewFact{Location: write, Quote: "turn := makeTurn()", Value: "turn", Text: "Создаётся фикстура."}},
		}}}, WriteEvidence: &WriteEvidence{Word: &word, Target: "turn", Value: "makeTurn()", WriteAt: write}}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameEntry(e, at("func TestTurn"), identifierWords("TestTurn"), source), want)
	}
	check("")
	e.ObjectComparison.Proof.Assignment.Use.Expression = "use(turn)"
	for _, read := range []string{at("{ turn :="), at("unused :="), at("synctest.Test(t, func(t *testing.T) { turn :=")} {
		e.ObjectComparison.Proof.Assignment.Use.At = read
		e.ObjectComparison.Proof.Words[0].Fact.Location = read
		check("proof.assignment.use")
	}
}

func TestNameAssignmentSynctestRejectsForeignCallbacks(t *testing.T) {
	for _, tc := range []struct{ name, imports, body, signature string }{
		{"foreign package", `import "example.com/synctest"`, `synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) })`, "(t *testing.T)"},
		{"shadowed package", `import "testing/synctest"`, `synctest := fake; synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) })`, "(t *testing.T)"},
		{"uninvoked closure", `import "testing/synctest"`, `f := func() { synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) }) }; _ = f`, "(t *testing.T)"},
		{"unrelated callback", `import "testing/synctest"`, `other(func(t *testing.T) { turn := makeTurn(); use(turn) })`, "(t *testing.T)"},
		{"foreign Run", `import "testing/synctest"`, `fake.Run("x", func(t *testing.T) { synctest.Test(t, func(t *testing.T) { turn := makeTurn(); use(turn) }) })`, "(t *testing.T)"},
		{"missing testing T", `import "testing/synctest"`, `synctest.Test(nil, func(t *testing.T) { turn := makeTurn(); use(turn) })`, "()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := synctestProofFixture(t, "package sample\nimport \"testing\"\n"+tc.imports+"\nfunc TestTurn"+tc.signature+" {\n"+tc.body+"\n}\n")
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
	source, at := synctestProofFixture(t, `package sample
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
	source, at := synctestProofFixture(t, `package sample
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
func TestNameAssignmentSynctestRejectsShadowedIndex(t *testing.T) {
	source, at := synctestProofFixture(t, `package sample
import "testing"
import "testing/synctest"
func TestTurn(t *testing.T) {
 synctest.Test(t, func(t *testing.T) {
  var x [2]struct { turn int }
  i := 0
  x[i].turn = 1
  { i := 1; use(x[i].turn) }
 })
}
`)
	g := newQueryGoSource(source)
	fn, err := g.declaration(at("func TestTurn"))
	if err != nil {
		t.Fatal(err)
	}
	write, read := at("x[i].turn = 1"), at("i := 1; use(x[i].turn)")
	e := Evidence{ObjectComparison: &NameObjectComparison{Actual: "turn", ActualAt: write, Proof: &NameObjectProof{Expression: "x[i].turn", Assignment: &NameAssignmentWrite{Writes: []NameAssignmentSite{{At: write, Target: "x[i].turn", Value: "1"}}, Use: NameCodeUse{Declaration: at("func TestTurn"), At: read, Expression: "x[i].turn"}}}}, WriteEvidence: &WriteEvidence{WriteAt: write}}
	if err := g.checkNameAssignment(e, fn, fn); err == nil {
		t.Fatal("accepted shadowed index i")
	}
}

func TestNameAssignmentSynctestRejectsComputedTargets(t *testing.T) {
	source, at := synctestProofFixture(t, `package sample
import "testing"
import "testing/synctest"
func TestTargets(t *testing.T) {
 synctest.Test(t, func(t *testing.T) {
  var x struct { field int }
  var a [2]int
  x.field = 1
  a[0] = 1
  use(x.field, a[0])
 })
}
`)
	g := newQueryGoSource(source)
	fn, err := g.declaration(at("func TestTargets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ line, target string }{{"x.field = 1", "x.field"}, {"a[0] = 1", "a[0]"}} {
		write := g.nameSynctestNode(fn, at(tc.line), tc.target, true)
		if write == nil {
			t.Fatalf("assignment %s not found", tc.target)
		}
		if nameSynctestTargetRoot(fn, write.node.(*ast.AssignStmt).Lhs[0]) != nil {
			t.Errorf("accepted computed target %s", tc.target)
		}
	}
}

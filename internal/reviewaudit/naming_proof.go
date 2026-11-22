package reviewaudit

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"slices"
	"strings"
)

// The reviewer interprets words; the checker verifies their cited data dependencies.
type NameObjectProof struct {
	Guards     []NameCodeUse        `json:"guards,omitempty"`
	Expression string               `json:"expression"`
	Type       string               `json:"type,omitempty"`
	Words      []NameProofWord      `json:"words"`
	Reference  *NameReferenceWrite  `json:"reference,omitempty"`
	Assignment *NameAssignmentWrite `json:"assignment,omitempty"`
	API        *NameAPIWrite        `json:"api,omitempty"`
	Setting    *NameSettingWrite    `json:"setting,omitempty"`
	Effect     *NameSystemWrite     `json:"effect,omitempty"`
	SQL        *NameSQLWrite        `json:"sql,omitempty"`
	CLI        *NameCLIWrite        `json:"cli,omitempty"`
}

type NameProofWord struct {
	Word string     `json:"word"`
	Role string     `json:"role"`
	Fact ReviewFact `json:"fact"`
}

func checkNameObjectProof(e Evidence, declaration string, source *evidenceSource) error {
	pair, proof := e.ObjectComparison, e.ObjectComparison.Proof
	if err := checkNameProofWords(pair, source); err != nil {
		return err
	}
	if !isGoRef(declaration) {
		if isPythonNameResult(e, declaration) {
			return checkNamePythonResult(e, declaration, source)
		}
		return fmt.Errorf("proof: разбор выражений для этого языка недоступен; используйте equivalent_at или для одноимённой реализации на Go возьмите --kind source")
	}
	g := newQueryGoSource(source)
	fn, err := g.declaration(declaration)
	if err != nil && e.Check == "writes" {
		fn, err = g.nameProofFunction(declaration, nameObjectSymbol(e.Object))
	}
	if err != nil {
		return err
	}
	for _, word := range proof.Words {
		_, _, base, _ := evidenceLocation(word.Fact.Location)
		if base != fn.base {
			return fmt.Errorf("proof.words: все факты должны относиться к проверяемой ревизии")
		}
	}
	if e.Check == "writes" {
		return g.checkNameWriteProof(e, fn)
	}
	if proof.Reference != nil || proof.Assignment != nil || proof.API != nil || proof.CLI != nil || proof.SQL != nil || proof.Setting != nil || proof.Effect != nil || proof.Type == "" {
		return fmt.Errorf("proof: для результата укажите expression и объявленный type, без reference")
	}
	ret, _, err := g.nameForwardReturn(fn, e.ResultEvidence.ReturnAt)
	if err != nil {
		return err
	}
	slot := slices.IndexFunc(ret.Results, func(expr ast.Expr) bool { return sameNameExpression(expr, proof.Expression) })
	if slot < 0 {
		return fmt.Errorf("proof.expression: укажите полное выражение из return, включая последний вызов")
	}
	expr := ret.Results[slot]
	if pair.Actual != nameReturnedObject(expr) {
		return fmt.Errorf("proof.expression: actual %q обозначает часть выражения; результат целиком обозначен %q", pair.Actual, nameReturnedObject(expr))
	}
	typ := nameResultType(fn, slot)
	if typ == nil || !sameNameExpression(typ, proof.Type) {
		return fmt.Errorf("proof.type: укажите объявленный тип проверяемого результата")
	}
	if flow := e.ResultEvidence.Flow; flow != nil {
		if err := checkNameResultFlow(e, declaration, source, flow, false); err != nil {
			return err
		}
	}
	needed := nameDependencies(fn, expr, ret.Pos())
	for i, word := range proof.Words {
		if err := g.checkNameProofFact(e, fn, ret.End(), typ, needed, word.Fact); err != nil {
			return fmt.Errorf("proof.words[%d] %s: %w", i, word.Word, err)
		}
	}
	return nil
}

func nameResultType(fn queryFunc, slot int) ast.Expr {
	if fn.node.Type.Results != nil {
		for _, field := range fn.node.Type.Results.List {
			count := max(1, len(field.Names))
			if slot < count {
				return field.Type
			}
			slot -= count
		}
	}
	return nil
}

func nameExpression(expr ast.Expr) string {
	var text bytes.Buffer
	_ = printer.Fprint(&text, token.NewFileSet(), expr)
	return text.String()
}

func sameNameExpression(expr ast.Expr, text string) bool {
	parsed, err := parser.ParseExpr(text)
	return err == nil && nameExpression(expr) == nameExpression(parsed)
}

func nameIdentifiers(expr ast.Expr) map[string]bool {
	ids := map[string]bool{}
	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			ids[n.Name] = true
		case *ast.SelectorExpr:
			for id := range nameIdentifiers(n.X) {
				ids[id] = true
			}
			return false
		}
		return true
	})
	return ids
}

func nameIntersects(expr ast.Expr, ids map[string]bool) bool {
	for id := range nameIdentifiers(expr) {
		if ids[id] {
			return true
		}
	}
	return false
}

// Follow inputs to a return within its function. Consumer identity is checked by flow.
func nameDependencies(fn queryFunc, expr ast.Expr, before token.Pos) map[string]bool {
	needed := nameIdentifiers(expr)
	ast.PreorderStack(fn.node.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		if _, closure := n.(*ast.FuncLit); closure {
			return false
		}
		if n == expr {
			for _, parent := range stack {
				if branch, ok := parent.(*ast.IfStmt); ok &&
					(branch.Body.Pos() <= expr.Pos() && expr.End() <= branch.Body.End() ||
						branch.Else != nil && branch.Else.Pos() <= expr.Pos() && expr.End() <= branch.Else.End()) {
					for id := range nameIdentifiers(branch.Cond) {
						needed[id] = true
					}
				}
			}
		}
		return true
	})
	for {
		size := len(needed)
		ast.PreorderStack(fn.node.Body, nil, func(n ast.Node, stack []ast.Node) bool {
			if n == nil {
				return true
			}
			if _, closure := n.(*ast.FuncLit); closure || n.Pos() >= before {
				return false
			}
			var outputs, inputs []ast.Expr
			switch n := n.(type) {
			case *ast.AssignStmt:
				outputs, inputs = n.Lhs, n.Rhs
			case *ast.RangeStmt:
				if n.Value != nil {
					outputs = append(outputs, n.Value)
				}
				if n.Key != nil {
					outputs = append(outputs, n.Key)
				}
				inputs = []ast.Expr{n.X}
			case *ast.CallExpr:
				for _, arg := range n.Args {
					if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
						outputs = append(outputs, u.X)
					}
				}
				if len(outputs) > 0 {
					inputs = append(inputs, n.Fun)
					inputs = append(inputs, n.Args...)
				}
			case *ast.ValueSpec:
				for _, id := range n.Names {
					outputs = append(outputs, id)
				}
				inputs = n.Values
			}
			if slices.ContainsFunc(outputs, func(x ast.Expr) bool { return nameIntersects(x, needed) }) {
				for _, parent := range stack {
					switch p := parent.(type) {
					case *ast.IfStmt:
						inputs = append(inputs, p.Cond)
					case *ast.SwitchStmt:
						if p.Tag != nil {
							inputs = append(inputs, p.Tag)
						}
					}
				}
				for _, input := range inputs {
					for id := range nameIdentifiers(input) {
						needed[id] = true
					}
				}
			}
			return true
		})
		if len(needed) == size {
			return needed
		}
	}
}

func (g *queryGoSource) checkNameProofFact(e Evidence, fn queryFunc, before token.Pos, typ ast.Expr, needed map[string]bool, fact ReviewFact) error {
	file, line, base, err := evidenceLocation(fact.Location)
	if err != nil || base != fn.base {
		return fmt.Errorf("факт должен относиться к проверяемой ревизии")
	}
	if fact.Parameter != "" {
		return g.checkNameSourceArgument(fn, needed, fact)
	}
	if file == fn.file && line >= g.fset.Position(fn.node.Pos()).Line && line <= g.fset.Position(before).Line {
		found := false
		ast.PreorderStack(fn.node, nil, func(n ast.Node, stack []ast.Node) bool {
			if _, closure := n.(*ast.FuncLit); closure {
				return false
			}
			if x, ok := n.(ast.Expr); ok && x.Pos() < before && g.fset.Position(x.Pos()).Line == line && sameNameExpression(x, fact.Value) {
				found = nameIntersects(x, needed) || x == typ
				if _, literal := x.(*ast.BasicLit); literal {
					for _, parent := range stack {
						if branch, ok := parent.(*ast.SwitchStmt); ok && branch.Tag != nil && nameIntersects(branch.Tag, needed) {
							found = true
						}
					}
				}
			}
			return true
		})
		if found {
			return nil
		}
	}
	if err := g.checkNameGuard(e, fn, needed, before, fact); err == nil {
		return nil
	}
	flow := e.ResultEvidence.Flow
	if flow != nil && fact.Location == e.ResultEvidence.ConsumerAt {
		x, err := parser.ParseExpr(fact.Value)
		if err == nil && nameIntersects(x, map[string]bool{flow.Value: true}) {
			caller, err := g.nameCaller(flow.Caller)
			if err != nil {
				return err
			}
			for n := range ast.Preorder(caller.node.Body) {
				if x, ok := n.(ast.Expr); ok && g.fset.Position(x.Pos()).Line == line && sameNameExpression(x, fact.Value) {
					return nil
				}
			}
		}
	}
	if g.nameTypeFact(fn, typ, fact) {
		return nil
	}
	return fmt.Errorf("выражение %q не связано с проверяемым результатом; покажите его источник, преобразование или подтверждённое использование; для аргумента вызывающего укажите parameter", fact.Value)
}

func (g *queryGoSource) checkNameSourceArgument(fn queryFunc, needed map[string]bool, fact ReviewFact) error {
	index, position := -1, 0
	for _, field := range fn.node.Type.Params.List {
		for _, param := range field.Names {
			if param.Name == fact.Parameter {
				index = position
			}
			position++
		}
	}
	if index < 0 || !needed[fact.Parameter] {
		return fmt.Errorf("parameter: укажите вход функции, от которого зависит результат")
	}
	file, line, base, _ := evidenceLocation(fact.Location)
	if path.Dir(file) != path.Dir(fn.file) || fn.node.Recv != nil {
		return fmt.Errorf("parameter: требуется прямой вызов функции в том же пакете")
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return err
	}
	for n := range ast.Preorder(tree) {
		call, ok := n.(*ast.CallExpr)
		if ok && nameIdentifier(call.Fun) == fn.node.Name.Name && g.fset.Position(call.Pos()).Line == line &&
			index < len(call.Args) && sameNameExpression(call.Args[index], fact.Value) {
			return nil
		}
	}
	return fmt.Errorf("parameter: выражение %q не передаётся в параметр %s проверяемой функции", fact.Value, fact.Parameter)
}

func (g *queryGoSource) nameTypeFact(fn queryFunc, typ ast.Expr, fact ReviewFact) bool {
	for {
		ptr, ok := typ.(*ast.StarExpr)
		if !ok {
			break
		}
		typ = ptr.X
	}
	file, line, base, _ := evidenceLocation(fact.Location)
	if sel, ok := typ.(*ast.SelectorExpr); ok {
		importPath := g.importPath(fn, nameIdentifier(sel.X))
		if importPath == "" || !strings.HasSuffix(importPath, "/"+path.Dir(file)) {
			return false
		}
	} else if path.Dir(file) != path.Dir(fn.file) {
		return false
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return false
	}
	for node := range ast.Preorder(tree) {
		t, ok := node.(*ast.TypeSpec)
		if !ok || t.Name.Name != typeName(typ) {
			continue
		}
		for n := range ast.Preorder(t) {
			if x, ok := n.(ast.Expr); ok && g.fset.Position(x.Pos()).Line == line && sameNameExpression(x, fact.Value) {
				return true
			}
		}
	}
	return false
}

func checkNameProofWords(pair *NameObjectComparison, source *evidenceSource) error {
	proof := pair.Proof
	if len(pair.Expected) == 0 || len(proof.Words) != len(pair.Expected) {
		return fmt.Errorf("proof.words: подтвердите каждое слово expected, сохранив их порядок и состав")
	}
	for i, word := range proof.Words {
		if word.Word != pair.Expected[i] || !slices.Contains([]string{"object", "source", "condition", "format", "context"}, word.Role) {
			return fmt.Errorf("proof.words[%d]: укажите слово %q и его роль object, source, condition, format или context", i, pair.Expected[i])
		}
		if err := checkReviewFact("statement", word.Fact, source); err != nil {
			return fmt.Errorf("proof.words[%d]: %w", i, err)
		}
		if word.Fact.Value == "" || !strings.Contains(word.Fact.Quote, word.Fact.Value) {
			return fmt.Errorf("proof.words[%d]: укажите выражение value из цитаты", i)
		}
	}
	return nil
}

func nameProofObjects(pair *NameObjectComparison) []string {
	if pair.Proof == nil {
		return pair.Expected
	}
	var words []string
	for _, w := range pair.Proof.Words {
		if w.Role == "object" {
			words = append(words, w.Word)
		}
	}
	return words
}
func checkNamePromisedWrite(e Evidence, fn queryFunc) error {
	pair := e.ObjectComparison
	if e.Result != "checked" || len(pair.Expected) == 0 || !namesResultOrWrite("writes", identifierWords(fn.node.Name.Name)) {
		return nil
	}
	actual := identifierWords(pair.Actual)
	if pair.Proof != nil && pair.Proof.Reference != nil {
		actual = identifierWords(pair.Proof.Reference.TargetTable)
	}
	if len(actual) == 0 || !sameObjectName(pair.Expected[len(pair.Expected)-1:], actual[len(actual)-1:]) {
		return nameConclusionError("write_object", "writes: запись %q не подтверждает объект %q", pair.Actual, strings.Join(pair.Expected, " "))
	}
	return nil
}

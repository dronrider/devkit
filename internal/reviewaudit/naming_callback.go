package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
)

type NameCallbackWrite struct {
	CallAt   string      `json:"call_at"`
	Helper   string      `json:"helper"`
	InvokeAt string      `json:"invoke_at"`
	Use      NameCodeUse `json:"use"`
}

type nameGuardedCallback struct {
	literal *ast.FuncLit
	guard   *ast.IfStmt
	data    string
}

// Resolve a literal passed to a helper that returns early when that literal
// reports no change. Keep this tied to the actual argument and parameter.
func nameCallbackGuard(helper queryFunc, call *ast.CallExpr) nameGuardedCallback {
	index := 0
	for _, field := range helper.node.Type.Params.List {
		typ, isCallback := field.Type.(*ast.FuncType)
		for _, param := range field.Names {
			if isCallback && index < len(call.Args) && typ.Results != nil &&
				len(typ.Results.List) == 1 && nameIdentifier(typ.Results.List[0].Type) == "bool" {
				if literal, ok := call.Args[index].(*ast.FuncLit); ok {
					if guard := nameFindCallbackGuard(helper, param, literal); guard.guard != nil {
						return guard
					}
				}
			}
			index++
		}
	}
	return nameGuardedCallback{}
}

func nameFindCallbackGuard(helper queryFunc, param *ast.Ident, literal *ast.FuncLit) nameGuardedCallback {
	for n := range ast.Preorder(helper.node.Body) {
		if branch, ok := n.(*ast.BranchStmt); ok && branch.Tok == token.GOTO {
			return nameGuardedCallback{}
		}
	}
	for _, stmt := range helper.node.Body.List {
		guard, ok := stmt.(*ast.IfStmt)
		if !ok || guard.Init != nil || guard.Else != nil || len(guard.Body.List) != 1 {
			continue
		}
		if _, ok := guard.Body.List[0].(*ast.ReturnStmt); !ok {
			continue
		}
		negated, ok := guard.Cond.(*ast.UnaryExpr)
		if !ok || negated.Op != token.NOT {
			continue
		}
		invoke, ok := negated.X.(*ast.CallExpr)
		if !ok || nameIdentifier(invoke.Fun) != param.Name || len(invoke.Args) != 1 ||
			!nameValueUnchangedWithin(helper.node.Body, param, invoke, param.Name) {
			continue
		}
		address, ok := invoke.Args[0].(*ast.UnaryExpr)
		if ok && address.Op == token.AND && nameIdentifier(address.X) != "" {
			return nameGuardedCallback{literal: literal, guard: guard, data: nameIdentifier(address.X)}
		}
	}
	return nameGuardedCallback{}
}

func (callback nameGuardedCallback) alwaysFalse() bool {
	if callback.guard == nil || len(callback.literal.Body.List) == 0 {
		return false
	}
	if _, ok := callback.literal.Body.List[len(callback.literal.Body.List)-1].(*ast.ReturnStmt); !ok {
		return false
	}
	allFalse := true
	ast.Inspect(callback.literal.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			allFalse = allFalse && len(node.Results) == 1 && nameIdentifier(node.Results[0]) == "false"
		case *ast.ValueSpec:
			allFalse = allFalse && !slices.ContainsFunc(node.Names, func(name *ast.Ident) bool { return name.Name == "false" })
		case ast.Stmt:
			allFalse = allFalse && !nameDeclaresLocal(node, "false")
		}
		return allFalse
	})
	return allFalse
}

func (callback nameGuardedCallback) argumentWrite() ast.Node {
	if callback.guard == nil {
		return nil
	}
	params := paramNames(callback.literal.Type.Params)
	if len(params) != 1 || params[0] == "" {
		return nil
	}
	for n := range ast.Preorder(callback.literal.Body) {
		var targets []ast.Expr
		switch s := n.(type) {
		case *ast.AssignStmt:
			targets = s.Lhs
		case *ast.IncDecStmt:
			targets = []ast.Expr{s.X}
		}
		for _, target := range targets {
			if nameIdentifier(target) == "" {
				if root := nameWriteRoot(target); root != nil && root.Name == params[0] {
					return n
				}
			}
		}
	}
	return nil
}

func checkNameCallbackWrite(e Evidence, declaration string, source *evidenceSource) error {
	w, pair := e.WriteEvidence, e.ObjectComparison
	proof, c := pair.Proof, w.Callback
	if w.File != nil || len(w.Calls) != 0 || proof == nil || pair.EquivalentAt != "" || pair.EquivalentFlow != nil ||
		proof.Assignment != nil || proof.API != nil || proof.CLI != nil || proof.Reference != nil || proof.SQL != nil || proof.Setting != nil {
		return fmt.Errorf("write_evidence.callback: укажите доказательства callback и слова имени одной записью")
	}
	if err := checkNameProofWords(pair, source); err != nil {
		return err
	}
	g := newQueryGoSource(source)
	fn, err := g.nameProofFunction(declaration, nameObjectSymbol(e.Object))
	if err != nil {
		return err
	}
	helper, err := g.declaration(c.Helper)
	if err != nil {
		return err
	}
	call, _, err := g.call(fn, c.CallAt, func(expr ast.Expr) bool { return calledName(expr) == helper.node.Name.Name })
	if err != nil {
		return err
	}
	if err := g.receiver(fn, helper, call, ""); err != nil {
		return err
	}
	callback := nameCallbackGuard(helper, call)
	if callback.guard == nil || c.InvokeAt != nameRef(helper, g.fset.Position(callback.guard.Pos()).Line) {
		return fmt.Errorf("write_evidence.callback.invoke_at: покажите вызов переданного callback и выход при false")
	}
	literal := callback.literal
	params := paramNames(literal.Type.Params)
	if len(params) != 1 || params[0] != pair.Actual || pair.ActualAt != w.WriteAt ||
		!callbackWritesArgument(g, fn, literal, w) {
		return fmt.Errorf("write_evidence.callback: подтвердите присваивание через переданный указатель и следующий return true")
	}
	use, err := g.nameCallExpression(helper, c.Use.At, c.Use.Expression)
	if err != nil {
		return err
	}
	if c.Use.Declaration != c.Helper || use.Pos() <= callback.guard.End() ||
		!slices.ContainsFunc(use.Args, func(expr ast.Expr) bool { return nameIdentifier(expr) == callback.data }) ||
		!nameValueUnchangedWithin(helper.node.Body, callback.guard, use, callback.data) {
		return fmt.Errorf("write_evidence.callback.use: покажите, кто получает изменённое значение после проверки callback")
	}
	return checkNameWriteWordLinks(proof.Words, map[string]bool{
		c.CallAt: true, c.InvokeAt: true, w.WriteAt: true, c.Use.At: true,
	})
}

func callbackWritesArgument(g *queryGoSource, fn queryFunc, literal *ast.FuncLit, w *WriteEvidence) bool {
	body := literal.Body.List
	params := literal.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 {
		return false
	}
	param := params[0].Names[0]
	for i, stmt := range body[:max(0, len(body)-1)] {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 ||
			w.WriteAt != nameRef(fn, g.fset.Position(assign.Pos()).Line) {
			continue
		}
		target, ok := assign.Lhs[0].(*ast.StarExpr)
		next, returns := body[i+1].(*ast.ReturnStmt)
		if ok && nameIdentifier(target.X) == param.Name && sameNameExpression(target, w.Target) &&
			sameNameExpression(assign.Rhs[0], w.Value) && nameValueUnchangedWithin(literal.Body, param, assign, param.Name) &&
			returns && len(next.Results) == 1 && nameIdentifier(next.Results[0]) == "true" {
			return true
		}
	}
	return false
}

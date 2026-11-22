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
	params := callback.literal.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 {
		return nil
	}
	param := params[0].Names[0]
	var mutation ast.Node
	ast.PreorderStack(callback.literal.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		if mutation != nil {
			return false
		}
		var targets []ast.Expr
		switch s := n.(type) {
		case *ast.AssignStmt:
			targets = s.Lhs
		case *ast.IncDecStmt:
			targets = []ast.Expr{s.X}
		}
		for _, target := range targets {
			if nameCallbackWriteBinding(param, target, n, stack) != nil {
				mutation = n
				return false
			}
		}
		return true
	})
	return mutation
}

func checkNameCallbackWrite(e Evidence, declaration string, source *evidenceSource) error {
	w, pair := e.WriteEvidence, e.ObjectComparison
	proof, c := pair.Proof, w.Callback
	if w.File != nil || len(w.Calls) != 0 || proof == nil || pair.EquivalentAt != "" || pair.EquivalentFlow != nil ||
		proof.Assignment != nil || proof.API != nil || proof.CLI != nil || proof.Reference != nil || proof.SQL != nil || proof.Setting != nil || proof.Effect != nil {
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
	binding := g.callbackWriteBinding(fn, literal, w)
	if binding.source == nil || binding.root.Name != pair.Actual || pair.ActualAt != w.WriteAt {
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
		nameRef(fn, g.fset.Position(binding.source.Pos()).Line): true,
	})
}

type nameCallbackAssignment struct {
	root   *ast.Ident
	source ast.Node
}

func (g *queryGoSource) callbackWriteBinding(fn queryFunc, literal *ast.FuncLit, w *WriteEvidence) nameCallbackAssignment {
	params := literal.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 {
		return nameCallbackAssignment{}
	}
	param := params[0].Names[0]
	// Deferred or concurrent actions may replace the callback data after this write.
	for node := range ast.Preorder(literal.Body) {
		switch node.(type) {
		case *ast.DeferStmt, *ast.GoStmt:
			return nameCallbackAssignment{}
		}
	}
	var found nameCallbackAssignment
	ast.PreorderStack(literal.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		if _, nested := n.(*ast.FuncLit); nested || found.source != nil {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN || len(assign.Lhs) != len(assign.Rhs) ||
			w.WriteAt != nameRef(fn, g.fset.Position(assign.Pos()).Line) {
			return true
		}
		if !g.nameCallbackReturnsTrue(fn, assign, stack) || !nameValueUnchangedWithin(literal.Body, param, assign, param.Name) {
			return true
		}
		for i, target := range assign.Lhs {
			if !nameCallbackDistinctTargets(assign.Lhs, i) {
				continue
			}
			if sameNameExpression(target, w.Target) && sameNameExpression(assign.Rhs[i], w.Value) {
				if source := nameCallbackWriteBinding(param, target, assign, stack); source != nil {
					found = nameCallbackAssignment{nameWriteRoot(target), source}
					return false
				}
			}
		}
		return true
	})
	return found
}

// Other simultaneous targets must not overwrite or alias the proven field.
func nameCallbackDistinctTargets(targets []ast.Expr, selected int) bool {
	if len(targets) == 1 {
		return true
	}
	field, ok := targets[selected].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	root, ok := field.X.(*ast.Ident)
	if !ok || root.Obj == nil {
		return false
	}
	for i, target := range targets {
		if i == selected {
			continue
		}
		other, ok := target.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := other.X.(*ast.Ident)
		if !ok || id.Obj != root.Obj || other.Sel.Name == field.Sel.Name {
			return false
		}
	}
	return true
}

// Bind writes through a local address of a slice element to the actual callback argument.
func nameCallbackWriteBinding(param *ast.Ident, target ast.Expr, write ast.Node, stack []ast.Node) ast.Node {
	root := nameWriteRoot(target)
	if root == nil || root.Obj == nil || target == root {
		return nil
	}
	if root.Obj == param.Obj {
		return param
	}
	if _, field := target.(*ast.SelectorExpr); !field {
		return nil
	}
	alias, ok := root.Obj.Decl.(*ast.AssignStmt)
	if !ok || alias.Tok != token.DEFINE || len(alias.Lhs) != 1 || len(alias.Rhs) != 1 {
		return nil
	}
	address, ok := alias.Rhs[0].(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return nil
	}
	element, ok := address.X.(*ast.IndexExpr)
	if !ok {
		return nil
	}
	slice, ok := ast.Unparen(element.X).(*ast.StarExpr)
	if !ok {
		return nil
	}
	id, ok := ast.Unparen(slice.X).(*ast.Ident)
	if !ok || id.Obj != param.Obj {
		return nil
	}
	for _, parent := range stack {
		block, ok := parent.(*ast.BlockStmt)
		if ok && slices.Contains(block.List, ast.Stmt(alias)) &&
			nameValueUnchangedWithin(block, alias, write, root.Name) &&
			nameCallbackKeepsSlice(block, alias, write, param) {
			return alias
		}
	}
	return nil
}

func nameCallbackKeepsSlice(block *ast.BlockStmt, alias, write ast.Node, param *ast.Ident) bool {
	for n := range ast.Preorder(block) {
		if n.Pos() <= alias.End() || n.Pos() >= write.Pos() {
			continue
		}
		if id, ok := n.(*ast.Ident); ok && id.Obj == param.Obj {
			return false
		}
	}
	return true
}

func (g *queryGoSource) nameCallbackReturnsTrue(fn queryFunc, assign *ast.AssignStmt, stack []ast.Node) bool {
	if g.nameInUnreachableBranch(fn, stack, assign) {
		return false
	}
	block, ok := stack[len(stack)-1].(*ast.BlockStmt)
	if !ok {
		return false
	}
	var stmt ast.Stmt = assign
	if len(block.List) == 1 && len(stack) >= 3 {
		branch, branchOK := stack[len(stack)-2].(*ast.IfStmt)
		parent, blockOK := stack[len(stack)-3].(*ast.BlockStmt)
		if branchOK && blockOK && branch.Body == block && branch.Init == nil && branch.Else == nil {
			stmt, block = branch, parent
		}
	}
	i := slices.Index(block.List, stmt)
	if i < 0 || i+1 == len(block.List) {
		return false
	}
	ret, ok := block.List[i+1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	return ok && id.Name == "true" && id.Obj == nil && g.nameUniverseIdentifier(fn, id.Name)
}

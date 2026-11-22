package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

func (g *queryGoSource) checkNameFlock(e Evidence, fn queryFunc, call *ast.CallExpr) error {
	pair, write := e.ObjectComparison, e.WriteEvidence
	effect := pair.Proof.Effect
	if !g.namePackageSelector(fn, call.Fun, "syscall", "Flock") || len(call.Args) != 2 ||
		!sameNameExpression(call.Args[0], write.Target) || !sameNameExpression(call.Args[1], write.Value) ||
		!g.nameFlockFlags(fn, call.Args[1], "syscall", []string{"LOCK_EX"}, []string{"LOCK_NB"}) {
		return fmt.Errorf("proof.effect: укажите syscall.Flock с точными аргументами дескриптора и режима блокировки")
	}
	fd, wrapped := g.nameFlockDescriptor(fn, call.Args[0])
	if fd == nil || fd.Obj == nil || fd.Name != pair.Actual || !g.nameFlockReachable(fn, call) {
		return fmt.Errorf("proof.effect: нужен достижимый syscall.Flock с именем дескриптора в actual")
	}
	binding, ok := fd.Obj.Decl.(*ast.AssignStmt)
	if !ok || binding.Tok != token.DEFINE || len(binding.Lhs) != 2 || len(binding.Rhs) != 1 ||
		nameIdentifier(binding.Lhs[0]) != fd.Name || !slices.Contains(fn.node.Body.List, ast.Stmt(binding)) ||
		nameRef(fn, g.fset.Position(binding.Pos()).Line) != effect.OpenAt || binding.End() >= call.Pos() ||
		!g.nameFlockReachable(fn, binding) {
		return fmt.Errorf("proof.effect.open_at: укажите создание того же локального дескриптора перед Flock")
	}
	open, ok := binding.Rhs[0].(*ast.CallExpr)
	if !ok || len(open.Args) != 3 || effect.Path == "" || !sameNameExpression(open.Args[0], effect.Path) {
		return fmt.Errorf("proof.effect.path: нужен фактический путь открытия дескриптора")
	}
	pkg, method, create := "syscall", "Open", "O_CREAT"
	if wrapped {
		pkg, method, create = "os", "OpenFile", "O_CREATE"
	}
	if !g.namePackageSelector(fn, open.Fun, pkg, method) ||
		!g.nameFlockFlags(fn, open.Args[1], pkg, []string{create, "O_RDWR"}, []string{"O_NOFOLLOW", "O_CLOEXEC"}) ||
		!g.nameFlockStable(fn, binding, call, fd, wrapped) {
		return fmt.Errorf("proof.effect: путь должен открываться для этого дескриптора до его использования")
	}
	if !g.nameFlockChecksOpen(fn, binding, call) {
		return fmt.Errorf("proof.effect: до Flock нужно вернуть управление при ошибке открытия дескриптора")
	}
	if e.Result == "checked" && !slices.EqualFunc(nameProofObjects(pair), []string{"lock"}, strings.EqualFold) &&
		len(nameProofObjects(pair)) != 0 {
		return fmt.Errorf("proof.effect: операция подтверждает файловый замок")
	}
	return checkNameWriteWordLinks(pair.Proof.Words, nameWriteLines(e, map[string]bool{write.WriteAt: true, effect.OpenAt: true}))
}

func (g *queryGoSource) nameFlockChecksOpen(fn queryFunc, binding *ast.AssignStmt, flock *ast.CallExpr) bool {
	errID, ok := binding.Lhs[1].(*ast.Ident)
	if !ok || errID.Obj == nil {
		return false
	}
	stmts := fn.node.Body.List
	next := slices.Index(stmts, ast.Stmt(binding)) + 1
	if next >= len(stmts) {
		return false
	}
	guard, ok := stmts[next].(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || guard.End() >= flock.Pos() || len(guard.Body.List) != 1 {
		return false
	}
	if _, ok := guard.Body.List[0].(*ast.ReturnStmt); !ok {
		return false
	}
	cond, ok := ast.Unparen(guard.Cond).(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return false
	}
	left, leftOK := ast.Unparen(cond.X).(*ast.Ident)
	right, rightOK := ast.Unparen(cond.Y).(*ast.Ident)
	if !leftOK || !rightOK {
		return false
	}
	if right.Obj == errID.Obj {
		left, right = right, left
	}
	return left.Obj == errID.Obj && right.Obj == nil && right.Name == "nil" && g.nameUniverseIdentifier(fn, "nil")
}

func (g *queryGoSource) nameFlockDescriptor(fn queryFunc, expr ast.Expr) (*ast.Ident, bool) {
	if id, ok := ast.Unparen(expr).(*ast.Ident); ok {
		return id, false
	}
	converted, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(converted.Args) != 1 || !g.nameFlockConversion(fn, converted, "int") {
		return nil, false
	}
	call, ok := ast.Unparen(converted.Args[0]).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Fd" {
		return nil, false
	}
	id, _ := sel.X.(*ast.Ident)
	return id, true
}

func (g *queryGoSource) nameFlockConversion(fn queryFunc, call *ast.CallExpr, name string) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == name && id.Obj == nil && g.nameUniverseIdentifier(fn, name)
}

func (g *queryGoSource) nameFlockFlags(fn queryFunc, expr ast.Expr, pkg string, required, optional []string) bool {
	found := make(map[string]bool)
	var collect func(ast.Expr) bool
	collect = func(expr ast.Expr) bool {
		expr = ast.Unparen(expr)
		if pair, ok := expr.(*ast.BinaryExpr); ok && pair.Op == token.OR {
			return collect(pair.X) && collect(pair.Y)
		}
		name := calledName(expr)
		if found[name] || (!slices.Contains(required, name) && !slices.Contains(optional, name)) ||
			!g.namePackageSelector(fn, expr, pkg, name) {
			return false
		}
		found[name] = true
		return true
	}
	return collect(expr) && !slices.ContainsFunc(required, func(name string) bool { return !found[name] })
}

func (g *queryGoSource) nameFlockReachable(fn queryFunc, target ast.Node) bool {
	reaches := false
	ast.PreorderStack(fn.node.Body, nil, func(node ast.Node, parents []ast.Node) bool {
		if node.Pos() > target.Pos() || node.End() < target.End() {
			return false
		}
		if node != target {
			return true
		}
		for _, parent := range parents {
			switch n := parent.(type) {
			case *ast.BlockStmt:
				if !g.nameNestedFieldPrefix(fn, n, target) {
					return false
				}
			case *ast.IfStmt:
				inThen := n.Body.Pos() <= target.Pos() && target.End() <= n.Body.End()
				inElse := n.Else != nil && n.Else.Pos() <= target.Pos() && target.End() <= n.Else.End()
				if b, known := g.nameNestedFieldCondition(fn, n.Cond); known && (inThen || inElse) && b != inThen {
					return false
				}
			case *ast.ForStmt:
				if n.Init != nil || n.Cond != nil || n.Post != nil {
					return false
				}
			case *ast.AssignStmt, *ast.ExprStmt, *ast.ReturnStmt:
			default:
				return false
			}
		}
		reaches = true
		return false
	})
	return reaches
}

func (g *queryGoSource) nameFlockStable(fn queryFunc, from, to ast.Node, fd *ast.Ident, wrapped bool) bool {
	var wrapper *ast.Object
	stable := true
	ast.PreorderStack(fn.node.Body, nil, func(node ast.Node, parents []ast.Node) bool {
		if !stable || node.Pos() >= to.Pos() {
			return false
		}
		id, ok := node.(*ast.Ident)
		if !ok || node.Pos() <= from.End() || id.Obj == nil {
			return true
		}
		if id.Obj == wrapper {
			stable = false
			return false
		}
		if id.Obj != fd.Obj {
			return true
		}
		if !wrapped && wrapper == nil && len(parents) >= 3 {
			cast, castOK := parents[len(parents)-1].(*ast.CallExpr)
			newFile, newOK := parents[len(parents)-2].(*ast.CallExpr)
			assign, assignOK := parents[len(parents)-3].(*ast.AssignStmt)
			if castOK && len(cast.Args) == 1 && cast.Args[0] == id && g.nameFlockConversion(fn, cast, "uintptr") &&
				newOK && len(newFile.Args) == 2 && newFile.Args[0] == cast && g.namePackageSelector(fn, newFile.Fun, "os", "NewFile") &&
				assignOK && len(assign.Rhs) == 1 && assign.Rhs[0] == newFile && len(assign.Lhs) == 1 && assign.Tok == token.DEFINE {
				if name, ok := assign.Lhs[0].(*ast.Ident); ok && name.Obj != nil {
					wrapper = name.Obj
					return true
				}
			}
		}
		stable = false
		return false
	})
	return stable
}

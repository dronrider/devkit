package reviewaudit

import (
	"go/ast"
	"go/token"
	"slices"
)

// A nested assignment may read a field from the same local instance. Limit
// the path to blocks, conditional branches and plain loops.
func (g *queryGoSource) nameNestedLocalFieldRead(writer, reader queryFunc, field, read *ast.SelectorExpr) bool {
	if writer.node.Body != reader.node.Body {
		return false
	}
	source, ok := field.X.(*ast.Ident)
	destination, reading := read.X.(*ast.Ident)
	if !ok || !reading || source.Obj == nil || source.Obj != destination.Obj {
		return false
	}
	body := reader.node.Body
	write, ok := nameFieldStatement(body, field).(*ast.AssignStmt)
	if !ok || write.End() >= read.Pos() || !g.nameNestedFieldPrefix(reader, body, write) {
		return false
	}
	switch nameFieldStatement(body, read).(type) {
	case *ast.IfStmt, *ast.ForStmt:
	default:
		return false
	}
	end := g.nameNestedFieldReadEnd(reader, read)
	fields := nameExplicitStructFields(source.Obj)
	return end.IsValid() && fields != nil && nameNestedFieldBindingStable(body, write, read, end, source.Obj, fields)
}

func (g *queryGoSource) nameNestedFieldReadEnd(fn queryFunc, read *ast.SelectorExpr) token.Pos {
	var end token.Pos
	ast.PreorderStack(fn.node.Body, nil, func(node ast.Node, parents []ast.Node) bool {
		if node.Pos() > read.Pos() || node.End() < read.End() {
			return false
		}
		if node != read {
			return true
		}
		assignment, ok := parents[len(parents)-1].(*ast.AssignStmt)
		if !ok || assignment.Tok != token.DEFINE || !slices.Contains(assignment.Rhs, ast.Expr(read)) ||
			g.nameInUnreachableBranch(fn, parents, read) {
			return false
		}
		through := read.Pos()
		for _, parent := range parents[:len(parents)-1] {
			switch parent := parent.(type) {
			case *ast.BlockStmt:
				if !g.nameNestedFieldPrefix(fn, parent, read) {
					return false
				}
			case *ast.IfStmt:
				if parent.Init != nil {
					return false
				}
				inThen := parent.Body.Pos() <= read.Pos() && read.End() <= parent.Body.End()
				if condition, known := g.nameNestedFieldCondition(fn, parent.Cond); known && condition != inThen {
					return false
				}
			case *ast.ForStmt:
				if parent.Init != nil || parent.Cond != nil || parent.Post != nil {
					return false
				}
				through = max(through, parent.End())
			default:
				return false
			}
		}
		end = through
		return false
	})
	return end
}

func (g *queryGoSource) nameNestedFieldPrefix(fn queryFunc, body *ast.BlockStmt, target ast.Node) bool {
	for _, stmt := range body.List {
		if stmt.Pos() <= target.Pos() && target.End() <= stmt.End() {
			return true
		}
		if g.nameNestedFieldExit(fn, stmt) {
			return false
		}
	}
	return false
}

func (g *queryGoSource) nameNestedFieldCondition(fn queryFunc, condition ast.Expr) (bool, bool) {
	id, ok := condition.(*ast.Ident)
	if ok && id.Obj != nil && id.Obj.Kind == ast.Con {
		if decl, ok := id.Obj.Decl.(*ast.ValueSpec); ok && len(decl.Names) == 1 && len(decl.Values) == 1 {
			return g.nameConstantCondition(fn, decl.Values[0])
		}
	}
	return g.nameConstantCondition(fn, condition)
}

func (g *queryGoSource) nameNestedFieldExit(fn queryFunc, stmt ast.Stmt) bool {
	switch node := stmt.(type) {
	case *ast.ReturnStmt, *ast.BranchStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.LabeledStmt:
		return true
	case *ast.BlockStmt:
		return slices.ContainsFunc(node.List, func(child ast.Stmt) bool { return g.nameNestedFieldExit(fn, child) })
	case *ast.IfStmt:
		if condition, known := g.nameNestedFieldCondition(fn, node.Cond); known {
			if condition {
				return g.nameNestedFieldExit(fn, node.Body)
			}
			return g.nameNestedFieldExit(fn, node.Else)
		}
		return node.Else != nil && g.nameNestedFieldExit(fn, node.Body) && g.nameNestedFieldExit(fn, node.Else)
	case *ast.ExprStmt:
		if call, ok := node.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" && id.Obj == nil {
				return g.nameUniverseIdentifier(fn, id.Name)
			}
		}
	}
	return false
}

// Check from the binding's declaration to catch an alias created before the
// write. Include the whole loop when later iterations can reach the same read.
func nameNestedFieldBindingStable(body *ast.BlockStmt, write *ast.AssignStmt, read *ast.SelectorExpr, end token.Pos, binding *ast.Object, fields *ast.FieldList) bool {
	valid := true
	writesField := func(expr ast.Expr) bool {
		return slices.ContainsFunc(slices.Collect(ast.Preorder(expr)), func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != read.Sel.Name {
				return false
			}
			return true
		})
	}
	ast.PreorderStack(body, nil, func(node ast.Node, parents []ast.Node) bool {
		if !valid || node.Pos() >= end {
			return false
		}
		switch node := node.(type) {
		case *ast.Ident:
			if node.Obj == nil {
				return true
			}
			candidate := fields
			if node.Obj != binding {
				candidate = nameExplicitStructFields(node.Obj)
				if candidate == nil || nameNamedStructField(candidate, read.Sel.Name) == nil {
					return true
				}
			}
			parent := parents[len(parents)-1]
			if sel, field := parent.(*ast.SelectorExpr); field && sel.X == node {
				valid = nameNamedStructField(candidate, sel.Sel.Name) != nil
			} else {
				valid = parent == binding.Decl
			}
		case *ast.AssignStmt:
			if node != write && node.Pos() > write.Pos() {
				valid = !slices.ContainsFunc(node.Lhs, writesField)
			}
		case *ast.IncDecStmt:
			if node.Pos() > write.Pos() {
				valid = !writesField(node.X)
			}
		case *ast.UnaryExpr:
			if node.Op == token.AND {
				valid = !writesField(node.X)
			}
		case *ast.ReturnStmt, *ast.BranchStmt:
			if jump, ok := node.(*ast.BranchStmt); ok && jump.Tok == token.GOTO {
				valid = false
			}
			if valid && node.Pos() > write.Pos() && node.Pos() < read.Pos() {
				if block, ok := parents[len(parents)-1].(*ast.BlockStmt); ok {
					valid = block.End() < read.Pos()
				}
			}
		}
		return valid
	})
	return valid
}

// A selector on the binding must denote a direct field. Passing a method
// value around can otherwise hide a mutation of the receiver.
func nameExplicitStructFields(binding *ast.Object) *ast.FieldList {
	var typ ast.Expr
	switch declaration := binding.Decl.(type) {
	case *ast.Field:
		typ = declaration.Type
	case *ast.ValueSpec:
		typ = declaration.Type
	default:
		return nil
	}
	seen := make(map[*ast.TypeSpec]bool)
	pointer := false
	for typ != nil {
		switch expr := ast.Unparen(typ).(type) {
		case *ast.StarExpr:
			if pointer {
				return nil
			}
			typ, pointer = expr.X, true
		case *ast.Ident:
			if expr.Obj == nil {
				return nil
			}
			declaration, ok := expr.Obj.Decl.(*ast.TypeSpec)
			if !ok || seen[declaration] {
				return nil
			}
			seen[declaration] = true
			typ = declaration.Type
		case *ast.StructType:
			return expr.Fields
		default:
			return nil
		}
	}
	return nil
}

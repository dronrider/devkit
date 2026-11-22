package reviewaudit

import (
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strconv"
)

func (g *queryGoSource) namePointerFieldUse(writer, reader queryFunc, write *ast.AssignStmt, field, read *ast.SelectorExpr, callAt, fieldAt string) bool {
	parameter, ok := field.X.(*ast.Ident)
	local, localOK := read.X.(*ast.Ident)
	if !ok || !localOK || parameter.Obj == nil || local.Obj == nil || nameMethodReceiver(writer) == nil ||
		writer.node.Body == reader.node.Body || write.Tok != token.ASSIGN {
		return false
	}
	fields := g.namePointerFieldDeclaration(writer, parameter, field.Sel.Name, fieldAt)
	if fields == nil || !nameDistinctDirectFields(write, parameter.Obj, fields) {
		return false
	}
	params := paramNames(writer.node.Type.Params)
	slot := slices.Index(params, parameter.Name)
	if slot < 0 || !namePointerFieldSafe(writer.node.Body, parameter.Obj, fields, field.Sel.Name, nil, write, writer.node.Body.End()) {
		return false
	}
	statements := writer.node.Body.List
	if len(statements) < 2 || statements[len(statements)-2] != write {
		return false
	}
	result, ok := statements[len(statements)-1].(*ast.ReturnStmt)
	if !ok || len(result.Results) != 2 || !g.namePointerBuiltin(writer, result.Results[0], "true") ||
		!g.namePointerBuiltin(writer, result.Results[1], "nil") || !g.namePointerUniqueSuccess(writer, result) {
		return false
	}
	receiver := nameMethodReceiver(reader)
	if receiver == nil || recvType(writer.node) != recvType(reader.node) || !g.nameSameGoPackage(writer, reader.file) {
		return false
	}
	call, _, err := g.call(reader, callAt, func(expr ast.Expr) bool {
		method, ok := expr.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != writer.node.Name.Name {
			return false
		}
		id, ok := method.X.(*ast.Ident)
		return ok && id.Obj == receiver
	})
	if err != nil || slot >= len(call.Args) || len(params) != len(call.Args) {
		return false
	}
	address, ok := call.Args[slot].(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}
	argument, ok := address.X.(*ast.Ident)
	if !ok || argument.Obj != local.Obj {
		return false
	}
	assignment, ok := nameFieldStatement(reader.node.Body, call).(*ast.AssignStmt)
	decl, declared := local.Obj.Decl.(*ast.AssignStmt)
	if !ok || len(assignment.Rhs) != 1 || assignment.Rhs[0] != call || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 ||
		!declared || decl.Tok != token.DEFINE || !slices.Contains(reader.node.Body.List, ast.Stmt(decl)) || decl.End() >= call.Pos() || call.End() >= read.Pos() ||
		!g.namePointerPath(reader, reader.node.Body, assignment, nil) ||
		!namePointerFieldSafe(reader.node.Body, local.Obj, fields, field.Sel.Name, address, nil, read.End()) {
		return false
	}
	var initialGuard func(ast.Expr) (bool, bool)
	if g.namePointerBuiltin(writer, nameNamedStructField(fields, field.Sel.Name).Type, "string") {
		initial, known := nameLocalStringField(decl, local.Obj, field.Sel.Name)
		if known {
			initialGuard = func(condition ast.Expr) (bool, bool) {
				return nameInitialStringCondition(condition, parameter.Obj, field.Sel.Name, initial)
			}
		}
	}
	if !g.namePointerPath(writer, writer.node.Body, write, initialGuard) {
		return false
	}
	flag, flagOK := assignment.Lhs[0].(*ast.Ident)
	failure, failureOK := assignment.Lhs[1].(*ast.Ident)
	if !flagOK || !failureOK || flag.Obj == nil || failure.Obj == nil || flag.Obj.Decl != assignment || failure.Obj.Decl != assignment {
		return false
	}
	known := func(condition ast.Expr) (bool, bool) {
		knownFailure := failure.Obj
		if !namePointerResultStable(reader.node.Body, assignment, condition, failure.Obj) {
			knownFailure = nil
		}
		return g.namePointerCondition(reader, condition, flag.Obj, true, knownFailure)
	}
	readStatement := namePointerReadStatement(reader.node.Body, read)
	failed := func(condition ast.Expr) (bool, bool) {
		return g.namePointerCondition(reader, condition, flag.Obj, false, nil)
	}
	return readStatement != nil && namePointerResultStable(reader.node.Body, assignment, readStatement, flag.Obj) &&
		g.namePointerPath(reader, reader.node.Body, readStatement, known) &&
		!g.namePointerPath(reader, reader.node.Body, readStatement, failed)
}

// Go assigns multiple targets in order; a later alias can overwrite the
// observed field before the statement completes.
func nameDistinctDirectFields(write *ast.AssignStmt, receiver *ast.Object, fields *ast.FieldList) bool {
	seen := make(map[string]bool, len(write.Lhs))
	for _, target := range write.Lhs {
		field, ok := ast.Unparen(target).(*ast.SelectorExpr)
		if !ok || !nameUsesObject(field.X, receiver) || seen[field.Sel.Name] || nameNamedStructField(fields, field.Sel.Name) == nil {
			return false
		}
		seen[field.Sel.Name] = true
	}
	return true
}

func nameNamedStructField(fields *ast.FieldList, name string) *ast.Field {
	for _, field := range fields.List {
		if slices.ContainsFunc(field.Names, func(id *ast.Ident) bool { return id.Name == name }) {
			return field
		}
	}
	return nil
}

func nameLocalStringField(declaration *ast.AssignStmt, binding *ast.Object, field string) (string, bool) {
	slot := slices.IndexFunc(declaration.Lhs, func(expr ast.Expr) bool { return nameUsesObject(expr, binding) })
	if len(declaration.Lhs) != len(declaration.Rhs) || slot < 0 {
		return "", false
	}
	literal, ok := ast.Unparen(declaration.Rhs[slot]).(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	for _, value := range literal.Elts {
		entry, ok := value.(*ast.KeyValueExpr)
		if !ok {
			return "", false
		}
		if nameIdentifier(entry.Key) == field {
			return nameLiteralString(entry.Value)
		}
	}
	return "", true
}

func nameInitialStringCondition(condition ast.Expr, receiver *ast.Object, field, initial string) (bool, bool) {
	comparison, ok := ast.Unparen(condition).(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL && comparison.Op != token.NEQ {
		return false, false
	}
	left, right := comparison.X, comparison.Y
	matches := func(expr ast.Expr) bool {
		selected, ok := ast.Unparen(expr).(*ast.SelectorExpr)
		return ok && nameUsesObject(selected.X, receiver) && selected.Sel.Name == field
	}
	if !matches(left) {
		left, right = right, left
	}
	if !matches(left) {
		return false, false
	}
	value, ok := nameLiteralString(right)
	return (initial == value) == (comparison.Op == token.EQL), ok
}

func nameLiteralString(expr ast.Expr) (string, bool) {
	literal, ok := ast.Unparen(expr).(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

// A true result must come through the write immediately above it. Otherwise
// the caller could read its original field after an earlier successful exit.
func (g *queryGoSource) namePointerUniqueSuccess(fn queryFunc, success *ast.ReturnStmt) bool {
	if fn.node.Type.Results == nil {
		return false
	}
	results := fn.node.Type.Results.List
	if len(results) != 2 || len(results[0].Names) != 0 || len(results[1].Names) != 0 ||
		!g.namePointerBuiltin(fn, results[0].Type, "bool") || !g.namePointerBuiltin(fn, results[1].Type, "error") {
		return false
	}
	valid := true
	ast.Inspect(fn.node.Body, func(node ast.Node) bool {
		if _, closure := node.(*ast.FuncLit); closure {
			return false
		}
		if result, ok := node.(*ast.ReturnStmt); ok && result != success {
			valid = valid && len(result.Results) == 2 && g.namePointerBuiltin(fn, result.Results[0], "false")
		}
		return valid
	})
	return valid
}

func nameMethodReceiver(fn queryFunc) *ast.Object {
	if fn.node.Recv == nil || len(fn.node.Recv.List) != 1 || len(fn.node.Recv.List[0].Names) != 1 {
		return nil
	}
	return fn.node.Recv.List[0].Names[0].Obj
}

func (g *queryGoSource) nameSameGoPackage(fn queryFunc, file string) bool {
	if path.Dir(fn.file) != path.Dir(file) {
		return false
	}
	left, err := g.parseFile(fn.file, fn.base)
	if err != nil {
		return false
	}
	right, err := g.parseFile(file, fn.base)
	return err == nil && left.Name.Name == right.Name.Name
}

func (g *queryGoSource) namePointerFieldDeclaration(fn queryFunc, parameter *ast.Ident, field, at string) *ast.FieldList {
	declaration, ok := parameter.Obj.Decl.(*ast.Field)
	if !ok {
		return nil
	}
	pointer, ok := declaration.Type.(*ast.StarExpr)
	if !ok {
		return nil
	}
	typ, ok := pointer.X.(*ast.Ident)
	file, line, base, err := evidenceLocation(at)
	if !ok || err != nil || base != fn.base || !g.nameSameGoPackage(fn, file) {
		return nil
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return nil
	}
	obj := tree.Scope.Lookup(typ.Name)
	if obj == nil || (typ.Obj != nil && typ.Obj != obj) {
		return nil
	}
	spec, ok := obj.Decl.(*ast.TypeSpec)
	if !ok || spec.Assign.IsValid() {
		return nil
	}
	structure, ok := spec.Type.(*ast.StructType)
	if !ok {
		return nil
	}
	for _, entry := range structure.Fields.List {
		for _, name := range entry.Names {
			if name.Name == field && g.fset.Position(name.Pos()).Line == line {
				return structure.Fields
			}
		}
	}
	return nil
}

// The address can reach only the verified call. Other uses must select direct
// fields without taking their addresses or changing the field under review.
func namePointerFieldSafe(body *ast.BlockStmt, object *ast.Object, fields *ast.FieldList, field string, address *ast.UnaryExpr, write *ast.AssignStmt, through token.Pos) bool {
	valid := true
	ast.PreorderStack(body, nil, func(node ast.Node, parents []ast.Node) bool {
		if !valid || node.Pos() >= through {
			return false
		}
		id, ok := node.(*ast.Ident)
		if !ok || id.Obj != object {
			return true
		}
		parent := parents[len(parents)-1]
		if parent == object.Decl || parent == address {
			return true
		}
		sel, selected := parent.(*ast.SelectorExpr)
		if !selected || sel.X != id || nameNamedStructField(fields, sel.Sel.Name) == nil {
			valid = false
			return false
		}
		for _, use := range parents[:len(parents)-1] {
			switch use := use.(type) {
			case *ast.UnaryExpr:
				if use.Op == token.AND {
					valid = false
				}
			case *ast.AssignStmt:
				if use != write && sel.Sel.Name == field && slices.ContainsFunc(use.Lhs, func(lhs ast.Expr) bool { return nameNodeContains(lhs, sel) }) {
					valid = false
				}
			case *ast.IncDecStmt, *ast.RangeStmt:
				if sel.Sel.Name == field {
					valid = false
				}
			}
		}
		return valid
	})
	return valid
}

func nameNodeContains(parent, child ast.Node) bool {
	return parent != nil && parent.Pos() <= child.Pos() && child.End() <= parent.End()
}

// Descend only through blocks and branches whose conditions permit this path.
func (g *queryGoSource) namePointerPath(fn queryFunc, body *ast.BlockStmt, target ast.Stmt, known func(ast.Expr) (bool, bool)) bool {
	for _, stmt := range body.List {
		if stmt == target {
			return true
		}
		if !nameNodeContains(stmt, target) {
			if g.namePointerStops(fn, stmt, known) {
				return false
			}
			continue
		}
		switch branch := stmt.(type) {
		case *ast.BlockStmt:
			return g.namePointerPath(fn, branch, target, known)
		case *ast.IfStmt:
			if branch.Init != nil && g.namePointerStops(fn, branch.Init, known) {
				return false
			}
			then := nameNodeContains(branch.Body, target)
			if value, certain := g.namePointerBranch(fn, branch.Cond, known); certain && value != then {
				return false
			}
			if then {
				return g.namePointerPath(fn, branch.Body, target, known)
			}
			other, ok := branch.Else.(*ast.BlockStmt)
			return ok && g.namePointerPath(fn, other, target, known)
		default:
			return false
		}
	}
	return false
}

func (g *queryGoSource) namePointerStops(fn queryFunc, stmt ast.Stmt, known func(ast.Expr) (bool, bool)) bool {
	switch stmt := stmt.(type) {
	case *ast.BlockStmt:
		return slices.ContainsFunc(stmt.List, func(child ast.Stmt) bool { return g.namePointerStops(fn, child, known) })
	case *ast.IfStmt:
		if stmt.Init != nil && g.namePointerStops(fn, stmt.Init, known) {
			return true
		}
		if answer, certain := g.namePointerBranch(fn, stmt.Cond, known); certain {
			if answer {
				return g.namePointerStops(fn, stmt.Body, known)
			}
			return stmt.Else != nil && g.namePointerStops(fn, stmt.Else, known)
		}
		return stmt.Else != nil && g.namePointerStops(fn, stmt.Body, known) && g.namePointerStops(fn, stmt.Else, known)
	case *ast.SwitchStmt:
		// Only a switch with a dynamic tag and at least one empty case can
		// precede a proof site. An empty case exits the switch directly.
		if stmt.Init != nil || stmt.Tag == nil {
			return true
		}
		if _, ok := stmt.Tag.(*ast.SelectorExpr); !ok {
			return true
		}
		return !slices.ContainsFunc(stmt.Body.List, func(branch ast.Stmt) bool {
			clause := branch.(*ast.CaseClause)
			return len(clause.Body) == 0
		})
	default:
		return g.nameNestedFieldExit(fn, stmt)
	}
}

func (g *queryGoSource) namePointerBranch(fn queryFunc, expr ast.Expr, known func(ast.Expr) (bool, bool)) (bool, bool) {
	if known != nil {
		if answer, certain := known(expr); certain {
			return answer, true
		}
	}
	return g.nameNestedFieldCondition(fn, expr)
}

func (g *queryGoSource) namePointerBuiltin(fn queryFunc, expr ast.Expr, name string) bool {
	id, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && id.Obj == nil && id.Name == name && g.nameUniverseIdentifier(fn, name)
}

func (g *queryGoSource) namePointerCondition(fn queryFunc, expr ast.Expr, flag *ast.Object, flagValue bool, failure *ast.Object) (bool, bool) {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		return flagValue, expr.Obj == flag
	case *ast.UnaryExpr:
		if expr.Op == token.NOT {
			answer, certain := g.namePointerBranch(fn, expr.X, func(expr ast.Expr) (bool, bool) {
				return g.namePointerCondition(fn, expr, flag, flagValue, failure)
			})
			return !answer, certain
		}
	case *ast.BinaryExpr:
		if lhs, ok := ast.Unparen(expr.X).(*ast.Ident); ok && failure != nil && lhs.Obj == failure && g.namePointerBuiltin(fn, expr.Y, "nil") {
			return expr.Op == token.EQL, expr.Op == token.EQL || expr.Op == token.NEQ
		}
		if expr.Op == token.LAND || expr.Op == token.LOR {
			decisive := expr.Op == token.LOR
			known := func(expr ast.Expr) (bool, bool) { return g.namePointerCondition(fn, expr, flag, flagValue, failure) }
			a, aKnown := g.namePointerBranch(fn, expr.X, known)
			b, bKnown := g.namePointerBranch(fn, expr.Y, known)
			if (aKnown && a == decisive) || (bKnown && b == decisive) {
				return decisive, true
			}
			return !decisive, aKnown && bKnown
		}
	}
	return false, false
}

func namePointerReadStatement(body *ast.BlockStmt, read *ast.SelectorExpr) ast.Stmt {
	var match ast.Stmt
	ast.PreorderStack(body, nil, func(node ast.Node, parents []ast.Node) bool {
		if !nameNodeContains(node, read) {
			return false
		}
		if node != read {
			return true
		}
		for index := len(parents) - 1; index >= 0; index-- {
			switch parent := parents[index].(type) {
			case *ast.ReturnStmt, *ast.ExprStmt, *ast.AssignStmt:
				match = parent.(ast.Stmt)
				return false
			case *ast.CallExpr, *ast.ParenExpr:
			case *ast.BinaryExpr:
				if parent.Op != token.LAND && parent.Op != token.LOR {
					continue
				}
				return false
			default:
				return false
			}
		}
		return false
	})
	return match
}

func namePointerResultStable(body *ast.BlockStmt, call, read ast.Node, result *ast.Object) bool {
	for node := range ast.Preorder(body) {
		if node.Pos() >= read.Pos() || node.Pos() < call.End() {
			continue
		}
		var targets []ast.Expr
		switch node := node.(type) {
		case *ast.AssignStmt:
			targets = node.Lhs
		case *ast.UnaryExpr:
			if node.Op == token.AND {
				targets = []ast.Expr{node.X}
			}
		case *ast.IncDecStmt:
			targets = []ast.Expr{node.X}
		case *ast.RangeStmt:
			targets = []ast.Expr{node.Key, node.Value}
		case *ast.BranchStmt:
			if node.Tok == token.GOTO {
				return false
			}
		}
		for _, target := range targets {
			if id, ok := target.(*ast.Ident); ok && id.Obj == result {
				return false
			}
		}
	}
	return true
}

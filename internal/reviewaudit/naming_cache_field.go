package reviewaudit

import (
	"go/ast"
	"go/token"
	"slices"
)

// A repeated call on the same pointer receiver reads the stored field before
// the refresh path. A claim about a different invocation needs both paths.
func (g *queryGoSource) nameCacheFieldUse(entry, writer, reader queryFunc, write *ast.AssignStmt, field, read *ast.SelectorExpr, value ast.Expr, fieldAt, returnAt string, calls []NameWriteCall) bool {
	receiver := nameMethodReceiver(writer)
	src, sourceOK := field.X.(*ast.Ident)
	dst, readOK := read.X.(*ast.Ident)
	if writer.node != reader.node || receiver == nil || len(writer.node.Type.Params.List) != 0 || !sourceOK || !readOK || src.Obj != receiver || dst.Obj != receiver || write.Tok != token.ASSIGN {
		return false
	}
	fields := g.namePointerFieldDeclaration(writer, src, field.Sel.Name, fieldAt)
	body := writer.node.Body
	if fields == nil || !nameDistinctDirectFields(write, receiver, fields) || !namePointerFieldSafe(body, receiver, fields, field.Sel.Name, nil, write, body.End()) ||
		!g.nameCacheEntry(entry, writer, calls) {
		return false
	}
	load, ok := nameFieldStatement(body, read).(*ast.AssignStmt)
	refresh, refreshOK := nameFieldStatement(body, write).(*ast.IfStmt)
	if !ok || !refreshOK || refresh.Init != nil || refresh.Else != nil || len(refresh.Body.List) != 3 || refresh.Body.List[1] != write ||
		load.Tok != token.DEFINE || len(load.Lhs) != len(load.Rhs) {
		return false
	}
	index := slices.Index(body.List, ast.Stmt(load))
	store := slices.Index(body.List, ast.Stmt(refresh))
	slot := slices.Index(load.Rhs, ast.Expr(read))
	if index < 1 || store <= index+2 || store+2 != len(body.List) || slot < 0 ||
		!g.namePointerPath(writer, body, load, nil) || !g.namePointerPath(writer, body, refresh, nil) {
		return false
	}
	if possible, known := g.nameNestedFieldCondition(writer, refresh.Cond); known && !possible {
		return false
	}
	lock, locked := nameCacheMutexCall(body.List[index-1], receiver, "Lock")
	unlock, unlocked := nameCacheMutexCall(body.List[index+1], receiver, "Unlock")
	writeLock, writeLocked := nameCacheMutexCall(refresh.Body.List[0], receiver, "Lock")
	writeUnlock, writeUnlocked := nameCacheMutexCall(refresh.Body.List[2], receiver, "Unlock")
	if !locked || !unlocked || !writeLocked || !writeUnlocked || lock != unlock || lock != writeLock || lock != writeUnlock ||
		!g.nameCacheMutexField(writer, fields, fieldAt, lock) {
		return false
	}
	cached, ok := load.Lhs[slot].(*ast.Ident)
	hit, hitOK := body.List[index+2].(*ast.IfStmt)
	if !ok || cached.Obj == nil || cached.Obj.Decl != load || !hitOK || hit.Init != nil || hit.Else != nil || len(hit.Body.List) != 1 ||
		!g.nameCacheNilGuard(writer, hit.Cond, cached.Obj) {
		return false
	}
	ret, ok := hit.Body.List[0].(*ast.ReturnStmt)
	returnLine, err := g.nameProofLine(writer, returnAt)
	if !ok || len(ret.Results) != 1 || err != nil || g.fset.Position(ret.Pos()).Line != returnLine {
		return false
	}
	valueRead, ok := ret.Results[0].(*ast.StarExpr)
	if !ok || !nameUsesObject(valueRead.X, cached.Obj) {
		return false
	}
	ref, ok := value.(*ast.UnaryExpr)
	if !ok || ref.Op != token.AND {
		return false
	}
	saved, ok := ref.X.(*ast.Ident)
	fallback, fallbackOK := body.List[store+1].(*ast.ReturnStmt)
	if !ok || saved.Obj == nil || !fallbackOK || len(fallback.Results) != 1 || !nameUsesObject(fallback.Results[0], saved.Obj) {
		return false
	}
	declaration, ok := saved.Obj.Decl.(*ast.AssignStmt)
	return ok && declaration.Pos() > hit.End() && declaration.End() < refresh.Pos() &&
		slices.Contains(body.List, ast.Stmt(declaration)) && namePointerResultStable(body, declaration, refresh, saved.Obj)
}

func (g *queryGoSource) nameCacheEntry(entry, writer queryFunc, calls []NameWriteCall) bool {
	if len(calls) == 0 {
		return entry.node == writer.node
	}
	receiver := nameMethodReceiver(entry)
	if len(calls) != 1 || receiver == nil || recvType(entry.node) != recvType(writer.node) || !g.nameSameGoPackage(writer, entry.file) {
		return false
	}
	call, _, err := g.call(entry, calls[0].At, func(expr ast.Expr) bool {
		method, ok := expr.(*ast.SelectorExpr)
		return ok && method.Sel.Name == writer.node.Name.Name && nameUsesObject(method.X, receiver)
	})
	if err != nil {
		return false
	}
	stmt := nameFieldStatement(entry.node.Body, call)
	loop, ok := stmt.(*ast.RangeStmt)
	if !ok {
		return false
	}
	selection, ok := loop.X.(*ast.SelectorExpr)
	return ok && selection.X == call && g.namePointerPath(entry, entry.node.Body, loop, nil)
}

func nameCacheMutexCall(stmt ast.Stmt, receiver *ast.Object, method string) (string, bool) {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return "", false
	}
	selected, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selected.Sel.Name != method {
		return "", false
	}
	field, ok := selected.X.(*ast.SelectorExpr)
	if !ok || !nameUsesObject(field.X, receiver) {
		return "", false
	}
	return field.Sel.Name, true
}

func (g *queryGoSource) nameCacheMutexField(writer queryFunc, fields *ast.FieldList, fieldAt, mutex string) bool {
	file, _, _, _ := evidenceLocation(fieldAt)
	decl := queryFunc{file: file, base: writer.base}
	return slices.ContainsFunc(fields.List, func(field *ast.Field) bool {
		return g.namePackageSelector(decl, field.Type, "sync", "Mutex") &&
			slices.ContainsFunc(field.Names, func(name *ast.Ident) bool { return name.Name == mutex })
	})
}

func nameUsesObject(expr ast.Expr, object *ast.Object) bool {
	id, ok := ast.Unparen(expr).(*ast.Ident)
	return ok && id.Obj == object
}

func (g *queryGoSource) nameCacheNilGuard(fn queryFunc, condition ast.Expr, bound *ast.Object) bool {
	nonNil := func(expr ast.Expr) bool {
		guard, ok := ast.Unparen(expr).(*ast.BinaryExpr)
		return ok && guard.Op == token.NEQ &&
			(nameUsesObject(guard.X, bound) && g.namePointerBuiltin(fn, guard.Y, "nil") ||
				nameUsesObject(guard.Y, bound) && g.namePointerBuiltin(fn, guard.X, "nil"))
	}
	if nonNil(condition) {
		return true
	}
	guard, ok := ast.Unparen(condition).(*ast.BinaryExpr)
	if !ok || guard.Op != token.LAND || !nonNil(guard.X) {
		return false
	}
	for node := range ast.Preorder(guard.Y) {
		if id, ok := node.(*ast.Ident); ok && id.Obj == bound {
			return false
		}
		if expr, ok := node.(*ast.BinaryExpr); ok && (expr.Op == token.LAND || expr.Op == token.LOR) {
			return false
		}
	}
	answer, known := g.nameNestedFieldCondition(fn, guard.Y)
	return !known || answer
}

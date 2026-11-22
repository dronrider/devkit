package reviewaudit

import (
	"fmt"
	"go/ast"
	"slices"
	"strings"
)

func (g *queryGoSource) checkNameHTTPJSONWrite(e Evidence, entry, helper queryFunc) error {
	pair, w := e.ObjectComparison, e.WriteEvidence
	p := pair.Proof
	if len(w.Calls) != 1 || p.Type != "" || !g.nameSameGoPackage(helper, entry.file) {
		return fmt.Errorf("proof.effect: укажите один вызов местного помощника записи JSON в HTTP-ответ")
	}
	callAt := w.Calls[0].At
	encode, err := g.nameCallExpression(helper, w.WriteAt, p.Expression)
	if err != nil || !g.nameHTTPJSONEncoder(helper, encode, w.Target, w.Value) {
		return fmt.Errorf("proof.effect: покажите запись статуса и JSON тем же http.ResponseWriter")
	}
	send, _, err := g.call(entry, callAt, func(expr ast.Expr) bool { return nameHTTPJSONHelper(expr, helper) })
	if err != nil || pair.ActualAt != callAt {
		return fmt.Errorf("proof.effect: укажите строку вызова помощника с полем ответа как actual_at")
	}
	if !g.nameHTTPJSONSite(entry, send, pair.Actual) {
		return fmt.Errorf("proof.effect: покажите статус и поле %q в ответе на том же http.ResponseWriter", pair.Actual)
	}
	for node := range ast.Preorder(entry.node.Body) {
		call, ok := node.(*ast.CallExpr)
		if ok && call != send && nameHTTPJSONHelper(call.Fun, helper) && !g.nameHTTPJSONSite(entry, call, pair.Actual) {
			return fmt.Errorf("proof.effect: выход %s записывает другой HTTP-ответ", nameRef(entry, g.fset.Position(call.Pos()).Line))
		}
	}
	if objects := nameProofObjects(pair); e.Result == "checked" && len(objects) != 0 && !sameObjectName(objects, identifierWords(pair.Actual)) {
		return fmt.Errorf("proof.effect: поле HTTP-ответа не соответствует объекту в имени")
	}
	return checkNameWriteWordLinks(p.Words, nameWriteLines(e, map[string]bool{w.WriteAt: true, pair.ActualAt: true}))
}

func nameHTTPJSONHelper(expr ast.Expr, helper queryFunc) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == helper.node.Name.Name && (id.Obj == nil || id.Obj.Decl == helper.node)
}

func (g *queryGoSource) nameHTTPJSONEncoder(fn queryFunc, call *ast.CallExpr, target, payload string) bool {
	params := paramNames(fn.node.Type.Params)
	if fn.node.Recv != nil || len(params) != 3 || params[0] != target || params[2] != payload || len(call.Args) != 1 {
		return false
	}
	encode, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || encode.Sel.Name != "Encode" {
		return false
	}
	newEncoder, ok := encode.X.(*ast.CallExpr)
	if !ok || !g.namePackageSelector(fn, newEncoder.Fun, "encoding/json", "NewEncoder") || len(newEncoder.Args) != 1 {
		return false
	}
	w, ok := newEncoder.Args[0].(*ast.Ident)
	if !ok || w.Name != target || !g.nameHTTPParam(fn, w, 0) || !g.nameParamUse(fn, call.Args[0], 2) {
		return false
	}
	body := fn.node.Body.List
	if len(body) < 2 || len(body) > 3 || nameHTTPCallStatement(body[len(body)-1]) != call {
		return false
	}
	status := nameHTTPCallStatement(body[len(body)-2])
	if status == nil || len(status.Args) != 1 || !g.nameParamUse(fn, status.Args[0], 1) {
		return false
	}
	header, ok := status.Fun.(*ast.SelectorExpr)
	if !ok || header.Sel.Name != "WriteHeader" || !nameUsesObject(header.X, w.Obj) {
		return false
	}
	return len(body) == 2 || nameHTTPJSONContentType(body[0], w.Obj)
}

func nameHTTPCallStatement(stmt ast.Stmt) *ast.CallExpr {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return nil
	}
	call, _ := expr.X.(*ast.CallExpr)
	return call
}

func nameHTTPJSONContentType(stmt ast.Stmt, receiver *ast.Object) bool {
	call := nameHTTPCallStatement(stmt)
	if call == nil || len(call.Args) != 2 || nameString(call.Args[0]) != "Content-Type" ||
		!slices.Contains([]string{"application/json", "application/json; charset=utf-8"}, nameString(call.Args[1])) {
		return false
	}
	set, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || set.Sel.Name != "Set" {
		return false
	}
	header, ok := set.X.(*ast.CallExpr)
	if !ok || len(header.Args) != 0 {
		return false
	}
	selected, ok := header.Fun.(*ast.SelectorExpr)
	return ok && selected.Sel.Name == "Header" && nameUsesObject(selected.X, receiver)
}

func (g *queryGoSource) nameHTTPJSONSite(entry queryFunc, call *ast.CallExpr, field string) bool {
	if len(call.Args) != 3 {
		return false
	}
	w, wOK := call.Args[0].(*ast.Ident)
	status, statusOK := call.Args[1].(*ast.SelectorExpr)
	payload, payloadOK := call.Args[2].(*ast.CompositeLit)
	if !wOK || !statusOK || !payloadOK || !g.nameHTTPParam(entry, w, 0) || !strings.HasPrefix(status.Sel.Name, "Status") ||
		!g.namePackageSelector(entry, status, "net/http", status.Sel.Name) || !sameNameExpression(payload.Type, "map[string]string") ||
		!namePointerResultStable(entry.node.Body, w.Obj.Decl.(*ast.Field), call, w.Obj) {
		return false
	}
	if !slices.ContainsFunc(payload.Elts, func(expr ast.Expr) bool {
		item, ok := expr.(*ast.KeyValueExpr)
		return ok && nameString(item.Key) == field
	}) {
		return false
	}
	var statement *ast.ExprStmt
	ast.PreorderStack(entry.node.Body, nil, func(node ast.Node, parents []ast.Node) bool {
		if node == call {
			statement, _ = parents[len(parents)-1].(*ast.ExprStmt)
			return false
		}
		return nameNodeContains(node, call)
	})
	return statement != nil && g.namePointerPath(entry, entry.node.Body, statement, nil)
}

func (g *queryGoSource) nameHTTPParam(fn queryFunc, ref *ast.Ident, slot int) bool {
	if !g.nameParamUse(fn, ref, slot) {
		return false
	}
	field, ok := ref.Obj.Decl.(*ast.Field)
	return ok && g.namePackageSelector(fn, field.Type, "net/http", "ResponseWriter")
}

func (g *queryGoSource) nameParamUse(fn queryFunc, expr ast.Expr, slot int) bool {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Obj == nil {
		return false
	}
	for _, field := range fn.node.Type.Params.List {
		if slot < len(field.Names) {
			return field.Names[slot].Obj == id.Obj
		}
		slot -= len(field.Names)
	}
	return false
}

package reviewaudit

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
)

type NameAssignmentWrite struct {
	Writes []NameAssignmentSite `json:"writes"`
	Use    NameCodeUse          `json:"use"`
	TypeAt string               `json:"type_at,omitempty"`
}

type NameAssignmentSite struct {
	At     string `json:"at"`
	Target string `json:"target"`
	Value  string `json:"value"`
}

type NameCodeUse struct {
	Declaration string `json:"declaration"`
	At          string `json:"at"`
	Expression  string `json:"expression"`
}

type nameConsumerIssue struct {
	field string
	err   error
}

func (i *nameConsumerIssue) Error() string { return i.err.Error() }
func (i *nameConsumerIssue) Unwrap() error { return i.err }

func (g *queryGoSource) checkNameWriteProof(e Evidence, fn queryFunc) error {
	p := e.ObjectComparison.Proof
	original := fn
	var err error
	fn, err = g.nameWriteFunction(fn, e.WriteEvidence.Calls)
	if err != nil {
		return err
	}
	var conclusion error
	if p.Reference == nil {
		conclusion = checkNamePromisedWrite(e, original)
	}
	kinds := 0
	for _, present := range []bool{p.Reference != nil, p.Assignment != nil, p.API != nil, p.CLI != nil, p.SQL != nil, p.Setting != nil} {
		if present {
			kinds++
		}
	}
	if kinds != 1 {
		return errors.Join(conclusion, fmt.Errorf("writes: выберите proof.assignment, proof.api, proof.cli, proof.sql, proof.setting или proof.reference по фактической операции"))
	}
	switch {
	case p.Assignment != nil:
		err = g.checkNameAssignment(e, fn)
	case p.Setting != nil:
		err = g.checkNameSettingWrite(e, fn)
	case p.SQL != nil:
		err = g.checkNameSQLWrite(e, fn)
	case p.API != nil:
		err = g.checkNameAPI(e, fn)
	case p.CLI != nil:
		err = g.checkNameCLI(e, fn)
	default:
		err = g.checkNameReference(e, fn)
	}
	return errors.Join(conclusion, err)
}

func (g *queryGoSource) nameProofLine(fn queryFunc, at string) (int, error) {
	file, line, base, err := evidenceLocation(at)
	if err != nil || file != fn.file || base != fn.base {
		return 0, fmt.Errorf("%s: укажите строку в функции %s той же ревизии", at, fn.node.Name.Name)
	}
	return line, nil
}

func (g *queryGoSource) nameNodeAt(fn queryFunc, at string, matches func(ast.Node) bool) (ast.Node, error) {
	line, err := g.nameProofLine(fn, at)
	if err != nil {
		return nil, err
	}
	var found ast.Node
	count := 0
	ast.Inspect(fn.node.Body, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		if _, closure := n.(*ast.FuncLit); closure {
			return false
		}
		if g.fset.Position(n.Pos()).Line == line && matches(n) {
			found = n
			count++
		}
		return true
	})
	if count != 1 {
		return nil, fmt.Errorf("%s: в %s требуется одно подходящее выражение, найдено %d", at, fn.node.Name.Name, count)
	}
	return found, nil
}

func (g *queryGoSource) nameCallExpression(fn queryFunc, at, expression string) (*ast.CallExpr, error) {
	n, err := g.nameNodeAt(fn, at, func(n ast.Node) bool { c, ok := n.(*ast.CallExpr); return ok && sameNameExpression(c, expression) })
	if err != nil {
		return nil, err
	}
	return n.(*ast.CallExpr), nil
}

func nameExprIn(n ast.Node, expression string) bool {
	return slices.ContainsFunc(slices.Collect(ast.Preorder(n)), func(n ast.Node) bool { x, ok := n.(ast.Expr); return ok && sameNameExpression(x, expression) })
}

func (g *queryGoSource) checkNameAssignment(e Evidence, fn queryFunc) error {
	pair, p := e.ObjectComparison, e.ObjectComparison.Proof
	a := p.Assignment
	if len(a.Writes) == 0 {
		return fmt.Errorf("proof.assignment.writes: укажите присваивания с целевым полем и выражением значения")
	}
	proofLines := map[string]bool{}
	var assigned ast.Expr
	var scenario *ast.FuncLit
	var binding *ast.Object
	var assignedTarget string
	var assignedNode ast.Expr
	ordinaryWrite := false
	commaOK := false
	for _, site := range a.Writes {
		if proofLines[site.At] {
			return fmt.Errorf("proof.assignment.writes: повтор строки %s", site.At)
		}
		node, err := g.nameNodeAt(fn, site.At, func(n ast.Node) bool {
			s, ok := n.(*ast.AssignStmt)
			return ok && slices.ContainsFunc(s.Lhs, func(x ast.Expr) bool { return sameNameExpression(x, site.Target) })
		})
		if err != nil {
			if ordinaryWrite {
				return fmt.Errorf("proof.assignment: записи должны относиться к одному сценарию")
			}
			var special *nameSynctestMatch
			special = g.nameSynctestNode(fn, site.At, site.Target, true)
			if special == nil || (scenario != nil && scenario != special.scenario) {
				return fmt.Errorf("proof.assignment: найдите присваивание %s: %w", site.Target, err)
			}
			node, scenario = special.node, special.scenario
		} else {
			if scenario != nil {
				return fmt.Errorf("proof.assignment: записи должны относиться к одному сценарию")
			}
			ordinaryWrite = true
		}
		stmt := node.(*ast.AssignStmt)
		slot := slices.IndexFunc(stmt.Lhs, func(x ast.Expr) bool { return sameNameExpression(x, site.Target) })
		if scenario != nil {
			root := nameSynctestTargetRoot(fn, stmt.Lhs[slot])
			if root == nil {
				return fmt.Errorf("proof.assignment: в synctest укажите простую локальную переменную; поля и индексы не поддерживаются")
			}
			if binding != nil && binding != root.Obj {
				return fmt.Errorf("proof.assignment: записи должны относиться к одному значению")
			}
			binding = root.Obj
		}
		if calledName(stmt.Lhs[slot]) != pair.Actual {
			return fmt.Errorf("proof.assignment: поле %s не соответствует actual %s", site.Target, pair.Actual)
		}
		exprSlot := slot
		isMapOK := false
		if len(stmt.Rhs) == 1 && len(stmt.Lhs) == 2 && slot == 1 {
			_, isMapOK = stmt.Rhs[0].(*ast.IndexExpr)
			if isMapOK {
				exprSlot = 0
			}
		}
		if exprSlot >= len(stmt.Rhs) || !sameNameExpression(stmt.Rhs[exprSlot], site.Value) {
			return fmt.Errorf("proof.assignment: %s должно получать правую часть %s", site.Target, site.Value)
		}
		if site.At == e.WriteEvidence.WriteAt && (site.At == pair.ActualAt || g.nameAssignmentFieldDeclaration(fn, stmt.Lhs[slot], pair.ActualAt)) && (sameNameExpression(stmt.Lhs[slot], p.Expression) || sameNameAssignment(stmt, p.Expression)) {
			assigned, commaOK = stmt.Rhs[exprSlot], isMapOK
			assignedTarget = site.Target
			assignedNode = stmt.Lhs[slot]
		}
		proofLines[site.At] = true
	}
	if assigned == nil {
		return fmt.Errorf("proof.expression: свяжите выражение с полем на write_at и actual_at")
	}
	if commaOK {
		if p.Type != "bool" || a.TypeAt != "" {
			return fmt.Errorf("proof.type: для признака наличия из второго результата обращения к карте укажите bool")
		}
	} else if p.Type != "" {
		call, ok := assigned.(*ast.CallExpr)
		if !ok || a.TypeAt == "" {
			return fmt.Errorf("proof.assignment.type_at: укажите объявление результата функции из правой части")
		}
		producer, err := g.declaration(a.TypeAt)
		if err != nil {
			return err
		}
		typ := nameResultType(producer, 0)
		if producer.base != fn.base || producer.node.Name.Name != calledName(call.Fun) || g.receiver(fn, producer, call, "") != nil || typ == nil || !sameNameExpression(typ, p.Type) {
			return fmt.Errorf("proof.assignment.type_at: тип должен соответствовать результату вызова в присваивании")
		}
		proofLines[a.TypeAt] = true
	} else if a.TypeAt != "" {
		return fmt.Errorf("proof.type: укажите тип из объявления")
	}
	consumer, err := g.nameCaller(a.Use.Declaration)
	if err != nil {
		return &nameConsumerIssue{field: pair.Actual, err: fmt.Errorf("proof.assignment.use: %w", err)}
	}
	if consumer.base != fn.base {
		return fmt.Errorf("proof.assignment.use: укажите потребителя на проверяемой ревизии")
	}
	_, err = g.nameNodeAt(consumer, a.Use.At, func(n ast.Node) bool {
		x, ok := n.(ast.Expr)
		if !ok || !sameNameExpression(x, a.Use.Expression) || nameIsAssignmentTarget(consumer, x) {
			return false
		}
		for node := range ast.Preorder(x) {
			if field, ok := node.(*ast.SelectorExpr); ok && field.Sel.Name == pair.Actual {
				return pair.ActualAt == e.WriteEvidence.WriteAt || nameBoundFieldUse(fn, consumer, assignedNode, field)
			}
			if id, ok := node.(*ast.Ident); ok && id.Name == pair.Actual && (consumer.node.Body == fn.node.Body || g.nameAssignmentGlobal(fn, consumer, id, assignedNode)) {
				return true
			}
		}
		return false
	})
	if scenario != nil {
		match := g.nameSynctestNode(consumer, a.Use.At, a.Use.Expression, false)
		if consumer.node.Body != fn.node.Body || match == nil || match.scenario != scenario || !nameSynctestUsesBinding(match.node, binding, assignedTarget) || nameIsAssignmentTarget(consumer, match.node.(ast.Expr)) {
			return &nameConsumerIssue{field: pair.Actual, err: fmt.Errorf("proof.assignment.use: покажите чтение того же значения в сценарии synctest")}
		}
		err = nil
	}
	if err != nil {
		return &nameConsumerIssue{field: pair.Actual, err: fmt.Errorf("proof.assignment.use: покажите выражение, которое читает %s: %w", pair.Actual, err)}
	}
	proofLines[a.Use.At] = true
	return checkNameWriteWordLinks(p.Words, nameWriteLines(e, proofLines))
}

// nameBoundFieldUse ties a declared field to a read from the written instance.
func nameBoundFieldUse(writer, reader queryFunc, assigned ast.Expr, read *ast.SelectorExpr) bool {
	field, ok := assigned.(*ast.SelectorExpr)
	if !ok || writer.file != reader.file {
		return false
	}
	source, sourceOK := field.X.(*ast.Ident)
	destination, destinationOK := read.X.(*ast.Ident)
	if !sourceOK || !destinationOK || source.Obj == nil || destination.Obj == nil {
		return false
	}
	writerBody, readerBody := writer.node.Body, reader.node.Body
	writeStmt, direct := nameFieldStatement(writerBody, field).(*ast.AssignStmt)
	readStmt := nameFieldStatement(readerBody, read)
	switch readStmt.(type) {
	case *ast.AssignStmt, *ast.ReturnStmt, *ast.ExprStmt:
	default:
		return false
	}
	if !direct {
		return false
	}
	if writerBody == readerBody {
		return source.Obj == destination.Obj && nameFieldUnchanged(writerBody, writeStmt, readStmt, field)
	}
	binding, ok := destination.Obj.Decl.(*ast.AssignStmt)
	if !ok || len(binding.Rhs) != 1 || nameFieldStatement(readerBody, binding) != binding {
		return false
	}
	call, ok := binding.Rhs[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	callee, ok := call.Fun.(*ast.Ident)
	if !ok || callee.Obj == nil || callee.Obj.Decl != writer.node {
		return false
	}
	slot := slices.IndexFunc(binding.Lhs, func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)
		return ok && id.Obj == destination.Obj
	})
	if slot < 0 || !nameFieldUnchanged(readerBody, binding, readStmt, read) {
		return false
	}
	for _, stmt := range writerBody.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || slot >= len(ret.Results) || !nameFieldUnchanged(writerBody, writeStmt, ret, field) {
			continue
		}
		if id, ok := ret.Results[slot].(*ast.Ident); ok && id.Obj == source.Obj {
			return true
		}
	}
	return false
}

func nameFieldStatement(body *ast.BlockStmt, node ast.Node) ast.Stmt {
	for _, stmt := range body.List {
		if stmt.Pos() <= node.Pos() && node.End() <= stmt.End() {
			return stmt
		}
	}
	return nil
}

func nameFieldUnchanged(body *ast.BlockStmt, from, to ast.Stmt, field *ast.SelectorExpr) bool {
	if !nameValueUnchanged(body, from, to, nameIdentifier(field.X)) {
		return false
	}
	sameField := func(expr ast.Expr) bool {
		other, ok := expr.(*ast.SelectorExpr)
		if !ok || other.Sel.Name != field.Sel.Name {
			return false
		}
		root, ok := other.X.(*ast.Ident)
		return ok && root.Obj == field.X.(*ast.Ident).Obj
	}
	for _, stmt := range body.List {
		if stmt.Pos() <= from.Pos() || stmt.Pos() >= to.Pos() {
			continue
		}
		switch stmt.(type) {
		case *ast.ReturnStmt, *ast.BranchStmt:
			return false
		}
		for n := range ast.Preorder(stmt) {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if slices.ContainsFunc(n.Lhs, sameField) {
					return false
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND && sameField(n.X) {
					return false
				}
			}
		}
	}
	return true
}

func sameNameAssignment(stmt *ast.AssignStmt, text string) bool {
	tree, err := parser.ParseFile(token.NewFileSet(), "proof.go", "package p\nfunc f() {\n"+text+"\n}", 0)
	if err != nil || len(tree.Decls) != 1 {
		return false
	}
	fn, ok := tree.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body.List) != 1 {
		return false
	}
	want, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok || stmt.Tok != want.Tok || len(stmt.Lhs) != len(want.Lhs) || len(stmt.Rhs) != len(want.Rhs) {
		return false
	}
	for i := range stmt.Lhs {
		if nameExpression(stmt.Lhs[i]) != nameExpression(want.Lhs[i]) {
			return false
		}
	}
	for i := range stmt.Rhs {
		if nameExpression(stmt.Rhs[i]) != nameExpression(want.Rhs[i]) {
			return false
		}
	}
	return true
}

func (g *queryGoSource) nameAssignmentFieldDeclaration(fn queryFunc, target ast.Expr, at string) bool {
	sel, ok := target.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	root, ok := sel.X.(*ast.Ident)
	if !ok || root.Obj == nil {
		return false
	}
	spec, ok := root.Obj.Decl.(*ast.ValueSpec)
	if !ok {
		return false
	}
	typ, ok := spec.Type.(*ast.Ident)
	if !ok {
		return false
	}
	file, line, base, err := evidenceLocation(at)
	if err != nil || file != fn.file || base != fn.base {
		return false
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return false
	}
	for _, d := range tree.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, s := range gen.Specs {
			t, ok := s.(*ast.TypeSpec)
			if !ok || t.Name.Name != typ.Name || typ.Obj == nil || typ.Obj != t.Name.Obj {
				continue
			}
			st, ok := t.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == sel.Sel.Name && g.fset.Position(name.Pos()).Line == line {
						return true
					}
				}
			}
		}
	}
	return false
}

func (g *queryGoSource) nameAssignmentGlobal(writer, reader queryFunc, read *ast.Ident, target ast.Expr) bool {
	written, ok := target.(*ast.Ident)
	if !ok || written.Name != read.Name || writer.base != reader.base || path.Dir(writer.file) != path.Dir(reader.file) {
		return false
	}
	wt, err := g.parseFile(writer.file, writer.base)
	if err != nil {
		return false
	}
	rt, err := g.parseFile(reader.file, reader.base)
	if err != nil || wt.Name.Name != rt.Name.Name {
		return false
	}
	var global *ast.Object
	for _, tree := range []*ast.File{wt, rt} {
		for _, decl := range tree.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				if v, ok := spec.(*ast.ValueSpec); ok {
					for _, id := range v.Names {
						if id.Name == written.Name {
							if global != nil && global != id.Obj {
								return false
							}
							global = id.Obj
						}
					}
				}
			}
		}
		if wt == rt {
			break
		}
	}
	if global == nil || (read.Obj != nil && read.Obj != global) || (written.Obj != nil && written.Obj != global) {
		return false
	}
	return true
}

func nameIsAssignmentTarget(fn queryFunc, expr ast.Expr) bool {
	for n := range ast.Preorder(fn.node.Body) {
		stmt, ok := n.(*ast.AssignStmt)
		if ok && slices.ContainsFunc(stmt.Lhs, func(x ast.Expr) bool { return x.Pos() <= expr.Pos() && expr.End() <= x.End() }) {
			return true
		}
	}
	return false
}

func checkNameWriteWordLinks(words []NameProofWord, lines map[string]bool) error {
	for _, word := range words {
		if !lines[word.Fact.Location] {
			return fmt.Errorf("proof.words %s: свяжите факт с подтверждённой операцией, контрактом или использованием", word.Word)
		}
	}
	return nil
}

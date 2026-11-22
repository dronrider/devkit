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
	Writes           []NameAssignmentSite `json:"writes"`
	Use              NameCodeUse          `json:"use"`
	TypeAt           string               `json:"type_at,omitempty"`
	PointerCallAt    string               `json:"pointer_call_at,omitempty"`
	NextCallReturnAt string               `json:"next_call_return_at,omitempty"`
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
	for _, present := range []bool{p.Reference != nil, p.Assignment != nil, p.API != nil, p.CLI != nil, p.SQL != nil, p.Setting != nil, p.Effect != nil} {
		if present {
			kinds++
		}
	}
	if kinds != 1 {
		return errors.Join(conclusion, fmt.Errorf("writes: выберите proof.assignment, proof.api, proof.cli, proof.sql, proof.setting, proof.effect или proof.reference по фактической операции"))
	}
	switch {
	case p.Assignment != nil:
		err = g.checkNameAssignment(e, original, fn)
	case p.Effect != nil && p.Effect.Kind == "http_json":
		err = g.checkNameHTTPJSONWrite(e, original, fn)
	case p.Effect != nil:
		err = g.checkNameSystemWrite(e, fn)
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

func (g *queryGoSource) checkNameAssignment(e Evidence, entry, fn queryFunc) error {
	pair, p := e.ObjectComparison, e.ObjectComparison.Proof
	a := p.Assignment
	pointerCall, nextCall := a.PointerCallAt != "", a.NextCallReturnAt != ""
	if pointerCall && nextCall || (pointerCall || nextCall) && len(a.Writes) != 1 {
		return fmt.Errorf("proof.assignment: укажите один способ чтения и одно присваивание")
	}
	if len(a.Writes) == 0 {
		return fmt.Errorf("proof.assignment.writes: укажите присваивания с целевым полем и выражением значения")
	}
	proofLines := map[string]bool{}
	var assigned ast.Expr
	var scenario *ast.FuncLit
	var binding *ast.Object
	var assignedTarget string
	var assignedNode ast.Expr
	var writeStmt *ast.AssignStmt
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
		if site.At == e.WriteEvidence.WriteAt && (site.At == pair.ActualAt || g.nameAssignmentFieldDeclaration(fn, stmt.Lhs[slot], pair.ActualAt) || pointerCall || nextCall) && (sameNameExpression(stmt.Lhs[slot], p.Expression) || sameNameAssignment(stmt, p.Expression)) {
			assigned, commaOK = stmt.Rhs[exprSlot], isMapOK
			assignedTarget = site.Target
			assignedNode = stmt.Lhs[slot]
			writeStmt = stmt
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
				if pointerCall {
					written, ok := assignedNode.(*ast.SelectorExpr)
					return ok && g.namePointerFieldUse(fn, consumer, writeStmt, written, field, a.PointerCallAt, pair.ActualAt)
				}
				if nextCall {
					written, ok := assignedNode.(*ast.SelectorExpr)
					return ok && g.nameCacheFieldUse(entry, fn, consumer, writeStmt, written, field, assigned, pair.ActualAt, a.NextCallReturnAt, e.WriteEvidence.Calls)
				}
				if written, ok := assignedNode.(*ast.SelectorExpr); ok && nameIdentifier(written.X) != "" {
					return nameBoundFieldUse(fn, consumer, written, field) ||
						g.nameNestedLocalFieldRead(fn, consumer, written, field) ||
						g.nameFieldCallUse(entry, fn, consumer, written, field, e.WriteEvidence.Calls)
				}
				return pair.ActualAt == e.WriteEvidence.WriteAt
			}
			if id, ok := node.(*ast.Ident); !pointerCall && !nextCall && ok && id.Name == pair.Actual && (consumer.node.Body == fn.node.Body || g.nameAssignmentGlobal(fn, consumer, id, assignedNode)) {
				return true
			}
		}
		return false
	})
	if scenario != nil && !pointerCall && !nextCall {
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
	if pointerCall {
		proofLines[a.PointerCallAt] = true
		proofLines[pair.ActualAt] = true
	}
	if nextCall {
		proofLines[a.NextCallReturnAt] = true
		proofLines[pair.ActualAt] = true
	}
	return checkNameWriteWordLinks(p.Words, nameWriteLines(e, proofLines))
}

// nameBoundFieldUse ties a field read to the written instance.
func nameBoundFieldUse(writer, reader queryFunc, field, read *ast.SelectorExpr) bool {
	if writer.file != reader.file {
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
	switch stmt := readStmt.(type) {
	case *ast.AssignStmt, *ast.ReturnStmt, *ast.ExprStmt:
	case *ast.IfStmt:
		if writerBody != readerBody || source.Obj != destination.Obj ||
			!nameDirectFieldCondition(readerBody, writeStmt, stmt, read, destination.Obj) {
			return false
		}
	default:
		return false
	}
	if !direct {
		return false
	}
	switch declaration := source.Obj.Decl.(type) {
	case *ast.ValueSpec, *ast.AssignStmt:
		decl, ok := declaration.(ast.Node)
		if !ok || !nameFieldUnchanged(writerBody, decl, writeStmt, field) {
			return false
		}
	}
	if writerBody == readerBody {
		return source.Obj == destination.Obj && nameFieldUnchanged(writerBody, writeStmt, readStmt, field)
	}
	if source.Obj == destination.Obj && writerBody.End() < readStmt.Pos() && len(writerBody.List) > 0 && writerBody.List[len(writerBody.List)-1] == writeStmt {
		return nameFieldClosureCalled(readerBody, writerBody, readStmt, read)
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
	next := slices.Index(writerBody.List, ast.Stmt(writeStmt)) + 1
	if next == 0 || next >= len(writerBody.List) {
		return false
	}
	ret, ok := writerBody.List[next].(*ast.ReturnStmt)
	if !ok || slot >= len(ret.Results) {
		return false
	}
	id, ok := ret.Results[slot].(*ast.Ident)
	return ok && id.Obj == source.Obj
}

// The condition must read the field directly before either branch. An init
// or a short-circuit expression can change or skip that read.
func nameDirectFieldCondition(body *ast.BlockStmt, write *ast.AssignStmt, stmt *ast.IfStmt, read *ast.SelectorExpr, receiver *ast.Object) bool {
	if write == nil || stmt.Init != nil {
		return false
	}
	start := slices.Index(body.List, ast.Stmt(write))
	end := slices.Index(body.List, ast.Stmt(stmt))
	if start < 0 || end <= start {
		return false
	}
	for _, between := range body.List[start+1 : end] {
		assignment, ok := between.(*ast.AssignStmt)
		if !ok || !nameIndependentAssignment(assignment, receiver) {
			return false
		}
	}
	switch cond := ast.Unparen(stmt.Cond).(type) {
	case *ast.SelectorExpr:
		return cond == read
	case *ast.UnaryExpr:
		return cond.Op == token.NOT && ast.Unparen(cond.X) == read
	case *ast.BinaryExpr:
		switch cond.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			_, leftLiteral := ast.Unparen(cond.X).(*ast.BasicLit)
			_, rightLiteral := ast.Unparen(cond.Y).(*ast.BasicLit)
			return ast.Unparen(cond.X) == read && rightLiteral ||
				ast.Unparen(cond.Y) == read && leftLiteral
		}
	}
	return false
}

// Both calls must receive the same binding. Unrelated simple assignments
// can intervene without accessing the instance.
func (g *queryGoSource) nameFieldCallUse(entry, writer, reader queryFunc, field, read *ast.SelectorExpr, calls []NameWriteCall) bool {
	source, sourceOK := field.X.(*ast.Ident)
	destination, destinationOK := read.X.(*ast.Ident)
	if len(calls) == 0 || writer.file != reader.file || !sourceOK || !destinationOK ||
		source.Obj == nil || destination.Obj == nil || !nameSameFieldParameter(source, destination) {
		return false
	}
	writeStmt, direct := nameFieldStatement(writer.node.Body, field).(*ast.AssignStmt)
	readStmt := nameFieldStatement(reader.node.Body, read)
	if !direct || writer.node.Body.List[len(writer.node.Body.List)-1] != writeStmt ||
		!nameFieldUnchanged(writer.node.Body, source.Obj.Decl.(*ast.Field), writeStmt, field) ||
		readStmt == nil ||
		!nameFieldUnchanged(reader.node.Body, destination.Obj.Decl.(*ast.Field), readStmt, read) {
		return false
	}
	switch readStmt.(type) {
	case *ast.AssignStmt, *ast.ReturnStmt, *ast.ExprStmt:
	default:
		return false
	}
	for _, stmt := range reader.node.Body.List {
		if stmt == readStmt {
			break
		}
		assignment, ok := stmt.(*ast.AssignStmt)
		if !ok || !nameIndependentAssignment(assignment, destination.Obj) {
			return false
		}
	}
	parent, err := g.nameWriteFunction(entry, calls[:len(calls)-1])
	if err != nil {
		return false
	}
	effect, _, err := g.call(parent, calls[len(calls)-1].At, func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)
		return ok && id.Obj != nil && id.Obj.Decl == writer.node
	})
	if err != nil {
		return false
	}
	effectStmt, ok := nameFieldStatement(parent.node.Body, effect).(*ast.ExprStmt)
	sourceSlot := slices.Index(paramNames(writer.node.Type.Params), source.Name)
	readSlot := slices.Index(paramNames(reader.node.Type.Params), destination.Name)
	if !ok || effectStmt.X != effect || sourceSlot < 0 || sourceSlot >= len(effect.Args) || readSlot < 0 {
		return false
	}
	binding, ok := effect.Args[sourceSlot].(*ast.Ident)
	next := slices.Index(parent.node.Body.List, ast.Stmt(effectStmt)) + 1
	if !ok || binding.Obj == nil || next >= len(parent.node.Body.List) {
		return false
	}
	for _, stmt := range parent.node.Body.List[next:] {
		var expression ast.Expr
		switch stmt := stmt.(type) {
		case *ast.AssignStmt:
			if !nameIndependentAssignment(stmt, binding.Obj) {
				return false
			}
			continue
		case *ast.ExprStmt:
			expression = stmt.X
		case *ast.ReturnStmt:
			if len(stmt.Results) == 1 {
				expression = stmt.Results[0]
			}
		}
		call, ok := expression.(*ast.CallExpr)
		if !ok || readSlot >= len(call.Args) {
			return false
		}
		for index, arg := range call.Args {
			if _, literal := arg.(*ast.BasicLit); index != readSlot && !literal {
				return false
			}
		}
		callee, called := call.Fun.(*ast.Ident)
		value, passed := call.Args[readSlot].(*ast.Ident)
		return called && passed && callee.Obj != nil && callee.Obj.Decl == reader.node && value.Obj == binding.Obj
	}
	return false
}

func nameIndependentAssignment(stmt *ast.AssignStmt, binding *ast.Object) bool {
	for _, target := range stmt.Lhs {
		if id, ok := target.(*ast.Ident); !ok || id.Obj == binding {
			return false
		}
	}
	for _, value := range stmt.Rhs {
		switch x := value.(type) {
		case *ast.BasicLit:
		case *ast.Ident:
			if x.Obj == binding {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func nameSameFieldParameter(source, destination *ast.Ident) bool {
	a, fromParameter := source.Obj.Decl.(*ast.Field)
	b, toParameter := destination.Obj.Decl.(*ast.Field)
	if !fromParameter || !toParameter {
		return false
	}
	aPointer, fromPointer := a.Type.(*ast.StarExpr)
	bPointer, toPointer := b.Type.(*ast.StarExpr)
	if !fromPointer || !toPointer {
		return false
	}
	aType, fromType := aPointer.X.(*ast.Ident)
	bType, toType := bPointer.X.(*ast.Ident)
	return fromType && toType && aType.Obj != nil && aType.Obj == bType.Obj
}

func nameFieldClosureCalled(readerBody, writerBody *ast.BlockStmt, use ast.Stmt, field *ast.SelectorExpr) bool {
	declaration, ok := nameFieldStatement(readerBody, writerBody).(*ast.AssignStmt)
	if !ok || len(declaration.Lhs) != 1 || len(declaration.Rhs) != 1 {
		return false
	}
	lit, ok := declaration.Rhs[0].(*ast.FuncLit)
	binding, named := declaration.Lhs[0].(*ast.Ident)
	if !ok || lit.Body != writerBody || !named || binding.Obj == nil {
		return false
	}
	for _, stmt := range readerBody.List {
		effect, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := effect.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		callee, ok := call.Fun.(*ast.Ident)
		if ok && callee.Obj == binding.Obj &&
			nameValueUnchanged(readerBody, declaration, effect, binding.Name) &&
			nameFieldUnchanged(readerBody, effect, use, field) {
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

func nameFieldUnchanged(body *ast.BlockStmt, from, to ast.Node, field *ast.SelectorExpr) bool {
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
		switch stmt := stmt.(type) {
		case *ast.ReturnStmt, *ast.BranchStmt:
			return false
		case *ast.IfStmt:
			if id, ok := stmt.Cond.(*ast.Ident); ok && id.Name == "true" && id.Obj == nil {
				for _, bodyStmt := range stmt.Body.List {
					if _, returns := bodyStmt.(*ast.ReturnStmt); returns {
						return false
					}
				}
			}
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

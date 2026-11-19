package reviewaudit

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

func nameReturnedObject(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return nameReturnedObject(expr.X)
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return nameReturnedObject(expr.X)
		}
	case *ast.CompositeLit:
		return typeName(expr.Type)
	case *ast.CallExpr:
		return calledName(expr.Fun)
	}
	return calledName(expr)
}

// Follow an assignment to an enclosing local through a single if branch.
func nameAssignmentThroughBranch(parents []ast.Node, assign *ast.AssignStmt, useBlock, use ast.Node, value string) bool {
	if assign.Tok != token.ASSIGN ||
		len(parents) < 4 {
		return false
	}
	branch, ok := parents[len(parents)-3].(*ast.IfStmt)
	if !ok || nameDeclaresLocal(branch.Init, value) {
		return false
	}
	inner, ok := parents[len(parents)-2].(*ast.BlockStmt)
	if !ok || (inner != branch.Body && inner != branch.Else) || !nameValueUnchanged(inner, assign, use, value) {
		return false
	}
	outer, ok := parents[len(parents)-4].(*ast.BlockStmt)
	if !ok || !slices.Contains(outer.List, ast.Stmt(branch)) || !nameValueUnchangedWithin(outer, branch, use, value) {
		return false
	}
	if slices.ContainsFunc(inner.List, func(stmt ast.Stmt) bool {
		return stmt.Pos() < assign.Pos() && nameDeclaresLocal(stmt, value)
	}) {
		return false
	}
	return slices.ContainsFunc(outer.List, func(stmt ast.Stmt) bool {
		return stmt.Pos() < branch.Pos() && nameDeclaresLocal(stmt, value)
	})
}

func nameDeclaresLocal(stmt ast.Stmt, value string) bool {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		return stmt.Tok == token.DEFINE && slices.ContainsFunc(stmt.Lhs, func(expr ast.Expr) bool { return nameIdentifier(expr) == value })
	case *ast.DeclStmt:
		group, ok := stmt.Decl.(*ast.GenDecl)
		return ok && group.Tok == token.VAR && slices.ContainsFunc(group.Specs, func(spec ast.Spec) bool {
			return slices.ContainsFunc(spec.(*ast.ValueSpec).Names, func(name *ast.Ident) bool { return name.Name == value })
		})
	}
	return false
}

func checkCreatedResult(e Evidence, declaration string, source *evidenceSource) error {
	g := newQueryGoSource(source)
	fn, err := g.declaration(declaration)
	if err != nil {
		return err
	}
	pair := e.ObjectComparison
	slot, err := g.nameReturnSlot(fn, e.ResultEvidence.ReturnAt, pair.Actual)
	if err != nil {
		return err
	}
	expected := nameProofObjects(pair)
	if pair.Proof != nil && (len(pair.Proof.Words) == 0 || len(pair.Proof.Words) != len(pair.Expected) || pair.Proof.Words[len(pair.Proof.Words)-1].Role != "object") {
		return fmt.Errorf("proof.words: обещанный результат должен иметь роль object")
	}
	results := fn.node.Type.Results
	if results != nil {
		for _, field := range results.List {
			count := max(1, len(field.Names))
			if slot < count {
				actual := typeName(field.Type)
				if createdResultMatches(expected, field.Type) {
					return nil
				}
				if actual != "" && pair.EquivalentAt != "" {
					typed := *pair
					typed.Actual = actual
					return checkNameEquivalence(&typed, source)
				}
				var spelling bytes.Buffer
				if err := printer.Fprint(&spelling, g.fset, field.Type); err != nil {
					return err
				}
				return nameConclusionError("result_object", "имя обещает %q, фактический тип результата %q; сопоставьте возвращаемый объект целиком", strings.Join(pair.Expected, " "), spelling.String())
			}
			slot -= count
		}
	}
	return fmt.Errorf("return_at не указывает тип возвращаемого значения")
}

func createdResultMatches(expected []string, result ast.Expr) bool {
	collection := false
	for {
		switch expr := result.(type) {
		case *ast.StarExpr:
			result = expr.X
		case *ast.ArrayType:
			collection = true
			result = expr.Elt
		case *ast.MapType:
			collection = true
			result = expr.Value
		default:
			words := identifierWords(typeName(result))
			if !collection {
				return sameObjectName(expected, words)
			}
			return len(words) == len(expected) && len(words) > 0 &&
				strings.EqualFold(words[len(words)-1]+"s", expected[len(expected)-1]) &&
				sameObjectName(expected, words)
		}
	}
}

func (g *queryGoSource) nameResultCall(producer queryFunc, callerAt, callAt, receiverAt string) (queryFunc, *ast.CallExpr, []ast.Node, error) {
	caller, err := g.nameCaller(callerAt)
	if err != nil {
		return queryFunc{}, nil, nil, err
	}
	call, parents, err := g.call(caller, callAt, func(expr ast.Expr) bool {
		return calledName(expr) == producer.node.Name.Name
	})
	if err == nil {
		err = g.receiver(caller, producer, call, receiverAt)
	}
	return caller, call, parents, err
}

func (g *queryGoSource) checkLocalNameUse(caller queryFunc, call *ast.CallExpr, parents []ast.Node, value string, slot int, at string, flow *NameResultFlow) error {
	if flow.Consumer != "" || flow.Projection != nil {
		return fmt.Errorf("flow: для локального использования укажите consumer_at в вызывающей функции")
	}
	if (flow.UseKind != "assignment" && flow.UseKind != "field_read") ||
		(flow.UseKind == "field_read") != (flow.UseField != "") {
		return fmt.Errorf("flow: укажите use_kind assignment или field_read с именем поля в use_field")
	}
	file, line, base, err := evidenceLocation(at)
	if err != nil {
		return err
	}
	if file != caller.file || base != caller.base || len(parents) < 2 || value == "" {
		return fmt.Errorf("flow: %s должен указывать использование полученного value в %s", at, caller.node.Name.Name)
	}
	block := parents[len(parents)-2]
	matches := 0
	for _, stmt := range nameStatements(block) {
		if g.fset.Position(stmt.Pos()).Line > line || g.fset.Position(stmt.End()).Line < line {
			continue
		}
		used := 0
		if flow.UseKind == "assignment" {
			assign, ok := stmt.(*ast.AssignStmt)
			if ok && g.fset.Position(assign.Pos()).Line == line &&
				(assign.Tok == token.ASSIGN || assign.Tok == token.DEFINE) &&
				slices.ContainsFunc(assign.Rhs, func(expr ast.Expr) bool { return nameIdentifier(expr) == value }) {
				used = 1
			}
		} else {
			used = g.countNameFieldReads(stmt, line, value, flow.UseField)
		}
		if used > 0 && nameBoundResult(call, parents, stmt, block, value, slot) {
			matches += used
		}
	}
	if matches != 1 {
		return fmt.Errorf("flow: %s должен однозначно указывать использование результата %q (%s)", at, value, flow.UseKind)
	}
	return nil
}

func (g *queryGoSource) countNameFieldReads(stmt ast.Stmt, line int, value, field string) int {
	var expressions []ast.Expr
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		expressions = s.Rhs
	case *ast.IfStmt:
		if s.Init == nil {
			expressions = []ast.Expr{s.Cond}
		}
	case *ast.ReturnStmt:
		expressions = s.Results
	case *ast.ExprStmt:
		expressions = []ast.Expr{s.X}
	}
	count := 0
	for _, expr := range expressions {
		ast.Inspect(expr, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.UnaryExpr:
				return n.Op != token.AND
			case *ast.SelectorExpr:
				if g.fset.Position(n.Pos()).Line == line && nameIdentifier(n.X) == value && n.Sel.Name == field {
					count++
				}
			}
			return true
		})
	}
	return count
}

func (g *queryGoSource) nameForwardReturn(fn queryFunc, at string) (*ast.ReturnStmt, ast.Node, error) {
	file, line, base, err := evidenceLocation(at)
	if err != nil {
		return nil, nil, err
	}
	var found *ast.ReturnStmt
	var block ast.Node
	nReturns := 0
	if file == fn.file && base == fn.base {
		ast.PreorderStack(fn.node.Body, nil, func(n ast.Node, parents []ast.Node) bool {
			if _, nested := n.(*ast.FuncLit); nested {
				return false
			}
			if ret, ok := n.(*ast.ReturnStmt); ok && g.fset.Position(ret.Pos()).Line == line {
				nReturns++
				found = ret
				if len(parents) > 0 {
					block = parents[len(parents)-1]
				}
			}
			return true
		})
	}
	if nReturns != 1 {
		return nil, nil, fmt.Errorf("%s: укажите возврат из %s", at, fn.node.Name.Name)
	}
	return found, block, nil
}

func (g *queryGoSource) checkNameProjection(producer, caller, consumer queryFunc, call *ast.CallExpr, parents []ast.Node,
	use *ast.CallExpr, useParents []ast.Node, value string, slot int, projection *NameProjection, expected []string, checkMeaning bool) error {
	file, line, base, err := evidenceLocation(projection.RangeAt)
	if err != nil {
		return err
	}
	if file != caller.file || base != caller.base || caller.base != consumer.base || value == "" {
		return fmt.Errorf("projection: укажите цикл и потребителя из той же ревизии")
	}
	var loop *ast.RangeStmt
	for _, parent := range useParents {
		if n, ok := parent.(*ast.RangeStmt); ok && g.fset.Position(n.Pos()).Line == line {
			loop = n
			break
		}
	}
	var block *ast.BlockStmt
	if len(parents) >= 2 {
		block, _ = parents[len(parents)-2].(*ast.BlockStmt)
	}
	if loop == nil || block == nil || !slices.Contains(block.List, ast.Stmt(loop)) || loop.Tok != token.DEFINE {
		return fmt.Errorf("projection: %s должен указывать цикл обработки результата", projection.RangeAt)
	}
	if nameFlowIndex(call, parents, loop, block, []ast.Expr{loop.X}, value, slot) < 0 {
		return fmt.Errorf("projection: цикл должен перебирать значение из вызова")
	}
	var loopStatement ast.Stmt
	for i := range useParents {
		if useParents[i] == loop.Body && i+1 < len(useParents) {
			loopStatement, _ = useParents[i+1].(ast.Stmt)
			break
		}
	}
	method, _ := use.Fun.(*ast.SelectorExpr)
	var field *ast.SelectorExpr
	if method != nil {
		field, _ = method.X.(*ast.SelectorExpr)
	}
	row := nameIdentifier(loop.Value)
	if row == "" || row == "_" || field == nil || nameIdentifier(field.X) != row ||
		field.Sel.Name != projection.Field || loopStatement == nil ||
		!nameValueUnchanged(loop.Body, loop.Value, loopStatement, row) {
		return fmt.Errorf("projection: вызов должен использовать поле %q элемента цикла", projection.Field)
	}
	receiver := recvType(consumer.node)
	if receiver == "" || !strings.EqualFold(receiver, projection.Field) {
		return fmt.Errorf("projection: тип получателя должен соответствовать полю %q", projection.Field)
	}
	if !checkMeaning {
		return nil
	}
	fieldWords := identifierWords(projection.Field)
	if sameObjectName(expected, fieldWords) {
		return nil
	}
	if len(expected) > 1 && sameObjectName(expected[1:], fieldWords) {
		return g.checkNameProjectionFilter(producer, projection.FilterAt, expected[0])
	}
	return fmt.Errorf("projection: поле %q не подтверждает объект из имени", projection.Field)
}

func (g *queryGoSource) checkNameProjectionFilter(fn queryFunc, at, qualifier string) error {
	file, line, base, err := evidenceLocation(at)
	if err != nil || file != fn.file || base != fn.base {
		return fmt.Errorf("projection.filter_at: укажите условие отбора %q в запросе проверяемой функции", qualifier)
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return err
	}
	for _, d := range tree.Decls {
		group, ok := d.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			s := spec.(*ast.ValueSpec)
			for i, value := range s.Values {
				lit, ok := value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || i >= len(s.Names) ||
					line < g.fset.Position(lit.Pos()).Line || line > g.fset.Position(lit.End()).Line {
					continue
				}
				if !slices.ContainsFunc(fn.node.Body.List, func(stmt ast.Stmt) bool {
					for n := range ast.Preorder(stmt) {
						if call, ok := n.(*ast.CallExpr); ok && driverCall(call.Fun) &&
							slices.ContainsFunc(call.Args, func(e ast.Expr) bool { return nameIdentifier(e) == s.Names[i].Name }) {
							return true
						}
					}
					return false
				}) {
					continue
				}
				query, err := strconv.Unquote(lit.Value)
				if err != nil || !strings.HasPrefix(lit.Value, "`") {
					continue
				}
				anchor := line - g.fset.Position(lit.Pos()).Line + 1
				tokens := sqlStatementAt(query, anchor)
				clause := ""
				for j, t := range tokens {
					switch strings.ToUpper(t.text) {
					case "WHERE", "ON", "SELECT", "ORDER", "GROUP", "LIMIT":
						clause = strings.ToUpper(t.text)
					}
					if t.line == anchor && (clause == "WHERE" || clause == "ON") &&
						(j == 0 || sqlWord(tokens, j-1) == "AND" || sqlWord(tokens, j-1) == "WHERE" || sqlWord(tokens, j-1) == "ON") {
						k := j + 1
						if sqlWord(tokens, k) == "." {
							k += 2
						}
						if sqlWord(tokens, k) == "=" && sqlWord(tokens, k+1) == "'"+strings.ToUpper(qualifier)+"'" {
							return nil
						}
					}
				}
			}
		}
	}
	return fmt.Errorf("projection.filter_at: %s не подтверждает отбор %q в выполняемом запросе", at, qualifier)
}

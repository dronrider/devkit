package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
)

// Each hop is checked in the preceding function, starting at the audited symbol.
type NameWriteCall struct {
	At          string            `json:"at"`
	Declaration string            `json:"declaration"`
	ReceiverAt  string            `json:"receiver_at,omitempty"`
	Bindings    []NameCallBinding `json:"bindings"`
}
type NameCallBinding struct {
	Parameter string `json:"parameter"`
	Argument  string `json:"argument"`
}

func (g *queryGoSource) nameWriteFunction(fn queryFunc, calls []NameWriteCall) (queryFunc, error) {
	for i, hop := range calls {
		next, err := g.declaration(hop.Declaration)
		if err != nil {
			return fn, fmt.Errorf("write_evidence.calls[%d]: %w", i, err)
		}
		if next.base != fn.base {
			return fn, fmt.Errorf("write_evidence.calls[%d]: другая ревизия", i)
		}
		call, _, err := g.call(fn, hop.At, func(x ast.Expr) bool { return calledName(x) == next.node.Name.Name })
		if err != nil {
			return fn, fmt.Errorf("write_evidence.calls[%d]: %w", i, err)
		}
		if err := g.receiver(fn, next, call, hop.ReceiverAt); err != nil {
			return fn, err
		}
		var parameters []string
		for _, field := range next.node.Type.Params.List {
			for _, p := range field.Names {
				parameters = append(parameters, p.Name)
			}
		}
		variadic := false
		if len(next.node.Type.Params.List) > 0 {
			_, variadic = next.node.Type.Params.List[len(next.node.Type.Params.List)-1].Type.(*ast.Ellipsis)
		}
		if (!variadic && len(parameters) != len(call.Args)) || (variadic && len(call.Args) < len(parameters)-1) || len(hop.Bindings) != len(call.Args) {
			return fn, fmt.Errorf("write_evidence.calls[%d]: укажите соответствие всех аргументов параметрам вызова", i)
		}
		for j, b := range hop.Bindings {
			parameter := parameters[min(j, len(parameters)-1)]
			if b.Parameter != parameter || !sameNameExpression(call.Args[j], b.Argument) {
				return fn, fmt.Errorf("write_evidence.calls[%d].bindings[%d]: параметр %s получает %s", i, j, parameter, nameExpression(call.Args[j]))
			}
		}
		fn = next
	}
	return fn, nil
}

func (g *queryGoSource) checkNameUseExpression(fn queryFunc, call *ast.CallExpr, parents []ast.Node, value string, slot int, use NameCodeUse) error {
	file, line, base, err := evidenceLocation(use.At)
	if err != nil || file != fn.file || base != fn.base {
		return fmt.Errorf("use_expression: укажите использование в вызывающей функции той же ревизии")
	}
	if nameInUnreachableBranch(parents, call) {
		return fmt.Errorf("use_expression: вызов находится в недостижимой ветке")
	}
	matches := 0
	ast.PreorderStack(fn.node.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		if _, closure := n.(*ast.FuncLit); closure {
			return false
		}
		x, ok := n.(ast.Expr)
		if !ok || g.fset.Position(x.Pos()).Line != line || !sameNameExpression(x, use.Expression) ||
			nameIsAssignmentTarget(fn, x) || nameInUnreachableBranch(stack, x) {
			return true
		}
		var block ast.Node
		for _, parent := range stack {
			switch parent.(type) {
			case *ast.BlockStmt, *ast.CaseClause:
				block = parent
			}
		}
		if value == "" {
			directUse := x == call && slices.ContainsFunc(stack, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.ReturnStmt:
					return slices.Contains(n.Results, ast.Expr(call))
				case *ast.IfStmt:
					return n.Cond == call
				case *ast.ForStmt:
					return n.Cond == call
				case *ast.SwitchStmt:
					return n.Tag == call
				}
				return false
			})
			if (directUse || x != call && slices.Contains(slices.Collect(ast.Preorder(x)), ast.Node(call))) && slot == 0 {
				matches++
			}
			return true
		}
		if nameIntersects(x, map[string]bool{value: true}) && nameBoundResult(call, parents, nameEnclosingStatement(stack, x), block, value, slot) {
			matches++
		}
		return true
	})
	if matches != 1 {
		return fmt.Errorf("use_expression: %s должен однозначно использовать результат %q в выражении %q без переприсваивания", use.At, value, use.Expression)
	}
	return nil
}

func nameInUnreachableBranch(stack []ast.Node, node ast.Node) bool {
	for _, parent := range stack {
		branch, ok := parent.(*ast.IfStmt)
		if !ok || branch.Init != nil {
			continue
		}
		inThen := branch.Body.Pos() <= node.Pos() && node.End() <= branch.Body.End()
		inElse := branch.Else != nil && branch.Else.Pos() <= node.Pos() && node.End() <= branch.Else.End()
		if !inThen && !inElse {
			continue
		}
		if condition, known := nameConstantCondition(branch.Cond); known && condition != inThen {
			return true
		}
	}
	return false
}

func nameConstantCondition(expr ast.Expr) (bool, bool) {
	for n := range ast.Preorder(expr) {
		if id, ok := n.(*ast.Ident); ok && id.Obj != nil {
			return false, false
		}
	}
	value, err := types.Eval(token.NewFileSet(), nil, token.NoPos, nameExpression(expr))
	if err != nil || value.Value == nil || value.Value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(value.Value), true
}

func nameValueUnchangedWithin(block ast.Node, from, to ast.Node, value string) bool {
	if from.End() >= to.Pos() || to.End() > block.End() {
		return false
	}
	valid := true
	ast.Inspect(block, func(n ast.Node) bool {
		if n == nil || !valid {
			return false
		}
		if _, closure := n.(*ast.FuncLit); closure {
			if n.Pos() <= to.Pos() && to.End() <= n.End() {
				valid = false
			}
			return false
		}
		if n.Pos() <= from.End() || n.Pos() >= to.Pos() {
			return true
		}
		switch x := n.(type) {
		case *ast.AssignStmt:
			if slices.ContainsFunc(x.Lhs, func(v ast.Expr) bool { return nameIdentifier(v) == value }) {
				valid = false
			}
		case *ast.ValueSpec:
			if slices.ContainsFunc(x.Names, func(v *ast.Ident) bool { return v.Name == value }) {
				valid = false
			}
		case *ast.RangeStmt:
			if nameIdentifier(x.Key) == value || nameIdentifier(x.Value) == value {
				valid = false
			}
		case *ast.IncDecStmt:
			if nameIdentifier(x.X) == value {
				valid = false
			}
		case *ast.UnaryExpr:
			if x.Op == token.AND && nameIdentifier(x.X) == value {
				valid = false
			}
		}
		return valid
	})
	return valid
}

// A function literal is a caller only at its own func token, never at a call site.
func (g *queryGoSource) nameCaller(ref string) (queryFunc, error) {
	fn, err := g.declaration(ref)
	if err == nil {
		return fn, nil
	}
	file, line, base, locErr := evidenceLocation(ref)
	if locErr != nil {
		return fn, err
	}
	tree, parseErr := g.parseFile(file, base)
	if parseErr != nil {
		return fn, parseErr
	}
	var found *ast.FuncLit
	for n := range ast.Preorder(tree) {
		if lit, ok := n.(*ast.FuncLit); ok && g.fset.Position(lit.Pos()).Line == line {
			if found != nil {
				return fn, err
			}
			found = lit
		}
	}
	if found == nil {
		return fn, err
	}
	return queryFunc{file: file, base: base, node: &ast.FuncDecl{Name: ast.NewIdent("anonymous"), Type: found.Type, Body: found.Body}}, nil
}

func nameWriteLines(e Evidence, lines map[string]bool) map[string]bool {
	for _, hop := range e.WriteEvidence.Calls {
		lines[hop.At] = true
	}
	return lines
}

func nameEnclosingStatement(stack []ast.Node, expr ast.Expr) ast.Node {
	for i := len(stack) - 1; i >= 0; i-- {
		if s, ok := stack[i].(ast.Stmt); ok {
			return s
		}
	}
	return expr
}

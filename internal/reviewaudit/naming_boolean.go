package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

func isNameBooleanExpression(text string) bool {
	expr, err := parser.ParseExpr(text)
	if err != nil {
		return false
	}
	switch x := ast.Unparen(expr).(type) {
	case *ast.UnaryExpr:
		return x.Op == token.NOT
	case *ast.BinaryExpr:
		switch x.Op {
		case token.LAND, token.LOR, token.EQL, token.NEQ, token.GTR, token.LSS, token.GEQ, token.LEQ:
			return true
		}
	}
	return false
}

func checkNameBooleanResult(e Evidence, declaration string, source *evidenceSource) error {
	pair, result := e.ObjectComparison, e.ResultEvidence
	proof, flow := pair.Proof, result.Flow
	if proof == nil || proof.Type != "bool" || flow == nil || flow.Value != "" ||
		flow.UseExpression == "" || flow.UseKind != "" || flow.UseField != "" || flow.Consumer != "" ||
		flow.Projection != nil || len(flow.Forward) != 0 || pair.EquivalentAt != "" || pair.EquivalentFlow != nil ||
		proof.Assignment != nil || proof.API != nil || proof.CLI != nil || proof.Reference != nil || proof.SQL != nil || proof.Setting != nil || proof.Effect != nil {
		return fmt.Errorf("булев результат: укажите полное выражение return, proof.type bool и прямое условие потребителя в flow")
	}
	if err := checkNameProofWords(pair, source); err != nil {
		return err
	}
	g := newQueryGoSource(source)
	fn, err := g.declaration(declaration)
	if err != nil {
		return err
	}
	ret, _, err := g.nameForwardReturn(fn, result.ReturnAt)
	if err != nil {
		return err
	}
	typ := nameResultType(fn, 0)
	if typ == nil || !sameNameExpression(typ, "bool") || len(ret.Results) != 1 ||
		pair.ActualAt != result.ReturnAt || !sameNameExpression(ret.Results[0], pair.Actual) ||
		!sameNameExpression(ret.Results[0], result.Returned) || !sameNameExpression(ret.Results[0], proof.Expression) {
		return fmt.Errorf("булев результат: actual, returned и proof.expression должны совпадать с полным выражением return")
	}
	caller, err := g.nameCaller(flow.Caller)
	if err != nil {
		return err
	}
	call, parents, err := g.call(caller, flow.CallAt, func(x ast.Expr) bool { return calledName(x) == fn.node.Name.Name })
	if err != nil {
		return err
	}
	if err := g.receiver(caller, fn, call, flow.ReceiverAt); err != nil {
		return err
	}
	if result.Consumer != caller.node.Name.Name ||
		!booleanResultUsed(g, caller, call, parents, result.ConsumerAt, flow.UseExpression) {
		return fmt.Errorf("булев результат: покажите условие вызывающей функции, которое использует этот вызов")
	}
	needed := nameDependencies(fn, ret.Results[0], ret.Pos())
	for _, word := range proof.Words {
		if err := g.checkNameProofFact(e, fn, ret.End(), typ, needed, word.Fact); err != nil {
			return fmt.Errorf("proof.words %s: %w", word.Word, err)
		}
	}
	return nil
}

func booleanResultUsed(g *queryGoSource, fn queryFunc, call *ast.CallExpr, parents []ast.Node, at, expression string) bool {
	for _, node := range parents {
		var condition ast.Expr
		switch n := node.(type) {
		case *ast.IfStmt:
			condition = n.Cond
		case *ast.ForStmt:
			condition = n.Cond
		}
		if condition != nil && at == nameRef(fn, g.fset.Position(condition.Pos()).Line) &&
			sameNameExpression(condition, expression) && nameBooleanOperand(condition, call) {
			return true
		}
	}
	return false
}

func nameBooleanOperand(expr ast.Expr, call *ast.CallExpr) bool {
	switch x := ast.Unparen(expr).(type) {
	case *ast.CallExpr:
		return x == call
	case *ast.UnaryExpr:
		return x.Op == token.NOT && nameBooleanOperand(x.X, call)
	case *ast.BinaryExpr:
		return (x.Op == token.LAND || x.Op == token.LOR) &&
			(nameBooleanOperand(x.X, call) || nameBooleanOperand(x.Y, call))
	}
	return false
}

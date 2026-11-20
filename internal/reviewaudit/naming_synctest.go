package reviewaudit

import (
	"go/ast"
	"slices"
	"strings"
)

type nameSynctestMatch struct {
	node     ast.Node
	scenario *ast.FuncLit
}

// Only test callbacks whose execution is guaranteed by testing are traversed.
// Other literals remain opaque, except a goroutine invoked directly by the scenario.
func (g *queryGoSource) nameSynctestNode(fn queryFunc, at, expression string, assignment bool) *nameSynctestMatch {
	line, err := g.nameProofLine(fn, at)
	if err != nil || !strings.HasPrefix(fn.node.Name.Name, "Test") {
		return nil
	}
	testParam := func(fields *ast.FieldList) *ast.Object {
		if fields == nil || len(fields.List) != 1 || len(fields.List[0].Names) != 1 {
			return nil
		}
		ptr, ok := fields.List[0].Type.(*ast.StarExpr)
		if !ok {
			return nil
		}
		sel, ok := ptr.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "T" {
			return nil
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Obj != nil || g.importPath(fn, pkg.Name) != "testing" {
			return nil
		}
		return fields.List[0].Names[0].Obj
	}
	root := testParam(fn.node.Type.Params)
	if root == nil {
		return nil
	}
	var matches []*nameSynctestMatch
	var walk func(ast.Node, *ast.Object, *ast.FuncLit)
	walk = func(body ast.Node, t *ast.Object, scenario *ast.FuncLit) {
		ast.Inspect(body, func(n ast.Node) bool {
			if n == nil {
				return true
			}
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if scenario != nil && g.fset.Position(n.Pos()).Line == line {
				if assignment {
					if stmt, ok := n.(*ast.AssignStmt); ok && slices.ContainsFunc(stmt.Lhs, func(x ast.Expr) bool { return sameNameExpression(x, expression) }) {
						matches = append(matches, &nameSynctestMatch{n, scenario})
					}
				} else if expr, ok := n.(ast.Expr); ok && sameNameExpression(expr, expression) {
					matches = append(matches, &nameSynctestMatch{n, scenario})
				}
			}
			if goStmt, ok := n.(*ast.GoStmt); ok && scenario != nil && !assignment {
				if lit, ok := goStmt.Call.Fun.(*ast.FuncLit); ok {
					walk(lit.Body, t, scenario)
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Run" && recv.Obj == t && len(call.Args) == 2 {
				if lit, ok := call.Args[1].(*ast.FuncLit); ok {
					if param := testParam(lit.Type.Params); param != nil {
						nextScenario := scenario
						if scenario != nil {
							nextScenario = lit
						}
						walk(lit.Body, param, nextScenario)
					}
				}
			}
			if sel.Sel.Name == "Test" && recv.Obj == nil && g.importPath(fn, recv.Name) == "testing/synctest" && len(call.Args) == 2 {
				arg, ok := call.Args[0].(*ast.Ident)
				if !ok || arg.Obj != t {
					return true
				}
				if lit, ok := call.Args[1].(*ast.FuncLit); ok && testParam(lit.Type.Params) != nil {
					walk(lit.Body, testParam(lit.Type.Params), lit)
				}
			}
			return true
		})
	}
	walk(fn.node.Body, root, nil)
	if len(matches) != 1 {
		return nil
	}
	return matches[0]
}

func nameSynctestUsesBinding(n ast.Node, binding *ast.Object, target string) bool {
	if binding == nil {
		return false
	}
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if expr, ok := node.(ast.Expr); ok && sameNameExpression(expr, target) {
			root := nameWriteRoot(expr)
			found = root != nil && root.Obj == binding
		}
		return !found
	})
	return found
}

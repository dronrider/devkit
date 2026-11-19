package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func nameObjectSymbol(object string) string {
	_, symbol, _ := strings.Cut(object, "#")
	return symbol[strings.LastIndexByte(symbol, '.')+1:]
}

func (g *queryGoSource) nameProofFunction(ref, symbol string) (queryFunc, error) {
	fn, err := g.declaration(ref)
	if err == nil || symbol == "" {
		return fn, err
	}
	file, line, base, locationErr := evidenceLocation(ref)
	if locationErr != nil {
		return fn, err
	}
	tree, parseErr := g.parseFile(file, base)
	if parseErr != nil {
		return fn, parseErr
	}
	var match *ast.FuncDecl
	for n := range ast.Preorder(tree) {
		var names, values []ast.Expr
		switch binding := n.(type) {
		case *ast.AssignStmt:
			if binding.Tok == token.DEFINE {
				names, values = binding.Lhs, binding.Rhs
			}
		case *ast.ValueSpec:
			for _, id := range binding.Names {
				names = append(names, id)
			}
			values = binding.Values
		}
		if len(names) != len(values) {
			continue
		}
		for i, name := range names {
			lit, ok := values[i].(*ast.FuncLit)
			id, named := name.(*ast.Ident)
			if !ok || !named || id.Name != symbol || g.fset.Position(id.Pos()).Line != line {
				continue
			}
			if match != nil {
				return queryFunc{}, fmt.Errorf("%s: неоднозначное объявление %s", ref, symbol)
			}
			match = &ast.FuncDecl{Name: id, Type: lit.Type, Body: lit.Body}
		}
	}
	if match == nil {
		return fn, err
	}
	return queryFunc{file: file, base: base, node: match}, nil
}

func checkNameSourceImplementation(e Evidence, declaration string, source *evidenceSource) (string, error) {
	implementation := e.ResultEvidence.Implementation
	name := nameObjectSymbol(e.Object)
	location, _, _ := strings.Cut(e.Object, "#")
	_, _, base, err := evidenceLocation(declaration)
	if e.Check != "result" || err != nil || isGoRef(declaration) || name == "" || location != declaration {
		return "", fmt.Errorf("source: укажите объявление именованной операции вне Go для подпроверки result")
	}
	declared := false
	for _, fact := range e.ResultEvidence.Facts {
		if fact.Location == declaration && containsIdentifier(fact.Quote, name) {
			declared = true
			break
		}
	}
	if !declared {
		return "", fmt.Errorf("source: в result_evidence.facts приведите точную цитату объявления %s на %s", name, declaration)
	}
	fn, err := newQueryGoSource(source).declaration(implementation)
	if err != nil {
		return "", err
	}
	if !isGoRef(implementation) || fn.base != base || fn.node.Name.Name != name {
		return "", fmt.Errorf("source: укажите одноимённую реализацию %s на Go в той же ревизии", name)
	}
	return implementation, nil
}

func (g *queryGoSource) checkNameParameter(ref, symbol string) (bool, error) {
	if symbol == "" || symbol == "_" {
		return false, nil
	}
	file, line, base, err := evidenceLocation(ref)
	if err != nil {
		return false, err
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return false, nil // Preserve the fallback for declarations without source parsing.
	}
	if nameParameterAt(g.fset, tree, line, symbol) == nil {
		return false, nil
	}
	body, err := g.source.read(file, base)
	if err != nil {
		return true, err
	}
	fset := token.NewFileSet()
	tree, err = parser.ParseFile(fset, file, body, 0)
	if err != nil {
		return true, err
	}
	param := nameParameterAt(fset, tree, line, symbol)
	if param == nil || param.Obj == nil {
		return true, fmt.Errorf("не удалось разрешить объявление параметра %s на %s", symbol, ref)
	}
	for node := range ast.Preorder(tree) {
		var targets []ast.Expr
		switch n := node.(type) {
		case *ast.AssignStmt:
			targets = n.Lhs
		case *ast.IncDecStmt:
			targets = []ast.Expr{n.X}
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				targets = []ast.Expr{n.Key, n.Value}
			}
		case *ast.CallExpr:
			if fn, ok := n.Fun.(*ast.Ident); ok && fn.Obj == nil && (fn.Name == "delete" || fn.Name == "clear" || fn.Name == "copy") && len(n.Args) > 0 {
				targets = n.Args[:1]
			}
		}
		for _, target := range targets {
			if id := nameWriteRoot(target); id != nil && id.Obj == param.Obj {
				return true, fmt.Errorf("not_applicable запрещён: параметр %s изменяется на %s:%d", symbol, file, fset.Position(target.Pos()).Line)
			}
		}
	}
	return true, nil
}

func nameParameterAt(fset *token.FileSet, tree *ast.File, line int, symbol string) *ast.Ident {
	for _, node := range tree.Decls {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name == symbol && fset.Position(fn.Pos()).Line == line {
			continue
		}
		for _, field := range fn.Type.Params.List {
			for _, id := range field.Names {
				if id.Name == symbol && (fset.Position(id.Pos()).Line == line || fset.Position(fn.Pos()).Line == line) {
					return id
				}
			}
		}
	}
	return nil
}

func nameWriteRoot(expr ast.Expr) *ast.Ident {
	switch x := expr.(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		return nameWriteRoot(x.X)
	case *ast.IndexExpr:
		return nameWriteRoot(x.X)
	case *ast.StarExpr:
		return nameWriteRoot(x.X)
	case *ast.ParenExpr:
		return nameWriteRoot(x.X)
	}
	return nil
}

package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func checkNameObjectNoWrites(e Evidence, declaration string, words []string, source *evidenceSource) error {
	g := newQueryGoSource(source)
	symbol := nameObjectSymbol(e.Object)
	if parameter, err := g.checkNameParameter(declaration, symbol); parameter || err != nil {
		if err == nil && e.ObjectComparison != nil && !slices.Equal(e.ObjectComparison.Expected, expectedNameWords(words)) {
			return fmt.Errorf("expected: сохраните полный список %q", strings.Join(expectedNameWords(words), " "))
		}
		return err
	}
	fn, err := g.nameProofFunction(declaration, symbol)
	if err != nil {
		return nil
	}
	if _, err := g.declaration(declaration); err != nil && namesResultOrWrite("writes", identifierWords(symbol)) {
		return fmt.Errorf("not_applicable запрещён: имя замыкания %s обещает запись; сопоставьте её с кодом", symbol)
	}
	entries, err := os.ReadDir(filepath.Join(source.root, path.Dir(fn.file)))
	if err != nil {
		return err
	}
	rootTree, err := g.parseFile(fn.file, fn.base)
	if err != nil {
		return err
	}
	functions := map[string][]queryFunc{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file := path.Join(path.Dir(fn.file), entry.Name())
		tree, err := g.parseFile(file, fn.base)
		if err != nil || tree.Name.Name != rootTree.Name.Name {
			continue
		}
		for _, d := range tree.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Body != nil {
				functions[f.Name.Name] = append(functions[f.Name.Name], queryFunc{file: file, base: fn.base, node: f})
			}
		}
	}
	visited := map[*ast.FuncDecl]bool{}
	var visit func(queryFunc) error
	visit = func(current queryFunc) error {
		if visited[current.node] {
			return nil
		}
		visited[current.node] = true
		for n := range ast.Preorder(current.node.Body) {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, selected := call.Fun.(*ast.SelectorExpr)
			if selected && g.importPath(current, nameIdentifier(sel.X)) == "os" && slices.Contains([]string{"WriteFile", "Create", "CreateTemp", "Mkdir", "MkdirAll", "Remove", "RemoveAll", "Rename"}, sel.Sel.Name) {
				return fmt.Errorf("not_applicable запрещён: достижимая запись %s в %s; заполните write_evidence и calls", sel.Sel.Name, nameRef(current, g.fset.Position(call.Pos()).Line))
			}
			if g.namePackageSelector(current, call.Fun, "github.com/spf13/viper", "Set") {
				return fmt.Errorf("not_applicable запрещён: запись настройки в %s; укажите вызов и чтение ключа", nameRef(current, g.fset.Position(call.Pos()).Line))
			}
			for _, next := range functions[calledName(call.Fun)] {
				if (next.node.Recv != nil) != selected || g.receiver(current, next, call, "") != nil {
					continue
				}
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(fn)
}

func checkNameImplementation(contract, implementation string, source *evidenceSource) (string, error) {
	g := newQueryGoSource(source)
	fn, err := g.declaration(implementation)
	if err != nil {
		return "", err
	}
	file, line, base, err := evidenceLocation(contract)
	if err != nil {
		return "", err
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return "", err
	}
	for n := range ast.Preorder(tree) {
		iface, ok := n.(*ast.InterfaceType)
		if !ok {
			continue
		}
		for _, field := range iface.Methods.List {
			signature, ok := field.Type.(*ast.FuncType)
			if !ok || len(field.Names) != 1 || g.fset.Position(field.Pos()).Line != line {
				continue
			}
			if base != fn.base || field.Names[0].Name != fn.node.Name.Name || fn.node.Recv == nil || !sameSignature(signature, fn.node.Type) {
				return "", fmt.Errorf("implementation: имя и сигнатура реализации должны соответствовать методу интерфейса той же ревизии")
			}
			return implementation, nil
		}
	}
	return "", fmt.Errorf("implementation: укажите объявление метода интерфейса")
}

func sameSignature(a, b *ast.FuncType) bool {
	types := func(fields *ast.FieldList) []string {
		var out []string
		if fields != nil {
			for _, field := range fields.List {
				for range max(1, len(field.Names)) {
					out = append(out, nameExpression(field.Type))
				}
			}
		}
		return out
	}
	return slices.Equal(types(a.Params), types(b.Params)) && slices.Equal(types(a.Results), types(b.Results))
}

func (g *queryGoSource) checkNameGuard(e Evidence, fn queryFunc, needed map[string]bool, before token.Pos, fact ReviewFact) error {
	for _, guard := range e.ObjectComparison.Proof.Guards {
		if guard.At != fact.Location {
			continue
		}
		call, err := g.nameCallExpression(fn, guard.At, guard.Expression)
		if err != nil {
			return err
		}
		direct := slices.ContainsFunc(fn.node.Body.List, func(stmt ast.Stmt) bool {
			expr, ok := stmt.(*ast.ExprStmt)
			return ok && expr.X == call && call.End() < before
		})
		if !direct {
			return fmt.Errorf("guards: проверка должна безусловно выполняться до возврата")
		}
		selector, selected := call.Fun.(*ast.SelectorExpr)
		if !selected || g.importPath(fn, nameIdentifier(selector.X)) != "github.com/stretchr/testify/require" || !slices.Contains([]string{"True", "Equal"}, selector.Sel.Name) {
			return fmt.Errorf("guards: требуется проверка, останавливающая тест при невыполнении условия")
		}
		if !nameExprIn(call, fact.Value) {
			return fmt.Errorf("guards: факт отсутствует в проверке")
		}
		for _, arg := range call.Args[1:] {
			dependencies := nameDependencies(fn, arg, call.Pos())
			for id := range dependencies {
				if needed[id] && id != "t" && g.importPath(fn, id) == "" {
					for n := range ast.Preorder(fn.node.Body) {
						if ret, ok := n.(*ast.ReturnStmt); ok && ret.End() == before && nameValueUnchangedWithin(fn.node.Body, call, ret, id) {
							return nil
						}
					}
				}
			}
		}
	}
	return fmt.Errorf("guards: нет связанной обязательной проверки")
}

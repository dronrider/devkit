package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
)

type queryGoSource struct {
	source *evidenceSource
	fset   *token.FileSet
	files  map[string]*ast.File
}

func newQueryGoSource(source *evidenceSource) *queryGoSource {
	return &queryGoSource{source: source, fset: token.NewFileSet(), files: make(map[string]*ast.File)}
}

type queryFunc struct {
	file string
	base bool
	node *ast.FuncDecl
}

func isGoRef(ref string) bool {
	file, _, _, err := evidenceLocation(ref)
	return err == nil && path.Ext(file) == ".go"
}

func (g *queryGoSource) parseFile(file string, base bool) (*ast.File, error) {
	key := file
	if base {
		key = "base@" + file
	}
	tree := g.files[key]
	if tree == nil {
		body, err := g.source.read(file, base)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		tree, err = parser.ParseFile(g.fset, key, body, 0)
		if err != nil {
			return nil, err
		}
		g.files[key] = tree
	}
	return tree, nil
}

func (g *queryGoSource) declaration(ref string) (queryFunc, error) {
	file, line, base, err := evidenceLocation(ref)
	if err != nil {
		return queryFunc{}, err
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return queryFunc{}, err
	}
	for _, item := range tree.Decls {
		decl, ok := item.(*ast.FuncDecl)
		if ok && decl.Body != nil && g.fset.Position(decl.Pos()).Line == line {
			return queryFunc{file: file, base: base, node: decl}, nil
		}
	}
	return queryFunc{}, fmt.Errorf("%s: укажите строку объявления функции", ref)
}

func (g *queryGoSource) checkAdapterCall(object, at string) error {
	method, err := g.declaration(object)
	if err != nil {
		return err
	}
	file, line, base, err := evidenceLocation(at)
	if err != nil {
		return err
	}
	if base != method.base {
		return fmt.Errorf("вызов адаптера должен относиться к той же ревизии")
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return err
	}
	for node := range ast.Preorder(tree) {
		call, ok := node.(*ast.CallExpr)
		if ok && calledName(call.Fun) == method.node.Name.Name && g.fset.Position(call.Pos()).Line == line {
			return nil
		}
	}
	return fmt.Errorf("%s: нужен вызов %s", at, method.node.Name.Name)
}

func (g *queryGoSource) checkEntry(check QueryEntryReview, calls []string) error {
	if strings.TrimSpace(check.Observation) == "" {
		return fmt.Errorf("укажите вывод о связи входа с операцией")
	}
	fn, err := g.declaration(check.Entry)
	if err != nil {
		return err
	}
	call, _, err := g.call(fn, check.CallAt, func(expr ast.Expr) bool { return slices.Contains(calls, calledName(expr)) })
	if err != nil {
		return err
	}
	switch check.Result {
	case "unrelated":
		return nil
	case "inspected":
		if _, err := g.source.line(check.ValidationAt); err != nil {
			return fmt.Errorf("validation_at: %w", err)
		}
		if check.ValidationAt == check.CallAt {
			return fmt.Errorf("validation_at: укажите проверку или место формирования входа отдельно от вызова %s", calledName(call.Fun))
		}
		return g.checkEntryValidation(fn, check)
	default:
		return fmt.Errorf("укажите inspected или unrelated")
	}
}

func (g *queryGoSource) checkEntryValidation(fn queryFunc, check QueryEntryReview) error {
	file, line, base, err := evidenceLocation(check.ValidationAt)
	if err != nil || base != fn.base {
		return fmt.Errorf("validation_at: проверка должна быть в той же ревизии, что и вход")
	}
	_, callLine, _, _ := evidenceLocation(check.CallAt)
	validationLine := line
	if check.ValidationCallAt != "" || check.Validator != "" {
		helper, err := g.declaration(check.Validator)
		if err != nil {
			return fmt.Errorf("validator: %w", err)
		}
		call, _, err := g.call(fn, check.ValidationCallAt, func(expr ast.Expr) bool {
			return calledName(expr) == helper.node.Name.Name
		})
		if err != nil {
			return fmt.Errorf("validation_call_at: %w", err)
		}
		validationLine = g.fset.Position(call.Pos()).Line
		fn = helper
	}
	body := fn.node.Body
	if file != fn.file || base != fn.base || line < g.fset.Position(body.Pos()).Line || line > g.fset.Position(body.End()).Line {
		return fmt.Errorf("validation_at: укажите строку во входной функции или в вызванном ею помощнике")
	}
	if validationLine >= callLine {
		return fmt.Errorf("validation_at: покажите проверку или формирование входа до вызова операции")
	}
	return nil
}

func (g *queryGoSource) path(entry string, trace QueryPath, keys string, static bool) (bool, error) {
	current, err := g.declaration(entry)
	if err != nil {
		return false, err
	}
	if keys != "" && !slices.Contains(paramNames(current.node.Type.Params), keys) {
		return false, fmt.Errorf("%s: параметр ключей %q отсутствует", entry, keys)
	}
	loop := ""
	for _, step := range trace.Steps {
		next, err := g.declaration(step.To)
		if err != nil {
			return false, err
		}
		call, parents, err := g.call(current, step.At, func(expr ast.Expr) bool {
			return calledName(expr) == next.node.Name.Name
		})
		if err != nil {
			return false, fmt.Errorf("%s не ведёт к %s: %w", step.At, step.To, err)
		}
		if err := g.receiver(current, next, call, step.ReceiverAt); err != nil {
			return false, err
		}
		loop, err = g.loopAt(parents, keys, loop, static)
		if err != nil {
			return false, err
		}
		keys = forwardedKeys(call, next.node, keys)
		current = next
	}
	_, parents, err := g.call(current, trace.DriverAt, driverCall)
	if err != nil {
		return false, fmt.Errorf("вызов драйвера: %w", err)
	}
	loop, err = g.loopAt(parents, keys, loop, static)
	if err != nil {
		return false, err
	}
	if loop != trace.LoopAt {
		return false, fmt.Errorf("loop_at: указан %q, вызов по цепочке находится в цикле %q", trace.LoopAt, loop)
	}
	return loop != "", nil
}

func (g *queryGoSource) call(fn queryFunc, ref string, matches func(ast.Expr) bool) (*ast.CallExpr, []ast.Node, error) {
	file, line, base, err := evidenceLocation(ref)
	if err != nil {
		return nil, nil, err
	}
	if file != fn.file || base != fn.base {
		return nil, nil, fmt.Errorf("%s находится вне функции %s", ref, fn.node.Name.Name)
	}
	var found *ast.CallExpr
	var parents []ast.Node
	count := 0
	ast.PreorderStack(fn.node.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && g.fset.Position(call.Pos()).Line == line && matches(call.Fun) {
			found, parents = call, slices.Clone(stack)
			count++
		}
		return true
	})
	if count != 1 {
		return nil, nil, fmt.Errorf("%s: в %s ожидается один подходящий вызов, найдено %d", ref, fn.node.Name.Name, count)
	}
	return found, parents, nil
}

func calledName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.SelectorExpr:
		return expr.Sel.Name
	case *ast.IndexExpr:
		return calledName(expr.X)
	case *ast.IndexListExpr:
		return calledName(expr.X)
	}
	return ""
}

func driverCall(expr ast.Expr) bool {
	if _, ok := expr.(*ast.SelectorExpr); !ok {
		return false
	}
	switch calledName(expr) {
	case "Query", "QueryContext", "QueryRow", "QueryRowContext", "Exec", "ExecContext", "SendBatch", "CopyFrom":
		return true
	}
	return false
}

func (g *queryGoSource) loopAt(parents []ast.Node, keys, existing string, static bool) (string, error) {
	for _, p := range parents {
		switch n := p.(type) {
		case *ast.RangeStmt:
			list, ok := n.X.(*ast.Ident)
			if static && (!ok || keys == "" || list.Name != keys || existing != "") {
				return "", fmt.Errorf("кратность цикла не следует из одного range по ключам, нужен замер")
			}
			if existing == "" {
				pos := g.fset.Position(n.For)
				existing = fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
			}
		case *ast.ForStmt:
			if static {
				return "", fmt.Errorf("для вызова в сложном цикле нужен замер")
			}
			if existing == "" {
				pos := g.fset.Position(n.For)
				existing = fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
			}
		case *ast.IfStmt, *ast.CaseClause, *ast.CommClause, *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
			if static {
				return "", fmt.Errorf("для вызова с условием или отложенным выполнением нужен замер")
			}
		}
	}
	return existing, nil
}

func paramNames(fields *ast.FieldList) []string {
	var names []string
	if fields != nil {
		for _, field := range fields.List {
			if len(field.Names) == 0 {
				names = append(names, "")
			}
			for _, name := range field.Names {
				names = append(names, name.Name)
			}
		}
	}
	return names
}

func forwardedKeys(call *ast.CallExpr, target *ast.FuncDecl, keys string) string {
	if keys != "" {
		params := paramNames(target.Type.Params)
		for i, arg := range call.Args {
			if id, ok := arg.(*ast.Ident); ok && id.Name == keys && i < len(params) {
				return params[i]
			}
		}
	}
	return ""
}

func recvType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	return typeName(fn.Recv.List[0].Type)
}

func typeName(expr ast.Expr) string {
	if ptr, ok := expr.(*ast.StarExpr); ok {
		expr = ptr.X
	}
	return calledName(expr)
}

func (g *queryGoSource) receiver(from, to queryFunc, call *ast.CallExpr, ref string) error {
	targetDir := path.Dir(to.file)
	if from.base != to.base {
		return fmt.Errorf("переход между базой и вершиной в одной цепочке")
	}
	selector, selected := call.Fun.(*ast.SelectorExpr)
	if !selected {
		if path.Dir(from.file) != targetDir || recvType(to.node) != "" {
			return fmt.Errorf("прямой вызов %s не соответствует объявлению в %s", to.node.Name.Name, to.file)
		}
		return nil
	}
	if inner, ok := selector.X.(*ast.CallExpr); ok {
		helper, err := g.declaration(ref)
		if err != nil {
			return fmt.Errorf("для вызова %s через возвращаемый объект укажите receiver_at: %w", to.node.Name.Name, err)
		}
		sel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || !slices.Contains(paramNames(from.node.Recv), calledName(sel.X)) ||
			helper.node.Name.Name != sel.Sel.Name || path.Dir(helper.file) != path.Dir(from.file) ||
			helper.base != from.base || recvType(helper.node) != recvType(from.node) {
			return fmt.Errorf("%s не объявляет вызываемый метод получения объекта", ref)
		}
		results := helper.node.Type.Results
		if results == nil || len(results.List) != 1 ||
			!g.typeMatches(helper, results.List[0].Type, targetDir, recvType(to.node)) {
			return fmt.Errorf("тип результата %s не соответствует получателю %s в %s", ref, to.node.Name.Name, to.file)
		}
		return nil
	}
	if id, ok := selector.X.(*ast.Ident); ok {
		if slices.Contains(paramNames(from.node.Recv), id.Name) &&
			(path.Dir(from.file) != targetDir || recvType(from.node) != recvType(to.node)) {
			return fmt.Errorf("получатель %s не соответствует объявлению в %s", id.Name, to.file)
		}
		for _, field := range from.node.Type.Params.List {
			if slices.ContainsFunc(field.Names, func(n *ast.Ident) bool { return n.Name == id.Name }) &&
				!g.typeMatches(from, field.Type, targetDir, recvType(to.node)) {
				return fmt.Errorf("тип параметра %s не соответствует объявлению в %s", id.Name, to.file)
			}
		}
		if imported := g.importPath(from, id.Name); imported != "" &&
			(!matchesImport(imported, targetDir) || recvType(to.node) != "") {
			return fmt.Errorf("импорт %s не соответствует объявлению в %s", imported, to.file)
		}
	}
	return nil
}

func (g *queryGoSource) typeMatches(from queryFunc, expr ast.Expr, dir, name string) bool {
	if ptr, ok := expr.(*ast.StarExpr); ok {
		expr = ptr.X
	}
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		return selector.Sel.Name == name && matchesImport(g.importPath(from, calledName(selector.X)), dir)
	}
	return typeName(expr) == name && path.Dir(from.file) == dir
}

func (g *queryGoSource) importPath(fn queryFunc, alias string) string {
	file := fn.file
	if fn.base {
		file = "base@" + file
	}
	for _, spec := range g.files[file].Imports {
		p, _ := strconv.Unquote(spec.Path.Value)
		name := path.Base(p)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == alias {
			return p
		}
	}
	return ""
}

func matchesImport(imported, dir string) bool {
	return imported == dir || strings.HasSuffix(imported, "/"+dir)
}

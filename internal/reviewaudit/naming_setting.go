package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
)

type NameSettingWrite struct {
	SubtestAt string      `json:"subtest_at"`
	Use       NameCodeUse `json:"use"`
	ReadAt    string      `json:"read_at"`
}

func (g *queryGoSource) checkNameSettingWrite(e Evidence, fn queryFunc) error {
	p, w := e.ObjectComparison, e.WriteEvidence
	proof := p.Proof.Setting
	if line, err := g.nameProofLine(fn, proof.SubtestAt); err == nil && line == g.fset.Position(fn.node.Pos()).Line {
		return g.checkNameEnvironmentSetting(e, fn)
	}
	run, parents, err := g.call(fn, proof.SubtestAt, func(x ast.Expr) bool {
		sel, ok := x.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" {
			return false
		}
		for _, field := range fn.node.Type.Params.List {
			typ, ok := field.Type.(*ast.StarExpr)
			if ok && g.namePackageSelector(fn, typ.X, "testing", "T") && nameHasParameter(field, nameIdentifier(sel.X)) {
				return true
			}
		}
		return false
	})
	if err != nil || len(run.Args) != 2 || slices.ContainsFunc(parents, func(n ast.Node) bool {
		_, closure := n.(*ast.FuncLit)
		return closure
	}) {
		return fmt.Errorf("proof.setting.subtest_at: укажите вызов Run на параметре *testing.T")
	}
	callback, ok := run.Args[1].(*ast.FuncLit)
	if !ok {
		return fmt.Errorf("proof.setting.subtest_at: второй аргумент Run должен быть телом подтеста")
	}
	fn.node = &ast.FuncDecl{Name: fn.node.Name, Type: callback.Type, Body: callback.Body}
	set, err := g.nameCallExpression(fn, w.WriteAt, p.Proof.Expression)
	if err != nil || !g.namePackageSelector(fn, set.Fun, "github.com/spf13/viper", "Set") || len(set.Args) != 2 ||
		nameSettingKey(set.Args[0]) != w.Target || w.Target != p.Actual || p.ActualAt != w.WriteAt ||
		!sameNameExpression(set.Args[1], w.Value) || !nameSettingStatement(fn, set) {
		return fmt.Errorf("proof.setting: укажите прямой вызов записи Viper в подтесте с точными ключом и значением")
	}
	if e.Result == "checked" && len(nameProofObjects(p)) > 0 && !sameObjectName(nameProofObjects(p), identifierWords(w.Target)) {
		return fmt.Errorf("proof.setting: записанный ключ %q не подтверждает объект в имени", w.Target)
	}
	use, err := g.nameCallExpression(fn, proof.Use.At, proof.Use.Expression)
	if err != nil || !nameSettingStatement(fn, use) || use.Pos() <= set.End() {
		return fmt.Errorf("proof.setting.use: укажите вызов проверяемого кода после записи в том же подтесте")
	}
	for node := range ast.Preorder(fn.node.Body) {
		if call, ok := node.(*ast.CallExpr); ok && call.Pos() > set.End() && call.Pos() < use.Pos() &&
			(g.namePackageSelector(fn, call.Fun, "github.com/spf13/viper", "Set") ||
				g.namePackageSelector(fn, call.Fun, "github.com/spf13/viper", "Reset")) {
			return fmt.Errorf("proof.setting.use: состояние изменилось между записью и проверяемым вызовом")
		}
	}
	reader, err := g.declaration(proof.Use.Declaration)
	if err != nil || reader.base != fn.base || !g.nameSettingConsumer(fn, reader, use) {
		return fmt.Errorf("proof.setting.use.declaration: укажите реализацию вызванной функции или метода")
	}
	get, _, err := g.call(reader, proof.ReadAt, func(x ast.Expr) bool {
		return g.namePackageSelector(reader, x, "github.com/spf13/viper", "GetStringMap")
	})
	if err != nil || len(get.Args) != 1 || nameSettingKey(get.Args[0]) != w.Target {
		return fmt.Errorf("proof.setting.read_at: укажите чтение того же ключа %q в вызванной реализации", w.Target)
	}
	return checkNameWriteWordLinks(p.Proof.Words, nameWriteLines(e, map[string]bool{
		w.WriteAt: true, proof.SubtestAt: true, proof.Use.At: true, proof.ReadAt: true,
	}))
}

func (g *queryGoSource) checkNameEnvironmentSetting(e Evidence, fn queryFunc) error {
	p, w := e.ObjectComparison, e.WriteEvidence
	proof := p.Proof.Setting
	if !strings.HasPrefix(fn.node.Name.Name, "Test") {
		return fmt.Errorf("proof.setting: требуется тестовая функция")
	}
	set, err := g.nameCallExpression(fn, w.WriteAt, p.Proof.Expression)
	if err != nil {
		return fmt.Errorf("proof.setting: вызов t.Setenv: %w", err)
	}
	if len(set.Args) != 2 || nameSettingKey(set.Args[0]) != w.Target || w.Target != p.Actual || p.ActualAt != w.WriteAt || !sameNameExpression(set.Args[1], w.Value) || !nameSettingStatement(fn, set) {
		return fmt.Errorf("proof.setting: укажите прямой вызов t.Setenv с точными ключом и значением")
	}
	sel, ok := set.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Setenv" {
		return fmt.Errorf("proof.setting: требуется t.Setenv")
	}
	receiver, ok := sel.X.(*ast.Ident)
	if !ok || receiver.Obj == nil {
		return fmt.Errorf("proof.setting: не найден параметр теста")
	}
	validReceiver := false
	for _, field := range fn.node.Type.Params.List {
		if typ, ok := field.Type.(*ast.StarExpr); ok && g.namePackageSelector(fn, typ.X, "testing", "T") && nameHasParameter(field, nameIdentifier(sel.X)) {
			for _, param := range field.Names {
				if param.Obj == receiver.Obj {
					validReceiver = true
				}
			}
		}
	}
	if !validReceiver {
		return fmt.Errorf("proof.setting: t.Setenv должен вызываться на параметре *testing.T")
	}
	if e.Result == "checked" && len(nameProofObjects(p)) > 0 && !sameObjectName(nameProofObjects(p), identifierWords(w.Target)) {
		return fmt.Errorf("proof.setting: записанный ключ не подтверждает объект в имени")
	}
	use, err := g.nameCallExpression(fn, proof.Use.At, proof.Use.Expression)
	if err != nil {
		return fmt.Errorf("proof.setting.use: вызов после записи: %w", err)
	}
	if !nameEnvironmentUse(fn, set, use, receiver.Obj, w.Target) {
		return fmt.Errorf("proof.setting.use: укажите проверку результата после записи без неподтверждённых действий между ними")
	}
	reader, err := g.declaration(proof.Use.Declaration)
	if err != nil {
		return fmt.Errorf("proof.setting.use.declaration: реализация вызванной функции: %w", err)
	}
	if reader.base != fn.base || !g.nameSettingConsumer(fn, reader, use) {
		return fmt.Errorf("proof.setting.use.declaration: укажите реализацию вызванной функции")
	}
	get, _, err := g.call(reader, proof.ReadAt, func(x ast.Expr) bool { return g.namePackageSelector(reader, x, "os", "Getenv") })
	if err != nil {
		return fmt.Errorf("proof.setting.read_at: чтение окружения: %w", err)
	}
	if len(get.Args) != 1 || nameSettingKey(get.Args[0]) != w.Target || !g.nameEnvironmentReturn(reader, get, fn, set, use) {
		return fmt.Errorf("proof.setting.read_at: укажите возвращаемое чтение того же ключа")
	}
	return checkNameWriteWordLinks(p.Proof.Words, nameWriteLines(e, map[string]bool{w.WriteAt: true, proof.SubtestAt: true, proof.Use.At: true, proof.ReadAt: true}))
}

// Only simple assignments and direct Setenv calls are allowed between the
// selected write and its assertion. Earlier failure-only checks of the same
// call can precede the selected write.
func nameEnvironmentUse(fn queryFunc, set, use *ast.CallExpr, tester *ast.Object, key string) bool {
	written := false
	for _, stmt := range fn.node.Body.List {
		switch stmt := stmt.(type) {
		case *ast.IfStmt:
			asserted := nameEnvironmentAssertion(stmt, tester)
			if written {
				return asserted == use
			}
			if asserted != nil && stmt.Else == nil && len(stmt.Body.List) == 1 && sameNameExpression(asserted, nameExpression(use)) {
				continue
			}
		case *ast.AssignStmt:
			if nameIndependentAssignment(stmt, tester) {
				continue
			}
		case *ast.ExprStmt:
			if stmt.X == set {
				written = true
				continue
			}
			call, ok := stmt.X.(*ast.CallExpr)
			if ok && len(call.Args) == 2 && nameTestingMethod(call, tester) == "Setenv" {
				otherKey := nameSettingKey(call.Args[0])
				value, literal := call.Args[1].(*ast.BasicLit)
				if otherKey != "" && (!written || otherKey != key) && literal && value.Kind == token.STRING {
					continue
				}
			}
		}
		return false
	}
	return false
}

func nameEnvironmentAssertion(cond *ast.IfStmt, tester *ast.Object) *ast.CallExpr {
	init, ok := cond.Init.(*ast.AssignStmt)
	if !ok || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
		return nil
	}
	used, ok := init.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := init.Lhs[0].(*ast.Ident)
	if !ok || id.Name == "_" || id.Obj == nil {
		return nil
	}
	comparison, ok := cond.Cond.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	var compared ast.Expr
	switch {
	case nameBinding(comparison.X, id.Obj):
		compared = comparison.Y
	case nameBinding(comparison.Y, id.Obj):
		compared = comparison.X
	}
	literal, ok := compared.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return nil
	}
	failure := cond.Body
	switch comparison.Op {
	case token.EQL:
		failure, ok = cond.Else.(*ast.BlockStmt)
		if !ok {
			return nil
		}
	case token.NEQ:
	default:
		return nil
	}
	if len(failure.List) == 0 {
		return nil
	}
	stmt, ok := failure.List[0].(*ast.ExprStmt)
	if !ok {
		return nil
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	switch nameTestingMethod(call, tester) {
	case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
		return used
	}
	return nil
}

func nameBinding(expr ast.Expr, binding *ast.Object) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Obj == binding
}

func nameTestingMethod(call *ast.CallExpr, tester *ast.Object) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if ok && nameBinding(sel.X, tester) {
		return sel.Sel.Name
	}
	return ""
}

// nameEnvironmentReturn supports a direct return or TrimSpace of the value.
// Other transformations require an explicit proof instead of guessing flow.
func (g *queryGoSource) nameEnvironmentReturn(fn queryFunc, get *ast.CallExpr, test queryFunc, set, use *ast.CallExpr) bool {
	for _, stmt := range fn.node.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			guard, ok := stmt.(*ast.IfStmt)
			if ok && guard.Else == nil {
				if c, ok := guard.Cond.(*ast.Ident); ok && c.Name == "false" && c.Obj == nil {
					continue
				}
			}
			if !ok || !g.nameEnvironmentEmptyGuard(fn, guard, test, set, use) {
				return false
			}
			continue
		}
		if len(ret.Results) != 1 {
			return false
		}
		if ret.Results[0] == get {
			return true
		}
		if wrap, ok := ret.Results[0].(*ast.CallExpr); ok && len(wrap.Args) == 1 && wrap.Args[0] == get && g.namePackageSelector(fn, wrap.Fun, "strings", "TrimSpace") {
			return true
		}
	}
	return false
}

// nameEnvironmentEmptyGuard proves that a preceding early return cannot run
// for this test: its other environment key was explicitly set to empty.
func (g *queryGoSource) nameEnvironmentEmptyGuard(fn queryFunc, guard *ast.IfStmt, test queryFunc, set, use *ast.CallExpr) bool {
	init, ok := guard.Init.(*ast.AssignStmt)
	if !ok || guard.Else != nil || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
		return false
	}
	id, ok := init.Lhs[0].(*ast.Ident)
	if !ok || id.Obj == nil {
		return false
	}
	wrap, ok := init.Rhs[0].(*ast.CallExpr)
	if !ok || len(wrap.Args) != 1 || !g.namePackageSelector(fn, wrap.Fun, "strings", "TrimSpace") {
		return false
	}
	read, ok := wrap.Args[0].(*ast.CallExpr)
	if !ok || len(read.Args) != 1 || !g.namePackageSelector(fn, read.Fun, "os", "Getenv") {
		return false
	}
	key := nameSettingKey(read.Args[0])
	if key == "" {
		ident, ok := read.Args[0].(*ast.Ident)
		if !ok || ident.Obj == nil || ident.Obj.Kind != ast.Con {
			return false
		}
		spec, ok := ident.Obj.Decl.(*ast.ValueSpec)
		if !ok || len(spec.Values) != 1 {
			return false
		}
		key = nameSettingKey(spec.Values[0])
	}
	if key == "" {
		return false
	}
	cond, ok := guard.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return false
	}
	lhs, ok := cond.X.(*ast.Ident)
	if !ok || lhs.Obj != id.Obj || nameSettingKey(cond.Y) != "" {
		return false
	}
	lit, ok := cond.Y.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	setter, named := set.Fun.(*ast.SelectorExpr)
	if !named {
		return false
	}
	tester, named := setter.X.(*ast.Ident)
	if !named || tester.Obj == nil {
		return false
	}
	var emptyAt token.Pos
	for _, stmt := range test.node.Body.List {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := expr.X.(*ast.CallExpr)
		if !ok || call.Pos() >= set.Pos() || len(call.Args) != 2 {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Setenv" {
			continue
		}
		arg, ok := call.Args[1].(*ast.BasicLit)
		receiver, same := sel.X.(*ast.Ident)
		if ok && arg.Kind == token.STRING && same && receiver.Obj == tester.Obj &&
			nameSettingKey(arg) == "" && nameSettingKey(call.Args[0]) == key {
			emptyAt = call.Pos()
		}
	}
	if emptyAt == token.NoPos {
		return false
	}
	for _, stmt := range test.node.Body.List {
		if stmt.Pos() <= emptyAt || stmt.End() > use.Pos() {
			continue
		}
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			return false
		}
		call, ok := expr.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 || nameSettingKey(call.Args[0]) == "" || nameSettingKey(call.Args[0]) == key {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Setenv" {
			return false
		}
		receiver, ok := sel.X.(*ast.Ident)
		value, literal := call.Args[1].(*ast.BasicLit)
		if !ok || receiver.Obj != tester.Obj || !literal || value.Kind != token.STRING {
			return false
		}
	}
	return true
}

func nameHasParameter(field *ast.Field, name string) bool {
	for _, p := range field.Names {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (g *queryGoSource) namePackageSelector(fn queryFunc, expr ast.Expr, pkg, method string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != method {
		return false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	return ok && qualifier.Obj == nil && g.importPath(fn, qualifier.Name) == pkg
}

func nameSettingKey(expr ast.Expr) string {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		value, _ := strconv.Unquote(lit.Value)
		return value
	}
	return ""
}

func nameSettingStatement(fn queryFunc, call *ast.CallExpr) bool {
	for _, stmt := range fn.node.Body.List {
		switch stmt.(type) {
		case *ast.ExprStmt, *ast.AssignStmt:
			if stmt.Pos() <= call.Pos() && call.End() <= stmt.End() {
				return true
			}
		}
	}
	return false
}

func (g *queryGoSource) nameSettingConsumer(from, to queryFunc, call *ast.CallExpr) bool {
	if calledName(call.Fun) != to.node.Name.Name {
		return false
	}
	sel, method := call.Fun.(*ast.SelectorExpr)
	if !method {
		return g.receiver(from, to, call, "") == nil
	}
	receiver := nameIdentifier(sel.X)
	if receiver == "" || recvType(to.node) == "" {
		return false
	}
	for _, stmt := range from.node.Body.List {
		binding, ok := stmt.(*ast.AssignStmt)
		if !ok || binding.Tok != token.DEFINE || len(binding.Lhs) != 1 || len(binding.Rhs) != 1 ||
			nameIdentifier(binding.Lhs[0]) != receiver {
			continue
		}
		rhs := binding.Rhs[0]
		if ref, ok := rhs.(*ast.UnaryExpr); ok && ref.Op == token.AND {
			rhs = ref.X
		}
		literal, ok := rhs.(*ast.CompositeLit)
		if ok && g.typeMatches(from, literal.Type, path.Dir(to.file), recvType(to.node)) &&
			nameValueUnchangedWithin(from.node.Body, binding, call, receiver) {
			return true
		}
	}
	return false
}

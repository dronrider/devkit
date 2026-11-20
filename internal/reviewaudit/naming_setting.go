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
	if err != nil || len(set.Args) != 2 || nameSettingKey(set.Args[0]) != w.Target || w.Target != p.Actual || p.ActualAt != w.WriteAt || !sameNameExpression(set.Args[1], w.Value) || !nameSettingStatement(fn, set) {
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
	if err != nil || use.Pos() <= set.End() || !nameEnvironmentUse(fn, use) {
		return fmt.Errorf("proof.setting.use: укажите проверку результата вызова после записи")
	}
	for node := range ast.Preorder(fn.node.Body) {
		if stmt, ok := node.(*ast.AssignStmt); ok && stmt.Pos() < set.Pos() {
			for _, target := range stmt.Lhs {
				if id, ok := target.(*ast.Ident); ok && id.Obj == receiver.Obj {
					return fmt.Errorf("proof.setting: параметр теста был изменён")
				}
			}
		}
		if call, ok := node.(*ast.CallExpr); ok && call.Pos() > set.End() && call.Pos() < use.Pos() {
			if s, ok := call.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Setenv" && nameIdentifier(s.X) == nameIdentifier(sel.X) && len(call.Args) > 0 && nameSettingKey(call.Args[0]) == w.Target {
				return fmt.Errorf("proof.setting.use: ключ изменился до вызова")
			}
			if (g.namePackageSelector(fn, call.Fun, "os", "Setenv") || g.namePackageSelector(fn, call.Fun, "os", "Unsetenv")) && len(call.Args) > 0 && nameSettingKey(call.Args[0]) == w.Target {
				return fmt.Errorf("proof.setting.use: ключ изменился до вызова")
			}
		}
	}
	reader, err := g.declaration(proof.Use.Declaration)
	if err != nil || reader.base != fn.base || !g.nameSettingConsumer(fn, reader, use) {
		return fmt.Errorf("proof.setting.use.declaration: укажите реализацию вызванной функции")
	}
	get, _, err := g.call(reader, proof.ReadAt, func(x ast.Expr) bool { return g.namePackageSelector(reader, x, "os", "Getenv") })
	if err != nil || len(get.Args) != 1 || nameSettingKey(get.Args[0]) != w.Target || !g.nameEnvironmentReturn(reader, get) {
		return fmt.Errorf("proof.setting.read_at: укажите возвращаемое чтение того же ключа")
	}
	return checkNameWriteWordLinks(p.Proof.Words, nameWriteLines(e, map[string]bool{w.WriteAt: true, proof.SubtestAt: true, proof.Use.At: true, proof.ReadAt: true}))
}

// nameEnvironmentUse accepts a top-level test condition that checks the
// returned value; calls inside unrelated or unreachable blocks prove nothing.
func nameEnvironmentUse(fn queryFunc, call *ast.CallExpr) bool {
	for _, stmt := range fn.node.Body.List {
		cond, ok := stmt.(*ast.IfStmt)
		if !ok {
			continue
		}
		init, ok := cond.Init.(*ast.AssignStmt)
		if !ok || len(init.Lhs) != 1 || len(init.Rhs) != 1 || init.Rhs[0] != call {
			continue
		}
		id, ok := init.Lhs[0].(*ast.Ident)
		if !ok || id.Name == "_" || id.Obj == nil {
			continue
		}
		for node := range ast.Preorder(cond.Cond) {
			if read, ok := node.(*ast.Ident); ok && read.Obj == id.Obj {
				return true
			}
		}
	}
	return false
}

// nameEnvironmentReturn supports a direct return or TrimSpace of the value.
// Other transformations require an explicit proof instead of guessing flow.
func (g *queryGoSource) nameEnvironmentReturn(fn queryFunc, get *ast.CallExpr) bool {
	for _, stmt := range fn.node.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
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
	return ok && sel.Sel.Name == method && g.importPath(fn, nameIdentifier(sel.X)) == pkg
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

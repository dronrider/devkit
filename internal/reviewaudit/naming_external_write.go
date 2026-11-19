package reviewaudit

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type NameAPIWrite struct {
	Method       string `json:"method"`
	Route        string `json:"route"`
	RouteAt      string `json:"route_at"`
	ObjectAt     string `json:"object_at"`
	JSONKey      string `json:"json_key"`
	RequestKey   string `json:"request_key"`
	RequestValue string `json:"request_value"`
	DecodeAt     string `json:"decode_at"`
	Decode       string `json:"decode"`
	Response     string `json:"response"`
	CheckAt      string `json:"check_at"`
	Check        string `json:"check"`
	ID           string `json:"id"`
}

type NameCLIWrite struct {
	Command       []string      `json:"command"`
	Destination   string        `json:"destination"`
	SuccessAt     string        `json:"success_at"`
	Success       string        `json:"success"`
	SuccessCodeAt string        `json:"success_code_at,omitempty"`
	ReadAt        string        `json:"read_at"`
	Read          string        `json:"read"`
	CountAt       string        `json:"count_at,omitempty"`
	Count         string        `json:"count,omitempty"`
	ArtifactAt    string        `json:"artifact_at"`
	Artifact      string        `json:"artifact"`
	Key           string        `json:"key"`
	ContentAt     string        `json:"content_at"`
	Content       string        `json:"content"`
	ContentChecks []NameCodeUse `json:"content_checks"`
}

func (g *queryGoSource) checkNameAPI(e Evidence, fn queryFunc) error {
	pair, p := e.ObjectComparison, e.ObjectComparison.Proof
	a := p.API
	if p.Type != "" {
		return fmt.Errorf("proof.api: укажите объект из ответа")
	}
	send, err := g.nameCallExpression(fn, e.WriteEvidence.WriteAt, p.Expression)
	if err != nil {
		return fmt.Errorf("proof.api: укажите строку вызова запроса: %w", err)
	}
	if a.Method == "" || a.Route == "" || !slices.ContainsFunc(send.Args, func(x ast.Expr) bool {
		return nameString(x) == a.Method || g.nameHTTPMethod(fn, x) == a.Method
	}) || !slices.ContainsFunc(send.Args, func(x ast.Expr) bool { return nameExprIn(x, strconv.Quote(a.Route)) }) {
		return fmt.Errorf("proof.api: метод и маршрут должны быть аргументами запроса")
	}
	requestField := nameRequestField(send, a.RequestKey, a.RequestValue)
	if a.RequestKey == "" || a.RequestValue == "" || requestField == nil {
		return fmt.Errorf("proof.api: покажите значение поля %q в отправляемом теле", a.RequestKey)
	}
	if err := g.checkNameAPISchema(fn, a); err != nil {
		return err
	}
	decode, err := g.nameCallExpression(fn, a.DecodeAt, a.Decode)
	if err != nil {
		return fmt.Errorf("proof.api: укажите декодирование ответа: %w", err)
	}
	if !g.nameCallResultUsed(fn, send, decode) || !slices.ContainsFunc(decode.Args, func(x ast.Expr) bool {
		ref, ok := x.(*ast.UnaryExpr)
		return ok && ref.Op.String() == "&" && nameIdentifier(ref.X) == a.Response
	}) {
		return fmt.Errorf("proof.api: декодируйте ответ этого же запроса в %s", a.Response)
	}
	if err := g.checkNameResponseObject(fn, decode, a, pair); err != nil {
		return err
	}
	check, err := g.nameCallExpression(fn, a.CheckAt, a.Check)
	if err != nil {
		return fmt.Errorf("proof.api: укажите проверку ID созданного объекта: %w", err)
	}
	idExpr, err := parser.ParseExpr(a.ID)
	id, ok := idExpr.(*ast.SelectorExpr)
	if err != nil || !ok || !sameNameExpression(id.X, a.Response+"."+pair.Actual) || decode.Pos() >= check.Pos() ||
		!slices.ContainsFunc(check.Args, func(x ast.Expr) bool { return sameNameExpression(x, a.ID) }) {
		return fmt.Errorf("proof.api: покажите проверку ID объекта из декодированного ответа")
	}
	if !g.nameTestAssertion(fn, check, "NotEmpty") && !g.nameTestAssertion(fn, check, "NotZero") {
		return fmt.Errorf("proof.api: укажите утверждение, проверяющее непустой ID")
	}
	if !g.nameCallChecksValue(fn, decode, check, a.Response) {
		return fmt.Errorf("proof.api: объект ответа изменён между декодированием и проверкой")
	}
	if e.Result == "checked" && !sameObjectName(nameProofObjects(pair), identifierWords(pair.Actual)) {
		return fmt.Errorf("proof.api: объект ответа %q не соответствует %q", pair.Actual, strings.Join(pair.Expected, " "))
	}
	return checkNameWriteWordLinks(p.Words, map[string]bool{e.WriteEvidence.WriteAt: true, a.RouteAt: true, a.ObjectAt: true, pair.ActualAt: true, a.DecodeAt: true, a.CheckAt: true,
		nameRef(fn, g.fset.Position(requestField.Pos()).Line): true})
}

func nameRef(fn queryFunc, line int) string {
	prefix := ""
	if fn.base {
		prefix = "base@"
	}
	return fmt.Sprintf("%s%s:%d", prefix, fn.file, line)
}

func nameString(x ast.Expr) string {
	if literal, ok := x.(*ast.BasicLit); ok {
		value, _ := strconv.Unquote(literal.Value)
		return value
	}
	return ""
}

func (g *queryGoSource) nameHTTPMethod(fn queryFunc, x ast.Expr) string {
	s, ok := x.(*ast.SelectorExpr)
	if ok && g.importPath(fn, nameIdentifier(s.X)) == "net/http" && strings.HasPrefix(s.Sel.Name, "Method") {
		return strings.ToUpper(strings.TrimPrefix(s.Sel.Name, "Method"))
	}
	return ""
}

func nameRequestField(send *ast.CallExpr, key, value string) *ast.KeyValueExpr {
	for _, arg := range send.Args {
		if body, ok := arg.(*ast.CompositeLit); ok {
			for _, item := range body.Elts {
				if kv, ok := item.(*ast.KeyValueExpr); ok && nameString(kv.Key) == key && sameNameExpression(kv.Value, value) {
					return kv
				}
			}
		}
	}
	return nil
}

func (g *queryGoSource) checkNameAPISchema(fn queryFunc, a *NameAPIWrite) error {
	file, _, base, err := evidenceLocation(a.RouteAt)
	if err != nil || base != fn.base || !slices.Contains([]string{".yaml", ".yml"}, path.Ext(file)) {
		return fmt.Errorf("proof.api.route_at: укажите маршрут в YAML-контракте той же ревизии")
	}
	route, err := g.source.line(a.RouteAt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(route) != a.Route+":" {
		return fmt.Errorf("proof.api.route_at: укажите объявление маршрута %s", a.Route)
	}
	body, err := g.source.read(file, base)
	if err != nil {
		return err
	}
	_, line, _, _ := evidenceLocation(a.RouteAt)
	lines := strings.Split(body, "\n")
	method := strings.ToLower(a.Method) + ":"
	methodLine := nameYAMLChild(lines, line-1, method)
	if methodLine < 0 {
		return fmt.Errorf("proof.api.route_at: маршрут %s не объявляет %s", a.Route, a.Method)
	}
	objectFile, objectLine, objectBase, _ := evidenceLocation(a.ObjectAt)
	object, err := g.source.line(a.ObjectAt)
	if err != nil {
		return err
	}
	if a.JSONKey == "" || objectFile != file || objectBase != base || !strings.HasPrefix(strings.TrimSpace(object), a.JSONKey+":") {
		return fmt.Errorf("proof.api.object_at: укажите поле %q контракта ответа", a.JSONKey)
	}
	if !nameAPIResponseHasField(lines, methodLine, objectLine-1) {
		return fmt.Errorf("proof.api.object_at: поле должно принадлежать схеме успешного ответа этого маршрута")
	}
	return nil
}

func nameIndent(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func nameYAMLBlockEnd(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "#") && nameIndent(lines[i]) <= nameIndent(lines[start]) {
			return i
		}
	}
	return len(lines)
}

func nameYAMLChild(lines []string, start int, key string) int {
	end := nameYAMLBlockEnd(lines, start)
	for i := start + 1; i < end; {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			i++
			continue
		}
		if line == key {
			return i
		}
		i = nameYAMLBlockEnd(lines, i)
	}
	return -1
}

var nameAPISchemaRef = regexp.MustCompile(`\$ref:\s*['"]?#/components/schemas/([a-zA-Z0-9_.-]+)`)
var nameAPISuccess = regexp.MustCompile(`^['"]?2[0-9][0-9]['"]?:`)

func nameAPIResponseHasField(lines []string, method, field int) bool {
	responses := nameYAMLChild(lines, method, "responses:")
	if responses < 0 {
		return false
	}
	schemas := -1
	for i, line := range lines {
		if line == "components:" {
			schemas = nameYAMLChild(lines, i, "schemas:")
			break
		}
	}
	var queue []int
	for i := responses + 1; i < nameYAMLBlockEnd(lines, responses); i++ {
		if nameAPISuccess.MatchString(strings.TrimSpace(lines[i])) {
			queue = append(queue, i)
		}
	}
	seen := map[int]bool{}
	for len(queue) > 0 {
		start := queue[0]
		queue = queue[1:]
		if seen[start] {
			continue
		}
		seen[start] = true
		end := nameYAMLBlockEnd(lines, start)
		if field > start && field < end {
			return true
		}
		if schemas >= 0 {
			for _, line := range lines[start:end] {
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				if m := nameAPISchemaRef.FindStringSubmatch(line); m != nil {
					if target := nameYAMLChild(lines, schemas, m[1]+":"); target >= 0 {
						queue = append(queue, target)
					}
				}
			}
		}
	}
	return false
}

func (g *queryGoSource) nameTestAssertion(fn queryFunc, call *ast.CallExpr, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	return slices.Contains([]string{"github.com/stretchr/testify/require", "github.com/stretchr/testify/assert"}, g.importPath(fn, nameIdentifier(selector.X)))
}

func (g *queryGoSource) checkNameResponseObject(fn queryFunc, decode *ast.CallExpr, a *NameAPIWrite, pair *NameObjectComparison) error {
	_, err := g.nameNodeAt(fn, pair.ActualAt, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || field.Tag == nil || field.Pos() >= decode.Pos() || !slices.ContainsFunc(field.Names, func(id *ast.Ident) bool { return id.Name == pair.Actual }) {
			return false
		}
		tag, _ := strconv.Unquote(field.Tag.Value)
		if strings.Split(reflect.StructTag(tag).Get("json"), ",")[0] != a.JSONKey {
			return false
		}
		for node := range ast.Preorder(fn.node.Body) {
			if decl, ok := node.(*ast.ValueSpec); ok && decl.Pos() < field.Pos() && field.End() <= decl.End() &&
				slices.ContainsFunc(decl.Names, func(id *ast.Ident) bool { return id.Name == a.Response }) {
				return true
			}
		}
		return false
	})
	if err != nil {
		return fmt.Errorf("proof.api: actual должен объявлять объект %q ответа %s: %w", a.JSONKey, a.Response, err)
	}
	return nil
}

func (g *queryGoSource) nameCallResultUsed(fn queryFunc, producer, consumer *ast.CallExpr) bool {
	return g.nameCallResultUsedIn(fn, producer, consumer, consumer.Args)
}

func (g *queryGoSource) nameCallResultUsedIn(fn queryFunc, producer, consumer *ast.CallExpr, args []ast.Expr) bool {
	for n := range ast.Preorder(fn.node.Body) {
		bind, ok := n.(*ast.AssignStmt)
		if !ok || len(bind.Rhs) != 1 || len(bind.Lhs) == 0 || bind.Rhs[0] != producer || len(bind.Lhs) != 1 {
			continue
		}
		name := nameIdentifier(bind.Lhs[0])
		if name != "" && slices.ContainsFunc(args, func(x ast.Expr) bool { return nameIdentifier(x) == name }) && g.nameCallChecksValue(fn, producer, consumer, name) {
			return true
		}
	}
	return false
}

func (g *queryGoSource) nameCallChecksValue(fn queryFunc, from, to ast.Node, value string) bool {
	return from.Pos() < to.Pos() && nameValueUnchanged(fn.node.Body, from, to, value)
}

func (g *queryGoSource) checkNameCLI(e Evidence, fn queryFunc) error {
	pair, p := e.ObjectComparison, e.ObjectComparison.Proof
	c := p.CLI
	if p.Type != "" {
		return fmt.Errorf("proof.cli: укажите артефакт из каталога назначения")
	}
	run, err := g.nameCallExpression(fn, e.WriteEvidence.WriteAt, p.Expression)
	if err != nil {
		return fmt.Errorf("proof.cli: укажите запуск команды: %w", err)
	}
	var issues []error
	command := findNameCommand(c.Command, run.Args)
	if command < 0 {
		issues = append(issues, fmt.Errorf("proof.cli.command: укажите подряд буквальные слова подкоманды без флагов и каталога"))
	}
	args := run.Args
	if command >= 0 {
		args = args[command+len(c.Command):]
	}
	if c.Destination == "" || !slices.ContainsFunc(args, func(x ast.Expr) bool { return sameNameExpression(x, c.Destination) }) {
		issues = append(issues, fmt.Errorf("proof.cli.destination: укажите выражение аргумента каталога назначения в вызове"))
	}

	success, err := g.nameCallExpression(fn, c.SuccessAt, c.Success)
	if err != nil {
		issues = append(issues, fmt.Errorf("proof.cli.success: укажите полный вызов проверки кода завершения в success_at: %w", err))
	} else {
		if !g.nameCallResultUsed(fn, run, success) {
			issues = append(issues, fmt.Errorf("proof.cli.success: проверка завершения должна использовать результат этого запуска"))
		}
		if !g.nameHasZeroArgument(fn, success, c.SuccessCodeAt) {
			issues = append(issues, fmt.Errorf("proof.cli.success_code_at: покажите нулевой код завершения или объявление константы"))
		}
	}
	read, err := g.nameCallExpression(fn, c.ReadAt, c.Read)
	if err != nil {
		issues = append(issues, fmt.Errorf("proof.cli.read: укажите вызов чтения каталога в read_at: %w", err))
	} else if !slices.ContainsFunc(read.Args, func(x ast.Expr) bool { return sameNameExpression(x, c.Destination) }) || (success != nil && read.Pos() <= success.Pos()) {
		issues = append(issues, fmt.Errorf("proof.cli.read: покажите чтение каталога, переданного команде, после проверки завершения"))
	} else if dest, _ := parser.ParseExpr(c.Destination); nameIdentifier(dest) != "" && !g.nameCallChecksValue(fn, run, read, c.Destination) {
		issues = append(issues, fmt.Errorf("proof.cli.destination: каталог назначения изменился после запуска"))
	}

	lines := map[string]bool{e.WriteEvidence.WriteAt: true, c.SuccessAt: true, c.ReadAt: true, c.ArtifactAt: true, c.ContentAt: true}
	if slices.Contains(identifierWords(fn.node.Name.Name), "Single") || c.CountAt != "" || c.Count != "" {
		if c.CountAt == "" || c.Count == "" {
			issues = append(issues, fmt.Errorf("proof.cli.count: укажите полный вызов проверки одного артефакта и его строку в count_at"))
		} else {
			count, err := g.nameCallExpression(fn, c.CountAt, c.Count)
			switch {
			case err != nil:
				issues = append(issues, fmt.Errorf("proof.cli.count: укажите вызов проверки количества в count_at: %w", err))
			case !g.nameTestAssertion(fn, count, "Len") || len(count.Args) < 3 || !sameNameExpression(count.Args[2], "1") ||
				(read != nil && !g.nameCallResultUsedIn(fn, read, count, count.Args[1:2])):
				issues = append(issues, fmt.Errorf("proof.cli.count: покажите проверку одного артефакта в прочитанном каталоге"))
			case read != nil:
				lines[c.CountAt] = true
			}
		}
	}

	binding, artifactErr := g.nameCLIArtifact(fn, c, pair, read)
	issues = append(issues, artifactErr)
	content, err := g.nameCallExpression(fn, c.ContentAt, c.Content)
	if err != nil {
		issues = append(issues, fmt.Errorf("proof.cli.content: укажите полный вызов проверки содержимого в content_at: %w", err))
	} else {
		if !nameReadsArtifact(content, c.Artifact) || (binding != nil && !g.nameCallChecksValue(fn, binding, content, c.Artifact)) {
			if artifactErr == nil {
				issues = append(issues, fmt.Errorf("proof.cli.content: проверка содержимого должна использовать выбранный артефакт"))
			}
		}
		issues = append(issues, g.checkNameCLIContent(fn, content, c.ContentChecks, lines))
	}
	if err := errors.Join(issues...); err != nil {
		return err
	}
	return checkNameWriteWordLinks(p.Words, nameWriteLines(e, lines))
}

func (g *queryGoSource) nameCLIArtifact(fn queryFunc, c *NameCLIWrite, pair *NameObjectComparison, read *ast.CallExpr) (*ast.AssignStmt, error) {
	node, err := g.nameNodeAt(fn, c.ArtifactAt, func(n ast.Node) bool {
		s, ok := n.(*ast.AssignStmt)
		return ok && len(s.Lhs) > 0 && len(s.Rhs) == 1 && nameIdentifier(s.Lhs[0]) == c.Artifact
	})
	if err != nil {
		return nil, fmt.Errorf("proof.cli.artifact: укажите имя переменной слева от присваивания в artifact_at: %w", err)
	}
	binding := node.(*ast.AssignStmt)
	index, ok := binding.Rhs[0].(*ast.IndexExpr)
	if !ok || pair.Actual != c.Artifact || pair.ActualAt != c.ArtifactAt || (read != nil && !g.nameReadResultIndexed(fn, read, index)) {
		return nil, fmt.Errorf("proof.cli.artifact: артефакт должен выбираться из результата чтения каталога")
	}
	if !sameNameExpression(index.Index, c.Key) {
		return nil, fmt.Errorf("proof.cli.key: укажите полное выражение внутри скобок выбора артефакта по ожидаемому имени")
	}
	return binding, nil
}

func (g *queryGoSource) checkNameCLIContent(fn queryFunc, content *ast.CallExpr, checks []NameCodeUse, lines map[string]bool) error {
	if len(checks) == 0 {
		return fmt.Errorf("proof.cli.content_checks: покажите проверки содержимого артефакта")
	}
	var issues []error
	for _, check := range checks {
		helper, err := g.declaration(check.Declaration)
		if err != nil {
			issues = append(issues, fmt.Errorf("proof.cli.content_checks: %w", err))
			continue
		}
		if helper.base != fn.base || !slices.ContainsFunc(slices.Collect(ast.Preorder(content)), func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			return ok && calledName(call.Fun) == helper.node.Name.Name && g.receiver(fn, helper, call, "") == nil
		}) {
			issues = append(issues, fmt.Errorf("proof.cli.content_checks: включите вызов %s в полный вызов проверки содержимого", helper.node.Name.Name))
			continue
		}
		if _, err := g.nameCallExpression(helper, check.At, check.Expression); err != nil {
			issues = append(issues, fmt.Errorf("proof.cli.content_checks: %w", err))
			continue
		}
		lines[check.At] = true
	}
	return errors.Join(issues...)
}

func (g *queryGoSource) nameHasZeroArgument(fn queryFunc, call *ast.CallExpr, at string) bool {
	if at == "" {
		return slices.ContainsFunc(call.Args, func(x ast.Expr) bool { return sameNameExpression(x, "0") })
	}
	file, line, base, err := evidenceLocation(at)
	if err != nil || base != fn.base {
		return false
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return false
	}
	for _, decl := range tree.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value := spec.(*ast.ValueSpec)
			if g.fset.Position(value.Pos()).Line != line {
				continue
			}
			for i, name := range value.Names {
				if i >= len(value.Values) || !sameNameExpression(value.Values[i], "0") {
					continue
				}
				if slices.ContainsFunc(call.Args, func(x ast.Expr) bool {
					if s, ok := x.(*ast.SelectorExpr); ok {
						return s.Sel.Name == name.Name && matchesImport(g.importPath(fn, nameIdentifier(s.X)), path.Dir(file))
					}
					return path.Dir(fn.file) == path.Dir(file) && nameIdentifier(x) == name.Name
				}) {
					return true
				}
			}
		}
	}
	return false
}

func findNameCommand(command []string, args []ast.Expr) int {
	if len(command) == 0 || len(command) > len(args) {
		return -1
	}
	for offset := 0; offset <= len(args)-len(command); offset++ {
		matches := true
		for i, word := range command {
			if word == "" || strings.HasPrefix(word, "-") || nameString(args[offset+i]) != word {
				matches = false
				break
			}
		}
		if matches {
			return offset
		}
	}
	return -1
}

func (g *queryGoSource) nameReadResultIndexed(fn queryFunc, read *ast.CallExpr, index *ast.IndexExpr) bool {
	for n := range ast.Preorder(fn.node.Body) {
		s, ok := n.(*ast.AssignStmt)
		if ok && len(s.Rhs) == 1 && len(s.Lhs) == 1 && s.Rhs[0] == read && nameIdentifier(s.Lhs[0]) != "" && nameIdentifier(s.Lhs[0]) == nameIdentifier(index.X) {
			return g.nameCallChecksValue(fn, read, index, nameIdentifier(index.X))
		}
	}
	return false
}

func nameReadsArtifact(call *ast.CallExpr, artifact string) bool {
	for n := range ast.Preorder(call) {
		if s, ok := n.(*ast.SelectorExpr); ok && nameIdentifier(s.X) == artifact {
			return true
		}
	}
	return false
}

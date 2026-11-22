package reviewaudit

import (
	"fmt"
	"path"
	"strings"
)

type pythonResultRequest struct {
	Source       string             `json:"source"`
	CallerSource string             `json:"caller_source,omitempty"`
	Import       string             `json:"import,omitempty"`
	Symbol       string             `json:"symbol"`
	Declaration  int                `json:"declaration"`
	ReturnLine   int                `json:"return_line"`
	Caller       int                `json:"caller"`
	CallLine     int                `json:"call_line"`
	UseLine      int                `json:"use_line"`
	Actual       string             `json:"actual"`
	Expression   string             `json:"expression"`
	Value        string             `json:"value"`
	Use          string             `json:"use"`
	Words        []pythonResultWord `json:"words"`
}

type pythonResultWord struct {
	Line  int    `json:"line"`
	Value string `json:"value"`
}

func isPythonNameResult(e Evidence, declaration string) bool {
	file, _, _, err := evidenceLocation(declaration)
	return err == nil && path.Ext(file) == ".py" && e.Check == "result" &&
		e.ResultEvidence != nil && e.ObjectComparison != nil && e.ObjectComparison.Proof != nil
}

func checkNamePythonResult(e Evidence, declaration string, source *evidenceSource) error {
	pair, result := e.ObjectComparison, e.ResultEvidence
	proof, flow := pair.Proof, result.Flow
	if flow == nil || flow.UseExpression == "" ||
		flow.ReceiverAt != "" || len(flow.Forward) != 0 || flow.Projection != nil ||
		flow.Consumer != "" || flow.UseKind != "" || flow.UseField != "" {
		return fmt.Errorf("proof: для Python укажите вызов и use_expression в result_evidence.flow")
	}
	if proof.Type != "" || len(proof.Guards) != 0 || proof.Assignment != nil || proof.API != nil ||
		proof.CLI != nil || proof.Reference != nil || proof.SQL != nil || proof.Setting != nil || proof.Effect != nil {
		return fmt.Errorf("proof: для Python укажите expression и words результата без типа, guards и доказательств записи")
	}
	file, declared, base, _ := evidenceLocation(declaration)
	returnFile, returnLine, returnBase, err := evidenceLocation(result.ReturnAt)
	if err != nil || returnFile != file || returnBase != base || pair.ActualAt != result.ReturnAt {
		return fmt.Errorf("return_at и actual_at: укажите строку возврата в файле объявления в той же ревизии")
	}
	callerFile, caller, callerBase, err := evidenceLocation(flow.Caller)
	if err != nil || callerBase != base || path.Ext(callerFile) != ".py" || path.Dir(callerFile) != path.Dir(file) {
		return fmt.Errorf("flow.caller: укажите потребителя в Python-файле из того же каталога и ревизии")
	}
	refs := []string{flow.CallAt, result.ConsumerAt}
	var lines [2]int
	for i, ref := range refs {
		p, line, fromBase, err := evidenceLocation(ref)
		if err != nil || p != callerFile || fromBase != base {
			return fmt.Errorf("proof: укажите вызов и чтение в файле потребителя в той же ревизии")
		}
		lines[i] = line
	}
	words := make([]pythonResultWord, 0, len(proof.Words))
	for _, word := range proof.Words {
		p, line, fromBase, err := evidenceLocation(word.Fact.Location)
		if err != nil || p != file || fromBase != base {
			return fmt.Errorf("proof.words: укажите выражения из возврата Python в той же ревизии")
		}
		words = append(words, pythonResultWord{Line: line, Value: word.Fact.Value})
	}
	body, err := source.read(file, base)
	if err != nil {
		return err
	}
	request := pythonResultRequest{
		Source: body, Symbol: nameObjectSymbol(e.Object), Declaration: declared,
		ReturnLine: returnLine, Caller: caller, CallLine: lines[0], UseLine: lines[1],
		Actual: pair.Actual, Expression: proof.Expression, Value: flow.Value,
		Use: flow.UseExpression, Words: words,
	}
	if callerFile != file {
		request.CallerSource, err = source.read(callerFile, base)
		if err != nil {
			return err
		}
		request.Import = strings.TrimSuffix(path.Base(file), ".py")
	}
	message, err := runNamePythonVerifier[string](pythonResultAST, request)
	if err != nil {
		return fmt.Errorf("proof: %w", err)
	}
	if message != "" {
		return fmt.Errorf("proof: %s", message)
	}
	return nil
}

const pythonResultAST = `
import ast
import json
import operator
import sys

FUNCTIONS = (ast.FunctionDef, ast.AsyncFunctionDef)
SCOPES = (*FUNCTIONS, ast.ClassDef, ast.Lambda)
MATCH = (ast.Match,) if hasattr(ast, "Match") else ()
PATTERN_BINDINGS = (ast.MatchAs, ast.MatchStar, ast.MatchMapping) if MATCH else ()
UNSUPPORTED = (*MATCH, ast.AsyncFor, ast.AsyncWith, getattr(ast, "TryStar", ast.Try))
DYNAMIC_NAMES = {"exec", "eval", "globals", "locals", "vars", "__import__", "setattr", "delattr"}
UNKNOWN = object()
COMPARE = {ast.Eq: operator.eq, ast.NotEq: operator.ne, ast.Lt: operator.lt,
           ast.LtE: operator.le, ast.Gt: operator.gt, ast.GtE: operator.ge}

def shape(node):
    return ast.dump(node)

def expression(text):
    return ast.parse(text, mode="eval").body

def literal(node, values=None):
    if isinstance(node, ast.Name) and values is not None:
        return values.get(node.id, UNKNOWN)
    if any(isinstance(child, ast.Call) for child in ast.walk(node)):
        return UNKNOWN
    try:
        return ast.literal_eval(node)
    except (TypeError, ValueError):
        return UNKNOWN

def truth(node, values=None):
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        value = truth(node.operand, values)
        return None if value is None else not value
    if isinstance(node, ast.BoolOp):
        operands = [truth(value, values) for value in node.values]
        if isinstance(node.op, ast.And):
            return False if False in operands else (True if None not in operands else None)
        return True if True in operands else (False if None not in operands else None)
    if isinstance(node, ast.Compare) and len(node.ops) == 1 and type(node.ops[0]) in COMPARE:
        left, right = literal(node.left, values), literal(node.comparators[0], values)
        if left is not UNKNOWN and right is not UNKNOWN:
            try:
                return COMPARE[type(node.ops[0])](left, right)
            except TypeError:
                return None
    value = literal(node, values)
    return None if value is UNKNOWN else bool(value)

def scope_nodes(body):
    for node in body:
        yield node
        if not isinstance(node, SCOPES):
            yield from scope_nodes(ast.iter_child_nodes(node))

def binds(node, name):
    if isinstance(node, ast.alias):
        return (node.asname or node.name.split(".")[0]) in (name, "*")
    if isinstance(node, ast.Name):
        return node.id == name and isinstance(node.ctx, (ast.Store, ast.Del))
    if isinstance(node, (ast.Global, ast.Nonlocal)):
        return name in node.names
    if isinstance(node, ast.arg):
        return node.arg == name
    return (isinstance(node, (*FUNCTIONS, ast.ClassDef, ast.ExceptHandler, *PATTERN_BINDINGS))
            and (getattr(node, "name", None) == name or getattr(node, "rest", None) == name))

def binding_nodes(scope):
    if isinstance(scope, FUNCTIONS):
        yield from ast.walk(scope.args)
    yield from scope_nodes(scope.body)

def attribute_root(node):
    while isinstance(node.value, ast.Attribute):
        node = node.value
    return node

def mutates_attribute(node, names):
    if not isinstance(node, ast.Attribute) or not isinstance(node.ctx, (ast.Store, ast.Del)):
        return False
    receiver = attribute_root(node).value
    return isinstance(receiver, ast.Name) and receiver.id in names

def stable_name(scope, name, origin=None, module_alias=False):
    for node in binding_nodes(scope):
        if node is not origin and binds(node, name):
            return False
        if isinstance(node, ast.Name) and node.id in DYNAMIC_NAMES:
            return False
        if isinstance(node, ast.Attribute):
            root = attribute_root(node)
            if isinstance(root.value, ast.Name) and root.value.id == name and (
                    isinstance(node.ctx, (ast.Store, ast.Del)) or root.attr.startswith("__")):
                return False
    if module_alias:
        for node in binding_nodes(scope):
            for child in ast.iter_child_nodes(node):
                if isinstance(child, ast.Name) and child.id == name and isinstance(child.ctx, ast.Load):
                    if not isinstance(node, ast.Attribute):
                        return False
    return True

def has_global_write(module, name):
    return any((isinstance(node, ast.Global) and name in node.names) or mutates_attribute(node, {name})
               for node in ast.walk(module))

def import_aliases(statements, package):
    return [part for stmt in statements if isinstance(stmt, ast.Import)
            for part in stmt.names if part.name == package]

def import_names(package, module, caller, imports, before):
    local = [] if caller is None else [part for part in import_aliases(imports, package)
                                      if stable_name(caller, part.asname or part.name, part, module_alias=True)]
    outer = [part for part in import_aliases(module.body, package)
             if part.lineno < before and stable_name(module, part.asname or part.name, part, module_alias=True)
             and (caller is None or stable_name(caller, part.asname or part.name, module_alias=True))]
    if not local and not outer:
        return set()
    nodes = list(ast.walk(module))
    known = {part.asname or part.name for part in import_aliases(nodes, package)}
    if any(mutates_attribute(node, known) for node in nodes):
        return set()
    return {part.asname or part.name for part in [*local, *outer]
            if not has_global_write(module, part.asname or part.name)}

def iterable_empty(node):
    value = literal(node)
    return isinstance(value, (str, bytes, list, tuple, set, dict)) and not value

def visit(body, imports, sites, values=None):
    # Imports from a branch stay in that branch. The scanner assumes that a
    # call, a nonconstant condition, or iteration may run normally.
    for index, stmt in enumerate(body):
        following = body[index + 1] if index + 1 < len(body) else None
        sites[stmt] = (imports, following)
        if isinstance(stmt, (ast.Return, ast.Raise, ast.Break, ast.Continue)):
            return False
        if isinstance(stmt, ast.Import):
            imports = (*imports, stmt)
        elif isinstance(stmt, ast.If):
            known = truth(stmt.test, values)
            yes = known is not False and visit(stmt.body, imports, sites, values)
            no = known is not True and visit(stmt.orelse, imports, sites, values)
            if not (yes or no):
                return False
        elif isinstance(stmt, ast.Try):
            if stmt.finalbody:
                return False
            if not visit(stmt.body, imports, sites, values) or not visit(stmt.orelse, imports, sites, values):
                return False
        elif isinstance(stmt, ast.For):
            if not iterable_empty(stmt.iter):
                visit(stmt.body, imports, sites, values)
            if stmt.orelse:
                return False
        elif isinstance(stmt, ast.While):
            known = truth(stmt.test, values)
            if known is not False:
                visit(stmt.body, imports, sites, values)
            if known is True or stmt.orelse:
                return False
        elif isinstance(stmt, ast.With):
            if not visit(stmt.body, imports, sites, values):
                return False
        elif isinstance(stmt, UNSUPPORTED):
            return False
        elif isinstance(stmt, ast.Assert) and truth(stmt.test, values) is False:
            return False
    return True

def evaluated(node):
    # Trace expression structure where operands can actually be evaluated.
    # A comprehension body is handled separately at its statement.
    yield node
    if isinstance(node, ast.BoolOp):
        for child in node.values:
            yield from evaluated(child)
            known = truth(child)
            if known is not None and known == isinstance(node.op, ast.Or):
                break
        return
    if isinstance(node, ast.IfExp):
        children = [node.test]
        known = truth(node.test)
        if known is not False:
            children.append(node.body)
        if known is not True:
            children.append(node.orelse)
    elif isinstance(node, ast.Compare):
        children = [node.left, node.comparators[0]]
    elif isinstance(node, ast.Call):
        children = [node.func, *node.args, *(kw.value for kw in node.keywords)]
    elif isinstance(node, (ast.UnaryOp, ast.Attribute, ast.BinOp, ast.Subscript, ast.Slice,
                           ast.List, ast.Tuple, ast.Set, ast.Dict, ast.Starred)):
        children = [child for child in ast.iter_child_nodes(node) if isinstance(child, ast.expr)]
    else:
        return
    for child in children:
        yield from evaluated(child)

def consumes(stmt, bound_result=""):
    # The flag means that the outermost call consumes its arguments but
    # its own result would be discarded as a statement.
    if isinstance(stmt, (ast.If, ast.While)):
        return [(stmt.test, False)]
    if isinstance(stmt, ast.For):
        return [(stmt.iter, False)]
    if isinstance(stmt, (ast.Return, ast.AugAssign)) and stmt.value is not None:
        return [(stmt.value, False)]
    if isinstance(stmt, ast.Expr) and isinstance(stmt.value, ast.Call):
        return [(stmt.value, True)]
    if isinstance(stmt, ast.Assign) and isinstance(stmt.value, (ast.DictComp, ast.ListComp, ast.SetComp)):
        generator = stmt.value.generators[0]
        conditions = [(generator.iter, False)]
        shadows_result = any(binds(node, bound_result) for node in ast.walk(generator.target))
        if not shadows_result and not generator.is_async and not iterable_empty(generator.iter):
            for cond in generator.ifs:
                conditions.append((cond, False))
                if truth(cond) is False:
                    break
        return conditions
    return []

def return_object(node):
    if isinstance(node, ast.Call):
        return return_object(node.func)
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        return node.attr
    if isinstance(node, ast.Constant) and type(node.value) in (bool, int):
        return repr(node.value)
    if isinstance(node, (ast.BoolOp, ast.Compare)) or isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return ast.unparse(node)
    return None

def matches_use(node, wanted, use_line, reads):
    return node.lineno == use_line and shape(node) == wanted and any(map(reads, evaluated(node)))

def entry_body(module, line):
    guard = shape(expression('__name__ == "__main__"'))
    for stmt in module.body:
        if (isinstance(stmt, ast.If) and stmt.lineno == line
                and shape(stmt.test) == guard and stable_name(module, "__name__")):
            return stmt.body
    return None

def fixed_arguments(function, call, unchanged):
    positional = [param.arg for param in (*function.args.posonlyargs, *function.args.args)]
    if any(isinstance(arg, ast.Starred) for arg in call.args) or any(kw.arg is None for kw in call.keywords):
        return {}
    bound = dict(zip(positional, call.args))
    posonly = {param.arg for param in function.args.posonlyargs}
    bound.update((kw.arg, kw.value) for kw in call.keywords if kw.arg not in posonly)
    return {name: value.value for name, value in bound.items() if name in unchanged and isinstance(value, ast.Constant)}

def check(request):
    module = ast.parse(request["source"])
    symbol = request["symbol"]
    producer = next((n for n in module.body if isinstance(n, ast.FunctionDef)
                     and n.lineno == request["declaration"] and n.name == symbol and not n.decorator_list), None)
    if producer is None or not stable_name(module, symbol, producer) or has_global_write(module, symbol):
        return "укажите устойчивое объявление функции Python и её потребителя"
    if any(isinstance(node, (ast.Yield, ast.YieldFrom)) for node in ast.walk(producer)):
        return "return_at: вызов функции с yield создаёт итератор"
    returns = {}
    visit(producer.body, (), returns)
    matched = [stmt for stmt in returns if isinstance(stmt, ast.Return)
               and stmt.lineno == request["return_line"] and stmt.value is not None]
    if len(matched) != 1:
        return "return_at: нужен достижимый возврат из проверяемой функции"
    return_stmt = matched[0]
    returned = return_stmt.value
    wanted_result = expression(request["expression"])
    actual = request["actual"]
    if isinstance(returned, (ast.BoolOp, ast.Compare, ast.UnaryOp)):
        actual = ast.unparse(expression(actual))
    if return_object(returned) != actual or shape(returned) != shape(wanted_result):
        return "expression: укажите точное выражение возврата и его идентификатор"
    returned_nodes = {(node.lineno, shape(node)) for node in evaluated(returned)}
    if any((word["line"], shape(expression(word["value"]))) not in returned_nodes for word in request["words"]):
        return "proof.words: покажите выражение, которое участвует в возвращаемом значении"

    package = request.get("import")
    use_module = ast.parse(request["caller_source"]) if package else module
    caller = next((n for n in use_module.body if isinstance(n, ast.FunctionDef)
                   and n.lineno == request["caller"] and not n.decorator_list), None)
    body = caller.body if caller is not None else entry_body(use_module, request["caller"])
    if body is None or (caller is None and (package or producer.lineno >= request["caller"] or not isinstance(returned, ast.Constant)
                                            or type(returned.value) is not int)):
        return "flow.caller: укажите объявление потребителя или условие запуска Python"
    if not package and caller is not None and not stable_name(caller, symbol):
        return "flow.caller: укажите устойчивое объявление функции Python и её потребителя"

    sites = {}
    visit(body, (), sites)
    wanted_use = shape(expression(request["use"]))
    value = request["value"]
    read_count = 0
    call_seen = False
    use_seen = False
    call_returns = {}
    params = {arg.arg for arg in (*producer.args.posonlyargs, *producer.args.args, *producer.args.kwonlyargs)}
    unchanged = {name for name in params if not any(binds(node, name) for node in ast.walk(producer)
                                                   if not isinstance(node, ast.arg))}
    for stmt, (imports, following) in sites.items():
        if not stmt.lineno <= request["call_line"] <= stmt.end_lineno:
            continue
        modules = import_names(package, use_module, caller, imports, request["caller"]) if package else set()

        def is_call(node):
            if not isinstance(node, ast.Call) or node.lineno != request["call_line"]:
                return False
            target = node.func
            if package:
                matches = (isinstance(target, ast.Attribute) and target.attr == symbol
                           and isinstance(target.value, ast.Name) and target.value.id in modules)
            else:
                matches = isinstance(target, ast.Name) and target.id == symbol
            if not matches:
                return False
            if node not in call_returns:
                values = fixed_arguments(producer, node, unchanged)
                reachable = {}
                if values:
                    visit(producer.body, (), reachable, values)
                call_returns[node] = not values or return_stmt in reachable
            return call_returns[node]

        if value:
            if (not isinstance(stmt, ast.Assign) or len(stmt.targets) != 1
                    or not isinstance(stmt.targets[0], ast.Name) or stmt.targets[0].id != value):
                continue
            # Set difference prepares the remaining roots before iteration.
            candidate = stmt.value
            if isinstance(candidate, ast.BinOp) and isinstance(candidate.op, ast.Sub):
                candidate = candidate.left
            if not is_call(candidate):
                continue
            call_seen = True
            if following is None or following not in sites:
                continue
            expressions = consumes(following, value)

            def reads(node):
                return isinstance(node, ast.Name) and node.id == value and isinstance(node.ctx, ast.Load)
        else:
            expressions = consumes(stmt)
            if caller is None:
                sys_names = import_names("sys", use_module, None, imports, request["caller"])
                expressions = [(outer, discarded) for outer, discarded in expressions
                               if (isinstance(outer, ast.Call) and isinstance(outer.func, ast.Attribute)
                                   and isinstance(outer.func.value, ast.Name) and outer.func.value.id in sys_names
                                   and outer.func.attr == "exit" and len(outer.args) == 1
                                   and not outer.keywords and is_call(outer.args[0]))]

            def reads(node):
                return is_call(node)

        for outer, discard_outer in expressions:
            use_seen = True
            if any(isinstance(node, ast.NamedExpr) for node in ast.walk(outer)):
                return "use_expression: выражение не должно переприсваивать результат"
            if value and any(isinstance(node, (ast.Global, ast.Nonlocal)) and value in node.names for node in ast.walk(caller)):
                return "use_expression: укажите локальное значение результата"
            if not value and not any(is_call(node) and (not discard_outer or node is not outer) for node in evaluated(outer)):
                continue
            call_seen = True
            read_count += sum((not discard_outer or node is not outer or not is_call(node))
                              and matches_use(node, wanted_use, request["use_line"], reads) for node in evaluated(outer))
    if not call_seen:
        if call_returns and not any(call_returns.values()):
            return "return_at: фактические аргументы вызова исключают указанный возврат"
        return ("call_at: укажите прямое присваивание результата проверяемого вызова" if value
                else "call_at: укажите вызов проверяемой функции, результат которого используется")
    if not use_seen:
        return "use_expression: укажите чтение сразу после присваивания результата"
    if read_count != 1:
        return "use_expression: покажите однозначное чтение полученного значения"
    return ""

try:
    print(json.dumps(check(json.load(sys.stdin))))
except (SyntaxError, ValueError, RecursionError) as error:
    print(json.dumps(str(error)))
`

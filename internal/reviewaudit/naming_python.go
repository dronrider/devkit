package reviewaudit

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

type pythonFileWrite struct {
	Source      string                `json:"source"`
	Declaration int                   `json:"declaration"`
	Symbol      string                `json:"symbol"`
	WriteLine   int                   `json:"write_line"`
	Destination string                `json:"destination"`
	Value       string                `json:"value"`
	Expression  string                `json:"expression"`
	Actual      string                `json:"actual"`
	ActualLine  int                   `json:"actual_line"`
	Role        string                `json:"role"`
	PayloadLine int                   `json:"payload_line"`
	EncodeLine  int                   `json:"encode_line"`
	Calls       []pythonFileWriteCall `json:"calls,omitempty"`
}

type pythonFileWriteCall struct {
	Line        int               `json:"line"`
	Declaration int               `json:"declaration"`
	Bindings    []NameCallBinding `json:"bindings"`
}

type pythonFileWriteReply struct {
	Error       string  `json:"error"`
	Binding     int     `json:"binding"`
	Destination *string `json:"destination"`
	Links       []int   `json:"links"`
	RecordKind  string  `json:"record_kind"`
}

func checkNamePythonFileWrite(e Evidence, declaration string, source *evidenceSource) error {
	w, pair := e.WriteEvidence, e.ObjectComparison
	f := w.File
	file, declared, base, err := evidenceLocation(declaration)
	if err != nil {
		return err
	}
	line := func(ref string) int {
		p, n, fromBase, err := evidenceLocation(ref)
		if err != nil || p != file || fromBase != base {
			return 0
		}
		return n
	}
	request := pythonFileWrite{
		Declaration: declared, Symbol: nameObjectSymbol(e.Object), WriteLine: line(w.WriteAt),
		Destination: f.Path, Value: w.Value, Actual: pair.Actual, ActualLine: line(pair.ActualAt),
		Role: f.Role, PayloadLine: line(f.PayloadAt), EncodeLine: line(f.EncodeAt),
	}
	if f.Path != w.Target || request.WriteLine == 0 || request.ActualLine == 0 {
		return fmt.Errorf("write_evidence.file: укажите фактический путь и строки в том же файле той же ревизии")
	}
	switch f.Role {
	case "destination":
		if f.PayloadAt != "" || f.EncodeAt != "" || f.Path != pair.Actual {
			return fmt.Errorf("write_evidence.file: для destination укажите путь как actual")
		}
	case "content":
		if request.PayloadLine == 0 || request.EncodeLine == 0 || pair.Proof == nil {
			return fmt.Errorf("write_evidence.file: для content укажите payload_at, encode_at и proof")
		}
	default:
		return fmt.Errorf("write_evidence.file.role: укажите destination или content")
	}
	for i, hop := range w.Calls {
		at, declaration := line(hop.At), line(hop.Declaration)
		if at == 0 || declaration == 0 || hop.ReceiverAt != "" {
			return fmt.Errorf("write_evidence.calls[%d]: укажите прямой вызов в том же файле той же ревизии", i)
		}
		request.Calls = append(request.Calls, pythonFileWriteCall{Line: at, Declaration: declaration, Bindings: hop.Bindings})
	}
	if pair.Proof != nil {
		if err := checkNameProofWords(pair, source); err != nil {
			return err
		}
		request.Expression = pair.Proof.Expression
	}
	body, err := source.read(file, base)
	if err != nil {
		return err
	}
	request.Source = body
	verified, err := runNamePythonVerifier[pythonFileWriteReply](pythonFileWriteAST, request)
	if err != nil {
		return fmt.Errorf("write_evidence.file: %w", err)
	}
	if verified.Error != "" {
		return fmt.Errorf("write_evidence.file: %s", verified.Error)
	}
	words := pair.Expected
	if pair.Proof != nil {
		lines := map[string]bool{w.WriteAt: true}
		prefix := ""
		if base {
			prefix = "base@"
		}
		for _, n := range append(verified.Links, verified.Binding) {
			if n != 0 {
				lines[prefix+file+":"+strconv.Itoa(n)] = true
			}
		}
		if err := checkNameWriteWordLinks(pair.Proof.Words, lines); err != nil {
			return err
		}
		if objects := nameProofObjects(pair); len(objects) != 0 {
			words = objects
		}
	}
	if f.Role == "content" {
		return checkPythonRecordWords(e, verified.RecordKind)
	}
	name := strings.Join(words, " ")
	var destination []string
	if verified.Destination != nil {
		destination = identifierWords(path.Base(*verified.Destination))
	}
	if e.Result == "checked" && name != "" &&
		!strings.EqualFold(name, "file") && !strings.EqualFold(name, "path") &&
		!sameObjectName(words, destination) {
		return fmt.Errorf("write_evidence.file: путь не подтверждает объект %q", name)
	}
	return nil
}

func checkPythonRecordWords(e Evidence, kind string) error {
	pair, f := e.ObjectComparison, e.WriteEvidence.File
	var objects []string
	for _, word := range pair.Proof.Words {
		name := strings.ToLower(word.Word)
		if e.Result == "checked" && (name == "reserve" || name == "finish" || name == "result" || name == "record") && word.Role != "object" {
			return fmt.Errorf("proof.words %s: укажите роль object для записываемого содержимого", word.Word)
		}
		switch word.Role {
		case "format":
			if word.Fact.Location != f.EncodeAt || (!strings.EqualFold(word.Word, "json") && !strings.EqualFold(word.Word, "jsonl")) {
				return fmt.Errorf("proof.words %s: укажите фактический формат JSONL на строке кодирования", word.Word)
			}
		case "object":
			if word.Fact.Location != f.PayloadAt {
				return fmt.Errorf("proof.words %s: укажите словарь, который записывается", word.Word)
			}
			objects = append(objects, name)
		}
	}
	if e.Result == "checked" {
		if len(objects) == 0 {
			return fmt.Errorf("proof.words: укажите слово роли object для содержимого записи")
		}
		name := strings.Join(objects, " ")
		finish := kind == "result" && (name == "finish" || name == "finish record")
		if name != "record" && name != kind && name != kind+" record" && !finish {
			return fmt.Errorf("write_evidence.file: записанное содержимое %q не подтверждает объект %q", kind+" record", name)
		}
	}
	return nil
}

const pythonFileWriteAST = `
import ast
import json
import sys

MATCH = (ast.Match,) if hasattr(ast, "Match") else ()
PATTERN_BINDINGS = (ast.MatchAs, ast.MatchStar, ast.MatchMapping) if MATCH else ()

def constant_truth(expr):
    if isinstance(expr, ast.Constant):
        return bool(expr.value)
    if isinstance(expr, ast.UnaryOp) and isinstance(expr.op, ast.Not):
        operand = constant_truth(expr.operand)
        if operand is not None:
            return not operand
    return None

def reachable(statements, calls, effects=None, finite=None):
    for statement in statements:
        if effects is not None:
            effects.append(statement)
        if isinstance(statement, (ast.Return, ast.Raise, ast.Break, ast.Continue)):
            return False
        if isinstance(statement, ast.Expr) and isinstance(statement.value, ast.Call):
            calls.append(statement.value)
        elif isinstance(statement, ast.If):
            condition = statement.test
            known = constant_truth(condition)
            if known is not None:
                branch = statement.body if known else statement.orelse
                if not reachable(branch, calls, effects, finite):
                    return False
            else:
                yes = reachable(statement.body, [])
                no = reachable(statement.orelse, [])
                if not (yes or no):
                    return False
        elif isinstance(statement, ast.Try):
            body_continues = reachable(statement.body, calls, effects, finite)
            if body_continues:
                body_continues = reachable(statement.orelse, calls, effects, finite)
            if not reachable(statement.finalbody, []) or not body_continues:
                return False
        elif isinstance(statement, ast.With):
            if not reachable(statement.body, calls, effects, finite):
                return False
        elif isinstance(statement, ast.For) and finite and isinstance(statement.iter, ast.Name) and not statement.orelse:
            origin = finite.get(statement.iter.id)
            if origin not in effects or not reachable(statement.body, []):
                return False
        elif isinstance(statement, (ast.For, ast.While, ast.AsyncFor, ast.AsyncWith, *MATCH, ast.ClassDef, getattr(ast, "TryStar", ast.Try))):
            return False
        elif isinstance(statement, ast.Assert) and constant_truth(statement.test) is False:
            return False
    return True

def stable_import(module, name, package, exported=None):
    matches = [part for stmt in module.body
               if (exported is None and isinstance(stmt, ast.Import))
               or (exported is not None and isinstance(stmt, ast.ImportFrom) and stmt.module == package and stmt.level == 0)
               for part in stmt.names if (part.asname or part.name) == name and part.name == (exported or package)]
    if len(matches) != 1 or not stable_binding(module, name, matches[0]):
        return False
    aliases = {part.asname or part.name for node in ast.walk(module) if isinstance(node, ast.Import)
               for part in node.names if part.name == package}
    parents = {child: parent for parent in ast.walk(module) for child in ast.iter_child_nodes(parent)}
    for node, parent in parents.items():
        if isinstance(node, ast.Name) and node.id in aliases and isinstance(node.ctx, ast.Load):
            if not isinstance(parent, ast.Attribute) or parent.value is not node or parent.attr.startswith("__"):
                return False
            while isinstance(parent, ast.Attribute):
                if isinstance(parent.ctx, (ast.Store, ast.Del)):
                    return False
                parent = parents.get(parent)
    return True

def stable_binding(module, name, origin):
    for node in ast.walk(module):
        if node is origin:
            continue
        if isinstance(node, ast.alias) and (node.asname or node.name.split(".")[0]) in (name, "*"):
            return False
        if isinstance(node, ast.Name) and node.id == name and isinstance(node.ctx, (ast.Store, ast.Del)):
            return False
        if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name) and node.value.id == name and isinstance(node.ctx, (ast.Store, ast.Del)):
            return False
        if isinstance(node, ast.arg) and node.arg == name:
            return False
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.ExceptHandler, *PATTERN_BINDINGS)) and (getattr(node, "name", None) == name or getattr(node, "rest", None) == name):
            return False
    return True

def shape(node):
    return ast.dump(node)

def path_literal(module, function, call, target):
    if isinstance(target, ast.Constant) and isinstance(target.value, str):
        return 0, target.value
    if not isinstance(target, ast.Name):
        return 0, None
    if any(isinstance(node, (ast.Global, ast.Nonlocal)) and target.id in node.names for node in ast.walk(function)):
        return 0, None
    assigned = [node for node in ast.walk(function)
                if isinstance(node, ast.Name) and node.id == target.id and isinstance(node.ctx, (ast.Store, ast.Del))]
    if len(assigned) != 1:
        return 0, None
    for line in function.body:
        if (not isinstance(line, ast.Assign) or len(line.targets) != 1
                or line.targets[0] is not assigned[0] or line.lineno >= call.lineno):
            continue
        value = line.value
        if isinstance(value, ast.Constant) and isinstance(value.value, str):
            return line.lineno, value.value
        if not isinstance(value, ast.Call) or not isinstance(value.func, ast.Name):
            return line.lineno, None
        if stable_import(module, value.func.id, "pathlib", "Path") and len(value.args) == 1 and not value.keywords:
            literal = value.args[0]
            if isinstance(literal, ast.Constant) and isinstance(literal.value, str):
                return line.lineno, literal.value
        if len(value.args) == 2 and not value.keywords and isinstance(value.args[1], ast.Constant) and isinstance(value.args[1].value, str):
            helpers = [node for node in module.body if isinstance(node, ast.FunctionDef) and node.name == value.func.id]
            if len(helpers) == 1 and stable_binding(module, value.func.id, helpers[0]) and home_join(module, helpers[0]):
                return line.lineno, value.args[1].value
        return line.lineno, None
    return 0, None

def home_join(module, helper):
    args = helper.args
    if args.posonlyargs or args.vararg or args.kwonlyargs or args.kwarg or args.defaults or len(args.args) != 2 or helper.decorator_list:
        return False
    left, right = [arg.arg for arg in args.args]
    body = helper.body
    if ast.get_docstring(helper) is not None:
        body = body[1:]
    if len(body) != 2 or not isinstance(body[0], ast.Assign) or len(body[0].targets) != 1 or not isinstance(body[0].targets[0], ast.Name):
        return False
    suffix = body[0].targets[0].id
    cond = ast.parse(f'{right}[2:] if {right}.startswith("~/") else {right}', mode="eval").body
    if shape(cond) != shape(body[0].value) or not isinstance(body[1], ast.Return):
        return False
    expr = body[1].value
    return (isinstance(expr, ast.BinOp) and isinstance(expr.op, ast.Div)
            and isinstance(expr.left, ast.Call) and isinstance(expr.left.func, ast.Name)
            and stable_import(module, expr.left.func.id, "pathlib", "Path")
            and not expr.left.keywords and len(expr.left.args) == 1
            and isinstance(expr.left.args[0], ast.Name) and expr.left.args[0].id == left
            and isinstance(expr.right, ast.Name) and expr.right.id == suffix)

def direct_call(statement):
    if isinstance(statement, (ast.Expr, ast.Return)):
        value = statement.value
    elif isinstance(statement, ast.If):
        value = statement.test
        if isinstance(value, ast.UnaryOp) and isinstance(value.op, ast.Not):
            value = value.operand
    else:
        return None
    return value if isinstance(value, ast.Call) else None

def reachable_effects(function):
    statements = []
    finite = {stmt.targets[0].id: stmt for stmt in function.body
              if isinstance(stmt, ast.Assign) and len(stmt.targets) == 1 and isinstance(stmt.targets[0], ast.Name)
              and isinstance(stmt.value, ast.ListComp) and local_stable(function, stmt.targets[0].id, stmt.targets[0])}
    reachable(function.body, [], statements, finite)
    return statements

def local_stable(function, name, origin):
    return (stable_binding(function, name, origin)
            and not any(isinstance(node, (ast.Global, ast.Nonlocal)) and name in node.names for node in ast.walk(function)))

def plain_function(function):
    return not function.decorator_list and not any(isinstance(node, (ast.Yield, ast.YieldFrom)) for node in ast.walk(function))

def local_assignment(function, name, before, parameter=False):
    for statement in reachable_effects(function):
        if (statement.lineno >= before.lineno or statement not in function.body
                or not isinstance(statement, ast.Assign) or len(statement.targets) != 1):
            continue
        target = statement.targets[0]
        if not isinstance(target, ast.Name) or target.id != name:
            continue
        if parameter:
            if [node for node in ast.walk(function) if isinstance(node, ast.Name) and node.id == name
                    and isinstance(node.ctx, (ast.Store, ast.Del))] == [target]:
                return statement
        elif local_stable(function, name, target):
            return statement
    return None

def path_helper(module, call):
    name = call.func.id
    for helper in module.body:
        if (not isinstance(helper, ast.FunctionDef) or helper.name != name or not plain_function(helper)
                or not stable_binding(module, name, helper) or helper_bindings(call, helper) is None):
            continue
        # A helper can have an environment override. The regular path must
        # still be visible as a string or a join of path components.
        for statement in reachable_effects(helper):
            if not isinstance(statement, ast.Return):
                continue
            value = statement.value
            if isinstance(value, ast.Constant) and isinstance(value.value, str) and value.value:
                return True
            if (isinstance(value, ast.Call) and isinstance(value.func, ast.Attribute) and value.func.attr == 'join'
                    and isinstance(value.func.value, ast.Attribute) and value.func.value.attr == 'path'
                    and isinstance(value.func.value.value, ast.Name)
                    and stable_import(module, value.func.value.value.id, 'os')
                    and value.args and not value.keywords
                    and all(not isinstance(arg, ast.Starred)
                            and (not isinstance(arg, ast.Constant) or isinstance(arg.value, str)) for arg in value.args)):
                return True
    return False

def helper_bindings(call, helper):
    args = helper.args
    if (args.posonlyargs or args.vararg or args.kwonlyargs or args.kwarg
            or any(isinstance(arg, ast.Starred) for arg in call.args)):
        return None
    params = [arg.arg for arg in args.args]
    bindings = list(zip(params, call.args))
    if len(bindings) != len(call.args):
        return None
    names = [keyword.arg for keyword in call.keywords]
    if len(set(names)) != len(names) or any(name not in params[len(bindings):] for name in names):
        return None
    bindings += [(keyword.arg, keyword.value) for keyword in call.keywords]
    if not set(params[:len(params)-len(args.defaults)]).issubset(name for name, _ in bindings):
        return None
    return bindings

def follow_helpers(module, function, claims, links):
    route = []
    seen = {function}
    helpers = {statement.lineno: statement for statement in module.body if isinstance(statement, ast.FunctionDef)}
    for i, claim in enumerate(claims):
        helper = helpers.get(claim['declaration'])
        if helper is None or helper in seen or not plain_function(helper) or not stable_binding(module, helper.name, helper):
            raise ValueError(f'calls[{i}]: укажите незатенённого помощника верхнего уровня')
        matching = [call for statement in reachable_effects(function) if (call := direct_call(statement)) is not None
                    and call.lineno == claim['line'] and isinstance(call.func, ast.Name) and call.func.id == helper.name]
        if len(matching) != 1:
            raise ValueError(f'calls[{i}]: укажите один достижимый прямой вызов помощника')
        call = matching[0]
        bindings = helper_bindings(call, helper)
        supplied = [(item['parameter'], shape(parse(item['argument']))) for item in (claim['bindings'] or [])]
        if bindings is None or [(name, shape(value)) for name, value in bindings] != supplied:
            raise ValueError(f'calls[{i}].bindings: укажите фактические аргументы в порядке вызова')
        links.add(call.lineno)
        links.update(app_gate_lines(module, function, call))
        route.append((function, dict(bindings)))
        seen.add(helper)
        function = helper
    return function, route

def app_gate_lines(module, function, call):
    if not stable_binding(module, 'any', None):
        return []
    lines = []
    for statement in function.body:
        if (not isinstance(statement, ast.If) or statement.lineno >= call.lineno or statement.orelse
                or not isinstance(statement.test, ast.UnaryOp) or not isinstance(statement.test.op, ast.Not)
                or reachable(statement.body, [])):
            continue
        check = statement.test.operand
        if (not isinstance(check, ast.Call) or not isinstance(check.func, ast.Name) or check.func.id != 'any'
                or check.keywords or len(check.args) != 1 or not isinstance(check.args[0], ast.GeneratorExp)):
            continue
        expr = check.args[0].elt
        clauses = expr.values if isinstance(expr, ast.BoolOp) and isinstance(expr.op, ast.And) else [expr]
        lines.extend(clause.lineno for clause in clauses if isinstance(clause, ast.Call) and isinstance(clause.func, ast.Attribute)
                     and clause.func.attr == 'startswith' and not clause.keywords and len(clause.args) == 1
                     and shape(clause.args[0]) == shape(ast.Constant('app:')))
    return lines

def parse(expression):
    return ast.parse(expression, mode='eval').body

def api(call, attribute, module_name=None):
    if (isinstance(call, ast.Call) and isinstance(call.func, ast.Attribute) and call.func.attr == attribute
            and isinstance(call.func.value, ast.Name) and (module_name is None or call.func.value.id == module_name)):
        return call.func.value.id
    return None

def flags(expression, name):
    if isinstance(expression, ast.BinOp) and isinstance(expression.op, ast.BitOr):
        return flags(expression.left, name) | flags(expression.right, name)
    if isinstance(expression, ast.Attribute) and isinstance(expression.value, ast.Name) and expression.value.id == name:
        return {expression.attr}
    return {None}

def append_path(module, function, statement, stream):
    for container in ast.walk(function):
        if (not isinstance(container, ast.With) or len(container.items) != 1 or container.body[0] is not statement):
            continue
        item = container.items[0]
        wrap, alias = item.context_expr, item.optional_vars
        name = api(wrap, 'fdopen')
        if (name is None or not stable_import(module, name, 'os') or not isinstance(alias, ast.Name) or alias.id != stream
                or not local_stable(function, stream, alias) or wrap.keywords or len(wrap.args) != 2
                or not isinstance(wrap.args[0], ast.Name) or shape(wrap.args[1]) != shape(ast.Constant('wb'))):
            return None
        fd = wrap.args[0].id
        for parent in ast.walk(function):
            for _, block in ast.iter_fields(parent):
                if not isinstance(block, list) or container not in block:
                    continue
                pos = block.index(container)
                assign = block[pos-1] if pos > 0 else None
                if not isinstance(assign, ast.Assign) or len(assign.targets) != 1:
                    return None
                target, opened = assign.targets[0], assign.value
                if (not isinstance(target, ast.Name) or target.id != fd or not local_stable(function, fd, target)
                        or api(opened, 'open', name) is None or len(opened.args) != 3 or opened.keywords
                        or not isinstance(opened.args[2], ast.Constant) or type(opened.args[2].value) is not int):
                    return None
                mode = flags(opened.args[1], name)
                if ('O_APPEND' not in mode or len(mode & {'O_WRONLY', 'O_RDWR'}) != 1
                        or not mode <= {'O_APPEND', 'O_WRONLY', 'O_RDWR', 'O_CREAT', 'O_NOFOLLOW'}):
                    return None
                return opened, wrap
    return None

def serialized_record(module, payload):
    if (not isinstance(payload, ast.Call) or payload.args or payload.keywords or not isinstance(payload.func, ast.Attribute)
            or payload.func.attr != 'encode'):
        return None
    line = payload.func.value
    if not isinstance(line, ast.BinOp) or not isinstance(line.op, ast.Add) or shape(line.right) != shape(ast.Constant('\n')):
        return None
    dump = line.left
    name = api(dump, 'dumps')
    if (name is None or not stable_import(module, name, 'json') or len(dump.args) != 1 or not isinstance(dump.args[0], ast.Name)
            or any(keyword.arg not in ('ensure_ascii', 'allow_nan') or not isinstance(keyword.value, ast.Constant)
                   or type(keyword.value.value) is not bool for keyword in dump.keywords)):
        return None
    return dump.args[0]

def record_source(function, value, route, links):
    # Each forwarding name is used once, by the next call or the serializer.
    # This excludes intervening stores, aliases and mutation via the same name.
    for caller, bindings in reversed(route):
        if not isinstance(value, ast.Name) or value.id not in bindings:
            return None
        parameters = [arg for arg in function.args.args if arg.arg == value.id]
        if (len(parameters) != 1 or not local_stable(function, value.id, parameters[0])
                or [node for node in ast.walk(function) if isinstance(node, ast.Name) and node.id == value.id
                    and isinstance(node.ctx, ast.Load)] != [value]):
            return None
        function, value = caller, bindings[value.id]
        if isinstance(value, ast.Name):
            binding = local_assignment(function, value.id, value)
            if binding is not None:
                if [node for node in ast.walk(function) if isinstance(node, ast.Name) and node.id == value.id
                        and isinstance(node.ctx, ast.Load)] != [value]:
                    return None
                value = binding.value
        if isinstance(value, ast.Dict):
            links.update(range(value.lineno, value.end_lineno + 1))
            return value
    return None

def recovery_record_kind(record):
    if record is None or any(not isinstance(k, ast.Constant) or type(k.value) is not str for k in record.keys):
        return None
    fields = dict(zip((k.value for k in record.keys), record.values))
    if len(fields) != len(record.keys) or 'attempt' not in fields:
        return None
    kind = fields.get('kind')
    if not isinstance(kind, ast.Constant):
        return None
    if kind.value == 'reserve' and 'incident' in fields:
        return 'reserve'
    if kind.value == 'result' and 'outcome' in fields:
        return 'result'
    return None

def verify_append(module, function, route, statement, call, request, links):
    stream = api(call, 'write')
    origin = append_path(module, function, statement, stream)
    if origin is None or len(call.args) != 1 or call.keywords or isinstance(call.args[0], ast.Starred):
        return 'укажите запись на дескрипторе от достижимого os.open через os.fdopen'
    opened, wrap = origin
    target, payload = opened.args[0], call.args[0]
    if shape(target) != shape(parse(request['destination'])):
        return 'укажите фактический путь, переданный в os.open'
    binding = local_assignment(function, target.id, opened) if isinstance(target, ast.Name) else None
    if binding is None and not (isinstance(target, ast.Constant) and isinstance(target.value, str) and target.value):
        return 'укажите неизменённый фактический путь os.open'
    if binding is not None:
        value = binding.value
        if isinstance(value, ast.Constant):
            if not isinstance(value.value, str) or not value.value:
                return 'пустой путь назначения os.open или путь не строка'
        elif not isinstance(value, ast.Call) or not isinstance(value.func, ast.Name) or not path_helper(module, value):
            return 'укажите привязку пути os.open к строке или помощнику, который возвращает путь'
    actual = request['actual']
    links.update((opened.lineno, wrap.lineno, call.lineno))
    if binding is not None:
        links.add(binding.lineno)
    if shape(payload) != shape(parse(request['value'])):
        return 'value: укажите точный аргумент stream.write'
    expression = request['expression']
    if request['role'] == 'destination':
        if request['actual_line'] not in (opened.lineno, binding.lineno if binding is not None else 0):
            return 'actual_at: укажите передачу или привязку пути os.open'
        if expression and shape(parse(expression)) not in (shape(target), shape(call)):
            return 'укажите путь или полный вызов записи в proof.expression'
        _, destination = path_literal(module, function, opened, target)
        if destination == '':
            return 'пустой путь назначения os.open'
        return {'destination': destination, 'links': sorted(links)}
    rec = serialized_record(module, payload)
    if (rec is None or rec.id != actual or request['actual_line'] != rec.lineno
            or request['encode_line'] != payload.lineno or expression != rec.id):
        return 'content: укажите словарь actual и его кодирование в JSONL в аргументе stream.write'
    record = record_source(function, rec, route, links)
    kind = recovery_record_kind(record)
    if kind is None or request['payload_line'] != record.lineno:
        return 'содержимое: укажите словарь резерва или исхода, переданный в сериализацию'
    for caller, bindings in route:
        for argument in bindings.values():
            if isinstance(argument, ast.Name):
                binding = local_assignment(caller, argument.id, argument, parameter=True)
                if binding is not None:
                    links.add(binding.lineno)
    return {'links': sorted(links), 'record_kind': kind}

def verify(request):
    module = ast.parse(request["source"])
    functions = [node for node in module.body if isinstance(node, ast.FunctionDef)]
    functions.extend(node for cls in module.body if isinstance(cls, ast.ClassDef)
                     for node in cls.body if isinstance(node, ast.FunctionDef))
    function = next((node for node in functions if node.lineno == request["declaration"] and node.name == request["symbol"]), None)
    if function is None or not plain_function(function):
        return "укажите обычную функцию Python без декоратора или yield"
    links = set()
    function, route = follow_helpers(module, function, request.get('calls', []), links)
    statements = [statement for statement in reachable_effects(function) if isinstance(statement, ast.Expr)
                  and statement.lineno == request['write_line'] and api(statement.value, 'write') is not None]
    if len(statements) == 1:
        statement = statements[0]
        return verify_append(module, function, route, statement, statement.value, request, links)
    if route or request['role'] != 'destination' or request['actual_line'] != request['write_line']:
        return 'укажите запись через os.fdopen на строке write_at или прямой os.replace'
    calls = []
    reachable(function.body, calls)
    expected = shape(ast.parse(request["value"], mode="eval").body)
    calls = [call for call in calls if call.lineno == request["write_line"] and shape(call) == expected]
    if len(calls) != 1:
        return "укажите один достижимый прямой вызов на строке записи"
    call = calls[0]
    fun = call.func
    if not isinstance(fun, ast.Attribute) or not isinstance(fun.value, ast.Name) or fun.attr != "replace" or not stable_import(module, fun.value.id, "os"):
        return "ожидается os.replace с незатенённым импортом os"
    if len(call.args) != 2 or call.keywords or any(isinstance(arg, ast.Starred) for arg in call.args):
        return "укажите два позиционных аргумента os.replace"
    target = call.args[1]
    if shape(call.args[0]) == shape(target):
        return "пути источника и назначения os.replace совпадают"
    if shape(target) != shape(ast.parse(request["destination"], mode="eval").body):
        return "укажите фактический путь назначения os.replace"
    if request["expression"] and shape(ast.parse(request["expression"], mode="eval").body) not in (expected, shape(target)):
        return "укажите полный вызов записи или путь в proof.expression"
    binding, dest = path_literal(module, function, call, target)
    if dest == "":
        return "пустой путь назначения os.replace"
    return {"binding": binding, "destination": dest}

try:
    result = verify(json.load(sys.stdin))
    print(json.dumps({"error": result} if isinstance(result, str) else result))
except ValueError as error:
    print(json.dumps({'error': str(error)}))
except SyntaxError:
    print(json.dumps({"error": "не удалось разобрать исходник или выражение Python"}))
`

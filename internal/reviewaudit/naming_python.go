package reviewaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

type pythonFileWrite struct {
	Source      string `json:"source"`
	Declaration int    `json:"declaration"`
	Symbol      string `json:"symbol"`
	WriteLine   int    `json:"write_line"`
	Destination string `json:"destination"`
	Value       string `json:"value"`
	Expression  string `json:"expression"`
}

func checkNamePythonFileWrite(e Evidence, declaration string, source *evidenceSource) error {
	w, pair := e.WriteEvidence, e.ObjectComparison
	f := w.File
	file, declared, base, err := evidenceLocation(declaration)
	if err != nil {
		return err
	}
	writePath, writeLine, writeBase, err := evidenceLocation(w.WriteAt)
	if err != nil {
		return err
	}
	if writePath != file || writeBase != base || len(w.Calls) != 0 {
		return fmt.Errorf("write_evidence.file: для Python укажите прямой вызов в проверяемой функции той же ревизии")
	}
	if f.Role != "destination" || f.PayloadAt != "" || f.EncodeAt != "" ||
		f.Path != w.Target || f.Path != pair.Actual || pair.ActualAt != w.WriteAt {
		return fmt.Errorf("write_evidence.file: для Python укажите destination, фактический путь и actual_at на строке записи")
	}
	expression := ""
	if pair.Proof != nil {
		if err := checkNameProofWords(pair, source); err != nil {
			return err
		}
		expression = pair.Proof.Expression
	}
	body, err := source.read(file, base)
	if err != nil {
		return err
	}
	request, err := json.Marshal(pythonFileWrite{
		Source: body, Declaration: declared, Symbol: nameObjectSymbol(e.Object), WriteLine: writeLine,
		Destination: f.Path, Value: w.Value, Expression: expression,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Source is data for ast.parse. The audited module is never imported.
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", pythonFileWriteAST)
	cmd.Stdin = bytes.NewReader(request)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("write_evidence.file: истёк срок разбора Python")
	}
	if err != nil {
		return fmt.Errorf("write_evidence.file: %w %s", err, strings.TrimSpace(string(out)))
	}
	var verified struct {
		Error       string  `json:"error"`
		Binding     int     `json:"binding"`
		Destination *string `json:"destination"`
	}
	if err := json.Unmarshal(out, &verified); err != nil {
		return fmt.Errorf("write_evidence.file: ответ разбора Python: %w", err)
	}
	if verified.Error != "" {
		return fmt.Errorf("write_evidence.file: %s", verified.Error)
	}
	words := pair.Expected
	if pair.Proof != nil {
		lines := map[string]bool{w.WriteAt: true}
		if verified.Binding != 0 {
			prefix := ""
			if base {
				prefix = "base@"
			}
			lines[prefix+file+":"+strconv.Itoa(verified.Binding)] = true
		}
		if err := checkNameWriteWordLinks(pair.Proof.Words, lines); err != nil {
			return err
		}
		if objects := nameProofObjects(pair); len(objects) != 0 {
			words = objects
		}
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

const pythonFileWriteAST = `
import ast
import json
import sys

def constant_truth(expr):
    if isinstance(expr, ast.Constant):
        return bool(expr.value)
    if isinstance(expr, ast.UnaryOp) and isinstance(expr.op, ast.Not):
        operand = constant_truth(expr.operand)
        if operand is not None:
            return not operand
    return None

def reachable(statements, calls):
    for statement in statements:
        if isinstance(statement, (ast.Return, ast.Raise, ast.Break, ast.Continue)):
            return False
        if isinstance(statement, ast.Expr) and isinstance(statement.value, ast.Call):
            calls.append(statement.value)
        elif isinstance(statement, ast.If):
            condition = statement.test
            known = constant_truth(condition)
            if known is not None:
                branch = statement.body if known else statement.orelse
                if not reachable(branch, calls):
                    return False
            else:
                yes = reachable(statement.body, [])
                no = reachable(statement.orelse, [])
                if not (yes or no):
                    return False
        elif isinstance(statement, ast.Try):
            body_continues = reachable(statement.body, calls)
            if body_continues:
                body_continues = reachable(statement.orelse, calls)
            if not reachable(statement.finalbody, []) or not body_continues:
                return False
        elif isinstance(statement, ast.With):
            if not reachable(statement.body, calls):
                return False
        elif isinstance(statement, (ast.For, ast.While, ast.AsyncFor, ast.AsyncWith, ast.Match, ast.ClassDef, getattr(ast, "TryStar", ast.Try))):
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
    if exported is None:
        parents = {child: parent for parent in ast.walk(module) for child in ast.iter_child_nodes(parent)}
        for node, parent in parents.items():
            if isinstance(node, ast.Name) and node.id == name and isinstance(node.ctx, ast.Load):
                if not isinstance(parent, ast.Attribute) or parent.value is not node or parent.attr.startswith("__"):
                    return False
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
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.ExceptHandler, ast.MatchAs, ast.MatchStar, ast.MatchMapping)) and (getattr(node, "name", None) == name or getattr(node, "rest", None) == name):
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

def verify(request):
    module = ast.parse(request["source"])
    functions = [node for node in module.body if isinstance(node, ast.FunctionDef)]
    functions.extend(node for cls in module.body if isinstance(cls, ast.ClassDef)
                     for node in cls.body if isinstance(node, ast.FunctionDef))
    function = next((node for node in functions if node.lineno == request["declaration"] and node.name == request["symbol"]), None)
    if function is None or function.decorator_list:
        return "укажите объявление проверяемой функции Python без декоратора"
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
except (SyntaxError, ValueError):
    print(json.dumps({"error": "не удалось разобрать исходник или выражение Python"}))
`

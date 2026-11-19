package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

func (g *queryGoSource) checkNameSQLWrite(e Evidence, fn queryFunc) error {
	p, w := e.ObjectComparison, e.WriteEvidence
	sql := p.Proof.SQL
	call, _, err := g.call(fn, w.WriteAt, func(x ast.Expr) bool { return calledName(x) == "ExecContext" || calledName(x) == "Exec" })
	if err != nil {
		return err
	}
	if !sameNameExpression(call, p.Proof.Expression) {
		return fmt.Errorf("proof.sql: укажите полный вызов записи")
	}
	offset := 0
	if calledName(call.Fun) == "ExecContext" {
		offset = 1
	}
	if len(call.Args) <= offset {
		return fmt.Errorf("proof.sql: отсутствует SQL")
	}
	statement, err := g.nameSQLText(fn, call.Args[offset])
	if err != nil {
		return err
	}
	if statement != sql.Statement {
		return fmt.Errorf("proof.sql.statement: текст должен совпадать с вызовом")
	}
	tokens := sqlStatementAt(statement, 1)
	table := 1
	switch sqlWord(tokens, 0) {
	case "INSERT":
		if sqlWord(tokens, 1) != "INTO" {
			return fmt.Errorf("proof.sql: ожидается INSERT INTO")
		}
		table = 2
	case "UPDATE":
	case "DELETE":
		if sqlWord(tokens, 1) != "FROM" {
			return fmt.Errorf("proof.sql: ожидается DELETE FROM")
		}
		table = 2
	default:
		return fmt.Errorf("proof.sql: вызов должен изменять данные")
	}
	if len(tokens) <= table || !sameSQLName(tokens[table].text, sql.Table) || p.Actual != sql.Table || w.Target != sql.Table {
		return fmt.Errorf("proof.sql.table: укажите фактически изменяемую таблицу")
	}
	args := call.Args[offset+1:]
	if len(args) != len(sql.Arguments) {
		return fmt.Errorf("proof.sql.arguments: укажите все аргументы в порядке вызова")
	}
	for i, arg := range args {
		if !sameNameExpression(arg, sql.Arguments[i]) {
			return fmt.Errorf("proof.sql.arguments[%d]: аргумент отличается от вызова", i)
		}
	}
	if e.Result == "checked" && len(nameProofObjects(p)) > 0 && !sameObjectName(nameProofObjects(p), identifierWords(sql.Table)) {
		return fmt.Errorf("proof.sql: таблица %s не подтверждает объект %s", sql.Table, strings.Join(nameProofObjects(p), " "))
	}
	anchors := nameWriteLines(e, map[string]bool{w.WriteAt: true})
	for _, word := range p.Proof.Words {
		fact := word.Fact
		if anchors[fact.Location] {
			continue
		}
		line, err := g.nameProofLine(fn, fact.Location)
		if err != nil || !slices.ContainsFunc(args, func(arg ast.Expr) bool {
			return g.fset.Position(arg.Pos()).Line == line && sameNameExpression(arg, fact.Value)
		}) {
			return fmt.Errorf("proof.words %s: укажите строку проверенного SQL-вызова %s или точное выражение аргумента на его строке", word.Word, w.WriteAt)
		}
	}
	return nil
}

func (g *queryGoSource) nameSQLText(fn queryFunc, expr ast.Expr) (string, error) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok {
		id, named := expr.(*ast.Ident)
		if !named {
			return "", fmt.Errorf("proof.sql: нужен строковый литерал или константа с литералом в том же файле")
		}
		body, err := g.source.read(fn.file, fn.base)
		if err != nil {
			return "", err
		}
		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, fn.file, body, 0)
		if err != nil {
			return "", err
		}
		offset := g.fset.Position(id.Pos()).Offset
		for node := range ast.Preorder(tree) {
			use, found := node.(*ast.Ident)
			if !found || fset.Position(use.Pos()).Offset != offset || use.Obj == nil || use.Obj.Kind != ast.Con {
				continue
			}
			decl, found := use.Obj.Decl.(*ast.ValueSpec)
			if !found {
				continue
			}
			for i, name := range decl.Names {
				if name.Name == use.Name && i < len(decl.Values) {
					literal, ok = decl.Values[i].(*ast.BasicLit)
				}
			}
		}
		if !ok {
			return "", fmt.Errorf("proof.sql: %s должен ссылаться на строковую константу с литералом в том же файле; проверьте объявление и затенение", id.Name)
		}
	}
	if literal.Kind != token.STRING {
		return "", fmt.Errorf("proof.sql: требуется строковая константа")
	}
	return strconv.Unquote(literal.Value)
}

type NameSQLWrite struct {
	Table     string   `json:"table"`
	Statement string   `json:"statement"`
	Arguments []string `json:"arguments"`
}

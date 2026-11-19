package reviewaudit

import (
	"fmt"
	"go/ast"
	"strconv"
	"strings"
)

type NameReferenceWrite struct {
	Table        string `json:"table"`
	Column       string `json:"column"`
	Value        string `json:"value"`
	RowColumn    string `json:"row_column"`
	RowValue     string `json:"row_value"`
	TargetTable  string `json:"target_table"`
	TargetColumn string `json:"target_column"`
	SchemaAt     string `json:"schema_at"`
}

func (g *queryGoSource) checkNameReference(e Evidence, fn queryFunc) error {
	pair, proof := e.ObjectComparison, e.ObjectComparison.Proof
	r := proof.Reference
	if r == nil || proof.Type != "" {
		return fmt.Errorf("proof.reference: укажите запись ссылки на объект, её значение и внешний ключ")
	}
	if pair.Actual != r.Column || r.Value == "" || r.RowValue == "" {
		return fmt.Errorf("proof.reference: actual должен называть столбец ссылки; различите значение ссылки и ID изменяемой записи")
	}
	call, _, err := g.call(fn, e.WriteEvidence.WriteAt, func(expr ast.Expr) bool {
		return calledName(expr) == "ExecContext" || calledName(expr) == "Exec"
	})
	if err != nil {
		return err
	}
	if !sameNameExpression(call, proof.Expression) {
		return fmt.Errorf("proof.expression: укажите полный вызов записи")
	}
	offset := 0
	if calledName(call.Fun) == "ExecContext" {
		offset = 1
	}
	if len(call.Args) < offset+3 {
		return fmt.Errorf("proof.reference: нужны SQL и аргументы записи")
	}
	literal, ok := call.Args[offset].(*ast.BasicLit)
	if !ok {
		return fmt.Errorf("proof.reference: требуется SQL-выражение в проверяемом вызове")
	}
	sql, err := strconv.Unquote(literal.Value)
	if err != nil {
		return err
	}
	tokens := sqlStatementAt(sql, 1)
	if len(tokens) != 10 || sqlWord(tokens, 0) != "UPDATE" || sqlWord(tokens, 2) != "SET" ||
		sqlWord(tokens, 4) != "=" || sqlWord(tokens, 6) != "WHERE" || sqlWord(tokens, 8) != "=" ||
		!sameSQLName(tokens[1].text, r.Table) || !sameSQLName(tokens[3].text, r.Column) || !sameSQLName(tokens[7].text, r.RowColumn) {
		return fmt.Errorf("proof.reference: подтвердите UPDATE таблицы и столбца ссылки с отбором изменяемой записи")
	}
	args := call.Args[offset+1:]
	largest := 0
	for _, binding := range []struct{ token, value string }{{tokens[5].text, r.Value}, {tokens[9].text, r.RowValue}} {
		slot := largest + 1
		if binding.token != "?" {
			if !strings.HasPrefix(binding.token, "$") && !strings.HasPrefix(binding.token, "?") {
				return fmt.Errorf("proof.reference: неподдерживаемый параметр %s", binding.token)
			}
			slot, err = strconv.Atoi(binding.token[1:])
			if err != nil {
				return fmt.Errorf("proof.reference: требуется номер параметра")
			}
		}
		if slot < 1 || slot > len(args) || !sameNameExpression(args[slot-1], binding.value) {
			return fmt.Errorf("proof.reference: параметр %s не получает %s", binding.token, binding.value)
		}
		largest = max(largest, slot)
	}
	_, _, schemaBase, err := evidenceLocation(r.SchemaAt)
	if err != nil || schemaBase != fn.base {
		return fmt.Errorf("proof.reference.schema_at: укажите схему в проверяемой ревизии")
	}
	if err := checkNameForeignKey(r, g.source); err != nil {
		return err
	}
	if e.Result == "checked" && !sameObjectName(pair.Expected[len(pair.Expected)-1:], identifierWords(r.TargetTable)) {
		return fmt.Errorf("proof.reference: ссылка ведёт в %s, это не подтверждает объект %s", r.TargetTable, strings.Join(pair.Expected, " "))
	}
	for i, word := range proof.Words {
		fact := word.Fact
		onWrite := fact.Location == e.WriteEvidence.WriteAt && (fact.Value == r.Value || fact.Value == r.RowValue || fact.Value == r.Column || sameNameExpression(call, fact.Value))
		onSchema := fact.Location == r.SchemaAt && (fact.Value == r.TargetTable || fact.Value == r.TargetColumn || fact.Value == r.Column)
		if !onWrite && !onSchema {
			return fmt.Errorf("proof.words[%d]: свяжите слово с аргументом записи или проверенным внешним ключом", i)
		}
	}
	return nil
}

func sameSQLName(a, b string) bool {
	return b != "" && strings.EqualFold(strings.Trim(a, "\"`[]"), b)
}

func checkNameForeignKey(r *NameReferenceWrite, source *evidenceSource) error {
	file, line, base, err := evidenceLocation(r.SchemaAt)
	if err != nil {
		return err
	}
	body, err := source.read(file, base)
	if err != nil {
		return err
	}
	tokens := sqlStatementAt(body, line)
	table := 2
	if sqlWord(tokens, table) == "IF" {
		table += 3
	}
	if sqlWord(tokens, 0) != "CREATE" || sqlWord(tokens, 1) != "TABLE" || table >= len(tokens) || !sameSQLName(tokens[table].text, r.Table) {
		return fmt.Errorf("proof.reference.schema_at: укажите внешний ключ таблицы %s", r.Table)
	}
	for i := range tokens {
		if i+10 > len(tokens) || tokens[i].line != line {
			continue
		}
		part := tokens[i : i+10]
		if sqlWord(part, 0) == "FOREIGN" && sqlWord(part, 1) == "KEY" && sqlWord(part, 2) == "(" && sameSQLName(part[3].text, r.Column) &&
			sqlWord(part, 4) == ")" && sqlWord(part, 5) == "REFERENCES" && sameSQLName(part[6].text, r.TargetTable) &&
			sqlWord(part, 7) == "(" && sameSQLName(part[8].text, r.TargetColumn) && sqlWord(part, 9) == ")" {
			return nil
		}
	}
	return fmt.Errorf("proof.reference.schema_at: не подтверждён внешний ключ %s.%s -> %s.%s", r.Table, r.Column, r.TargetTable, r.TargetColumn)
}

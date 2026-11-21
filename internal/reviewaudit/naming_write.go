package reviewaudit

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
)

// checkNameFileWrite verifies the destination and the payload independently.
func checkNameFileWrite(e Evidence, declaration string, source *evidenceSource) error {
	w, pair := e.WriteEvidence, e.ObjectComparison
	f := w.File
	g := newQueryGoSource(source)
	fn, err := g.nameProofFunction(declaration, nameObjectSymbol(e.Object))
	if err != nil {
		return err
	}
	if pair.Proof != nil {
		if err := checkNameProofWords(pair, source); err != nil {
			return err
		}
		for _, word := range pair.Proof.Words {
			_, _, base, _ := evidenceLocation(word.Fact.Location)
			if base != fn.base {
				return fmt.Errorf("proof.words: другая ревизия")
			}
		}
	}

	original := fn
	fn, err = g.nameWriteFunction(fn, w.Calls)
	if err != nil {
		return err
	}
	write, _, err := g.call(fn, w.WriteAt, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		switch call.Sel.Name {
		case "Remove", "RemoveAll", "Rename":
			return g.namePackageSelector(fn, call, "os", call.Sel.Name)
		case "Write":
			return true
		case "WriteFile":
			return g.importPath(fn, nameIdentifier(call.X)) == "os"
		case "Fprintf":
			return g.importPath(fn, nameIdentifier(call.X)) == "fmt"
		}
		return false
	})
	if err != nil {
		return err
	}
	var path, payload ast.Expr
	var open *ast.CallExpr
	switch operation := calledName(write.Fun); operation {
	case "Remove", "RemoveAll", "Rename":
		if !sameNameExpression(write, w.Value) || f.Role != "destination" {
			return fmt.Errorf("write_evidence.file: укажите полный вызов как value и роль destination")
		}
		arity := 1
		if operation == "Rename" {
			arity = 2
		}
		if len(write.Args) == arity {
			path = write.Args[arity-1]
		}
	case "WriteFile":
		if len(write.Args) == 3 && g.namePackageSelector(fn, write.Fun, "os", "WriteFile") {
			path, payload = write.Args[0], write.Args[1]
		}
	case "Fprintf":
		open = g.nameOpenFileForWrite(fn, write)
		if open == nil || !sameNameExpression(write, w.Value) || f.Role != "destination" {
			return fmt.Errorf("write_evidence.file: укажите Fprintf на дескрипторе из os.OpenFile, полный вызов как value и роль destination")
		}
		path = open.Args[0]
	case "Write":
		open = g.nameOpenFileForWrite(fn, write)
		if open == nil {
			return fmt.Errorf("write_evidence.file: укажите Write на том же файловом дескрипторе, который вернул os.OpenFile")
		}
		path, payload = open.Args[0], write.Args[0]
		if !sameNameExpression(payload, w.Value) {
			return fmt.Errorf("write_evidence.value: укажите фактический аргумент Write")
		}
	}
	if path == nil || f.Path != w.Target || f.Path == "" || !sameNameExpression(path, f.Path) {
		return fmt.Errorf("write_evidence.file: укажите фактический путь операции с файлом")
	}
	switch f.Role {
	case "destination":
		actual, err := parser.ParseExpr(pair.Actual)
		if err != nil || nameExpression(path) != nameExpression(actual) || f.PayloadAt != "" || f.EncodeAt != "" {
			return fmt.Errorf("write_evidence.file: для destination укажите путь как actual")
		}
		if _, simple := actual.(*ast.Ident); !simple {
			openAt := ""
			if open != nil {
				openAt = nameRef(fn, g.fset.Position(open.Pos()).Line)
			}
			if pair.ActualAt != w.WriteAt && pair.ActualAt != openAt {
				return fmt.Errorf("actual_at: укажите строку вызова с путём")
			}
		} else if line, err := source.line(pair.ActualAt); err != nil || !containsIdentifier(line, pair.Actual) {
			return fmt.Errorf("actual_at: идентификатор пути не найден")
		}
		if e.Result == "checked" && !g.nameFileDestinationMatches(fn, pair, open) {
			return fmt.Errorf("запись файла не подтверждает %q как объект назначения", strings.Join(pair.Expected, " "))
		}
	case "content":
		if err := g.checkNameFileContent(e, fn, write, payload); err != nil {
			return err
		}
	default:
		return fmt.Errorf("write_evidence.file.role: укажите destination или content")
	}
	if pair.Proof != nil {
		lines := map[string]bool{w.WriteAt: true, pair.ActualAt: true}
		if open != nil {
			lines[nameRef(fn, g.fset.Position(open.Pos()).Line)] = true
		}
		if f.Role == "content" {
			lines[f.PayloadAt] = true
			lines[f.EncodeAt] = true
		}
		for _, hop := range w.Calls {
			lines[hop.At] = true
		}
		if err := checkNameWriteWordLinks(pair.Proof.Words, lines); err != nil {
			return err
		}
	}
	return checkNamePromisedWrite(e, original)
}

func (g *queryGoSource) nameFileDestinationMatches(fn queryFunc, pair *NameObjectComparison, open *ast.CallExpr) bool {
	words := nameProofObjects(pair)
	if len(words) == 0 || slices.ContainsFunc(words, func(word string) bool {
		return strings.EqualFold(word, "file") || strings.EqualFold(word, "path")
	}) {
		return true
	}
	if open == nil || nameIdentifier(open.Args[0]) != pair.Actual {
		return false
	}
	// A named destination can be established by the call that supplied
	// the path, e.g. journalPath := store.chatJournalPath(session).
	for _, stmt := range fn.node.Body.List {
		binding, ok := stmt.(*ast.AssignStmt)
		if !ok || binding.Tok != token.DEFINE || len(binding.Lhs) != 1 || len(binding.Rhs) != 1 ||
			nameIdentifier(binding.Lhs[0]) != pair.Actual || nameRef(fn, g.fset.Position(binding.Pos()).Line) != pair.ActualAt ||
			!nameValueUnchangedWithin(fn.node.Body, binding, open, pair.Actual) {
			continue
		}
		call, ok := binding.Rhs[0].(*ast.CallExpr)
		if ok {
			if selector, qualified := call.Fun.(*ast.SelectorExpr); qualified && selector.Sel.Name == "Join" && len(call.Args) > 0 {
				if pkg, imported := selector.X.(*ast.Ident); imported && pkg.Obj == nil && g.importPath(fn, pkg.Name) == "path/filepath" {
					last := nameSettingKey(call.Args[len(call.Args)-1])
					return last != "" && !strings.ContainsAny(last, "/\\") && sameObjectName(words, identifierWords(last))
				}
			}
			name := strings.ToLower(strings.Join(identifierWords(calledName(call.Fun)), " "))
			phrase := strings.ToLower(strings.Join(words, " "))
			return strings.Contains(" "+name+" ", " "+phrase+" ")
		}
	}
	return false
}

func (g *queryGoSource) nameOpenFileForWrite(fn queryFunc, write *ast.CallExpr) *ast.CallExpr {
	sel, ok := write.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	var receiver string
	formatted := sel.Sel.Name == "Fprintf"
	switch {
	case sel.Sel.Name == "Write" && len(write.Args) == 1:
		receiver = nameIdentifier(sel.X)
	case formatted && len(write.Args) >= 2 && g.namePackageSelector(fn, write.Fun, "fmt", "Fprintf"):
		receiver = nameIdentifier(write.Args[0])
	}
	if receiver == "" {
		return nil
	}
	for _, stmt := range fn.node.Body.List {
		binding, ok := stmt.(*ast.AssignStmt)
		if !ok || binding.Tok != token.DEFINE || len(binding.Lhs) != 2 || len(binding.Rhs) != 1 ||
			nameIdentifier(binding.Lhs[0]) != receiver || !nameValueUnchangedWithin(fn.node.Body, binding, write, receiver) {
			continue
		}
		open, ok := binding.Rhs[0].(*ast.CallExpr)
		if !ok || len(open.Args) != 3 {
			continue
		}
		if g.namePackageSelector(fn, open.Fun, "os", "OpenFile") {
			return open
		}
	}
	return nil
}

func (g *queryGoSource) checkNameFileContent(e Evidence, fn queryFunc, write *ast.CallExpr, recorded ast.Expr) error {
	w, pair := e.WriteEvidence, e.ObjectComparison
	f := w.File
	if f.PayloadAt == "" {
		return fmt.Errorf("write_evidence.file.payload_at: укажите строку сериализации для content; для вспомогательной записи используйте destination и actual с путём файла")
	}
	if f.EncodeAt == "" {
		return fmt.Errorf("write_evidence.file.encode_at: укажите строку кодирования для content; для вспомогательной записи используйте destination и actual с путём файла")
	}
	payload, _, err := g.call(fn, f.PayloadAt, func(expr ast.Expr) bool {
		selector, ok := expr.(*ast.SelectorExpr)
		return ok && nameFilePayloadType(g.importPath(fn, nameIdentifier(selector.X)), selector.Sel.Name) != ""
	})
	if err != nil {
		return err
	}
	encoder, _, err := g.call(fn, f.EncodeAt, func(expr ast.Expr) bool {
		selector, ok := expr.(*ast.SelectorExpr)
		return ok && selector.Sel.Name == "EncodeToMemory" && g.importPath(fn, nameIdentifier(selector.X)) == "encoding/pem"
	})
	if err != nil {
		return err
	}
	if len(payload.Args) != 1 || nameIdentifier(payload.Args[0]) != pair.Actual ||
		len(encoder.Args) != 1 || !nameFileEncodedValue(fn, encoder, write, recorded) {
		return fmt.Errorf("write_evidence.file: проследите значение actual через кодирование до содержимого файла")
	}
	block := nameIdentifier(encoder.Args[0])
	if !g.namePEMBytes(fn, encoder.Args[0], payload) &&
		(block == "" || !g.namePEMBlock(fn, block, payload, encoder)) {
		return fmt.Errorf("write_evidence.file: Bytes блока PEM должны содержать результат указанной сериализации")
	}
	sel := payload.Fun.(*ast.SelectorExpr)
	actual := nameFilePayloadType(g.importPath(fn, nameIdentifier(sel.X)), sel.Sel.Name)
	if e.Result == "checked" && !sameObjectName(nameContentWords(pair), identifierWords(actual)) {
		return fmt.Errorf("write_evidence.file.role: записанное содержимое %q не подтверждает %q; для вспомогательной записи используйте destination", actual, strings.Join(pair.Expected, " "))
	}
	return nil
}

func nameFileEncodedValue(fn queryFunc, encoder, write *ast.CallExpr, payload ast.Expr) bool {
	if payload == encoder {
		return true
	}
	value := nameIdentifier(payload)
	for _, stmt := range fn.node.Body.List {
		binding, ok := stmt.(*ast.AssignStmt)
		if ok && binding.Tok == token.DEFINE && len(binding.Lhs) == 1 && len(binding.Rhs) == 1 &&
			nameIdentifier(binding.Lhs[0]) == value && binding.Rhs[0] == encoder &&
			nameValueUnchangedWithin(fn.node.Body, binding, write, value) {
			return true
		}
	}
	return false
}

func (g *queryGoSource) namePEMBytes(fn queryFunc, expr ast.Expr, payload *ast.CallExpr) bool {
	address, ok := expr.(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}
	block, ok := address.X.(*ast.CompositeLit)
	if !ok {
		return false
	}
	typ, ok := block.Type.(*ast.SelectorExpr)
	if !ok || typ.Sel.Name != "Block" || g.importPath(fn, nameIdentifier(typ.X)) != "encoding/pem" {
		return false
	}
	return slices.ContainsFunc(block.Elts, func(expr ast.Expr) bool {
		field, ok := expr.(*ast.KeyValueExpr)
		return ok && nameIdentifier(field.Key) == "Bytes" && field.Value == payload
	})
}

func (g *queryGoSource) namePEMBlock(fn queryFunc, name string, payload, encoder *ast.CallExpr) bool {
	for _, stmt := range fn.node.Body.List {
		binding, ok := stmt.(*ast.AssignStmt)
		if !ok || binding.Tok != token.DEFINE || len(binding.Lhs) != 1 || len(binding.Rhs) != 1 ||
			nameIdentifier(binding.Lhs[0]) != name {
			continue
		}
		if g.namePEMBytes(fn, binding.Rhs[0], payload) && nameValueUnchanged(fn.node.Body, binding, encoder, name) {
			return true
		}
	}
	return false
}

func nameFilePayloadType(path, function string) string {
	if path == "crypto/x509" && function == "MarshalPKCS1PrivateKey" {
		return "RSAKey"
	}
	return ""
}

func nameContentWords(pair *NameObjectComparison) []string {
	if pair.Proof == nil {
		return pair.Expected
	}
	var words []string
	for _, w := range pair.Proof.Words {
		if w.Role == "object" || w.Role == "format" {
			words = append(words, w.Word)
		}
	}
	return words
}

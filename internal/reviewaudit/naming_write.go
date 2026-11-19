package reviewaudit

import (
	"fmt"
	"go/ast"
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
		return ok && call.Sel.Name == "WriteFile" && g.importPath(fn, nameIdentifier(call.X)) == "os"
	})
	if err != nil {
		return err
	}
	if len(write.Args) != 3 || f.Path != w.Target || f.Path == "" || !sameNameExpression(write.Args[0], f.Path) {
		return fmt.Errorf("write_evidence.file: укажите фактический путь в первом аргументе записи")
	}
	switch f.Role {
	case "destination":
		if pair.Actual != f.Path || f.PayloadAt != "" || f.EncodeAt != "" {
			return fmt.Errorf("write_evidence.file: для destination укажите путь как actual")
		}
		if e.Result == "checked" && len(nameProofObjects(pair)) != 0 &&
			!slices.ContainsFunc(nameProofObjects(pair), func(word string) bool { return strings.EqualFold(word, "file") || strings.EqualFold(word, "path") }) {
			return fmt.Errorf("запись файла не подтверждает %q как объект назначения", strings.Join(pair.Expected, " "))
		}
	case "content":
		if err := g.checkNameFileContent(e, fn, write); err != nil {
			return err
		}
	default:
		return fmt.Errorf("write_evidence.file.role: укажите destination или content")
	}
	if pair.Proof != nil {
		lines := map[string]bool{w.WriteAt: true, pair.ActualAt: true}
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

func (g *queryGoSource) checkNameFileContent(e Evidence, fn queryFunc, write *ast.CallExpr) error {
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
		len(encoder.Args) != 1 || !nameFileEncodedValue(fn, encoder, write) {
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

func nameFileEncodedValue(fn queryFunc, encoder, write *ast.CallExpr) bool {
	if write.Args[1] == encoder {
		return true
	}
	value := nameIdentifier(write.Args[1])
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

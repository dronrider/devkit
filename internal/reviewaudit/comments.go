package reviewaudit

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const commentRule = "common-style:STYLE-015"

type commentBlock struct {
	line         int
	text         string
	fragments    []string
	function     string
	functionLine int
}

func addCommentTargets(rule *Rule, name string, current, previous goTargetFile, changedFunctions map[int]bool) {
	if ast.IsGenerated(current.file) {
		return
	}
	previousBlocks := make(map[[2]string]int)
	for _, block := range previous.comments() {
		previousBlocks[[2]string{block.function, block.text}]++
	}
	for _, block := range current.comments() {
		key := [2]string{block.function, block.text}
		unchanged := previousBlocks[key] > 0
		if unchanged {
			previousBlocks[key]--
		}
		if unchanged && !changedFunctions[block.functionLine] {
			continue
		}
		location := fmt.Sprintf("%s:%d", name, block.line)
		rule.Targets = append(rule.Targets, Target{
			Object: location, Location: location, Symbol: block.function, CommentFragments: block.fragments,
		})
		addFileObject(rule.Files, name, location)
	}
}

func (g goTargetFile) comments() []commentBlock {
	if g.file == nil {
		return nil
	}
	var functions []*ast.FuncDecl
	for _, decl := range g.file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil {
			functions = append(functions, fn)
		}
	}
	var blocks []commentBlock
	for _, group := range g.file.Comments {
		var prose []*ast.Comment
		for _, comment := range group.List {
			body := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if body == "+build" || strings.HasPrefix(body, "+build ") ||
				body == "nolint" || strings.HasPrefix(body, "nolint:") {
				continue
			}
			prose = append(prose, comment)
		}
		body := (&ast.CommentGroup{List: prose}).Text()
		text := compactComment(body)
		if text == "" {
			continue
		}
		block := commentBlock{
			line: g.fset.PositionFor(group.Pos(), false).Line, text: text, fragments: commentFragments(body),
		}
		for _, fn := range functions {
			if fn.Doc == group || (fn.Pos() <= group.Pos() && group.Pos() < fn.End()) {
				block.function = functionKey(fn)
				block.functionLine = g.fset.Position(fn.Pos()).Line
				break
			}
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func compactComment(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

var (
	commentListItem     = regexp.MustCompile(`^\s*(?:[-*+]\s+|[0-9]+[.)]\s+)`)
	commentAbbreviation = regexp.MustCompile(`(?i)(?:^|[\s(])(?:др|пр|см|напр|рис|т|т\.\s*[едпкн]|e\.g|i\.e|etc|vs|mr|mrs|ms|dr|fig|no)\.$`)
	commentNamedSource  = regexp.MustCompile("(?i)(?:^|[ (])(?:из|from) +[`«]?([a-z][a-z0-9]*(?:[._][a-z0-9_]+)+)")
)

func commentFragments(body string) []string {
	var fragments []string
	var paragraph []string
	flush := func() {
		if len(paragraph) != 0 {
			fragments = append(fragments, commentSentences(compactComment(strings.Join(paragraph, " ")))...)
			paragraph = paragraph[:0]
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if marker := commentListItem.FindStringIndex(line); marker != nil {
			flush()
			line = line[marker[1]:]
		}
		paragraph = append(paragraph, line)
	}
	flush()
	return fragments
}

func commentSentences(text string) []string {
	var fragments []string
	start, codeDelimiter := 0, 0
	for i := 0; i < len(text); {
		if text[i] == '`' {
			width := len(text[i:]) - len(strings.TrimLeft(text[i:], "`"))
			switch codeDelimiter {
			case 0:
				if strings.Contains(text[i+width:], strings.Repeat("`", width)) {
					codeDelimiter = width
				}
			case width:
				codeDelimiter = 0
			}
			i += width
			continue
		}
		end := i + 1
		if codeDelimiter == 0 && strings.ContainsRune(".!?", rune(text[i])) {
			for end < len(text) && strings.ContainsRune(".!?", rune(text[end])) {
				end++
			}
			if end == len(text) || text[end] == ' ' {
				next, _ := utf8.DecodeRuneInString(strings.TrimSpace(text[end:]))
				if text[i] != '.' || end != i+1 || unicode.IsUpper(next) || !commentAbbreviation.MatchString(text[start:end]) {
					fragments = append(fragments, strings.TrimSpace(text[start:end]))
					start = end
				}
			}
		}
		i = end
	}
	if rest := strings.TrimSpace(text[start:]); rest != "" {
		fragments = append(fragments, rest)
	}
	return fragments
}

type goCommentEvidence struct {
	blocks     map[int]commentBlock
	signatures map[int]map[string]bool
}

func readGoCommentEvidence(source *evidenceSource, ref string, files map[string]goCommentEvidence) (goCommentEvidence, error) {
	path, _, base, err := evidenceLocation(ref)
	if err != nil {
		return goCommentEvidence{}, err
	}
	key := ref[:strings.LastIndexByte(ref, ':')]
	if evidence, ok := files[key]; ok {
		return evidence, nil
	}
	file, err := readGoTargetFile(source, path, base)
	if err != nil {
		return goCommentEvidence{}, err
	}
	evidence := goCommentEvidence{blocks: make(map[int]commentBlock), signatures: file.signatureLines()}
	for _, block := range file.comments() {
		evidence.blocks[block.line] = block
	}
	files[key] = evidence
	return evidence, nil
}

func (g goTargetFile) signatureLines() map[int]map[string]bool {
	lines := make(map[int]map[string]bool)
	if g.file == nil {
		return lines
	}
	ast.Inspect(g.file, func(node ast.Node) bool {
		var signature *ast.FuncType
		var body *ast.BlockStmt
		switch fn := node.(type) {
		case *ast.FuncDecl:
			signature, body = fn.Type, fn.Body
		case *ast.FuncLit:
			signature, body = fn.Type, fn.Body
		default:
			return true
		}
		first := g.fset.PositionFor(signature.Pos(), false).Line
		last := g.fset.PositionFor(signature.End(), false).Line
		if body != nil {
			last = g.fset.PositionFor(body.Lbrace, false).Line
			if len(body.List) > 0 && g.fset.PositionFor(body.List[0].Pos(), false).Line == last {
				last--
			}
		}
		for line := first; line <= last; line++ {
			lines[line] = make(map[string]bool)
		}
		for _, fields := range []*ast.FieldList{signature.Params, signature.Results} {
			if fields == nil {
				continue
			}
			for _, field := range fields.List {
				ast.Inspect(field.Type, func(node ast.Node) bool {
					switch node := node.(type) {
					case *ast.SelectorExpr:
						line := g.fset.PositionFor(node.Sel.Pos(), false).Line
						if lines[line] != nil {
							lines[line][node.Sel.Name] = true
						}
						return false
					case *ast.Ident:
						line := g.fset.PositionFor(node.Pos(), false).Line
						if lines[line] != nil {
							lines[line][node.Name] = true
						}
					}
					return true
				})
			}
		}
		return true
	})
	return lines
}

func checkComments(root, reportPath, output string, report Report) error {
	for _, rule := range report.Rules {
		if rule.ID != commentRule || len(rule.Evidence) == 0 {
			continue
		}
		files, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		defer files.Close()
		source := &evidenceSource{root: root, reportPath: reportPath, output: output, base: report.BaseSHA, files: files, cache: make(map[string][]string)}
		goFiles := make(map[string]goCommentEvidence)
		var issues []error
		for _, e := range rule.Evidence {
			if err := checkCommentEvidence(e, source, goFiles); err != nil {
				issues = append(issues, fmt.Errorf("%s / %s: %w", commentRule, e.Object, err))
			}
		}
		return errors.Join(issues...)
	}
	return nil
}

func checkCommentEvidence(e Evidence, source *evidenceSource, goFiles map[string]goCommentEvidence) error {
	if e.Result == "not_applicable" {
		return fmt.Errorf("для блока комментария укажите checked, violated или pending")
	}
	quote := compactComment(e.CommentQuote)
	if quote == "" {
		return fmt.Errorf("укажите точную цитату в comment_quote")
	}
	block, err := readCommentBlock(source, e.Object, goFiles)
	if err != nil {
		return err
	}
	if !strings.Contains(block.text, quote) {
		return fmt.Errorf("comment_quote не найдена в указанном комментарии")
	}
	if e.Location != "" || e.Result != "pending" {
		proof, err := source.line(e.Location)
		if err != nil {
			return fmt.Errorf("доказательство: %w", err)
		}
		if e.Location == e.Object && !commentLineHasCode(proof) {
			return fmt.Errorf("укажите доказательство за пределами самого комментария")
		}
	}
	return checkCommentClaims(e, block, source, goFiles)
}

func readCommentBlock(source *evidenceSource, object string, goFiles map[string]goCommentEvidence) (commentBlock, error) {
	_, line, _, err := evidenceLocation(object)
	if err != nil {
		return commentBlock{}, err
	}
	var block commentBlock
	if isGoRef(object) {
		file, err := readGoCommentEvidence(source, object, goFiles)
		if err != nil {
			return commentBlock{}, err
		}
		block = file.blocks[line]
	} else {
		if _, err := source.line(object); err != nil {
			return commentBlock{}, err
		}
		key := object[:strings.LastIndexByte(object, ':')]
		body := plainCommentBody(source.cache[key], line-1)
		block = commentBlock{text: compactComment(body), fragments: commentFragments(body)}
	}
	if len(block.fragments) == 0 {
		return commentBlock{}, fmt.Errorf("укажите начало блока комментария")
	}
	return block, nil
}

func plainCommentBody(lines []string, start int) string {
	first := strings.TrimLeftFunc(lines[start], unicode.IsSpace)
	if strings.HasPrefix(first, "/*") {
		var prose []string
		for i, line := range lines[start:] {
			if i == 0 {
				line = strings.TrimPrefix(first, "/*")
			}
			line, _, closed := strings.Cut(line, "*/")
			prose = append(prose, strings.TrimPrefix(strings.TrimSpace(line), "*"))
			if closed {
				return strings.Join(prose, "\n")
			}
		}
		return ""
	}
	for _, marker := range []string{"//", "#", "--"} {
		if !strings.HasPrefix(first, marker) {
			continue
		}
		prefix := lines[start][:len(lines[start])-len(first)] + marker
		if start > 0 && strings.HasPrefix(lines[start-1], prefix) {
			return ""
		}
		var prose []string
		for _, line := range lines[start:] {
			text, ok := strings.CutPrefix(line, prefix)
			if !ok {
				break
			}
			prose = append(prose, text)
		}
		return strings.Join(prose, "\n")
	}
	return ""
}

func commentLineHasCode(line string) bool {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "/*") {
		_, rest, closed := strings.Cut(line, "*/")
		return closed && strings.TrimSpace(rest) != ""
	}
	return !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "--")
}

func checkCommentClaims(e Evidence, block commentBlock, source *evidenceSource, goFiles map[string]goCommentEvidence) error {
	claims := e.Facts["claim"]
	quotes := make([]string, len(claims))
	for i, claim := range claims {
		quotes[i] = compactComment(claim.Quote)
	}
	if !slices.Equal(quotes, block.fragments) {
		var missing []error
		remaining := slices.Clone(quotes)
		for _, fragment := range block.fragments {
			if i := slices.Index(remaining, fragment); i >= 0 {
				remaining = slices.Delete(remaining, i, i+1)
			} else {
				missing = append(missing, fmt.Errorf("facts.claim: нет вывода по фрагменту %q", fragment))
			}
		}
		if len(missing) > 0 {
			return errors.Join(missing...)
		}
		return fmt.Errorf("facts.claim: нужна одна полная цитата на фрагмент в порядке исходного текста")
	}
	result := "checked"
	var claimIssues []error
	goCode := newQueryGoSource(source)
	for i, claim := range claims {
		quote := quotes[i]
		if strings.TrimSpace(claim.Text) == "" {
			return fmt.Errorf("facts.claim: укажите основание вывода по фрагменту %q", quote)
		}
		switch claim.Verdict {
		case "pending":
			if result == "checked" {
				result = "pending"
			}
			if claim.Location == "" {
				continue
			}
		case "violated":
			result = "violated"
		case "checked":
		default:
			return fmt.Errorf("facts.claim: укажите checked, violated или pending для %q", quote)
		}
		proof, err := source.line(claim.Location)
		if err != nil {
			return fmt.Errorf("facts.claim: %w", err)
		}
		if claim.Verdict == "pending" {
			continue
		}
		if strings.TrimSpace(proof) == "" || !commentLineHasCode(proof) {
			return fmt.Errorf("facts.claim: нужна строка реализации для %q", quote)
		}
		if isGoRef(claim.Location) {
			file, err := readGoCommentEvidence(source, claim.Location, goFiles)
			if err != nil {
				return fmt.Errorf("facts.claim: %w", err)
			}
			_, line, _, _ := evidenceLocation(claim.Location)
			if types := file.signatures[line]; types != nil && (!types[claim.Value] || !containsIdentifier(quote, claim.Value)) {
				claimIssues = append(claimIssues, fmt.Errorf("facts.claim: %s: сигнатура не подтверждает %q. Укажите условие, вызов, присваивание или возврат из реализации", claim.Location, quote))
				continue
			}
		}
		if claim.Verdict == "checked" {
			if err := checkConfirmedCommentClaim(claim, quote, proof, goCode); err != nil {
				claimIssues = append(claimIssues, fmt.Errorf("facts.claim: %q: %w", quote, err))
			}
		}
	}
	if result != e.Result {
		return fmt.Errorf("facts.claim: по выводам фрагментов результат блока должен быть %s", result)
	}
	return errors.Join(claimIssues...)
}

func checkConfirmedCommentClaim(claim ReviewFact, quote, proof string, goCode *queryGoSource) error {
	note := strings.ToLower(compactComment(claim.Text))
	note = strings.TrimSpace(strings.TrimPrefix(note, "проверено:"))
	if suffix, repeated := strings.CutPrefix(note, strings.ToLower(quote)); repeated {
		note = strings.TrimSpace(suffix)
		ref := strings.TrimSpace(strings.TrimPrefix(note, "реализация:"))
		if note == "" || (ref != note && !strings.ContainsAny(ref, " \t") && isEvidenceLocation(ref)) {
			return fmt.Errorf("пересказ цитаты не подтверждает checked. Укажите вход, условие или наблюдаемый эффект")
		}
	}
	anchor := compactComment(claim.Value)
	if anchor == "" || !containsIdentifier(compactComment(proof), anchor) {
		return fmt.Errorf("для checked укажите в value выражение или идентификатор из строки реализации %s", claim.Location)
	}
	if match := commentNamedSource.FindStringSubmatch(quote); match != nil {
		return checkCommentSourceClaim(claim, match[1], goCode)
	}
	return nil
}

func checkCommentSourceClaim(claim ReviewFact, name string, goCode *queryGoSource) error {
	if strings.TrimSpace(claim.Case) == "" || claim.Via == "" {
		return fmt.Errorf("для источника %q укажите конкретный вход в case, чтение источника в via и использование значения в location", name)
	}
	sourceLine, err := goCode.source.line(claim.Via)
	if err != nil {
		return fmt.Errorf("via: %w", err)
	}
	// A field can be read through an alias or on a line within a record initializer.
	_, field, qualified := strings.Cut(name, ".")
	sourceLine = strings.ToLower(sourceLine)
	if !commentLineHasCode(sourceLine) ||
		(!containsIdentifier(sourceLine, strings.ToLower(name)) &&
			(!qualified || !containsIdentifier(sourceLine, strings.ToLower(field)))) {
		return fmt.Errorf("via %s: строка чтения должна содержать %q", claim.Via, name)
	}
	if isGoRef(claim.Location) {
		return goCode.checkCommentValueUse(claim.Location, compactComment(claim.Value))
	}
	return nil
}

func (g *queryGoSource) checkCommentValueUse(ref, value string) error {
	file, line, base, err := evidenceLocation(ref)
	if err != nil {
		return err
	}
	tree, err := g.parseFile(file, base)
	if err != nil {
		return err
	}
	for node := range ast.Preorder(tree) {
		if g.fset.Position(node.Pos()).Line != line {
			continue
		}
		switch node.(type) {
		case *ast.AssignStmt, *ast.ReturnStmt, *ast.CallExpr, *ast.KeyValueExpr:
			if containsIdentifier(compactComment(g.expression(node)), value) {
				return nil
			}
		}
	}
	return fmt.Errorf("location %s: покажите, где значение из источника присваивается, передаётся или возвращается", ref)
}

func (g *queryGoSource) expression(node ast.Node) string {
	var text strings.Builder
	if err := format.Node(&text, g.fset, node); err != nil {
		return ""
	}
	return text.String()
}

func isEvidenceLocation(ref string) bool {
	_, _, _, err := evidenceLocation(ref)
	return err == nil
}

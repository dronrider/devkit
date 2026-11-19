package reviewaudit

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SQLFilterLiteral resolves a literal still present in a query predicate.
type SQLFilterLiteral struct {
	Source      string `json:"source"`
	SourceQuote string `json:"source_quote,omitempty"`
}

type sqlLiteralRef struct {
	location string
	quote    string
}

type sqlLiteralCheck struct {
	values     map[string]string
	unresolved bool
}

func checkSQLFilterLiterals(entry Evidence, source *evidenceSource) (sqlLiteralCheck, error) {
	result := sqlLiteralCheck{values: make(map[string]string)}
	anchor := entry.Object
	path, _, _, err := evidenceLocation(anchor)
	if err != nil || !strings.EqualFold(filepath.Ext(path), ".sql") {
		anchor = entry.Location
		path, _, _, _ = evidenceLocation(anchor)
	}
	var refs []sqlLiteralRef
	filterScopes := make(map[string]bool)
	if strings.EqualFold(filepath.Ext(path), ".sql") {
		refs, err = queryLiteralRefs(anchor, source)
		if err != nil {
			return result, fmt.Errorf("parameters: %w", err)
		}
		filterScopes[path] = true
	}
	for _, filter := range entry.Facts["filter"] {
		filterPath, _, _, err := evidenceLocation(filter.Location)
		if err == nil && strings.EqualFold(filepath.Ext(filterPath), ".sql") && !filterScopes[filterPath] {
			more, err := queryLiteralRefs(filter.Location, source)
			if err != nil {
				return result, fmt.Errorf("parameters: %w", err)
			}
			refs = append(refs, more...)
			filterScopes[filterPath] = true
		}
	}
	required := make(map[sqlLiteralRef]int)
	predicates := make(map[string]bool)
	for _, ref := range refs {
		required[ref]++
		predicates[strings.TrimPrefix(ref.location, "base@")] = true
	}
	for _, filter := range entry.Facts["filter"] {
		if filter.SQLLiteral == nil {
			continue
		}
		ref := sqlLiteralRef{filter.Location, filter.Quote}
		if len(filterScopes) > 0 && required[ref] == 0 {
			return result, fmt.Errorf("parameters: %s: %q не соответствует отдельному литералу отбора в этом запросе", ref.location, ref.quote)
		}
		if predicates[strings.TrimPrefix(filter.Related, "base@")] {
			return result, fmt.Errorf("parameters: %s: related должен ссылаться на источник значения за пределами условий этого запроса", filter.Location)
		}
		required[ref]--
		value, err := checkFilterLiteral(entry.Result, filter, source)
		if err != nil {
			return result, fmt.Errorf("parameters: %s %s: %w", ref.location, ref.quote, err)
		}
		switch filter.SQLLiteral.Source {
		case "constant":
			if old, exists := result.values[filter.Parameter]; exists && old != value {
				return result, fmt.Errorf("parameters: разные значения литералов используют параметр %s", filter.Parameter)
			}
			result.values[filter.Parameter] = value
		case "pending":
			result.unresolved = true
		}
	}
	// Report the first missing literal in SQL order.
	for _, ref := range refs {
		if required[ref] > 0 {
			return result, fmt.Errorf("parameters: %s: для литерала %s нужна отдельная запись facts.filter.sql_literal", ref.location, ref.quote)
		}
	}
	return result, nil
}

func checkFilterLiteral(status string, filter ReviewFact, source *evidenceSource) (string, error) {
	literal := filter.SQLLiteral
	if !sqlValue.MatchString(filter.Quote) {
		return "", fmt.Errorf("quote должна содержать один литерал SQL")
	}
	line, err := source.line(filter.Location)
	if err != nil {
		return "", err
	}
	if err := checkFactQuote(filter.Quote, line); err != nil {
		return "", err
	}
	value := filter.Quote
	if strings.HasPrefix(value, "'") {
		value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	if status == "not_applicable" {
		return "", fmt.Errorf("отбор по литералу требует проверки parameters")
	}
	switch literal.Source {
	case "pending":
		if (status != "pending" && status != "violated") || filter.Parameter != "" {
			return "", fmt.Errorf("для открытого источника литерала нужен pending или уже доказанный violated без нового параметра для этого литерала")
		}
	case "constant", "fixed":
		if literal.Source == "fixed" && !fixedFilterSource(filter.Related) {
			return "", fmt.Errorf("для fixed укажите строку схемы SQL или контракта в документации либо постановке")
		}
		if strings.TrimPrefix(filter.Location, "base@") == strings.TrimPrefix(filter.Related, "base@") ||
			!quoteContainsSQLValue(literal.SourceQuote, value, filter.Quote) {
			return "", fmt.Errorf("related и source_quote должны подтвердить это значение вне проверяемого условия SQL")
		}
		if err := checkFilterSourceQuote(filter, source); err != nil {
			return "", fmt.Errorf("related и source_quote должны подтвердить это значение: %w", err)
		}
		if literal.Source == "constant" {
			if status != "violated" || filter.Parameter == "" {
				return "", fmt.Errorf("литерал дублирует константу, укажите parameters: violated и имя нового параметра")
			}
		} else if filter.Parameter != "" {
			return "", fmt.Errorf("фиксированное условие SQL не задаёт новый параметр")
		}
	default:
		return "", fmt.Errorf("укажите sql_literal.source: constant, fixed или pending")
	}
	return value, nil
}

func checkFilterSourceQuote(filter ReviewFact, source *evidenceSource) error {
	quote := filter.SQLLiteral.SourceQuote
	if filter.SQLLiteral.Source == "fixed" && strings.HasPrefix(filter.Related, "task:") {
		return checkContractFact(ReviewFact{Location: filter.Related, Quote: quote}, source)
	}
	line, err := source.line(filter.Related)
	if err != nil {
		return err
	}
	return checkFactQuote(quote, line)
}

func fixedFilterSource(location string) bool {
	if strings.HasPrefix(location, "task:") {
		return true
	}
	path, _, _, err := evidenceLocation(location)
	if err != nil {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sql", ".md", ".mdx", ".rst", ".adoc", ".txt":
		return true
	}
	return false
}

func quoteContainsSQLValue(quote, value, spelling string) bool {
	if strings.HasPrefix(spelling, "'") {
		return strings.Contains(quote, spelling) || strings.Contains(quote, strconv.Quote(value)) ||
			strings.Contains(quote, "'"+strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", `\'`)+"'")
	}
	return containsIdentifier(strings.ToLower(quote), strings.ToLower(value))
}

// Tokenize the SQL source to find literal operands in predicates. This keeps
// review independent of the application's language and query generator.
const sqlString = `'(?:''|[^'])*'`
const sqlNumber = `[-+]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?`

var sqlValue = regexp.MustCompile(`^(?:` + sqlString + `|` + sqlNumber + `|(?i:true|false))$`)
var sqlTokens = regexp.MustCompile(`--[^\n]*|(?s:/\*.*?\*/)|` + sqlString + `|"(?:""|[^"])*"|` + "`[^`]*`" +
	`|\[[^\]]*\]|::|[$:@][\pL\pN_]+|\?[0-9]*|` + sqlNumber + `|[\pL_][\pL\pN_$]*|!=|<>|<=|>=|==|[^\s]`)

type sqlToken struct {
	text string
	line int
}

func queryLiteralRefs(anchor string, source *evidenceSource) ([]sqlLiteralRef, error) {
	path, line, fromBase, err := evidenceLocation(anchor)
	if err != nil {
		return nil, err
	}
	body, err := source.read(path, fromBase)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", anchor, err)
	}
	tokens := sqlStatementAt(body, line)
	refPath := path
	if fromBase {
		refPath = "base@" + path
	}
	var refs []sqlLiteralRef
	type scope struct{ predicate, list bool }
	scopes := []scope{{}}
	for i, token := range tokens {
		state := &scopes[len(scopes)-1]
		before, after := sqlWord(tokens, i-1), sqlWord(tokens, i+1)
		switch strings.ToUpper(token.text) {
		case "(":
			scopes = append(scopes, scope{predicate: state.predicate, list: before == "IN"})
		case ")":
			if len(scopes) > 1 {
				scopes = scopes[:len(scopes)-1]
			}
		case "WHERE", "ON", "HAVING", "QUALIFY":
			state.predicate = true
		case "SELECT", "FROM", "JOIN", "ORDER", "GROUP", "LIMIT", "OFFSET", "RETURNING", "SET", "UNION":
			state.predicate = false
		default:
			between := before == "BETWEEN" || (before == "AND" && sqlWord(tokens, i-3) == "BETWEEN")
			listValue := state.list && (before == "(" || before == ",") && (after == "," || after == ")")
			negated := before == "NOT" && sqlWord(tokens, i-2) == "IS"
			if state.predicate && sqlValue.MatchString(token.text) &&
				(sqlComparison(before) || sqlComparison(after) || between || listValue || negated) {
				refs = append(refs, sqlLiteralRef{fmt.Sprintf("%s:%d", refPath, token.line), token.text})
			}
		}
	}
	return refs, nil
}

func sqlStatementAt(body string, anchor int) []sqlToken {
	line, previous := 1, 0
	var tokens []sqlToken
	for _, match := range sqlTokens.FindAllStringIndex(body, -1) {
		start, end := match[0], match[1]
		line += strings.Count(body[previous:start], "\n")
		token := body[start:end]
		switch {
		case token == ";":
			if line >= anchor && len(tokens) > 0 {
				return tokens
			}
			tokens = nil
		case !strings.HasPrefix(token, "--") && !strings.HasPrefix(token, "/*"):
			if strings.TrimSpace(token) != "" {
				tokens = append(tokens, sqlToken{token, line})
			}
		}
		line += strings.Count(token, "\n")
		previous = end
	}
	return tokens
}

func sqlWord(tokens []sqlToken, at int) string {
	if at < 0 || at >= len(tokens) {
		return ""
	}
	return strings.ToUpper(tokens[at].text)
}

func sqlComparison(word string) bool {
	switch word {
	case "=", "==", "!=", "<>", "<", "<=", ">", ">=", "LIKE", "ILIKE", "GLOB", "REGEXP", "IS":
		return true
	}
	return false
}

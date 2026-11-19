package reviewaudit

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"slices"
	"strings"
)

type Target struct {
	Object           string   `json:"object"`
	Symbol           string   `json:"symbol"`
	Location         string   `json:"location"`
	CommentFragments []string `json:"comment_fragments,omitempty"`
}

type QueryEntryTarget struct {
	Target
	Calls []string `json:"calls"`
}

type FileReview struct {
	Path    string   `json:"path"`
	Objects []string `json:"objects,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

func collectTargets(root, reportPath, base, output string, rules []Rule) error {
	naming := slices.IndexFunc(rules, func(rule Rule) bool { return rule.ID == namingRule })
	queries := slices.IndexFunc(rules, func(rule Rule) bool { return rule.ID == queryRule })
	comments := slices.IndexFunc(rules, func(rule Rule) bool { return rule.ID == commentRule })
	if naming < 0 && queries < 0 && comments < 0 {
		return nil
	}
	files, err := changedFiles(root, reportPath, base, output)
	if err != nil {
		return err
	}
	for _, index := range []int{naming, queries, comments} {
		if index >= 0 {
			rules[index].Files = slices.Clone(files)
			rules[index].Targets = nil
		}
	}
	if queries >= 0 {
		rules[queries].QueryEntries = nil
	}
	changed := make(map[string]bool)
	for _, file := range files {
		if goTargetPath(file.Path) {
			changed[file.Path] = true
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return collectGoTargets(root, reportPath, base, output, rules, naming, queries, comments, changed)
}

func changedFiles(root, reportPath, base, output string) ([]FileReview, error) {
	diff, err := gitRaw(root, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", base)
	if err != nil {
		return nil, err
	}
	untracked, err := gitRaw(root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []FileReview
	for name := range pathSet(diff + untracked) {
		if name != output && !excluded(root, reportPath, name) {
			files = append(files, FileReview{Path: name})
		}
	}
	slices.SortFunc(files, func(a, b FileReview) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

func collectGoTargets(root, reportPath, base, output string, rules []Rule, naming, queries, comments int, changed map[string]bool) error {
	paths, err := gitRaw(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go")
	if err != nil {
		return err
	}
	basePaths, err := gitRaw(root, "ls-tree", "-r", "--name-only", "-z", base)
	if err != nil {
		return err
	}
	files, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer files.Close()
	source := evidenceSource{root: root, reportPath: reportPath, output: output, base: base, files: files, names: pathSet(paths)}
	previous := pathSet(basePaths)
	allPaths := strings.Split(paths, "\x00")
	slices.Sort(allPaths)
	allPaths = slices.Compact(allPaths)

	var declarations []targetFunction
	invoked := map[string]map[string]bool{}
	changedMethod := false
	for _, name := range allPaths {
		if !changed[name] {
			continue
		}
		current, err := readGoTargetFile(&source, name, false)
		if err != nil {
			return err
		}
		if current.file == nil {
			continue
		}
		var previousFile goTargetFile
		old := make(map[[2]string]int)
		if previous[name] {
			previousFile, err = readGoTargetFile(&source, name, true)
			if err != nil {
				return err
			}
			for _, fn := range previousFile.functions(name, true, true) {
				old[[2]string{fn.key, fn.content}]++
			}
		}
		var changedFunctions map[int]bool
		if comments >= 0 {
			changedFunctions = make(map[int]bool)
		}
		for _, fn := range current.functions(name, false, true) {
			key := [2]string{fn.key, fn.content}
			fn.changed = old[key] == 0
			if !fn.changed {
				old[key]--
			}
			if fn.changed && !fn.generated {
				if changedFunctions != nil {
					changedFunctions[fn.line] = true
				}
				if naming >= 0 {
					target := fn.target
					target.Object += "#" + fn.key
					rules[naming].Targets = append(rules[naming].Targets, target)
					addFileObject(rules[naming].Files, name, target.Object)
				}
				if !strings.HasSuffix(name, "_test.go") {
					changedMethod = changedMethod || fn.collection
					for _, call := range fn.calls {
						if invoked[call] == nil {
							invoked[call] = make(map[string]bool)
						}
						invoked[call][name] = true
					}
				}
			}
			if !strings.HasSuffix(name, "_test.go") {
				declarations = append(declarations, fn)
			}
		}
		if comments >= 0 {
			addCommentTargets(&rules[comments], name, current, previousFile, changedFunctions)
		}
	}
	if queries < 0 || (len(invoked) == 0 && !changedMethod) {
		return nil
	}
	for _, name := range allPaths {
		if changed[name] || name == output || !goTargetPath(name) || strings.HasSuffix(name, "_test.go") || excluded(root, reportPath, name) {
			continue
		}
		file, err := readGoTargetFile(&source, name, false)
		if err != nil {
			return err
		}
		declarations = append(declarations, file.functions(name, false, false)...)
	}
	rule := &rules[queries]
	rule.Targets = queryTargets(declarations, invoked)
	rule.QueryEntries = queryEntryTargets(declarations, rule.Targets)
	for _, target := range rule.Targets {
		name, _, _, err := evidenceLocation(target.Location)
		if err != nil {
			return err
		}
		addFileObject(rule.Files, name, target.Object)
		for caller := range invoked[target.Symbol] {
			if caller != name {
				addFileObject(rule.Files, caller, target.Object)
			}
		}
	}
	return nil
}

func addFileObject(files []FileReview, name, object string) {
	i, found := slices.BinarySearchFunc(files, name, func(file FileReview, name string) int {
		return strings.Compare(file.Path, name)
	})
	if found {
		files[i].Objects = append(files[i].Objects, object)
	}
}

func pathSet(out string) map[string]bool {
	paths := make(map[string]bool)
	for _, name := range strings.Split(out, "\x00") {
		if name != "" {
			paths[name] = true
		}
	}
	return paths
}

func goTargetPath(name string) bool {
	return path.Ext(name) == ".go" && !strings.HasPrefix(name, "vendor/") && !strings.Contains(name, "/vendor/")
}

type targetFunction struct {
	target     Target
	line       int
	key        string
	content    string
	generated  bool
	changed    bool
	collection bool
	driver     bool
	calls      []string
	selectors  map[string]bool
	loopCalls  map[string]bool
}

type goTargetFile struct {
	file *ast.File
	fset *token.FileSet
}

func readGoTargetFile(source *evidenceSource, name string, fromBase bool) (goTargetFile, error) {
	if !fromBase {
		info, err := source.files.Lstat(name)
		if err == nil && !info.Mode().IsRegular() {
			return goTargetFile{}, nil
		}
	}
	body, err := source.read(name, fromBase)
	if os.IsNotExist(err) {
		return goTargetFile{}, nil
	}
	if err != nil {
		return goTargetFile{}, fmt.Errorf("перечень объектов %s: %w", name, err)
	}
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, name, body, parser.ParseComments|parser.SkipObjectResolution)
	return goTargetFile{file: file, fset: fset}, nil
}

func (g goTargetFile) functions(name string, fromBase, compare bool) []targetFunction {
	if g.file == nil {
		return nil
	}
	generated := ast.IsGenerated(g.file)
	var functions []targetFunction
	for _, decl := range g.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil || fn.Type.Params == nil || fn.Name.Name == "_" {
			continue
		}
		line := g.fset.Position(fn.Pos()).Line
		location := fmt.Sprintf("%s:%d", name, line)
		item := targetFunction{
			target: Target{Object: location, Location: location, Symbol: fn.Name.Name},
			line:   line, key: functionKey(fn), generated: generated,
			collection: slices.ContainsFunc(fn.Type.Params.List, func(field *ast.Field) bool {
				switch field.Type.(type) {
				case *ast.ArrayType, *ast.MapType, *ast.Ellipsis:
					return true
				}
				return false
			}),
		}
		if compare {
			withoutDoc := *fn
			withoutDoc.Doc = nil
			var content strings.Builder
			if err := format.Node(&content, token.NewFileSet(), &withoutDoc); err != nil {
				continue
			}
			item.content = content.String()
		}
		if fromBase {
			functions = append(functions, item)
			continue
		}
		ast.PreorderStack(fn.Body, nil, func(node ast.Node, stack []ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if name := calledName(call.Fun); name != "" {
				item.calls = append(item.calls, name)
				if _, selector := call.Fun.(*ast.SelectorExpr); selector {
					if item.selectors == nil {
						item.selectors = make(map[string]bool)
					}
					item.selectors[name] = true
				}
				for _, parent := range stack {
					switch parent.(type) {
					case *ast.RangeStmt, *ast.ForStmt:
						if item.loopCalls == nil {
							item.loopCalls = make(map[string]bool)
						}
						item.loopCalls[name] = true
					}
				}
			}
			if len(call.Args) > 0 && driverCall(call.Fun) {
				item.driver = true
			}
			return true
		})
		functions = append(functions, item)
	}
	return functions
}

func functionKey(fn *ast.FuncDecl) string {
	if recv := recvType(fn); recv != "" {
		return recv + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func queryTargets(functions []targetFunction, invoked map[string]map[string]bool) []Target {
	driverNames := make(map[string]bool)
	for _, fn := range functions {
		if fn.driver {
			driverNames[fn.target.Symbol] = true
		}
	}
	var candidates []targetFunction
	operations := make(map[string]bool)
	for _, fn := range functions {
		if fn.generated || !fn.collection || (!fn.changed && len(invoked[fn.target.Symbol]) == 0) {
			continue
		}
		if fn.driver || slices.ContainsFunc(fn.calls, func(name string) bool { return driverNames[name] }) {
			candidates = append(candidates, fn)
			operations[fn.target.Symbol] = true
		}
	}
	var targets []Target
	for _, fn := range candidates {
		if fn.driver || slices.ContainsFunc(fn.calls, func(name string) bool {
			return driverNames[name] && (name == fn.target.Symbol || !operations[name] || fn.loopCalls[name])
		}) {
			targets = append(targets, fn.target)
		}
	}
	slices.SortFunc(targets, func(a, b Target) int { return strings.Compare(a.Object, b.Object) })
	return targets
}

// Trace same-package callers and cross-package selector calls. After
// reaching a caller outside the adapter, keep tracing its package to
// find entry candidates, including handlers in generated files.
func queryEntryTargets(functions []targetFunction, targets []Target) map[string][]QueryEntryTarget {
	type caller struct {
		target    Target
		dir       string
		selectors map[string]bool
	}
	callers := make(map[string][]caller)
	dirs := make(map[string]string)
	for _, fn := range functions {
		dir := path.Dir(fn.target.Location)
		dirs[fn.target.Location] = dir
		for _, name := range fn.calls {
			callers[name] = append(callers[name], caller{fn.target, dir, fn.selectors})
		}
	}
	entries := make(map[string][]QueryEntryTarget)
	for _, target := range targets {
		seen := map[string]bool{target.Location: true}
		found := make(map[string]int)
		queue := []Target{target}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, candidate := range callers[current.Symbol] {
				if candidate.target.Symbol == current.Symbol {
					continue
				}
				outside := candidate.dir != dirs[current.Location]
				if outside && (!ast.IsExported(current.Symbol) || !candidate.selectors[current.Symbol]) {
					continue
				}
				if outside && dirs[current.Location] != dirs[target.Location] {
					i, ok := found[candidate.target.Location]
					if !ok {
						i = len(entries[target.Object])
						found[candidate.target.Location] = i
						entries[target.Object] = append(entries[target.Object], QueryEntryTarget{Target: candidate.target})
					}
					entry := &entries[target.Object][i]
					if !slices.Contains(entry.Calls, current.Symbol) {
						entry.Calls = append(entry.Calls, current.Symbol)
					}
					continue
				}
				if !seen[candidate.target.Location] {
					seen[candidate.target.Location] = true
					queue = append(queue, candidate.target)
				}
			}
		}
		for i := range entries[target.Object] {
			slices.Sort(entries[target.Object][i].Calls)
		}
		slices.SortFunc(entries[target.Object], func(a, b QueryEntryTarget) int { return strings.Compare(a.Location, b.Location) })
	}
	return entries
}

func validateQueryEntries(rule Rule, expected map[string][]QueryEntryTarget) error {
	if len(rule.QueryEntries) != len(expected) {
		return fmt.Errorf("%s: перечень query_entries из снимка изменён; обновите аудит", rule.ID)
	}
	for _, target := range rule.Targets {
		object := target.Object
		if !slices.EqualFunc(rule.QueryEntries[object], expected[object], func(a, b QueryEntryTarget) bool {
			return sameTarget(a.Target, b.Target) && slices.Equal(a.Calls, b.Calls)
		}) {
			return fmt.Errorf("%s / %s: перечень query_entries из снимка изменён; обновите аудит", rule.ID, object)
		}
	}
	return nil
}

func sameTarget(a, b Target) bool {
	return a.Object == b.Object && a.Symbol == b.Symbol && a.Location == b.Location &&
		slices.Equal(a.CommentFragments, b.CommentFragments)
}

func validateTargetList(rule Rule, expected []Target) error {
	if !slices.EqualFunc(rule.Targets, expected, func(saved, current Target) bool {
		// Older audits do not carry the derived fragments. Evidence is checked against the source.
		if rule.ID == commentRule && saved.CommentFragments == nil {
			saved.CommentFragments = current.CommentFragments
		}
		return sameTarget(saved, current)
	}) {
		return fmt.Errorf("%s: перечень targets из диффа изменён; восстановите его из снимка review audit", rule.ID)
	}
	return nil
}

func validateTargetCoverage(rule Rule, expected []Target) error {
	var issues []error
	for _, target := range expected {
		if !slices.Contains(rule.Objects, target.Object) {
			issues = append(issues, newObjectIssue(rule.ID, evidenceKey{object: target.Object}, "обязательный объект %s отсутствует в objects", target.Object))
		}
	}
	if len(expected) > 0 && rule.Applicability != "applicable" {
		issues = append(issues, fmt.Errorf("%s: проверьте применимость отдельно для каждого объекта из диффа", rule.ID))
	}
	return errors.Join(issues...)
}

func validateFileReviews(rule Rule, expected []FileReview) error {
	if len(rule.Files) != len(expected) {
		return fmt.Errorf("%s: нужен вывод по всем файлам диффа (%d из %d в files); обновите отчёт при изменении диффа", rule.ID, len(rule.Files), len(expected))
	}
	objects := make(map[string]bool, len(rule.Objects))
	for _, object := range rule.Objects {
		objects[object] = true
	}
	var issues []error
	for i, file := range rule.Files {
		for _, err := range checkFileReview(rule.ID, file, expected[i], objects) {
			if _, ok := err.(*objectIssue); !ok {
				err = fmt.Errorf("%s / %s: %w", rule.ID, file.Path, err)
			}
			issues = append(issues, err)
		}
	}
	return errors.Join(issues...)
}

func checkFileReview(rule string, file, want FileReview, objects map[string]bool) []error {
	var issues []error
	if file.Path != want.Path {
		return []error{fmt.Errorf("в files ожидается %q, указано %q", want.Path, file.Path)}
	}
	if len(file.Objects) == 0 && strings.TrimSpace(file.Reason) == "" {
		issues = append(issues, fmt.Errorf("укажите объекты проверки или основание неприменимости"))
	}
	seen := make(map[string]bool, len(file.Objects))
	for _, object := range file.Objects {
		if !objects[object] || seen[object] {
			issues = append(issues, fmt.Errorf("неизвестный или повторный объект %q", object))
		}
		seen[object] = true
	}
	for _, object := range want.Objects {
		if !seen[object] {
			issues = append(issues, newObjectIssue(rule, evidenceKey{object: object}, "%s: обязательный объект %s отсутствует в files.objects", file.Path, object))
		}
	}
	return issues
}

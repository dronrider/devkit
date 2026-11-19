package reviewaudit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// CheckCoverage checks a review of any verdict against its recorded scope.
func CheckCoverage(root, path, id, summaryPath string) error {
	return CheckCoverageWithRequest(root, path, id, summaryPath, "")
}

func CheckCoverageWithRequest(root, path, id, summaryPath, request string) error {
	_, err := CoverageWithNextSteps(root, path, id, summaryPath, request)
	return err
}

// CoverageWithNextSteps returns actions for valid pending name checks separately
// from coverage errors, so an open question does not change the coverage verdict.
func CoverageWithNextSteps(root, path, id, summaryPath, request string) ([]string, error) {
	root, r, now, output, err := snapshotForCoverage(root, path, id, summaryPath, request)
	if err != nil {
		return nil, err
	}
	gaps, invalid := validateInventory(r, now, id)
	if invalid != nil {
		return nil, invalid
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil {
		return nil, errors.Join(gaps, err)
	}
	return nameNextSteps(r, errors.Join(gaps, checkArchitecture(r.Architecture),
		checkNames(root, path, output, r), checkCoverage(r, string(summary)),
		checkQueryEvidence(root, path, output, path, r),
		checkComments(root, path, output, r), checkMissingFiles(root, r, now),
		checkReviewFacts(root, path, output, path, r, now.taskText)))
}

func snapshotForCoverage(root, path, id, summaryPath, request string) (string, Report, Report, string, error) {
	root, r, now, err := currentSnapshot(root, path, id, request)
	if err != nil {
		return "", Report{}, Report{}, "", err
	}
	output := ""
	if summaryPath != "" && r.CodeHash != now.CodeHash {
		output, err = untrackedSummary(root, summaryPath)
		if err != nil {
			return "", Report{}, Report{}, "", err
		}
		if output != "" {
			now.CodeHash, err = codeSnapshot(root, path, output)
			if err != nil {
				return "", Report{}, Report{}, "", err
			}
			if err := collectTargets(root, path, now.BaseSHA, output, now.Rules); err != nil {
				return "", Report{}, Report{}, "", err
			}
		}
	}
	return root, r, now, output, nil
}

func checkMissingFiles(root string, report, snapshot Report) error {
	var issues []error
	for _, rule := range report.Rules {
		for _, entry := range rule.Evidence {
			if entry.MissingFile == nil {
				continue
			}
			if err := checkMissingFile(root, entry, snapshot); err != nil {
				issues = append(issues, newObjectIssue(rule.ID, evidenceKey{entry.Object, entry.Check}, "%w", err))
			}
		}
	}
	return errors.Join(issues...)
}

func checkMissingFile(root string, entry Evidence, snapshot Report) error {
	claim := entry.MissingFile
	if entry.Result != "pending" {
		return fmt.Errorf("missing_file допустим только для pending")
	}
	if claim.Path == "." || !filepath.IsLocal(claim.Path) || filepath.ToSlash(filepath.Clean(claim.Path)) != claim.Path {
		return fmt.Errorf("missing_file.path: нужен точный путь от корня репозитория")
	}
	var sha string
	switch claim.Revision {
	case "base":
		sha = snapshot.BaseSHA
	case "head":
		sha = snapshot.HeadSHA
	default:
		return fmt.Errorf("missing_file.revision: укажите base или head")
	}
	out, err := gitRaw(root, "ls-tree", "--full-tree", "-z", sha, "--", ":(literal)"+claim.Path)
	if err != nil {
		return fmt.Errorf("missing_file: проверка %q на %s: %w", claim.Path, sha, err)
	}
	if out != "" {
		return fmt.Errorf("missing_file: %q есть на %s (%s)", claim.Path, claim.Revision, sha)
	}
	return nil
}

// A summary created after the audit is output, not a change to a tracked file.
func untrackedSummary(root, path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(canonicalPath(root), canonicalPath(absolute))
	if err != nil || !filepath.IsLocal(rel) {
		return "", nil
	}
	name := filepath.ToSlash(rel)
	out, err := gitRaw(root, "ls-files", "--others", "--exclude-standard", "-z", "--", ":(literal)"+name)
	if err != nil || out != name+"\x00" {
		return "", err
	}
	return name, nil
}

func checkCoverage(report Report, summary string) error {
	headings := summarySections(summary)
	var issues []error
	a := report.Architecture
	if (a.Result == "violated" || a.Result == "pending") && headings[a.Heading] == nil {
		issues = append(issues, fmt.Errorf("архитектурная оценка %s: укажите точный заголовок замечания или вопроса из итога", a.Result))
	}
	for _, rule := range report.Rules {
		checks := objectChecks(rule.ID)
		tracked := len(checks) > 0 || tracksObjects(rule.ID)
		if len(checks) > 0 {
			issues = append(issues, checkSplitRule(rule, checks)...)
		} else if rule.Applicability == "applicable" && tracked {
			issues = append(issues, checkObjects(rule, []string{""})...)
		}
		linked := false
		targets := make(map[string]Target, len(rule.Targets))
		fileObjects := make(map[string]bool)
		for _, file := range rule.Files {
			for _, object := range file.Objects {
				fileObjects[object] = true
			}
		}
		for _, target := range rule.Targets {
			targets[target.Object] = target
		}
		for _, evidence := range rule.Evidence {
			key := evidenceKey{object: evidence.Object, check: evidence.Check}
			target, required := targets[evidence.Object]
			location := target.Location
			if rule.ID == commentRule {
				required, location = true, evidence.Object
			}
			queryGap := rule.ID == queryRule && evidence.Check == "inputs" && evidence.QueryInputs != nil && evidence.QueryInputs.Gap != nil
			if evidence.Heading == "" {
				if evidence.Result == "violated" {
					issues = append(issues, newObjectIssue(rule.ID, key, "нарушение без замечания в итоге").
						withNext("добавьте замечание и его заголовок"))
				} else if (required || fileObjects[evidence.Object] || requiresReviewFacts(rule.ID, evidence.Check) || queryGap ||
					rule.ID == namingRule || (rule.ID == queryRule && evidence.Check == "calls")) && evidence.Result == "pending" {
					issues = append(issues, newObjectIssue(rule.ID, key, "открытая проверка без вопроса в итоге").
						withNext("добавьте открытый вопрос и его заголовок"))
				}
				continue
			}
			sections := headings[evidence.Heading]
			if sections == nil {
				issues = append(issues, newObjectIssue(rule.ID, key, "заголовок %q отсутствует в итоге", evidence.Heading).
					withNext("сверьте heading с заголовком в Markdown"))
				continue
			}
			if evidence.Result == "pending" {
				if queryGap && (len(sections) != 1 || !strings.Contains(sections[0], evidence.QueryInputs.Gap.Question)) {
					issues = append(issues, fmt.Errorf("%s / %s: в разделе %q нужен вопрос из query_inputs.gap", rule.ID, key, evidence.Heading))
				}
				for _, fact := range evidence.Facts["question"] {
					if len(sections) != 1 || !strings.Contains(sections[0], fact.Text) {
						issues = append(issues, newObjectIssue(rule.ID, key, "в разделе %q нужен вопрос из facts.question", evidence.Heading).
							withNext("перенесите вопрос из facts.question в итог"))
					}
				}
			}
			if rule.ID == commentRule {
				for _, claim := range evidence.Facts["claim"] {
					if claim.Verdict == "pending" && (len(sections) != 1 ||
						!strings.Contains(compactComment(sections[0]), compactComment(claim.Text))) {
						issues = append(issues, fmt.Errorf("%s / %s: в разделе %q нужен открытый вопрос по фрагменту %q", rule.ID, key, evidence.Heading, claim.Quote))
					}
				}
			}
			if required && (evidence.Result == "violated" || evidence.Result == "pending") &&
				(len(sections) != 1 || location == "" || !containsIdentifier(sections[0], location)) {
				issues = append(issues, newObjectIssue(rule.ID, key, "в разделе %q нужна ссылка на %s", evidence.Heading, location).
					withNext("добавьте в итог ссылку на объявление"))
				continue
			}
			if evidence.Result == "violated" || !tracked {
				linked = true
			}
		}
		if rule.Result == "violated" && !linked {
			issues = append(issues, fmt.Errorf("%s: нарушение без ссылки на замечание в итоге", rule.ID))
		}
	}
	return errors.Join(issues...)
}

func summarySections(summary string) map[string][]string {
	lines := strings.Split(taskform.VisibleText(summary), "\n")
	mask, _ := taskform.FenceMask(lines)
	for i, fenced := range mask {
		if fenced {
			lines[i] = ""
		}
	}
	sections := make(map[string][]string)
	type heading struct {
		text  string
		at    int
		level int
	}
	var open []heading
	closeSections := func(level, end int) {
		for len(open) > 0 && open[len(open)-1].level >= level {
			h := open[len(open)-1]
			sections[h.text] = append(sections[h.text], strings.Join(lines[h.at:end], "\n"))
			open = open[:len(open)-1]
		}
	}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		level := len(line) - len(strings.TrimLeft(line, "#"))
		if mask[i] || level == 0 || level > 6 || len(line) <= level || line[level] != ' ' {
			continue
		}
		closeSections(level, i)
		open = append(open, heading{line, i, level})
	}
	closeSections(0, len(lines))
	return sections
}

func requiredChecks(id string) []string {
	switch id {
	case configOptionRule:
		return []string{"behavior", "basis"}
	case contractRule:
		return []string{"schema", "output", "consumer"}
	case selectionRule:
		return []string{"usage", "order"}
	case readRule:
		return []string{"with_consumer", "without_consumer"}
	case testStrengthRule:
		return []string{"strength", "overlap"}
	case dataBoundaryRule:
		return []string{"boundaries", "parameters", "binding", "proposed_binding"}
	case queryRule:
		return []string{"calls", "inputs", "batch"}
	case namingRule:
		return []string{"meaning", "result", "writes"}
	default:
		return nil
	}
}

func objectChecks(id string) []string {
	if id == commentRule {
		return []string{""}
	}
	return requiredChecks(id)
}

func tracksObjects(id string) bool {
	switch id {
	case "common-architecture:ARCH-022", moduleRule, failureRule, flowRule, descriptionRule, testLevelRule:
		return true
	}
	return false
}

func checkSplitRule(rule Rule, checks []string) []error {
	var issues []error
	if rule.Checks != nil && !slices.Equal(rule.Checks, checks) {
		issues = append(issues, fmt.Errorf("%s: список checks изменён, ожидается %s", rule.ID, strings.Join(checks, ", ")))
	}
	switch rule.Applicability {
	case "applicable":
	case "pending", "not_applicable":
		expected := "pending"
		if rule.Applicability == "not_applicable" {
			expected = "checked"
		}
		if rule.Result != expected || strings.TrimSpace(rule.Reason) == "" {
			issues = append(issues, fmt.Errorf("%s: для применимости %s нужны результат %s и основание", rule.ID, rule.Applicability, expected))
		}
	default:
		issues = append(issues, fmt.Errorf("%s: применимость правила не указана", rule.ID))
	}
	if rule.Applicability == "applicable" || len(rule.Objects) > 0 ||
		slices.ContainsFunc(rule.Evidence, func(e Evidence) bool { return e.Check != "" }) {
		issues = append(issues, checkObjects(rule, checks)...)
	}
	return issues
}

type evidenceKey struct {
	object string
	check  string
}

func (k evidenceKey) String() string {
	if k.check == "" {
		return k.object
	}
	return k.object + " / " + k.check
}

type objectIssue struct {
	rule    string
	key     evidenceKey
	err     error
	next    string
	missing bool
}

func newObjectIssue(rule string, key evidenceKey, format string, args ...any) *objectIssue {
	return &objectIssue{rule: rule, key: key, err: fmt.Errorf(format, args...)}
}

func (i *objectIssue) withNext(next string) *objectIssue {
	i.next = next
	return i
}

func (i *objectIssue) Error() string {
	return fmt.Sprintf("%s / %s: %s", i.rule, i.key, i.err)
}

func (i *objectIssue) Unwrap() error { return i.err }

func eachAuditIssue(err error, visit func(error)) {
	switch err := err.(type) {
	case nil:
	case interface{ Unwrap() []error }:
		for _, child := range err.Unwrap() {
			eachAuditIssue(child, visit)
		}
	default:
		visit(err)
	}
}

func checkObjects(rule Rule, checks []string) []error {
	if len(rule.Objects) == 0 {
		return []error{fmt.Errorf("%s: отсутствует список проверяемых объектов или сценариев", rule.ID)}
	}
	var issues []error
	objects := make(map[string]bool, len(rule.Objects))
	for _, object := range rule.Objects {
		if strings.TrimSpace(object) == "" || objects[object] {
			issues = append(issues, fmt.Errorf("%s: пустой или повторный объект %q", rule.ID, object))
		}
		objects[object] = true
	}
	seen := make(map[evidenceKey]bool, len(rule.Evidence))
	result := "checked"
	for _, evidence := range rule.Evidence {
		key := evidenceKey{object: evidence.Object, check: evidence.Check}
		if !objects[key.object] || seen[key] {
			issues = append(issues, newObjectIssue(rule.ID, key, "лишнее или повторное доказательство для %q", key))
		}
		if !slices.Contains(checks, key.check) {
			issues = append(issues, newObjectIssue(rule.ID, key, "неизвестная подпроверка %q, ожидается одна из %q", key.check, checks))
			continue
		}
		seen[key] = true
		if strings.TrimSpace(evidence.Observation) == "" ||
			(evidence.Result != "pending" && strings.TrimSpace(evidence.Location) == "") {
			issues = append(issues, newObjectIssue(rule.ID, key, "нет места или основания вывода"))
		}
		if rule.Applicability == "not_applicable" && evidence.Result != "not_applicable" {
			issues = append(issues, newObjectIssue(rule.ID, key, "неприменимость правила противоречит результату %s", evidence.Result))
		}
		switch evidence.Result {
		case "checked", "not_applicable":
		case "violated":
			result = "violated"
		case "pending":
			if result != "violated" {
				result = "pending"
			}
		default:
			issues = append(issues, newObjectIssue(rule.ID, key, "не указан результат проверки"))
		}
	}
	for _, object := range rule.Objects {
		for _, check := range checks {
			key := evidenceKey{object: object, check: check}
			if seen[key] {
				continue
			}
			issue := newObjectIssue(rule.ID, key, "нет записи в evidence")
			issue.missing = true
			issues = append(issues, issue)
			if result != "violated" {
				result = "pending"
			}
		}
	}
	if rule.Result != result {
		issues = append(issues, fmt.Errorf("%s: результат %s не соответствует доказательствам, ожидается %s", rule.ID, rule.Result, result))
	}
	return issues
}

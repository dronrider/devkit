// Package reviewaudit checks completeness and freshness of review evidence.
package reviewaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/gitrun"
	"github.com/dronrider/devkit/internal/kvconf"
	"github.com/dronrider/devkit/internal/taskform"
	"github.com/dronrider/devkit/internal/taskhead"
)

type Source struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Hash   string `json:"hash"`
}
type Evidence struct {
	Object      string `json:"object"`
	Location    string `json:"location"`
	Observation string `json:"observation"`
}
type Rule struct {
	ID            string     `json:"id"`
	Source        string     `json:"source"`
	Text          string     `json:"text"`
	Applicability string     `json:"applicability"`
	Result        string     `json:"result"`
	Reason        string     `json:"reason"`
	Evidence      []Evidence `json:"evidence"`
}
type Architecture struct {
	Goal          string   `json:"goal"`
	Decision      string   `json:"decision"`
	Boundaries    string   `json:"boundaries"`
	Alternative   string   `json:"alternative"`
	Justification string   `json:"justification"`
	Result        string   `json:"result"`
	Evidence      []string `json:"evidence"`
}
type Report struct {
	Version      int          `json:"version"`
	ID           string       `json:"id"`
	BaseSHA      string       `json:"base_sha"`
	HeadSHA      string       `json:"head_sha"`
	CodeHash     string       `json:"code_hash"`
	TaskHash     string       `json:"task_hash"`
	Sources      []Source     `json:"sources"`
	Architecture Architecture `json:"architecture"`
	Rules        []Rule       `json:"rules"`
}

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func gitRaw(root string, args ...string) (string, error) {
	limit, e := gitrun.Timeout()
	if e != nil {
		return "", e
	}
	return gitrun.RunRaw(root, args, limit)
}

func git(root string, args ...string) (string, error) {
	limit, e := gitrun.Timeout()
	if e != nil {
		return "", e
	}
	return gitrun.Run(root, args, limit)
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func Path(root, id string, board bool) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("пустой ярлык ревью")
	}
	if board {
		if !safeID.MatchString(id) {
			return "", fmt.Errorf("недопустимый ID задачи %q", id)
		}
		return filepath.Join(root, "docs", "tasks", id+".review-audit.json"), nil
	}
	return filepath.Join(root, ".devkit", "review-audits", digest(id)[:16]+".json"), nil
}

func readReport(path string) (Report, error) {
	var r Report
	f, e := os.Open(path)
	if e != nil {
		return r, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return r, e
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return r, fmt.Errorf("после отчёта есть лишние данные")
	}
	return r, nil
}

// Create never silently replaces evidence. Refresh archives it and resets conclusions.
func Create(root, path, id, base string, refresh bool) (string, error) {
	old, e := readReport(path)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return "", fmt.Errorf("прочитать прежний отчёт: %w", e)
	}
	if exists && old.ID != id {
		return "", fmt.Errorf("ID прежнего отчёта не совпадает с %s", id)
	}
	if exists && !refresh {
		return "", fmt.Errorf("отчёт уже есть: %s; для нового снимка review audit %s --refresh", path, id)
	}
	if refresh && !exists {
		return "", fmt.Errorf("обновлять нечего: создайте review audit %s --base <база>", id)
	}
	if base == "" && exists {
		base = old.BaseSHA
	}
	if base == "" {
		return "", fmt.Errorf("первый отчёт требует явный --base <база всей правки>")
	}
	r, e := snapshot(root, path, id, base, "")
	if e != nil {
		return "", e
	}
	r.Architecture.Result = "pending"
	r.Architecture.Evidence = []string{}
	for i := range r.Rules {
		r.Rules[i].Applicability = "pending"
		r.Rules[i].Result = "pending"
		r.Rules[i].Evidence = []Evidence{}
	}
	data, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return "", e
	}
	data = append(data, '\n')
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return "", e
	}
	backup := ""
	if exists {
		f, e := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".previous-")
		if e != nil {
			return "", e
		}
		backup = f.Name()
		previous, e := os.ReadFile(path)
		if e == nil {
			_, e = f.Write(previous)
		}
		ce := f.Close()
		if e != nil {
			return "", e
		}
		if ce != nil {
			return "", ce
		}
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".audit-write-")
	if e != nil {
		return "", e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(tmp, path); e != nil {
		return "", e
	}
	msg := path
	if backup != "" {
		msg += "; прежний отчёт: " + backup
	}
	return msg, nil
}

func snapshot(root, path, id, base, standards string) (Report, error) {
	r := Report{Version: 1, ID: id}
	top, e := git(root, "rev-parse", "--show-toplevel")
	if e != nil {
		return r, fmt.Errorf("отчёт требует git-дерево кода: %w", e)
	}
	root = strings.TrimSpace(top)
	b, e := git(root, "rev-parse", "--verify", base+"^{commit}")
	if e != nil {
		return r, fmt.Errorf("база ревью не найдена: %w", e)
	}
	r.BaseSHA = strings.TrimSpace(b)
	h, e := git(root, "rev-parse", "HEAD")
	if e != nil {
		return r, e
	}
	r.HeadSHA = strings.TrimSpace(h)
	if _, e = git(root, "merge-base", "--is-ancestor", r.BaseSHA, r.HeadSHA); e != nil {
		return r, fmt.Errorf("база ревью не предок HEAD; укажите актуальную базу всей правки")
	}
	r.Sources, r.Rules, e = inventory(root, standards)
	if e != nil {
		return r, e
	}
	r.CodeHash, r.TaskHash, e = codeSnapshot(root, path)
	return r, e
}

func taskPath(report string) string {
	if strings.HasSuffix(report, ".review-audit.json") {
		return strings.TrimSuffix(report, ".review-audit.json") + ".md"
	}
	return ""
}
func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(canonicalPath(parent), filepath.Base(p))
}
func excluded(root, report, rel string) bool {
	p := canonicalPath(filepath.Join(root, rel))
	report = canonicalPath(report)
	normalized := filepath.ToSlash(rel)
	if strings.HasPrefix(normalized, "docs/tasks/") && strings.HasSuffix(normalized, ".md") {
		return true
	}
	if strings.HasPrefix(normalized, ".devkit/review-audits/") {
		return true
	}
	if filepath.Dir(normalized) == "docs/tasks" {
		name := filepath.Base(normalized)
		if strings.HasSuffix(name, ".review-audit.json") || strings.Contains(name, ".review-audit.json.previous-") {
			return true
		}
	}
	return p == report || strings.HasPrefix(p, report+".previous-") || p == taskPath(report) || p == strings.TrimSuffix(taskPath(report), ".md")+".review.md" || rel == "docs/TASKS.md" || rel == "docs/TASKS-archive.md"
}
func codeSnapshot(root, report string) (string, string, error) {
	out, e := gitRaw(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if e != nil {
		return "", "", e
	}
	names := strings.Split(out, "\x00")
	sort.Strings(names)
	var data strings.Builder
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] || excluded(root, report, name) {
			continue
		}
		seen[name] = true
		p := filepath.Join(root, name)
		info, e := os.Lstat(p)
		if os.IsNotExist(e) {
			// Sparse entries and uninitialized submodules remain reviewed content.
			stage, err := gitRaw(root, "ls-files", "--stage", "--", name)
			if err != nil {
				return "", "", err
			}
			fields := strings.Fields(stage)
			if len(fields) >= 3 {
				if fields[2] != "0" {
					return "", "", fmt.Errorf("неразрешённый конфликт индекса: %s", name)
				}
				if fields[0] == "160000" {
					fmt.Fprintf(&data, "%s\x00gitlink\x00%s\x00", name, fields[1])
					continue
				}
				flags, err := gitRaw(root, "ls-files", "-v", "--", name)
				if err != nil {
					return "", "", err
				}
				if strings.HasPrefix(flags, "S ") || strings.HasPrefix(flags, "s ") {
					blob, err := gitRaw(root, "cat-file", "blob", fields[1])
					if err != nil {
						return "", "", err
					}
					mode := os.FileMode(0)
					if fields[0] == "100755" {
						mode = 0111
					}
					if fields[0] == "120000" {
						mode = os.ModeSymlink
					}
					fmt.Fprintf(&data, "%s\x00%v\x00%s\x00", name, mode, digest(blob))
				}
			}
			continue
		}
		if e != nil {
			return "", "", e
		}
		var b []byte
		if info.Mode()&os.ModeSymlink != 0 {
			v, e := os.Readlink(p)
			if e != nil {
				return "", "", e
			}
			b = []byte(v)
		} else if info.IsDir() {
			stage, err := gitRaw(root, "ls-files", "--stage", "--", name)
			if err != nil {
				return "", "", err
			}
			fields := strings.Fields(stage)
			if len(fields) < 3 || fields[0] != "160000" {
				return "", "", fmt.Errorf("неожиданный каталог в индексе: %s", name)
			}
			sha := fields[1]
			if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
				head, err := git(p, "rev-parse", "HEAD")
				if err != nil {
					return "", "", err
				}
				status, err := git(p, "status", "--porcelain", "--untracked-files=all")
				if err != nil {
					return "", "", err
				}
				if strings.TrimSpace(status) != "" {
					return "", "", fmt.Errorf("подмодуль %s содержит незакоммиченные изменения; завершите его отдельное ревью", name)
				}
				sha = strings.TrimSpace(head)
			}
			fmt.Fprintf(&data, "%s\x00gitlink\x00%s\x00", name, sha)
			continue
		} else {
			b, e = os.ReadFile(p)
			if e != nil {
				return "", "", e
			}
		}
		fmt.Fprintf(&data, "%s\x00%v\x00%s\x00", name, info.Mode()&(os.ModeSymlink|0111), digest(string(b)))
	}
	taskHash := ""
	if p := taskPath(report); p != "" {
		b, e := os.ReadFile(p)
		if e != nil {
			return "", "", fmt.Errorf("файл задачи отчёта: %w", e)
		}
		taskHash = digest(taskContent(string(b)))
	}
	return digest(data.String()), taskHash, nil
}
func taskContent(text string) string {
	lines := strings.Split(text, "\n")
	mask, unclosed := taskform.FenceMask(lines)
	if unclosed > 0 {
		return text
	}
	var out []string
	skip := false
	for i, line := range lines {
		if !mask[i] && strings.HasPrefix(line, "## ") {
			h := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			skip = h == "Ход работы" || h == "Ревью" || h == "Выкат" || h == "Проверка"
		}
		if !skip {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func inventory(root, standards string) ([]Source, []Rule, error) {
	home, _ := os.UserHomeDir()
	dk := standards
	if dk == "" {
		dk = taskhead.Devkit(root, home)
	}
	if dk == "" {
		return nil, nil, fmt.Errorf("не найден чекаут общих правил devkit; подключите .devkit/devkit или задайте DEVKIT_HOME")
	}
	specs := []struct {
		name, path, section string
		required            bool
	}{
		{"common-architecture", filepath.Join(dk, "RULES.architecture.md"), "", true},
		{"common-style", filepath.Join(dk, "RULES.style.md"), "", true},
		{"common-tests", filepath.Join(dk, "RULES.md"), "Тесты обязательны", true},
		{"project-architecture", filepath.Join(root, "docs", "ARCHITECTURE.md"), "", false},
		{"project-style", filepath.Join(root, "docs", "STYLEGUIDE.md"), "", false}}
	pairs, err := kvconf.Read(filepath.Join(root, ".devkit", "review.conf"))
	if err != nil {
		return nil, nil, err
	}
	seenPaths := map[string]bool{}
	for _, spec := range specs {
		seenPaths[canonicalPath(spec.path)] = true
	}
	seenKey := false
	for _, pair := range pairs {
		if pair.Key != "rule_sources" {
			continue
		}
		if seenKey {
			return nil, nil, fmt.Errorf("rule_sources указан повторно")
		}
		seenKey = true
		if strings.TrimSpace(pair.Value) == "" {
			continue
		}
		for _, entry := range strings.Split(pair.Value, ",") {
			rel := strings.TrimSpace(entry)
			if rel == "" || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
				return nil, nil, fmt.Errorf("недопустимый путь rule_sources %q", rel)
			}
			full := filepath.Join(root, filepath.FromSlash(rel))
			resolved := canonicalPath(full)
			resolvedRoot := canonicalPath(root)
			inside, e := filepath.Rel(resolvedRoot, resolved)
			if e != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || seenPaths[resolved] {
				return nil, nil, fmt.Errorf("rule_sources выходит из проекта или повторяет источник: %s", rel)
			}
			seenPaths[resolved] = true
			specs = append(specs, struct {
				name, path, section string
				required            bool
			}{"project-extra:" + rel, full, "", true})
		}
	}
	var sources []Source
	var rules []Rule
	for _, spec := range specs {
		b, e := os.ReadFile(spec.path)
		sourcePath := "project:"
		sourceRoot := root
		common := strings.HasPrefix(spec.name, "common-")
		if common {
			sourcePath = "common:"
			sourceRoot = dk
		}
		rel, _ := filepath.Rel(sourceRoot, spec.path)
		s := Source{Name: spec.name, Path: sourcePath + filepath.ToSlash(rel), Status: "present"}
		if os.IsNotExist(e) && !spec.required {
			s.Status = "absent"
			sources = append(sources, s)
			continue
		}
		if e != nil {
			return nil, nil, fmt.Errorf("источник %s: %w", spec.name, e)
		}
		text := string(b)
		if spec.section != "" {
			text, e = section(text, spec.section)
			if e != nil {
				return nil, nil, fmt.Errorf("%s: %w", spec.name, e)
			}
			if text == "" {
				return nil, nil, fmt.Errorf("источник %s: нет раздела %s", spec.name, spec.section)
			}
		}
		s.Hash = digest(text)
		sources = append(sources, s)
		rr, e := parseRules(spec.name, text, common)
		if e != nil {
			return nil, nil, e
		}
		rules = append(rules, rr...)
	}
	return sources, rules, nil
}
func section(text, name string) (string, error) {
	lines := strings.Split(text, "\n")
	mask, unclosed := taskform.FenceMask(lines)
	if unclosed > 0 {
		return "", fmt.Errorf("незакрытый блок кода, строка %d", unclosed)
	}
	start := -1
	for i, l := range lines {
		if mask[i] {
			continue
		}
		if l == "## "+name {
			start = i + 1
			continue
		}
		if start >= 0 && strings.HasPrefix(l, "## ") {
			return strings.Join(lines[start:i], "\n"), nil
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n"), nil
	}
	return "", nil
}

var listBlock = regexp.MustCompile(`^(?:[-*+] |[0-9]+[.)] )`)

var ruleID = regexp.MustCompile(`^[-*] \*\*([A-Z][A-Z0-9-]+)\.\*\*\s+`)

func parseRules(source, text string, common bool) ([]Rule, error) {
	var blocks []string
	var current []string
	blankAfterList := false
	lines := strings.Split(text, "\n")
	mask, unclosed := taskform.FenceMask(lines)
	if unclosed > 0 {
		return nil, fmt.Errorf("%s: незакрытый блок кода, строка %d", source, unclosed)
	}
	flush := func() {
		blankAfterList = false
		if len(current) > 0 {
			blocks = append(blocks, strings.TrimSpace(strings.Join(current, "\n")))
			current = nil
		}
	}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if mask[i] {
			if !common {
				current = append(current, line)
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			flush()
			if !common {
				blocks = append(blocks, strings.TrimSpace(line))
			}
			continue
		}
		if listBlock.MatchString(line) {
			flush()
			current = append(current, line)
			continue
		}
		if t == "" {
			if len(current) > 0 {
				if listBlock.MatchString(current[0]) {
					blankAfterList = true
				} else {
					flush()
				}
			}
			continue
		}
		if blankAfterList && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			flush()
		}
		blankAfterList = false
		current = append(current, line)
	}
	flush()
	var rules []Rule
	seen := map[string]bool{}
	occurrences := map[string]int{}
	for _, b := range blocks {
		m := ruleID.FindStringSubmatch(b)
		tag := ""
		if len(m) > 0 {
			tag = m[1]
		} else if common {
			if listBlock.MatchString(b) {
				return nil, fmt.Errorf("%s: правило без устойчивого ID: %s", source, strings.Split(b, "\n")[0])
			}
			continue
		} else {
			tag = "block-" + digest(strings.Join(strings.Fields(b), " "))[:16]
		}
		id := source + ":" + tag
		if !common && len(m) == 0 {
			occurrences[id]++
			if occurrences[id] > 1 {
				id = fmt.Sprintf("%s:%d", id, occurrences[id])
			}
		}
		if seen[id] {
			return nil, fmt.Errorf("повтор правила %s", id)
		}
		seen[id] = true
		rules = append(rules, Rule{ID: id, Source: source, Text: b})
	}
	if common && len(rules) == 0 {
		return nil, fmt.Errorf("%s: источник не содержит правил с ID", source)
	}
	return rules, nil
}

func Validate(root, path, id string) error {
	top, err := git(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root = strings.TrimSpace(top)
	r, e := readReport(path)
	if e != nil {
		return fmt.Errorf("отчёт ревью не читается (%s): %w; создайте review audit %s --base <база>", path, e, id)
	}
	if r.Version != 1 || r.ID != id {
		return fmt.Errorf("версия или ID отчёта не совпадают")
	}
	now, e := snapshot(root, path, id, r.BaseSHA, "")
	if e != nil {
		return e
	}
	if e = validateSnapshot(r, now, id); e != nil {
		return e
	}
	// A verdict is attached to HEAD, so reviewed working files must have reached it.
	out, e := gitRaw(root, "diff", "--name-only", "-z", "HEAD")
	if e != nil {
		return e
	}
	untracked, e := gitRaw(root, "ls-files", "--others", "--exclude-standard", "-z")
	if e != nil {
		return e
	}
	for _, name := range strings.Split(out+untracked, "\x00") {
		if name != "" && !excluded(root, path, name) {
			return fmt.Errorf("код %s ещё не закоммичен; закоммитьте проверенный код перед clean", name)
		}
	}
	return nil
}

func validateSnapshot(r, now Report, id string) error {
	if r.Version != 1 || r.ID != id {
		return fmt.Errorf("версия или ID отчёта не совпадают")
	}
	if r.BaseSHA != now.BaseSHA {
		return fmt.Errorf("base_sha должен быть полным SHA, а не перемещаемой ссылкой")
	}
	if now.CodeHash != r.CodeHash || now.TaskHash != r.TaskHash {
		return fmt.Errorf("код или постановка изменились после отчёта; review audit %s --refresh", id)
	}
	if len(now.Sources) != len(r.Sources) {
		return fmt.Errorf("неполный перечень источников")
	}
	for i, s := range now.Sources {
		if s != r.Sources[i] {
			return fmt.Errorf("источник %s изменён, отсутствует или подменён; обновите отчёт", s.Name)
		}
	}
	if len(now.Rules) != len(r.Rules) {
		return fmt.Errorf("отчёт не покрывает все правила: нужно %d, записано %d", len(now.Rules), len(r.Rules))
	}
	expected := map[string]Rule{}
	for _, rr := range now.Rules {
		expected[rr.ID] = rr
	}
	seen := map[string]bool{}
	for _, rr := range r.Rules {
		want, ok := expected[rr.ID]
		if !ok || seen[rr.ID] {
			return fmt.Errorf("лишнее или повторное правило %q", rr.ID)
		}
		seen[rr.ID] = true
		if rr.Source != want.Source || rr.Text != want.Text {
			return fmt.Errorf("содержание правила %s изменено в отчёте", rr.ID)
		}
		if rr.Result != "checked" {
			return fmt.Errorf("правило %s не завершено или нарушено (%s)", rr.ID, rr.Result)
		}
		if strings.TrimSpace(rr.Reason) == "" {
			return fmt.Errorf("у правила %s нет основания вывода", rr.ID)
		}
		switch rr.Applicability {
		case "not_applicable":
		case "applicable":
			if len(rr.Evidence) == 0 {
				return fmt.Errorf("у правила %s нет проверенных объектов", rr.ID)
			}
			for _, ev := range rr.Evidence {
				if strings.TrimSpace(ev.Object) == "" || strings.TrimSpace(ev.Location) == "" || strings.TrimSpace(ev.Observation) == "" {
					return fmt.Errorf("у правила %s неполное свидетельство", rr.ID)
				}
			}
		default:
			return fmt.Errorf("применимость правила %s не разобрана", rr.ID)
		}
	}
	a := r.Architecture
	if a.Result != "checked" {
		return fmt.Errorf("архитектурная оценка не завершена или выявила нарушение")
	}
	for _, v := range []string{a.Goal, a.Decision, a.Boundaries, a.Alternative, a.Justification} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("архитектурная оценка неполна")
		}
	}
	if len(a.Evidence) == 0 {
		return fmt.Errorf("нет свидетельств архитектурной оценки")
	}
	for _, v := range a.Evidence {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("пустое свидетельство архитектурной оценки")
		}
	}

	return nil
}

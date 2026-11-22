package reviewaudit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dronrider/devkit/internal/taskhead"
)

// ValidateAt assesses the commit being shipped without treating unrelated local
// work as part of that commit. It does not modify the caller's tree or index.
func ValidateAt(root, path, id, sha string) error {
	top, e := git(root, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	root = strings.TrimSpace(top)
	resolved, e := git(root, "rev-parse", "--verify", sha+"^{commit}")
	if e != nil {
		return e
	}
	r, e := readReport(path)
	if e != nil {
		return fmt.Errorf("отчёт ревью не читается (%s): %w; создайте review audit %s --base <база>", path, e, id)
	}
	home, _ := os.UserHomeDir()
	standards := taskhead.Devkit(root, home)
	if standards == "" {
		return fmt.Errorf("не найден чекаут общих правил devkit; подключите .devkit/devkit или задайте DEVKIT_HOME")
	}
	tmp, e := os.MkdirTemp("", "devkit-review-audit-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	tree := filepath.Join(tmp, "tree")
	if _, e = git(root, "-c", "core.hooksPath="+os.DevNull, "clone", "--quiet", "--shared", "--no-checkout", "--", root, tree); e != nil {
		return e
	}
	if _, e = git(tree, "-c", "core.hooksPath="+os.DevNull, "checkout", "--quiet", "--detach", strings.TrimSpace(resolved)); e != nil {
		return e
	}
	metadata := path
	rel, e := filepath.Rel(canonicalPath(root), canonicalPath(path))
	if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		metadata = filepath.Join(tree, rel)
	}
	r, e = runtimeReportAtCommit(root, tree, path, id, r)
	if e != nil {
		return e
	}
	if containsRuntimeProof(r) {
		if _, e = nameRuntimeAllowedSigners(root); e != nil {
			return e
		}
		metadata, e = Path(tree, id, true)
		if e != nil {
			return e
		}
	}
	now, e := snapshot(tree, metadata, id, r.BaseSHA, standards, r.RequestPath)
	if e != nil {
		return e
	}
	return validateSnapshot(tree, metadata, path, r, now, id)
}

func containsRuntimeProof(report Report) bool {
	return slices.ContainsFunc(report.Rules, func(rule Rule) bool {
		return rule.ID == namingRule && slices.ContainsFunc(rule.Evidence, func(entry Evidence) bool {
			return entry.ObjectComparison != nil && entry.ObjectComparison.Proof != nil && entry.ObjectComparison.Proof.Runtime != nil
		})
	})
}

func runtimeReportAtCommit(root, tree, livePath, id string, live Report) (Report, error) {
	expected, err := Path(root, id, true)
	if err != nil || filepath.Base(livePath) != filepath.Base(expected) ||
		canonicalPath(filepath.Dir(livePath)) != canonicalPath(filepath.Dir(expected)) {
		if containsRuntimeProof(live) {
			return Report{}, fmt.Errorf("proof.runtime: укажите штатный файл аудита задачи доски")
		}
		return live, nil
	}
	relative, err := filepath.Rel(root, expected)
	if err != nil {
		return Report{}, err
	}
	hasRuntime := containsRuntimeProof(live)
	name := filepath.ToSlash(relative)
	entry, err := gitRaw(tree, "--no-replace-objects", "ls-tree", "--full-tree", "HEAD", "--", ":(literal)"+name)
	if err != nil || (!strings.HasPrefix(entry, "100644 blob ") && !strings.HasPrefix(entry, "100755 blob ")) {
		if hasRuntime {
			return Report{}, fmt.Errorf("proof.runtime: файл аудита должен быть обычным файлом в выпускаемом коммите")
		}
		return live, nil
	}
	blob, err := gitRaw(tree, "--no-replace-objects", "cat-file", "blob", "HEAD:"+name)
	if err != nil {
		return Report{}, err
	}
	committed, err := decodeReport(strings.NewReader(blob))
	if !hasRuntime && !containsRuntimeProof(committed) {
		return live, nil
	}
	if err != nil {
		return Report{}, fmt.Errorf("proof.runtime: отчёт должен быть в выпускаемом коммите: %w", err)
	}
	liveBytes, err := os.ReadFile(livePath)
	if err != nil {
		return Report{}, err
	}
	if !bytes.Equal(liveBytes, []byte(blob)) {
		return Report{}, fmt.Errorf("proof.runtime: рабочий отчёт отличается от отчёта в выпускаемом коммите")
	}
	return committed, nil
}

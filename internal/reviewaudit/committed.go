package reviewaudit

import (
	"fmt"
	"os"
	"path/filepath"
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
	now, e := snapshot(tree, metadata, id, r.BaseSHA, standards)
	if e != nil {
		return e
	}
	return validateSnapshot(r, now, id)
}

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/drafttx"
)

type deferredPublication struct {
	root  string
	paths []string
	opts  CommitOpts
}

var cliTxEnabled bool
var cliSkipRunLog bool
var cliTx *drafttx.Lock
var cliWorktreeRoot string
var cliIsLinkedWorktree bool
var cliPublications []deferredPublication
var cliAfterUnlock []func() string

type deferredRemoval struct {
	root, path, destination string
	gitRepo                 bool
}

var cliRemovals []deferredRemoval

func cliIndexAction(root, path, destination string) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		canonical = root
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		canonical = root
	}
	fallback := filepath.Join(canonical, ".devkit", "draft-tx")
	cliRemovals = append(cliRemovals, deferredRemoval{root: root, path: path, destination: destination,
		gitRepo: filepath.Clean(cliTx.ServiceDir()) != filepath.Clean(fallback)})
}

func indexHasPath(root, path string) (bool, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", path).CombinedOutput()
	if err == nil {
		return true, nil
	}
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git ls-files: %v (%s)", err, out)
}

func cliNeedsDraftLock(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "add", "set", "dep", "sort", "list", "show", "id", "move", "ask", "decide", "fail", "debt", "debt-done", "arm", "plan", "chain", "file", "progress", "elapsed", "lint", "closable", "close":
		return true
	case "review":
		return len(args) > 1 && args[1] != "publish" && args[1] != "poll" && args[1] != "approve-mr"
	case "draft":
		// Proposals currently acquire their own lock; do not recursively flock.
		return len(args) < 2 || args[1] != "proposals"
	}
	return false
}
func cliTakeDraftLock(root string) error {
	if !cliTxEnabled || cliTx != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, ".devkit")); os.IsNotExist(err) {
		cliSkipRunLog = true
	}
	cliWorktreeRoot = root
	cliIsLinkedWorktree = linkedWorktree(root)
	l, err := drafttx.Acquire(root)
	if err != nil {
		return err
	}
	cliTx = l
	return nil
}
func cliQueuePublication(root string, paths []string, c CommitOpts) bool {
	if cliTx == nil || c.Msg == "" {
		return false
	}
	cliPublications = append(cliPublications, deferredPublication{root: root, paths: append([]string(nil), paths...), opts: c})
	return true
}
func cliFinishDraftLock(success bool) (string, error) {
	if cliTx != nil {
		err := cliTx.Close()
		cliTx = nil
		if err != nil {
			return "", err
		}
	}
	// The file was already removed under the board lock. Git's index is
	// updated only after releasing it, so hooks and wrappers may call taskctl.
	untracked := map[string]bool{}
	for _, r := range cliRemovals {
		rel, err := filepath.Rel(r.root, r.path)
		if err != nil {
			return "", fmt.Errorf("применено, индекс не обновлён: %w", err)
		}
		if !r.gitRepo {
			untracked[filepath.Clean(rel)] = true
			continue
		}
		tracked, err := indexHasPath(r.root, rel)
		if err != nil {
			return "", fmt.Errorf("применено, индекс не прочитан: %w", err)
		}
		if !tracked {
			untracked[filepath.Clean(rel)] = true
			continue
		}
		if r.destination == "" {
			if out, err := exec.Command("git", "-C", r.root, "rm", "--cached", "-q", "-f", "--", rel).CombinedOutput(); err != nil {
				return "", fmt.Errorf("применено, индекс не обновлён: %v (%s)", err, out)
			}
			continue
		}
		dst, err := filepath.Rel(r.root, r.destination)
		if err != nil {
			return "", fmt.Errorf("применено, индекс не обновлён: %w", err)
		}
		// A tracked destination made git mv refuse; the old fallback was a
		// filesystem rename without index changes.
		dstTracked, err := indexHasPath(r.root, dst)
		if err != nil {
			return "", fmt.Errorf("применено, индекс не прочитан: %w", err)
		}
		if dstTracked {
			untracked[filepath.Clean(rel)] = true
			continue
		}
		out, err := exec.Command("git", "-C", r.root, "ls-files", "--stage", "-z", "--", rel).Output()
		if err != nil {
			return "", fmt.Errorf("применено, индекс не прочитан: %w", err)
		}
		entry := strings.SplitN(strings.TrimSuffix(string(out), "\x00"), "\t", 2)
		fields := strings.Fields(entry[0])
		if len(entry) != 2 || len(fields) != 3 || fields[2] != "0" || entry[1] != rel {
			return "", fmt.Errorf("применено, индекс исходного файла %s неоднозначен", rel)
		}
		input := fmt.Sprintf("0 %s\t%s\x00%s %s\t%s\x00", strings.Repeat("0", len(fields[1])), rel, fields[0], fields[1], dst)
		cmd := exec.Command("git", "-C", r.root, "update-index", "-z", "--index-info")
		cmd.Stdin = bytes.NewBufferString(input)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("применено, индекс не обновлён: %v (%s)", err, out)
		}
	}
	cliRemovals = nil
	if !success {
		cliPublications = nil
		cliAfterUnlock = nil
		return "", nil
	}
	var tails string
	for _, p := range cliPublications {
		var paths []string
		for _, path := range p.paths {
			if !untracked[filepath.Clean(path)] {
				paths = append(paths, path)
			}
		}
		if len(paths) == 0 {
			continue
		}
		tail, err := p.opts.apply(p.root, paths)
		if err != nil {
			return tails, fmt.Errorf("применено, публикация не завершена: %w", err)
		}
		tails += tail
	}
	cliPublications = nil
	for _, f := range cliAfterUnlock {
		tails += f()
	}
	cliAfterUnlock = nil
	return tails, nil
}

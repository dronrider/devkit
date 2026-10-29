package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/stage"
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
var cliMainRoot string
var cliPublications []deferredPublication
var cliAfterUnlock []func() string

type deferredRemoval struct {
	root, path, destination string
	gitRepo                 bool
}

var cliRemovals []deferredRemoval
var errDraftChanged = errors.New("материал изменился при внешней проверке; повторите команду")

// cliReadOutside runs a read-only external check without the board lock and
// refuses to continue when a writer changed its inputs in the meantime.
func cliReadOutside(root string, check func() error) error {
	if cliTx == nil {
		return check()
	}
	lock := cliTx
	cliTx = nil
	var err error
	cliTx, err = draftReadOutside(root, cliMainRoot, lock, check)
	return err
}

func draftReadOutside(root, main string, lock *drafttx.Lock, check func() error) (*drafttx.Lock, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		lock.Close()
		return nil, err
	}
	before, err := collectDocs(root)
	if err != nil {
		lock.Close()
		return nil, err
	}
	var mainCanonical string
	var mainBefore map[string]drafttx.Image
	if main != "" {
		mainCanonical, err = filepath.EvalSymlinks(main)
		if err != nil {
			lock.Close()
			return nil, err
		}
		if mainCanonical != canonical {
			mainBefore, err = collectDocs(main)
			if err != nil {
				lock.Close()
				return nil, err
			}
		}
	}
	if err = lock.Close(); err != nil {
		return nil, err
	}
	if err = check(); err != nil {
		return nil, err
	}
	lock, err = drafttx.Acquire(root)
	if err != nil {
		return nil, err
	}
	currentRoot, err := filepath.EvalSymlinks(root)
	if err != nil || currentRoot != canonical {
		lock.Close()
		return nil, fmt.Errorf("%w: путь проекта изменился", errDraftChanged)
	}
	if mainBefore != nil {
		currentMain, mainErr := filepath.EvalSymlinks(main)
		if mainErr != nil || currentMain != mainCanonical {
			lock.Close()
			return nil, fmt.Errorf("%w: путь основного дерева изменился", errDraftChanged)
		}
		mainAfter, mainErr := collectDocs(main)
		if mainErr != nil {
			lock.Close()
			return nil, mainErr
		}
		if !reflect.DeepEqual(mainBefore, mainAfter) {
			lock.Close()
			return nil, errDraftChanged
		}
	}
	after, err := collectDocs(root)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		lock.Close()
		return nil, errDraftChanged
	}
	return lock, nil
}

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
	case "add", "set", "dep", "sort", "list", "show", "id", "move", "ask", "decide", "fail", "debt", "debt-done", "arm", "plan", "chain", "file", "progress", "elapsed", "lint", "closable", "close", "batch", "slot", "pilot", "stops", "kinds", "remind":
		return true
	case "team":
		return len(args) > 1 && args[1] == "show"
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
	cliMainRoot = stage.MainRoot(root)
	l, err := drafttx.Acquire(root)
	if err != nil {
		return err
	}
	cliTx = l
	return nil
}

func taskMainRoot(root string) string {
	if isStagingBoard(root) {
		return root
	}
	if cliTx != nil && root == cliWorktreeRoot {
		return cliMainRoot
	}
	return stage.MainRoot(root)
}
func cliQueuePublication(root string, paths []string, c CommitOpts) bool {
	if cliTx == nil || c.Msg == "" {
		return false
	}
	cliPublications = append(cliPublications, deferredPublication{root: root, paths: append([]string(nil), paths...), opts: c})
	return true
}
func cliFinishDraftLock(success bool) (result string, finalErr error) {
	var publication string
	publicationRoot := cliWorktreeRoot
	finishPublication := func() error {
		if publication == "" {
			return nil
		}
		err := drafttx.FinishPublication(publicationRoot, publication)
		if err == nil {
			publication = ""
		}
		return err
	}
	defer func() {
		if err := finishPublication(); err != nil {
			if finalErr != nil {
				finalErr = fmt.Errorf("%v; журнал публикации не снят: %w", finalErr, err)
			} else {
				finalErr = fmt.Errorf("применено, журнал публикации не снят: %w", err)
			}
		}
		if finalErr != nil {
			cliRemovals = nil
			cliPublications = nil
			cliAfterUnlock = nil
		}
	}()
	if cliTx != nil {
		if len(cliRemovals) > 0 || (success && len(cliPublications) > 0) {
			var err error
			publication, err = cliTx.BeginPublication()
			if err != nil {
				cliTx.Close()
				cliTx = nil
				return "", fmt.Errorf("применено, публикация не начата: %w", err)
			}
		}
		err := cliTx.Close()
		cliTx = nil
		if err != nil {
			return "", err
		}
	}
	// The file was already removed under the board lock. Git's index is
	// updated only after releasing it, so hooks and wrappers may call taskctl.
	untracked := map[string]bool{}
	removals := cliRemovals
	cliRemovals = nil
	for _, r := range removals {
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
	if !success {
		cliPublications = nil
		cliAfterUnlock = nil
		return "", nil
	}
	var tails string
	publications := cliPublications
	cliPublications = nil
	for _, p := range publications {
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
			cliAfterUnlock = nil
			return tails, fmt.Errorf("применено, публикация не завершена: %w", err)
		}
		tails += tail
	}
	if err := finishPublication(); err != nil {
		return tails, fmt.Errorf("применено, журнал публикации не снят: %w", err)
	}
	callbacks := cliAfterUnlock
	cliAfterUnlock = nil
	for _, f := range callbacks {
		tails += f()
	}
	return tails, nil
}

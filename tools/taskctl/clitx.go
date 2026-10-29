package main

import (
	"fmt"
	"os"
	"path/filepath"

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
var cliPublications []deferredPublication
var cliAfterUnlock []func() string

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
	if !success {
		cliPublications = nil
		cliAfterUnlock = nil
		return "", nil
	}
	var tails string
	for _, p := range cliPublications {
		tail, err := p.opts.apply(p.root, p.paths)
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

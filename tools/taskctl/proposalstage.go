package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/drafttx"
)

// stagingBoard is an isolated non-git copy; commands run on the copy before
// any live image is installed.
type stagingBoard struct {
	root   string
	before map[string]drafttx.Image
}

func collectDocs(root string) (map[string]drafttx.Image, error) {
	out := map[string]drafttx.Image{}
	dir := filepath.Join(root, "docs")
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("символическая ссылка в материале транзакции: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("необычный файл в материале транзакции: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		img, err := drafttx.Read(path)
		if err != nil {
			return err
		}
		out[rel] = img
		return nil
	})
	return out, err
}
func newStagingBoard(root string) (*stagingBoard, error) {
	before, err := collectDocs(root)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "devkit-draft-stage-")
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*stagingBoard, error) { os.RemoveAll(dir); return nil, err }
	for rel, img := range before {
		path := filepath.Join(dir, rel)
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fail(err)
		}
		if err = os.WriteFile(path, img.Data, os.FileMode(img.Mode)); err != nil {
			return fail(err)
		}
	}
	return &stagingBoard{root: dir, before: before}, nil
}
func (s *stagingBoard) Close() error { return os.RemoveAll(s.root) }

// changes returns complete before/after images, placing source files last.
func (s *stagingBoard) changes(sources map[string]bool) ([]drafttx.Change, error) {
	after, err := collectDocs(s.root)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for p := range s.before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	names := make([]string, 0, len(paths))
	for p := range paths {
		names = append(names, p)
	}
	sort.Slice(names, func(i, j int) bool {
		if sources[names[i]] != sources[names[j]] {
			return !sources[names[i]]
		}
		return strings.Compare(names[i], names[j]) < 0
	})
	var out []drafttx.Change
	for _, p := range names {
		a, b := s.before[p], after[p]
		if a.Exists == b.Exists && a.Mode == b.Mode && bytes.Equal(a.Data, b.Data) {
			continue
		}
		out = append(out, drafttx.Change{Path: p, Before: a, After: b})
	}
	return out, nil
}

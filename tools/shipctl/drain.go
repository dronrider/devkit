package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type drainFailure struct {
	Artifact string   `json:"artifact"`
	Tasks    []string `json:"tasks"`
	Reason   string   `json:"reason"`
}

// Коммит пометки taskctl fail меняет HEAD, но не груз выката вне docs/.
func drainArtifact(root, main string) (string, error) {
	tree, err := git(root, "ls-tree", "-rz", main)
	if err != nil {
		return "", err
	}
	var code []string
	for _, entry := range strings.Split(tree, "\x00") {
		_, path, ok := strings.Cut(entry, "\t")
		if ok && !strings.HasPrefix(path, "docs/") {
			code = append(code, entry)
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(code, "\x00")))), nil
}

func drainFailurePath(root string) (string, error) {
	path, err := git(root, "rev-parse", "--git-path", "devkit/ship-drain-failure.json")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return path, nil
}

func readDrainFailure(root string) (drainFailure, error) {
	var st drainFailure
	path, err := drainFailurePath(root)
	if err != nil {
		return st, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(data, &st)
	return st, err
}

func saveDrainFailure(root string, st drainFailure) error {
	path, err := drainFailurePath(root)
	if err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "ship-drain-failure-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func clearDrainFailure(root string) error {
	path, err := drainFailurePath(root)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

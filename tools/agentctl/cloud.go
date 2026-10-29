package main

import (
	"github.com/dronrider/devkit/internal/draftmeta"
	"os"
)

func cloudForRoots(id string, roots ...string) ([]*draftmeta.CloudLock, error) {
	seen := map[string]bool{}
	var locks []*draftmeta.CloudLock
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		lock, err := draftmeta.CloudShared(root, id)
		if err != nil {
			for _, l := range locks {
				l.Close()
			}
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, nil
}
func closeCloud(locks []*draftmeta.CloudLock) {
	for _, l := range locks {
		l.Close()
	}
}
func cloudFiles(locks []*draftmeta.CloudLock) []*os.File {
	out := make([]*os.File, 0, len(locks))
	for _, l := range locks {
		out = append(out, l.File())
	}
	return out
}

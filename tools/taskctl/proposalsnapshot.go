package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// proposalBasis includes the record, the effective definition, board, archive,
// team and target files. Missing files are distinct from empty ones.
func proposalBasis(root string, source []byte, state draftproposal.State) (string, error) {
	h, err := draftmeta.Parse(source)
	if err != nil {
		return "", err
	}
	set, err := draftTypeSet(root)
	if err != nil {
		return "", err
	}
	def, ok := set.Types[h.Type]
	if !ok {
		return "", fmt.Errorf("типа %q нет в словаре", h.Type)
	}
	paths := []string{boardPath(root), archivePath(root), team.Path(stage.Home()), def.File}
	seen := map[string]bool{}
	for _, p := range state.Proposals {
		if p.Target == "" {
			continue
		}
		if !idRe.MatchString(p.Target) {
			return "", fmt.Errorf("%s: неверное назначение %q", p.ID, p.Target)
		}
		path := taskFileAbs(root, p.Target)
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	sort.Strings(paths)
	parts := [][]byte{source}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			parts = append(parts, []byte(path), []byte("absent"))
			continue
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, []byte(path), []byte("present"), data)
	}
	return draftproposal.Digest(parts...), nil
}

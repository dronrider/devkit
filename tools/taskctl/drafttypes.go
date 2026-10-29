package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/drafttypes"
)

func draftTypeSet(root string) (*drafttypes.Set, error) {
	home, _ := os.UserHomeDir()
	kit := drafttypes.KitDir(root, home)
	if kit == "" {
		return nil, fmt.Errorf("каталог kit/draft-types не найден: укажите DEVKIT_HOME")
	}
	return drafttypes.Load(kit, drafttypes.ProjectDir(root), filepath.Join(filepath.Dir(kit), "skills"))
}

func cmdDraftTypes(root, dump string, jsonOut bool) (string, error) {
	set, err := draftTypeSet(root)
	if err != nil {
		return "", err
	}
	if dump != "" {
		d, ok := set.Types[dump]
		if !ok {
			return "", fmt.Errorf("типа %q нет в словаре", dump)
		}
		data, err := os.ReadFile(d.File)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	if jsonOut {
		defs := make([]drafttypes.Definition, 0, len(set.Names))
		for _, n := range set.Names {
			defs = append(defs, set.Types[n])
		}
		return marshal(struct {
			Types    []drafttypes.Definition `json:"types"`
			Warnings []string                `json:"warnings"`
		}{defs, set.Warnings})
	}
	var b strings.Builder
	for _, n := range set.Names {
		d := set.Types[n]
		layer := "встроенный"
		if d.Project {
			layer = "проектный"
		}
		fmt.Fprintf(&b, "%s: %s (%s)\n", d.Name, d.Title, layer)
	}
	for _, w := range set.Warnings {
		fmt.Fprintf(&b, "предупреждение: %s\n", w)
	}
	return strings.TrimSpace(b.String()), nil
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShipCleanProtectsCodeAndTrainCards(t *testing.T) {
	for _, tc := range []struct {
		name, path      string
		protected       []string
		staged, allowed bool
	}{
		{"чужая карточка", "docs/tasks/XR-002.md", []string{"XR-001"}, false, true},
		{"чужая карточка в индексе", "docs/tasks/XR-002.md", []string{"XR-001"}, true, true},
		{"своя карточка", "docs/tasks/XR-001.md", []string{"XR-001"}, false, false},
		{"своя карточка в индексе", "docs/tasks/XR-001.md", []string{"XR-001"}, true, false},
		{"код", "src/main.go", nil, false, false},
		{"конфигурация", "config.toml", nil, true, false},
		{"архив", "docs/tasks/archive/XR-002.md", nil, false, false},
		{"обычная документация", "docs/guide.md", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := setup(t, rowInProg, "")
			write(t, root, tc.path, "before\n")
			gitT(t, root, "add", "--", tc.path)
			gitT(t, root, "commit", "-qm", "docs(tasks): XR-002 fixture")
			write(t, root, tc.path, "after\n")
			if tc.staged {
				gitT(t, root, "add", "--", tc.path)
			}
			before := gitT(t, root, "status", "--porcelain")
			err := requireCleanShip(root, tc.protected)
			if (err == nil) != tc.allowed {
				t.Fatalf("допуск=%t: %v", tc.allowed, err)
			}
			if after := gitT(t, root, "status", "--porcelain"); after != before {
				t.Fatal("проверка изменила индекс или дерево")
			}
			data, err := os.ReadFile(filepath.Join(root, tc.path))
			if err != nil || string(data) != "after\n" {
				t.Fatalf("правки потеряны: %s (%v)", data, err)
			}
		})
	}
}

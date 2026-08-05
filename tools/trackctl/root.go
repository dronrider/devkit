package main

import (
	"os"
	"path/filepath"
)

// findRoot ищет рабочий корень devkit тем же порядком, что соседние утилиты:
// сперва редирект корп-контура, потом подъём до docs/TASKS.md. trackctl доску
// не читает и не правит, но привязка к трекеру и журнал лежат в той же
// директории .devkit, что у shipctl, и находиться она обязана одинаково.
func findRoot(start string) (string, error) {
	if local := corpLocal(start); local != "" {
		return local, nil
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "TASKS.md")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", corpRootErr(start)
		}
		dir = parent
	}
}

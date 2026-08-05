package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// logRun дописывает строку о запуске в журнал .devkit/log репозитория, как
// taskctl и shipctl: по журналу видно, гоняется ли regcheck на багфиксах
// (shipctl merge подсказывает по нему). Журнал ведётся только там, где есть
// .devkit, провал записи работу не ломает.
func logRun(startDir string, code int) {
	root, err := gitOut(startDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\tregcheck\trun\t%d\n", time.Now().Format("2006-01-02T15:04:05"), code)
}

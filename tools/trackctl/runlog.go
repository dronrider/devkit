package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Стартовая директория и подкоманда для журнала, выставляет main до разбора.
var logStart, logCmd string

// logRun дописывает строку о запуске в журнал .devkit/log проекта, как в
// taskctl и shipctl: по нему видно, какие команды реально гоняются и как часто
// падают. Журнал ведётся только там, где есть .devkit, провал записи работу не
// ломает.
func logRun(code int) {
	if logCmd == "" {
		return
	}
	root, err := findRoot(logStart)
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
	fmt.Fprintf(f, "%s\ttrackctl\t%s\t%d\n", time.Now().Format("2006-01-02T15:04:05"), logCmd, code)
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
)

// Стартовая директория и подкоманда для журнала, выставляет main до разбора.
var logStart, logCmd string

// logRun дописывает строку о запуске в журнал .devkit/log проекта, как в
// taskctl: статистика, какие команды реально гоняются и как часто падают.
// Журнал ведётся только там, где есть .devkit (обвязку заводит devkitctl),
// провал записи работу не ломает.
func logRun(code int) {
	if logCmd == "" {
		return
	}
	root, err := findRoot(logStart)
	if err != nil {
		return
	}
	_ = appendRunLog(root, logCmd, code)
}

func appendRunLog(root, command string, code int) error {
	path := filepath.Join(root, ".devkit", "log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\tshipctl\t%s\t%d\t%s\n", time.Now().Format("2006-01-02T15:04:05"), command, code, taskhead.ExitClass(code))
	return err
}

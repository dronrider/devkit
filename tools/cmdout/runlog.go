package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// logRun дописывает строку о запуске в журнал .devkit/log репозитория, как
// regcheck и shipctl: по журналу видно, гоняется ли cmdout на багфиксах. Журнал
// ведётся только там, где есть .devkit, провал записи работу не ломает.
func logRun(startDir string, code int) {
	out, err := exec.Command("git", "-C", startDir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return
	}
	root := strings.TrimSpace(string(out))
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\tcmdout\trun\t%d\n", time.Now().Format("2006-01-02T15:04:05"), code)
}

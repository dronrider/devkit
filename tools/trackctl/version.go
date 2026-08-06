package main

import (
	"fmt"
	"io"
	"os"
)

// Версию и коммит зашивает сборка (devkitctl build) линковкой,
// -ldflags "-X main.version=... -X main.commit=...". Значения по умолчанию
// остаются в бинаре, собранном голым go build мимо devkit, и по строке версии
// видно, что версии у него нет.
var (
	version = "dev"
	commit  = "unknown"
)

const toolName = "trackctl"

// printVersion разбирает --version до всех прочих аргументов: флагу не нужны ни
// проект, ни доска, ни -C, и в сеть он не ходит. Формат строки один на все
// утилиты devkit, потому что читает её не только человек: doctor берёт отсюда
// коммит бинаря. Печатается она первой и единственной строкой.
func printVersion(args []string, w io.Writer) bool {
	if len(args) == 0 || (args[0] != "--version" && args[0] != "-version") {
		return false
	}
	fmt.Fprintf(w, "%s %s (%s)\n", toolName, version, commit)
	return true
}

// versionRequested зовётся первой строкой main, до разбора чего бы то ни было.
func versionRequested() bool {
	return printVersion(os.Args[1:], os.Stdout)
}

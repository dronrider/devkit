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

const toolName = "obeycheck"

// printVersion разбирает --version до всех прочих аргументов: флагу не нужны ни
// проект, ни доска, ни -C, и в сеть он не ходит. Ищется он по всей строке
// аргументов, как -h у соседней helpRequested: -C ставится и до команды, и
// после, и версия не должна зависеть от того, что написано рядом. Формат строки
// один на все утилиты devkit, потому что читает её не только человек: doctor
// берёт отсюда коммит бинаря. Печатается она первой и единственной строкой.
func printVersion(args []string, w io.Writer) bool {
	for _, a := range args {
		if a == "--version" || a == "-version" {
			fmt.Fprintf(w, "%s %s (%s)\n", toolName, version, commit)
			return true
		}
	}
	return false
}

// versionRequested зовётся первой строкой main, до разбора чего бы то ни было.
func versionRequested() bool {
	return printVersion(os.Args[1:], os.Stdout)
}

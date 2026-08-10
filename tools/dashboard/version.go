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

const toolName = "dashboard"

// printVersion разбирает --version до всех прочих аргументов. Формат строки
// один на все утилиты devkit: её читает не только человек, doctor берёт
// отсюда коммит бинаря, а по смене версии в /healthz выкат проверяется
// машинно.
func printVersion(args []string, w io.Writer) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
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

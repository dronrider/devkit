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

const toolName = "agentctl"

// printVersion разбирает --version до всех прочих аргументов: флагу не нужны ни
// проект, ни доска, ни -C, и в сеть он не ходит. Ищется он не только в первой
// позиции, как -h у соседней helpRequested: -C ставится и до команды, и после,
// и версия не должна зависеть от того, что написано рядом. Формат строки один
// на все утилиты devkit, потому что читает её не только человек: doctor берёт
// отсюда коммит бинаря. Печатается она первой и единственной строкой.
func printVersion(args []string, w io.Writer) bool {
	for _, a := range args {
		// Голое «--» останавливает разбор: дальше идут не наши флаги, а чужая
		// полезная нагрузка. У regcheck там лежит команда теста, и --version
		// внутри неё принадлежит ей; напечатать вместо прогона версию и выйти
		// нулём значит соврать обвязке, что тест прошёл.
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

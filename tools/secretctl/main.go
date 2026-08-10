package main

import (
	"flag"
	"fmt"
	"os"
)

const usageText = `secretctl: имена агенту, значение в подпроцесс

Принцип цели DK-207: модель оперирует именами секретов, а значение в момент
исполнения подставляет утилита. Команды, печатающей значение секрета в stdout,
у утилиты нет.

  names                          перечисляет имена секретов хранилища, по
                                 одному на строку, без значений
  exec <имя> -- <команда> ...    запускает команду в подпроцессе, подставив
                                 значение секрета в его окружение: имя
                                 переменной равно имени секрета

Бэкенд выбирается по окружению: macOS Keychain через security CLI, когда
доступен; файлы с правами 0600 в ином случае. Разбор в
tools/secretctl/README.md.

Флаг --version печатает версию и коммит, как у соседних утилит.
`

func helpRequested(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "-help" || a == "--help" {
			return true
		}
	}
	return false
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

func main() {
	if versionRequested() {
		return
	}
	args := os.Args[1:]
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	if helpRequested(args) {
		fmt.Print(usageText)
		return
	}
	switch args[0] {
	case "names":
		fs := flag.NewFlagSet("names", flag.ExitOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
		fs.Parse(args[1:])
		backend, err := defaultBackend()
		if err != nil {
			fail(err)
		}
		names, err := backend.Names()
		if err != nil {
			fail(err)
		}
		for _, n := range names {
			fmt.Println(n)
		}
	case "exec":
		// «--» обязателен: имя секрета отделяется от команды подпроцесса
		// однозначно, и флаги команды не путаются с флагами secretctl.
		rest := args[1:]
		sep := -1
		for i, a := range rest {
			if a == "--" {
				sep = i
				break
			}
		}
		if sep < 0 {
			fail(fmt.Errorf("жду «--» перед командой: exec <имя> -- <команда> [аргументы...]"))
		}
		fs := flag.NewFlagSet("exec", flag.ExitOnError)
		fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
		fs.Parse(rest[:sep])
		if fs.NArg() != 1 {
			fail(fmt.Errorf("жду одно имя секрета: exec <имя> -- <команда> [аргументы...]"))
		}
		name := fs.Arg(0)
		command := rest[sep+1:]
		backend, err := defaultBackend()
		if err != nil {
			fail(err)
		}
		code, err := cmdExec(backend, name, command)
		if err != nil {
			fail(err)
		}
		if code != 0 {
			os.Exit(code)
		}
	case "help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", args[0], usageText)
		os.Exit(2)
	}
}

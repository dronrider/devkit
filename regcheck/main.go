package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const usageText = `regcheck: регрессионный тест обязан краснеть на старом коде

Команда теста гоняется дважды: в рабочем дереве (должна пройти) и во
временном worktree со старым кодом, куда из рабочего дерева переносятся
только тестовые файлы (должна упасть). Тест, зелёный в обоих прогонах,
регрессию не ловит и покрытием не считается (RULES.md, «Тесты обязательны»).

  regcheck [-C dir] [--base ref] [--tests f1,f2] -- <команда теста ...>

  -C dir        откуда искать корень репозитория (по умолчанию текущая)
  --base ref    где живёт старый код: по умолчанию HEAD при незакоммиченной
                правке, иначе main; для правки, закоммиченной на ветке,
                указывать --base main явно
  --tests ...   тестовые файлы через запятую, когда автопоиск (изменённые
                относительно базы *_test.*, test_*, *.spec.*, каталоги
                tests/ и подобные) не нашёл нужное

Ограничение: правка и тест должны жить в разных файлах, иначе тест не
перенести на старый код (инлайновые тесты Rust так не проверяются).
Рабочее дерево утилита не трогает, старый код собирается в worktree.
`

func main() {
	args := os.Args[1:]
	var cmd []string
	for i, a := range args {
		if a == "--" {
			cmd = args[i+1:]
			args = args[:i]
			break
		}
	}
	fs := flag.NewFlagSet("regcheck", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	dir := fs.String("C", ".", "откуда искать корень репозитория")
	base := fs.String("base", "", "реф старого кода")
	tests := fs.String("tests", "", "тестовые файлы через запятую")
	fs.Parse(args)
	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "лишние аргументы %v: команда теста ставится после «--»\n\n%s", fs.Args(), usageText)
		os.Exit(2)
	}
	p := Params{Dir: *dir, Base: *base, Cmd: cmd}
	if *tests != "" {
		for _, t := range strings.Split(*tests, ",") {
			if t = strings.TrimSpace(t); t != "" {
				p.Tests = append(p.Tests, t)
			}
		}
	}
	msg, err := Run(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
	fmt.Println(msg)
}

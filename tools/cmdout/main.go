package main

import (
	"fmt"
	"os"

	"github.com/dronrider/devkit/internal/frame"
)

const usageText = `cmdout: вывод команды -> файлу, агенту выжимку

Команда гоняется под обёрткой, полный вывод пишется файлом в
.devkit/cmdout/<timestamp>-<slug>/out репозитория, а в стандартный вывод
уходит выжимка. Ниже порога (4K символов или 100 строк) выжимка не строится:
агенту отдаётся полный вывод как есть, и обёртка ведёт себя прозрачно. Сверху
порога выжимка несёт код возврата, число строк, сколько осталось за кадром,
значимые строки с маркерами ошибок, хвост и путь к файлу полного вывода.

Формат и порог описаны в docs/lld/DK-137-cmdout-wrapper.md, модуль общего
каркаса в docs/lld/DK-237-shared-go-module.md.

  cmdout [--version] <команда ...>

  --version    печатает строку версии и выходит, до разбора команды

Команда ставится как есть, без разделителя «--». Имя каталога вывода (slug)
берётся от первой лексемы команды, например «git diff» даёт «git».

Код возврата cmdout совпадает с кодом возврата команды.
`

func main() {
	if versionRequested() {
		return
	}
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	os.Exit(run(args))
}

// run держит логику отдельно от main, чтобы тесты бинаря звали её без порождения
// подпроцесса: на собранном бинаре проверяется разбор аргументов и печать, а не
// внутренний вызов frame.Capture.
func run(args []string) int {
	summary, err := frame.Capture(".", args)
	if err != nil {
		logRun(".", 1)
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	if summary.Summarized {
		fmt.Print(summary.Render())
	} else {
		fmt.Print(summary.Raw)
	}
	logRun(".", summary.Exit)
	return summary.Exit
}

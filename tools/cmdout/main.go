package main

import (
	"flag"
	"fmt"
	"os"
	"time"

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
  cmdout clean [--days N] [--dry-run]

  --version    печатает строку версии и выходит, до разбора команды

Команда ставится как есть, без разделителя «--». Имя каталога вывода (slug)
берётся от первой лексемы команды, например «git diff» даёт «git».

Код возврата cmdout совпадает с кодом возврата команды.

Подкоманда clean удаляет каталоги .devkit/cmdout старше порога возраста (по
умолчанию 7 дней, перекрывается --days). Порог и правило живут в internal/frame,
как и порог выжимки, чтобы чистка и запись не разъехались. --dry-run печатает
пути каталогов под удаление, не трогая файлы. doctor зовёт clean подпроцессом,
порог ему не дублируется: на сухом прогоне он видит, что cmdout счёл устаревшим.
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
	if len(args) > 0 && args[0] == "clean" {
		return runClean(args[1:])
	}
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

// runClean разбирает флаги чистки и зовёт frame.Clean из того же модуля, где
// живёт порог возраста. Умолчание дней берётся из frame.DefaultCleanDays, а не
// дублируется тут: правило «старше недели это мусор» одно на пишущий и чистящий
// код. now это время вызова, для подкоманды это нормально: детерминизм через
// now параметр нужен тесту frame, а не живому прогону. Каталоги читаются от
// текущего каталога, как и у writeFull, через git root.
func runClean(args []string) int {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	days := fs.Int("days", frame.DefaultCleanDays,
		"удалять каталоги старше N дней (по умолчанию 7)")
	dryRun := fs.Bool("dry-run", false,
		"печатать пути каталогов под удаление, не трогая файлы")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *days < 0 {
		fmt.Fprintln(os.Stderr, "ошибка: --days не может быть отрицательным")
		return 2
	}
	maxAge := time.Duration(*days) * 24 * time.Hour
	stats, err := frame.Clean(".", maxAge, time.Now(), *dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	for _, p := range stats.Removed {
		fmt.Println(p)
	}
	if !*dryRun && len(stats.Removed) > 0 {
		fmt.Fprintf(os.Stderr, "почищено %d каталогов, освобождено %d байт\n",
			len(stats.Removed), stats.Bytes)
	}
	return 0
}

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
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
  cmdout digest [слово]

  --version    печатает строку версии и выходит, до разбора команды

Команда ставится как есть, без разделителя «--». Имя каталога вывода (slug)
берётся от первой лексемы команды, например «git diff» даёт «git».

Код возврата cmdout совпадает с кодом возврата команды.

Подкоманда clean удаляет каталоги .devkit/cmdout старше порога возраста (по
умолчанию 7 дней, перекрывается --days). Порог и правило живут в internal/frame,
как и порог выжимки, чтобы чистка и запись не разъехались. --dry-run печатает
пути каталогов под удаление, не трогая файлы. doctor зовёт clean подпроцессом,
порог ему не дублируется: на сухом прогоне он видит, что cmdout счёл устаревшим.

Подкоманда digest берёт готовый вывод из stdin, а не запускает команду: её
зовёт PostToolUse-хук длинного вывода (hooks/longout.py, DK-499), у которого
вывод Bash уже лежит в событии. На пороге и выше печатается выжимка, полный
текст уходит файлом, как у обычного прогона; ниже порога подкоманда молчит и
ничего не пишет, чтобы короткая команда прошла в контекст нетронутой и не
оставляла после себя пустых каталогов. Необязательное слово после digest
держит slug имени каталога (первое слово команды), без него slug падает на
«cmd». Поля exit в этой выжимке нет: у хука нет кода возврата команды, и поле
врало бы «нет различий» на git diff с различиями.
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
	if len(args) > 0 && args[0] == "digest" {
		return runDigest(args[1:])
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

// runDigest берёт готовый вывод из stdin и строит по нему выжимку для
// PostToolUse-хука длинного вывода (DK-499): событие Bash уже содержит вывод,
// запускать команду заново некому. Порог смотрит frame.OverThreshold до
// Summarize: ниже порога stdin уходит молча и без файла, чтобы короткая команда
// дошла до модели нетронутой и не оставляла после себя пустого каталога в
// .devkit/cmdout. На пороге Summarize пишет полный текст файлом и печатается
// сводка; поле exit из неё уходит: кода возврата команды в событии PostToolUse
// нет, и всегдашний 0 врал бы «нет различий» на git diff с различиями. Выход 0
// при пустом stdout означает «подменять нечего», и хук оставляет вывод как
// есть; так же ведёт себя подкоманда вне git-дерева, где полного файла не
// будет: без файла на диске подмена потеряла бы текст.
func runDigest(args []string) int {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка: чтение stdin:", err)
		return 1
	}
	if !frame.OverThreshold(in) {
		return 0
	}
	// Пустой argv не ошибочен: без слова слог падает на «cmd», и сводка
	// строится, как и у Summarize из прочего готового вывода.
	summary := frame.Summarize(".", args, in, 0)
	if summary.Path == "" {
		return 0
	}
	rendered := summary.Render()
	if first, rest, ok := strings.Cut(rendered, "\n"); ok && strings.HasPrefix(first, "exit: ") {
		rendered = rest
	}
	fmt.Print(rendered)
	return 0
}

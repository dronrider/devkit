package frame

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// DetachRunFlag это слово, которым бинарь зовут в режиме шага. Флаг живёт тут,
// а не в одном cmdout: рамки argv переживаемого шага это договор frame с
// задачей DK-722, и тесты модуля проверяют его тем же словом.
const DetachRunFlag = "--detach-run"

// Detached это поднятый переживающий шаг: каталог вывода и pid потомка.
type Detached struct {
	Dir string
	Pid int
}

// Detach поднимает argv переживающим шагом (DK-722) и сразу возвращается.
// Потомок это self в режиме шага, ушедший в свой сеанс: выход головы его не
// убивает, и группе процессов головы он оттого не принадлежит. Меткой живого
// шага становится файл pid в каталоге вывода, по нему конвейер задачи ждёт
// конца шага перед следующим проходом. Каталог заводится обычным правилом
// вывода, чистит его тот же cmdout clean.
func Detach(dir, self string, argv []string) (*Detached, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("нужна команда для переживаемого шага")
	}
	out, err := outDir(dir, argv)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "cmd"),
		[]byte(strings.Join(argv, " ")+"\n"), 0o644); err != nil {
		return nil, err
	}
	runner := exec.Command(self, append([]string{DetachRunFlag, out}, argv...)...)
	runner.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := runner.Start(); err != nil {
		return nil, fmt.Errorf("подъём шага: %v", err)
	}
	pid := runner.Process.Pid
	if err := os.WriteFile(filepath.Join(out, "pid"),
		[]byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		// Шаг без метки это шаг, которого для конвейера нет: следующий проход
		// поднимался бы поверх идущей работы, и тихо. Убитый старт честнее.
		_ = runner.Process.Kill()
		return nil, err
	}
	// Ждать потомка родителю не нужно: он переживает и родителя, и голову,
	// ради этого режим и заведён.
	return &Detached{Dir: out, Pid: pid}, nil
}

// RunInto это режим шага: команда запускается, весь вывод копится файлом out
// по мере хода, код возврата ложится в exit. Никакой выжимки тут нет, звать её
// некому, итог прочтёт следующий проход конвейера из файла.
func RunInto(dir string, argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "ошибка: шагу нужна команда")
		return 2
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	defer out.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = out
	cmd.Stderr = out
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			fmt.Fprintln(out, "ошибка запуска:", err)
			code = 127
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"),
		[]byte(fmt.Sprintf("%d\n", code)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
	}
	return code
}

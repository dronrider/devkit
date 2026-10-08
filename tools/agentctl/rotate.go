package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// execRotateDefault это порог ротации исполнителя-субагента, когда ключа
// exec_rotate_tokens в машинном конфиге нет. Число из статистики работы
// диспетчеров: типовая крупная пачка задач стоит исполнителю 470-490 тысяч
// токенов, усталость видна с 600 тысяч, деградация с 900. Порог отсекает
// исполнителя после одной крупной пачки.
const execRotateDefault = 500000

// execRotate отдаёт порог ротации и повод этого числа. Ключ живёт в машинном
// конфиге, а умолчание считает agentctl, а не потребитель: число одно на
// диспетчера в чате и на дашборд, и разъехаться двум копиям константы негде.
func execRotate(l *layers, path string) (int, string) {
	if l != nil && l.ExecRotateTokens > 0 {
		return l.ExecRotateTokens, fmt.Sprintf("ключ exec_rotate_tokens машинного конфига %s", path)
	}
	if l != nil && rotateWarn(l) != "" {
		return execRotateDefault, fmt.Sprintf("умолчание agentctl, значение ключа exec_rotate_tokens в %s не годится", path)
	}
	return execRotateDefault, fmt.Sprintf("умолчание agentctl, ключа exec_rotate_tokens в %s нет", path)
}

// rotateWarn достаёт предупреждение про ключ порога, если слияние слоёв его
// оставило: битое значение ключа раскладку не роняет, но молчать о нём нельзя.
func rotateWarn(l *layers) string {
	for _, w := range l.Warns {
		if strings.Contains(w, "exec_rotate_tokens") {
			return w
		}
	}
	return ""
}

// cmdRotate печатает порог ротации двумя строками по образцу budget: машинное
// число первой, источник и повод второй. С ключом --mark дописывает строку
// журнала сессий о ротации: прежний и новый адрес, порог. Строка бронируется
// один раз общей механикой agentctl rotate (стык с DK-662, шов без ребра).
func cmdRotate(start, oldSess, newSess string, mark bool) (string, error) {
	dir, err := harnessDir(start)
	if err != nil {
		return "", err
	}
	l, err := mergeLayers(dir, machineConfigPath(), projectConfigPath(start))
	if err != nil {
		return "", err
	}
	n, why := execRotate(l, machineConfigPath())
	if w := rotateWarn(l); w != "" {
		why += "; " + w
	}
	text := fmt.Sprintf("rotate: %d\n%s", n, why)
	if mark {
		line, err := rotateMark(oldSess, newSess, n)
		if err != nil {
			return text, err
		}
		text += "\n" + line
	}
	return text, nil
}

// rotateMark пишет строку журнала сессий о ротации и возвращает её.
// Формат именованными полями как у turn-mark.py: слово хода «ротация»,
// повод несёт порог, новая сессия отдельным полем «новая». Отказ записи
// возвращается вызывающему: след механики теряется громко, а не молча
// (замечание 7 ревью DK-1312).
func rotateMark(oldSess, newSess string, threshold int) (string, error) {
	ts := time.Now().Format("2006-01-02T15:04:05")
	line := fmt.Sprintf("%s сессия %s ход ротация повод порог-%d дерево - новая %s\n",
		ts, dashlessField(oldSess), threshold, dashlessField(newSess))
	logPath := turnsLogPath()
	if logPath == "" {
		return line, fmt.Errorf("журнал сессий не найден: DEVKIT_TURN_MARK_LOG пуст, HOME не дал пути")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return line, fmt.Errorf("журнал сессий %s не создан: %w", logPath, err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return line, fmt.Errorf("журнал сессий %s не открыт: %w", logPath, err)
	}
	defer f.Close()
	if _, err := fmt.Fprint(f, line); err != nil {
		return line, fmt.Errorf("строка ротации не записана в %s: %w", logPath, err)
	}
	return line, nil
}

// turnsLogPath отдаёт путь журнала отметок ходов: тот же файл, что пишет
// turn-mark.py. Переменная DEVKIT_TURN_MARK_LOG перебивает умолчание.
func turnsLogPath() string {
	if p := os.Getenv("DEVKIT_TURN_MARK_LOG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".devkit", "turns.log")
}

// dashlessField пишет пустое поле дефисом, как у turn-mark.py.
func dashlessField(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "-"
	}
	return v
}

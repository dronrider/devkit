package main

import (
	"fmt"
	"strings"
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
// число первой, источник и повод второй.
func cmdRotate(start string) (string, error) {
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
	return fmt.Sprintf("rotate: %d\n%s", n, why), nil
}

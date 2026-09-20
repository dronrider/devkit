package main

// Расход прогона. Сессии стенда живут во временном доме, и дом этот сносится
// по концу прогона вместе с журналами харнеса: искать их потом своду негде, а
// в дереве транскриптов разработчика прогон виден работой оболочки без единого
// токена. Поэтому числа складывает сам стенд, пока дом цел, и кладёт их в
// отметку прогона раздела «Проверка». Оттуда их читает `taskctl spend` статьёй
// «стенд» (DK-913).

import (
	"path/filepath"

	"github.com/dronrider/devkit/internal/spend"
)

// homeUsage складывает расход всех сессий временного дома: и головных, и
// субагентских. Нечитаемый журнал пропускается молча: прогон кончился, дом
// сейчас снесут, и ронять из-за одного файла нечего.
func homeUsage(home string) spend.Usage {
	var out spend.Usage
	pats := []string{
		filepath.Join(spend.Dir(home), "*", "*.jsonl"),
		filepath.Join(spend.Dir(home), "*", "*", "subagents", "agent-*.jsonl"),
	}
	for _, pat := range pats {
		paths, _ := filepath.Glob(pat)
		for _, p := range paths {
			u, err := spend.Read(p)
			if err != nil {
				continue
			}
			out = out.Add(u)
		}
	}
	return out
}

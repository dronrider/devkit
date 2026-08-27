package main

import (
	"os"
	"path/filepath"
)

// Команда проверки сценария. Греп ищет построчно, а проверки сценариев ищут
// фразы в прозе, которую агент переформатирует по ширине. Фраза уезжает на две
// строки, греп её не находит, и клетка краснеет от переноса, а не от поведения
// агента (DK-547). phrase сжимает пробельные разрывы и в файле, и в искомой
// фразе, поэтому разбивка на строки на ответ не влияет.
//
// Вызывается она на месте грепа: первым аргументом фраза, вторым файл, ответ
// кодом возврата.
//
//	phrase "(виток стоять не будет)" "$f" || { echo "скобки сняты"; exit 1; }
const phraseScript = `#!/bin/sh
# Ищет фразу в файле, не глядя на разбивку по строкам. Кладёт её в окружение
# проверки сам стенд, исходник в tools/obeycheck/phrase.go.
if [ $# -ne 2 ]; then
	echo "phrase: жду фразу и файл, вижу $# аргументов" >&2
	exit 2
fi
if [ ! -f "$2" ]; then
	echo "phrase: файла $2 нет" >&2
	exit 2
fi
squeeze() { tr -s '[:space:]' ' '; }
needle=$(printf '%s' "$1" | squeeze)
needle=${needle# }
needle=${needle% }
if [ -z "$needle" ]; then
	echo "phrase: пустая фраза" >&2
	exit 2
fi
squeeze <"$2" | grep -qF -e "$needle"
`

const phraseName = "phrase"

// writePhrase кладёт команду в каталог прогона. Каталог свой у каждого
// прогона: сносится он вместе с прогоном, и на машине после стенда ничего не
// остаётся.
func writePhrase(bin string) error {
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(bin, phraseName), []byte(phraseScript), 0o755)
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Команда проверки сценария. Греп ищет построчно, а проверки сценариев ищут
// фразы в прозе, которую агент переформатирует по ширине. Фраза уезжает на две
// строки, греп её не находит, и клетка краснеет от переноса, а не от поведения
// агента (DK-547). phrase склеивает каждый блок текста в одну строку, поэтому
// разбивка по ширине на ответ не влияет. Абзац, заголовок и пункт списка при
// этом остаются каждый сам по себе, и фраза со стыка двух блоков не находится.
//
// Вызывается она на месте грепа: первым аргументом фраза, вторым файл, ответ
// кодом возврата.
//
//	phrase "(виток стоять не будет)" "$f" || { echo "скобки сняты"; exit 1; }
const phraseScript = `#!/bin/sh
# Ищет фразу в файле, не глядя на разбивку по строкам внутри блока. Кладёт её в
# окружение проверки сам стенд, исходник в tools/obeycheck/phrase.go.
if [ $# -ne 2 ]; then
	echo "phrase: жду фразу и файл, вижу $# аргументов" >&2
	exit 2
fi
if [ ! -f "$2" ]; then
	echo "phrase: файла $2 нет" >&2
	exit 2
fi
# Абзац, заголовок и пункт списка склеиваются каждый в свою строку. Через
# пустую строку и через границу пункта склейка не идёт. Иначе фраза со стыка
# двух блоков находилась бы как написанная, и проверка с || зеленела бы на
# тексте, где её нет.
blocks() {
	awk '
	function flush() { if (buf != "") { print buf; buf = "" } }
	{
		line = $0
		gsub(/[ \t\r]+/, " ", line)
		sub(/^ /, "", line)
		sub(/ $/, "", line)
		if (line == "") { flush(); next }
		if (line ~ /^(#+|[-*+>]+|[0-9]+[.)])( |$)/) flush()
		buf = (buf == "" ? line : buf " " line)
	}
	END { flush() }'
}
needle=$(printf '%s' "$1" | tr -s '[:space:]' ' ')
needle=${needle# }
needle=${needle% }
if [ -z "$needle" ]; then
	echo "phrase: пустая фраза" >&2
	exit 2
fi
blocks <"$2" | grep -qF -e "$needle"
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

// assistantReplies достаёт реплики ассистента из транскрипта: это вход судьи
// по умолчанию, слово «ответ» в ключе «вход». Транскрипт headless-прогона
// это поток stream-json по одному событию на строку, и формат его хрупок,
// поэтому знать его должно одно место. Реплика это текстовые блоки события
// «assistant», вызовы инструментов и их результаты в счёт не идут. Транскрипт
// без единой JSON-строки (команда прогона печатает голый текст) отдаётся
// целиком: там реплика и есть весь вывод.
func assistantReplies(transcript string) (string, error) {
	data, err := os.ReadFile(transcript)
	if err != nil {
		return "", fmt.Errorf("транскрипт: %v", err)
	}
	var parts []string
	jsonSeen := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "{") {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(t), &ev) != nil {
			continue
		}
		jsonSeen = true
		if ev.Type != "assistant" || len(ev.Message.Content) == 0 {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(ev.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					parts = append(parts, strings.TrimSpace(b.Text))
				}
			}
			continue
		}
		var text string
		if json.Unmarshal(ev.Message.Content, &text) == nil && strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	if !jsonSeen {
		return strings.TrimSpace(string(data)), nil
	}
	return strings.Join(parts, "\n\n"), nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Реестр целей под надзором сторожка цикла (tools/devkitctl/watch.py). Сторожок
// живёт вне сессии и сам по себе не знает, какая цель где ведётся, а гейт
// бюджета это единственное место, через которое проходят оба входа цикла:
// оболочка goal-run и сессия живого чата зовут его в начале каждого витка. Файл
// на цель поэтому кладёт гейт, а не оболочка.
//
// Свои поля гейт на каждом витке переписывает, вместе с отметкой stopped,
// которую ставит сам сторожок: вызов гейта это и есть движение цикла, и держать
// после него отметку прошлого стопа не за чем. Чужие ключи записи при этом
// переносятся как есть: в ней живёт не только гейт, и счётчик попыток
// перезапуска (DK-169) не должен пропадать на первом же витке.
const (
	watchDir   = ".devkit/goals"
	watchStamp = "2006-01-02T15:04:05"
)

// Порядок известных ключей записи, тот же, что у сторожка (watch.py, KEYS):
// сначала поля гейта, следом отметки сторожка, а незнакомое дописывается в
// хвост по алфавиту.
var watchKeys = []string{"goal", "root", "file", "seen", "stopped"}

// watchSlug делает из пути корня имя, годное в имя файла: два проекта с
// одинаковым именем директории не должны занимать одну запись реестра.
func watchSlug(root string) string {
	var b strings.Builder
	for _, r := range root {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return strings.Trim(b.String(), "-")
}

// watchRegister отмечает, что цель ведётся: сторожок берёт отсюда корень
// проекта и ID цели, а движение меряет по журналу цикла и по .devkit/log
// этого корня. Провал
// записи гейт не роняет, как и провал журнала запусков: без надзора цикл
// работает, просто молча.
func watchRegister(root, goalPath string, now time.Time) {
	path, err := goalPathOf(root, goalPath)
	if err != nil {
		return
	}
	id := strings.TrimSuffix(filepath.Base(path), ".md")
	if id == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, filepath.FromSlash(watchDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	entry := filepath.Join(dir, id+"-"+watchSlug(root)+".watch")
	data := watchRead(entry)
	delete(data, "stopped")
	data["goal"] = id
	data["root"] = root
	data["file"] = abs
	data["seen"] = now.Format(watchStamp)
	os.WriteFile(entry, []byte(watchBody(data)), 0o644)
}

// watchRead разбирает запись реестра в ключи и значения. Формат тот же, что у
// остальных локальных файлов devkit: строки «ключ = значение», решётка
// комментарий. Нечитаемая запись это пустой набор: гейт перепишет её своими
// полями, а ронять из-за неё виток незачем.
func watchRead(path string) map[string]string {
	data := map[string]string{}
	body, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	for _, ln := range strings.Split(string(body), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		key, val, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		data[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return data
}

// watchBody собирает запись обратно в текст: известные ключи в своём порядке,
// остальные следом по алфавиту, как это делает сторожок.
func watchBody(data map[string]string) string {
	var b strings.Builder
	known := map[string]bool{}
	for _, key := range watchKeys {
		known[key] = true
		if val := data[key]; val != "" {
			fmt.Fprintf(&b, "%s = %s\n", key, val)
		}
	}
	rest := make([]string, 0, len(data))
	for key := range data {
		if !known[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		fmt.Fprintf(&b, "%s = %s\n", key, data[key])
	}
	return b.String()
}

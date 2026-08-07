package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Реестр целей под надзором сторожка цикла (tools/devkitctl/watch.py). Сторожок
// живёт вне сессии и сам по себе не знает, какая цель где ведётся, а гейт
// бюджета это единственное место, через которое проходят оба входа цикла:
// оболочка goal-run и сессия живого чата зовут его в начале каждого витка. Файл
// на цель поэтому кладёт гейт, а не оболочка.
//
// Запись переписывается целиком на каждом витке, вместе с отметкой stopped,
// которую ставит сам сторожок: гейт витка это и есть движение цикла, и держать
// после него отметку прошлого стопа не за чем.
const (
	watchDir   = ".devkit/goals"
	watchStamp = "2006-01-02T15:04:05"
)

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
// проекта и ID цели, а движение меряет по .devkit/log этого корня. Провал
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
	body := fmt.Sprintf("goal = %s\nroot = %s\nfile = %s\nseen = %s\n",
		id, root, abs, now.Format(watchStamp))
	os.WriteFile(filepath.Join(dir, id+"-"+watchSlug(root)+".watch"), []byte(body), 0o644)
}

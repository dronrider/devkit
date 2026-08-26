package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// index.html грузит app.js тегом `<script type="module">`, а в модуле второе
// объявление того же имени это SyntaxError на весь файл: браузер не исполняет
// ни строки, и экран остаётся пустым каркасом (инцидент dash-poc, две функции
// workAge). `node --check` по .js разбирает файл как обычный скрипт и дубль
// пропускает, поэтому копия кладётся под именем .mjs: расширение переключает
// разбор в модульный, тот же, что у браузера.
func TestStaticAppParsesAsModule(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден, разбор статики не гоняется")
	}
	src, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "app.mjs")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("app.js не разбирается как модуль, браузер его не исполнит: %v\n%s", err, out)
	}
}

// Приписки, которые говорили за экран неправду, сняты все разом, и стенд
// сторожит их отсутствие: вернувшись, они врут снова.
//
// «Исполнителя не видно» стояла на признаке other и попадала ровно в задачи,
// которые человек вёл из дашборда; «поднята вне дашборда» и «идёт вне
// дашборда» объясняли словами то, что теперь говорит собой погашенная кнопка
// закрытия; чип «сессии нет» занимал место в каждой строке In progress и не
// звал ни к какому действию (замечания пользователя, разбор в board.go и
// tasks.go).
func TestStaticDropsLyingSays(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile(filepath.Join("static", "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		`"исполнителя не видно"`,
		"Исполнителя задачи на этой машине не видно",
		`"поднята вне дашборда"`,
		`"идёт вне дашборда"`,
		`el("span", "chip", "сессии нет")`,
		`row.run === "other"`,
	} {
		if strings.Contains(string(app), gone) {
			t.Errorf("в app.js вернулась снятая приписка: %s", gone)
		}
	}
	// Класс приписки убран вместе с ней: мёртвое правило переживает разметку и
	// возвращает её следующей правкой.
	if strings.Contains(string(css), ".anone") {
		t.Error("в style.css остался класс .anone от снятой приписки")
	}
}

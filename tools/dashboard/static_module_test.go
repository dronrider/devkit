package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

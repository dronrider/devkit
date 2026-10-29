package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Мысль быстрой записи переживает перерисовку главной и перезагрузку страницы
// (DK-1503, находка 1): текст жил только в поле, и обновление доски по
// возврату фокуса очищал его вместе с пересобранной формой. Стенд гонит
// главную в node с игрушечным DOM и памятью браузера
// (стенд testdata/poc_quicknote.mjs) и заодно держит возврат обзора без
// пустой заглушки и вертикальные отступы карточек главной (находки 2 и 3).
func TestQuickNoteDraft(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд быстрой записи пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_quicknote.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("быстрая запись главной: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

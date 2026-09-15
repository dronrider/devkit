package main

import (
	"strings"
	"testing"
)

// TestDecideAskNeedsHint: вопрос без рекомендации отказывает (DK-974). Первая
// голова DK-941 спросила тремя вариантами без рекомендации, верного среди
// них не было, и цель простояла ночь. Отказ называет обход и ключ --tie.
func TestDecideAskNeedsHint(t *testing.T) {
	root := setup(t)
	_, err := cmdDecide(root, DecideParams{ID: "XR-005", Ask: "разрез", Text: "куда вынести раздел?", Opts: []string{"свой скилл", "файл рядом"}, Now: decideDay})
	if err == nil {
		t.Fatal("вопрос без рекомендации завёлся")
	}
	for _, w := range []string{"рекомендации", "--hint", "--tie", "Обход"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("отказ не назвал %q: %v", w, err)
		}
	}
	if strings.Contains(readTask(t, root, "XR-005"), "«разрез»") {
		t.Fatal("отказ оставил развилку в файле")
	}
}

package main

import (
	"os"
	"strings"
	"testing"
)

// TestCloseHeldByFork: ворота закрытия (DK-804). Открытая человеческая
// развилка не даёт увезти задачу в архив, отказ называет её теми же словами,
// что и отказ старта, а ответ ворота отпускает. Развилка, оставленная
// исполнителю, закрытия не держит.
func TestCloseHeldByFork(t *testing.T) {
	root := setup(t)
	doc := readTask(t, root, "XR-005")
	held := doc + "\n## Развилки\n\n- «доступ»: токен положен в secretctl?\n  - решает: человек\n" +
		"  - рекомендация: считать приватным\n"
	if err := os.WriteFile(taskFilePath(root, "XR-005"), []byte(held), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-06"})
	if err == nil {
		t.Fatal("закрытие при открытой человеческой развилке должно отбиваться")
	}
	for _, want := range []string{"«доступ»", "токен положен в secretctl?",
		"taskctl decide XR-005 «доступ» --by человек"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q:\n%v", want, err)
		}
	}

	// Передача исполнителю снимает ворота: старта такая развилка не держала,
	// не держит и закрытия.
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "доступ", Leave: true, By: "человек", Text: "решить по факту"})
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-06"}); err != nil {
		t.Fatalf("развилка исполнителя не должна держать закрытие: %v", err)
	}
}

// TestCloseAnsweredForkPasses: ответ на развилку отпускает закрытие. Отдельный
// прогон от передачи: закрытие и передача идут разными строками перечня, и
// пропущенная проверка «решено» держала бы задачу отвеченной развилкой.
func TestCloseAnsweredForkPasses(t *testing.T) {
	root := setup(t)
	doc := readTask(t, root, "XR-005") + "\n## Развилки\n\n- «доступ»: токен положен?\n  - решает: человек\n"
	if err := os.WriteFile(taskFilePath(root, "XR-005"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-06"}); err == nil {
		t.Fatal("открытая развилка должна держать закрытие")
	}
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "доступ", By: "человек", Text: "положил, имя xr-token"})
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-06"}); err != nil {
		t.Fatalf("после ответа закрытие должно проходить: %v", err)
	}
}

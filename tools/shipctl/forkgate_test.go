package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forkDoc это файл задачи с перечнем развилок: тело подставляется строками
// перечня, остальное файлу ворот старта безразлично.
func forkDoc(t *testing.T, root, id, list string) {
	t.Helper()
	write(t, root, filepath.Join("docs", "tasks", id+".md"),
		"# "+id+": задел\n\n## Развилки\n\n"+list+"\n## DoD\n\nготово\n")
}

// TestStartHeldByFork: ворота старта (DK-804). Открытая человеческая развилка
// не даёт завести ветку, отказ называет её имя, вопрос и команду ответа, а
// доска при этом остаётся нетронутой: отказавший старт ничего не успел
// поменять. Ответ на развилку ворота отпускает.
func TestStartHeldByFork(t *testing.T) {
	root, callLog := setup(t, "", "")
	forkDoc(t, root, "XR-002", "- «доступ»: токен положен в secretctl?\n  - решает: человек\n  - рекомендация: считать приватным\n")

	_, err := cmdStart(root, StartParams{ID: "XR-002", Slug: "wt"})
	if err == nil {
		t.Fatal("старт при открытой человеческой развилке должен отбиваться")
	}
	for _, want := range []string{"«доступ»", "токен положен в secretctl?",
		"рекомендация: считать приватным", "taskctl decide XR-002 «доступ» --by человек", "--leave"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q:\n%v", want, err)
		}
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move XR-002") {
		t.Fatalf("отказавший старт двинул доску: %q", calls)
	}
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-xr-002")
	if _, err := os.Stat(wt); err == nil {
		t.Fatalf("отказавший старт завёл дерево %s", wt)
	}

	// Ответ снимает развилку, и ветка заводится обычным порядком.
	forkDoc(t, root, "XR-002", "- «доступ»: токен положен в secretctl?\n  - решает: человек\n"+
		"  - решено человеком 2026-09-06: положил, имя xr-token\n")
	if _, err := cmdStart(root, StartParams{ID: "XR-002", Slug: "wt"}); err != nil {
		t.Fatalf("после ответа старт должен проходить: %v", err)
	}
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-002-wt" {
		t.Fatalf("в дереве задачи стоит %q", br)
	}
}

// TestStartPassesExecutorFork: развилка, оставленная исполнителю, старта не
// держит (LLD DK-552, решение 2): исполнитель идёт по рекомендации. Файла
// задачи может не быть вовсе, и это тоже старт без вопросов.
func TestStartPassesExecutorFork(t *testing.T) {
	root, _ := setup(t, "", "")
	forkDoc(t, root, "XR-002", "- «поле»: как звать поле?\n  - решает: исполнитель\n  - рекомендация: «решает»\n")
	if _, err := cmdStart(root, StartParams{ID: "XR-002"}); err != nil {
		t.Fatalf("развилка исполнителя не должна держать старт: %v", err)
	}

	root, _ = setup(t, "", "")
	if err := os.Remove(filepath.Join(root, "docs", "tasks", "XR-002.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := cmdStart(root, StartParams{ID: "XR-002"}); err != nil {
		t.Fatalf("строка без файла задачи должна стартовать: %v", err)
	}
}

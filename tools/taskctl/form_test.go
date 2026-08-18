package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddPutsFormSkeleton: болванка task и bug несёт заголовки формы, а раздел
// «Приёмка» от неагентского вида встаёт на своё место по форме, а не в хвост
// файла за «Ходом работы».
func TestAddPutsFormSkeleton(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{Title: "Форменная", Type: "task", Rank: "0+1+1+0+1", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# XR-008: Форменная\n\n## Что происходит\n\n## Чего хотим\n\n## DoD\n\n## Ранг\n\n## Ход работы\n"
	if string(data) != want {
		t.Fatalf("болванка не по форме:\n%s", data)
	}
	if _, err := cmdAdd(root, AddParams{Title: "С барьером", Type: "bug", Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-009.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "## Приёмка\n\n- вид: user\n- барьер «глаза»:\n") {
		t.Fatalf("раздел «Приёмка» не дописан:\n%s", body)
	}
	if strings.Index(body, acceptanceHeading) > strings.Index(body, stageSection) {
		t.Fatalf("«Приёмка» встала после «Хода работы»:\n%s", body)
	}
}

// TestLintFindsFormOrder: переставленные разделы файла задачи это находка lint,
// а заголовок внутри блока кода разметкой файла не считается.
func TestLintFindsFormOrder(t *testing.T) {
	root := setup(t)
	path := filepath.Join(root, "docs", "tasks", "XR-001.md")
	body := "# XR-001: Средняя\n\n## Выкат\n\n- слито: abc1234\n\n## DoD\n\nСделано.\n" + fixtureScenario
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	hit := ""
	for _, f := range finds {
		if strings.Contains(f, "XR-001.md") && strings.Contains(f, "порядок разделов") {
			hit = f
		}
	}
	if hit == "" {
		t.Fatalf("порядок разделов не проверен:\n%s", strings.Join(finds, "\n"))
	}
	if !strings.Contains(hit, formDoc) {
		t.Errorf("находка не называет страницу формы: %s", hit)
	}
	quoted := "# XR-001: Средняя\n\n## DoD\n\nСделано.\n\n```\n## Выкат\n## DoD\n```\n" + fixtureScenario
	if err := os.WriteFile(path, []byte(quoted), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "XR-001.md") && strings.Contains(f, "порядок разделов") {
			t.Fatalf("заголовок из блока кода принят за раздел: %s", f)
		}
	}
}

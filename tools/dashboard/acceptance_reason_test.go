package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptanceReasonPreservesQuotedSection(t *testing.T) {
	for _, example := range []string{"```md\n## Приёмка\n- барьер «доступ»:\n```\n", "<!--\n## Приёмка\n- барьер «доступ»:\n-->\n"} {
		t.Run(example[:3], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "docs", "tasks", "XR-001.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			original := "# XR-001\n\n## Что происходит\n\n" + example + "\n## Приёмка\n\n- барьер «доступ»:\n\n## Ход работы\n"
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := writeAcceptReason(root, "XR-001", "Ожидается доступ"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, example) {
				t.Fatal("пример изменён: " + text)
			}
			if !strings.Contains(text, "\n## Приёмка\n\n- барьер «доступ»: Ожидается доступ\n") {
				t.Fatal("причина не записана в настоящий раздел: " + text)
			}
		})
	}
}

func TestAcceptanceReasonStaysInsideSection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-001.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "# XR-001\n\n## Приёмка\n\n- вид: mixed\n\n## Ход работы\n\n- барьер «доступ»:\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAcceptReason(root, "XR-001", "Ожидается доступ"); err == nil {
		t.Fatal("причина записана за пределами приёмки")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatal("файл изменён при отсутствии барьера")
	}
}

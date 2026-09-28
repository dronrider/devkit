package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planKit собирает синтетический devkit и проект с доской: команда ищет набор
// от корня проекта, и настоящее дерево тут не участвует.
func planKit(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	kit := filepath.Join(home, "devkit", "kit", "plans")
	root := filepath.Join(home, "app")
	for _, d := range []string{kit, filepath.Join(root, "docs"), filepath.Join(root, ".devkit", "plans")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	planWriteFile(t, filepath.Join(root, "docs", "TASKS.md"), "# Задачи\n")
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача доски\"\ntypes = [\"task\"]\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"stage:разработка\"\nslot = true\n[tests]\ntitle = \"тесты\"\nby = \"сам\"\ntrace = \"tests\"\ngate = [\"merge\"]\n")
	t.Setenv("DEVKIT_HOME", filepath.Join(home, "devkit"))
	return root, kit
}

func planWriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPlanTemplatesList: список называет слой и число этапов, а проектный файл
// приходит в набор рядом со встроенным.
func TestPlanTemplatesList(t *testing.T) {
	root, _ := planKit(t)
	planWriteFile(t, filepath.Join(root, ".devkit", "plans", "security.toml"),
		"title = \"Разбор безопасности\"\n[audit]\ntitle = \"аудит\"\nby = \"субагент\"\ntrace = \"слово\"\n")
	got, err := cmdPlanTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"шаблонов плана 2", "task", "слой встроенный, этапов 2",
		"security", "слой проектный", "--dump"} {
		if !strings.Contains(got, want) {
			t.Fatalf("в списке нет %q:\n%s", want, got)
		}
	}
}

// TestPlanTemplatesShowsStaleCopy: отставшую копию видит не только доктор.
// Команда, которой набор и смотрят, называет пропавший этап тут же.
func TestPlanTemplatesShowsStaleCopy(t *testing.T) {
	root, _ := planKit(t)
	planWriteFile(t, filepath.Join(root, ".devkit", "plans", "task.toml"),
		"title = \"Копия\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	got, err := cmdPlanTemplates(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "находка:") || !strings.Contains(got, "[tests]") {
		t.Fatalf("копия без этапа tests прошла молча:\n%s", got)
	}
}

// TestPlanTemplatesDump: дамп отдаёт файл как есть, комментариями и порядком. Им
// организация заводит свою копию, и перепечатанный разбор потерял бы причины,
// записанные в комментариях.
func TestPlanTemplatesDump(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "# причина порядка\nname = \"task\"\ntitle = \"Задача доски\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	got, err := cmdPlanTemplates(root, "task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "# причина порядка\n") {
		t.Fatalf("дамп потерял комментарий:\n%s", got)
	}
	if _, err := cmdPlanTemplates(root, "нетакого"); err == nil {
		t.Fatal("дамп неизвестного шаблона прошёл молча")
	}
}

// TestPlanShowTemplate: этапы печатаются по порядку, со следом, воротами и
// слотом своих пунктов. По этой печати агент понимает, что ему делать самому, а
// что за него сделает субагент.
func TestPlanShowTemplate(t *testing.T) {
	root, _ := planKit(t)
	got, err := planTemplateShow(root, "task")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) < 4 {
		t.Fatalf("печать короче четырёх строк:\n%s", got)
	}
	if !strings.Contains(lines[1], "этапов 2") {
		t.Fatalf("шапка не называет число этапов: %q", lines[1])
	}
	if !strings.Contains(lines[2], "разработка") || !strings.Contains(lines[2], "слот своих пунктов") {
		t.Fatalf("первый этап напечатан как %q", lines[2])
	}
	if !strings.Contains(lines[3], "ворота merge") || !strings.Contains(lines[3], "след tests") {
		t.Fatalf("второй этап напечатан как %q", lines[3])
	}
	if _, err := planTemplateShow(root, "нетакого"); err == nil {
		t.Fatal("печать неизвестного шаблона прошла молча")
	}
}

// TestPlanShowTemplateWordTrace: этап без следа помечен честной надписью, а не
// выглядит проверяемым.
func TestPlanShowTemplateWordTrace(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "poc.toml"), "title = \"Режим POC\"\ndropped = [\"tests: результат неизвестен\"]\n[show]\ntitle = \"показ\"\nby = \"человек\"\ntrace = \"слово\"\n")
	got, err := planTemplateShow(root, "poc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "на слове человека") {
		t.Fatalf("этап без следа напечатан без пометки:\n%s", got)
	}
	if !strings.Contains(got, "снят этап tests: результат неизвестен") {
		t.Fatalf("снятый этап напечатан без причины:\n%s", got)
	}
}

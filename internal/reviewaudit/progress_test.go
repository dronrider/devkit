package reviewaudit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameProgressAdvancesByFileWithoutSummary(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "a.md"), "Описание изменения.\n")
	write(t, filepath.Join(root, "code.go"), "package sample\nfunc fresh() {}\n")
	report := completed(t, root, path)
	rule := findTargetRule(t, &report, namingRule)
	target := rule.Targets[0]
	rule.Objects = []string{target.Object}
	rule.Result = "pending"
	rule.Evidence = nil

	check := func(want ...string) string {
		t.Helper()
		save(t, path, report)
		state, err := NameProgress(root, path, "XR-1", "", "")
		if err != nil {
			t.Fatal(err)
		}
		got := state.String()
		for _, detail := range want {
			if !strings.Contains(got, detail) {
				t.Errorf("нет %q в выводе\n%s", detail, got)
			}
		}
		return got
	}
	got := check("закрыто 0/1 | незаполнено 1 | отклонено 0 | обоснованно открыто 0", "0/2", "Следующий файл: a.md")
	if strings.Contains(got, target.Object) {
		t.Errorf("цель из следующего файла попала в вывод: %s", got)
	}
	progress, err := NameProgress(root, path, "XR-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.OpenFiles) != 2 || progress.OpenFiles[0].Path != "a.md" ||
		progress.OpenFiles[1].Path != "code.go" || len(progress.OpenFiles[1].Goals) != 1 {
		t.Fatalf("очередь не сохранила файлы с пробелами в порядке снимка: %+v", progress.OpenFiles)
	}
	rule.Files[0].Reason = "Обновлено описание."
	check("1/2", "Следующий файл: code.go", target.Object+" -> заполните meaning, result, writes")
	rule.Evidence = []Evidence{
		{Object: target.Object, Check: "meaning", Result: "checked", Symbol: target.Symbol, Location: target.Location,
			Observation: "Имя совпадает с назначением."}, // Missing word_checks must be rejected before Markdown.
		{Object: target.Object, Check: "result", Result: "not_applicable", Location: target.Location, Observation: "Нет возврата."},
		{Object: target.Object, Check: "writes", Result: "not_applicable", Location: target.Location, Observation: "Нет записи."},
	}
	rule.Result = "checked"
	check("незаполнено 0 | отклонено 1", "пропущены слова fresh", "Следующий файл: code.go")
	rule.Evidence[0].WordChecks = []WordCheck{{Word: "fresh", Meaning: "начинает", Fact: "объявлена операция", Location: target.Location, Result: "checked"}}
	check("закрыто 1/1 | незаполнено 0 | отклонено 0 | обоснованно открыто 0", "2/2", "соберите итог")

	rule.Result, rule.Evidence[0].Result, rule.Evidence[0].Kind = "pending", "pending", "contract"
	rule.Evidence[0].Facts = map[string][]ReviewFact{"question": {{Text: "Какое назначение задано в контракте?", Location: target.Location}}}
	check("закрыто 0/1 | незаполнено 0 | отклонено 0 | обоснованно открыто 1", "2/2", "соберите итог")

	write(t, filepath.Join(root, "code.go"), "package sample\nfunc changed() {}\n")
	_, err = NameProgress(root, path, "XR-1", "", "")
	assertTargetCheck(t, err, "код или постановка изменились")
}

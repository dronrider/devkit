package main

import (
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

// taskScenarioDoc собирает файл задачи со сценарием и строкой уровня ревью, но
// без единого слова про обкатку: остальные ворота готовности он проходит.
func taskScenarioDoc(id string) string {
	return "# " + id + ": заголовок\n\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n" + fixtureReviewLevel
}

// rehearsalLine собирает отметку обкатки под сценарий файла и коммит, на
// котором прогон шёл: ворота сверяют и отпечаток сценария, и коммит.
func rehearsalLine(doc, sha string) string {
	return "\n## Проверка\n\n" + taskform.RehearsalNote +
		" 2026-09-24 10:00, свежее дерево " + sha + ", сценарий " + taskform.ScenarioPrint(doc) +
		", временный HOME, утилит дерева 3, шагов 1, все зелёные.\n"
}

// TestMergeNeedsRehearsal: регрессия DK-685. Ворот обкатки стоял только на
// переводе в Check, а тот идёт уже после слияния: ветка уезжала в main, move
// отбивался, и задача оставалась в In progress при слитом коде. Ворот стоит в
// готовности правки, до слияния, и main отказ не трогает.
func TestMergeNeedsRehearsal(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	write(t, root, "docs/tasks/XR-003.md", taskScenarioDoc("XR-003"))
	gitT(t, root, "add", "docs/tasks/XR-003.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 файл задачи")
	before := gitT(t, root, "rev-parse", "main")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	_, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err == nil {
		t.Fatal("слияние необкатанной агентской задачи должно падать")
	}
	if !strings.Contains(err.Error(), "taskctl rehearse XR-003") {
		t.Fatalf("отказ не называет команду обкатки: %v", err)
	}
	if after := gitT(t, root, "rev-parse", "main"); after != before {
		t.Fatalf("отказ ворот тронул main: было %s, стало %s", before, after)
	}
}

// TestMergePassesAfterRehearsal: отметка обкатки под вершиной ветки ворот
// открывает, и задача сливается целиком.
func TestMergePassesAfterRehearsal(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	head := strings.TrimSpace(gitT(t, root, "rev-parse", "--short=12", "HEAD"))
	doc := taskScenarioDoc("XR-003")
	write(t, root, "docs/tasks/XR-003.md", doc+rehearsalLine(doc, head))
	gitT(t, root, "add", "docs/tasks/XR-003.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 запись обкатки")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatalf("обкатанная задача должна сливаться: %v", err)
	}
}

// TestMergeSkipsRehearsalForUserKind: у пользовательского вида часть шагов
// держит человек, машинной отметки с него не спрашивают, и слияние проходит.
func TestMergeSkipsRehearsalForUserKind(t *testing.T) {
	root, _ := setup(t, "| XR-003 | Вторая мелочь [приёмка: user] | task | P3 | 8 (0+3+0+5+0) |  |\n", "")
	write(t, root, "docs/tasks/XR-003.md", taskScenarioDoc("XR-003"))
	gitT(t, root, "add", "docs/tasks/XR-003.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 файл задачи")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatalf("пользовательский вид обкатки не требует: %v", err)
	}
}

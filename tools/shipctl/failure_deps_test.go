package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func failureRow(id, suffix string) string {
	return fmt.Sprintf("| %s | Работа %s %s | task | P2 | 30 (25+5+0+0+0) | [tasks/%s.md](tasks/%s.md) |\n", id, id, suffix, id, id)
}

func TestFailureDependenciesMerge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, suffix, others string
		paths                []string
	}{
		{"direct", "[после XR-003]", "", []string{"XR-001 -> XR-003"}},
		{"transitive", "[после XR-004]", failureRow("XR-004", "[после XR-003]"), []string{"XR-001 -> XR-004 -> XR-003"}},
		{"multiple-and-cycle", "[после XR-004, XR-005]", failureRow("XR-004", "[после XR-001, XR-003]") + failureRow("XR-005", "[провал: второй сбой] [после XR-003]"), []string{"XR-001 -> XR-004 -> XR-003", "XR-001 -> XR-005"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, calls := setup(t, failureRow("XR-001", tc.suffix)+tc.others+rowFailed, "")
			branchWithFix(t, root)
			before := gitT(t, root, "rev-parse", "main")
			boardBefore, _ := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
			_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
			if err == nil {
				t.Fatal("зависимая задача слилась")
			}
			for _, path := range tc.paths {
				if !strings.Contains(err.Error(), path) {
					t.Fatalf("нет цепочки %s: %v", path, err)
				}
			}
			if !strings.Contains(err.Error(), "провал проверки за XR-003") || !strings.Contains(err.Error(), "shipctl revert XR-003") {
				t.Fatalf("нет причины и починки: %v", err)
			}
			boardAfter, _ := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
			if string(boardAfter) != string(boardBefore) || gitT(t, root, "rev-parse", "main") != before {
				t.Fatal("отказ изменил доску или main")
			}
			if data, _ := os.ReadFile(calls); len(data) != 0 {
				t.Fatalf("отказ вызвал taskctl: %s", data)
			}
		})
	}
}

func TestFailureIndependentTrainAndOwnFix(t *testing.T) {
	t.Parallel()
	for _, own := range []bool{false, true} {
		t.Run(fmt.Sprintf("own-%v", own), func(t *testing.T) {
			first := rowInProg
			if own {
				first = rowSelfFailed
			}
			root, calls := setup(t, first+rowInProgLow+rowFailed+failureRow("XR-005", "[провал: второй сбой]"), "")
			taskWithScenario(t, root, "XR-007")
			branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
			if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
				t.Fatal(err)
			}
			b, _ := loadBoard(root)
			if failedOf(b, "XR-003") == "" || (own && failedOf(b, "XR-001") == "") {
				t.Fatal("слияние очистило пометку")
			}
			branchFor(t, root, "XR-007", "xr-007-fix", "b.txt")
			if _, err := cmdMerge(root, MergeParams{ID: "XR-007", Test: "true", Train: true}); err != nil {
				t.Fatal(err)
			}
			msg, err := cmdShip(root, ShipParams{Deploy: "true", Drain: true})
			if err != nil || !strings.Contains(msg, "поезд выкачен") {
				t.Fatalf("независимый поезд не выкачен: %q, %v", msg, err)
			}
			data, _ := os.ReadFile(calls)
			for _, id := range []string{"XR-001", "XR-007"} {
				if !strings.Contains(string(data), "move "+id+" check") {
					t.Fatalf("не переведена %s: %s", id, data)
				}
			}
			b, _ = loadBoard(root)
			if failedOf(b, "XR-003") == "" || failedOf(b, "XR-005") == "" {
				t.Fatal("потерян чужой провал")
			}
		})
	}
}

func TestFailureTrainBecomesDependent(t *testing.T) {
	t.Parallel()
	for _, drain := range []bool{false, true} {
		t.Run(fmt.Sprint(drain), func(t *testing.T) {
			root, calls := setup(t, rowInProg+rowInProg3+failureRow("XR-004", "[после XR-003]"), "")
			branchWithFix(t, root)
			if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
				t.Fatal(err)
			}
			boardData, _ := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
			data := strings.Replace(string(boardData), "| Починка бага |", "| Починка бага [после XR-004] |", 1)
			data = strings.Replace(data, "| Вторая мелочь |", "| Вторая мелочь [провал: сбой] |", 1)
			write(t, root, "docs/TASKS.md", data)
			gitT(t, root, "add", "docs/TASKS.md")
			gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 провал")
			before := gitT(t, root, "rev-parse", "deployed")
			callsBefore, _ := os.ReadFile(calls)
			msg, err := cmdShip(root, ShipParams{Deploy: "touch unexpected-deploy", Drain: drain})
			if (!drain && err == nil) || (drain && err != nil) {
				t.Fatalf("исход ship: %q,%v", msg, err)
			}
			said := msg
			if err != nil {
				said = err.Error()
			}
			if !strings.Contains(said, "XR-001 -> XR-004 -> XR-003") {
				t.Fatalf("нет пути: %s", said)
			}
			b, _ := loadBoard(root)
			train, _, terr := trainTasks(root, "main", b)
			callsAfter, _ := os.ReadFile(calls)
			afterBoard, _ := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
			if terr != nil || len(train) != 1 || train[0] != "XR-001" || gitT(t, root, "rev-parse", "deployed") != before || string(callsBefore) != string(callsAfter) || string(afterBoard) != data {
				t.Fatal("отказ потерял состав, пометку или изменил состояние")
			}
			if _, err := os.Stat(filepath.Join(root, "unexpected-deploy")); !os.IsNotExist(err) {
				t.Fatal("выкат запустился")
			}
			st, err := cmdStatus(root)
			if err != nil || !strings.Contains(st, "XR-001 заблокирована") || !strings.Contains(st, "XR-001 -> XR-004 -> XR-003") {
				t.Fatalf("status не показывает зависимость: %s,%v", st, err)
			}
		})
	}
}

func TestFailureQueueSkipsDependent(t *testing.T) {
	t.Parallel()
	root, _ := setup(t, failureRow("XR-001", "[после XR-004]")+failureRow("XR-004", "[после XR-003]")+rowInProgLow+rowFailed, "")
	devkitDir(t, root)
	taskWithScenario(t, root, "XR-007")
	queueBranch(t, root, "XR-001", "xr-001-fix", "code.txt")
	queueBranch(t, root, "XR-007", "xr-007-fix", "small.txt")
	items := queueJSONItems(t, root, QueueParams{Drain: true, Test: "true"})
	if len(items) != 2 || items[0].ID != "XR-001" || !items[0].Waiting || items[0].Failed || items[1].ID != "XR-007" || !items[1].Merged {
		t.Fatalf("очередь не прошла зависимую строку: %+v", items)
	}
	if !strings.Contains(items[0].Line, "XR-001 -> XR-004 -> XR-003") {
		t.Fatalf("нет пути в очереди: %+v", items[0])
	}
}

func TestFailureOwnCycleAndClearing(t *testing.T) {
	t.Parallel()
	root, _ := setup(t, strings.Replace(rowSelfFailed, "[провал:", "[после XR-004] [провал:", 1)+failureRow("XR-004", "[после XR-001]"), "")
	taskWithScenario(t, root, "XR-004")
	codeCommit(t, root, "XR-004", "prior.txt")
	gitT(t, root, "tag", "deployed")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatalf("свой провал через цикл не должен мешать починке: %v", err)
	}
	b, _ := loadBoard(root)
	if failedOf(b, "XR-001") == "" {
		t.Fatal("поезд погасил пометку")
	}
	branchFor(t, root, "XR-004", "xr-004-fix", "follow.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-004", Test: "true", Train: true}); err == nil || !strings.Contains(err.Error(), "XR-004 -> XR-001") {
		t.Fatalf("слияние починки преждевременно сняло блокировку: %v", err)
	}
	gitT(t, root, "checkout", "main")
	// Стенд taskctl только регистрирует вызов. Воспроизводим снятие пометки при Check.
	boardData, _ := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
	write(t, root, "docs/TASKS.md", strings.Replace(string(boardData), " [провал: прод отдаёт 500]", "", 1))
	gitT(t, root, "add", "docs/TASKS.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 провал погашен")
	gitT(t, root, "checkout", "xr-004-fix")
	gitT(t, root, "rebase", "main")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-004", Test: "true", Train: true}); err != nil {
		t.Fatalf("после снятия пометки зависимая задача должна сливаться: %v", err)
	}
}

func TestFailureDrainStopsSameArtifact(t *testing.T) {
	for _, retry := range []string{"manual", "new-independent"} {
		t.Run(retry, func(t *testing.T) {
			root, calls := setup(t, rowInProg+rowInProgLow, "")
			stub := filepath.Join(filepath.Dir(calls), "taskctl")
			body, err := os.ReadFile(stub)
			if err != nil {
				t.Fatal(err)
			}
			body = []byte(strings.Replace(string(body), "printf '<!-- move -->", "[ \"$6\" = --dry-run ] && exit 0\nprintf '<!-- move -->", 1))
			if err := os.WriteFile(stub, body, 0o755); err != nil {
				t.Fatal(err)
			}
			taskWithScenario(t, root, "XR-007")
			branchWithFix(t, root)
			if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
				t.Fatal(err)
			}
			side := t.TempDir()
			runs := filepath.Join(side, "runs")
			permit := filepath.Join(side, "permit")
			deploy := fmt.Sprintf("printf 'run\\n' >> %q; test -f %q", runs, permit)
			if _, err := cmdShip(root, ShipParams{Deploy: deploy, Drain: true}); err == nil {
				t.Fatal("ожидался провал выката")
			}
			// taskctl-стенд регистрирует fail; фиксируем пометку коммитом как настоящая команда.
			data, _ := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
			write(t, root, "docs/TASKS.md", strings.Replace(string(data), "| Починка бага |", "| Починка бага [провал: выкат упал] |", 1))
			gitT(t, root, "add", "docs/TASKS.md")
			gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 провал проверки")
			for range 2 {
				msg, err := cmdShip(root, ShipParams{Deploy: deploy, Drain: true})
				if err != nil || !strings.Contains(msg, "того же артефакта") || !strings.Contains(msg, "shipctl ship") {
					t.Fatalf("повтор не остановлен с подсказкой: %s,%v", msg, err)
				}
			}
			count := func(want int) {
				t.Helper()
				data, err := os.ReadFile(runs)
				if err != nil || strings.Count(string(data), "run\n") != want {
					t.Fatalf("ожидалось запусков %d: %s,%v", want, data, err)
				}
			}
			count(1)
			write(t, side, "permit", "")
			drain := false
			if retry == "new-independent" {
				branchFor(t, root, "XR-007", "xr-007-fix", "independent.txt")
				if _, err := cmdMerge(root, MergeParams{ID: "XR-007", Test: "true", Train: true}); err != nil {
					t.Fatal(err)
				}
				drain = true
			}
			msg, err := cmdShip(root, ShipParams{Deploy: deploy, Drain: drain})
			if err != nil || !strings.Contains(msg, "поезд выкачен") {
				t.Fatalf("повтор или новый артефакт не прошёл: %s,%v", msg, err)
			}
			count(2)
			b, _ := loadBoard(root)
			train, _, err := trainTasks(root, "main", b)
			if err != nil || len(train) != 0 {
				t.Fatalf("после успешного повтора поезд не пуст: %v,%v", train, err)
			}
		})
	}
}

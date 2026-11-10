package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

func TestQueueResumesOnFreshRegcheckWithoutNewCommit(t *testing.T) {
	root := queueSetup(t)
	wt, err := taskWorktree(root, "XR-001")
	if err != nil || wt == nil {
		t.Fatalf("дерево задачи: %v, %v", wt, err)
	}
	head := gitT(t, wt.Path, "rev-parse", "HEAD")
	base := gitT(t, root, "merge-base", "main", head)
	logPath := ".devkit/log"
	stale := "2000-01-01T00:00:00\tregcheck\trun\t0\n"
	write(t, wt.Path, logPath, stale)
	runs := filepath.Join(t.TempDir(), "runs")
	p := QueueParams{Drain: true, Test: fmt.Sprintf("printf 'run\\n' >> %q", runs)}

	type reportItem struct {
		ID      string
		Line    string
		Failed  bool
		Waiting bool
		Merged  bool
		Changed bool
		Block   struct {
			Code, Reason, Owner, Resume, Head, Base, Log string
			Since                                        time.Time
		}
	}
	tick := func() []reportItem {
		t.Helper()
		raw, err := cmdQueueJSON(root, p)
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Items []reportItem }
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result.Items
	}
	countRuns := func(want int) {
		t.Helper()
		got, err := os.ReadFile(runs)
		if err != nil || strings.Count(string(got), "run\n") != want {
			t.Fatalf("прогонов должно быть %d: %q, %v", want, got, err)
		}
	}

	items := tick()
	if len(items) != 2 || items[0].ID != "XR-001" || !items[0].Waiting || items[0].Failed ||
		!items[0].Changed || items[0].Block.Code != "regcheck" ||
		items[1].ID != "XR-007" || !items[1].Merged {
		t.Fatalf("ожидание regcheck не должно останавливать готовую ветку: %+v", items)
	}
	block := items[0].Block
	if block.Head != head || block.Base != base || block.Log != filepath.Join(wt.Path, logPath) || block.Since.IsZero() ||
		!strings.Contains(block.Reason, "нет зелёного regcheck") ||
		!strings.Contains(block.Resume, "актуальной базе") || block.Owner != "исполнитель XR-001" {
		t.Fatalf("неполная диагностика ожидания: %+v", block)
	}
	status, err := cmdQueue(root, QueueParams{})
	if err != nil || !strings.Contains(status, "XR-001") ||
		!strings.Contains(status, "ожидает") || !strings.Contains(status, block.Reason) {
		t.Fatalf("ожидание должно быть видно в статусе: %q, %v", status, err)
	}
	countRuns(1)

	gitT(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	fresh := time.Now().Format("2006-01-02T15:04:05")
	write(t, wt.Path, logPath, stale+fresh+"\tregcheck\trun\t1\n")
	items = tick()
	if len(items) != 1 || !items[0].Waiting || items[0].Changed || items[0].Block != block {
		t.Fatalf("красная запись и недоступный origin не меняют ожидание: %+v", items)
	}
	if err := os.Remove(filepath.Join(wt.Path, logPath)); err != nil {
		t.Fatal(err)
	}
	items = tick()
	if len(items) != 1 || !items[0].Waiting || items[0].Changed || items[0].Block != block {
		t.Fatalf("исчезновение журнала не подтверждает regcheck: %+v", items)
	}
	write(t, wt.Path, "code.txt", "unfinished\n")
	items = tick()
	if len(items) != 1 || items[0].Block.Code != "preflight" ||
		!strings.Contains(items[0].Block.Reason, "незакоммиченное") {
		t.Fatalf("новый стопор должен быть виден в диагностике: %+v", items)
	}
	gitT(t, wt.Path, "restore", "--", "code.txt")
	items = tick()
	if len(items) != 1 || !items[0].Waiting || items[0].Block.Code != "regcheck" {
		t.Fatalf("устранение другого стопора не подтверждает regcheck: %+v", items)
	}
	countRuns(1)
	for _, s := range stagesOf(t, root, "XR-001") {
		if s.Kind == stage.Merge {
			t.Fatalf("ожидание regcheck записано как слияние: %+v", s)
		}
	}
	origin := t.TempDir()
	gitT(t, origin, "init", "--bare", "-q")
	gitT(t, root, "remote", "set-url", "origin", origin)
	gitT(t, root, "push", "-u", "origin", "main")
	p.Push = true
	write(t, wt.Path, logPath, stale+fresh+"\tregcheck\trun\t0\n")
	if got := gitT(t, wt.Path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("до возобновления SHA ветки должен остаться %s, получен %s", head, got)
	}

	items = tick()
	if len(items) != 1 || !items[0].Failed || !strings.Contains(items[0].Line, "отчёт ревью") {
		t.Fatalf("ребейз на слитую XR-007 требует нового ревью: %+v", items)
	}
	completeReviewAudit(t, wt.Path, "XR-001", true)
	items = tick()
	if len(items) != 1 || items[0].ID != "XR-001" || !items[0].Merged {
		t.Fatalf("зелёная запись должна вернуть ветку на следующем тике: %+v", items)
	}
	if items := tick(); len(items) != 0 {
		t.Fatalf("после слияния разлив не должен повторяться: %+v", items)
	}
	countRuns(2)
	if local, remote := gitT(t, root, "rev-parse", "main"), gitT(t, origin, "rev-parse", "main"); local != remote {
		t.Fatalf("слияние не опубликовано: main %s, origin %s", local, remote)
	}
	if mark := loadQueue(root).Tasks["XR-001"]; mark != nil {
		t.Fatalf("после слияния осталась запись ожидания: %+v", mark)
	}
}

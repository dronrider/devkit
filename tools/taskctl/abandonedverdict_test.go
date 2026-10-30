package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/peers"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

// Тесты вердикта `taskctl abandoned` зовут новые символы (cmdAbandoned,
// holdsRow), на старом коде не собираются и переносятся regcheck целиком:
// краснота исхода «взял и умер» доказана соседним abandoned_test.go, тут
// проверяется сам вердикт.

// liveStageView строит снимок с одной живой сессией, привязанной к строке по
// факту работы: PID самого теста жив, касание свежее, и реестр читается из
// карт снимка, не из дома.
func liveStageView(id string) *stageView {
	now := timeNow()
	sid := "test-session"
	return &stageView{
		main:   "",
		recs:   map[string]stage.Record{},
		files:  map[string]*stage.Record{},
		peers:  map[string]peers.Peer{sid: {PID: os.Getpid(), SessionID: sid, Updated: now.UnixMilli()}},
		binds:  map[string][]sessions.Bind{sid: {{Task: id, Source: sessions.BySrc}}},
		loaded: true,
		now:    now,
	}
}

// TestAbandonedReturnsHollowOldRow: вердикт отдаёт брошенную строку голым ID
// первой строкой, и это контракт тика сторожка.
func TestAbandonedReturnsHollowOldRow(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +5 дней

	out, err := cmdAbandoned(root)
	if err != nil {
		t.Fatal(err)
	}
	if out != "XR-005" {
		t.Fatalf("жду голый XR-005 единственной строкой вердикта:\n%s", out)
	}
}

// TestAbandonedRefusesHeldAndYoungRows: строка с деревом и строка моложе
// срока в возврат не идут, а причина видна под «отказано:».
func TestAbandonedRefusesHeldAndYoungRows(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	gitOut(t, root, "branch", "xr-005")
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +1 день

	out, err := cmdAbandoned(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(out, "XR-005") {
		t.Fatalf("строка с деревом ушла в возврат:\n%s", out)
	}
	if !strings.Contains(out, "XR-005: дерево задачи") {
		t.Fatalf("причина удержания деревом не названа:\n%s", out)
	}
}

// TestAbandonedHoldsMergedRow: слитая в main работа держит строку, пока выкат
// не заберёт её из In progress.
func TestAbandonedHoldsMergedRow(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitCommitDated(t, root, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC), "XR-005 работа слита")
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +5 дней

	out, err := cmdAbandoned(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-005: слита в main, ждёт выката") {
		t.Fatalf("слитая строка не удержана:\n%s", out)
	}
	if strings.HasPrefix(out, "XR-005") {
		t.Fatalf("слитая строка ушла в возврат:\n%s", out)
	}
}

// TestAbandonedHoldsLiveSession: живая сессия из реестра чатов держит строку
// без ветки и без слитости.
func TestAbandonedHoldsLiveSession(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +5 дней

	sv := liveStageView("XR-005")
	b := mustBoard(t, root)
	if got := holdsRow(root, b.find("XR-005"), sv); got != "сессия жива" {
		t.Fatalf("живая сессия не держит строку: %q", got)
	}
	if rows := abandoned(root, b, boardTimes(root), sv); len(rows) != 1 || rows[0].Reason != "сессия жива" {
		t.Fatalf("вердикт по строке с живой сессией: %+v", rows)
	}
}

// TestAbandonedSkipsGoalRow: цель в In progress не отдается возврату, её
// держит надзор реестра целей.
func TestAbandonedSkipsGoalRow(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	b := mustBoard(t, root)
	row := b.find("XR-005")
	row.Title = strings.Replace(row.Title, "Задача в работе", "Цель: разобраться", 1)
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +5 дней

	out, err := cmdAbandoned(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "XR-005") {
		t.Fatalf("цель попала в возврат:\n%s", out)
	}
}

// mustBoard читает доску для прямых вызовов вердикта.
func mustBoard(t *testing.T, root string) *Board {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// Тесты сужения вердикта командой машины (DK-1468): назначение строки суффиксом
// «[исп: имя]» ведётся участником из ~/.devkit/team.toml, и агентский участник
// заменяет ярус вердикта своим. Строка T-007 выбрана нарочно: её нет в снимке
// вердиктов, и сужение не двигает чужие ожидания.

// teamBoard это доска с назначением на T-007: строка пишется поверх фикстуры,
// чтобы не плодить вторую копию sampleBoard.
func teamBoard(t *testing.T, handle string) string {
	t.Helper()
	root := writeBoard(t)
	path := filepath.Join(root, "docs", "TASKS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	suf := ""
	if handle != "" {
		suf = " [исп: " + handle + "]"
	}
	fixed := strings.Replace(string(data), "| T-007 | задача ценой M |",
		"| T-007 | задача ценой M"+suf+" |", 1)
	if fixed == string(data) && suf != "" {
		t.Fatal("строка T-007 не найдена в фикстуре")
	}
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// squadSave кладёт команду из двух участников: агент mini и человек.
func squadSave(t *testing.T, home string) {
	t.Helper()
	if err := team.Save(home, []team.Participant{
		{Handle: "mini4", Kind: team.Agent, Model: "mini-4", Tier: tierMini, Roles: []string{team.RoleExec, team.RoleReview}},
		{Handle: "pro9", Kind: team.Agent, Model: "pro-9", Tier: tierPro, Roles: []string{team.RoleExec}},
		{Handle: "rider", Kind: team.Human, Roles: []string{team.RoleExec}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPickAssignNarrowsTier(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "mini4")
	squadSave(t, stage.Home())
	out, err := cmdPick(root, "T-007", roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.Contains(out, "tier: mini") {
		t.Fatalf("назначение не сузило ярус:\n%s", out)
	}
	if !strings.Contains(out, "строка назначена участнику команды mini4, ярус mini") {
		t.Fatalf("причина не называет назначение:\n%s", out)
	}
	if !strings.Contains(out, "не смотрели, ярус задан назначением строки") {
		t.Fatalf("корректор должен молчать при фиксированном участнике:\n%s", out)
	}
}

func TestPickAssignBeatsOverride(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "mini4")
	squadSave(t, stage.Home())
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "T-007.md"),
		[]byte("# T-007\n\n- Модель: pro (по решению человека)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdPick(root, "T-007", roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.Contains(out, "tier: mini") {
		t.Fatalf("override перебил назначение:\n%s", out)
	}
	if !strings.Contains(out, "перебивает override-строку файла задачи") {
		t.Fatalf("причина не называет победу над override:\n%s", out)
	}
}

func TestPickAssignHumanKeepsVerdict(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "rider")
	squadSave(t, stage.Home())
	out, err := cmdPick(root, "T-007", roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	// T-007 это task ценой M с неопределённостью 1: вердикт base как был.
	if !strings.Contains(out, "tier: base") {
		t.Fatalf("человеческое назначение двинуло ярус:\n%s", out)
	}
	if !strings.Contains(out, "строка назначена участнику-человеку rider, вердикт для подсобных подъёмов") {
		t.Fatalf("причина не помечает человеческое назначение:\n%s", out)
	}
}

func TestPickAssignUnknownWarns(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "ghost")
	squadSave(t, stage.Home())
	out, err := cmdPick(root, "T-007", roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.Contains(out, "tier: base") {
		t.Fatalf("без участника вердикт не по метаданным:\n%s", out)
	}
	if !strings.Contains(out, "участнику ghost, которого нет в команде") {
		t.Fatalf("предупреждение об уехавшем участнике не пришло:\n%s", out)
	}
}

func TestPickAssignKeepsGroomFloor(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	// T-004 это неопределённость 5: грумминговый вердикт pro, назначение
	// участника mini его не опускает.
	root := teamBoard(t, "mini4")
	path := filepath.Join(root, "docs", "TASKS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixed := strings.Replace(string(data), "| T-004 | неразобранная задача |",
		"| T-004 | неразобранная задача [исп: mini4] |", 1)
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	squadSave(t, stage.Home())
	out, err := cmdPick(root, "T-004", roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.Contains(out, "tier: pro") {
		t.Fatalf("назначение опустило грумминговый вердикт:\n%s", out)
	}
	if !strings.Contains(out, "грумминговый вердикт ниже pro не опускается") {
		t.Fatalf("причина не назвала пол грумминга:\n%s", out)
	}
}

func TestPickAssignReviewShift(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "mini4")
	squadSave(t, stage.Home())
	out, err := cmdPick(root, "T-007", roleReview, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	// Роль ревью идёт от назначенного яруса с обычным полом: mini -> base.
	if !strings.Contains(out, "tier: base") {
		t.Fatalf("пол ревью не поднял mini:\n%s", out)
	}
	if !strings.Contains(out, "mini -> base") {
		t.Fatalf("причина не назвала подъём ревью:\n%s", out)
	}
}

func TestPickAssignGoalCapCuts(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "pro9")
	squadSave(t, stage.Home())
	goal := "docs/tasks/T-100.md"
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\nярус: base\n", ""))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
	out, err := cmdPick(root, "T-007", roleExec, goal)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	// Потолок цели режет и назначение: бюджетная рамка сильнее любых ручных
	// строк, порядок ярусной оси проговорён в причине.
	if !strings.Contains(out, "tier: base") {
		t.Fatalf("потолок цели не срезал назначение pro:\n%s", out)
	}
	if !strings.Contains(out, "потолок цели: base, pro -> base") {
		t.Fatalf("причина не назвала срез потолком:\n%s", out)
	}
}

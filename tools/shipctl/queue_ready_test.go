package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// queueMarkField читает поле наклейки задачи из файла очереди как есть, без
// типизированной структуры. Регчек возит этот файл на базу, где поля ready
// ещё нет, и краснота тестов обязана быть поведенческой, а не несборкой
// пакета от нового символа.
func queueMarkField(t *testing.T, root, id, field string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, mergeQueuePath))
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Tasks map[string]map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	v, _ := st.Tasks[id][field].(string)
	return v
}

// taskTree даёт путь дерева задачи: queueBranch выкладывает ветку туда же.
func taskTree(root, id string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+strings.ToLower(id))
}

// logGreen кладёт в журнал запусков зелёный прогон regcheck текущим временем,
// тем же форматом, каким его пишет сам regcheck. Свежесть готовности меряется
// по секундной метке журнала, поэтому пишется она после коммитов ветки, а
// стенды спят по секунде между шагами.
func logGreen(t *testing.T, logRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(logRoot, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(logRoot, ".devkit", "log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\tregcheck\trun\t0\tok\n", time.Now().Format("2006-01-02T15:04:05"))
}

// TestQueueDrainWaitsForReadiness: доработка после зелёного regcheck гасит
// готовность, и разлив не заказывает слияние. Каждая попытка по живой строке
// оставляла записи слияния в файле задачи до конца разработки (DK-1650, у
// DK-1072 их легло пятнадцать), поэтому неготовая строка не получает попытку
// вовсе, а причина ожидания названа в отчёте тика.
func TestQueueDrainWaitsForReadiness(t *testing.T) {
	root := queueSetup(t)
	time.Sleep(1100 * time.Millisecond)
	logGreen(t, taskTree(root, "XR-001"))
	time.Sleep(1100 * time.Millisecond)
	wt := taskTree(root, "XR-001")
	write(t, wt, "code.txt", "доработка\n")
	gitT(t, wt, "add", "code.txt")
	gitT(t, wt, "commit", "-qm", "fix: XR-001 доработка после regcheck")
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "XR-001 слита") {
		t.Fatalf("неготовая строка получила слияние: %q", msg)
	}
	if !strings.Contains(msg, "XR-001: ждёт готовности: после последнего regcheck была доработка") {
		t.Fatalf("разлив обязан назвать причину ожидания: %q", msg)
	}
	log := gitT(t, root, "log", "--format=%s")
	if strings.Contains(log, "доработка после regcheck") {
		t.Errorf("доработка неготовой строки уехала в main:\n%s", log)
	}
	m := loadQueue(root).Tasks["XR-001"]
	if m == nil || m.Block == nil || m.Block.Code != "readiness" {
		t.Fatalf("неготовая строка стоит блоком готовности: %+v", m)
	}
	if ready := queueMarkField(t, root, "XR-001", "ready"); ready != "" {
		t.Errorf("доработка после regcheck не подтверждает отпечаток: %q", ready)
	}
}

// TestQueueDrainPoursReadyRow: зелёный regcheck новее кода и замечаний без
// исхода нет, готовая строка заказывает слияние и льётся.
func TestQueueDrainPoursReadyRow(t *testing.T) {
	root := queueSetup(t)
	time.Sleep(1100 * time.Millisecond)
	logGreen(t, taskTree(root, "XR-001"))
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001 слита очередью") {
		t.Fatalf("готовая строка обязана пролиться: %q", msg)
	}
	if strings.Contains(msg, "XR-007 слита") {
		t.Errorf("за один заход льётся одна ветка: %q", msg)
	}
}

// TestQueueReadinessSurvivesOwnRebase: ребейз, сделанный самой очередью,
// перезастаивает коммиты ветки, и готовность судится отпечатком диффа, а не
// линией коммитов. Окно такое: разлив двинул main досовским коммитом (так он
// двигает его, сливая чужие задачи), regcheck прогнан уже по новому main, но
// до ребейза нашей ветки, и после ребейза линия коммитов новее регчека, а
// дифф задачи не менялся вовсе: отбитая нагрузочной краснотой ветка не ждёт
// нового regcheck.
func TestQueueReadinessSurvivesOwnRebase(t *testing.T) {
	root := queueSetup(t)
	board, err := os.ReadFile(filepath.Join(root, "docs/TASKS.md"))
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(board), "| XR-002 | Задел |",
		"| XR-003 | Ещё задел | task | P3 | 10 (0+5+0+0+5) |  |\n| XR-002 | Задел |", 1)
	write(t, root, "docs/TASKS.md", moved)
	gitT(t, root, "add", "docs/TASKS.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 новая строка")
	time.Sleep(1100 * time.Millisecond)
	logGreen(t, taskTree(root, "XR-001"))
	// Первый заход: готовность подтверждена по времени, слияние отбито
	// нагрузочной краснотой вне диффа задачи, очередь перебазировала ветку.
	if _, err := cmdQueue(root, QueueParams{Drain: true, Test: loadRedTest}); err != nil {
		t.Fatal(err)
	}
	if m := loadQueue(root).Tasks["XR-001"]; m == nil ||
		queueMarkField(t, root, "XR-001", "ready") == "" {
		t.Fatalf("подтверждённая готовность обязана запомнить отпечаток диффа: %+v", m)
	}
	// Второй заход: линия коммитов не готова, ребейз перезастаил коммиты,
	// а отпечаток диффа держит готовность.
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001 слита очередью") {
		t.Fatalf("ребейз очереди не гасит подтверждённую готовность: %q", msg)
	}
}

// TestQueueDrainSkipsRowWithOpenNotes: открытое замечание ревью гасит
// готовность до заказа слияния, а не воротами merge, которые уже успели бы
// оставить след попытки.
func TestQueueDrainSkipsRowWithOpenNotes(t *testing.T) {
	root := queueSetup(t)
	wt := taskTree(root, "XR-001")
	doc, err := os.ReadFile(filepath.Join(wt, "docs", "tasks", "XR-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, wt, "docs/tasks/XR-001.md", string(doc)+"- передумать: без исхода\n")
	gitT(t, wt, "add", "docs/tasks/XR-001.md")
	gitT(t, wt, "commit", "-qm", "docs(tasks): XR-001 замечание ревью")
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "XR-001 слита") {
		t.Fatalf("строка с открытым замечанием получила слияние: %q", msg)
	}
	if !strings.Contains(msg, "XR-001: ждёт готовности: в ревью XR-001 замечаний без исхода") {
		t.Fatalf("открытое замечание обязано стоять в причине ожидания: %q", msg)
	}
}

// TestQueueReadinessReadsDocFromBranch: кандидат без распакованного дерева
// читает файл задачи из ветки, и открытое замечание веточной версии гасит
// готовность, а не выглядит «файл не читается».
func TestQueueReadinessReadsDocFromBranch(t *testing.T) {
	root := queueSetup(t)
	wt := taskTree(root, "XR-001")
	doc, err := os.ReadFile(filepath.Join(wt, "docs", "tasks", "XR-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, wt, "docs/tasks/XR-001.md", string(doc)+"- передумать: без исхода\n")
	gitT(t, wt, "add", "docs/tasks/XR-001.md")
	gitT(t, wt, "commit", "-qm", "docs(tasks): XR-001 замечание")
	if out, err := git(root, "worktree", "remove", "--force", wt); err != nil {
		t.Fatal(err, out)
	}
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "XR-001 слита") {
		t.Fatalf("строка с открытым замечанием в ветке получила слияние: %q", msg)
	}
	if !strings.Contains(msg, "XR-001: ждёт готовности: в ревью XR-001 замечаний без исхода") {
		t.Fatalf("файл задачи обязан читаться из ветки: %q", msg)
	}
}

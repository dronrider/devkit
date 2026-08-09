package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDeploy кладёт обвязку выката проекта с нужным значением флага
// автономии: подсказка перед слиянием читает её живьём.
func writeDeploy(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, deployConfigPath), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAutonomousFlag: флаг читается из плоского конфига, а без файла, без
// ключа и на мусорном значении остаётся снятым, как у shipctl.
func TestAutonomousFlag(t *testing.T) {
	root := setup(t)
	if autonomousFlag(root) {
		t.Fatal("без файла флаг поднят")
	}
	cases := []struct {
		body string
		want bool
	}{
		{"autonomous = true\n", true},
		{"# autonomous = true\ndeploy = echo\n", false},
		{"autonomous = false\n", false},
		{"autonomous = \"true\"\n", true},
		{"autonomous = ага\n", false},
		{"deploy = echo катим\n", false},
	}
	for _, c := range cases {
		writeDeploy(t, root, c.body)
		if got := autonomousFlag(root); got != c.want {
			t.Errorf("конфиг %q: жду %v, получил %v", c.body, c.want, got)
		}
	}
}

// TestNextAfterMove: у каждого статуса своя подсказка, и подсказка взятия в
// работу называет ветку autonomous вместе со значением флага.
func TestNextAfterMove(t *testing.T) {
	root := setup(t)
	cases := []struct {
		target string
		want   string
	}{
		{SectInProgress, "shipctl start XR-005"},
		{SectCheck, "docs/tasks/XR-005.md"},
		{SectBlocked, "снять блокер"},
		{SectBacklog, "ждёт очереди в Backlog"},
	}
	for _, c := range cases {
		got := nextAfterMove(root, "XR-005", c.target)
		if !strings.HasPrefix(got, "следующий шаг") {
			t.Errorf("%s: подсказка без общего зачина: %q", c.target, got)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: жду %q, получил %q", c.target, c.want, got)
		}
	}

	// Сдача это Check, а не написанный код: на этом месте встал живой прогон
	// DK-180, и слова про сдачу обязаны прозвучать при взятии в работу.
	inProg := nextAfterMove(root, "XR-005", SectInProgress)
	if !strings.Contains(inProg, "Check") {
		t.Fatalf("подсказка взятия в работу не называет сдачу: %q", inProg)
	}
	if !strings.Contains(inProg, "autonomous = false") || !strings.Contains(inProg, deployConfigPath) {
		t.Fatalf("без конфига развилка слияния не за пользователем: %q", inProg)
	}
	writeDeploy(t, root, "deploy = echo катим\nautonomous = true\n")
	inProg = nextAfterMove(root, "XR-005", SectInProgress)
	if !strings.Contains(inProg, "autonomous = true") || !strings.Contains(inProg, "Слияние зовёт агент сам") {
		t.Fatalf("при autonomous = true развилка не за агентом: %q", inProg)
	}

	// Перевод в Check называет обе ветки сценария: пометка стоит прозой в
	// файле задачи, и выбрать за автора утилита не может.
	check := nextAfterMove(root, "XR-005", SectCheck)
	if !strings.Contains(check, "агентский") || !strings.Contains(check, "пользовательский") {
		t.Fatalf("перевод в Check не называет ветки сценария: %q", check)
	}
}

// TestMoveAndClosePrintNextStep: подсказка доезжает до вывода самих команд
// перехода, а не только до своей функции.
func TestMoveAndClosePrintNextStep(t *testing.T) {
	root := setup(t)
	msg, err := cmdMove(root, "XR-002", SectInProgress, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	last := msg[strings.LastIndex(msg, "\n")+1:]
	if !strings.HasPrefix(last, "следующий шаг: код в дереве задачи (shipctl start XR-002)") {
		t.Fatalf("move не печатает следующий шаг: %q", msg)
	}

	msg, err = cmdMove(root, "XR-002", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "следующий шаг: прогнать сценарий проверки") {
		t.Fatalf("перевод в Check без подсказки: %q", msg)
	}

	msg, err = cmdClose(root, CloseParams{ID: "XR-002", Date: "2026-08-09"})
	if err != nil {
		t.Fatal(err)
	}
	last = msg[strings.LastIndex(msg, "\n")+1:]
	if !strings.HasPrefix(last, "следующий шаг: очередь выката свободна") {
		t.Fatalf("close не печатает следующий шаг: %q", msg)
	}
}

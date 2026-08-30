package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain держит уведомитель выключенным по умолчанию (DEVKIT_NOTIFY_OFF):
// без стаба notifyScript у большинства тестов уходит в ~/projects/devkit,
// а там лежит настоящий hooks/notify.py, который на машине разработчика
// реально дал бы баннер. Тесты самого уведомления включают его точечно.
//
// Он же снимает с прогона ID живой сессии. Команды доски отмечают работу
// сессии строкой в реестре (touchWork -> sessions.Touch), и берётся ID из
// окружения: прогон, запущенный из живого разговора, дописывал её именем
// десятки записей в живой ~/.devkit/sessions.log деревьями /var/folders/...
// Реестр после этого забывал имя tmux живой сессии, и дашборд переставал
// видеть, чем её снимать (живой случай chat-DK-397-1, замечание
// пользователя). Без ID отметка работы не пишется никуда вовсе.
//
// Тем же ходом глохнет shipctl: с DK-312 close зовёт разлив, и без стаба
// close-тесты звали бы настоящий shipctl с машины (drain_test.go).
func TestMain(m *testing.M) {
	os.Setenv("DEVKIT_NOTIFY_OFF", "1")
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	muteShipctlInTests()
	os.Exit(m.Run())
}

// writeNotifyStub кладёт в root/hooks/notify.py поддельный уведомитель:
// вместо реальной отправки он пишет свои аргументы (без argv[0]) построчно
// в hooks/calls.log и завершается exitCode. Так move проверяется без риска
// реального баннера и без зависимости от системного бэкенда. DEVKIT_HOME
// прибивается к корню стаба: она первая в поиске notifyScript, и чужое
// значение из окружения сессии (например, POC-дерева) уводило бы вызов мимо
// стаба в живой notify.py.
func writeNotifyStub(t *testing.T, root string, exitCode int) string {
	t.Helper()
	t.Setenv("DEVKIT_HOME", root)
	dir := filepath.Join(root, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/usr/bin/env python3
import sys
with open(%q, "a") as f:
    f.write("\t".join(sys.argv[1:]) + "\n")
sys.exit(%d)
`, callLog, exitCode)
	if err := os.WriteFile(filepath.Join(dir, "notify.py"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return callLog
}

func TestMoveToCheckNotifiesLoud(t *testing.T) {
	root := setup(t)
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root, 0)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("уведомитель не позван: %v", err)
	}
	line := strings.TrimSpace(string(got))
	if !strings.Contains(line, "XR-005") || !strings.Contains(line, "в Check") {
		t.Fatalf("заголовок уведомления не про Check: %q", line)
	}
	// Повод едет словом: по нему лента дашборда отбирает события задач, а из
	// заголовка тип события не вытащить.
	if !strings.HasPrefix(line, "--reason\ttask_check\t") {
		t.Fatalf("уведомление ушло без повода task_check: %q", line)
	}
	// Задача едет своим ключом (DK-323): по полю события лента дашборда ведёт
	// к строке доски, а из заголовка баннера ID она больше не выковыривает.
	if !strings.Contains(line, "--task\tXR-005\t") {
		t.Fatalf("уведомление ушло без поля задачи: %q", line)
	}
	if !strings.HasSuffix(line, "Задача в работе") {
		t.Fatalf("в теле нет заголовка задачи: %q", line)
	}
}

func TestMoveToBlockedNotifiesLoud(t *testing.T) {
	root := setup(t)
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root, 0)
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("уведомитель не позван: %v", err)
	}
	line := strings.TrimSpace(string(got))
	if !strings.Contains(line, "XR-004") || !strings.Contains(line, "на блокере") {
		t.Fatalf("заголовок уведомления не про блокер: %q", line)
	}
	if !strings.HasPrefix(line, "--reason\ttask_blocked\t") {
		t.Fatalf("уведомление ушло без повода task_blocked: %q", line)
	}
	if !strings.Contains(line, "--task\tXR-004\t") {
		t.Fatalf("уведомление ушло без поля задачи: %q", line)
	}
	if !strings.HasSuffix(line, "ждём железо") {
		t.Fatalf("в теле нет причины блокировки: %q", line)
	}
}

// TestMoveToInProgressDoesNotNotify: у входа в In progress нет повода, ради
// которого стоит звать человека (RULES.board.md), уведомитель молчит.
func TestMoveToInProgressDoesNotNotify(t *testing.T) {
	root := setup(t)
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root, 0)
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(calls); err == nil {
		t.Fatal("уведомитель позван на входе в In progress, не должен")
	}
}

// TestMoveSurvivesMissingNotifier: move не должен падать, если уведомитель
// не нашёлся, но и молчать об этом нельзя, RULES.md требует того и другого.
func TestMoveSurvivesMissingNotifier(t *testing.T) {
	root := setup(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEVKIT_HOME", "")
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	msg, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatalf("move упал из-за ненайденного уведомителя: %v", err)
	}
	if !strings.Contains(msg, "уведомление не отправлено") {
		t.Fatalf("непосланное уведомление прошло молча: %q", msg)
	}
}

// TestMoveSurvivesFailingNotifier: тот же случай, но notify.py нашёлся и
// упал (например, бэкенда на машине нет).
func TestMoveSurvivesFailingNotifier(t *testing.T) {
	root := setup(t)
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	writeNotifyStub(t, root, 1)
	msg, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatalf("move упал из-за упавшего уведомителя: %v", err)
	}
	if !strings.Contains(msg, "уведомление не отправлено") {
		t.Fatalf("провал отправки прошёл молча: %q", msg)
	}
}

// TestMoveFromSandboxSkipsBanner: корень доски лежит во временной директории,
// это песочница (синтетическая доска из обкатки сценария), и живой notify.py
// молчит сам, без заглушек на стороне зовущего; пропуск при этом не проходит
// тихо, причина доезжает припиской до вывода move (DK-069).
func TestMoveFromSandboxSkipsBanner(t *testing.T) {
	root := setup(t)
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	t.Setenv("HOME", t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(filepath.Dir(wd)))
	// Бэкенд подставной: сработай отправка вопреки рубежу, тест увидит след,
	// а не покажет живой баннер на машине разработчика.
	sent := filepath.Join(t.TempDir(), "sent")
	stub := filepath.Join(t.TempDir(), "backend")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n: > "+sent+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_NOTIFY_BACKEND", stub)
	msg, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "уведомление пропущено") {
		t.Fatalf("пропуск песочницы не дошёл до вывода move: %q", msg)
	}
	if _, err := os.Stat(sent); err == nil {
		t.Fatal("баннер ушёл из песочницы")
	}
}

func TestNotifyScriptResolution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEVKIT_HOME", "")
	root := t.TempDir()
	if _, err := notifyScript(root); err == nil || !strings.Contains(err.Error(), "DEVKIT_HOME") {
		t.Fatalf("ждал отказ без единой кандидатуры, получил %v", err)
	}
	own := filepath.Join(root, "hooks", "notify.py")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := notifyScript(root); err != nil || got != own {
		t.Fatalf("не нашёл notify.py в корне проекта: %v %q", err, got)
	}
	dh := t.TempDir()
	dhScript := filepath.Join(dh, "hooks", "notify.py")
	if err := os.MkdirAll(filepath.Dir(dhScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dhScript, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", dh)
	if got, err := notifyScript(root); err != nil || got != dhScript {
		t.Fatalf("DEVKIT_HOME не победил: %v %q", err, got)
	}
}

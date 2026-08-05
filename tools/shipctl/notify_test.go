package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain держит уведомитель выключенным по умолчанию (DEVKIT_NOTIFY_OFF):
// без стаба notifyScript у большинства тестов уходит в ~/projects/devkit, а
// там лежит настоящий hooks/notify.py, который на машине разработчика реально
// дал бы баннер. Тесты самого уведомления включают его точечно.
func TestMain(m *testing.M) {
	os.Setenv("DEVKIT_NOTIFY_OFF", "1")
	os.Exit(m.Run())
}

// writeNotifyStub кладёт в root/hooks/notify.py поддельный уведомитель:
// вместо реальной отправки он пишет свои аргументы (без argv[0]) построчно в
// hooks/calls.log и завершается exitCode. Так merge/ship/revert проверяются
// без риска реального баннера и без зависимости от системного бэкенда.
func writeNotifyStub(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/usr/bin/env python3
import sys
with open(%q, "a") as f:
    f.write("\t".join(sys.argv[1:]) + "\n")
`, callLog)
	if err := os.WriteFile(filepath.Join(dir, "notify.py"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return callLog
}

// TestMergeDeployFailureNotifies: выкат при одиночном merge упал, задача
// остаётся в In progress, но человека об этом позвали громко (самый громкий
// из трёх поводов DK-063, прод в этот момент может быть сломан).
func TestMergeDeployFailureNotifies(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root)
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "echo прод сломан >&2; false"})
	if err == nil || !strings.Contains(err.Error(), "выкат упал") {
		t.Fatalf("ждал отказ по выкату, получил: %v", err)
	}
	got, rerr := os.ReadFile(calls)
	if rerr != nil {
		t.Fatalf("уведомитель не позван: %v", rerr)
	}
	line := strings.TrimSpace(string(got))
	if !strings.Contains(line, "XR-001") || !strings.Contains(line, "выкат") || !strings.Contains(line, "упал") {
		t.Fatalf("заголовок уведомления не про упавший выкат: %q", line)
	}
	if !strings.Contains(line, "прод сломан") {
		t.Fatalf("в теле нет вывода упавшей команды: %q", line)
	}
	// Доска не переведена: задача остаётся в In progress, а не в Check.
	if b, berr := loadBoard(root); berr != nil || b.sectOf("XR-001") != "in-progress" {
		t.Fatalf("XR-001 должна остаться в In progress: %v %v", berr, b)
	}
}

// TestShipDeployFailureNotifies: то же самое на выкате поезда.
func TestShipDeployFailureNotifies(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root)
	_, err := cmdShip(root, ShipParams{Deploy: "false"})
	if err == nil || !strings.Contains(err.Error(), "выкат поезда упал") {
		t.Fatalf("ждал отказ по выкату поезда, получил: %v", err)
	}
	got, rerr := os.ReadFile(calls)
	if rerr != nil {
		t.Fatalf("уведомитель не позван: %v", rerr)
	}
	line := strings.TrimSpace(string(got))
	if !strings.Contains(line, "XR-001") || !strings.Contains(line, "поезда") || !strings.Contains(line, "упал") {
		t.Fatalf("заголовок уведомления не про упавший выкат поезда: %q", line)
	}
}

// TestMergeDeployFailureFromSandboxRelaysSkip: репозиторий во временной
// директории это песочница, живой notify.py оттуда молчит сам (DK-069), а
// причина пропуска доезжает до сообщения об упавшем выкате.
func TestMergeDeployFailureFromSandboxRelaysSkip(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	t.Setenv("HOME", t.TempDir())
	wd, werr := os.Getwd()
	if werr != nil {
		t.Fatal(werr)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(filepath.Dir(wd)))
	// Бэкенд подставной: сработай отправка вопреки рубежу, на машине
	// разработчика не появится живой баннер.
	stub := filepath.Join(t.TempDir(), "backend")
	if werr := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); werr != nil {
		t.Fatal(werr)
	}
	t.Setenv("DEVKIT_NOTIFY_BACKEND", stub)
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "false"})
	if err == nil || !strings.Contains(err.Error(), "выкат упал") {
		t.Fatalf("ждал отказ по выкату, получил: %v", err)
	}
	if !strings.Contains(err.Error(), "уведомление пропущено") {
		t.Fatalf("пропуск песочницы не дошёл до сообщения об ошибке: %v", err)
	}
}

// TestRevertRedeployFailureNotifies: откат закоммичен, но повторный выкат
// (revert -> catch) сам упал, прод остаётся в сломанном состоянии.
func TestRevertRedeployFailureNotifies(t *testing.T) {
	root, _ := setup(t, "", rowInProg) // задача уже в Check
	addRemote(t, root)
	write(t, root, "code.txt", "broken\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: XR-001 фича")
	gitT(t, root, "push", "-q")
	write(t, root, ".devkit/deploy.local", "deploy = false\nautonomous = true\n")
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root)

	_, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "повторный выкат упал") {
		t.Fatalf("ждал отказ по повторному выкату, получил: %v", err)
	}
	got, rerr := os.ReadFile(calls)
	if rerr != nil {
		t.Fatalf("уведомитель не позван: %v", rerr)
	}
	line := strings.TrimSpace(string(got))
	if !strings.Contains(line, "XR-001") || !strings.Contains(line, "повторный выкат") || !strings.Contains(line, "упал") {
		t.Fatalf("заголовок уведомления не про упавший повторный выкат: %q", line)
	}
}

// TestMergeSurvivesMissingNotifier: провал выката с ненайденным уведомителем
// не должен добавлять свою собственную панику к уже упавшему выкату, но и
// молчать об этом нельзя (RULES.md: не падать из-за уведомления, не молчать
// про непосланное).
func TestMergeSurvivesMissingNotifier(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEVKIT_HOME", "")
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "false"})
	if err == nil || !strings.Contains(err.Error(), "выкат упал") {
		t.Fatalf("ждал отказ по выкату, получил: %v", err)
	}
	if !strings.Contains(err.Error(), "уведомление не отправлено") {
		t.Fatalf("непосланное уведомление прошло молча: %v", err)
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

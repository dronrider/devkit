package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Предел ожидания своего подпроцесса: security может встать в ожидании
// авторизации UI (makeLoginAuthUI) и не отвечать бесконечно. Заглушка спит
// дольше предела теста, вызывающий обязан снять её по сроку и вернуть ошибку,
// а не ждать потолка go test и оставлять сирот. Предел здесь впрыскивается
// переменной окружения, а не полем: файл переносится на старый код целиком и
// обязан собираться без новых символов (regcheck, --tests).
func TestRealSecurityTimeoutKillsHungSecurity(t *testing.T) {
	t.Setenv("SECRETCTL_SECURITY_TIMEOUT", "1s")
	dir := t.TempDir()
	stub := filepath.Join(dir, "security")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &realSecurity{Path: stub}
	for _, name := range []string{"Add", "Find"} {
		done := make(chan error, 1)
		go func(name string) {
			if name == "Add" {
				done <- r.Add("devkit.secretctl", "PROBE", "value")
				return
			}
			_, err := r.Find("devkit.secretctl", "PROBE")
			done <- err
		}(name)
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("%s: прошёл после зависшего security, жду ошибку предела", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: завис вместе с security: нет предела ожидания", name)
		}
	}
}

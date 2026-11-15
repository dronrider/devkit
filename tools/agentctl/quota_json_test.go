package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuotaJSONReadOnlyRefusals(t *testing.T) {
	for _, name := range []string{"healthy", "optional-gap", "missing", "stale", "future", "incomplete", "partial", "borrowed", "spent", "expired", "no-reset", "no-buckets"} {
		t.Run(name, func(t *testing.T) {
			q := glmSpec(t, filepath.Join(t.TempDir(), "quota.local"))
			q.Buckets = []string{"week_all", "window5h_all"}
			s := snapOf(time.Minute, bucketAt("week_all", 20, time.Hour), bucketAt("window5h_all", 20, time.Hour))
			switch name {
			case "stale":
				s.Taken = testNow.Add(-snapshotMaxAge - time.Second)
			case "future":
				s.Taken = testNow.Add(time.Minute)
			case "incomplete":
				s.Buckets = s.Buckets[:1]
			case "optional-gap":
				// Пропавшее необязательное окно не пробел: у обязательных
				// бакетов цифры на месте, и вердикт остаётся доступным.
				q.Optional = []string{"window5h_all"}
				s.Buckets = s.Buckets[:1]
			case "partial":
				s.Partial = map[string]string{"week_all": "нет ответа"}
			case "borrowed":
				s.Borrowed = map[string]string{"week_all": "старое значение"}
			case "spent":
				s.Buckets[0].Used = 1
			case "expired":
				s.Buckets[0].Reset = testNow.Add(-time.Second)
			case "no-reset":
				s.Buckets[0].Reset = time.Time{}
			case "no-buckets":
				q.Buckets = nil
			}
			if name != "missing" {
				if err := q.write(s); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := q.read()
			out, err := cmdQuotaJSON(q, testNow)
			if err != nil {
				t.Fatal(err)
			}
			var got quotaJSON
			if err = json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if got.Schema != "agentctl/quota" || got.Version != 1 || got.Harness != q.Harness {
				t.Fatal(out)
			}
			if got.Available != (name == "healthy" || name == "optional-gap") {
				t.Fatalf("%s: %s", name, out)
			}
			if !got.Available && got.Reason == "" {
				t.Fatal("нет причины отказа")
			}
			after, _ := q.read()
			if !before.Taken.Equal(after.Taken) {
				t.Fatal("чтение пересняло квоту")
			}
		})
	}
}

func TestQuotaJSONRefusesParserWarnings(t *testing.T) {
	q := glmSpec(t, filepath.Join(t.TempDir(), "quota.local"))
	s := snapOf(time.Minute, bucketAt("week_all", 20, time.Hour), bucketAt("window5h_all", 20, time.Hour))
	s.Warns = []string{"проверка"}
	if got := quotaRefusal(q, s, testNow, nil); !strings.Contains(got, "проверка") {
		t.Fatal(got)
	}
}

// TestQuotaJSONCodexWindows: у Codex недельное окно обязательное, а
// пятичасовое живёт в подписке с перерывами и объявлено необязательным.
// Снимок без пятичасового окна это полноценный снимок: полноту и доступность
// из quota --json читает восстановитель конвейера, и пробел в перерывистом
// окне закрывал бы запуск по подписке навсегда. Профилю без optional та же
// строгость сохраняется: его исчезнувшее окно остаётся пробелом.
func TestQuotaJSONCodexWindows(t *testing.T) {
	cases := []struct {
		name    string
		windows []bucket
	}{
		{"week-only", []bucket{bucketAt("week_all", 46, 72*time.Hour)}},
		{"both", []bucket{bucketAt("week_all", 46, 72*time.Hour), bucketAt("window5h_all", 12, time.Hour)}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			q := codexSpec(t, filepath.Join(t.TempDir(), "codex.local"))
			if err := q.write(snapOf(time.Minute, tt.windows...)); err != nil {
				t.Fatal(err)
			}
			out, err := cmdQuotaJSON(q, testNow)
			if err != nil {
				t.Fatal(err)
			}
			var got quotaJSON
			if err = json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Complete || !got.Available || got.Reason != "" {
				t.Fatalf("снимок %s не прошёл целиком: %s", tt.name, out)
			}
			if len(got.Buckets) != len(tt.windows) {
				t.Fatalf("в ответе %d бакетов, жду %d: %s", len(got.Buckets), len(tt.windows), out)
			}
			for _, w := range tt.windows {
				found := false
				for _, b := range got.Buckets {
					if b.Name == w.Name {
						found = true
					}
				}
				if !found {
					t.Fatalf("в ответе нет окна %s: %s", w.Name, out)
				}
			}
		})
	}
	glm := glmSpec(t, filepath.Join(t.TempDir(), "glm-code.local"))
	if err := glm.write(snapOf(time.Minute, bucketAt("week_all", 46, 72*time.Hour))); err != nil {
		t.Fatal(err)
	}
	out, err := cmdQuotaJSON(glm, testNow)
	if err != nil {
		t.Fatal(err)
	}
	var got quotaJSON
	if err = json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Complete || got.Available || !strings.Contains(got.Reason, "window5h_all") {
		t.Fatalf("профиль без optional потерял строгость: %s", out)
	}
}

func TestQuotaJSONCLIIsReadOnly(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentctl")
	build := exec.Command("go", "build", "-p", "2", "-o", bin, ".")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	home, root := t.TempDir(), t.TempDir()
	writeFile(t, root, "docs/TASKS.md", "# Стенд (префикс RV)\n")
	writeFile(t, root, ".devkit/log", "before\n")
	writeFile(t, home, ".devkit/harness.local", `default = "glm-code"
enabled = ["glm-code"]
auto = ["glm-code"]
[glm-code]
mini = "glm-5.3-flash"
base = "glm-5.3"
pro = "glm-5.3"
max = "glm-5.3"
`)
	cases := [][]string{{"quota", "--json", "refresh"}, {"quota", "--json", "--all"}, {"quota", "--json", "--if-stale"}}
	for _, args := range cases {
		cmd := exec.Command(bin, append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t))
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "--json") {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	for _, args := range [][]string{{"quota", "--json", "--harness", "glm-code"}, {"harness", "--json"}} {
		cmd := exec.Command(bin, append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t))
		out, err := cmd.CombinedOutput()
		if err != nil || !json.Valid(out) {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	cmd := exec.Command(bin, "-C", root, "wait", "RV-1", "есть", filepath.Join(root, "docs"), "--check")
	cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("read-only wait: %v %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("чтение изменило журнал: %v %q", err, data)
	}
	if _, err := os.Stat(filepath.Join(home, ".devkit", "quota")); !os.IsNotExist(err) {
		t.Fatal("чтение создало снимок квоты")
	}
}

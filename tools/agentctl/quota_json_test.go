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
	for _, name := range []string{"healthy", "missing", "stale", "future", "incomplete", "partial", "borrowed", "spent", "expired", "no-reset", "no-buckets"} {
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
			if got.Available != (name == "healthy") {
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

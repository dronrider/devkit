package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRecordNameEvidence(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	const id = "XR-005"
	path := completeReviewAudit(t, root, id)
	put := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	put(filepath.Join(root, "sample.go"), "package sample\nfunc userQuota() {}\n")
	if out, err := runCLI(t, "-C", root, "review", "audit", id, "--refresh"); err != nil {
		t.Fatalf("refresh: %s %v", out, err)
	}
	const object = "sample.go:2#userQuota"
	input := filepath.Join(t.TempDir(), "evidence.json")
	record := func() (string, error) {
		return runCLI(t, "-C", root, "review", "audit-record", id, "--rule", "STYLE-001", "--object", object, "--input", input)
	}
	const result = `{"object":"sample.go:2#userQuota","check":"result","result":"not_applicable","location":"sample.go:2","observation":"Нет возвращаемого значения","object_comparison":{"expected":["user","Quota"],"actual":"","actual_at":""}}`
	const writes = `{"object":"sample.go:2#userQuota","check":"writes","result":"not_applicable","location":"sample.go:2","observation":"Пустое тело функции"}`
	const meaning = `{"object":"sample.go:2#userQuota","check":"meaning","result":"checked","symbol":"userQuota","location":"sample.go:2","observation":"Проверено назначение","word_checks":[{"word":"user","meaning":"пользователь","fact":"слово обозначает пользователя","location":"sample.go:2","result":"checked"},{"word":"Quota","meaning":"квота","fact":"слово обозначает ограничение","location":"sample.go:2","result":"checked"}]}`
	put(input, "["+result+","+writes+"]")
	if out, err := record(); err != nil || !strings.Contains(out, "meaning") {
		t.Fatalf("сохранение двух подпроверок: %s %v", out, err)
	}
	put(input, "["+meaning+"]")
	for range 2 {
		if out, err := record(); err != nil || !strings.Contains(out, "meaning: checked") {
			t.Fatalf("сохранение и повтор: %s %v", out, err)
		}
	}
	out, err := runCLI(t, "-C", root, "review", "audit-progress", id, "--json")
	var progress struct{ Closed, Rejected, Missing int }
	if err != nil || json.Unmarshal([]byte(out), &progress) != nil || progress.Closed != 1 || progress.Rejected+progress.Missing != 0 {
		t.Fatalf("цель открыта: %s %v", out, err)
	}
	before := read()
	var compact bytes.Buffer
	if err := json.Compact(&compact, before); err != nil || !strings.Contains(compact.String(), `"expected":["user","Quota"]`) {
		t.Fatalf("потеряно обещание имени: %v", err)
	}
	put(input, "["+result+","+strings.Replace(writes, object, "other", 1)+"]")
	if _, err := record(); err == nil || !bytes.Equal(before, read()) {
		t.Fatal("пакет с чужой целью должен целиком отклоняться")
	}
	put(input, "["+strings.Replace(result, "Нет возвращаемого значения", "Изменённое доказательство", 1)+"]")
	for _, denied := range []string{path, filepath.Dir(path)} {
		t.Run(filepath.Base(denied), func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("для отказа в записи нужен обычный пользователь")
			}
			info, err := os.Stat(denied)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(denied, info.Mode().Perm()&^0222); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(denied, info.Mode().Perm()); err != nil {
					t.Error(err)
				}
			})
			out, err := record()
			if err == nil || !strings.Contains(out, path) || !strings.Contains(out, os.ErrPermission.Error()) || !bytes.Equal(before, read()) {
				t.Fatalf("отказ должен оставить аудит и назвать путь: %s %v", out, err)
			}
		})
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/reviewaudit"
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

// An import keyed only by check would drop the writes of one of the objects.
// Writing before validation would retain a candidate from a rejected batch.
func TestCLIRecordNameEvidenceBatch(t *testing.T) {
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
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	put(filepath.Join(root, "a.go"), "package sample\nfunc alpha() {}\n")
	put(filepath.Join(root, "b.go"), "package sample\nfunc beta() {}\n")
	if out, err := runCLI(t, "-C", root, "review", "audit", id, "--refresh"); err != nil {
		t.Fatalf("refresh: %s %v", out, err)
	}
	input := filepath.Join(t.TempDir(), "evidence.json")
	runBatch := func(wantSaved bool) reviewaudit.NameEvidenceBatchResult {
		t.Helper()
		cmd := exec.Command("go", "run", ".", "-C", root, "review", "audit-record", id, "--batch", "--input", input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if (err == nil) != wantSaved {
			t.Fatalf("код выхода: %v\n%s\n%s", err, out, stderr.String())
		}
		var result reviewaudit.NameEvidenceBatchResult
		if err := json.Unmarshal(out, &result); err != nil || result.Saved != wantSaved {
			t.Fatalf("результат: %s\n%s\n%v", out, stderr.String(), err)
		}
		return result
	}
	const alpha = `{"object":"a.go:2#alpha","check":"writes","result":"not_applicable","location":"a.go:2","observation":"Пустое тело alpha"}`
	const beta = `{"object":"b.go:2#beta","check":"writes","result":"not_applicable","location":"b.go:2","observation":"Пустое тело beta"}`
	put(input, "["+alpha+","+beta+"]")
	result := runBatch(true)
	if result.Submitted != 2 || result.Progress == nil || result.Progress.Missing != 2 || len(result.Progress.OpenFiles) != 2 {
		t.Fatalf("потеряны открытые цели: %+v", result)
	}
	for i, object := range []string{"a.go:2#alpha", "b.go:2#beta"} {
		file := result.Progress.OpenFiles[i]
		if len(file.Goals) != 1 || file.Goals[0].Object != object ||
			!slices.Equal(file.Goals[0].MissingChecks, []string{"meaning", "result"}) {
			t.Fatalf("неверные пробелы в %s: %+v", file.Path, file.Goals)
		}
	}
	before := read()
	runBatch(true)
	if !bytes.Equal(before, read()) {
		t.Fatal("повтор пакета изменил сохранённые записи")
	}
	for _, observation := range []string{"Пустое тело alpha", "Пустое тело beta"} {
		if !bytes.Contains(before, []byte(observation)) {
			t.Fatalf("запись потеряна: %s", observation)
		}
	}
	const foreign = `{"object":"other","check":"writes"}`
	const badAlpha = `{"object":"a.go:2#alpha","check":"meaning","symbol":"foreign","location":"a.go:2","result":"checked","observation":"Имя из другого файла"}`
	const badBeta = `{"object":"b.go:2#beta","check":"result","location":"b.go:2","result":"unknown"}`
	updatedAlpha := strings.Replace(alpha, "Пустое тело alpha", "Новое наблюдение", 1)
	put(input, "["+strings.Join([]string{updatedAlpha, foreign, badAlpha, badBeta, alpha}, ",")+"]")
	result = runBatch(false)
	if result.Submitted != 5 || result.Progress == nil || result.Progress.Rejected != 2 || !bytes.Equal(before, read()) {
		t.Fatalf("неверный или частично записанный пакет: %+v", result)
	}
	var rows []int
	for _, issue := range result.Issues {
		rows = append(rows, issue.Row)
	}
	if !slices.Equal(rows, []int{2, 3, 4, 4, 4, 5}) {
		t.Fatalf("потеряны ошибки обеих целей или повтор: %+v", result.Issues)
	}
	if !strings.Contains(result.Issues[1].Message, `symbol "alpha"`) || !strings.Contains(result.Issues[len(result.Issues)-1].Message, "повторное") {
		t.Fatalf("ошибки без причины: %+v", result.Issues)
	}
}

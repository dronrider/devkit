package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DoD DK-932: merge в конце зовёт обход ждущих taskctl в корне слияния, и зовёт
// после перевода в Check, когда работа задачи уже лежит в main. Без --push и без
// автономного выката обход коммитит перевод строк, но не пушит.
func TestMergeCallsWake(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(callLog)
	got := string(calls)
	move := strings.Index(got, "move XR-001 check")
	wake := strings.Index(got, " wake --quiet")
	if move < 0 || wake < move {
		t.Fatalf("жду обход ждущих после перевода в Check:\n%s", got)
	}
	if strings.Contains(got, "wake --quiet --push") {
		t.Fatalf("обход запушил доску без --push:\n%s", got)
	}
}

// Слова обхода идут в отчёт merge как есть: подъём строки, ждавшей слияния,
// виден тому, кто сливал.
func TestMergeReportsWake(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchWithFix(t, root)
	stub := "#!/bin/sh\necho \"$@\" >> \"" + callLog + "\"\n" +
		"if [ \"$3\" = wake ]; then echo 'XR-002: ждала «слияние: XR-001», XR-001 слита'; exit 0; fi\n" +
		"printf '<!-- move -->\\n' >> \"$2/docs/TASKS.md\"\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(callLog), "taskctl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Push: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-002: ждала «слияние: XR-001», XR-001 слита") {
		t.Fatalf("подъём ждущей не попал в отчёт merge:\n%s", msg)
	}
}

// Отказ обхода слияние не роняет: слова идут в отчёт, строку повторит тик.
func TestMergeWakeFailureIsANote(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchWithFix(t, root)
	stub := "#!/bin/sh\necho \"$@\" >> \"" + callLog + "\"\n" +
		"if [ \"$3\" = wake ]; then echo 'XR-002: голову поднять нечем'; exit 1; fi\n" +
		"printf '<!-- move -->\\n' >> \"$2/docs/TASKS.md\"\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(callLog), "taskctl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("отказ обхода уронил merge: %v", err)
	}
	if !strings.Contains(msg, "обход ждущих оставил строки стоять") || !strings.Contains(msg, "голову поднять нечем") {
		t.Fatalf("отказ обхода не назван в отчёте:\n%s", msg)
	}
}

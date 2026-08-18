package main

import (
	"strings"
	"testing"
)

// TestDraftTitleGuardEdge: порог держится ровно на 72 символах, 73 отбиваются,
// а совет про stdin печатается только тому, кто пришёл аргументом.
func TestDraftTitleGuardEdge(t *testing.T) {
	ok := strings.Repeat("а", draftTitleLimit)
	if err := draftTitleGuard(ok, false); err != nil {
		t.Fatalf("строка в %d символов отбита: %v", draftTitleLimit, err)
	}
	err := draftTitleGuard(ok+"б", false)
	if err == nil {
		t.Fatalf("строка в %d символов прошла", draftTitleLimit+1)
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Errorf("отказ на аргументе не советует stdin: %v", err)
	}
	err = draftTitleGuard(ok+"б", true)
	if err == nil {
		t.Fatal("со stdin строка длиннее порога прошла")
	}
	if strings.Contains(err.Error(), "stdin") || strings.Contains(err.Error(), "EOF") {
		t.Errorf("отказ пришедшему со stdin советует stdin: %v", err)
	}
	if !strings.Contains(err.Error(), formDoc) {
		t.Errorf("отказ не называет форму: %v", err)
	}
}

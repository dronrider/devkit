package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestReviewWorklogAmbiguousFailureDoesNotRepeat(t *testing.T) {
	root := setup(t)
	old := runTrackctl
	t.Cleanup(func() { runTrackctl = old })
	calls := 0
	runTrackctl = func(string, ...string) ([]byte, error) { calls++; return nil, fmt.Errorf("connection lost") }
	_ = reviewWorklog(root, "XR-005", "Ревью ABC-12: проверка", "round")
	second := reviewWorklog(root, "XR-005", "Ревью ABC-12: проверка", "round")
	if calls != 1 || !strings.Contains(second, "повтор остановлен") {
		t.Fatalf("calls=%d output=%s", calls, second)
	}
}

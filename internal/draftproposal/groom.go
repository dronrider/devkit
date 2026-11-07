package draftproposal

import (
	"strings"

	"github.com/dronrider/devkit/internal/draftideas"
)

func HasGroomResult(record []byte, state State) bool {
	if state.State == "history" || len(state.Proposals) > 0 || strings.TrimSpace(state.Conclusion) != "" || state.Analysis == "empty" {
		return true
	}
	if draftideas.GroomSection(record) {
		return true
	}
	total, pending := draftideas.Count(record)
	return total > pending
}

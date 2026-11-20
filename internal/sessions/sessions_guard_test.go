package sessions_test

import (
	"os"
	"testing"

	"github.com/dronrider/devkit/internal/runsguard"
)

func TestMain(m *testing.M) {
	os.Unsetenv("CLAUDE_CODE_SESSION_ID")
	os.Unsetenv("CODEX_THREAD_ID")
	os.Exit(runsguard.Guard(m))
}

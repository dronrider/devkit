package taskhead

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type LegacyAppPassStatus string

const (
	LegacyAppPassNone      LegacyAppPassStatus = "none"
	LegacyAppPassAmbiguous LegacyAppPassStatus = "ambiguous"
	LegacyAppPassActive    LegacyAppPassStatus = "active"
)

// ReadLegacyAppPassStatus uses the same journal reader as the Python supervisor.
func ReadLegacyAppPassStatus(devkit, home, task, started string) (LegacyAppPassStatus, error) {
	journal := filepath.Join(home, ".devkit", "heads", task+".log")
	if _, err := os.Stat(journal); errors.Is(err, os.ErrNotExist) {
		return LegacyAppPassNone, nil
	} else if err != nil {
		return LegacyAppPassNone, fmt.Errorf("не прочитан старый журнал: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	script := filepath.Join(devkit, "tools", "devkitctl", "recovery.py")
	cmd := exec.CommandContext(ctx, "python3", script, "app-legacy", home, task, started)
	cmd.Dir = devkit
	raw, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(raw))
	if err != nil {
		return LegacyAppPassNone, fmt.Errorf("не прочитан старый журнал: %s (%w)", line, errors.Join(ctx.Err(), err))
	}
	status := LegacyAppPassStatus(line)
	switch status {
	case LegacyAppPassNone, LegacyAppPassAmbiguous, LegacyAppPassActive:
		return status, nil
	default:
		return LegacyAppPassNone, fmt.Errorf("старый журнал: неизвестный результат %q", line)
	}
}

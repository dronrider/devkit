package reviewaudit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func runNamePythonVerifier[T any](program string, request any) (T, error) {
	var reply T
	input, err := json.Marshal(request)
	if err != nil {
		return reply, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The verifier parses source from stdin. Only the bundled program is executed.
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", program)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return reply, fmt.Errorf("истёк срок разбора Python")
	}
	if err != nil {
		return reply, fmt.Errorf("разбор Python: %w %s", err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return reply, fmt.Errorf("ответ разбора Python: %w", err)
	}
	return reply, nil
}

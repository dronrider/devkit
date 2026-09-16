package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// TestJudgeBaseRun закрепляет признаки раннеров по выводу без репозитория.
func TestJudgeBaseRun(t *testing.T) {
	exit := func(code int) error {
		return exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	}
	cases := []struct {
		name string
		argv []string
		out  string
		err  error
		want baseVerdict
	}{
		{"cargo сборка", []string{"cargo", "test"}, "error[E0425]: cannot find\n", exit(101), verdictBuild},
		{"cargo could not compile", []string{"cargo", "test"}, "error: could not compile `x`\n", exit(101), verdictBuild},
		{"cargo упавший тест", []string{"cargo", "test"}, "test result: FAILED. 0 passed\n", exit(101), verdictRed},
		{"cargo без признака", []string{"cargo", "test"}, "error: failed to load manifest\n", exit(101), verdictUnproven},
		{"go сборка", []string{"go", "test", "./..."}, "FAIL\tpkg [build failed]\n", exit(1), verdictBuild},
		{"go setup", []string{"go", "test", "./..."}, "FAIL\tpkg [setup failed]\n", exit(1), verdictBuild},
		{"go упавший тест", []string{"go", "test", "./..."}, "--- FAIL: TestX (0.00s)\nFAIL\n", exit(1), verdictRed},
		{"go сборка и упавший тест рядом", []string{"go", "test", "./..."}, "--- FAIL: TestX\nFAIL\tpkg2 [build failed]\n", exit(1), verdictBuild},
		{"go без признака", []string{"go", "test", "./..."}, "flag provided but not defined\n", exit(2), verdictUnproven},
		{"чужой раннер с кодом", []string{"sh", "probe_test.sh"}, "", exit(1), verdictRed},
		{"чужой раннер с ошибкой сборки", []string{"make", "test"}, "error[E0433]: failed to resolve\n", exit(2), verdictBuild},
		{"не запустилась", []string{"./run.sh"}, "", &exec.Error{Name: "./run.sh", Err: os.ErrNotExist}, verdictNoStart},
	}
	for _, c := range cases {
		if got := judgeBaseRun(c.argv, c.out, c.err); got != c.want {
			t.Errorf("%s: вердикт %v, ожидала %v", c.name, got, c.want)
		}
	}
}

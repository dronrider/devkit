package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGlobalDirParse(t *testing.T) {
	cases := []struct {
		in   []string
		dir  string
		rest []string
	}{
		{[]string{"-C", "/x", "lint"}, "/x", []string{"lint"}},
		{[]string{"--C", "/x", "close", "XR-1"}, "/x", []string{"close", "XR-1"}},
		{[]string{"-C=/x", "sort"}, "/x", []string{"sort"}},
		{[]string{"--C=/x", "id"}, "/x", []string{"id"}},
		{[]string{"lint"}, "", []string{"lint"}},
		{[]string{"close", "-C", "/x", "XR-1"}, "", []string{"close", "-C", "/x", "XR-1"}},
	}
	for _, c := range cases {
		dir, rest, err := globalDir(c.in)
		if err != nil {
			t.Fatalf("%v: %v", c.in, err)
		}
		if dir != c.dir || strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Errorf("%v: получил dir=%q rest=%v", c.in, dir, rest)
		}
	}
	if _, _, err := globalDir([]string{"-C"}); err == nil {
		t.Error("-C без значения должен падать с ошибкой")
	}
}

func TestGlobalDirBeforeCommand(t *testing.T) {
	root := setup(t)
	out, err := exec.Command("go", "run", ".", "-C", root, "lint").CombinedOutput()
	if err != nil {
		t.Fatalf("-C перед командой отбит: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "доска и архив в порядке") {
		t.Fatalf("неожиданный вывод: %s", out)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftTypesListAndDump(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	out, err := cmdDraftTypes(root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Types []struct {
			Name string `json:"name"`
		} `json:"types"`
	}
	if err = json.Unmarshal([]byte(out), &v); err != nil || len(v.Types) != 4 {
		t.Fatalf("types: %s %v", out, err)
	}
	out, err = cmdDraftTypes(root, "milestone", false)
	if err != nil || !strings.Contains(out, `"due"`) {
		t.Fatalf("dump: %s %v", out, err)
	}
	p := filepath.Join(root, ".devkit", "draft-types", "task.toml")
	if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte("bad = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftTypes(root, "", false); err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("invalid: %v", err)
	}
}

package reviewaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamePythonFileDestination(t *testing.T) {
	for _, tc := range []struct {
		name, imports, body, actual, want string
	}{
		{"replace", "import os", "    try:\n        os.replace(temporary, path)\n    finally:\n        cleanup(temporary)", "path", ""},
		{"alias", "import os as fs", "    fs.replace(temporary, path)", "path", ""},
		{"wrong_import", "from os import os", "    os.replace(temporary, path)", "path", "os.replace"},
		{"unrelated_destination", "import os", "    os.replace(path, other)", "path", "фактический путь"},
		{"empty_destination", "import os", "    path = ''\n    os.replace(temporary, path)", "path", "пустой путь"},
		{"same_source_and_destination", "import os", "    os.replace(path, path)", "path", "совпадают"},
		{"class_raises", "import os", "    class Stop:\n        raise RuntimeError()\n    os.replace(temporary, path)", "path", "достижимый"},
		{"local_shadow", "import os", "    os = fake\n    os.replace(temporary, path)", "path", "os.replace"},
		{"module_shadow", "import os\nos = fake", "    os.replace(temporary, path)", "path", "os.replace"},
		{"shadowed_attribute", "import os\nos.replace = fake", "    os.replace(temporary, path)", "path", "os.replace"},
		{"through_alias", "import os\nfs = os\nfs.replace = fake", "    os.replace(temporary, path)", "path", "os.replace"},
		{"through_dictionary", "import os\nos.__dict__['replace'] = fake", "    os.replace(temporary, path)", "path", "os.replace"},
		{"unknown_branch", "import os", "    enabled = False\n    if enabled:\n        os.replace(temporary, path)", "path", "достижимый"},
		{"failed_assertion", "import os", "    assert False\n    os.replace(temporary, path)", "path", "достижимый"},
		{"negated_failed_assertion", "import os", "    assert not True\n    os.replace(temporary, path)", "path", "достижимый"},
		{"infinite_loop", "import os", "    while True:\n        pass\n    os.replace(temporary, path)", "path", "достижимый"},
		{"false_branch", "import os", "    if False:\n        os.replace(temporary, path)", "path", "достижимый"},
		{"uninvoked_function", "import os", "    def later():\n        os.replace(temporary, path)", "path", "достижимый"},
		{"after_return", "import os", "    return\n    os.replace(temporary, path)", "path", "достижимый"},
		{"after_finally_return", "import os", "    try:\n        prepare()\n    finally:\n        return\n    os.replace(temporary, path)", "path", "достижимый"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := tc.imports + "\ndef write_file(temporary, path, other):\n" + tc.body + "\n"
			root := t.TempDir()
			write(t, filepath.Join(root, "sample.py"), code)
			files, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			source := &evidenceSource{root: root, files: files, cache: map[string][]string{}, names: map[string]bool{"sample.py": true}}
			lines := strings.Split(code, "\n")
			var declaration, writeAt, call string
			for n, line := range lines {
				if strings.HasPrefix(line, "def write_file") {
					declaration = fmt.Sprintf("sample.py:%d", n+1)
				}
				if strings.Contains(line, ".replace(") {
					writeAt, call = fmt.Sprintf("sample.py:%d", n+1), strings.TrimSpace(line)
				}
			}
			word := "file"
			e := Evidence{
				Object: declaration + "#write_file", Check: "writes", Result: "checked",
				ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: tc.actual, ActualAt: writeAt},
				WriteEvidence: &WriteEvidence{Word: &word, Target: tc.actual, Value: call, WriteAt: writeAt,
					File: &NameFileWrite{Path: tc.actual, Role: "destination"}},
			}
			assertTargetCheck(t, checkNameEntry(e, declaration, identifierWords("write_file"), source), tc.want)
		})
	}
}

func TestNamePythonFileNamedDestination(t *testing.T) {
	const fixture = `import os
from pathlib import Path

def locate(home, relative):
    tail = relative[2:] if relative.startswith("~/") else relative
    return Path(home) / tail

def signal_tick(home, temporary):
    path = locate(home, "~/tick")
    try:
        prepare()
        os.replace(temporary, path)
    finally:
        cleanup(temporary)
`
	const decl, writeAt, bindingAt = "sample.py:8", "sample.py:12", "sample.py:9"
	for _, tc := range []struct{ name, code, quote, want string }{
		{"same_path", fixture, `path = locate(home, "~/tick")`, ""},
		{"wrong_literal", strings.Replace(fixture, "~/tick", "~/other", 1), `path = locate(home, "~/other")`, "путь не подтверждает объект"},
		{"unrelated_helper", strings.Replace(fixture, "Path(home) / tail", `Path(home) / "unrelated"`, 1), `path = locate(home, "~/tick")`, "путь не подтверждает объект"},
		{"rebound_helper", fixture + "locate = fake\n", `path = locate(home, "~/tick")`, "путь не подтверждает объект"},
		{"global_path", strings.Replace(fixture, "    path = locate", "    global path; path = locate", 1), `path = locate(home, "~/tick")`, "свяжите факт"},
		{"multiple_wrong_destinations", strings.Replace(strings.Replace(fixture, "~/tick", "~/other", 1), "        prepare()", `        path = "/tmp/unrelated"`, 1), `path = locate(home, "~/other")`, "свяжите факт"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "sample.py"), tc.code)
			files, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			source := &evidenceSource{root: root, files: files, cache: map[string][]string{}, names: map[string]bool{"sample.py": true}}
			word := "tick"
			e := Evidence{
				Object: decl + "#signal_tick", Check: "writes", Result: "checked",
				ObjectComparison: &NameObjectComparison{
					Expected: []string{"signal", "tick"}, Actual: "path", ActualAt: writeAt,
					Proof: &NameObjectProof{Expression: "path", Words: []NameProofWord{
						{Word: "signal", Role: "context", Fact: ReviewFact{Location: writeAt, Quote: "os.replace(temporary, path)", Value: "os.replace", Text: "Публикует новый сигнал."}},
						{Word: "tick", Role: "object", Fact: ReviewFact{Location: bindingAt, Quote: tc.quote, Value: "path", Text: "Имя назначения берётся из аргумента."}},
					}},
				},
				WriteEvidence: &WriteEvidence{Word: &word, Target: "path", Value: "os.replace(temporary, path)", WriteAt: writeAt,
					File: &NameFileWrite{Path: "path", Role: "destination"}},
			}
			assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("signal_tick"), source), tc.want)
			if tc.name == "wrong_literal" {
				e.ObjectComparison.Proof.Words[1].Role = "context"
				assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("signal_tick"), source), "путь не подтверждает объект")
				e.ObjectComparison.Proof = nil
				assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("signal_tick"), source), "путь не подтверждает объект")
			}
		})
	}
}

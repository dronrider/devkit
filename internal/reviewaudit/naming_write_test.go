package reviewaudit

import "testing"

func TestNameFileDestinationExpression(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "os"
import "path/filepath"
func TestFresh(dir string) {
 os.WriteFile(filepath.Join(dir, "agentctl"), []byte("script"), 0755)
}
`)
	raw := `{"check":"writes","result":"checked","object_comparison":{"expected":["Test","Fresh"],"actual":"filepath.Join(dir, \"agentctl\")","actual_at":"@os.WriteFile","proof":{"expression":"os.WriteFile(filepath.Join(dir, \"agentctl\"), []byte(\"script\"), 0755)","words":[{"word":"Test","role":"context","fact":{"location":"@os.WriteFile","quote":"os.WriteFile(filepath.Join(dir, \"agentctl\"), []byte(\"script\"), 0755)","value":"filepath.Join(dir, \"agentctl\")","text":"Путь записи."}},{"word":"Fresh","role":"context","fact":{"location":"@os.WriteFile","quote":"os.WriteFile(filepath.Join(dir, \"agentctl\"), []byte(\"script\"), 0755)","value":"filepath.Join(dir, \"agentctl\")","text":"Путь записи."}}]}},"write_evidence":{"word":"Fresh","target":"filepath.Join(dir, \"agentctl\")","value":"[]byte(\"script\")","write_at":"@os.WriteFile","file":{"path":"filepath.Join(dir, \"agentctl\")","role":"destination"}}}`
	e := namingWire(t, raw, at)
	assertTargetCheck(t, checkNameEntry(e, at("func TestFresh"), identifierWords("TestFresh"), source), "")
	e.ObjectComparison.Actual = `filepath.Join(dir, "other")`
	e.WriteEvidence.Target = e.ObjectComparison.Actual
	e.WriteEvidence.File.Path = e.ObjectComparison.Actual
	assertTargetCheck(t, checkNameEntry(e, at("func TestFresh"), identifierWords("TestFresh"), source), "фактический путь")
}

func TestNameFormattedFileDestination(t *testing.T) {
	for _, tc := range []struct {
		name, extra, output, want string
	}{
		{"format", "", `fmt.Fprintf(out, "%s", text)`, ""},
		{"unrelated_file", "", `fmt.Fprintf(other, "%s", text)`, "дескрипторе"},
		{"shadowed_formatter", "fmt := localFormatter{}", `fmt.Fprintf(out, "%s", text)`, "дескрипторе"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
import "fmt"
import "os"
func WriteFile(path, text string, other *os.File) {
 out, err := os.OpenFile(path, os.O_APPEND, 0600)
 if err != nil { return }
 `+tc.extra+`
 `+tc.output+`
}
`)
			openAt, writeAt := at("out, err :="), at(tc.output)
			word := "File"
			e := Evidence{
				Check: "writes", Result: "checked",
				ObjectComparison: &NameObjectComparison{Expected: []string{"File"}, Actual: "path", ActualAt: openAt},
				WriteEvidence: &WriteEvidence{Word: &word, Target: "path", Value: tc.output, WriteAt: writeAt,
					File: &NameFileWrite{Path: "path", Role: "destination"}},
			}
			assertTargetCheck(t, checkNameEntry(e, at("func WriteFile"), []string{"Write", "File"}, source), tc.want)
		})
	}
}

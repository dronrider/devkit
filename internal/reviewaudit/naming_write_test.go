package reviewaudit

import (
	"strings"
	"testing"
)

func TestNameFileDestinationExpression(t *testing.T) {
	const fixture = `package sample
import "os"
import "path/filepath"
func TestFresh(dir string) {
 os.WriteFile(filepath.Join(dir, "agentctl"), []byte("script"), 0755)
}
`
	source, at := nameProofFixture(t, fixture)
	raw := `{"check":"writes","result":"checked","object_comparison":{"expected":["Test","Fresh"],"actual":"filepath.Join(dir, \"agentctl\")","actual_at":"@os.WriteFile","proof":{"expression":"os.WriteFile(filepath.Join(dir, \"agentctl\"), []byte(\"script\"), 0755)","words":[{"word":"Test","role":"context","fact":{"location":"@os.WriteFile","quote":"os.WriteFile(filepath.Join(dir, \"agentctl\"), []byte(\"script\"), 0755)","value":"filepath.Join(dir, \"agentctl\")","text":"Путь записи."}},{"word":"Fresh","role":"context","fact":{"location":"@os.WriteFile","quote":"os.WriteFile(filepath.Join(dir, \"agentctl\"), []byte(\"script\"), 0755)","value":"filepath.Join(dir, \"agentctl\")","text":"Путь записи."}}]}},"write_evidence":{"word":"Fresh","target":"filepath.Join(dir, \"agentctl\")","value":"[]byte(\"script\")","write_at":"@os.WriteFile","file":{"path":"filepath.Join(dir, \"agentctl\")","role":"destination"}}}`
	e := namingWire(t, raw, at)
	assertTargetCheck(t, checkNameEntry(e, at("func TestFresh"), identifierWords("TestFresh"), source), "")
	e.ObjectComparison.Actual = `filepath.Join(dir, "other")`
	e.WriteEvidence.Target = e.ObjectComparison.Actual
	e.WriteEvidence.File.Path = e.ObjectComparison.Actual
	assertTargetCheck(t, checkNameEntry(e, at("func TestFresh"), identifierWords("TestFresh"), source), "фактический путь")
	source, at = nameProofFixture(t, strings.Replace(fixture, " os.WriteFile", " os := localWriter{}\n os.WriteFile", 1))
	e = namingWire(t, raw, at)
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
	for _, name := range []string{"log", "other"} {
		t.Run("literal_"+name, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
import "fmt"
import "os"
import "path/filepath"
func appendLog(dir, text string) {
 path := filepath.Join(dir, "`+name+`")
 out, err := os.OpenFile(path, os.O_APPEND, 0600)
 if err != nil { return }
 fmt.Fprintf(out, "%s", text)
}
`)
			const call = `fmt.Fprintf(out, "%s", text)`
			word := "Log"
			e := Evidence{
				Check: "writes", Result: "checked",
				ObjectComparison: &NameObjectComparison{Expected: []string{"append", word}, Actual: "path", ActualAt: at("path :="),
					Proof: &NameObjectProof{Expression: call, Words: []NameProofWord{
						{Word: "append", Role: "context", Fact: ReviewFact{Location: at("out, err :="), Quote: "os.OpenFile(path, os.O_APPEND, 0600)", Value: "os.O_APPEND", Text: "Файл открыт для добавления записи."}},
						{Word: word, Role: "object", Fact: ReviewFact{Location: at("path :="), Quote: `filepath.Join(dir, "` + name + `")`, Value: `"` + name + `"`, Text: "Последняя часть пути называет файл."}},
					}}},
				WriteEvidence: &WriteEvidence{Word: &word, Target: "path", Value: call, WriteAt: at(call),
					File: &NameFileWrite{Path: "path", Role: "destination"}},
			}
			want := ""
			if name != "log" {
				want = "не подтверждает"
			}
			assertTargetCheck(t, checkNameEntry(e, at("func appendLog"), []string{"append", "Log"}, source), want)
		})
	}

}

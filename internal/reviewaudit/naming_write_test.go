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

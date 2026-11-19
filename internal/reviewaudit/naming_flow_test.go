package reviewaudit

import (
	"go/parser"
	"os"
	"path/filepath"
	"testing"
)

func TestCreatedCollectionRequiresMatchingPlural(t *testing.T) {
	result, err := parser.ParseExpr("map[string][]*domain.File")
	if err != nil {
		t.Fatal(err)
	}
	if !createdResultMatches([]string{"Files"}, result) ||
		createdResultMatches([]string{"File"}, result) ||
		createdResultMatches([]string{"Licenses"}, result) {
		t.Fatal("ожидается только имя коллекции файлов")
	}
}

func TestNameResultAcrossForwarding(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "files.go"), `package sample
type File struct{}
func createLicense() map[string][]*File {
	files := map[string][]*File{}
	return files
}
func dispatch(kind int) map[string][]*File {
	switch kind {
	case 1:
		files := createLicense()
		return files
	case 2:
		files := createLicense()
		files = nil
		return files
	}
	return nil
}
func archive(files map[string][]*File) {}
func export() {
	files := dispatch(1)
	archive(files)
}
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	word := "License"
	flow := &NameResultFlow{Caller: "files.go:7", CallAt: "files.go:10", Value: "files",
		Consumer: "files.go:19", Forward: []NameResultHop{{ReturnAt: "files.go:11", Caller: "files.go:20", CallAt: "files.go:21", Value: "files"}}}
	e := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "files", ActualAt: "files.go:5"},
		ResultEvidence: &ResultEvidence{Word: &word, Returned: "files", ReturnAt: "files.go:5",
			Consumer: "archive(files)", ConsumerAt: "files.go:22", Flow: flow}}
	words := identifierWords("createLicense")
	assertTargetCheck(t, checkNameEntry(e, "files.go:3", words, source), "фактический тип результата \"map[string][]*File\"")
	e.Result = "violated"
	assertTargetCheck(t, checkNameEntry(e, "files.go:3", words, source), "")
	flow.CallAt, flow.Forward[0].ReturnAt = "files.go:13", "files.go:15"
	assertTargetCheck(t, checkNameEntry(e, "files.go:3", words, source), "должен возвращать то же значение")
}

func TestNameResultFromCompositeAndDirectReturn(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "flow.go"), `package sample
type Invoice struct{}
func createInvoice() (*Invoice, error) {
	return &Invoice{}, nil
}
func issue() (*Invoice, error) {
	return createInvoice()
}
func persist(invoice *Invoice) {}
func export() {
	invoice, _ := issue()
	persist(invoice)
}
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	word := "Invoice"
	flow := &NameResultFlow{Caller: "flow.go:6", CallAt: "flow.go:7", Consumer: "flow.go:9",
		Forward: []NameResultHop{{ReturnAt: "flow.go:7", Caller: "flow.go:10", CallAt: "flow.go:11", Value: "invoice"}}}
	entry := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "Invoice", ActualAt: "flow.go:4"},
		ResultEvidence:   &ResultEvidence{Word: &word, Returned: "Invoice", ReturnAt: "flow.go:4", Consumer: "persist", ConsumerAt: "flow.go:12", Flow: flow}}
	assertTargetCheck(t, checkNameEntry(entry, "flow.go:3", identifierWords("createInvoice"), source), "")
}

func TestNameResultFollowsAssignmentInBranch(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "flow.go"), `package sample
type Invoice struct{}
func createInvoice() *Invoice {
	invoice := &Invoice{}
	return invoice
}
func issue(enabled bool) *Invoice {
	var value *Invoice
	if enabled {
		value = createInvoice()
	} else {
		value := &Invoice{}
		value = createInvoice()
		_ = value
	}
	return value
}
func persist(invoice *Invoice) {}
func export() {
	value := issue(true)
	persist(value)
}
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	word := "Invoice"
	flow := &NameResultFlow{Caller: "flow.go:7", CallAt: "flow.go:10", Value: "value", Consumer: "flow.go:18",
		Forward: []NameResultHop{{ReturnAt: "flow.go:16", Caller: "flow.go:19", CallAt: "flow.go:20", Value: "value"}}}
	entry := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "invoice", ActualAt: "flow.go:5"},
		ResultEvidence:   &ResultEvidence{Word: &word, Returned: "invoice", ReturnAt: "flow.go:5", Consumer: "persist", ConsumerAt: "flow.go:21", Flow: flow}}
	words := identifierWords("createInvoice")
	assertTargetCheck(t, checkNameEntry(entry, "flow.go:3", words, source), "")
	flow.CallAt = "flow.go:13"
	assertTargetCheck(t, checkNameEntry(entry, "flow.go:3", words, source), "должен возвращать то же значение")
}

func TestNameResultChecksLocalUseAndSupplementalFacts(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "flow.go"), `package sample
type Feature struct { Enabled bool }
func lookupFeature() (*Feature, bool) {
	feature := &Feature{Enabled: true}
	return feature, true
}
func inspect() {
	value, present := lookupFeature()
	if !present { return }
	values := map[string]*Feature{}
	values["feature"] = value
	if value.Enabled { return }
	value.Enabled = false
	value = nil
	values["other"] = value
}
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	word := "Feature"
	e := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: "feature", ActualAt: "flow.go:5"},
		ResultEvidence: &ResultEvidence{
			Word: &word, Returned: "feature", ReturnAt: "flow.go:5", Consumer: "inspect uses value",
			Flow: &NameResultFlow{Caller: "flow.go:7", CallAt: "flow.go:8", Value: "value"},
			Facts: []ReviewFact{
				{Location: "flow.go:4", Quote: "Enabled: true", Text: "Создаётся включённая опция."},
				{Location: "flow.go:9", Quote: "if !present", Text: "Отсутствующее значение пропускается."},
			},
		}}
	words := identifierWords("lookupFeature")
	for _, tc := range []struct{ kind, field, at, want string }{
		{"assignment", "", "flow.go:11", ""},
		{"assignment", "", "flow.go:8", "должен однозначно указывать использование"},
		{"assignment", "", "flow.go:15", "должен однозначно указывать использование"},
		{"field_read", "Enabled", "flow.go:12", ""},
		{"field_read", "Enabled", "flow.go:13", "должен однозначно указывать использование"},
		{"field_read", "Other", "flow.go:12", "должен однозначно указывать использование"},
	} {
		t.Run(tc.kind+"/"+tc.at+"/"+tc.field, func(t *testing.T) {
			e.ResultEvidence.ConsumerAt = tc.at
			e.ResultEvidence.Flow.UseKind, e.ResultEvidence.Flow.UseField = tc.kind, tc.field
			assertTargetCheck(t, checkNameEntry(e, "flow.go:3", words, source), tc.want)
		})
	}
	e.ResultEvidence.ConsumerAt = "flow.go:11"
	e.ResultEvidence.Flow.UseKind, e.ResultEvidence.Flow.UseField = "assignment", ""
	e.ResultEvidence.Facts[1].Quote = "if present"
	assertTargetCheck(t, checkNameEntry(e, "flow.go:3", words, source), "result_evidence.facts[1]")
	e.ResultEvidence.Facts[1].Quote = "if !present"
	e.ObjectComparison.Expected = []string{"Other"}
	other := "Other"
	e.ResultEvidence.Word = &other
	assertTargetCheck(t, checkNameEntry(e, "flow.go:3", identifierWords("lookupOther"), source), "укажите equivalent_at")
}

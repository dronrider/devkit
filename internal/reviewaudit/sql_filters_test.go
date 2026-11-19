package reviewaudit

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSQLFilterLiteralsRequireEvidenceForEachOccurrence(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "query.sql"), `SELECT 'previous projection' FROM rights WHERE state = 'old';
-- operation
SELECT 'projection' FROM rights WHERE
    (state = 'active' OR backup_state = 'active') -- state = 'comment'
    AND deleted IS FALSE
    AND id IN ($1, $2)
    AND (tier IN ('trial', 'paid') OR priority BETWEEN 1 AND 5)
    AND owner IN (SELECT id FROM owners WHERE region = 'north')
    AND number = COALESCE($3, 0)
    ORDER BY id LIMIT 10;
SELECT date FROM rights WHERE state = 'next';
`)
	write(t, filepath.Join(root, "settings.py"), "ACTIVE = 'active'\nTRIAL = 'trial'\n")
	write(t, filepath.Join(root, "schema.sql"), "ALTER TABLE rights ADD CHECK (deleted IS FALSE);\n")
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	refs, err := queryLiteralRefs("query.sql:2", source)
	if err != nil {
		t.Fatal(err)
	}
	want := []sqlLiteralRef{
		{"query.sql:4", "'active'"},
		{"query.sql:4", "'active'"},
		{"query.sql:5", "FALSE"},
		{"query.sql:7", "'trial'"},
		{"query.sql:7", "'paid'"},
		{"query.sql:7", "1"},
		{"query.sql:7", "5"},
		{"query.sql:8", "'north'"},
	}
	if !slices.Equal(refs, want) {
		t.Fatalf("отбор литералов: получено %v, ожидалось %v", refs, want)
	}
	unchanged, changed := false, true
	entry := Evidence{Object: "query.sql:2", Location: "query.sql:2", Result: "checked", BindingChange: &unchanged}
	proposal := Evidence{Result: "not_applicable"}
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "query.sql:4: для литерала 'active' нужна отдельная запись")

	facts := []ReviewFact{
		{Location: "query.sql:4", Quote: "'active'", Related: "settings.py:1", Parameter: "status",
			SQLLiteral: &SQLFilterLiteral{Source: "constant", SourceQuote: "ACTIVE = 'active'"}},
	}
	entry.Facts = map[string][]ReviewFact{"filter": facts}
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "укажите parameters: violated")
	entry.Result, entry.BindingChange, entry.Kind = "violated", &changed, "collection"
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "query.sql:4: для литерала 'active' нужна отдельная запись")
	facts = append(facts, facts[0], ReviewFact{
		Location: "query.sql:5", Quote: "FALSE", Related: "schema.sql:1",
		SQLLiteral: &SQLFilterLiteral{Source: "fixed", SourceQuote: "deleted IS FALSE"},
	})
	for _, ref := range refs[3:] {
		facts = append(facts, ReviewFact{Location: ref.location, Quote: ref.quote, SQLLiteral: &SQLFilterLiteral{Source: "pending"}})
	}
	entry.Facts["filter"] = facts
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "при открытой проверке литерала нужен pending")
	proposal.Result = "pending"
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "")
	facts[0].Related = "settings.py:2"
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "related и source_quote должны подтвердить")
	facts[0].Related = "settings.py:1"

	write(t, filepath.Join(root, "fixed.sql"), "SELECT id FROM rights WHERE deleted IS FALSE;\n")
	source.names = nil
	entry.Object, entry.Location = "fixed.sql:1", "fixed.sql:1"
	entry.Result, entry.BindingChange = "checked", &unchanged
	entry.Facts["filter"] = []ReviewFact{{
		Location: "fixed.sql:1", Quote: "FALSE", Related: "schema.sql:1",
		SQLLiteral: &SQLFilterLiteral{Source: "fixed", SourceQuote: "deleted IS FALSE"},
	}}
	proposal.Result = "not_applicable"
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "")
	entry.Facts["filter"][0].Related = "task:1"
	source.taskText = "Схема разрешает выдавать записи только при deleted IS FALSE."
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "")
	assertTargetCheck(t, checkReviewFact("filter", ReviewFact{Location: "fixed.sql:1", Text: "Фиксирует условие по схеме.", Related: "task:1",
		SQLLiteral: entry.Facts["filter"][0].SQLLiteral}, source), "")
	entry.Facts["filter"][0].Related = "settings.py:1"
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "для fixed укажите строку схемы SQL или контракта")
	entry.Facts["filter"][0].Related = "base@fixed.sql:1"
	assertTargetCheck(t, checkBindingProposal(entry, proposal, source), "related должен ссылаться на источник значения за пределами")
}

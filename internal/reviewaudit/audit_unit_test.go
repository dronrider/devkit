package reviewaudit

import (
	"strings"
	"testing"
)

func TestReviewAuditUnitParserKeepsUntaggedProjectRules(t *testing.T) {
	if _, e := parseRules("common", "- Правило без ID", true); e == nil {
		t.Fatal("пропущено")
	}
	rr, e := parseRules("project", "# Слой\n\nПравило прозой.\n\n- Правило списком.\n  Продолжение.\n", false)
	if e != nil || len(rr) != 3 {
		t.Fatal(rr, e)
	}
}

func TestReviewAuditUnitProjectHeadingsAndRepeatedBlocksAreInventoried(t *testing.T) {
	rules, e := parseRules("project-style", "# Rules\n\n## Never log tokens\n\n- Keep atomic.\n\n## Other scope\n\n- Keep atomic.\n", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(rules) != 5 {
		t.Fatalf("blocks skipped: %+v", rules)
	}
	if rules[2].ID == rules[4].ID {
		t.Fatal("repeated block lost identity")
	}
}

func TestReviewAuditUnitProjectListDoesNotSwallowIndependentParagraph(t *testing.T) {
	rules, e := parseRules("project-style", "- First rule.\n\n  Indented explanation.\n\nIndependent requirement.\n\n1. Numbered rule.\n2. Another rule.\n+ Another bullet.\n", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(rules) != 5 {
		t.Fatalf("independent requirements merged: %+v", rules)
	}
	if !strings.Contains(rules[0].Text, "Indented explanation") || strings.Contains(rules[0].Text, "Independent requirement") {
		t.Fatalf("wrong list boundary: %+v", rules)
	}
}

package reviewaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nameReachabilityFixture = `package sample
func readValue() int { value := 3; return value }
func consume(int) {}
func use() {
 if CONDITION {
  consume(readValue())
  BODY
 }
}
`

func TestReviewNameReachabilityWithIfInitAndPackageShadow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition string
		sibling   string
		want      string
	}{
		{name: "false after initializer", condition: "n := 1; 1 == 0", want: "недостижимой ветке"},
		{name: "true after initializer", condition: "n := 1; 1 == 1"},
		{name: "condition reads initializer", condition: "n := 1; n == 1"},
		{name: "ordinary false", condition: "false", want: "недостижимой ветке"},
		{name: "predeclared name shadowed in sibling", condition: "false", sibling: "package sample\nconst false = true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := ""
			if strings.Contains(tc.condition, ";") {
				body = "_ = n"
			}
			code := strings.NewReplacer("CONDITION", tc.condition, "BODY", body).Replace(nameReachabilityFixture)
			source, at := nameProofFixture(t, code)
			if tc.sibling != "" {
				write(t, filepath.Join(source.root, "shadow.go"), tc.sibling)
			}
			assertNameReachability(t, source, at, tc.want)
		})
	}
}

func TestReviewNameReachabilityUsesSameRevision(t *testing.T) {
	code := strings.NewReplacer("CONDITION", "false", "BODY", "").Replace(nameReachabilityFixture)
	for _, shadowInBase := range []bool{true, false} {
		t.Run(map[bool]string{true: "base shadow", false: "head shadow"}[shadowInBase], func(t *testing.T) {
			source, at := nameProofFixture(t, code)
			write(t, filepath.Join(source.root, "pkg", "sample.go"), code)
			shadow := filepath.Join(source.root, "pkg", "shadow.go")
			if shadowInBase {
				write(t, shadow, "package sample\nconst false = true\n")
			}
			gitT(t, source.root, "add", ".")
			gitT(t, source.root, "commit", "-qm", "record package scope")
			source.base = gitT(t, source.root, "rev-parse", "HEAD")
			if shadowInBase {
				if err := os.Remove(shadow); err != nil {
					t.Fatal(err)
				}
				gitT(t, source.root, "add", "-u")
			} else {
				write(t, shadow, "package sample\nconst false = true\n")
				gitT(t, source.root, "add", "pkg/shadow.go")
			}
			gitT(t, source.root, "commit", "-qm", "change package scope")
			base, head := "", "недостижимой ветке"
			if !shadowInBase {
				base, head = head, base
			}
			baseAt := func(text string) string { return "base@pkg/" + at(text) }
			headAt := func(text string) string { return "pkg/" + at(text) }
			assertNameReachability(t, source, baseAt, base)
			assertNameReachability(t, source, headAt, head)
		})
	}
}

func assertNameReachability(t *testing.T, source *evidenceSource, at func(string) string, want string) {
	t.Helper()
	entry := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["Value"],"actual":"value","actual_at":"@func readValue"},"result_evidence":{"word":"Value","returned":"value","return_at":"@func readValue","consumer":"consume","consumer_at":"@consume(readValue()","flow":{"caller":"@func use","call_at":"@consume(readValue()","value":"","use_expression":"consume(readValue())"}}}`, at)
	assertTargetCheck(t, checkNameEntry(entry, at("func readValue"), identifierWords("readValue"), source), want)
}

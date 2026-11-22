package reviewaudit

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestNameAssignmentThroughAddressOfLocal(t *testing.T) {
	for _, test := range []struct {
		name, before, argument, writer, store, initial, between, condition, observed, want string
	}{
		{name: "same cross-file instance"},
		{name: "different argument", argument: "&other", want: "покажите выражение"},
		{name: "shadowed argument", argument: "&st", before: "if valid { st := store{}; _ = &st }", observed: "other.Value", want: "покажите выражение"},
		{name: "overwritten instance", between: "st = other", want: "покажите выражение"},
		{name: "overwritten field", between: `st.Value = "other"`, want: "покажите выражение"},
		{name: "field address escapes", between: "_ = &st.Value", want: "покажите выражение"},
		{name: "address escapes before call", before: "_ = &st", want: "покажите выражение"},
		{name: "pointer escapes inside writer", writer: "_ = st", want: "покажите выражение"},
		{name: "early successful return always taken", writer: `if st.Value == "" { return true, nil }`, want: "покажите выражение"},
		{name: "consumer accepts a failed write", condition: "valid", want: "покажите выражение"},
		{name: "unreachable when the condition initializer panics", condition: `panic("stop"); written`, want: "покажите выражение"},
		{name: "initial zero field stops writer", writer: `if st.Value == "" { return false, nil }`, want: "покажите выражение"},
		{name: "initial field permits writer", initial: `Value: "ready"`, writer: `if "" == st.Value { return false, nil }`},
		{name: "field overwritten in the same statement", store: `st.Value, (st.Value) = "written", "other"`, want: "присваивается повторно"},
		{name: "other object in parallel statement", writer: "other := &store{}", store: `st.Value, other.Value = "written", "other"`, want: "покажите выражение"},
		{name: "writer not reached", writer: "if true { return false, nil }", want: "покажите выражение"},
		{name: "unreachable read", condition: "false", want: "покажите выражение"},
		{name: "opposite return", condition: "!written", want: "покажите выражение"},
		{name: "no fallthrough", between: "if written { return \"other\" }", want: "покажите выражение"},
		{name: "overwritten flag", between: "written = false", want: "покажите выражение"},
		{name: "parenthesized flag", writer: "if blocked() { return false, nil }", between: "(written) = true", want: "покажите выражение"},
		{name: "parenthesized flag address", writer: "if blocked() { return false, nil }", between: "*(&(written)) = true", want: "покажите выражение"},
	} {
		t.Run(test.name, func(t *testing.T) {
			argument, condition, observed := test.argument, test.condition, test.observed
			writeLine := test.store
			if writeLine == "" {
				writeLine = `st.Value, st.Other = "written", ""`
			}
			if argument == "" {
				argument = "&st"
			}
			if condition == "" {
				condition = "valid && written"
			}
			if observed == "" {
				observed = "st.Value"
			}
			source, at := nameCurrentProofFixture(t, `package sample
 type server struct{}
 type store struct {
 Value string
 Other string
 }
 func blocked() bool { return true }
 func (s *server) write(st *store) (bool, error) {
 `+test.writer+`
 `+writeLine+`
 return true, nil
 }
 `)
			write(t, filepath.Join(source.root, "reader.go"), `package sample
 func (s *server) read(valid bool) string {
 st := store{`+test.initial+`}
 other := store{}
 _ = other
 `+test.before+`
 written, err := s.write(`+argument+`)
 if err != nil { return "" }
 if valid { st.Other, err = "different field", nil }
 `+test.between+`
 if `+condition+` { return `+observed+` }
 return ""
 }
 `)
			source.names["reader.go"] = true
			g := newQueryGoSource(source)
			fn, err := g.declaration(at("func (s *server) write"))
			if err != nil {
				t.Fatal(err)
			}
			readerAt := func(text string) string {
				body, err := source.read("reader.go", false)
				if err != nil {
					t.Fatal(err)
				}
				index := strings.Index(body, text)
				if index < 0 {
					t.Fatalf("no %q in reader", text)
				}
				return fmt.Sprintf("reader.go:%d", 1+strings.Count(body[:index], "\n"))
			}
			proof := &NameObjectProof{Expression: "st.Value", Assignment: &NameAssignmentWrite{
				Writes: []NameAssignmentSite{{At: at(writeLine), Target: "st.Value", Value: `"written"`}},
				Use:    NameCodeUse{Declaration: readerAt("func (s *server) read"), At: readerAt("{ return " + observed), Expression: observed},
			}}
			e := Evidence{ObjectComparison: &NameObjectComparison{Actual: "Value", ActualAt: at("Value string"), Proof: proof}, WriteEvidence: &WriteEvidence{WriteAt: at(writeLine)}}
			if err := json.Unmarshal(fmt.Appendf(nil, `{"pointer_call_at":%q}`, readerAt("written, err :=")), proof.Assignment); err != nil {
				t.Fatal(err)
			}
			assertTargetCheck(t, g.checkNameAssignment(e, fn, fn), test.want)
		})
	}
}

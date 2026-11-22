package reviewaudit

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestNameAssignmentReadsCacheOnNextCall(t *testing.T) {
	for _, tc := range []struct {
		name, receiver, params, arguments, read, guard, before, store, mutex, returned, want string
	}{
		{name: "same cache on same receiver"},
		{name: "different entry receiver", receiver: "other", want: "покажите выражение"},
		{name: "different read receiver", read: "other.harn", want: "покажите выражение"},
		{name: "wrong mutex", mutex: "backup", want: "покажите выражение"},
		{name: "field overwritten", before: "s.harn = nil", want: "покажите выражение"},
		{name: "pointer aliases receiver", before: "_ = s", want: "покажите выражение"},
		{name: "unreachable cache hit", guard: "v != nil && false", want: "покажите выражение"},
		{name: "cache read before impossible guard", guard: "v != nil && v == nil", want: "покажите выражение"},
		{name: "cache guard symmetric contradiction", guard: "v != nil && nil == v", want: "покажите выражение"},
		{name: "entry prevents store and read", params: "valid bool", arguments: "false", guard: "v != nil && valid", store: "valid", want: "покажите выражение"},
		{name: "unreachable cache refresh", store: "false", want: "покажите выражение"},
		{name: "cached value ignored", returned: "view{}", want: "покажите выражение"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defaults := map[*string]string{&tc.receiver: "s", &tc.read: "s.harn", &tc.guard: "v != nil && true", &tc.store: "len(fresh.values) > 0", &tc.mutex: "mu", &tc.returned: "*v"}
			for expression, fallback := range defaults {
				if *expression == "" {
					*expression = fallback
				}
			}
			source, at := nameCurrentProofFixture(t, `package sample
import "sync"
type view struct { values []string }
type server struct {
 mu, backup sync.Mutex
 harn *view
}
func (s *server) cached(`+tc.params+`) view {
 other := &server{}
 _ = other
 s.mu.Lock()
 v := `+tc.read+`
 s.mu.Unlock()
 if `+tc.guard+` {
  return `+tc.returned+`
 }
 fresh := view{values: []string{"record"}}
 `+tc.before+`
 if `+tc.store+` {
  s.`+tc.mutex+`.Lock()
  s.harn = &fresh
  s.`+tc.mutex+`.Unlock()
 }
 return fresh
}
func (s *server) choose(other *server) {
 for range `+tc.receiver+`.cached(`+tc.arguments+`).values {}
}
`)
			g := newQueryGoSource(source)
			writer, err := g.declaration(at("func (s *server) cached"))
			if err != nil {
				t.Fatal(err)
			}
			entry, err := g.declaration(at("func (s *server) choose"))
			if err != nil {
				t.Fatal(err)
			}
			writeAt := at("s.harn = &fresh")
			proof := &NameObjectProof{Expression: "s.harn", Assignment: &NameAssignmentWrite{
				Writes: []NameAssignmentSite{{At: writeAt, Target: "s.harn", Value: "&fresh"}},
				Use:    NameCodeUse{Declaration: at("func (s *server) cached"), At: at("v := "), Expression: tc.read},
			}}
			if err := json.Unmarshal(fmt.Appendf(nil, `{"next_call_return_at":%q}`, at("return "+tc.returned)), proof.Assignment); err != nil {
				t.Fatal(err)
			}
			e := Evidence{
				ObjectComparison: &NameObjectComparison{Actual: "harn", ActualAt: at("harn *view"), Proof: proof},
				WriteEvidence:    &WriteEvidence{WriteAt: writeAt, Calls: []NameWriteCall{{At: at("for range ")}}},
			}
			assertTargetCheck(t, g.checkNameAssignment(e, entry, writer), tc.want)
		})
	}
}

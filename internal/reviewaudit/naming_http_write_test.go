package reviewaudit

import "testing"

func TestNameHTTPJSONResponseWritesStatusAndPayload(t *testing.T) {
	for _, tc := range []struct {
		name, before, encoder, statusWriter, additional, want string
	}{
		{name: "same response"},
		{name: "different response body", encoder: "other", want: "запись статуса и JSON"},
		{name: "different response status", statusWriter: "other", want: "запись статуса и JSON"},
		{name: "response replaced", before: "w = other", want: "на том же http.ResponseWriter"},
		{name: "unreachable call", before: "return", want: "на том же http.ResponseWriter"},
		{name: "different payload on another branch", additional: `if condition { writeJSON(w, http.StatusConflict, map[string]string{"not_error": "ignored"}); return }`, want: "записывает другой HTTP-ответ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.encoder == "" {
				tc.encoder = "w"
			}
			if tc.statusWriter == "" {
				tc.statusWriter = "w"
			}
			encode := "json.NewEncoder(" + tc.encoder + ").Encode(v)"
			source, at := nameCurrentProofFixture(t, `package sample
import "encoding/json"
import "net/http"
var other http.ResponseWriter
func writeJSON(w http.ResponseWriter, code int, v any) {
 w.Header().Set("Content-Type", "application/json; charset=utf-8")
 `+tc.statusWriter+`.WriteHeader(code)
 `+encode+`
}
func writeQueueError(w http.ResponseWriter, condition bool) {
 `+tc.before+`
 `+tc.additional+`
 writeJSON(w, http.StatusBadGateway, map[string]string{"error": "missing"})
}
`)
			g := newQueryGoSource(source)
			entry, err := g.declaration(at("func writeQueueError"))
			if err != nil {
				t.Fatal(err)
			}
			callAt := at("writeJSON(w, http.StatusBadGateway")
			e := Evidence{
				ObjectComparison: &NameObjectComparison{Actual: "error", ActualAt: callAt, Proof: &NameObjectProof{Expression: encode, Effect: &NameSystemWrite{Kind: "http_json"}}},
				WriteEvidence: &WriteEvidence{
					Target: "w", Value: "v", WriteAt: at(encode), Calls: []NameWriteCall{{At: callAt, Declaration: at("func writeJSON"), Bindings: []NameCallBinding{
						{"w", "w"}, {"code", "http.StatusBadGateway"}, {"v", `map[string]string{"error": "missing"}`},
					}}},
				},
			}
			assertTargetCheck(t, g.checkNameWriteProof(e, entry), tc.want)
		})
	}
}

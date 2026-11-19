package reviewaudit

import "testing"

func TestNameSQLConstantScope(t *testing.T) {
	for _, tc := range []struct {
		name, param, setup, argument, statement, want string
	}{
		{"constant", "", "", "query", "UPDATE records SET flag = ? WHERE id = ?", ""},
		{"literal", "", "", `"UPDATE records SET flag = ? WHERE id = ?"`, "UPDATE records SET flag = ? WHERE id = ?", ""},
		{"wrong_sql", "", "", "query", "DELETE FROM records WHERE id = ?", "текст должен совпадать"},
		{"local_variable", "", `query := "DELETE FROM records"`, "query", "UPDATE records SET flag = ? WHERE id = ?", "затенение"},
		{"parameter", ", query string", "", "query", "UPDATE records SET flag = ? WHERE id = ?", "затенение"},
		{"local_constant", "", `const query = "DELETE FROM records WHERE flag = ? AND id = ?"`, "query", "DELETE FROM records WHERE flag = ? AND id = ?", ""},
		{"shadow_constant_mismatch", "", `const query = "DELETE FROM records WHERE flag = ? AND id = ?"`, "query", "UPDATE records SET flag = ? WHERE id = ?", "текст должен совпадать"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := "db.Exec(" + tc.argument + ", flag, id)"
			source, at := nameProofFixture(t, "package sample\nconst query = \"UPDATE records SET flag = ? WHERE id = ?\"\nfunc updateRecords(db DB, flag, id int"+tc.param+") {\n"+tc.setup+"\n "+call+"\n}\n")
			e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["Records"],"actual":"records","actual_at":"@const query","proof":{"expression":`+strconvQuote(call)+`,"words":[{"word":"Records","role":"object","fact":{"location":"@db.Exec","quote":`+strconvQuote(call)+`,"value":"id","text":"Записывается выбранная запись."}}],"sql":{"table":"records","statement":`+strconvQuote(tc.statement)+`,"arguments":["flag","id"]}}},"write_evidence":{"word":"Records","target":"records","value":"flag","write_at":"@db.Exec"}}`, at)
			assertTargetCheck(t, checkNameEntry(e, at("func updateRecords"), identifierWords("updateRecords"), source), tc.want)
			if tc.want == "" {
				e.ObjectComparison.Proof.SQL.Arguments = []string{"id", "flag"}
				assertTargetCheck(t, checkNameEntry(e, at("func updateRecords"), identifierWords("updateRecords"), source), "аргумент отличается")
			}
		})
	}
}

func TestNamePredicateConditionAndConsumer(t *testing.T) {
	for _, tc := range []struct{ name, body, returned, fact, want string }{
		{"if", "if usesField(input, other) {}", "true", "input.Field", ""},
		{"for", "for usesField(input, other) { break }", "true", "input.Field", ""},
		{"switch", "switch usesField(input, other) { case true: }", "true", "input.Field", ""},
		{"false_branch", "if usesField(input, other) {}", "false", "input.Field", ""},
		{"discarded", "usesField(input, other)", "true", "input.Field", "use_expression"},
		{"unrelated_fact", "if usesField(input, other) {}", "true", "other.Field", "не связано"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, "package sample\ntype Config struct { Field bool }\nfunc usesField(input, other Config) bool {\n if other.Field {}\n if input.Field {\n  return true\n } else {\n  return false\n }\n}\nfunc consume(input, other Config) {\n "+tc.body+"\n}\n")
			fact := `{"location":"@if ` + tc.fact + `","quote":"if ` + tc.fact + `","value":"` + tc.fact + `","text":"Условие возврата признака."}`
			e := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["uses","Field"],"actual":"`+tc.returned+`","actual_at":"@return `+tc.returned+`","proof":{"expression":"`+tc.returned+`","type":"bool","words":[{"word":"uses","role":"condition","fact":`+fact+`},{"word":"Field","role":"object","fact":`+fact+`}]}},"result_evidence":{"word":"Field","returned":"`+tc.returned+`","return_at":"@return `+tc.returned+`","consumer":"условие","consumer_at":"@`+tc.body+`","flow":{"caller":"@func consume","call_at":"@`+tc.body+`","value":"","use_expression":"usesField(input, other)"}}}`, at)
			assertTargetCheck(t, checkNameEntry(e, at("func usesField"), identifierWords("usesField"), source), tc.want)
		})
	}
}

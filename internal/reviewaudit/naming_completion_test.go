package reviewaudit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameCompletionTemporaryWrites(t *testing.T) {
	for _, tc := range []struct {
		body     string
		rejected bool
	}{
		{`os.WriteFile("fixture", nil, 0600)`, true},
		{`helper()`, true},
		{`t.Run("case", func(t T) { helper() })`, true},
		{`value := 1; _ = value`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			source, at := nameProofFixture(t, "package sample\nimport \"os\"\nfunc TestScenario() {\n"+tc.body+"\n}\nfunc helper() { os.WriteFile(\"fixture\", nil, 0600) }\n")
			e := Evidence{Check: "writes", Result: "not_applicable", Location: at("func TestScenario")}
			err := checkNameEntry(e, e.Location, identifierWords("TestScenario"), source)
			if tc.rejected {
				assertTargetCheck(t, err, "not_applicable запрещён")
			} else {
				assertTargetCheck(t, err, "")
			}
		})
	}
}

func TestNameCompletionOpenFileWrite(t *testing.T) {
	const open = "f, err := fs.OpenFile(path, fs.O_WRONLY|fs.O_APPEND, 0600)"
	for _, tc := range []struct {
		name, beforeOpen, beforeWrite, write, value, target, want string
	}{
		{name: "append in loop", write: "f.Write(line)", value: "line", target: "path"},
		{name: "another file", write: "f.Write(line)", value: "line", target: "other", want: "фактический путь"},
		{name: "another argument", write: "f.Write(line)", value: "otherLine", target: "path", want: "фактический аргумент"},
		{name: "another receiver", write: "buffer.Write(line)", value: "line", target: "path", want: "файловом дескрипторе"},
		{name: "rebound receiver", beforeWrite: "f = another\n", write: "f.Write(line)", value: "line", target: "path", want: "файловом дескрипторе"},
		{name: "shadowed package", beforeOpen: "fs := opener\n", write: "f.Write(line)", value: "line", target: "path", want: "файловом дескрипторе"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := "package sample\nimport fs \"os\"\nfunc appendLogFile(path, other string, lines [][]byte) error {\n" +
				tc.beforeOpen + open + "\nif err != nil { return err }\ndefer f.Close()\nfor _, line := range lines {\n" +
				tc.beforeWrite + "if _, err := " + tc.write + "; err != nil { return err }\n}\nreturn nil\n}\n"
			source, at := nameProofFixture(t, code)
			decl := at("func appendLogFile")
			word := "File"
			e := Evidence{
				Object: decl + "#appendLogFile", Check: "writes", Result: "checked", Location: decl,
				ObjectComparison: &NameObjectComparison{Expected: []string{"append", "Log", "File"}, Actual: tc.target, ActualAt: decl},
				WriteEvidence: &WriteEvidence{Word: &word, Target: tc.target, Value: tc.value, WriteAt: at(tc.write),
					File: &NameFileWrite{Path: tc.target, Role: "destination"}},
			}
			assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("appendLogFile"), source), tc.want)
			if tc.want == "" {
				e.Result = "not_applicable"
				assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("appendLogFile"), source), "достижимая запись Write")
			}
		})
	}
}

func TestNameCompletionUseAfterBranch(t *testing.T) {
	for _, tc := range []struct {
		body, expression string
		good             bool
	}{
		{"return value", "value", true},
		{"for _, id := range ids { consume(value[id]) }", "value[id]", true},
		{"value = other; return value", "value", false},
		{"for _, id := range ids { value := other; consume(value[id]) }", "value[id]", false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			source, at := nameProofFixture(t, "package sample\nfunc getValue() map[int]int { value := make(map[int]int); return (value) }\nfunc use() map[int]int {\n var value map[int]int\n if enabled {\n value = getValue()\n }\n "+tc.body+"\n}\n")
			e := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["Value"],"actual":"value","actual_at":"@func getValue"},"result_evidence":{"word":"Value","returned":"value","return_at":"@func getValue","consumer":"use","consumer_at":"@`+tc.body+`","flow":{"caller":"@func use","call_at":"@value = getValue()","value":"value","use_expression":`+strconvQuote(tc.expression)+`}}}`, at)
			err := checkNameEntry(e, at("func getValue"), identifierWords("getValue"), source)
			if tc.good {
				assertTargetCheck(t, err, "")
			} else {
				assertTargetCheck(t, err, "use_expression")
			}
		})
	}
}

func TestNameCompletionInterface(t *testing.T) {
	for _, signature := range []string{"int", "string"} {
		t.Run(signature, func(t *testing.T) {
			source, at := nameProofFixture(t, "package sample\ntype Getter interface {\n getValue() "+signature+"\n}\ntype Store struct{}\nfunc (s Store) getValue() int { value := 1; return value }\nfunc use(s Store) int { value := s.getValue(); return value }\n")
			e := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["Value"],"actual":"value","actual_at":"@func (s Store)"},"result_evidence":{"word":"Value","returned":"value","return_at":"@func (s Store)","implementation":"@func (s Store)","consumer":"use","consumer_at":"@func use","flow":{"caller":"@func use","call_at":"@func use","value":"value","use_expression":"value"}}}`, at)
			err := checkNameEntry(e, at(" getValue()"), identifierWords("getValue"), source)
			if signature == "int" {
				assertTargetCheck(t, err, "")
			} else {
				assertTargetCheck(t, err, "сигнатура")
			}
		})
	}
}

func TestNameCompletionGuard(t *testing.T) {
	for _, tc := range []struct {
		assertion, input string
		good             bool
	}{
		{"require.True(t, token.Valid)", "raw", true},
		{"assert.True(t, token.Valid)", "raw", false},
		{"require.True(t, token.Valid)", "other", false},
		{"if enabled { require.True(t, token.Valid) }", "raw", false},
		{"require.True(t, token.Valid); raw = other", "raw", false},
	} {
		t.Run(tc.assertion+tc.input, func(t *testing.T) {
			source, at := nameProofFixture(t, `package sample
import "github.com/stretchr/testify/require"
import "github.com/stretchr/testify/assert"
func verifiedClaims(raw, other []byte) map[string]any {
 token := parse(`+tc.input+`)
 `+tc.assertion+`
 decoder := decode(raw)
 var claims map[string]any
 decoder.Decode(&claims)
 return claims
}
`)
			expr := "require.True(t, token.Valid)"
			if strings.HasPrefix(tc.assertion, "assert") {
				expr = "assert.True(t, token.Valid)"
			}
			e := namingWire(t, `{"check":"result","result":"checked","object_comparison":{"expected":["verified","Claims"],"actual":"claims","actual_at":"@return claims","proof":{"expression":"claims","type":"map[string]any","guards":[{"at":"@`+tc.assertion+`","expression":`+strconvQuote(expr)+`}],"words":[{"word":"verified","role":"condition","fact":{"location":"@`+tc.assertion+`","quote":`+strconvQuote(tc.assertion)+`,"value":"token.Valid","text":"Проверен тот же вход."}},{"word":"Claims","role":"object","fact":{"location":"@decoder.Decode","quote":"decoder.Decode(&claims)","value":"&claims","text":"Заполнен результат."}}]}},"result_evidence":{"word":"Claims","returned":"claims","return_at":"@return claims","consumer":"use","consumer_at":"@func verifiedClaims"}}`, at)
			err := checkNameEntry(e, at("func verifiedClaims"), identifierWords("verifiedClaims"), source)
			if tc.good {
				assertTargetCheck(t, err, "")
			} else {
				assertTargetCheck(t, err, "не связано")
			}
		})
	}
}

func TestNameCompletionContentFormat(t *testing.T) {
	source, at := nameProofFixture(t, `package sample
import "os"
import "encoding/pem"
import "crypto/x509"
func writeRSAKey(path string, key *rsa.PrivateKey) {
 block := &pem.Block{Bytes: x509.MarshalPKCS1PrivateKey(key)}
 os.WriteFile(path, pem.EncodeToMemory(block), 0600)
}
`)
	e := namingWire(t, `{"check":"writes","result":"checked","object_comparison":{"expected":["RSA","Key"],"actual":"key","actual_at":"@block :=","proof":{"expression":"key","words":[{"word":"RSA","role":"format","fact":{"location":"@block :=","quote":"x509.MarshalPKCS1PrivateKey(key)","value":"key","text":"RSA в PKCS1."}},{"word":"Key","role":"object","fact":{"location":"@block :=","quote":"x509.MarshalPKCS1PrivateKey(key)","value":"key","text":"Записывается ключ."}}]}},"write_evidence":{"word":"Key","target":"path","value":"pem.EncodeToMemory(block)","write_at":"@os.WriteFile","file":{"role":"content","path":"path","payload_at":"@block :=","encode_at":"@os.WriteFile"}}}`, at)
	assertTargetCheck(t, checkNameEntry(e, at("func writeRSAKey"), identifierWords("writeRSAKey"), source), "")
	e.ObjectComparison.Actual = "path"
	e.ObjectComparison.ActualAt = at("os.WriteFile")
	assertTargetCheck(t, checkNameEntry(e, at("func writeRSAKey"), identifierWords("writeRSAKey"), source), "проследите значение actual")
}

func TestNameCompletionOtherPackageIsNotACallee(t *testing.T) {
	source, at := nameProofFixture(t, "package sample\nfunc TestScenario() { helper() }\n")
	write(t, filepath.Join(source.root, "external_test.go"), "package sample_test\nimport \"os\"\nfunc helper() { os.WriteFile(\"fixture\", nil, 0600) }\n")
	e := Evidence{Check: "writes", Result: "not_applicable", Location: at("func TestScenario")}
	assertTargetCheck(t, checkNameEntry(e, e.Location, identifierWords("TestScenario"), source), "")
}

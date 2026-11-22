package reviewaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamePythonResult(t *testing.T) {
	const code = `import json

def read_file(name):
    with open(name, encoding="utf-8") as stream:
        raw = stream.read(65537)
    if len(raw) > 65536:
        raise ValueError("too large")
    return json.loads(raw)

def read(name):
    data = read_file(name)
    if not isinstance(data, dict) or data.get("version") != 1:
        raise ValueError("invalid")
    return data
`
	for _, tc := range []struct {
		name, old, new, use, want string
	}{
		{name: "decoded file consumed"},
		{name: "returned value differs", old: "return json.loads(raw)", new: "return json.loads(other)", want: "точное выражение возврата"},
		{name: "later return unreachable", old: "if len(raw) > 65536:", new: "if True:", want: "достижимый возврат"},
		{name: "different producer", old: "data = read_file(name)", new: "data = call_file(name)", want: "прямое присваивание"},
		{name: "rebound producer", old: "def read(name):", new: "read_file = replacement\ndef read(name):", want: "устойчивое объявление"},
		{name: "other consumer", old: "isinstance(data, dict)", new: "isinstance(other, dict)", use: "isinstance(other, dict)", want: "чтение полученного значения"},
		{name: "short circuit", old: "not isinstance(data, dict) or", new: "True or isinstance(data, dict) or", want: "чтение полученного значения"},
		{name: "same-line reassignment", old: "not isinstance(data, dict) or", new: "check((data := other), isinstance(data, dict)) or", want: "переприсваивать результат"},
		{name: "intervening assignment", old: "    if not isinstance", new: "    data = other\n    if not isinstance", want: "чтение сразу после присваивания"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sample := code
			if tc.old != "" {
				sample = strings.Replace(sample, tc.old, tc.new, 1)
			}
			root := t.TempDir()
			const file = "sample.py"
			write(t, filepath.Join(root, file), sample)
			files, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			source := &evidenceSource{root: root, files: files, cache: map[string][]string{}, names: map[string]bool{file: true}}
			at := func(fragment string) (string, string) {
				t.Helper()
				for n, line := range strings.Split(sample, "\n") {
					if strings.Contains(line, fragment) {
						return fmt.Sprintf("%s:%d", file, n+1), strings.TrimSpace(line)
					}
				}
				t.Fatalf("no line matching %q", fragment)
				return "", ""
			}
			declaration, _ := at("def read_file")
			ret, quote := at("return json.loads")
			caller, _ := at("def read(")
			call, _ := at("data = ")
			consumer, _ := at("isinstance(")
			use := tc.use
			if use == "" {
				use = "isinstance(data, dict)"
			}
			word := "file"
			fact := ReviewFact{Location: ret, Quote: quote, Value: "raw", Text: "Read text feeds JSON decoding."}
			if tc.name == "returned value differs" {
				fact.Value = "other"
			}
			evidence := Evidence{
				Object: declaration + "#read_file", Check: "result", Result: "checked", Location: declaration,
				ObjectComparison: &NameObjectComparison{
					Expected: []string{word}, Actual: "loads", ActualAt: ret,
					Proof: &NameObjectProof{
						Expression: "json.loads(raw)",
						Words:      []NameProofWord{{Word: word, Role: "source", Fact: fact}},
					},
				},
				ResultEvidence: &ResultEvidence{
					Word: &word, Returned: "decoded object from json.loads", ReturnAt: ret,
					Consumer: "The caller checks the returned document.", ConsumerAt: consumer,
					Flow: &NameResultFlow{Caller: caller, CallAt: call, Value: "data", UseExpression: use},
				},
			}
			assertTargetCheck(t, checkNameEntry(evidence, declaration, identifierWords("read_file"), source), tc.want)
		})
	}
}

func TestNamePythonResultFlow(t *testing.T) {
	const producer = `def produce_value(item):
    return item
`
	const condition = `def consume(item):
    if not produce_value(item):
        raise ValueError()
`
	const filter = `def consume(items):
    import producer as origin
    matches = [item for item in items if origin.produce_value(item)]
    return matches
`
	const assigned = `def consume(items, skipped):
    import producer as origin
    roots = origin.produce_value(items) - skipped
    for root in sorted(roots):
        report(root)
`
	const entry = `import sys

def produce_value():
    return 0

if __name__ == "__main__":
    sys.exit(produce_value())
`
	for _, tc := range []struct {
		name, body, replace, with, declaration, call, use, value, want string
		producerCode, result                                           string
		crossFile                                                      bool
	}{
		{name: "direct condition", body: condition, declaration: "def consume(", call: "if not produce_value", use: "not produce_value(item)"},

		{name: "generator result is not the return", body: condition, declaration: "def consume(", call: "if not produce_value", use: "not produce_value(item)",
			producerCode: "def produce_value(item):\n    yield False\n    return item\n", want: "yield"},
		{name: "comprehension shadows the result", body: "def consume(item):\n    value = produce_value(item)\n    found = [entry for value in (True,) if bool(value)]\n    return found\n",
			declaration: "def consume(", call: "value = produce_value", use: "bool(value)", value: "value", want: "чтение полученного значения"},
		{name: "constant argument excludes the return", body: "def consume(item):\n    if not produce_value(False):\n        raise ValueError()\n",
			declaration: "def consume(", call: "if not produce_value", use: "not produce_value(False)",
			producerCode: "def produce_value(item):\n    if item:\n        return item\n    return False\n", want: "фактические аргументы"},
		{name: "boolean result", body: condition, declaration: "def consume(", call: "if not produce_value", use: "not produce_value(item)",
			producerCode: "def produce_value(item):\n    return item or True\n", result: "item or True"},
		{name: "unreachable use", body: condition, replace: "    if not produce_value", with: "    return\n    if not produce_value",
			declaration: "def consume(", call: "if not produce_value", use: "not produce_value(item)", want: "результат которого используется"},
		{name: "dead producer return", body: condition, declaration: "def consume(", call: "if not produce_value",
			producerCode: "def produce_value(item):\n    if 1 < 2:\n        raise ValueError()\n    return item\n",
			use:          "not produce_value(item)", want: "достижимый возврат"},
		{name: "cross module filter", body: filter, crossFile: true, declaration: "def consume(", call: "origin.produce_value", use: "origin.produce_value(item)"},
		{name: "wrong import", body: filter, crossFile: true, replace: "import producer", with: "import impostor",
			declaration: "def consume(", call: "origin.produce_value", use: "origin.produce_value(item)", want: "вызов проверяемой функции"},
		{name: "conditional import", body: filter, crossFile: true, replace: "    import producer", with: "    if False:\n        import producer",
			declaration: "def consume(", call: "origin.produce_value", use: "origin.produce_value(item)", want: "вызов проверяемой функции"},
		{name: "rebound module member", body: filter, crossFile: true, replace: "    matches =", with: "    origin.produce_value = replacement\n    matches =",
			declaration: "def consume(", call: "matches =", use: "origin.produce_value(item)", want: "вызов проверяемой функции"},
		{name: "module member changed through another import", body: filter, crossFile: true, replace: "def consume", with: "def patch():\n    import producer as patched\n    patched.produce_value = replacement\ndef consume",
			declaration: "def consume(", call: "matches =", use: "origin.produce_value(item)", want: "вызов проверяемой функции"},
		{name: "empty filter", body: filter, crossFile: true, replace: "item in items", with: "item in []",
			declaration: "def consume(", call: "origin.produce_value", use: "origin.produce_value(item)", want: "результат которого используется"},
		{name: "unused direct call", body: condition, replace: "    if not produce_value(item):\n        raise ValueError()", with: "    produce_value(item)",
			declaration: "def consume(", call: "    produce_value(item)", use: "produce_value(item)", want: "результат которого используется"},
		{name: "set difference reaches iteration", body: assigned, crossFile: true, declaration: "def consume(", call: "roots =", use: "sorted(roots)", value: "roots"},
		{name: "iteration reads another value", body: assigned, crossFile: true, replace: "sorted(roots)", with: "sorted(other)",
			declaration: "def consume(", call: "roots =", use: "sorted(other)", value: "roots", want: "чтение полученного значения"},
		{name: "entrypoint receives status", body: entry, declaration: "if __name__", call: "sys.exit(", use: "sys.exit(produce_value())"},
		{name: "status routed elsewhere", body: entry, replace: "sys.exit(", with: "sys.stderr.write(",
			declaration: "if __name__", call: "sys.stderr.write(", use: "sys.stderr.write(produce_value())", want: "результат которого используется"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(tc.body, tc.replace, tc.with, 1)
			producerCode := tc.producerCode
			if producerCode == "" {
				producerCode = producer
			}
			const producerFile = "producer.py"
			callerFile := producerFile
			if tc.crossFile {
				callerFile = "consumer.py"
			} else {
				if strings.Contains(body, "def produce_value(") {
					producerCode = body
				} else {
					producerCode += body
				}
				body = producerCode
			}
			root := t.TempDir()
			names := map[string]bool{producerFile: true, callerFile: true}
			write(t, filepath.Join(root, producerFile), producerCode)
			if tc.crossFile {
				write(t, filepath.Join(root, callerFile), body)
			}
			files, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			source := &evidenceSource{root: root, files: files, cache: map[string][]string{}, names: names}
			at := func(file, code, fragment string) string {
				t.Helper()
				for n, line := range strings.Split(code, "\n") {
					if strings.Contains(line, fragment) {
						return fmt.Sprintf("%s:%d", file, n+1)
					}
				}
				t.Fatalf("no line matching %q", fragment)
				return ""
			}
			result := tc.result
			switch {
			case strings.Contains(producerCode, "return 0"):
				result = "0"
			case result == "":
				result = "item"
			}
			declaration := at(producerFile, producerCode, "def produce_value")
			ret := at(producerFile, producerCode, "return "+result)
			caller := at(callerFile, body, tc.declaration)
			call := at(callerFile, body, tc.call)
			use := at(callerFile, body, tc.use)
			word := "value"
			evidence := Evidence{
				Object: declaration + "#produce_value", Check: "result", Result: "checked", Location: declaration,
				ObjectComparison: &NameObjectComparison{
					Expected: []string{"produce", word}, Actual: result, ActualAt: ret,
					Proof: &NameObjectProof{
						Expression: result,
						Words: []NameProofWord{
							{Word: "produce", Role: "context", Fact: ReviewFact{Location: ret, Quote: "return " + result, Value: result, Text: "The function returns this value."}},
							{Word: word, Role: "object", Fact: ReviewFact{Location: ret, Quote: "return " + result, Value: result, Text: "The expression supplies the result."}},
						},
					},
				},
				ResultEvidence: &ResultEvidence{
					Word: &word, Returned: "the value", ReturnAt: ret, Consumer: "The caller reads the result.", ConsumerAt: use,
					Flow: &NameResultFlow{Caller: caller, CallAt: call, Value: tc.value, UseExpression: tc.use},
				},
			}
			assertTargetCheck(t, checkNameEntry(evidence, declaration, identifierWords("produce_value"), source), tc.want)
		})
	}
}

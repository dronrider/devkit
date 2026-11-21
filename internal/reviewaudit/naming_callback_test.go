package reviewaudit

import (
	"strings"
	"testing"
)

const guardedQueue = `package sample
import "os"
func update(change func(*[]int) bool) error {
  var items []int
  if !change(&items) {
    return nil
  }
  persist(items)
  return os.WriteFile("queue", encode(items), 0600)
}
`

func TestNameCallbackMutationProof(t *testing.T) {
	const code = guardedQueue + `
func queueAppend(n int) error {
  return update(func(entries *[]int) bool {
    if n == 0 { return false }
    *entries = append(*entries, n)
    return true
  })
}
`
	for _, tc := range []struct{ name, old, replacement, want string }{
		{name: "valid"},
		{name: "no persistence", old: "return true", replacement: "return false", want: "следующий return true"},
		{name: "unrelated data", old: "persist(items)", replacement: "persist(nil)", want: "кто получает"},
		{name: "changed value", old: "append(*entries, n)", replacement: "append(*entries, 0)", want: "присваивание"},
		{name: "wrong pointer", old: "change(&items)", replacement: "change(items)", want: "выход при false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, at := nameProofFixture(t, strings.Replace(code, tc.old, tc.replacement, 1))
			const wire = `{
				"object":"@func queueAppend","check":"writes","result":"checked",
				"object_comparison":{"expected":["queue","Append"],"actual":"entries","actual_at":"@*entries =",
					"proof":{"words":[
						{"word":"queue","role":"object","fact":{"location":"@*entries =","quote":"*entries =","value":"*entries","text":"Запись в очередь."}},
						{"word":"Append","role":"context","fact":{"location":"@*entries =","quote":"append(*entries","value":"append","text":"Добавление в хвост."}}]}},
				"write_evidence":{"word":"queue","target":"*entries","value":"append(*entries, n)","write_at":"@*entries =",
					"callback":{"call_at":"@return update(","helper":"@func update","invoke_at":"@if !change",
					"use":{"declaration":"@func update","at":"@persist(","expression":"persist(items)"}}}}`
			text := wire
			if tc.name == "unrelated data" {
				text = strings.Replace(wire, tc.old, tc.replacement, 1)
			}
			e := namingWire(t, text, at)
			assertTargetCheck(t, checkNameEntry(e, at("func queueAppend"), identifierWords("queueAppend"), source), tc.want)
		})
	}
}

func TestNameCallbackNoQueueWrite(t *testing.T) {
	for _, tc := range []struct{ name, before, body, want string }{
		{name: "read only", body: "consume(*entries); return false"},
		{name: "writes after true", body: "return true", want: "достижимая запись WriteFile"},
		{name: "writes before false guard", before: "os.WriteFile(\"journal\", nil, 0600)\n", body: "return false", want: "достижимая запись WriteFile"},
		{name: "updates pointer despite false", body: "*entries = nil; return false", want: "callback меняет переданное значение"},
		{name: "false is shadowed", body: "const false = true; return false", want: "достижимая запись WriteFile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := strings.Replace(guardedQueue, "if !change", tc.before+"if !change", 1) +
				"func queueNext() error { return update(func(entries *[]int) bool { " + tc.body + " }) }\n"
			source, at := nameProofFixture(t, code)
			decl := at("func queueNext")
			e := Evidence{Object: decl + "#queueNext", Check: "writes", Result: "not_applicable", Location: decl}
			assertTargetCheck(t, checkNameEntry(e, decl, identifierWords("queueNext"), source), tc.want)
		})
	}
}

func TestNameCallbackSliceElementWrite(t *testing.T) {
	const code = guardedQueue + `
type item struct { Text string }
func queueClear() error {
  return update(func(entries *[]item) bool {
    for i := range *entries {
      entry := &(*entries)[i]
      if i == 0 {
        if ready {
          entry.Text = ""
        }
        return true
      }
    }
    return false
  })
}
`
	for _, tc := range []struct{ name, old, replacement string }{
		{name: "accepts the address of an element"},
		{name: "unrelated element", old: "&(*entries)[i]", replacement: "&other[i]"},
		{name: "alias rebound", old: "if ready {", replacement: "entry = &other[i]; if ready {"},
		{name: "slice replaced", old: "if ready {", replacement: "*entries = nil; if ready {"},
		{name: "slice escapes", old: "if ready {", replacement: "replace(entries); if ready {"},
		{name: "dead branch", old: "if ready {", replacement: "if false {"},
		{name: "constant comparison", old: "if ready {", replacement: "if 1 == 0 {"},
		{name: "flag is shadowed", old: "if ready {", replacement: "true := false; if ready {"},
		{name: "mutation not persisted", old: "return true", replacement: "return false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := strings.ReplaceAll(code, "[]int", "[]item")
			source, at := nameProofFixture(t, strings.Replace(code, tc.old, tc.replacement, 1))
			e := namingWire(t, `{
                "object":"@func queueClear","check":"writes","result":"checked",
                "object_comparison":{"expected":["queue","Clear"],"actual":"entry","actual_at":"@entry.Text",
                    "proof":{"words":[
                        {"word":"queue","role":"object","fact":{"location":"@entry :=","quote":"entry :=","value":"entry","text":"Элемент очереди."}},
                        {"word":"Clear","role":"context","fact":{"location":"@entry.Text","quote":"entry.Text =","value":"entry.Text","text":"Удалён текст."}}]}},
                "write_evidence":{"word":"Clear","target":"entry.Text","value":"\"\"","write_at":"@entry.Text",
                    "callback":{"call_at":"@return update(","helper":"@func update","invoke_at":"@if !change",
                    "use":{"declaration":"@func update","at":"@persist(","expression":"persist(items)"}}}}`, at)
			want := ""
			if tc.old != "" {
				want = "следующий return true"
			}
			assertTargetCheck(t, checkNameEntry(e, at("func queueClear"), identifierWords("queueClear"), source), want)
		})
	}
}

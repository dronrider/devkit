package reviewaudit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNameRuntimeResultIntegration(t *testing.T) {
	source, key, writeRecord, _ := nameRuntimeFixture(t)
	const code = "function putClip(clip) {\n" +
		"  return clip.upload;\n" +
		"}\nconsume(putClip(input));\n" +
		"function readRevision() {\n" +
		"  return revision\n" +
		"}\nconsume(readRevision());\n" +
		"function getChoice(input) {\n" +
		"  return{\n    choice: input,\n  };\n" +
		"}\nconsume(getChoice(input));\n" +
		"// return clip.upload;\n"
	write(t, filepath.Join(source.root, "result.mjs"), code)
	gitT(t, source.root, "add", "result.mjs")
	gitT(t, source.root, "commit", "-qm", "JavaScript result examples")
	revision := gitT(t, source.root, "rev-parse", "HEAD")

	for _, example := range []struct {
		function, word, declaration, returnAt, quote, expression string
		consumerAt, consumer, wordAt, wordQuote, control, value  string
	}{
		{"putClip", "Clip", "result.mjs:1", "result.mjs:2", "return clip.upload;", "clip.upload",
			"result.mjs:4", "putClip(input)", "result.mjs:2", "clip.upload", "Promise(clip-b)", "Promise(clip-a)"},
		{"readRevision", "Revision", "result.mjs:5", "result.mjs:6", "return revision", "revision",
			"result.mjs:8", "readRevision()", "result.mjs:6", "revision", "18", "17"},
		{"getChoice", "Choice", "result.mjs:9", "result.mjs:10", "return{\n    choice: input,\n  };", "{\n    choice: input,\n  }",
			"result.mjs:14", "getChoice(input)", "result.mjs:11", "choice: input", "{\"choice\":2}", "{\"choice\":1}"},
	} {
		t.Run(example.function, func(t *testing.T) {
			program, positiveArg, negativeArg := "independent-js-probe", "positive", "control"
			returned := nameRuntimeSite{At: example.returnAt, Quote: example.quote, Expression: example.expression, Identity: "return value"}
			read := nameRuntimeSite{At: example.consumerAt, Quote: "consume(" + example.consumer + ");", Expression: example.consumer, Identity: returned.Identity}
			label := example.word
			words := []NameProofWord{{Word: label, Role: "source", Fact: ReviewFact{
				Location: example.wordAt, Quote: example.wordQuote, Value: example.wordQuote, Text: "Источник возвращённого значения.",
			}}}
			run := nameRuntimeResultRecord{
				Version: 2, Task: source.taskID, Commit: revision, Object: example.declaration + "#" + example.function,
				Declaration: example.declaration, Actual: example.expression, Expected: []string{label}, Words: words,
				nameRuntimeResultRun: nameRuntimeResultRun{
					Command:  nameRuntimeCommand{&program, &positiveArg},
					Returned: nameRuntimeObservation{nameRuntimeSite: returned, Value: &example.value},
					Read:     nameRuntimeObservation{nameRuntimeSite: read, Value: &example.value},
				},
				Negative: nameRuntimeResultRun{
					Command:  nameRuntimeCommand{&program, &negativeArg},
					Returned: nameRuntimeObservation{nameRuntimeSite: returned, Value: &example.control},
					Read:     nameRuntimeObservation{nameRuntimeSite: read, Value: &example.control},
				},
			}
			e := Evidence{
				Object: run.Object, Check: "result", Result: "checked",
				ObjectComparison: &NameObjectComparison{Expected: run.Expected, Actual: example.expression, ActualAt: example.returnAt,
					Proof: &NameObjectProof{Expression: example.expression, Words: words}},
				ResultEvidence: &ResultEvidence{Word: &label, Returned: example.expression, ReturnAt: example.returnAt,
					Consumer: example.consumer, ConsumerAt: example.consumerAt},
			}
			proof := e.ObjectComparison.Proof
			objectWords := identifierWords(example.function)
			check := func(want string) {
				t.Helper()
				assertTargetCheck(t, checkNameEntry(e, example.declaration, objectWords, source), want)
			}
			proof.Runtime = signNameRuntimeRecord(t, key, run)
			check("")
			if example.function == "getChoice" {
				run.Actual = "{"
				run.Returned.Quote, run.Returned.Expression = "return{", run.Actual
				run.Negative.Returned.nameRuntimeSite = run.Returned.nameRuntimeSite
				e.ObjectComparison.Actual, e.ResultEvidence.Returned, proof.Expression = run.Actual, run.Actual, run.Actual
				proof.Runtime = signNameRuntimeRecord(t, key, run)
				check("полным возвращаемым выражением")
			}
			if example.function != "putClip" {
				return
			}

			proof.Runtime.Payload += " "
			check("подпись не подтверждена")
			proof.Runtime = signNameRuntimeRecord(t, key, writeRecord)
			check("unknown field")
			proof.Runtime = signNameRuntimeRecord(t, key, run)
			e.ResultEvidence.Consumer = "unknown"
			check("точку и выражение потребителя")
			e.ResultEvidence.Consumer = example.consumer
			proof.Words[0].Role = "context"
			check("payload.words")
			proof.Words[0].Role = "source"

			wrong := run
			wrong.Negative = run.nameRuntimeResultRun
			wrong.Negative.Command = run.Negative.Command
			proof.Runtime = signNameRuntimeRecord(t, key, wrong)
			check("другое значение")
			wrong = run
			wrong.Read.Identity = "different result"
			proof.Runtime = signNameRuntimeRecord(t, key, wrong)
			check("один объект и одно значение")
			wrong = run
			wrong.Negative.Command = run.Command
			proof.Runtime = signNameRuntimeRecord(t, key, wrong)
			check("отдельного контроля")
			wrong = run
			wrong.Returned.At = "result.mjs:15"
			wrong.Negative.Returned.At = wrong.Returned.At
			e.ObjectComparison.ActualAt, e.ResultEvidence.ReturnAt = wrong.Returned.At, wrong.Returned.At
			proof.Runtime = signNameRuntimeRecord(t, key, wrong)
			check("начните цитату с инструкции return")
			e.ObjectComparison.ActualAt, e.ResultEvidence.ReturnAt = example.returnAt, example.returnAt

			truncated := run
			truncated.Actual = "clip"
			truncated.Returned.Quote, truncated.Returned.Expression = "return clip", truncated.Actual
			truncated.Negative.Returned.nameRuntimeSite = truncated.Returned.nameRuntimeSite
			truncated.Words = []NameProofWord{{Word: label, Role: "source", Fact: ReviewFact{
				Location: example.returnAt, Quote: "clip", Value: "clip", Text: "Объект передан в функцию.",
			}}}
			e.ObjectComparison.Actual, e.ResultEvidence.Returned, proof.Expression = truncated.Actual, truncated.Actual, truncated.Actual
			proof.Words = truncated.Words
			proof.Runtime = signNameRuntimeRecord(t, key, truncated)
			check("всё выражение возврата")
			e.ObjectComparison.Actual, e.ResultEvidence.Returned, proof.Expression = run.Actual, run.Actual, run.Actual
			proof.Words = run.Words

			proof.Runtime = signNameRuntimeRecord(t, key, run)
			write(t, filepath.Join(source.root, "result.mjs"), strings.Replace(code, "return clip.upload;", "return clip.other;", 1))
			check("дерево проверки содержит изменение")
			write(t, filepath.Join(source.root, "result.mjs"), code)
		})
	}
}

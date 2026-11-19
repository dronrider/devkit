package reviewaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryCoverageUsesTheActualAdapterCallAndLoop(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "repo/sqlite/adapter.go"), `package sqlite
import "example/repo/sqlite/gen"
func (r *Repo) load(keys []string) {
  for _, id := range keys {
    r.getDB().ByID(id)
  }
}
func (r *Repo) getDB() *gen.Queries { return r.db }
`)
	generated := `package gen
func (q *Queries) ByID(id string) { q.db.QueryContext(ctx, id) }
func (q *Queries) Other(id string) { q.db.ExecContext(ctx, id) }
`
	write(t, filepath.Join(root, "repo/sqlite/gen/sql.go"), generated)
	write(t, filepath.Join(root, "repo/postgres/gen/sql.go"), generated)
	write(t, filepath.Join(root, "entry.go"), `package sample
func entry(a, b string) {
    if !valid(a,b) { return }
    keys := selectKeys(a,b)
    repo.load(keys)
}
`)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "query")
	const object = "repo/sqlite/adapter.go:3"
	const driver = "repo/sqlite/gen/sql.go:2"
	const codeHash = "snapshot"
	rule := Rule{ID: queryRule, Objects: []string{object}}
	one, two := 1, 2
	valid := func() []Evidence {
		return []Evidence{
			{Object: object, Check: "calls", Result: "checked", QueryCalls: &QueryCalls{
				KeysParam: "keys", One: &one, Two: &two, Repeat: &one, Probe: &QueryProbe{File: "loop.json"}, Paths: []QueryPath{{
					Steps:    []QueryStep{{At: "repo/sqlite/adapter.go:5", To: "repo/sqlite/gen/sql.go:2", ReceiverAt: "repo/sqlite/adapter.go:8"}},
					DriverAt: "repo/sqlite/gen/sql.go:2", LoopAt: "repo/sqlite/adapter.go:4",
				}},
			}},
			{Object: object, Check: "inputs", Result: "checked", QueryInputs: &QueryInputs{
				One: []string{"A"}, Two: []string{"A", "B"}, Repeat: []string{"A"}, Locations: []string{"entry.go:2"},
				TwoScenario: &QueryTwoScenario{
					Entry: "entry.go:2", Parameters: map[string]string{"a": "A", "b": "B"}, IDs: []string{"A", "B"},
					Validation: ReviewFact{Location: "entry.go:3", Quote: "valid(a,b)", Text: "Переданы оба ключа."},
					Selection:  ReviewFact{Location: "entry.go:4", Quote: "selectKeys(a,b)", Text: "Возвращаются A и B."},
					Adapter:    ReviewFact{Location: "entry.go:5", Quote: "repo.load(keys)", Text: "Оба ключа переданы адаптеру."},
				},
			}},
			{Object: object, Check: "batch", Result: "violated"},
		}
	}
	saveProbe := func(object string, twoIDs, twoCalls []string) {
		t.Helper()
		body, err := json.Marshal(queryProbeLog{CodeHash: codeHash, Object: object, Command: "go test -run TestDriver",
			Runs: []queryProbeRun{{Case: "one", IDs: []string{"A"}, Calls: []string{driver}},
				{Case: "two", IDs: twoIDs, Calls: twoCalls}, {Case: "repeat", IDs: []string{"A"}, Calls: []string{driver}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(path+".queries", "loop.json"), string(body))
	}
	base := gitT(t, root, "rev-parse", "HEAD")
	for _, tc := range []struct {
		name, want string
		edit       func([]Evidence)
	}{
		{name: "real loop"},
		{name: "unrelated driver call", want: "в ByID ожидается один подходящий вызов", edit: func(e []Evidence) {
			e[0].QueryCalls.Paths[0].DriverAt = "repo/sqlite/gen/sql.go:3"
		}},
		{name: "wrong method", want: "не ведёт", edit: func(e []Evidence) {
			e[0].QueryCalls.Paths[0].Steps[0].To = "repo/sqlite/gen/sql.go:3"
		}},
		{name: "wrong adapter", want: "не соответствует получателю", edit: func(e []Evidence) {
			e[0].QueryCalls.Paths[0].Steps[0].To = "repo/postgres/gen/sql.go:2"
		}},
		{name: "missing loop", want: "находится в цикле", edit: func(e []Evidence) {
			e[0].QueryCalls.Paths[0].LoopAt = ""
		}},
		{name: "incorrect count", want: "замер two содержит 2 обращений", edit: func(e []Evidence) {
			e[0].QueryCalls.Two = &one
		}},
		{name: "missing concrete input", want: "заполните two_scenario", edit: func(e []Evidence) {
			e[1].QueryInputs.TwoScenario = nil
		}},
		{name: "wrong concrete input", want: "должны совпадать с query_inputs.two", edit: func(e []Evidence) {
			e[1].QueryInputs.TwoScenario.IDs[1] = "C"
		}},
		{name: "wrong adapter call", want: "нужен вызов load", edit: func(e []Evidence) {
			e[1].QueryInputs.TwoScenario.Adapter = ReviewFact{Location: "entry.go:6", Quote: "}", Text: "Ссылка на постороннюю строку."}
		}},
		{name: "missing driver capture", want: "нужен файл замера на драйвере", edit: func(e []Evidence) {
			e[0].QueryCalls.Probe = nil
		}},
		{name: "counter text is not a capture", want: "нужен файл замера на драйвере", edit: func(e []Evidence) {
			e[0].QueryCalls.Probe = &QueryProbe{Command: "go test", Counter: "1 / 2 / 1"}
		}},
		{name: "driver capture for another method", want: "замер должен относиться к этому снимку и методу", edit: func(e []Evidence) {
			saveProbe("repo/postgres/gen/sql.go:2", []string{"A", "B"}, []string{driver, driver})
		}},
		{name: "driver capture for other IDs", want: "замер two должен содержать те же ID", edit: func(e []Evidence) {
			saveProbe(object, []string{"A", "C"}, []string{driver, driver})
		}},
		{name: "driver capture for another query", want: "обращение вне цепочки метода", edit: func(e []Evidence) {
			saveProbe(object, []string{"A", "B"}, []string{driver, "repo/sqlite/gen/sql.go:3"})
		}},
		{name: "same key twice", want: "два разных ID", edit: func(e []Evidence) {
			e[1].QueryInputs.Two = []string{"A", "A"}
		}},
		{name: "unjustified per-key queries", want: "обоснование поштучных запросов", edit: func(e []Evidence) {
			e[2].Result = "checked"
		}},
		{name: "pending input with prose alone", want: "для pending заполните query_inputs.gap", edit: func(e []Evidence) {
			e[1].Result, e[2].Result = "pending", "pending"
			e[1].Location, e[1].Observation = "entry.go:2", "Не найден транспорт"
			e[1].QueryInputs = nil
		}},
		{name: "pending input with inspected call", edit: func(e []Evidence) {
			e[1].Result, e[2].Result = "pending", "pending"
			e[1].QueryInputs = &QueryInputs{Locations: []string{"entry.go:2"}, One: []string{"A"}, Two: []string{"A", "B"}, Repeat: []string{"A"}}
			e[1].QueryInputs.Gap = &QueryInputGap{Kind: "contract", LastAt: "entry.go:2", Question: "Допускает ли вызывающая сторона два разных ID?"}
		}},
		{name: "pending input with invalid call reference", want: "строка 99 отсутствует", edit: func(e []Evidence) {
			e[1].Result, e[2].Result = "pending", "pending"
			e[1].QueryInputs = &QueryInputs{Locations: []string{"entry.go:99"}}
			e[1].QueryInputs.Gap = &QueryInputGap{Kind: "chain", LastAt: "entry.go:2", Question: "Какой вход передаёт два ID?"}
		}},
		{name: "pending measurement needs a question", want: "для pending заполните facts.question", edit: func(e []Evidence) {
			e[0].Result, e[2].Result = "pending", "pending"
			e[0].QueryCalls.Probe = nil
		}},
		{name: "pending measurement preserves static count", edit: func(e []Evidence) {
			e[0].Result, e[2].Result = "pending", "pending"
			e[0].QueryCalls.Probe = nil
			e[0].Facts = map[string][]ReviewFact{"question": {{Location: driver, Text: "Сколько запросов реально получает драйвер?"}}}
		}},
		{name: "pending missing file without path", want: "причина missing_file требует путь и ревизию", edit: func(e []Evidence) {
			e[1].Result, e[2].Result = "pending", "pending"
			e[1].QueryInputs.Gap = &QueryInputGap{Kind: "missing_file", LastAt: "entry.go:2", Question: "Где проверяется вход?"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saveProbe(object, []string{"A", "B"}, []string{driver, driver})
			rule.Evidence = valid()
			if tc.edit != nil {
				tc.edit(rule.Evidence)
			}
			err := checkQueryEvidence(root, path, "", path, Report{BaseSHA: base, CodeHash: codeHash, Rules: []Rule{rule}})
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("ожидается %q, получено %v", tc.want, err)
			}
		})
	}
}

func TestQueryLimitsCoverEachRelatedEntry(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "entry.py"), `def export(id):
    assert isinstance(id, str)
def download(id):
    assert isinstance(id, str)
def unrelated(id):
    pass
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	in := &QueryInputs{Entries: []QueryEntryReview{
		{Entry: "entry.py:1", Result: "inspected"}, {Entry: "entry.py:3", Result: "inspected"},
		{Entry: "entry.py:5", Result: "unrelated"},
	}, TwoLimit: "entry.py:2"}
	assertTargetCheck(t, checkQueryLimits(in, source), "укажите two_limits")
	in.TwoLimits = []QueryInputLimit{
		{Entry: "entry.py:1", Condition: ReviewFact{Location: "entry.py:2", Quote: "assert isinstance(id, str)", Text: "Принимает только одну строку."}},
	}
	assertTargetCheck(t, checkQueryLimits(in, source), "укажите ограничение для entry.py:3")
	in.TwoLimits = append(in.TwoLimits, QueryInputLimit{Entry: "entry.py:3",
		Condition: ReviewFact{Location: "entry.py:4", Quote: "assert isinstance(id, str)", Text: "Принимает только одну строку."}})
	assertTargetCheck(t, checkQueryLimits(in, source), "")
}

func TestPendingQueryRequiresReviewOfDiscoveredEntry(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "repo/adapter.go"), `package repo
func (r Repo) Load(keys []string) { r.db.QueryContext(ctx, keys) }
`)
	write(t, filepath.Join(root, "usecase/service.go"), `package usecase
func (u Service) Export(keys []string) { u.repo.Load(keys) }
`)
	write(t, filepath.Join(root, "transport/handler.go"), `// Code generated by generator. DO NOT EDIT.
package transport
func validate(a, b string) bool { return a != "" || b != "" }
func (h Handler) Post(a, b string) {
    log(a)
    if !validate(a, b) { return }
    h.service.Export([]string{a, b})
}
`)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "existing entry and validator")
	base := gitT(t, root, "rev-parse", "HEAD")
	write(t, filepath.Join(root, "usecase/service.go"), `package usecase
func (u Service) Export(keys []string) {
    u.repo.Load(keys)
    log(keys)
}
`)
	const object, entry = "repo/adapter.go:2", "transport/handler.go:4"
	rules := []Rule{{ID: queryRule}}
	if err := collectTargets(root, path, base, "", rules); err != nil {
		t.Fatal(err)
	}
	rule := &rules[0]
	if got := rule.QueryEntries[object]; len(got) != 1 || got[0].Location != entry {
		t.Fatalf("вход из неизменённого файла не найден: %+v", got)
	}
	rule.Objects = []string{object}
	in := &QueryInputs{Locations: []string{"usecase/service.go:3"}}
	input := Evidence{Object: object, Check: "inputs", Result: "pending", QueryInputs: in,
		Observation: "Граница допуска двух разных ключей остаётся открытой."}
	rule.Evidence = []Evidence{input}
	report := Report{BaseSHA: base, HeadSHA: base, Rules: rules}
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkQueryEvidence(root, path, "", path, report), want)
	}
	check("для pending заполните query_inputs.gap")
	in.Gap = &QueryInputGap{Kind: "contract", LastAt: "usecase/service.go:3", Question: "Может ли транспорт передать два разных ключа?"}
	check("разберите штатный вход " + entry)
	in.Entries = []QueryEntryReview{{
		Entry: entry, Result: "inspected", CallAt: "transport/handler.go:7",
		ValidationAt: "transport/handler.go:3", Validator: "transport/handler.go:3", ValidationCallAt: "transport/handler.go:6",
		Observation: "Валидатор допускает оба поля. Контракт выдачи двух записей пока не найден.",
	}}
	check("")
	summary := "### Проверить контракт\n\n" + object + "\n"
	rule.Applicability, rule.Result = "applicable", "pending"
	rule.Evidence[0].Heading = "### Проверить контракт"
	assertTargetCheck(t, checkCoverage(report, summary), "нужен вопрос из query_inputs.gap")
	rule.Evidence = append(rule.Evidence,
		Evidence{Object: object, Check: "calls", Result: "checked", Location: object, Observation: "Один вызов драйвера."},
		Evidence{Object: object, Check: "batch", Result: "pending", Heading: rule.Evidence[0].Heading, Observation: "Ожидает проверки входа."})
	assertTargetCheck(t, checkCoverage(report, summary+in.Gap.Question+"\n"), "")
	rule.Evidence = rule.Evidence[:1]
	expected := rule.QueryEntries
	rule.QueryEntries = nil
	assertTargetCheck(t, validateQueryEntries(*rule, expected), "перечень query_entries из снимка изменён")
	rule.QueryEntries = expected
	in.Entries[0].CallAt = "transport/handler.go:5"
	check("ожидается один подходящий вызов")
	in.Entries[0].CallAt = "transport/handler.go:7"
	in.Entries[0].ValidationAt = "usecase/service.go:3"
	check("укажите строку во входной функции")
	in.Entries[0].ValidationAt = "transport/handler.go:3"
	in.Gap.Kind = "missing_file"
	check("причина missing_file требует путь и ревизию")
	rule.Evidence[0].MissingFile = &MissingFileEvidence{Path: "transport/handler.go", Revision: "head"}
	assertTargetCheck(t, checkMissingFiles(root, report, report), "есть на head")
	rule.Evidence[0].MissingFile.Path = "consumer/contract.md"
	assertTargetCheck(t, checkMissingFiles(root, report, report), "")
}

func TestQueryCoverageUsesSourceReferencesAndCountsOutsideGo(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "adapter.py"), `def load(ids):
    for id in ids:
        fetch(id)
def fetch(id):
    db.execute("SELECT value FROM records WHERE id = ?", [id])
`)
	write(t, filepath.Join(root, "entry.py"), `def run(a, b):
    assert a and b
    keys = [a, b]
    load(keys)
`)
	const object = "adapter.py:1"
	one, two := 1, 2
	calls := &QueryCalls{KeysParam: "ids", One: &one, Two: &one, Paths: []QueryPath{{
		Steps: []QueryStep{{At: "adapter.py:3", To: "adapter.py:4"}}, DriverAt: "adapter.py:5",
	}}}
	rule := Rule{ID: queryRule, Objects: []string{object}, Evidence: []Evidence{
		{Object: object, Check: "calls", Result: "checked", QueryCalls: calls},
		{Object: object, Check: "inputs", Result: "checked", QueryInputs: &QueryInputs{
			One: []string{"A"}, Two: []string{"A", "B"}, Locations: []string{object},
			TwoScenario: &QueryTwoScenario{
				Entry: "entry.py:1", Parameters: map[string]string{"a": "A", "b": "B"}, IDs: []string{"A", "B"},
				Validation: ReviewFact{Location: "entry.py:2", Quote: "assert a and b", Text: "Оба ID заполнены."},
				Selection:  ReviewFact{Location: "entry.py:3", Quote: "keys = [a, b]", Text: "В наборе A и B."},
				Adapter:    ReviewFact{Location: "entry.py:4", Quote: "load(keys)", Text: "Оба ключа переданы адаптеру."},
			},
		}},
		{Object: object, Check: "batch", Result: "violated"},
	}}
	report := Report{Rules: []Rule{rule}}
	calls.Paths[0].LoopAt = "adapter.py:2"
	calls.Two = &two
	assertTargetCheck(t, checkQueryEvidence(root, path, "", path, report), "нужен файл замера на драйвере")
	calls.Two = &one
	assertTargetCheck(t, checkQueryEvidence(root, path, "", path, report), "для two цепочка даёт 2")
	calls.Paths[0].LoopAt = "adapter.py:20"
	assertTargetCheck(t, checkQueryEvidence(root, path, "", path, report), "строка 20 отсутствует")
}

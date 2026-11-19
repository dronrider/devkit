package reviewaudit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIdentifierWordsPreservesInitialisms(t *testing.T) {
	for name, want := range map[string][]string{
		"createPDFReportFromRows": {"create", "PDF", "Report", "From", "Rows"},
		"jwtReceiptIDs":           {"jwt", "Receipt", "IDs"},
		"listURLsByID":            {"list", "URLs", "By", "ID"},
		"JWT_claim-attribute":     {"JWT", "claim", "attribute"},
	} {
		if got := identifierWords(name); !slices.Equal(got, want) {
			t.Errorf("%s: получено %q, ожидается %q", name, got, want)
		}
	}
}

func TestNameChecksLiteralReturnAndConsumer(t *testing.T) {
	root, report := fixture(t)
	write(t, filepath.Join(root, "sample.py"), "def page_size():\n    return 25\n\nassert page_size() == 25\n")
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: report, files: files, cache: make(map[string][]string)}
	word := "size"
	ev := Evidence{Check: "result", Object: "sample.py:1#page_size", Result: "checked",
		ObjectComparison: &NameObjectComparison{Actual: "25", ActualAt: "sample.py:2", Expected: []string{"page", "size"}},
		ResultEvidence:   &ResultEvidence{Word: &word, Returned: "25", ReturnAt: "sample.py:2", ConsumerAt: "sample.py:4", Consumer: "assert page_size() == 25"}}
	check := func() error {
		return checkNameObject(ev, &word, identifierWords("page_size"), ev.ResultEvidence.Returned, "sample.py:1", source)
	}
	assertTargetCheck(t, check(), "")
	ev.ResultEvidence.Consumer = "assert other_size() == 25"
	assertTargetCheck(t, check(), "consumer_at")
	ev.ResultEvidence.Consumer = "assert page_size() == 25"
	ev.ObjectComparison.Actual = "2"
	assertTargetCheck(t, check(), "actual_at")
}

func TestNamingRejectsEmptyExpectedForNamedResultAndWrite(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "code.go"), `package sample
func createInvoiceFromOrders() []File { return files }
func addInvoiceAttributes() { data.Orders = orders }
func validateInvoice() bool { return ok }
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	for _, tc := range []struct {
		name, check, actual, at, reason, want string
		expected                              []string
		word                                  string
	}{
		{name: "createInvoiceFromOrders", check: "result", actual: "files", at: "code.go:2", want: "имя \"create Invoice From Orders\" называет объект"},
		{name: "addInvoiceAttributes", check: "writes", actual: "Orders", at: "code.go:3", reason: "Запись определена реализацией", want: "имя \"add Invoice Attributes\" называет объект"},
		{name: "createInvoiceFromOrders", check: "result", actual: "files", at: "code.go:2", expected: []string{"Invoice"}, word: "Invoice"},
		{name: "addInvoiceAttributes", check: "writes", actual: "Orders", at: "code.go:3", expected: []string{"Invoice", "Attributes"}, word: "Attributes"},
		{name: "validateInvoice", check: "result", actual: "ok", at: "code.go:4", want: "полный состав"},
		{name: "validateInvoice", check: "result", actual: "ok", at: "code.go:4", reason: "Invoice обозначает вход проверки. Возвращается признак её успеха.", want: "полный состав"},
	} {
		result := "checked"
		if len(tc.expected) != 0 {
			result = "violated"
		}
		entry := Evidence{Check: tc.check, Result: result, ObjectComparison: &NameObjectComparison{
			Expected: tc.expected, NoExpectationReason: tc.reason, Actual: tc.actual, ActualAt: tc.at,
		}}
		assertTargetCheck(t, checkNameObject(entry, &tc.word, identifierWords(tc.name), tc.actual, "", source), tc.want)
	}
}

func TestNamingRejectsNotApplicableForPromisedObject(t *testing.T) {
	root, path := fixture(t)
	const location = "names.go:2"
	write(t, filepath.Join(root, "names.go"), "package sample\nfunc noOp() {}\n")
	for _, tc := range []struct{ name, check, want string }{
		{"createInvoiceFromOrders", "result", "обещает объект для result"},
		{"addInvoiceAttributes", "writes", "обещает объект для writes"},
		{"createInvoiceFromOrders", "writes", ""},
		{"addInvoiceAttributes", "result", ""},
		{"createIfMissing", "result", ""},
		{"validateInvoice", "writes", ""},
	} {
		t.Run(tc.name+"/"+tc.check, func(t *testing.T) {
			rule := Rule{
				Targets:  []Target{{Object: tc.name, Symbol: tc.name, Location: location}},
				Evidence: []Evidence{{Object: tc.name, Check: tc.check, Result: "not_applicable", Location: location}},
			}
			assertTargetCheck(t, checkNameEvidence(root, path, "", "", rule), tc.want)
		})
	}
}

func TestCoverageChecksMissingFileOnSelectedCommit(t *testing.T) {
	root, path := fixture(t)
	const removed, added = "transport[old].txt", "transport[new].txt"
	write(t, filepath.Join(root, removed), "old transport\n")
	gitT(t, root, "add", removed)
	gitT(t, root, "commit", "-qm", "base transport")
	base := gitT(t, root, "rev-parse", "HEAD")
	gitT(t, root, "rm", removed)
	write(t, filepath.Join(root, added), "new transport\n")
	gitT(t, root, "add", added)
	gitT(t, root, "commit", "-qm", "new transport")
	head := gitT(t, root, "rev-parse", "HEAD")
	report := completed(t, root, path)
	report.BaseSHA = base
	// Missing-file checks must use the resolved HEAD, even if the report records an older HEAD.
	report.HeadSHA = base
	summary := filepath.Join(t.TempDir(), "review.md")
	write(t, summary, "### Контракт транспорта\n")
	for i := range report.Rules {
		if report.Rules[i].ID == namingRule {
			report.Rules[i].Files = []FileReview{
				{Path: added, Reason: "Текст контракта без объявлений"},
				{Path: removed, Reason: "Удалённый текст контракта без объявлений"},
			}
		}
	}
	rule := findTargetRule(t, &report, "common-architecture:ARCH-001")
	rule.Result = "pending"
	entry := &rule.Evidence[0]
	entry.Result, entry.Heading = "pending", "### Контракт транспорта"
	for _, tc := range []struct {
		file, revision, want string
	}{
		{removed, "head", ""},
		{removed, "base", "есть на base (" + base + ")"},
		{added, "base", ""},
		{added, "head", "есть на head (" + head + ")"},
		{"transport*.txt", "head", ""},
		{"../transport.txt", "head", "точный путь от корня"},
		{added, "HEAD^", "укажите base или head"},
	} {
		t.Run(tc.file+"/"+tc.revision, func(t *testing.T) {
			entry.MissingFile = &MissingFileEvidence{Path: tc.file, Revision: tc.revision}
			save(t, path, report)
			assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), tc.want)
		})
	}
}

func TestCoverageReportsOpenArchitectureAndBlocksClean(t *testing.T) {
	root, path := fixture(t)
	report := completed(t, root, path)
	summary := filepath.Join(root, "review.md")
	for _, status := range []string{"pending", "violated"} {
		report.Architecture.Result = status
		report.Architecture.Reason = ""
		report.Architecture.Heading = "### Владение состоянием"
		write(t, summary, "### Владение состоянием\n")
		save(t, path, report)
		if err := CheckCoverage(root, path, "XR-1", summary); err == nil ||
			!strings.Contains(err.Error(), "укажите дефект или недостающий факт") {
			t.Fatalf("ожидается отказ для %s без основания, получено %v", status, err)
		}
		report.Architecture.Reason = "Граница владения состоянием требует решения"
		save(t, path, report)
		write(t, summary, "### Другое замечание\n")
		if err := CheckCoverage(root, path, "XR-1", summary); err == nil ||
			!strings.Contains(err.Error(), "заголовок замечания или вопроса") {
			t.Fatalf("ожидается отказ без ссылки на архитектурный вывод, получено %v", err)
		}
		write(t, summary, "### Владение состоянием\n")
		if err := CheckCoverage(root, path, "XR-1", summary); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(summary); err != nil {
			t.Fatal(err)
		}
		if err := Validate(root, path, "XR-1"); err == nil ||
			!strings.Contains(err.Error(), "архитектурная оценка") {
			t.Fatalf("чистый вердикт с %s: %v", status, err)
		}
	}
	write(t, filepath.Join(root, "code.go"), "package changed\n")
	write(t, summary, "### Владение состоянием\n")
	if err := CheckCoverage(root, path, "XR-1", summary); err == nil ||
		!strings.Contains(err.Error(), "код или постановка изменились") {
		t.Fatalf("проверка покрытия приняла устаревший снимок: %v", err)
	}
}

func TestCoverageChecksExactNameAndEvidenceLocations(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "code.go"), "package sample\nfunc updateJWTInvoice(invoice *JWTInvoice) *JWTInvoice {\n invoice.Token = jwt()\n return invoice\n}\nfunc export() { archive(updateJWTInvoice(current)) }\ntype Bill = JWTInvoice\n")
	gitT(t, root, "add", "code.go")
	gitT(t, root, "commit", "-qm", "invoice")
	report := completed(t, root, path)
	summary := filepath.Join(t.TempDir(), "review.md")
	write(t, summary, "# Ревью\n")
	invoice := "Invoice"
	validEvidence := func() []Evidence {
		return []Evidence{
			{Object: "updateJWTInvoice", Check: "meaning", Result: "checked", Symbol: "updateJWTInvoice",
				Location: "code.go:2", Observation: "Меняет JWT счёта и возвращает счёт.",
				WordChecks: []WordCheck{
					{Word: "update", Meaning: "обновляет", Fact: "есть присваивание", Location: "code.go:3"},
					{Word: "JWT", Meaning: "формат токена", Fact: "вызывается jwt", Location: "code.go:3"},
					{Word: "Invoice", Meaning: "счёт", Fact: "параметр invoice", Location: "base@code.go:2"},
				}},
			{Object: "updateJWTInvoice", Check: "result", Result: "checked", Location: "code.go:4", Observation: "Возвращает счёт.",
				ObjectComparison: &NameObjectComparison{Expected: []string{"JWT", "Invoice"}, Actual: "JWTInvoice", ActualAt: "code.go:2"},
				ResultEvidence: &ResultEvidence{Word: &invoice, Returned: "invoice (*JWTInvoice)", ReturnAt: "code.go:4",
					Consumer: "Передаёт счёт в архив", ConsumerAt: "code.go:6"}},
			{Object: "updateJWTInvoice", Check: "writes", Result: "checked", Location: "code.go:3", Observation: "Меняет JWT счёта.",
				ObjectComparison: &NameObjectComparison{Expected: []string{"JWT", "Invoice"}, Actual: "invoice", ActualAt: "code.go:3", EquivalentAt: "code.go:2"},
				WriteEvidence:    &WriteEvidence{Word: &invoice, Target: "invoice.Token", Value: "jwt()", WriteAt: "code.go:3"}},
		}
	}
	for _, tc := range []struct {
		name, want string
		edit       func([]Evidence)
	}{
		{name: "complete"},
		{name: "generic result", want: "заполните result_evidence", edit: func(e []Evidence) { e[1].ResultEvidence = nil }},
		{name: "generic writes", want: "заполните write_evidence", edit: func(e []Evidence) { e[2].WriteEvidence = nil }},
		{name: "legacy result without object comparison", want: "заполните object_comparison", edit: func(e []Evidence) {
			e[1].ObjectComparison = nil
		}},
		{name: "invented expectation", want: "полный состав", edit: func(e []Evidence) {
			e[1].ObjectComparison.Expected = []string{"Invoice", "Files"}
		}},
		{name: "actual object missing from source", want: "идентификатор \"Bill\" должен быть виден", edit: func(e []Evidence) {
			e[1].ObjectComparison.Actual = "Bill"
			e[1].ResultEvidence.Returned = "Bill"
		}},
		{name: "unsubstantiated match", want: "для checked укажите equivalent_at", edit: func(e []Evidence) {
			e[2].ObjectComparison.Actual = "Token"
			e[2].ObjectComparison.EquivalentAt = ""
		}},
		{name: "equivalence reference misses expected name", want: "строка должна называть \"JWT Invoice\" и \"Token\"", edit: func(e []Evidence) {
			e[2].ObjectComparison.Actual = "Token"
			e[2].ObjectComparison.EquivalentAt = "code.go:3"
		}},
		{name: "declared synonym", edit: func(e []Evidence) {
			e[1].ResultEvidence.Returned = "Bill"
			e[1].ObjectComparison.Actual = "Bill"
			e[1].ObjectComparison.ActualAt = "code.go:7"
			e[1].ObjectComparison.EquivalentAt = "code.go:7"
		}},
		{name: "missing acronym", want: "пропущены слова JWT", edit: func(e []Evidence) {
			e[0].WordChecks = append(e[0].WordChecks[:1], e[0].WordChecks[2])
		}},
		{name: "unknown word", want: "отсутствует в имени", edit: func(e []Evidence) {
			other := "File"
			e[1].ResultEvidence.Word = &other
		}},
		{name: "absent caller", want: "строка 90 отсутствует", edit: func(e []Evidence) { e[1].ResultEvidence.ConsumerAt = "code.go:90" }},
		{name: "different declaration", want: "имя \"updateJWTInvoice\" отсутствует", edit: func(e []Evidence) { e[0].Location = "code.go:1" }},
		{name: "review metadata", want: "файл не входит в снимок", edit: func(e []Evidence) { e[2].WriteEvidence.WriteAt = "docs/tasks/XR-1.md:1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range report.Rules {
				if report.Rules[i].ID == namingRule {
					report.Rules[i].Objects = []string{"updateJWTInvoice"}
					report.Rules[i].Evidence = validEvidence()
					if tc.edit != nil {
						tc.edit(report.Rules[i].Evidence)
					}
				}
			}
			save(t, path, report)
			for _, err := range []error{CheckCoverage(root, path, "XR-1", summary), Validate(root, path, "XR-1")} {
				if tc.want == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("ожидается %q, получено %v", tc.want, err)
				}
			}
		})
	}
}

func TestNameResultFlowRequiresTheSameValueAndExpectedType(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "names.go"), `package sample
type Invoice struct{}
type FileInfo struct{}
type Key struct{ UpdateShippingRecords []string }
func createInvoiceFromKey() *Invoice {
  result := &Invoice{}
  return result
}
func createReceiptFromKey() *FileInfo {
  files := &FileInfo{}
  return files
}
func addUpdateAttributes(key *Key) {
  key.UpdateShippingRecords = []string{"update"}
}
func persist(value *Invoice) {}
func archive(value *FileInfo) {}
func export() {
  current := createInvoiceFromKey()
  persist(current)
  files := createReceiptFromKey()
  archive(files)
}
func replaced() {
  current := createInvoiceFromKey()
  current = nil
  persist(current)
}
func direct() {
  persist(createInvoiceFromKey())
}
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	invoice, receipt, attributes := "Invoice", "Receipt", "Attributes"
	invoiceEntry := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{invoice}, Actual: "result", ActualAt: "names.go:7",
			EquivalentFlow: &NameResultFlow{Caller: "names.go:18", CallAt: "names.go:19", Value: "current", Consumer: "names.go:16"}},
		ResultEvidence: &ResultEvidence{Word: &invoice, Returned: "result", ReturnAt: "names.go:7", Consumer: "persist(current)", ConsumerAt: "names.go:20"}}
	invoiceWords := identifierWords("createInvoiceFromKey")
	assertTargetCheck(t, checkNameEntry(invoiceEntry, "names.go:5", invoiceWords, source), "")
	invoiceEntry.ObjectComparison.EquivalentFlow = &NameResultFlow{Caller: "names.go:29", CallAt: "names.go:30", Consumer: "names.go:16"}
	invoiceEntry.ResultEvidence.ConsumerAt = "names.go:30"
	assertTargetCheck(t, checkNameEntry(invoiceEntry, "names.go:5", invoiceWords, source), "")
	invoiceEntry.ObjectComparison.EquivalentFlow = &NameResultFlow{Caller: "names.go:24", CallAt: "names.go:25", Value: "current", Consumer: "names.go:16"}
	invoiceEntry.ResultEvidence.ConsumerAt = "names.go:27"
	assertTargetCheck(t, checkNameEntry(invoiceEntry, "names.go:5", invoiceWords, source), "покажите, как результат")

	receiptEntry := Evidence{Check: "result", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{receipt}, Actual: "files", ActualAt: "names.go:11",
			EquivalentFlow: &NameResultFlow{Caller: "names.go:18", CallAt: "names.go:21", Value: "files", Consumer: "names.go:17"}},
		ResultEvidence: &ResultEvidence{Word: &receipt, Returned: "files", ReturnAt: "names.go:11", Consumer: "archive(files)", ConsumerAt: "names.go:22"}}
	receiptWords := identifierWords("createReceiptFromKey")
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "имя обещает \"Receipt\", фактический тип результата \"*FileInfo\"")
	receiptEntry.Result, receiptEntry.ObjectComparison.EquivalentFlow = "violated", nil
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "")

	writeEntry := Evidence{Check: "writes", Result: "checked",
		ObjectComparison: &NameObjectComparison{Expected: []string{"Update", attributes}, Actual: "UpdateShippingRecords", ActualAt: "names.go:14", EquivalentAt: "names.go:14"},
		WriteEvidence:    &WriteEvidence{Word: &attributes, Target: "key.UpdateShippingRecords", Value: "update", WriteAt: "names.go:14"}}
	writeWords := identifierWords("addUpdateAttributes")
	assertTargetCheck(t, checkNameEntry(writeEntry, "names.go:13", writeWords, source), "строка должна называть \"Update Attributes\" и \"UpdateShippingRecords\"")
	writeEntry.Result, writeEntry.ObjectComparison.EquivalentAt = "violated", ""
	assertTargetCheck(t, checkNameEntry(writeEntry, "names.go:13", writeWords, source), "")

	receiptEntry.Result, receiptEntry.Kind = "pending", "flow"
	receiptEntry.Facts = map[string][]ReviewFact{"question": {{Location: "names.go:11", Text: "Как заполнить equivalent_at?"}}}
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "назовите недостающий факт")
	receiptEntry.Facts["question"][0].Text = "Что внешний потребитель делает с files?"
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "последний подтверждённый переход")
	receiptEntry.Facts["question"][0].Value = "files"
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "")
	receiptEntry.Facts["question"][0].Text = "Как внешняя интеграция использует files после возврата?"
	receiptEntry.Facts["question"][0].Location = "names.go:10"
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "покажите возврат, запись или вызов")
	receiptEntry.Facts["question"][0].Location = "names.go:22"
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "переход вне тела")
	receiptEntry.Facts["question"][0] = ReviewFact{
		Location: "names.go:14", Value: "UpdateShippingRecords", Text: "Кто использует UpdateShippingRecords за границей пакета?",
	}
	assertTargetCheck(t, checkNameEntry(receiptEntry, "names.go:9", receiptWords, source), "переход вне тела")
}

func TestNamePendingFlowChecksCallToHelper(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "flow.go"), `package sample
func addEntries() {
	writeEntries()
}
func writeEntries() {
	external.Write(entries)
}
func unrelated() {
	external.Write(entries)
}
`)
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	source := &evidenceSource{root: root, reportPath: path, files: files, cache: make(map[string][]string)}
	e := Evidence{Result: "pending", Kind: "flow", Check: "writes", Facts: map[string][]ReviewFact{
		"question": {{Location: "flow.go:6", Value: "entries", Text: "Куда внешний обработчик записывает entries?",
			Related: "flow.go:3", Via: "flow.go:5"}},
	}}
	assertTargetCheck(t, checkNameEntry(e, "flow.go:2", nil, source), "")
	e.Facts["question"][0].Via = "flow.go:8"
	assertTargetCheck(t, checkNameEntry(e, "flow.go:2", nil, source), "найдено 0")
}

func TestCoverageReportsMissingObjectsAndFindingsTogether(t *testing.T) {
	report := Report{Rules: []Rule{
		{ID: "common-style:STYLE-001", Applicability: "applicable", Result: "checked",
			Objects: []string{"createFiles", "loadRights"}, Evidence: []Evidence{
				{Object: "createFiles / loadRights", Check: "meaning", Result: "checked", Location: "export.go:10", Observation: "Имена понятны"},
			}},
		{ID: "common-architecture:ARCH-018", Applicability: "applicable", Result: "violated",
			Objects: []string{"postgres / prices", "sqlite / prices"}, Evidence: []Evidence{
				{Object: "postgres / prices", Check: "calls", Result: "checked", Location: "postgres.go:10", Observation: "Один SQL"},
				{Object: "sqlite / prices", Check: "batch", Result: "violated", Location: "sqlite.go:12", Observation: "SQL в цикле"},
			}},
		{ID: "common-architecture:ARCH-022", Applicability: "applicable", Result: "pending",
			Objects: []string{"флаг"}, Evidence: []Evidence{
				{Object: "флаг", Result: "pending", Observation: "Нужен контракт", Heading: "### Новый флаг"},
			}},
	}}
	err := checkCoverage(report, "### Другое замечание\n")
	if err == nil {
		t.Fatal("незавершённое покрытие прошло сверку")
	}
	for _, want := range []string{
		"лишнее или повторное доказательство для \"createFiles / loadRights / meaning\"",
		"createFiles / meaning: нет записи", "loadRights / meaning: нет записи", "ожидается pending",
		"sqlite / prices / batch: нарушение без замечания", "заголовок \"### Новый флаг\" отсутствует",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("нет диагностики %q в %v", want, err)
		}
	}
}

func TestNamingExpectedObjectExcludesSourceWords(t *testing.T) {
	words := identifierWords("createInvoiceFilesFromOrders")
	for _, tt := range []struct {
		expected []string
		head     string
		valid    bool
	}{
		{[]string{"Invoice", "Files"}, "Files", true},
		{[]string{"Orders"}, "Orders", false},
		{[]string{"Files", "From", "Orders"}, "Files", false},
		{[]string{"Invoice", "Files"}, "", false},
	} {
		if err := checkExpectedObject(tt.expected, tt.head, words); (err == nil) != tt.valid {
			t.Errorf("expected %v с word=%q, valid=%v: %v", tt.expected, tt.head, tt.valid, err)
		}
	}
	if !sameObjectName([]string{"Invoice", "Files"}, identifierWords("InvoiceFile")) ||
		sameObjectName([]string{"Invoice"}, identifierWords("InvoiceFile")) ||
		sameObjectName([]string{"Update", "Attributes"}, identifierWords("UpdateShippingRecords")) {
		t.Fatal("сверка объектов перепутала множественное число, представление или исходные данные")
	}
}

func TestCoverageAcceptsViolationsAndOpenQuestionsWithEvidence(t *testing.T) {
	report := Report{Rules: []Rule{
		{ID: "common-architecture:ARCH-018", Applicability: "applicable", Result: "violated",
			Objects: []string{"sqlite / prices", "postgres / prices"}, Evidence: []Evidence{
				{Object: "sqlite / prices", Check: "calls", Result: "checked", Location: "sqlite.go:12", Observation: "Один вызов драйвера на ID"},
				{Object: "sqlite / prices", Check: "inputs", Result: "checked", Location: "http.go:18", Observation: "Два разных ID допускаются"},
				{Object: "sqlite / prices", Check: "batch", Result: "violated", Location: "sqlite.go:12", Observation: "Независимые запросы идут в цикле", Heading: "### Чтение цен"},
				{Object: "postgres / prices", Check: "calls", Result: "pending", Observation: "Реализация недоступна", Heading: "### Открытый вопрос"},
				{Object: "postgres / prices", Check: "inputs", Result: "checked", Location: "http.go:18", Observation: "Два разных ID допускаются"},
				{Object: "postgres / prices", Check: "batch", Result: "pending", Observation: "Нужно установить количество запросов в адаптере"},
			}},
	}}
	if err := checkCoverage(report, "### Чтение цен\n\n### Открытый вопрос\n"); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageAcceptsNameSubchecksAndLegacyField(t *testing.T) {
	const report = `{
		"version": 1,
		"id": "MR-42",
		"rules": [{
			"id": "common-style:STYLE-001",
			"applicability": "applicable",
			"result": "violated",
			"objects": ["createInvoice"],
			"evidence": [{
				"object": "createInvoice",
				"check": "meaning",
				"result": "violated",
				"location": "invoice.go:24",
				"observation": "Имя обещает создать счёт, функция экспортирует существующий.",
				"heading": "### Имя функции"
			}, {
				"object": "createInvoice",
				"check": "result",
				"result": "violated",
				"location": "invoice.go:24; export.go:19",
				"observation": "Invoice обещает создать счёт. Вызывающий передаёт PDF готового счёта в архив.",
				"heading": "### Имя функции"
			}, {
				"object": "createInvoice",
				"check": "writes",
				"result": "not_applicable",
				"location": "invoice.go:24",
				"observation": "Функция формирует выходные байты и не изменяет вход.",
				"heading": "### Имя функции"`
	const legacyWords = `,
				"word_checks": [
					{"word":"create","meaning":"сформировать","fact":"создаёт PDF","location":"invoice.go:24","result":"checked"},
					{"word":"Invoice","meaning":"счёт","fact":"возвращает PDF","location":"export.go:19","result":"violated"}
				]`
	for _, tc := range []struct {
		name, extra string
	}{
		{name: "подпроверки на символ"},
		{name: "старое поле word_checks", extra: legacyWords},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "audit.json")
			write(t, path, report+tc.extra+`}]}]}`)
			r, err := readReport(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkCoverage(r, "### Имя функции\n"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCoverageRejectsInvalidSubcheckEvidence(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Rule)
		want string
	}{
		{
			name: "границы не закрывают параметры",
			edit: func(r *Rule) { r.Evidence = r.Evidence[:1] },
			want: "операция / parameters: нет записи в evidence",
		},
		{
			name: "одна подпроверка дважды",
			edit: func(r *Rule) { r.Evidence[2].Check = "parameters" },
			want: `повторное доказательство для "операция / parameters"`,
		},
		{
			name: "неизвестный ключ",
			edit: func(r *Rule) { r.Evidence[2].Check = "query" },
			want: `неизвестная подпроверка "query"`,
		},
		{
			name: "подмена перечня",
			edit: func(r *Rule) { r.Checks = []string{"boundaries"} },
			want: "список checks изменён",
		},
		{
			name: "нет основания неприменимости",
			edit: func(r *Rule) { r.Evidence[2].Observation = "" },
			want: "операция / binding: нет места или основания вывода",
		},
		{
			name: "открытая подпроверка при checked",
			edit: func(r *Rule) { r.Evidence[2].Result = "pending" },
			want: "ожидается pending",
		},
		{
			name: "неприменимость скрывает выполненную проверку",
			edit: func(r *Rule) { r.Applicability = "not_applicable" },
			want: "операция / parameters: неприменимость правила противоречит результату checked",
		},
		{
			name: "при открытой применимости пропущена подпроверка известного объекта",
			edit: func(r *Rule) {
				r.Applicability, r.Result = "pending", "pending"
				r.Evidence = r.Evidence[:1]
			},
			want: "операция / parameters: нет записи в evidence",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := Rule{
				ID: "common-architecture:ARCH-019", Applicability: "applicable", Result: "checked",
				Reason:  "Правка меняет отбор записей",
				Objects: []string{"операция"},
				Evidence: []Evidence{
					{Object: "операция", Check: "boundaries", Location: "repo.go:8", Observation: "Адаптер выбирает записи", Result: "checked"},
					{Object: "операция", Check: "parameters", Location: "query.sql:4", Observation: "Статус передаётся параметром", Result: "checked"},
					{Object: "операция", Check: "binding", Location: "repo.go:8", Observation: "Состав и порядок аргументов прежние", Result: "not_applicable"},
					{Object: "операция", Check: "proposed_binding", Location: "repo.go:8", Observation: "Привязка не меняется", Result: "not_applicable"},
				},
			}
			tc.edit(&rule)
			err := checkCoverage(Report{Rules: []Rule{rule}}, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ожидается %q, получено %v", tc.want, err)
			}
		})
	}
}

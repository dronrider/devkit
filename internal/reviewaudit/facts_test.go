package reviewaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalRequestIsPinnedAndCitedByPhysicalLine(t *testing.T) {
	root, boardPath := fixture(t)
	standard := filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.architecture.md")
	write(t, standard, "- **ARCH-001.** Проверяется инвариант.\n- **ARCH-023.** Обоснован выбор.\n")
	report := completed(t, root, boardPath)
	path, err := Path(root, "XR-1", false)
	if err != nil {
		t.Fatal(err)
	}
	request := filepath.Join(t.TempDir(), "REQUEST.md")
	const body = "\n# Контракт\n\n## Проверка\nВключать только согласованные флаги.\n"
	write(t, request, body)
	if err := CheckCoverageWithRequest(root, boardPath, "XR-1", "", request); err == nil {
		t.Fatal("постановка должна быть привязана при создании снимка")
	}
	if _, err := CreateWithRequest(root, path, "XR-1", "HEAD", request, false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RequestPath != request || snapshot.TaskHash != digest(body) {
		t.Fatalf("неверная привязка %q / %q", snapshot.RequestPath, snapshot.TaskHash)
	}
	report.RequestPath, report.TaskHash = snapshot.RequestPath, snapshot.TaskHash
	rule := findTargetRule(t, &report, configOptionRule)
	fact := []ReviewFact{{Location: "code.go:1", Text: "Список флагов применяется при запуске."}}
	rule.Evidence[0].Kind = "option"
	rule.Evidence[0].Facts = map[string][]ReviewFact{"input": fact, "before": fact, "after": fact, "activation": fact}
	rule.Evidence[1].Facts = map[string][]ReviewFact{"contract": {{
		Location: "task:5", Text: "Согласовано ограничение доступа.", Quote: "Включать только согласованные флаги.",
	}}}
	save(t, path, report)
	summary := filepath.Join(root, "review.md")
	write(t, summary, "# Ревью\n")
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "")
	if err := os.Remove(summary); err != nil {
		t.Fatal(err)
	}
	assertTargetCheck(t, Validate(root, path, "XR-1"), "")
	assertTargetCheck(t, ValidateAt(root, path, "XR-1", "HEAD"), "")
	write(t, summary, "# Ревью\n")
	rule.Evidence[1].Facts["contract"][0].Quote = "несуществующая цитата"
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "quote должна точно цитировать")
	rule.Evidence[1].Facts["contract"][0].Quote = "Включать только согласованные флаги."
	save(t, path, report)
	moved := filepath.Join(t.TempDir(), "moved.md")
	if err := os.Rename(request, moved); err != nil {
		t.Fatal(err)
	}
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "файл постановки")
	assertTargetCheck(t, CheckCoverageWithRequest(root, path, "XR-1", summary, moved), "")
	write(t, moved, body+"Изменено.\n")
	assertTargetCheck(t, CheckCoverageWithRequest(root, path, "XR-1", summary, moved), "постановка изменились")
	if _, err := CreateWithRequest(root, path, "XR-1", "", moved, true); err != nil {
		t.Fatal(err)
	}
	snapshot, err = readReport(path)
	if err != nil || snapshot.RequestPath != moved || snapshot.TaskHash != digest(body+"Изменено.\n") {
		t.Fatalf("refresh не привязал обновлённую постановку: %v", err)
	}
}

func TestReadCoverageRequiresAnInputWithoutConsumer(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.architecture.md"),
		"- **ARCH-016.** Проверь необходимость чтения.\n")
	write(t, filepath.Join(root, "export.py"), "def export(config):\n    features = repo.read()\n    return build(config, features)\n")
	gitT(t, root, "add", "export.py")
	gitT(t, root, "commit", "-qm", "export")
	report := completed(t, root, path)
	rule := findTargetRule(t, &report, readRule)
	const call = "export.py:2"
	const heading = "### Лишнее чтение"
	zero, one := 0, 1
	rule.Objects = []string{call}
	with := Evidence{Object: call, Check: "with_consumer", Result: "checked", Location: call,
		Observation: "Запрошен результат, зависящий от фич.",
		ReadScenario: &ReadScenario{Input: "config=feature_limit", InputAt: "export.py:1", CallAt: call,
			Consumers: []string{"export.py:3"}, Observed: &one, Expected: &one, Failure: "Экспорт прерывается.", FailureAt: call}}
	without := Evidence{Object: call, Check: "without_consumer", Result: "violated", Location: call, Heading: heading,
		Observation: "Описание запрашивает только данные клиента.",
		ReadScenario: &ReadScenario{Input: "config=customer_name", InputAt: "export.py:1", CallAt: call,
			Consumers: []string{}, Observed: &one, Expected: &zero, Failure: "Лишний отказ останавливает экспорт.", FailureAt: call}}
	summary := filepath.Join(t.TempDir(), "review.md")
	write(t, summary, heading+"\n\nПроверить ноль чтений для customer_name.\n")
	verify := func(want string) {
		t.Helper()
		save(t, path, report)
		assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), want)
	}
	rule.Evidence = []Evidence{with}
	verify("without_consumer: нет записи")
	rule.Evidence = append(rule.Evidence, without)
	rule.Result = "violated"
	verify("")
	without.ReadScenario.Input = with.ReadScenario.Input
	verify("требуют разных входов")
	without.ReadScenario.Input = "config=customer_name"
	without.ReadScenario.Expected = &one
	verify("нужен пустой consumers и expected: 0")
	without.ReadScenario.Expected = &zero
	rule.Evidence[1].Result = "checked"
	rule.Result = "checked"
	verify("статус не соответствует observed и expected")
	rule.Evidence[1].Result = "violated"
	rule.Result = "violated"
	verify("")
}

func TestCoverageRequiresReviewFacts(t *testing.T) {
	root, path := fixture(t)
	standards := os.Getenv("DEVKIT_HOME")
	write(t, filepath.Join(standards, "RULES.architecture.md"), `- **ARCH-001.** Проверяется инвариант.
- **ARCH-003.** Проверь границы модуля.
- **ARCH-005.** Проверь контракт результата.
- **ARCH-014.** Проверь классификацию отказа.
- **ARCH-015.** Проверь использование выборки.
- **ARCH-019.** Проверь параметры.
- **ARCH-023.** Обоснуй выбор в конфиге.
`)
	write(t, filepath.Join(standards, "RULES.style.md"), `- **STYLE-001.** Проверь имя.
- **STYLE-011.** Проверь цепочку вызовов.
- **STYLE-015.** Проверь комментарий.
- **STYLE-016.** Проверь описание.
`)
	write(t, filepath.Join(standards, "RULES.md"), `## Тесты обязательны

- **TEST-004.** Проверь команды и зависимости.
- **TEST-005.** Проверь силу и пересечение тестов.
`)
	write(t, filepath.Join(root, "export.py"), `# The consumer receives only explicitly approved flags.
def export(config):
    if config is None:
        return legacy()
    rows = load_rows(config)
    return render(rows, config)

def load_rows(config):
    return db.execute(query, (ACTIVE, config.first, config.second))

def render(rows, config):
    return max(row.date for row in rows)
`)
	write(t, filepath.Join(root, "query.sql"), "SELECT date FROM rights WHERE state = ? AND id IN (?, ?) ORDER BY id\n")
	write(t, filepath.Join(root, "settings.py"), "ACTIVE = 'active'\n")
	write(t, filepath.Join(root, "test_export.py"), `def test_render():
    assert result["date"] == expected_date

def test_archive():
    assert payload["date"] == expected_date
`)
	write(t, filepath.Join(root, "Makefile"), "unit:\n\tpytest -m 'not integration'\nintegration:\n\tpytest -m integration\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "proof fixture")
	report := completed(t, root, path)
	summary := filepath.Join(t.TempDir(), "review.md")
	write(t, summary, "# Ревью\n")

	type checkCase struct {
		id, check, object, kind, missing string
		facts                            map[string][]ReviewFact
	}
	const load, fallback, rendered = "export.py:5", "export.py:4", "export.py:12"
	const comment, filter, unit, integration = "export.py:1", "query.sql:1", "Makefile:2", "Makefile:4"
	const object = "экспорт даты"
	stops := true
	cases := []checkCase{
		{configOptionRule, "behavior", "flags", "list", "absent", map[string][]ReviewFact{
			"input":      {{Location: load, Text: "В данных появился включённый флаг whiteboard."}},
			"before":     {{Location: load, Text: "С прежним списком whiteboard отсутствует."}},
			"after":      {{Location: load, Text: "В расширенном списке whiteboard появится."}},
			"activation": {{Location: load, Text: "Изменение применяется при чтении нового конфига."}},
			"disabled":   {{Location: load, Text: "Выключенный whiteboard не попадает в результат."}},
			"absent":     {{Location: load, Text: "При отсутствии значения флаг пропускается."}},
		}},
		{configOptionRule, "basis", "flags", "", "contract", map[string][]ReviewFact{
			"contract": {{Location: "base@" + comment, Quote: "only explicitly approved flags", Text: "Потребитель требует явного включения."}},
		}},
		{moduleRule, "", "render", "", "caller", map[string][]ReviewFact{
			"implementation": {{Location: "export.py:11", Quote: "def render", Text: "Выбирает конечный срок."}},
			"caller":         {{Location: "export.py:6", Text: "Экспорт передаёт найденные права."}},
			"dependency":     {{Location: rendered, Text: "Политика срока берёт максимум.", Verdict: "domain"}},
			"change":         {{Location: rendered, Text: "Замена максимума минимумом меняет предметную агрегацию."}},
			"placement":      {{Location: rendered, Value: "export", Text: "Предметная агрегация остаётся рядом с экспортом."}},
		}},
		{testLevelRule, "", "test_archive", "", "dependency", map[string][]ReviewFact{
			"test":                  {{Location: "test_export.py:4", Text: "Тест доставки архива."}},
			"dependency":            {{Location: "export.py:9", Text: "Штатная операция обращается к БД."}},
			"unit_selection":        {{Location: unit, Text: "pytest исключает integration из юнитов."}},
			"integration_selection": {{Location: integration, Text: "Отдельный pytest выбирает integration."}},
		}},
		{testStrengthRule, "strength", "date", "", "assertion", map[string][]ReviewFact{
			"assertion":    {{Location: "test_export.py:2", Quote: `result["date"] == expected_date`, Text: "Сравнивает вычисленную дату."}},
			"boundary":     {{Location: "test_export.py:2", Text: "Результат вычисления."}},
			"failure_case": {{Location: rendered, Text: "Инверсия сравнения выбирает более раннюю дату."}},
		}},
		{testStrengthRule, "overlap", "date", "cross_level", "second", map[string][]ReviewFact{
			"first":          {{Location: "test_export.py:2", Related: "test_export.py:1", Quote: `result["date"] == expected_date`, Text: "Ожидает нужную дату в модели."}},
			"second":         {{Location: "test_export.py:5", Related: "test_export.py:4", Quote: `payload["date"] == expected_date`, Text: "Ожидает ту же дату в архиве."}},
			"first_level":    {{Location: "test_export.py:1", Value: "unit", Text: "Проверяет вычисление даты."}},
			"second_level":   {{Location: "test_export.py:4", Value: "integration", Text: "Проверяет доставку даты в архиве."}},
			"first_command":  {{Location: unit, Text: "pytest -m 'not integration'"}},
			"second_command": {{Location: integration, Text: "pytest -m integration"}},
			"first_input":    {{Location: "test_export.py:2", Text: "Две разные даты в модели."}},
			"second_input":   {{Location: "test_export.py:5", Text: "Две разные даты в архиве."}},
			"common_failure": {{Location: rendered, Text: "Инверсия максимума на двух датах."}},
			"guarantees":     {{Location: "test_export.py:5", Text: "Интеграционный тест подтверждает передачу даты до архива."}},
		}},
		{flowRule, "", "export / explicit config", "", "control", map[string][]ReviewFact{
			"entry":        {{Location: "export.py:2", Text: "Экспорт начинается здесь."}},
			"condition":    {{Location: "export.py:3", Text: "Явный конфиг включает новый путь."}},
			"step":         {{Location: load, Text: "Читает права, которые использует render.", Failure: &stops}},
			"use":          {{Location: "export.py:6", Related: load, Value: "rows", Verdict: "used", Text: "Передаются в render."}},
			"outcome":      {{Location: "export.py:6", Text: "На успехе возвращается вычисленная дата."}},
			"control":      {{Location: fallback, Text: "Legacy путь уже возвращается отдельно."}},
			"return_value": {{Location: "export.py:6", Value: "date", Text: "Дата возвращается напрямую."}},
			"return_error": {{Location: load, Value: "исключение", Text: "Отказ прерывает вызов до render."}},
		}},
		{selectionRule, "usage", object, "", "consumer", map[string][]ReviewFact{
			"query":    {{Location: filter, Text: "Возвращает даты активных прав."}},
			"consumer": {{Location: rendered, Text: "Из дат вычисляется максимум."}},
		}},
		{selectionRule, "order", object, "", "effect", map[string][]ReviewFact{
			"order":  {{Location: filter, Text: "Сортирует по ID."}},
			"effect": {{Location: rendered, Text: "Максимальная дата инвариантна к перестановке."}},
		}},
		{dataBoundaryRule, "boundaries", object, "", "", nil},
		{dataBoundaryRule, "parameters", object, "", "filter", map[string][]ReviewFact{
			"filter": {{Location: filter, Related: "settings.py:1", Text: "state получает доменную константу ACTIVE."}},
		}},
		{dataBoundaryRule, "binding", object, "", "argument", map[string][]ReviewFact{
			"sql":      {{Location: filter, Text: "SQL на три аргумента."}},
			"input":    {{Location: "export.py:9", Text: "Активный статус и разные ID 17, 21."}},
			"argument": {{Location: "export.py:9", Related: filter, Text: "state, первый ID, второй ID в порядке плейсхолдеров."}},
		}},
		{dataBoundaryRule, "proposed_binding", object, "", "", nil},
		{failureRule, "", load, "", "classification", map[string][]ReviewFact{
			"origin":                    {{Location: load, Text: "Чтение прав может завершиться ошибкой."}},
			"classification":            {{Location: "export.py:9", Quote: "return db.execute", Verdict: "forwarded", Text: "Сохраняется ошибка базы."}},
			"classification_definition": {{Location: "export.py:9", Quote: "db.execute", Text: "Источник исключения базы."}},
			"boundary":                  {{Location: load, Value: "исключение базы", Text: "Исключение выходит вызывающему без подписания."}},
			"cause":                     {{Location: "export.py:9", Verdict: "preserved", Text: "Исключение передаётся напрямую."}},
			"message":                   {{Location: "export.py:9", Verdict: "once", Text: "Одна исходная причина, обёртки нет."}},
		}},
		{commentRule, "", comment, "", "purpose", map[string][]ReviewFact{
			"purpose": {{Location: load, Text: "Поясняет ограничение доступа на флаги."}},
			"claim":   {{Location: load, Quote: "The consumer receives only explicitly approved flags.", Value: "load_rows(config)", Verdict: "checked", Text: "Фильтрация подтверждает утверждение."}},
		}},
		{descriptionRule, "", comment, "", "implementation", map[string][]ReviewFact{
			"statement":      {{Location: comment, Quote: "only explicitly approved flags", Text: "Описание разрешённых флагов."}},
			"implementation": {{Location: load, Text: "Выбор зависит от разрешённых полей конфигурации."}},
		}},
		{contractRule, "schema", "date", "", "schema", map[string][]ReviewFact{
			"schema": {{Location: "export.py:11", Case: "two-dates", Forms: []string{"date"}, Quote: "def render", Text: "Возвращает максимальную дату."}},
		}},
		{contractRule, "output", "date", "", "output", map[string][]ReviewFact{
			"input":  {{Location: load, Case: "two-dates", Text: "Два активных права с разными датами."}},
			"output": {{Location: rendered, Case: "two-dates", Forms: []string{"date"}, Text: "Выходит максимальная дата."}},
		}},
		{contractRule, "consumer", "date", "", "contract", map[string][]ReviewFact{
			"contract": {{Location: "task:5", Case: "two-dates", Quote: "Правильный результат.", Text: "Согласован результат экспорта."}},
		}},
	}
	initialized := make(map[string]bool)
	for _, tc := range cases {
		rule := findTargetRule(t, &report, tc.id)
		if !initialized[tc.id] {
			rule.Evidence, rule.Objects = nil, nil
			initialized[tc.id] = true
		}
		if len(rule.Objects) == 0 {
			rule.Objects = []string{tc.object}
		}
		entry := Evidence{Object: tc.object, Check: tc.check, Kind: tc.kind,
			Result: "checked", Location: load, Observation: "Вывод подтверждён доказательствами.", Facts: tc.facts}
		if tc.id == configOptionRule && tc.check == "basis" {
			entry.ListDecision = "explicit"
		}
		if tc.id == moduleRule {
			entry.Symbol = "render"
		}
		if tc.id == dataBoundaryRule && tc.check == "parameters" {
			unchanged := false
			entry.BindingChange = &unchanged
		}
		if tc.check == "proposed_binding" {
			entry.Result, entry.Observation = "not_applicable", "Изменение привязки не предложено."
		}
		if tc.id == commentRule {
			entry.CommentQuote = "only explicitly approved flags"
		}
		rule.Evidence = append(rule.Evidence, entry)
	}
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "")
	assertTargetCheck(t, Validate(root, path, "XR-1"), "")

	for _, tc := range cases {
		if tc.missing == "" {
			continue
		}
		t.Run(tc.id+"/"+tc.check, func(t *testing.T) {
			facts := tc.facts[tc.missing]
			delete(tc.facts, tc.missing)
			save(t, path, report)
			assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "заполните facts."+tc.missing)
			assertTargetCheck(t, Validate(root, path, "XR-1"), "заполните facts."+tc.missing)
			tc.facts[tc.missing] = facts
		})
	}

	testRule := findTargetRule(t, &report, testStrengthRule)
	overlap := &testRule.Evidence[1]
	second := overlap.Facts["second"]
	overlap.Facts["second"] = overlap.Facts["first"]
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "укажите два разных теста")
	overlap.Facts["second"] = second
	second[0].Quote = "assert no such statement"
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "facts.second: quote должна точно цитировать")
	second[0].Quote = `payload["date"] == expected_date`
	second[0].Related = "test_export.py:99"
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "facts.second: related: строка 99 отсутствует")
	second[0].Related = "test_export.py:4"

	basisRule := findTargetRule(t, &report, configOptionRule)
	basis := &basisRule.Evidence[1]
	basis.ListDecision = ""
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "укажите list_decision")
	basis.ListDecision = "explicit"
	basis.Facts["contract"][0].Location = comment
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "укажите цитату и независимое основание")
	contract := basis.Facts["contract"]
	delete(basis.Facts, "contract")
	const question, heading = "Нужен ли явный допуск нового флага?", "### Контракт флагов"
	basisRule.Result, basis.Result, basis.Heading = "pending", "pending", heading
	basis.ListDecision = "undecided"
	basis.Facts["question"] = []ReviewFact{{Location: load, Text: question}}
	save(t, path, report)
	write(t, summary, heading+"\n")
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "нужен вопрос из facts.question")
	write(t, summary, heading+"\n\n"+question+"\n")
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "")
	basisRule.Result, basis.Result, basis.Heading = "checked", "checked", ""
	basis.ListDecision = "explicit"
	contract[0].Location = "base@" + comment
	delete(basis.Facts, "question")
	basis.Facts["contract"] = contract

	sqlRule := findTargetRule(t, &report, dataBoundaryRule)
	parameters, proposal := &sqlRule.Evidence[1], &sqlRule.Evidence[3]
	changes := true
	parameters.BindingChange = &changes
	parameters.Kind = "collection"
	parameters.Facts["filter"][0].Parameter = "state"
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "при binding_change: true нужны отдельный разбор")
	proposal.Result = "checked"
	proposal.SQLProposal = &SQLBindingProposal{
		SQL: "SELECT date FROM rights WHERE state = {{1:state}} AND id IN ({{2:id}}, {{3:id}}) AND kind = {{4:kind}}", Style: "positional",
		Arguments: []SQLBindingArgument{
			{Parameter: "state", Value: "active"}, {Parameter: "id", IDs: []string{"17"}},
			{Parameter: "id", IDs: []string{"21"}}, {Parameter: "kind", Value: "update"},
		},
		Expected: []SQLBindingResult{{ID: "17", Rows: "Дата первой записи"}, {ID: "21", Rows: "Дата второй записи"}},
		Evidence: ReviewFact{Location: filter, Text: "Порядок проверен по вызову драйвера."},
	}
	const missingProbe = "для checked укажите probe_file с фактическим SQL и порядком аргументов драйвера"
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), missingProbe)
	assertTargetCheck(t, Validate(root, path, "XR-1"), missingProbe)

	proposal.SQLProposal.ProbeFile = "rights.json"
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "proposed_binding: probe_file:")
	probePath := filepath.Join(path+".bindings", proposal.SQLProposal.ProbeFile)
	probe := sqlBindingProbe{
		CodeHash: report.CodeHash, Object: proposal.Object, Location: proposal.Location,
		Command:   "python3 probe.py",
		SQL:       "SELECT date FROM rights WHERE state = ? AND id IN (?, ?) AND kind = ?",
		Arguments: []json.RawMessage{[]byte(`"active"`), []byte(`"17"`), []byte(`"21"`), []byte(`"update"`)},
	}
	saveProbe := func() {
		t.Helper()
		body, err := json.Marshal(probe)
		if err != nil {
			t.Fatal(err)
		}
		write(t, probePath, string(body))
	}
	probe.Arguments[1], probe.Arguments[3] = probe.Arguments[3], probe.Arguments[1]
	saveProbe()
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "аргумент драйвера 2 не совпадает")
	probe.Arguments[1], probe.Arguments[3] = probe.Arguments[3], probe.Arguments[1]
	probe.CodeHash = "old-snapshot"
	saveProbe()
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "проба должна относиться к этому снимку и запросу")
	probe.CodeHash = report.CodeHash
	probe.SQL = "SELECT date FROM other WHERE state = ? AND id IN (?, ?) AND kind = ?"
	saveProbe()
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "SQL на границе драйвера не совпадает")
	probe.SQL = "SELECT date FROM rights WHERE state = ? AND id IN (?, ?) AND kind = ?"
	saveProbe()
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "")
	assertTargetCheck(t, Validate(root, path, "XR-1"), "")
	assertTargetCheck(t, ValidateAt(root, path, "XR-1", "HEAD"), "")

	const sqlQuestion, sqlHeading = "В каком порядке драйвер получит параметры запроса?", "### Привязка запроса"
	sqlRule.Result = "pending"
	proposal.Result, proposal.Heading = "pending", sqlHeading
	proposal.Facts = map[string][]ReviewFact{"question": {{Location: filter, Text: sqlQuestion}}}
	proposal.SQLProposal.ProbeFile = ""
	write(t, summary, sqlHeading+"\n\n"+sqlQuestion+"\n")
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "")
	sqlRule.Result = "checked"
	proposal.Result, proposal.Heading, proposal.Facts = "checked", "", nil
	proposal.SQLProposal.ProbeFile = "rights.json"
	save(t, path, report)
	args := proposal.SQLProposal.Arguments
	args[0], args[1] = args[1], args[0]
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "аргумент 1 должен задавать значение для state")
	args[0], args[1] = args[1], args[0]
	results := proposal.SQLProposal.Expected
	parameters.Kind = "scalar"
	proposal.SQLProposal.Expected = results[:1]
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "два разных ID в аргументах и ожидаемые строки для каждого")
	proposal.SQLProposal.Expected = results
	args[1], args[2] = SQLBindingArgument{Parameter: "id", Value: "17"}, SQLBindingArgument{Parameter: "id", Value: "21"}
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), "для каждого входного ID нужны отдельные ожидаемые строки")
	args[1], args[2] = SQLBindingArgument{Parameter: "id", IDs: []string{"17"}}, SQLBindingArgument{Parameter: "id", IDs: []string{"21"}}
	parameters.Kind = "collection"

	errRule := findTargetRule(t, &report, failureRule)
	errRule.Objects[0], errRule.Evidence[0].Object = fallback, fallback
	errRule.Evidence[0].Facts["origin"][0].Location = fallback
	save(t, path, report)
	assertTargetCheck(t, CheckCoverage(root, path, "XR-1", summary), load+": для отказа из STYLE-011 нужна отдельная запись")
}

func TestSQLBindingProbeMatchesNumberedParametersAndArray(t *testing.T) {
	proposal := &SQLBindingProposal{
		SQL:   "SELECT id FROM rights WHERE id = ANY({{1:ids}}) AND (owner = {{2:owner}} OR parent = {{2:owner}})",
		Style: "numbered",
		Arguments: []SQLBindingArgument{
			{Parameter: "ids", IDs: []string{"17", "21"}},
			{Parameter: "owner", Value: "12"},
		},
	}
	const observed = "SELECT id FROM rights WHERE id = ANY($1) AND (owner = $2 OR parent = $2)"
	if !matchesDriverSQL(proposal, observed) {
		t.Fatal("запрос с повторным использованием номера должен совпасть")
	}
	if matchesDriverSQL(proposal, "SELECT id FROM rights WHERE id = ANY($1) AND (owner = $2 OR parent = $3)") {
		t.Fatal("другой номер параметра должен отклоняться")
	}
	if !matchesDriverArgument(proposal.Arguments[0], json.RawMessage(`["17","21"]`)) ||
		!matchesDriverArgument(proposal.Arguments[1], json.RawMessage(`12`)) {
		t.Fatal("массив ID и числовой аргумент должны совпасть")
	}
	if matchesDriverArgument(proposal.Arguments[0], json.RawMessage(`["21","17"]`)) {
		t.Fatal("переставленные ID в массиве должны отклоняться")
	}
}

func TestSQLBindingProbeMatchesSQLiteSlots(t *testing.T) {
	const literal = `SELECT '?8 '' ?7', "?6", [?5] /* ?9 */ -- ?10
FROM rights WHERE `
	for _, tc := range []struct {
		name, template, observed string
		want                     bool
	}{
		{
			name:     "scalar slots before expanded IDs",
			template: "state={{1:state}} AND child={{2:child}} AND kind={{3:kind}} AND id IN ({{4:id}},{{5:id}})",
			observed: "state=?1 AND child=?2 AND kind=?3 AND id IN (?,?)",
			want:     true,
		},
		{
			name:     "scalar slot collides with the second ID",
			template: "id IN ({{1:id}},{{2:id}}) AND state={{3:state}} AND child={{4:child}} AND kind={{5:kind}}",
			observed: "id IN (?,?) AND state=?2 AND child=?3 AND kind=?4",
		},
		{
			name:     "anonymous slots follow the highest explicit slot",
			template: "state={{2:state}} AND child={{1:child}} AND kind={{3:kind}}",
			observed: "state=?2 AND child=?1 AND kind=?",
			want:     true,
		},
		{
			name:     "unrecorded placeholder shifts later slots",
			template: "state=?1 AND child={{1:child}}",
			observed: "state=?1 AND child=?",
		},
		{
			name:     "marker in a literal is not an argument",
			template: "state='{{1:state}}'",
			observed: "state='?1'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposal := &SQLBindingProposal{SQL: literal + tc.template, Style: "sqlite"}
			if got := matchesDriverSQL(proposal, literal+tc.observed); got != tc.want {
				t.Fatalf("получено %t, ожидается %t", got, tc.want)
			}
		})
	}
}

func TestListDecisionDistinguishesOptionsFromRequirements(t *testing.T) {
	const task = "# Задача\n## Варианты реализации\n### YAML\nПользователь может выбрать флаги.\n## DoD\nНовый флаг появляется после явного допуска.\n"
	behavior := Evidence{Kind: "list"}
	basis := Evidence{ListDecision: "explicit", Result: "checked", Facts: map[string][]ReviewFact{
		"contract": {{Location: "task:4"}},
	}}
	assertTargetCheck(t, checkListDecision(behavior, basis, task), "варианты реализации не подтверждают")
	basis.Facts["contract"][0].Location = "task:6"
	assertTargetCheck(t, checkListDecision(behavior, basis, task), "")
	basis.ListDecision, basis.Result = "undecided", "pending"
	basis.Facts["contract"][0].Location = "task:4"
	assertTargetCheck(t, checkListDecision(behavior, basis, task), "")
}

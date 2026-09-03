package main

import (
	"strings"
	"testing"
)

// branchCodeOnly заводит фичеветку с одним коммитом кода и без тестового файла:
// негатив для ворот тестов. Контраст с branchFor, который тест кладёт.
func branchCodeOnly(t *testing.T, root, branch, file string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", branch, "main")
	write(t, root, file, "новое\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: правка без теста")
}

// TestMergeTestsGateAcceptance: признак приёмки из постановки DK-092. Задача
// без теста в диффе не сливается, та же задача с тестом сливается. Тип задачи
// task и файл со сценарием снимают прочие ворота, чтобы число переменных в
// проверке было ровно одно: есть тест в диффе или нет.
func TestMergeTestsGateAcceptance(t *testing.T) {
	// Ветка с кодом, но без теста: ворот отказывает.
	root, _ := setup(t, rowInProg3, "")
	taskWithScenario(t, root, "XR-003")
	branchCodeOnly(t, root, "xr-003-fix", "feature.txt")
	head := gitT(t, root, "rev-parse", "main")
	_, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "нет тестовых файлов") {
		t.Fatalf("ветка без теста должна отбиваться воротом: %v", err)
	}
	if gitT(t, root, "rev-parse", "main") != head {
		t.Fatal("отказ по вороту тестов случился после слияния, а не до него")
	}

	// Та же задача с тестом в диффе сливается. branchFor кладёт тест по ID.
	root, _ = setup(t, rowInProg3, "")
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"}); err != nil {
		t.Fatalf("ветка с тестом должна сливаться: %v", err)
	}
}

// TestMergeTestsGateRustLayout: ветка Rust-проекта, где тест лежит по
// соглашению Cargo в tests/ рядом с src, сливается. Ворот знал только имена
// файлов Go, Python и shell, и на xr-proxy отбивал слияние правки с честным
// интеграционным тестом, предлагая загасить себя пометкой-исключением.
func TestMergeTestsGateRustLayout(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	taskWithScenario(t, root, "XR-003")
	gitT(t, root, "checkout", "-qb", "xr-003-fix", "main")
	write(t, root, "xr-core/src/presets.rs", "fn apply() {}\n")
	write(t, root, "xr-core/tests/preset_signature.rs", "#[test]\nfn rejects() {}\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-003 правка с тестом в tests/")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"}); err != nil {
		t.Fatalf("ветка с тестом в tests/ должна сливаться: %v", err)
	}
}

// TestMergeTestsGateOverride: пометка-исключение в файле задачи гасит ворот
// тестов. Так сливаются правки, к которым тест неприменим (правка конфигурации,
// перенос константы), не снимая ворот для остальных задач.
func TestMergeTestsGateOverride(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	write(t, root, "docs/tasks/XR-003.md", "# XR-003\n\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n"+fixtureReviewLevel+"\n## Ход работы\n\n- Исключение: тесты (правка конфигурации, тест неприменим)\n")
	gitT(t, root, "add", "docs/tasks/XR-003.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 пометка тестов")
	branchCodeOnly(t, root, "xr-003-fix", "config.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"}); err != nil {
		t.Fatalf("пометка-исключение должна гасить ворот тестов: %v", err)
	}
}

// TestMergeTestsGateDocsOnly: бескодовая ветка (только docs/) ворот тестов не
// требует: кода нет, тест не нужен. Ворот снимается, а не падает.
func TestMergeTestsGateDocsOnly(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	write(t, root, "docs/tasks/XR-003.md", "# XR-003\n\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n"+fixtureReviewLevel)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 файл задачи")
	gitT(t, root, "checkout", "-qb", "xr-003-docs", "main")
	write(t, root, "docs/lld/dk.md", "# LLD\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: XR-003 правка LLD")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatalf("бескодовая ветка не должна требовать тест: %v", err)
	}
}

// TestMergeScenarioGateOverride: пометка-исключение гасит ворот сценария, когда
// проверка неприменима (задача проверяется вместе с другой, разбор без выката).
func TestMergeScenarioGateOverride(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	write(t, root, "docs/tasks/XR-003.md", "# XR-003\n\n## Ход работы\n\n- Исключение: сценарий (проверяется вместе с XR-001)\n"+fixtureReviewLevel)
	gitT(t, root, "add", "docs/tasks/XR-003.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 пометка сценария")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatalf("пометка-исключение должна гасить ворот сценария: %v", err)
	}
}

// TestIsTestFile: предикат опознаёт тест по соглашениям об именах для языков
// проекта (Go, Python, shell), не привязываясь к одному языку, и не считает
// тестом файл, который лишь содержит слово test. Путь берётся с каталогом.
func TestIsTestFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"advice_test.go", true},
		{"tools/shipctl/ops_test.go", true},
		{"test_helpers.py", true},
		{"helpers_test.py", true},
		{"test_run.sh", true},
		{"run_test.sh", true},
		// Rust держит интеграционный тест не в имени файла, а в каталоге
		// tests/ рядом с src: xr-proxy на этом ловил ложный отказ ворота.
		{"xr-core/tests/preset_signature.rs", true},
		{"tests/smoke.rs", true},
		{"crate/src/lib_test.rs", true},
		// JVM кладёт тест в src/test, соседний src/main тестом не считается.
		{"app/src/test/java/com/xrproxy/app/GroupsTest.kt", true},
		{"app/src/test/java/com/xrproxy/app/Groups.java", true},
		{"app/src/main/java/com/xrproxy/app/Groups.kt", false},
		// Каталог опознаётся вместе с расширением: заглушка стенда и данные
		// примера рядом с тестом сами тестом не становятся.
		{"xr-setup/tests/fixtures/nft.sh", false},
		{"xr-core/tests/data/preset.json", false},
		// Cargo .rs в подпапке tests/ это модули-хелперы, не интеграционные
		// тесты. Верхний уровень tests/ собирается в отдельный крейт.
		{"tests/common/mod.rs", false},
		{"tests/fixtures/data.rs", false},
		{"tests/helpers/mock_server.rs", false},
		{"tests", false},
		{"advice.go", false},
		{"code.txt", false},
		{"README.md", false},
		{"test.go", false},
		{"testdata_fix.go", false},
		{"feature_test.go.bak", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isTestFile(c.path); got != c.want {
			t.Errorf("isTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestHasException: пометка-исключение разбирается по тем же правилам, что и
// override в pick: маркерная строка, ворот по имени, пояснение в скобках
// отбрасывается. Цитата в ограждённом блоке и голая проза без маркера ворот не
// гасят. Регистр нестрогий, ворота независимы.
func TestHasException(t *testing.T) {
	if !hasException("- Исключение: тесты (правка доки)\n", "тесты") {
		t.Error("пометка не найдена")
	}
	if !hasException("- Исключение: Тесты\n", "тесты") {
		t.Error("регистр должен быть нестрогим")
	}
	if !hasException("- Исключение: regcheck\n", "regcheck") {
		t.Error("пометка без скобок должна работать")
	}
	if hasException("- Исключение: тесты (причина)\n", "сценарий") {
		t.Error("пометка одного ворот не должна гасить другой")
	}
	if hasException("```\n- Исключение: тесты (цитата)\n```\n", "тесты") {
		t.Error("цитата в ограждении не должна гасить ворот")
	}
	if hasException("Исключение: тесты (в прозе)\n", "тесты") {
		t.Error("проза без маркера не должна гасить ворот")
	}
	// Пояснение без скобок после имени ворот не подхватывается: имя ворот это
	// первое слово, остальное должно идти в скобках. Так «тесты» и «тесты в
	// другом репо» не расходятся в написании.
	if hasException("- Исключение: тесты в другом репо\n", "тесты") {
		t.Error("хвост без скобок не должен подходить под имя ворот")
	}
}

// reviewDocFor кладёт файл задачи со сценарием и заданным разделом «Ревью» и
// коммитит его на main: ветка задачи поднимается отсюда, и ворот читает файл
// уже в её дереве.
func reviewDocFor(t *testing.T, root, id, review string) {
	t.Helper()
	write(t, root, "docs/tasks/"+id+".md", "# "+id+"\n\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n"+review)
	gitT(t, root, "add", "docs/tasks/"+id+".md")
	gitT(t, root, "commit", "-qm", "docs(tasks): "+id+" файл задачи")
}

// TestMergeReviewLevelGateMissingSection: файл задачи без раздела «Ревью»
// слиянию не годится. Ревью, прошедшее мимо скилла, машинного следа не
// оставляет, и молчание тут неотличимо от пропущенного ревью.
func TestMergeReviewLevelGateMissingSection(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	reviewDocFor(t, root, "XR-003", "")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	head := gitT(t, root, "rev-parse", "main")
	_, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err == nil || !strings.Contains(err.Error(), "нет строки уровня ревью") {
		t.Fatalf("задача без раздела «Ревью» должна отбиваться воротом: %v", err)
	}
	if gitT(t, root, "rev-parse", "main") != head {
		t.Fatal("отказ по вороту случился после слияния, а не до него")
	}
}

// TestMergeReviewLevelGateNoLevelLine: раздел «Ревью» с одними замечаниями
// ворот не открывает. Замечания пишет и ревью мимо скилла, уровень пишет
// только скилл.
func TestMergeReviewLevelGateNoLevelLine(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	reviewDocFor(t, root, "XR-003", "\n## Ревью\n\n- нейминг: отклонено, стиль проекта\n")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err == nil ||
		!strings.Contains(err.Error(), "нет строки уровня ревью") {
		t.Fatalf("раздел без строки уровня должен отбиваться: %v", err)
	}
}

// TestMergeReviewLevelGatePasses: строка уровня первой строкой раздела ворот
// открывает, а нулевой уровень проходит наравне с прочими: это записанный с
// причиной пропуск ревью, а не молчание.
func TestMergeReviewLevelGatePasses(t *testing.T) {
	for _, level := range []string{"2", "0"} {
		root, _ := setup(t, rowInProg3, "")
		reviewDocFor(t, root, "XR-003", "\n## Ревью\n\nУровень "+level+" до 1a2b3c4: тронут tools/shipctl.\n")
		branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
		if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
			t.Fatalf("уровень %s должен открывать ворот: %v", level, err)
		}
	}
}

// TestMergeReviewLevelGateOverride: пометка-исключение гасит ворот следа ревью
// тем же порядком, что и соседние.
func TestMergeReviewLevelGateOverride(t *testing.T) {
	root, _ := setup(t, rowInProg3, "")
	reviewDocFor(t, root, "XR-003", "\n## Ход работы\n\n- Исключение: ревью (правка ведётся в main мимо ветки)\n")
	branchFor(t, root, "XR-003", "xr-003-fix", "feature.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatalf("пометка-исключение должна гасить ворот следа ревью: %v", err)
	}
}

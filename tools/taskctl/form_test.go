package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/stage"
)

// TestAddPutsFormSkeleton: болванка task и bug несёт заголовки формы, а раздел
// «Приёмка» от неагентского вида встаёт на своё место по форме, а не в хвост
// файла за «Ходом работы».
func TestAddPutsFormSkeleton(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{Title: "Форменная", Type: "task", Rank: "0+1+1+0+1", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# XR-008: Форменная\n\n## Что происходит\n\n## Чего хотим\n\n## DoD\n\n## Ранг\n\n## Ход работы\n"
	if string(data) != want {
		t.Fatalf("болванка не по форме:\n%s", data)
	}
	if _, err := cmdAdd(root, AddParams{Title: "С барьером", Type: "bug", Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-009.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "## Приёмка\n\n- вид: user\n- барьер «глаза»:\n") {
		t.Fatalf("раздел «Приёмка» не дописан:\n%s", body)
	}
	if strings.Index(body, acceptanceHeading) > strings.Index(body, stageSection) {
		t.Fatalf("«Приёмка» встала после «Хода работы»:\n%s", body)
	}
}

// TestLintFindsFormOrder: переставленные разделы файла задачи это находка lint,
// а заголовок внутри блока кода разметкой файла не считается.
func TestLintFindsFormOrder(t *testing.T) {
	root := setup(t)
	path := filepath.Join(root, "docs", "tasks", "XR-001.md")
	body := "# XR-001: Средняя\n\n## Выкат\n\n- слито: abc1234\n\n## DoD\n\nСделано.\n" + fixtureScenario
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	hit := ""
	for _, f := range finds {
		if strings.Contains(f, "XR-001.md") && strings.Contains(f, "порядок разделов") {
			hit = f
		}
	}
	if hit == "" {
		t.Fatalf("порядок разделов не проверен:\n%s", strings.Join(finds, "\n"))
	}
	if !strings.Contains(hit, formDoc) {
		t.Errorf("находка не называет страницу формы: %s", hit)
	}
	quoted := "# XR-001: Средняя\n\n## DoD\n\nСделано.\n\n```\n## Выкат\n## DoD\n```\n" + fixtureScenario
	if err := os.WriteFile(path, []byte(quoted), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "XR-001.md") && strings.Contains(f, "порядок разделов") {
			t.Fatalf("заголовок из блока кода принят за раздел: %s", f)
		}
	}
}

// TestReviewSectionGoesByForm: первое замечание на файл со сценарием проверки
// заводит раздел «Ревью» выше сценария, а не в хвосте файла. Хвост давал бы
// находку сторожа порядка на каждой отревьюенной задаче.
func TestReviewSectionGoesByForm(t *testing.T) {
	root := setup(t)
	path := filepath.Join(root, "docs", "tasks", "XR-001.md")
	body := "# XR-001: Средняя\n\n## DoD\n\nСделано.\n" + fixtureScenario + fixtureVerification
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewAdd(root, "XR-001", "порядок разделов проверен", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "- порядок разделов проверен") {
		t.Fatalf("замечание не записано:\n%s", text)
	}
	if strings.Index(text, reviewHeading) > strings.Index(text, scenarioSection) {
		t.Fatalf("раздел «Ревью» встал после сценария:\n%s", text)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "XR-001.md") && strings.Contains(f, "порядок разделов") {
			t.Fatalf("сторож порядка краснеет после review add: %s", f)
		}
	}
}

// draftSCQA это черновик с подразделами по форме: первая строка заголовок,
// дальше ситуация, осложнение, вопрос и гипотеза.
const draftSCQA = "уведомитель шумит из песочницы\n\n### Ситуация\n\nхук старта берёт адрес чата из окружения.\n\n### Осложнение\n\nвторая сессия перебивает первую.\n\n### Вопрос\n\nбрать адрес из реестра чатов или из tmux?\n\n### Гипотеза\n\nадрес берётся из реестра чатов, там он лежит с признаком живости.\n"

// TestDraftPromotedByForm: путь черновик -> add --id -> сценарий -> move check
// даёт файл по форме. Подразделы черновика раскладываются по разделам
// TASKFORM.md, H1 берётся из --title, шапка и «## Черновик» как есть не
// переезжают, а «Ход работы» стоит до «Сценария проверки», так что lint после
// первого перехода зелёный. До правки файл переезжал как есть, пакет этапов
// вставал в хвост за сценарием, и первый же move check делал находку lint.
func TestDraftPromotedByForm(t *testing.T) {
	stageHome(t)
	root := setup(t)
	if _, err := cmdDraft(root, draftSCQA, "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	if _, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Оформленная", Type: "bug", Rank: "25+4+2+5+2", Cost: "S", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs", "tasks", "XR-008.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	want := "# XR-008: Оформленная\n\n" +
		"## Что происходит\n\nхук старта берёт адрес чата из окружения.\n\nвторая сессия перебивает первую.\n\n" +
		"## Чего хотим\n\nадрес берётся из реестра чатов, там он лежит с признаком живости.\n\n" +
		"## Развилки\n\nбрать адрес из реестра чатов или из tmux?\n\n" +
		"## DoD\n\n## Ранг\n\n`25+4+2+5+2 = 38`, P2.\n\n## Ход работы\n\n- Из черновика XR-008 «уведомитель шумит из песочницы»: записан " + today() + ", оформлен " + today() + ".\n"
	if body != want {
		t.Fatalf("файл из черновика не по форме:\n%s\nждал:\n%s", body, want)
	}
	if err := os.WriteFile(path, append(data, []byte(fixtureScenario+fixtureVerification)...), 0o644); err != nil {
		t.Fatal(err)
	}
	openStages(t, root, "XR-008", stage.Dev)
	if _, err := cmdMove(root, "XR-008", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-008", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	body = string(data)
	if strings.Index(body, stageSection) > strings.Index(body, scenarioSection) {
		t.Fatalf("«Ход работы» встал после «Сценария проверки»:\n%s", body)
	}
	if !strings.Contains(body, "- Разработка: субагент opus/high по вердикту pick") {
		t.Fatalf("этап не записан:\n%s", body)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("lint после оформления и перехода: %v", finds)
	}
}

// TestDraftWithoutSubsectionsGoesToSituation: черновик без разметки (старая
// запись или мысль одним абзацем со stdin) целиком ложится в «Что происходит»
// без копии заголовка, которой старые черновики открывали тело; причины
// грумера из «## Ранг» встают под формулой, а незнакомый раздел грумера
// после «Ранга» и до контрактной части.
func TestDraftWithoutSubsectionsGoesToSituation(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "уведомитель шумит из песочницы\n\nхук берёт адрес из окружения, вторая сессия перебивает первую.", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	old := "# XR-008: уведомитель шумит из песочницы\n\nзаписан 2026-08-01\n\n## Черновик\n\nуведомитель шумит из песочницы\n\nстарая запись одним абзацем.\n\n## Смежное\n\nDK-430 про реестр.\n\n## DoD\n\nшум ушёл.\n\n## Ранг\n\n- Серьёзность 25: шум.\n"
	if err := os.WriteFile(draftFile(root, "XR-008"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{ID: "XR-008", Title: "Тихий уведомитель", Type: "bug", Rank: "25+4+2+5+2", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# XR-008: Тихий уведомитель\n\n## Что происходит\n\nстарая запись одним абзацем.\n\n## Чего хотим\n\n## DoD\n\nшум ушёл.\n\n## Ранг\n\n`25+4+2+5+2 = 38`, P2.\n\n- Серьёзность 25: шум.\n\n## Смежное\n\nDK-430 про реестр.\n\n## Ход работы\n\n- Из черновика XR-008 «уведомитель шумит из песочницы»: записан 2026-08-01, оформлен " + today() + ".\n"
	if string(data) != want {
		t.Fatalf("старый черновик не по форме:\n%s\nждал:\n%s", data, want)
	}
}

// TestMoveStagesGoByForm: у файла без «Хода работы» (заведён до формы или
// LLD) пакет этапов заводит раздел по форме перед «Сценарием проверки», а не в
// хвост файла, и lint после перехода зелёный.
func TestMoveStagesGoByForm(t *testing.T) {
	stageHome(t)
	root := setup(t)
	path := taskFilePath(root, "XR-002")
	if err := os.WriteFile(path, []byte("# XR-002: Без хода работы\n\n## DoD\n\nсделано.\n"+fixtureScenario), 0o644); err != nil {
		t.Fatal(err)
	}
	openStages(t, root, "XR-002", stage.Dev)
	if _, err := cmdMove(root, "XR-002", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# XR-002: Без хода работы\n\n## DoD\n\nсделано.\n\n## Ход работы\n\n- Разработка: ") {
		t.Fatalf("«Ход работы» не по форме:\n%s", data)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("lint после перехода: %v", finds)
	}
}

// TestFileAddsStagesByForm: у файла task или bug без «Хода работы» file
// дописывает раздел на месте по форме, а не переписывает файл; повторный
// вызов ничего не меняет.
func TestFileAddsStagesByForm(t *testing.T) {
	root := setup(t)
	path := taskFilePath(root, "XR-002")
	old := "# XR-002: Без хода работы\n\n## DoD\n\nсделано.\n" + fixtureScenario
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdFile(root, "XR-002", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "«Ход работы» дописан по форме") {
		t.Fatalf("сообщение: %q", msg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# XR-002: Без хода работы\n\n## DoD\n\nсделано.\n\n## Ход работы\n" + fixtureScenario
	if string(data) != want {
		t.Fatalf("файл после file:\n%s\nждал:\n%s", data, want)
	}
	if msg, err = cmdFile(root, "XR-002", CommitOpts{}); err != nil || !strings.Contains(msg, "уже есть и файл, и ссылка") {
		t.Fatalf("повторный file: %q, %v", msg, err)
	}
}

// TestDraftBodyKeepsOwnSections: текст без «###», но с «## DoD» внутри, даёт
// подразделы до «## DoD», а не за ним; иначе оформление уносило бы пустые
// «Осложнение», «Вопрос» и «Гипотезу» в раздел DoD файла задачи.
func TestDraftBodyKeepsOwnSections(t *testing.T) {
	got := draftBodySection("хук берёт адрес из окружения.\n\n## DoD\n\nшум ушёл.\n")
	want := "## Черновик\n\n### Ситуация\n\nхук берёт адрес из окружения.\n\n### Осложнение\n\n### Вопрос\n\n### Гипотеза\n\n## DoD\n\nшум ушёл.\n"
	if got != want {
		t.Fatalf("тело черновика:\n%s\nждал:\n%s", got, want)
	}
	file := renderTaskFromDraft("XR-008", "Тихий", "`25+4+2+5+2 = 38`, P2.", "# XR-008: шум\n\n"+got, "2026-08-18")
	if !strings.Contains(file, "## DoD\n\nшум ушёл.\n\n## Ранг\n") || strings.Contains(file, "### ") {
		t.Fatalf("подразделы уехали в DoD или в файл задачи:\n%s", file)
	}
}

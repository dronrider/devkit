package main

import (
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/taskform"
)

const standSkill = "kit/skills/prose/SKILL.md"

// standSkillText это агентский файл стенда в двух редакциях: сеяной и правленой
// веткой. Разделы второго уровня тут не украшение, ворота считают тронутым
// именно раздел.
const standSkillText = "# prose\n\n## Кто зовёт\n\nСкилл зовут до письма.\n\n## Как звать\n\nКоманда одна.\n"

// standSeed кладёт на main агентский файл и сценарий стенда с предметом на его
// раздел. Без каталога сценариев ворота молчат (проект не devkit), поэтому
// сеется он всем тестам ворот.
func standSeed(t *testing.T, root, subject string) {
	t.Helper()
	write(t, root, standSkill, standSkillText)
	write(t, root, scenarioDir+"/14-prose-sample.md",
		"# выборка эталонов\n\nконец: любой\nпредмет: "+subject+"\n\n## Промпт\n\nНапиши абзац.\n\n## Проверка\n\n```sh\ntrue\n```\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed: скилл и сценарий стенда")
}

// standBranch заводит ветку задачи с правкой агентского файла и тестом: без
// теста слияние отбили бы ворота теста, а предмет теста тут не они.
func standBranch(t *testing.T, root, skillText string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, standSkill, skillText)
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка скилла")
}

// standMark дописывает в файл задачи отметку стенда с отпечатком предметов
// сценария, снятым с дерева ветки, и коммитит её туда же.
func standMark(t *testing.T, root string, m taskform.StandMark, tail string) {
	t.Helper()
	doc := "# XR-001: починка бага\n\n## Сценарий проверки\n\nАгентский: `git log -1`.\n" +
		fixtureReviewLevel + "\n## Проверка\n\n" + taskform.StandLine(m, time.Now(), tail) + "\n"
	write(t, root, "docs/tasks/XR-001.md", doc)
	gitT(t, root, "add", "docs/tasks/XR-001.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 отметка стенда")
}

// standPrint считает отпечаток предметов по дереву ветки: тот же, что стенд
// кладёт в отметку после прогона.
func standPrint(t *testing.T, root, branch, subject string) string {
	t.Helper()
	subs, err := obey.ParseSubjects(subject)
	if err != nil {
		t.Fatal(err)
	}
	print, err := obey.PrintFrom(treeReader(root, branch), subs)
	if err != nil {
		t.Fatal(err)
	}
	return print
}

// TestStandGateNoScenario: правка скилла без сценария стенда отбивается, и
// отказ называет файл с разделом, из-за которого встал.
func TestStandGateNoScenario(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, "kit/skills/review/SKILL.md")
	// Предмет сеяного сценария указывает на чужой файл, поэтому дерево обязано
	// его иметь: осиротевший предмет ловится отдельным тестом.
	write(t, root, "kit/skills/review/SKILL.md", "# review\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed: чужой скилл")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "нет сценария стенда") ||
		!strings.Contains(err.Error(), standSkill+" «Как звать»") {
		t.Fatalf("правка скилла без сценария должна отбиваться с именем раздела: %v", err)
	}
}

// TestStandGateException: пометка «- Исключение: стенд» гасит ворота, как и у
// четырёх соседних.
func TestStandGateException(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, "kit/skills/review/SKILL.md")
	write(t, root, "kit/skills/review/SKILL.md", "# review\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed: чужой скилл")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	write(t, root, "docs/tasks/XR-001.md", "# XR-001: починка бага\n\n## Сценарий проверки\n\nАгентский: `git log -1`.\n"+
		fixtureReviewLevel+"\n## Ход работы\n\n- Исключение: стенд (правка формулировки, шаги не менялись)\n")
	gitT(t, root, "add", "docs/tasks/XR-001.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 пометка стенда")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("пометка-исключение должна гасить ворота стенда: %v", err)
	}
}

// TestStandGateNoMark: сценарий на раздел есть, а следа прогона в файле задачи
// нет. Отказ печатает команду прогона с назначенной базой.
func TestStandGateNoMark(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "сценарий не гонялся") ||
		!strings.Contains(err.Error(), "--base старый") {
		t.Fatalf("правка раздела без следа прогона должна отбиваться: %v", err)
	}
}

// TestStandGatePasses: сценарий и зачтённый след с той же базой и сошедшимся
// отпечатком открывают ворота.
func TestStandGatePasses(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	standMark(t, root, taskform.StandMark{
		Tree: "a1b2c3d", Print: standPrint(t, root, "xr-001-fix", standSkill+" «Как звать»"),
		Base: "старый", Tier: "mini", Repeats: 5, Scenarios: []string{"14-prose-sample"},
	}, "зачтён")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("зачтённый след должен открывать ворота: %v", err)
	}
}

// TestStandGateWrongBase: прогон шёл с базой «пусто», а разделу назначена
// «старый». Зачёт с базой «старый» мягче, и подменять им прогон нельзя.
func TestStandGateWrongBase(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	standMark(t, root, taskform.StandMark{
		Tree: "a1b2c3d", Print: standPrint(t, root, "xr-001-fix", standSkill+" «Как звать»"),
		Base: "пусто", Tier: "mini", Repeats: 5, Scenarios: []string{"14-prose-sample"},
	}, "зачтён")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "другой базой") {
		t.Fatalf("прогон с чужой базой ворота открывать не должен: %v", err)
	}
}

// TestStandGateStalePrint: текст предмета правили после прогона, отпечаток не
// сходится, и след устарел.
func TestStandGateStalePrint(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	standMark(t, root, taskform.StandMark{
		Tree: "a1b2c3d", Print: "0badbeef",
		Base: "старый", Tier: "mini", Repeats: 5, Scenarios: []string{"14-prose-sample"},
	}, "зачтён")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "отпечаток отметки не совпал") {
		t.Fatalf("устаревший след ворота открывать не должен: %v", err)
	}
}

// TestStandGateFailedMark: красная отметка «- Стенд не зачтён:» ворота не
// открывает, иначе провал прогона стоил бы столько же, сколько его отсутствие.
func TestStandGateFailedMark(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	standBranch(t, root, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	standMark(t, root, taskform.StandMark{
		Failed: true, Tree: "a1b2c3d", Print: standPrint(t, root, "xr-001-fix", standSkill+" «Как звать»"),
		Base: "старый", Tier: "mini", Repeats: 5, Scenarios: []string{"14-prose-sample"},
	}, "просадка 14-prose-sample, ворота закрыты")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "нет зачтённого следа стенда") {
		t.Fatalf("красная отметка ворота открывать не должна: %v", err)
	}
}

// TestStandGateNewSection: раздел, которого на main не было, требует базу
// «пусто». Новый текст едет в контекст каждой сессии, и вопрос к нему «есть ли
// польза», а не «не стало ли хуже».
func TestStandGateNewSection(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill)
	standBranch(t, root, standSkillText+"\n## Чего не делать\n\nНе звать скилл после письма.\n")
	// Пустая строка перед новым заголовком относится к прежнему разделу, и он
	// закрыт своим прогоном: спрос тут именно с нового раздела.
	standMark(t, root, taskform.StandMark{
		Tree: "a1b2c3d", Print: standPrint(t, root, "xr-001-fix", standSkill),
		Base: "старый", Tier: "mini", Repeats: 5, Scenarios: []string{"14-prose-sample"},
	}, "зачтён")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "--base пусто") ||
		!strings.Contains(err.Error(), "«Чего не делать»") {
		t.Fatalf("новому разделу должна назначаться база «пусто»: %v", err)
	}
}

// TestStandGateDeletedLines: ханк чистого удаления внутри уцелевшего раздела это
// правка раздела с базой «старый»: снятая строка правила меняет поведение не
// меньше дописанной.
func TestStandGateDeletedLines(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	standBranch(t, root, strings.Replace(standSkillText, "\nКоманда одна.\n", "", 1))
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "«Как звать»") ||
		!strings.Contains(err.Error(), "--base старый") {
		t.Fatalf("снятая строка раздела должна требовать прогона с базой «старый»: %v", err)
	}
}

// TestStandGateDroppedSection: раздела нет в новой редакции, и ворота на него не
// встают. Доказательство удаления это прогон ревизии с базой «пусто», а
// сценарий уходит или перепривязывается той же веткой.
func TestStandGateDroppedSection(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Кто зовёт»")
	standBranch(t, root, "# prose\n\n## Кто зовёт\n\nСкилл зовут до письма.\n")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "«Кто зовёт»") {
		t.Fatalf("уцелевший раздел с правкой должен требовать след, а исчезнувший нет: %v", err)
	}
	if strings.Contains(err.Error(), "«Как звать»") {
		t.Fatalf("на исчезнувший раздел ворота вставать не должны: %v", err)
	}
}

// TestStandGateOrphanSubject: ветка удалила файл, на который смотрит сценарий.
// Прогон такого сценария зеленел бы на тексте, которого больше нет.
func TestStandGateOrphanSubject(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	gitT(t, root, "rm", "-q", standSkill)
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 скилл убран")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "остался без предмета") {
		t.Fatalf("осиротевший предмет сценария должен отбивать слияние: %v", err)
	}
}

// TestStandGateSilent: ворота молчат на ветке без агентских файлов и в проекте,
// где сценариев стенда нет вовсе.
func TestStandGateSilent(t *testing.T) {
	// Дифф без агентских файлов.
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("ветка без правки промптов ворота стенда не касается: %v", err)
	}

	// Проект не devkit: каталога сценариев в дереве нет.
	root, _ = setup(t, rowInProg, "")
	standBranch(t, root, standSkillText+"\n## Чего не делать\n\nНе звать скилл после письма.\n")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("без сценариев в дереве ворота стенда молчат: %v", err)
	}
}

// TestStandGateMultiScenarioMark: один прогон закрывает два сценария разом
// (`--for` берёт несколько путей), и отпечаток в его отметке снят с объединения
// предметов обоих. Ворота, считавшие отпечаток по одному покрывающему
// сценарию, отбивали такое слияние словами про несошедшийся отпечаток.
func TestStandGateMultiScenarioMark(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	second := "kit/skills/review/SKILL.md"
	secondText := "# review\n\n## Как читать\n\nЧитают дифф целиком.\n"
	write(t, root, standSkill, standSkillText)
	write(t, root, second, secondText)
	write(t, root, scenarioDir+"/14-prose-sample.md",
		"# выборка эталонов\n\nконец: любой\nпредмет: "+standSkill+" «Как звать»\n\n## Промпт\n\nНапиши абзац.\n\n## Проверка\n\n```sh\ntrue\n```\n")
	write(t, root, scenarioDir+"/15-review-order.md",
		"# порядок ревью\n\nконец: любой\nпредмет: "+second+" «Как читать»\n\n## Промпт\n\nПрочитай дифф.\n\n## Проверка\n\n```sh\ntrue\n```\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed: два скилла и два сценария")

	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, standSkill, strings.Replace(standSkillText, "Команда одна.", "Команда одна, зовут её сразу.", 1))
	write(t, root, second, strings.Replace(secondText, "Читают дифф целиком.", "Читают дифф целиком, начиная с пути бага.", 1))
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка двух скиллов")

	subsA, err := obey.ParseSubjects(standSkill + " «Как звать»")
	if err != nil {
		t.Fatal(err)
	}
	subsB, err := obey.ParseSubjects(second + " «Как читать»")
	if err != nil {
		t.Fatal(err)
	}
	// Объединение предметов идёт по пути, и здесь оно совпадает с порядком
	// сценариев: prose раньше review.
	print, err := obey.PrintFrom(treeReader(root, "xr-001-fix"), append(subsA, subsB...))
	if err != nil {
		t.Fatal(err)
	}
	standMark(t, root, taskform.StandMark{
		Tree: "a1b2c3d", Print: print, Base: "старый", Tier: "mini", Repeats: 5,
		Scenarios: []string{"14-prose-sample", "15-review-order"},
	}, "зачтён")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("отметка одного прогона на два сценария должна открывать ворота: %v", err)
	}
}

// TestStandGateHeadSection: правка шапки файла, то есть строк до первого
// заголовка. Раздела с таким заголовком в файле нет, поэтому покрывает её
// только предмет на весь файл, а база у шапки прежнего файла это «старый».
func TestStandGateHeadSection(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill)
	standBranch(t, root, strings.Replace(standSkillText, "# prose\n", "# prose\n\nСкилл про выборку эталонов.\n", 1))
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "(шапка файла)") ||
		!strings.Contains(err.Error(), "--base старый") {
		t.Fatalf("правка шапки прежнего файла должна требовать прогона с базой «старый»: %v", err)
	}
}

// TestStandGateRenamedFile: переименование агентского файла ворота разбирают как
// новый файл, поэтому базу «пусто» получают все его разделы. Сценарий той же
// веткой перепривязан на новый путь, иначе он остался бы без предмета.
func TestStandGateRenamedFile(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	standSeed(t, root, standSkill+" «Как звать»")
	renamed := "kit/skills/prose-sample/SKILL.md"
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	gitT(t, root, "rm", "-q", standSkill)
	write(t, root, renamed, standSkillText)
	write(t, root, scenarioDir+"/14-prose-sample.md",
		"# выборка эталонов\n\nконец: любой\nпредмет: "+renamed+"\n\n## Промпт\n\nНапиши абзац.\n\n## Проверка\n\n```sh\ntrue\n```\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 скилл переехал")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), renamed) ||
		!strings.Contains(err.Error(), "--base пусто") {
		t.Fatalf("разделы переехавшего файла должны требовать базу «пусто»: %v", err)
	}
}

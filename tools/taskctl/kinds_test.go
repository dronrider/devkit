package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeKindsTask кладёт файл задачи с заданными разделами. Заголовок уровня
// «## » кончает раздел, поэтому части склеиваются без ограждений.
func writeKindsTask(t *testing.T, root, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(taskFilePath(root, id)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFilePath(root, id), []byte("# "+id+"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// addKindsRow заводит строку на живую доску через cmdAdd: у фикстуры уже есть
// пять строк (XR-001..XR-005), новые идут следом и в счёт попадают все.
func addKindsRow(t *testing.T, root, id, title, accept, barrier string) {
	t.Helper()
	if _, err := cmdAdd(root, AddParams{ID: id, Title: title, Type: "task", Rank: "0+1+1+0+1", Accept: accept, Barrier: barrier}); err != nil {
		t.Fatal(err)
	}
}

// archTaskFile кладёт файл закрытой задачи в архив её года.
func archTaskFile(t *testing.T, root, id, year, body string) {
	t.Helper()
	p := filepath.Join(root, "docs", "tasks", "archive", year, id+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# "+id+"\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendArchRow дописывает строку в архив фикстуры напрямую: close двигал бы
// файлы и ворота, а тесту сводки нужен сам состав архива.
func appendArchRow(t *testing.T, root, id, title, date string) {
	t.Helper()
	if err := appendArchiveRow(archivePath(root), []string{id, title, "task", "P2", date, "-"}); err != nil {
		t.Fatal(err)
	}
}

// TestKinds гоняет сводку по фикстуре с известным распределением и утверждает
// все четыре счёта плюс ноль строк без вида. Состав:
//
// живые: XR-005 и XR-002 из фикстуры (agent), XR-100 user с барьером,
//
//	XR-101 mixed без раздела «Приёмка» (дорогая сторона), XR-102 agent
//	с непогашенным провалом (дешёвая сторона, первый след), XR-104 user
//	с пересмотром вниз в «Приёмка»;
//
// закрытые с 2026-08-15: XR-200 agent с выкатом, XR-201 agent без выката и
//
//	без замечаний (третий след), XR-202 agent без выката, но с замечанием
//	(в третий след не идёт), XR-203 user с барьером и пересмотром вверх;
//
// закрытая 2026-08-12 XR-300 вне счёта строк, но её файл несёт пересмотр
// вниз 2026-08-14, и счёт пересмотров его видит: след несёт свою дату.
func TestKinds(t *testing.T) {
	root := setup(t)

	addKindsRow(t, root, "XR-100", "Юзерская", acceptUser, "глаза")
	writeKindsTask(t, root, "XR-100", "\n## Приёмка\n\n- вид: user\n- барьер «глаза»: причина\n"+
		"  - макет: годится\n  - замер: годится\n  - тест: годится\n")

	// mixed без «Приёмка»: вид назначен позже add (set --accept барьер не
	// ставит), раздел в файле не появился. Так выглядит дорогая сторона.
	addKindsRow(t, root, "XR-101", "Смешанная", acceptAgent, "")
	if _, err := cmdSet(root, SetParams{ID: "XR-101", Accept: acceptMixed}); err != nil {
		t.Fatal(err)
	}
	writeKindsTask(t, root, "XR-101", "\n## Сценарий проверки\n\n1. Шаг.\n")

	addKindsRow(t, root, "XR-102", "Провальная", acceptAgent, "")
	// Непогашенный провал живёт суффиксом живой строки.
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-102")
	base, deps, acceptSuf, _, blockSuf := splitTitle(row.Title)
	row.Title = joinTitle(base, deps, acceptSuf, " [провал: прод отдаёт 500]", blockSuf)
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	// Живая user-задача с пересмотром вниз в «Приёмка».
	addKindsRow(t, root, "XR-104", "Пониженная", acceptUser, "глаза")
	writeKindsTask(t, root, "XR-104", "\n## Приёмка\n\n"+
		"- вид: user, пересмотрен с mixed 2026-08-14 вниз: остался один шаг человека\n"+
		"- барьер «глаза»: причина\n"+
		"  - макет: годится\n  - замер: годится\n  - тест: годится\n")

	appendArchRow(t, root, "XR-200", "С выкатом", "2026-08-15")
	archTaskFile(t, root, "XR-200", "2026", "\n## Выкат\n\n- слит.\n")

	appendArchRow(t, root, "XR-201", "Тихая", "2026-08-15")
	archTaskFile(t, root, "XR-201", "2026", "\n## Итог\n")

	appendArchRow(t, root, "XR-202", "С замечанием", "2026-08-15")
	archTaskFile(t, root, "XR-202", "2026", "\n## Ревью\n\n- нейминг: исправлено\n")

	appendArchRow(t, root, "XR-203", "Пересмотренная [приёмка: user]", "2026-08-15")
	archTaskFile(t, root, "XR-203", "2026", "\n## Приёмка\n\n"+
		"- вид: user, пересмотрен с agent 2026-08-14 вверх: барьер нашёлся\n"+
		"- барьер «событие»: причина\n"+
		"  - лог: годится\n  - пустой лог: годится\n  - сторонний источник: годится\n")

	appendArchRow(t, root, "XR-300", "Старая", "2026-08-12")
	// Закрыта до границы, но пересмотр в её файле датирован позже DK-298:
	// счёт пересмотров судит по дате строки, а не по дате закрытия.
	archTaskFile(t, root, "XR-300", "2026", "\n## Приёмка\n\n"+
		"- вид: agent, пересмотрен с user 2026-08-14 вниз: эталон нашёлся\n"+
		"- барьер «глаза», перебор обходов:\n"+
		"  - макет: годится\n  - замер: годится\n  - тест: годится\n")

	msg, err := cmdKinds(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"строк в счёте: 13 (живых 9, закрытых с 2026-08-15: 4)",
		"распределение по видам: agent 9, mixed 1, user 3",
		"дорогая сторона: строк user и mixed без барьера в «Приёмка»: 1 (XR-101)",
		"дешёвая сторона: провалов проверки 1 (XR-102), понижений вида 2 (XR-104, XR-300), закрыто агентских без выката и без замечаний ревью: 1 (XR-201)",
		"пересмотры: вниз 2 (XR-104, XR-300), вверх 1 (XR-203)",
		"строк без вида: 0",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в сводке нет %q:\n%s", want, msg)
		}
	}
}

// TestKindsNoKindRow: суффикс с неразобранным значением это строка без вида,
// а не «agent по умолчанию»: DoD цели DK-308 п. 7 требует ноль строк без вида,
// и сводка обязана показывать такие строки, а не прятать их в agent.
func TestKindsNoKindRow(t *testing.T) {
	root := setup(t)
	// Ручная правка доски: add такое значение не пропустит.
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-005")
	row.Title = "Задача в работе [приёмка: users]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdKinds(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "строк без вида: 1 (XR-005)") {
		t.Fatalf("строка с битым суффиксом не названа без вида:\n%s", msg)
	}
}

package taskform

import (
	"strings"
	"testing"
)

// TestOrderFollowsSections: порядок разделов читается по префиксу заголовка,
// а заголовок не из формы отвечает -1.
func TestOrderFollowsSections(t *testing.T) {
	if Order(Situation) != 0 || Order(Verification) != len(Sections)-1 {
		t.Fatalf("края формы: %d, %d", Order(Situation), Order(Verification))
	}
	if Order("## Проверка после выката") != Order(Verification) {
		t.Fatal("раздел с хвостом в заголовке не узнан по префиксу")
	}
	if Order("## Журнал") != -1 || Order("Что происходит") != -1 {
		t.Fatal("чужой заголовок получил место в форме")
	}
	if Order(Merged) > Order(Verification) || Order(Stages) > Order(Review) {
		t.Fatal("порядок жизни задачи нарушен: Выкат раньше Проверки, Ход работы раньше Ревью")
	}
}

// TestInsertIntoMissingSectionGoesByForm: раздела нет, и он встаёт перед
// первым разделом, который по форме идёт позже, а не в хвост файла. Файл,
// оформленный из черновика, до этого получал «Ход работы» после «Сценария
// проверки» на первом же move check.
func TestInsertIntoMissingSectionGoesByForm(t *testing.T) {
	doc := "# T-001\n\n## Что происходит\n\nтекст\n\n## DoD\n\nконец\n\n## Сценарий проверки\n\n1. шаг\n"
	got := InsertIntoSection(doc, Stages, "- Разработка: раз.")
	want := "# T-001\n\n## Что происходит\n\nтекст\n\n## DoD\n\nконец\n\n## Ход работы\n\n- Разработка: раз.\n\n## Сценарий проверки\n\n1. шаг\n"
	if got != want {
		t.Fatalf("раздел не по форме:\n%s", got)
	}
	// Заголовок из формы, процитированный в блоке кода, местом не считается.
	quoted := "# T-002\n\n## DoD\n\n```\n## Сценарий проверки\n```\n"
	got = InsertIntoSection(quoted, Stages, "- Разработка: раз.")
	if !strings.HasSuffix(got, "```\n\n## Ход работы\n\n- Разработка: раз.\n") {
		t.Fatalf("цитата принята за раздел:\n%s", got)
	}
}

// TestInsertIntoSectionOutsideForm: раздел не из формы (журнал файла цели)
// заводится в конце файла, как и раньше.
func TestInsertIntoSectionOutsideForm(t *testing.T) {
	got := InsertIntoSection("# T-003\n\n## Цель\n\nтекст\n", "## Журнал", "- виток 1")
	if got != "# T-003\n\n## Цель\n\nтекст\n\n## Журнал\n\n- виток 1\n" {
		t.Fatalf("журнал не в хвосте: %q", got)
	}
}

// TestInsertSectionKeepsBodyAndPlace: готовый раздел с телом встаёт по форме,
// пустое тело даёт голый заголовок, а последний по форме раздел идёт в конец.
func TestInsertSectionKeepsBodyAndPlace(t *testing.T) {
	doc := "# T-004\n\n## DoD\n\n## Ход работы\n"
	got := InsertSection(doc, Acceptance, "- вид: user\n- барьер «глаза»:\n")
	want := "# T-004\n\n## DoD\n\n## Приёмка\n\n- вид: user\n- барьер «глаза»:\n\n## Ход работы\n"
	if got != want {
		t.Fatalf("приёмка не на месте:\n%s", got)
	}
	got = InsertSection(doc, Rank, "")
	if got != "# T-004\n\n## DoD\n\n## Ранг\n\n## Ход работы\n" {
		t.Fatalf("пустой раздел:\n%s", got)
	}
	got = InsertSection(doc, Merged, "- слито: abc")
	if got != "# T-004\n\n## DoD\n\n## Ход работы\n\n## Выкат\n\n- слито: abc\n" {
		t.Fatalf("хвостовой раздел:\n%s", got)
	}
}

// TestSmokeCovers: отметка прогона smoke действует на последний выкат, а круг
// доработки её гасит. По этому вердикту shipctl считает очередь выката, а
// taskctl отбирает строки Check, которые вправе закрыть автоматика, и
// разошедшийся разбор освободил бы очередь без прогона.
func TestSmokeCovers(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want bool
	}{
		{"отметка после слияния", "## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n- smoke прогнан, 2026-08-21\n", true},
		{"раздела нет", "# XR-1\n\n## Проверка\n\n- прогон вложен\n", false},
		{"слияние без отметки", "## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n", false},
		{"круг доработки гасит отметку", "## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n" +
			"- smoke прогнан, 2026-08-21\n- 2026-08-22 слито: b2c3d4e5\n", false},
		{"второй прогон после круга", "## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n" +
			"- smoke прогнан, 2026-08-21\n- 2026-08-22 слито: b2c3d4e5\n- smoke прогнан, 2026-08-23\n", true},
		{"проза с двоеточием не выкат", "## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n" +
			"- smoke прогнан, 2026-08-21\n- заметка: катали руками\n", true},
		{"цитата чужой отметки не считается", "## Выкат\n\n- 2026-08-20 слито: a1b2c3d4\n\n" +
			"## Проверка\n\n```\n## Выкат\n\n- smoke прогнан, 2026-08-21\n```\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SmokeCovers(c.doc); got != c.want {
				t.Fatalf("SmokeCovers = %v, ждали %v на:\n%s", got, c.want, c.doc)
			}
		})
	}
}

// TestException: пометка гасит названный ворот, поясняющий хвост в скобках
// отбрасывается, регистр не строг, а процитированная в ограждённом блоке
// пометка ворот не открывает.
func TestException(t *testing.T) {
	doc := "# XR-001\n\n## Ход работы\n\n- Исключение: Тесты (правка бескодовая)\n" +
		"\n## Проверка\n\n```\n- Исключение: обкатка (цитата чужого файла)\n```\n"
	if !Exception(doc, GateTests) {
		t.Fatal("пометка с хвостом и заглавной буквой ворот не погасила")
	}
	if Exception(doc, GateRegcheck) {
		t.Fatal("пометка одного ворот погасила другой")
	}
	if Exception(doc, GateRehearsal) {
		t.Fatal("пометка из ограждённого блока погасила ворот")
	}
}

// TestRehearsed: отметка обкатки видна воротам, цитата отметки внутри
// ограждённого блока не считается, а файл без отметки её не показывает.
func TestRehearsed(t *testing.T) {
	mark := RehearsalNote + " 2026-08-31 12:00, свежее дерево 1a2b3c4d5e6f, шагов 2, все зелёные."
	if !Rehearsed("# XR-001\n\n## Проверка\n\n" + mark + "\n") {
		t.Fatal("отметка обкатки не найдена")
	}
	quoted := "# XR-001\n\n## Проверка\n\n```console\n$ taskctl show XR-002\n" + mark + "\n```\n"
	if Rehearsed(quoted) {
		t.Fatal("процитированная отметка чужой задачи открыла ворота")
	}
	if Rehearsed("# XR-001\n\n## Проверка\n\n- прогон пройден.\n") {
		t.Fatal("отметки нет, а Rehearsed говорит обратное")
	}
}

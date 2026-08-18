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

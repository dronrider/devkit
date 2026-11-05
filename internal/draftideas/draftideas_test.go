package draftideas

import "testing"

const sample = "# XR-008: запись\n\nматериал записи с примером разметки:\n\n```md\n## Идеи\n\n- 2026-11-01: мысль из примера, отклонена: пример\n```\n\n## Идеи\n\n- 2026-11-02: сделать вход в чат без запуска\n- 2026-11-03: убрать портянку тел, поднята в задаче DK-1611\n- 2026-11-04: сменить шапку\n  судьба: влита в материал записи\n- 2026-11-05: выкинуть счётчик, отклонена: счётчик нужен\n- 2026-11-06: сменить подачу -> отклонена: поздно\n- 2026-11-07: Надпись «отклонена: причина» нужно убрать из интерфейса\n\n## Грумминг\n\n- 2026-11-05, отложен: ждём ответа\n"

func TestUndecidedSeesOnlyIdeasWithoutFate(t *testing.T) {
	got := Undecided([]byte(sample))
	want := []string{
		"- 2026-11-02: сделать вход в чат без запуска",
		"- 2026-11-07: Надпись «отклонена: причина» нужно убрать из интерфейса",
	}
	if len(got) != len(want) {
		t.Fatalf("неразобранные мысли: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("неразобранная мысль %d: %q вместо %q", i, got[i], want[i])
		}
	}
}

func TestCountSeparatesFates(t *testing.T) {
	total, undecided := Count([]byte(sample))
	if total != 6 || undecided != 2 {
		t.Fatalf("счёт мыслей: всего %d, без судьбы %d", total, undecided)
	}
}

func TestIdeaTextMentioningFateStaysUndecided(t *testing.T) {
	total, pending := Count([]byte("## Идеи\n\n- 2026-11-05: Надпись «отклонена: причина» нужно убрать из интерфейса\n"))
	if total != 1 || pending != 1 {
		t.Fatalf("мысль со словом судьбы в тексте посчитана разобранной: total=%d pending=%d", total, pending)
	}
}

func TestArrowIsTheExplicitFateMark(t *testing.T) {
	total, pending := Count([]byte("## Идеи\n\n- 2026-11-05: сменить подачу -> отклонена: поздно\n- 2026-11-05: сменить вход -> поднята в задаче DK-1611\n"))
	if total != 2 || pending != 0 {
		t.Fatalf("стрелка не признана отметкой судьбы: total=%d pending=%d", total, pending)
	}
}

func TestIdeasOutsideSectionIgnored(t *testing.T) {
	record := "# XR-009: запись\n\n- не мысль, а пункт материала\n\n## Прочее\n\n- 2026-11-02: мимо раздела\n"
	if got := Undecided([]byte(record)); len(got) != 0 {
		t.Fatalf("пункты мимо раздела посчитаны мыслями: %v", got)
	}
	if total, undecided := Count([]byte(record)); total != 0 || undecided != 0 {
		t.Fatalf("счёт мимо раздела: %d %d", total, undecided)
	}
}

func TestNoIdeasSectionMeansNone(t *testing.T) {
	record := []byte("# XR-010: запись\n\nматериал без идей\n")
	if got := Undecided(record); len(got) != 0 {
		t.Fatalf("мыслей нет, а найдены: %v", got)
	}
}

func TestFencedSectionIsExampleNotRecord(t *testing.T) {
	record := []byte("# XR-011: запись\n\n```md\n## Идеи\n\n- 2026-11-02: мысль из примера\n```\n")
	if got := Undecided(record); len(got) != 0 {
		t.Fatalf("мысли из примера кода посчитаны мыслями записи: %v", got)
	}
}

func TestGroomSectionDetected(t *testing.T) {
	if !GroomSection([]byte(sample)) {
		t.Fatal("раздел груминга не найден")
	}
	if GroomSection([]byte("# XR-010: запись\n\nупоминание слов «## Грумминг» внутри строки\n")) {
		t.Fatal("раздел груминга найден внутри строки")
	}
}

func TestQuotedGroomHeadingIsNotHistory(t *testing.T) {
	if GroomSection([]byte("# Новый черновик\n\nПример разметки:\n```md\n## Грумминг\n```\n")) {
		t.Fatal("заголовок из примера кода посчитан прошлым разбором")
	}
}

func TestQuotedCommaBeforeFateWordIsProse(t *testing.T) {
	total, pending := Count([]byte("## Идеи\n\n- Надпись «сначала, отклонена: причина» убрать\n"))
	if total != 1 || pending != 1 {
		t.Fatalf("запятая внутри цитаты признана отметкой судьбы: total=%d pending=%d", total, pending)
	}
}

func TestQuotedArrowFateIsProse(t *testing.T) {
	total, pending := Count([]byte("## Идеи\n\n- цитата «подача -> отклонена: поздно» из письма\n"))
	if total != 1 || pending != 1 {
		t.Fatalf("стрелка внутри цитаты признана отметкой судьбы: total=%d pending=%d", total, pending)
	}
}

func TestAsciiQuotedCommaBeforeFateWordIsProse(t *testing.T) {
	total, pending := Count([]byte("## Идеи\n\n- 2026-11-06: Заменить надпись \"сначала, отклонена: причина\" на понятное состояние отправки\n"))
	if total != 1 || pending != 1 {
		t.Fatalf("запятая внутри прямых кавычек признана отметкой судьбы: total=%d pending=%d", total, pending)
	}
}

func TestBacktickQuotedFateWordIsProse(t *testing.T) {
	total, pending := Count([]byte("## Идеи\n\n- 2026-11-06: Убрать технический шаблон `сначала, отклонена: причина` из подписи сообщения\n"))
	if total != 1 || pending != 1 {
		t.Fatalf("слово судьбы внутри inline-примера признано отметкой: total=%d pending=%d", total, pending)
	}
}

func TestTildeFenceIsExampleNotRecord(t *testing.T) {
	record := []byte("# XR-012: запись\n\n~~~md\n## Идеи\n\n- пример\n~~~\n")
	if got := Undecided(record); len(got) != 0 {
		t.Fatalf("мысли из примера за волной посчитаны мыслями записи: %v", got)
	}
	if total, undecided := Count(record); total != 0 || undecided != 0 {
		t.Fatalf("счёт примера за волной: %d %d", total, undecided)
	}
}

func TestShortFenceInsideLongFenceStaysExample(t *testing.T) {
	record := []byte("# XR-013: запись\n\n````md\n```md\n## Идеи\n\n- пример\n```\n````\n")
	if got := Undecided(record); len(got) != 0 {
		t.Fatalf("короткая кавычка закрыла длинное ограждение: %v", got)
	}
	if total, undecided := Count(record); total != 0 || undecided != 0 {
		t.Fatalf("счёт за длинным ограждением: %d %d", total, undecided)
	}
}

func TestGroomHeadingBehindTildeAndLongFencesIsNotHistory(t *testing.T) {
	if GroomSection([]byte("# Новый черновик\n\n~~~md\n## Грумминг\n~~~\n")) {
		t.Fatal("заголовок за волной посчитан прошлым разбором")
	}
	if GroomSection([]byte("# Новый черновик\n\n````md\n```md\n## Грумминг\n```\n````\n")) {
		t.Fatal("заголовок за длинным ограждением посчитан прошлым разбором")
	}
}

func TestInlineCodeDelimiterRuns(t *testing.T) {
	for _, text := range []string{
		"шаблон ``сначала, отклонена: причина`` убрать",
		"шаблон ``пример `, отклонена: причина`` убрать",
		"шаблон ```пример ` и ``, отклонена: причина``` убрать",
	} {
		total, pending := Count([]byte("## Идеи\n- " + text + "\n"))
		if total != 1 || pending != 1 {
			t.Errorf("%q: total=%d pending=%d", text, total, pending)
		}
	}
	for _, text := range []string{
		"шаблон ``пример ` внутри`` убрать, отклонена: поздно",
		"шаблон ``пример`` убрать -> отклонена: поздно",
	} {
		total, pending := Count([]byte("## Идеи\n- " + text + "\n"))
		if total != 1 || pending != 0 {
			t.Errorf("%q: total=%d pending=%d", text, total, pending)
		}
	}
}

func TestQuoteAndCodeDelimitersStayIndependent(t *testing.T) {
	for _, text := range []string{
		"убрать шаблон `\"пример` -> отклонена: поздно",
		"убрать шаблон ``«пример`` -> отклонена: поздно",
		"убрать шаблон \"пример `\" -> отклонена: поздно",
		"убрать шаблон «пример ``» -> отклонена: поздно",
	} {
		total, pending := Count([]byte("## Идеи\n- " + text + "\n"))
		if total != 1 || pending != 0 {
			t.Errorf("%q: total=%d pending=%d", text, total, pending)
		}
	}
	for _, text := range []string{
		"убрать шаблон `\"пример, отклонена: поздно`",
		"убрать шаблон ``«пример -> отклонена: поздно``",
		"убрать шаблон \"пример `, отклонена: поздно\"",
		"убрать шаблон «пример `` -> отклонена: поздно»",
	} {
		total, pending := Count([]byte("## Идеи\n- " + text + "\n"))
		if total != 1 || pending != 1 {
			t.Errorf("%q: total=%d pending=%d", text, total, pending)
		}
	}
}

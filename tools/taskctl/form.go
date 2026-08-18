package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/stage"
)

// Форма черновика и файла задачи описана в TASKFORM.md рядом с RANKING.md и
// ACCEPTANCE.md. Здесь лежит машинная её часть: потолок первой строки
// черновика, порядок разделов файла задачи и болванка, которую кладут add и
// file. Текст формы и разбор решений в самом TASKFORM.md, дублировать его тут
// нечем.

// formDoc это имя страницы формы: его называют отказы, чтобы читатель шёл за
// разбором в одно место, а не собирал форму по подсказкам команд.
const formDoc = "TASKFORM.md"

// draftTitleLimit это потолок первой строки черновика в символах. Мера та же,
// что у subject коммита: строка живёт заголовком накопителя, и черновик,
// записанный одним абзацем, становится заголовком целиком (так записаны
// DK-330, DK-439 и DK-442, у последнего заголовок на 1800 символов).
const draftTitleLimit = 72

// formSections это порядок разделов файла задачи: сначала то, что пишет
// человек, дальше контрактные разделы в порядке жизни задачи. Порядок держит
// lint, а болванка кладёт по нему заголовки. «Выкат» стоит перед «Проверкой»
// потому, что раздел выката пишет слияние, а вывод прогона вкладывается уже
// после выката.
var formSections = []string{
	"## Что происходит",
	"## Чего хотим",
	"## Границы",
	"## Развилки",
	dodHeading,
	"## Ранг",
	acceptanceHeading,
	stageSection,
	reviewHeading,
	scenarioSection,
	mergedSection,
	verificationHeading,
}

// formRank говорит, каким по счёту идёт раздел формы в строке заголовка, и
// отвечает -1 на заголовке не из формы. Совпадение по префиксу, как у
// readSectionFromPath: «## Проверка после выката» это тот же раздел.
func formRank(line string) int {
	for i, h := range formSections {
		if strings.HasPrefix(line, h) {
			return i
		}
	}
	return -1
}

// taskFormSkeleton это болванка файла задачи для типов task и bug: заголовки
// формы, которые наполняет тот, кто заводит строку. Пустой «Ход работы» стоит
// в ней не для человека: пакет этапов от смены статуса иначе уезжает в хвост
// файла и встаёт после разделов, которые по форме идут позже.
func taskFormSkeleton() string {
	var b strings.Builder
	for _, h := range []string{"## Что происходит", "## Чего хотим", dodHeading, "## Ранг", stageSection} {
		fmt.Fprintf(&b, "\n%s\n", h)
	}
	return b.String()
}

// insertFormSection вписывает готовый раздел на его место по форме: перед
// первым разделом, который по порядку идёт позже. Раздела позже нет, значит
// место в конце файла. Так «Приёмка» от add встаёт выше «Хода работы» из
// болванки, а не приклеивается за ним последней строкой.
func insertFormSection(body []byte, heading, section string) []byte {
	rank := formRank(heading)
	if rank < 0 {
		return appendSection(body, section)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	mask, _ := stage.FenceMask(lines)
	at := -1
	for i, ln := range lines {
		if mask[i] || !strings.HasPrefix(ln, "## ") {
			continue
		}
		if r := formRank(ln); r > rank {
			at = i
			break
		}
	}
	if at < 0 {
		return appendSection(body, section)
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, strings.Split(strings.TrimRight(section, "\n"), "\n")...)
	out = append(out, "")
	out = append(out, lines[at:]...)
	return []byte(strings.Join(out, "\n") + "\n")
}

// draftTitleGuard отбивает черновик, у которого первая строка не заголовок, а
// весь текст идеи. Отказ стоит на записи, а не на разборе: заголовок правится
// дешевле всего в ту минуту, когда мысль ещё в голове, а на груминге простыню
// уже читают целиком, чтобы понять, о чём она.
func draftTitleGuard(text string) error {
	title, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	n := len([]rune(strings.TrimSpace(title)))
	if n <= draftTitleLimit {
		return nil
	}
	return fmt.Errorf("первая строка черновика длиннее %d символов (%d): она идёт заголовком, по ней черновик узнают в накопителе.\n"+
		"  подробности пишутся абзацем ниже через пустую строку, форма черновика в %s\n"+
		"  многострочный текст удобнее отдать на stdin: taskctl draft <<'EOF' ... EOF",
		draftTitleLimit, n, formDoc)
}

// clipTitle укорачивает заголовок для печати накопителя. Порог держит запись,
// но черновики, записанные до него, лежат простынями, и `draft list` без
// обрезки печатает их телом на весь экран.
func clipTitle(s string) string {
	r := []rune(s)
	if len(r) <= draftTitleLimit {
		return s
	}
	cut := string(r[:draftTitleLimit])
	if i := strings.LastIndex(cut, " "); i > draftTitleLimit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "..."
}

package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// Форма черновика и файла задачи описана в TASKFORM.md рядом с RANKING.md и
// ACCEPTANCE.md. Порядок разделов и вставка по форме живут в
// internal/taskform, общем для taskctl, shipctl и пакета этапов; здесь
// остаётся то, что читает только taskctl: потолок первой строки черновика,
// подразделы черновика, болванка файла задачи и перенос черновика в файл.

// formDoc это имя страницы формы: его называют отказы, чтобы читатель шёл за
// разбором в одно место, а не собирал форму по подсказкам команд.
const formDoc = taskform.Doc

// draftTitleLimit это потолок первой строки черновика в символах. Мера та же,
// что у subject коммита: строка живёт заголовком накопителя, и черновик,
// записанный одним абзацем, становится заголовком целиком (так записаны
// DK-330, DK-439 и DK-442, у последнего заголовок на 1800 символов).
const draftTitleLimit = 72

// Подразделы черновика по SCQA: ситуация и осложнение обязательны, вопрос и
// гипотеза могут стоять пустыми. Живут под «## Черновик» заголовками третьего
// уровня, чтобы шапка черновика и разделы грумера («## DoD», «## Грумминг»)
// читались отдельно от тела идеи.
const (
	draftBodyHeading    = "## Черновик"
	draftSituation      = "### Ситуация"
	draftComplication   = "### Осложнение"
	draftQuestion       = "### Вопрос"
	draftHypothesis     = "### Гипотеза"
	draftPromotedPrefix = "- Из черновика"
)

var draftSubsections = []string{draftSituation, draftComplication, draftQuestion, draftHypothesis}

// formRank говорит, каким по счёту идёт раздел формы в строке заголовка, и
// отвечает -1 на заголовке не из формы.
func formRank(line string) int { return taskform.Order(line) }

// taskFormSkeleton это болванка файла задачи для типов task и bug: заголовки
// формы, которые наполняет тот, кто заводит строку. Пустой «Ход работы» стоит
// в ней не для человека: пакет этапов от смены статуса иначе уезжает в хвост
// файла и встаёт после разделов, которые по форме идут позже.
func taskFormSkeleton() string {
	var b strings.Builder
	for _, h := range []string{taskform.Situation, taskform.Want, dodHeading, "## Ранг", stageSection} {
		fmt.Fprintf(&b, "\n%s\n", h)
	}
	return b.String()
}

// insertFormSection вписывает готовый раздел на его место по форме: перед
// первым разделом, который по порядку идёт позже. Раздела позже нет, значит
// место в конце файла.
func insertFormSection(body []byte, heading, section string) []byte {
	if formRank(heading) < 0 {
		return appendSection(body, section)
	}
	text := strings.TrimPrefix(strings.TrimRight(section, "\n"), heading)
	return []byte(taskform.InsertSection(string(body), heading, text))
}

// formSectionAt отвечает, перед какой строкой встаёт раздел по форме: это
// первый заголовок, который по порядку идёт позже. Ответ -1 значит, что позже
// ничего нет и место разделу в конце файла.
func formSectionAt(lines []string, heading string) int {
	return taskform.SectionAt(lines, heading)
}

// draftTitleGuard отбивает черновик, у которого первая строка не заголовок, а
// весь текст идеи. Отказ стоит на записи, а не на разборе: заголовок правится
// дешевле всего в ту минуту, когда мысль ещё в голове, а на грумминге простыню
// уже читают целиком, чтобы понять, о чём она. Совет про stdin печатается
// только тому, кто пришёл аргументом: пришедшему со stdin (дашборд, пайп)
// советовать stdin не о чем.
func draftTitleGuard(text string, viaStdin bool) error {
	title, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	n := len([]rune(strings.TrimSpace(title)))
	if n <= draftTitleLimit {
		return nil
	}
	msg := fmt.Sprintf("первая строка черновика длиннее %d символов (%d): она идёт заголовком, по ней черновик узнают в накопителе.\n"+
		"  подробности пишутся строками ниже через пустую строку, форма черновика в %s",
		draftTitleLimit, n, formDoc)
	if !viaStdin {
		msg += "\n  многострочный текст удобнее отдать на stdin: taskctl draft <<'EOF' ... EOF"
	}
	return fmt.Errorf("%s", msg)
}

// draftBodySection собирает раздел «Черновик» из текста после первой строки. Готовые
// подразделы («### Ситуация» и соседи) идут как есть; текст без разметки
// целиком ложится в «Ситуацию», а остальные подразделы стоят пустыми: черновик
// дешёвый, и отбивать мысль за отсутствие разметки дороже, чем разложить её на
// грумминге.
func draftBodySection(rest string) string {
	rest = strings.TrimSpace(rest)
	var b strings.Builder
	b.WriteString(draftBodyHeading + "\n")
	if strings.Contains("\n"+rest, "\n### ") {
		b.WriteString("\n" + rest + "\n")
		return b.String()
	}
	// Разделы второго уровня в тексте («## DoD» от автора) остаются своими
	// разделами после подразделов: иначе пустые подразделы вставали бы за
	// «## DoD», и оформление уносило бы их в раздел DoD файла задачи.
	body, others := rest, ""
	if secs := splitSections(rest, "## "); len(secs) > 1 {
		body = strings.Join(secs[0].body, "\n")
		var tail []string
		for _, sec := range secs[1:] {
			tail = append(tail, "", sec.heading)
			if len(sec.body) > 0 {
				tail = append(tail, "")
				tail = append(tail, sec.body...)
			}
		}
		others = strings.Join(tail, "\n") + "\n"
	}
	for _, h := range draftSubsections {
		b.WriteString("\n" + h + "\n")
		if h == draftSituation && body != "" {
			b.WriteString("\n" + body + "\n")
		}
	}
	b.WriteString(others)
	return b.String()
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

// docSection это раздел разметки: заголовок строкой «## ...» или «### ...» и
// тело без хвостовых пустых строк.
type docSection struct {
	heading string
	body    []string
}

// splitSections режет текст на разделы по заголовкам заданного уровня вне
// ограждённых блоков; первым элементом идёт всё, что стоит до первого
// заголовка, с пустым heading.
func splitSections(text, prefix string) []docSection {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	mask, _ := taskform.FenceMask(lines)
	out := []docSection{{}}
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, prefix) {
			out = append(out, docSection{heading: strings.TrimSpace(ln)})
			continue
		}
		out[len(out)-1].body = append(out[len(out)-1].body, ln)
	}
	for i := range out {
		out[i].body = trimBlank(out[i].body)
	}
	return out
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// renderTaskFromDraft собирает файл задачи из черновика по форме TASKFORM.md.
// Заголовок H1 и первая строка «Ранга» (формула и бакет) берутся из строки
// доски, а не из черновика; ситуация и
// осложнение едут в «Что происходит», гипотеза в «Чего хотим», вопрос в
// «Развилки», «## DoD» и другие разделы формы, дописанные грумером, встают на
// свои места; черновик без подразделов ложится в «Что происходит» целиком.
// Шапка черновика и «## Грумминг» в файл как есть не переезжают: от них
// остаётся строка в «Ходе работы» с заголовком черновика и датами записи и
// оформления, а под ней пометки грумминга.
func renderTaskFromDraft(id, title, rank, draft, today string) string {
	top := splitSections(draft, "## ")
	written, draftTitle := "", ""
	for _, ln := range top[0].body {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "# ") && draftTitle == "":
			draftTitle = draftTitleLine(t)
		case strings.HasPrefix(t, draftWrittenPrefix):
			written = strings.TrimSpace(strings.TrimPrefix(t, draftWrittenPrefix))
		}
	}
	form := map[string][]string{}
	var extra []docSection
	var groom []string
	add := func(h string, body []string) {
		if len(body) == 0 {
			return
		}
		if len(form[h]) > 0 {
			form[h] = append(form[h], "")
		}
		form[h] = append(form[h], body...)
	}
	if rank != "" {
		add("## Ранг", []string{rank})
	}
	for _, s := range top[1:] {
		switch {
		case s.heading == draftBodyHeading:
			// Черновик, записанный до формы, открывает тело копией
			// заголовка; повторять её в «Что происходит» незачем.
			body := s.body
			if len(body) > 0 && strings.TrimSpace(body[0]) == draftTitle {
				body = trimBlank(body[1:])
			}
			subs := splitSections(strings.Join(body, "\n"), "### ")
			add(taskform.Situation, subs[0].body)
			for _, sub := range subs[1:] {
				switch sub.heading {
				case draftSituation, draftComplication:
					add(taskform.Situation, sub.body)
				case draftHypothesis:
					add(taskform.Want, sub.body)
				case draftQuestion:
					add(taskform.Forks, sub.body)
				default:
					add(taskform.Situation, append([]string{sub.heading, ""}, sub.body...))
				}
			}
		case s.heading == draftGroomHeading:
			groom = append(groom, s.body...)
		case formRank(s.heading) >= 0:
			add(s.heading, s.body)
		default:
			extra = append(extra, s)
		}
	}
	trace := draftPromotedPrefix + " " + id
	if draftTitle != "" {
		trace += " «" + draftTitle + "»"
	}
	if written != "" {
		trace += ": записан " + written + ","
	} else {
		trace += ":"
	}
	trace += " оформлен " + today + "."
	add(stageSection, append([]string{trace}, groom...))

	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s\n", id, title)
	always := map[string]bool{taskform.Situation: true, taskform.Want: true, dodHeading: true, "## Ранг": true, stageSection: true}
	write := func(h string, body []string) {
		b.WriteString("\n" + h + "\n")
		if len(body) > 0 {
			b.WriteString("\n" + strings.Join(body, "\n") + "\n")
		}
	}
	for _, h := range taskform.Sections {
		body := form[h]
		if len(body) == 0 && !always[h] {
			continue
		}
		write(h, body)
		if h == "## Ранг" {
			for _, s := range extra {
				write(s.heading, s.body)
			}
		}
	}
	return b.String()
}

package taskform

import (
	"fmt"
	"regexp"
	"strings"
)

// Машинный перечень развилок раздела «Развилки» (LLD DK-552, решения 1 и 2).
// Развилка это элемент списка с головой «- «имя»: вопрос» и подстроками
// фиксированной формы под ней. Состояние не хранится полем, а выводится из
// двух: есть строка «решено», развилка решена; строки нет и «решает: человек»,
// развилка открыта и держит старт; строки нет и «решает: исполнитель»,
// развилка оставлена исполнителю. Разбор лежит тут, потому что писатель один
// (`taskctl decide`), а читателей трое: сама taskctl (`lint` и печать), ворота
// shipctl и дашборд, и вторая копия регулярки держала бы старт через раз.

// Значения поля «решает»: кто отвечает на вопрос развилки.
const (
	ForkHuman    = "человек"
	ForkExecutor = "исполнитель"
)

// Авторы решения в строках «решено» и «оставлена». Различие несёт цифру для
// счёта возвратов: развилка, закрытая исполнителем, это вопрос, который стоило
// задать до старта.
const (
	ByHuman    = "человеком"
	ByAgent    = "агентом"
	ByExecutor = "исполнителем"
)

// Состояния развилки, как их печатает команда и читают ворота.
const (
	StateOpen    = "открыта"
	StateLeft    = "оставлена"
	StateDecided = "решена"
)

// Fork это разобранная развилка перечня.
type Fork struct {
	Name     string   // имя между «ёлочками», уникальное в файле
	Question string   // вопрос после двоеточия в голове
	Who      string   // значение поля «решает»
	Hint     string   // рекомендация, она же ответ по умолчанию у оставленной
	Options  []string // варианты ответа, кроме рекомендованного, в порядке записи
	Answer   string   // текст последней строки «решено»
	By       string   // автор последнего решения
	Date     string   // дата последнего решения
	Left     string   // причина последней строки «оставлена»
	Line     int      // номер строки головы в файле, с единицы
}

// Decided говорит, что под головой стоит хотя бы одна строка «решено».
// Второе «решено» законно: решение поменялось после приёмки, побеждает
// последнее, а прежнее остаётся следом.
func (f Fork) Decided() bool { return f.By != "" && f.Answer != "" }

// State называет состояние развилки одним словом.
func (f Fork) State() string {
	switch {
	case f.Decided():
		return StateDecided
	case f.Who == ForkExecutor:
		return StateLeft
	default:
		return StateOpen
	}
}

// HoldsStart говорит, держит ли развилка старт задачи: открытая развилка со
// значением «решает: человек» и никакая другая (LLD DK-552, решение 2).
func (f Fork) HoldsStart() bool { return !f.Decided() && f.Who != ForkExecutor }

// Формы строк перечня. Голова идёт элементом списка без отступа, подстроки с
// отступом. Хвост головы после двоеточия это свободный текст вопроса.
var (
	forkHeadRe  = regexp.MustCompile(`^-\s+«([^»]+)»:\s*(.*)$`)
	forkSubRe   = regexp.MustCompile(`^\s+-\s+(.*)$`)
	forkWhoRe   = regexp.MustCompile(`^решает:\s*(человек|исполнитель)$`)
	forkHintRe  = regexp.MustCompile(`^рекомендация:\s*(\S.*)$`)
	forkOptRe   = regexp.MustCompile(`^вариант:\s*(\S.*)$`)
	forkDoneRe  = regexp.MustCompile(`^решено (человеком|агентом|исполнителем) (\d{4}-\d{2}-\d{2}):\s*(\S.*)$`)
	forkLeaveRe = regexp.MustCompile(`^оставлена (человеком|агентом) (\d{4}-\d{2}-\d{2})(?::\s*(.*))?$`)
)

// forkWords это слова, с которых начинаются машинные подстроки. Подстрока,
// начатая таким словом и не подошедшая под форму, это опечатка формы, и её
// называет lint. Подстрока без этих слов это проза при развилке (довод,
// ссылка на документ): разбор её пропускает, а писатель сохраняет.
var forkWords = []string{"решает", "рекомендация", "решено", "оставлена", "вариант"}

// ParseForks читает перечень развилок файла. Читается только тело раздела
// «## Развилки» и только вне ограждённых блоков: голова вида «- «имя»:» в
// другом разделе это проза, а процитированный в «Проверке» вывод команды не
// должен ни заводить, ни закрывать развилок.
func ParseForks(doc string) []Fork {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	from, to := forkSection(lines, mask)
	var out []Fork
	for i := from; i < to; i++ {
		if mask[i] {
			continue
		}
		m := forkHeadRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := Fork{Name: strings.TrimSpace(m[1]), Question: strings.TrimSpace(m[2]), Who: ForkHuman, Line: i + 1}
		for j := i + 1; j < to && !mask[j]; j++ {
			sub := forkSubRe.FindStringSubmatch(lines[j])
			if sub == nil {
				if strings.TrimSpace(lines[j]) == "" {
					continue
				}
				break
			}
			applyForkSub(&f, strings.TrimSpace(sub[1]))
		}
		out = append(out, f)
	}
	return out
}

// applyForkSub кладёт подстроку в поле развилки. Последняя строка своей формы
// побеждает: так второе «решено» отменяет первое, оставляя его следом в файле.
func applyForkSub(f *Fork, text string) {
	switch {
	case forkWhoRe.MatchString(text):
		f.Who = forkWhoRe.FindStringSubmatch(text)[1]
	case forkHintRe.MatchString(text):
		f.Hint = forkHintRe.FindStringSubmatch(text)[1]
	case forkOptRe.MatchString(text):
		f.Options = append(f.Options, strings.TrimSpace(forkOptRe.FindStringSubmatch(text)[1]))
	case forkDoneRe.MatchString(text):
		m := forkDoneRe.FindStringSubmatch(text)
		f.By, f.Date, f.Answer = m[1], m[2], m[3]
	case forkLeaveRe.MatchString(text):
		f.Left = strings.TrimSpace(forkLeaveRe.FindStringSubmatch(text)[3])
		if f.Left == "" {
			f.Left = "причина не названа"
		}
	}
}

// forkSection отдаёт границы тела раздела «Развилки»: первую строку после
// заголовка и первую строку следующего раздела. Заголовок ищется вне
// ограждений, иначе процитированный в «Проверке» вывод перехватывал бы поиск.
func forkSection(lines []string, mask []bool) (from, to int) {
	from, to = -1, len(lines)
	for i, ln := range lines {
		if mask[i] || !strings.HasPrefix(ln, "## ") {
			continue
		}
		if from >= 0 {
			return from, i
		}
		if strings.HasPrefix(ln, Forks) {
			from = i + 1
		}
	}
	if from < 0 {
		return 0, 0
	}
	return from, to
}

// ForkFind это находка сторожа формы: номер строки в файле и суть.
type ForkFind struct {
	Line int
	Text string
}

// ForkFinds судит перечень так, как его прочтут ворота: подстрока, начатая
// словом формы и не подошедшая под неё, повтор имени и строка «оставлена» при
// «решает: человек». Последнее это противоречие, а не проза: развилку отдали
// исполнителю, а поле осталось человеческим, и старт держится записью, которую
// уже передали.
func ForkFinds(doc string) []ForkFind {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	from, to := forkSection(lines, mask)
	var finds []ForkFind
	seen := map[string]int{}
	for i := from; i < to; i++ {
		if mask[i] {
			continue
		}
		m := forkHeadRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])
		if prev, ok := seen[name]; ok {
			finds = append(finds, ForkFind{i + 1, fmt.Sprintf("развилка «%s» заведена второй раз, первая в строке %d", name, prev)})
		} else {
			seen[name] = i + 1
		}
		f := Fork{Name: name, Who: ForkHuman}
		for j := i + 1; j < to && !mask[j]; j++ {
			sub := forkSubRe.FindStringSubmatch(lines[j])
			if sub == nil {
				if strings.TrimSpace(lines[j]) == "" {
					continue
				}
				break
			}
			text := strings.TrimSpace(sub[1])
			if word, bad := forkOffForm(text); bad {
				finds = append(finds, ForkFind{j + 1, fmt.Sprintf("подстрока развилки «%s» начата словом «%s», а формы не держит: %s", name, word, ForkFormHint(word))})
				continue
			}
			applyForkSub(&f, text)
		}
		if f.Left != "" && f.Who == ForkHuman {
			finds = append(finds, ForkFind{i + 1, fmt.Sprintf("развилка «%s» оставлена исполнителю, а поле держит «решает: человек»: передача правит и поле", name)})
		}
	}
	return finds
}

// forkOffForm говорит, что подстрока начата словом формы и формы не держит.
func forkOffForm(text string) (string, bool) {
	for _, w := range forkWords {
		if !strings.HasPrefix(text, w) {
			continue
		}
		switch {
		case forkWhoRe.MatchString(text), forkHintRe.MatchString(text),
			forkOptRe.MatchString(text), forkDoneRe.MatchString(text),
			forkLeaveRe.MatchString(text):
			return w, false
		}
		return w, true
	}
	return "", false
}

// ForkFormHint напоминает форму подстроки, названной словом word.
func ForkFormHint(word string) string {
	switch word {
	case "решает":
		return "«решает: человек» либо «решает: исполнитель»"
	case "рекомендация":
		return "«рекомендация: <ответ>, <довод>»"
	case "вариант":
		return "«вариант: <ответ>», по строке на вариант"
	case "решено":
		return "«решено человеком ГГГГ-ММ-ДД: <ответ и довод>»"
	default:
		return "«оставлена человеком ГГГГ-ММ-ДД: <причина>»"
	}
}

// Отступ подстроки: два пробела, как в примере формы.
const forkIndent = "  - "

// ForkHead собирает голову развилки.
func ForkHead(name, question string) string {
	return fmt.Sprintf("- «%s»: %s", name, strings.TrimSpace(question))
}

// ForkWhoLine собирает подстроку «решает».
func ForkWhoLine(who string) string { return forkIndent + "решает: " + who }

// ForkHintLine собирает подстроку рекомендации.
func ForkHintLine(hint string) string {
	return forkIndent + "рекомендация: " + strings.TrimSpace(hint)
}

// ForkOptionLine собирает подстроку варианта.
func ForkOptionLine(text string) string {
	return forkIndent + "вариант: " + strings.TrimSpace(text)
}

// ForkDecisionLine собирает подстроку решения.
func ForkDecisionLine(by, date, answer string) string {
	return fmt.Sprintf("%sрешено %s %s: %s", forkIndent, by, date, strings.TrimSpace(answer))
}

// ForkLeaveLine собирает подстроку передачи исполнителю. Причина
// необязательна: сам факт передачи с датой и автором это уже след.
func ForkLeaveLine(by, date, reason string) string {
	if reason = strings.TrimSpace(reason); reason == "" {
		return fmt.Sprintf("%sоставлена %s %s", forkIndent, by, date)
	}
	return fmt.Sprintf("%sоставлена %s %s: %s", forkIndent, by, date, reason)
}

// FindFork ищет развилку по имени.
func FindFork(doc, name string) (Fork, bool) {
	for _, f := range ParseForks(doc) {
		if f.Name == name {
			return f, true
		}
	}
	return Fork{}, false
}

// AddFork заводит развилку в перечне: голова, поле «решает», рекомендация и
// варианты ответа, если они есть. Раздела нет, значит он встаёт на своё
// место по форме. Занятое имя отказывает: перечень адресуется именем, и
// второй элемент с тем же именем сделал бы отказ ворот неразрешимым.
func AddFork(doc, name, question, who, hint string, opts ...string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("у развилки нет имени: имя от одного до трёх слов, по нему её называют ворота и человек")
	}
	if strings.Contains(name, "«") || strings.Contains(name, "»") {
		return "", fmt.Errorf("в имени развилки «ёлочки» не нужны: их ставит сама запись")
	}
	if who != ForkHuman && who != ForkExecutor {
		return "", fmt.Errorf("--who %q не из {%s, %s}", who, ForkHuman, ForkExecutor)
	}
	if _, ok := FindFork(doc, name); ok {
		return "", fmt.Errorf("развилка «%s» уже заведена: закрыть её ответом или дописать след, а второй с тем же именем не будет", name)
	}
	lines := []string{ForkHead(name, question), ForkWhoLine(who)}
	if strings.TrimSpace(hint) != "" {
		lines = append(lines, ForkHintLine(hint))
	}
	for _, o := range opts {
		if strings.TrimSpace(o) != "" {
			lines = append(lines, ForkOptionLine(o))
		}
	}
	return InsertIntoSection(doc, Forks, strings.Join(lines, "\n")), nil
}

// AppendToFork дописывает подстроку под голову названной развилки, после её
// последней подстроки. Незаведённое имя отказывает: молча заведённая ответом
// развилка прошла бы мимо того, кто задавал вопрос.
func AppendToFork(doc, name, sub string) (string, error) {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	from, to := forkSection(lines, mask)
	for i := from; i < to; i++ {
		if mask[i] {
			continue
		}
		m := forkHeadRe.FindStringSubmatch(lines[i])
		if m == nil || strings.TrimSpace(m[1]) != name {
			continue
		}
		end := i + 1
		for j := i + 1; j < to && !mask[j]; j++ {
			if forkSubRe.MatchString(lines[j]) {
				end = j + 1
				continue
			}
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			break
		}
		out := append([]string{}, lines[:end]...)
		out = append(out, sub)
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n"), nil
	}
	return "", fmt.Errorf("развилки «%s» в перечне нет: завести её через --ask", name)
}

// HoldingForks отбирает из перечня развилки, держащие старт задачи. Читают
// его ворота: `shipctl start` перед заведением ветки и `taskctl close` перед
// архивацией. Отбор один на обоих: разойдись они, задача заводилась бы с
// вопросом, который потом не даёт себя закрыть, и наоборот.
func HoldingForks(doc string) []Fork {
	var out []Fork
	for _, f := range ParseForks(doc) {
		if f.HoldsStart() {
			out = append(out, f)
		}
	}
	return out
}

// ForkGateNote собирает отказ ворот: имена развилок с вопросами, рекомендация,
// если она есть, и обе команды, которыми отказ снимается. Пустой список это
// пустая строка, и звать ворота на ней незачем.
func ForkGateNote(id, what string, forks []Fork) string {
	if len(forks) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: открытых развилок %d, решает человек, и до ответа %s не идёт:", id, len(forks), what)
	for _, f := range forks {
		fmt.Fprintf(&b, "\n- «%s»: %s", f.Name, f.Question)
		if f.Hint != "" {
			b.WriteString("\n  рекомендация: " + f.Hint)
		}
	}
	fmt.Fprintf(&b, "\nответ снимает развилку: taskctl decide %s «%s» --by человек \"ответ и довод\"", id, forks[0].Name)
	fmt.Fprintf(&b, "\nвопрос, на который отвечает исполнитель, передаётся ему: taskctl decide %s «%s» --leave", id, forks[0].Name)
	return b.String()
}

// ForkChoice это вариант ответа развилки, каким его получает нумерованный
// список в чате.
type ForkChoice struct {
	Text        string
	Recommended bool
}

// Choices собирает варианты по порядку номеров. Первой идёт рекомендация: она
// уже лежит своей подстрокой, и заводить под тем же текстом второй «вариант»
// значило бы держать одно решение в двух строках. Развилка без рекомендации
// нумеруется с первого «варианта», и рекомендованного среди них нет.
func (f Fork) Choices() []ForkChoice {
	var out []ForkChoice
	if hint := strings.TrimSpace(f.Hint); hint != "" {
		out = append(out, ForkChoice{Text: hint, Recommended: true})
	}
	for _, o := range f.Options {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, ForkChoice{Text: o})
		}
	}
	return out
}

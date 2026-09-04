package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// Файл замечаний это журнал чужого ревью: docs/tasks/<ID>.review.md рядом с
// файлом задачи (LLD DK-756, решение 2). Раздел «Ревью» файла задачи для него
// не годится, там замечания к моей работе, а тут мои к чужой. Формат читается
// человеком как markdown и разбирается утилитой: шапка со ссылкой на MR, sha
// ревью и уровнем, дальше блок на замечание. Ручная правка допустима, поэтому
// разбор терпит лишние пустые строки и порядок полей, а вот непонятную строку
// не проглатывает: тихо потерянное поле уехало бы в чужой MR неполным
// замечанием.

const (
	reviewLabelIssue      = "issue"
	reviewLabelSuggestion = "suggestion (non-blocking)"
	// reviewLabelSummary это отчёт владельцу строки: что прогнано, чего
	// ревьювер не смог и почему, что осталось открытым. Блок без файла и
	// строки, в MR он не публикуется (DK-797), publish снимает его с
	// припиской reviewSummaryNote.
	reviewLabelSummary = "итог"
	reviewSummaryNote  = "не публикуется"

	reviewStateNew       = "черновик"
	reviewStateApproved  = "одобрено"
	reviewStatePublished = "опубликовано"
	reviewStateDropped   = "снято"
)

var reviewLabels = []string{reviewLabelIssue, reviewLabelSuggestion, reviewLabelSummary}

var reviewStates = []string{reviewStateNew, reviewStateApproved, reviewStatePublished, reviewStateDropped}

// reviewHeadRe узнаёт заголовок блока. Номер в заголовке пишется для человека,
// а команды считают блоки по месту в файле: удалённый руками блок сдвигает
// номера соседей, и следующая запись перенумеровывает заголовки сама.
var reviewHeadRe = regexp.MustCompile(`^## Замечание( \d+)?$`)

// reviewLevelLineRe вытаскивает уровень и sha из строки уровня ревью, которую пишет
// review level в файл задачи. Голову строки судит taskform.IsReviewLevel, тут
// разбирается хвост.
var reviewLevelLineRe = regexp.MustCompile(`^Уровень ([0-3]) до ([0-9a-fA-F]{4,40}):`)

type reviewBlock struct {
	File   string
	Line   string
	Label  string
	State  string
	Thread string // id треда, вписывается публикацией
	// Note это приписка к состоянию: «снято, тред 42, MR закрыт». Судьба MR
	// снимает опубликованные блоки скопом (LLD DK-756, решение 7), и без
	// приписки снятое по своей воле не отличить от снятого закрытым MR.
	Note string
	Text string
}

// published говорит, ушёл ли блок в MR: такой блок повторной публикацией не
// трогается и снимать его командой уже поздно, тред живёт в чужом MR.
func (b reviewBlock) published() bool { return b.State == reviewStatePublished }

type reviewDraft struct {
	path  string
	id    string
	MR    string
	Sha   string
	Level string
	// Mark это пометка строки ревью: её печатают `taskctl list` и `show` под
	// строкой, и по ней видно, чего ждёт ревью, не открывая файла («автор
	// ответил», «автор молчит с 2026-09-02», «MR закрыт»).
	Mark   string
	Blocks []reviewBlock
}

func reviewDraftAbs(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".review.md")
}

func reviewDraftRel(id string) string {
	return filepath.Join("docs", "tasks", id+".review.md")
}

// render собирает файл целиком. Точечной правки тут нет в отличие от доски:
// файл целиком наш, а перенумерация заголовков после ручного удаления блока
// как раз и требует переписывания.
func (d *reviewDraft) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Замечания ревью %s\n\n", d.id)
	fmt.Fprintf(&b, "- MR: %s\n", d.MR)
	fmt.Fprintf(&b, "- ревью до: %s\n", d.Sha)
	fmt.Fprintf(&b, "- уровень: %s\n", d.Level)
	if d.Mark != "" {
		fmt.Fprintf(&b, "- пометка: %s\n", d.Mark)
	}
	for i, bl := range d.Blocks {
		fmt.Fprintf(&b, "\n## Замечание %d\n\n", i+1)
		if bl.File != "" {
			fmt.Fprintf(&b, "- файл: %s\n", bl.File)
		}
		if bl.Line != "" {
			fmt.Fprintf(&b, "- строка: %s\n", bl.Line)
		}
		fmt.Fprintf(&b, "- метка: %s\n", bl.Label)
		state := bl.State
		if bl.Thread != "" {
			state += ", тред " + bl.Thread
		}
		if bl.Note != "" {
			state += ", " + bl.Note
		}
		fmt.Fprintf(&b, "- состояние: %s\n\n%s\n", state, bl.Text)
	}
	return b.String()
}

func (d *reviewDraft) save() error {
	return os.WriteFile(d.path, []byte(d.render()), 0o644)
}

// cutReviewField режет строку поля «- ключ: значение». Разделитель это двоеточие с
// пробелом, иначе ссылка на MR («- MR: https://...») разъехалась бы по «://».
func cutReviewField(line string) (key, val string, ok bool) {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	if k, v, found := strings.Cut(body, ": "); found {
		return strings.TrimSpace(k), strings.TrimSpace(v), true
	}
	if strings.HasSuffix(body, ":") {
		return strings.TrimSpace(strings.TrimSuffix(body, ":")), "", true
	}
	return "", "", false
}

// cutReviewState разбирает значение поля «состояние»: само состояние, id треда
// и приписку. Запятая делит части, «тред <id>» узнаётся по первому слову, а
// всё прочее это приписка судьбы («MR закрыт»). Порядок частей у записи свой,
// но разбор его не сторожит: файл правят руками.
func cutReviewState(val string) (state, thread, note string) {
	var notes []string
	for i, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		switch {
		case i == 0:
			state = part
		case strings.HasPrefix(part, "тред "):
			thread = strings.TrimSpace(strings.TrimPrefix(part, "тред "))
		case part != "":
			notes = append(notes, part)
		}
	}
	return state, thread, strings.Join(notes, ", ")
}

func oneOfReview(val string, set []string) bool {
	for _, s := range set {
		if val == s {
			return true
		}
	}
	return false
}

// loadReviewDraft разбирает файл замечаний. Имя блока стоит в каждом отказе: файл
// правится руками, и «не разобрал» без места правится вслепую.
func loadReviewDraft(path, id string) (*reviewDraft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := &reviewDraft{path: path, id: id}
	lines := strings.Split(string(data), "\n")
	i := 0
	for ; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		if strings.HasPrefix(ln, "## ") {
			break
		}
		if ln == "" || strings.HasPrefix(ln, "# ") {
			continue
		}
		key, val, ok := cutReviewField(ln)
		if !ok {
			return nil, fmt.Errorf("%s, шапка: не разобрал строку %q, жду «- ключ: значение»", filepath.Base(path), ln)
		}
		switch strings.ToLower(key) {
		case "mr":
			d.MR = val
		case "ревью до":
			d.Sha = val
		case "уровень":
			d.Level = val
		case "пометка":
			d.Mark = val
		default:
			return nil, fmt.Errorf("%s, шапка: ключ %q не из списка (MR, ревью до, уровень, пометка)", filepath.Base(path), key)
		}
	}
	for i < len(lines) {
		name := fmt.Sprintf("замечание %d", len(d.Blocks)+1)
		head := strings.TrimSpace(lines[i])
		if !reviewHeadRe.MatchString(head) {
			return nil, fmt.Errorf("%s, %s: не разобрал заголовок %q, жду «## Замечание N»", filepath.Base(path), name, head)
		}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		var bl reviewBlock
		for i < len(lines) {
			ln := strings.TrimSpace(lines[i])
			// Пустая строка закрывает поля: список внутри текста реплики
			// начинается с того же маркера и полем считаться не должен.
			if ln == "" {
				i++
				break
			}
			if !strings.HasPrefix(ln, "- ") {
				break
			}
			key, val, ok := cutReviewField(ln)
			if !ok {
				return nil, fmt.Errorf("%s, %s: не разобрал строку %q, жду «- ключ: значение»", filepath.Base(path), name, ln)
			}
			switch strings.ToLower(key) {
			case "файл":
				bl.File = val
			case "строка":
				bl.Line = val
			case "метка":
				bl.Label = val
			case "состояние":
				bl.State, bl.Thread, bl.Note = cutReviewState(val)
			default:
				return nil, fmt.Errorf("%s, %s: ключ %q не из списка (файл, строка, метка, состояние)", filepath.Base(path), name, key)
			}
			i++
		}
		var text []string
		for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			text = append(text, lines[i])
			i++
		}
		bl.Text = strings.Trim(strings.Join(text, "\n"), "\n")
		if err := validateReviewBlock(bl, filepath.Base(path), name); err != nil {
			return nil, err
		}
		d.Blocks = append(d.Blocks, bl)
	}
	return d, nil
}

func validateReviewBlock(bl reviewBlock, file, name string) error {
	switch {
	case bl.Label == "":
		return fmt.Errorf("%s, %s: нет поля «метка»", file, name)
	case !oneOfReview(bl.Label, reviewLabels):
		return fmt.Errorf("%s, %s: метка %q не из списка (%s)", file, name, bl.Label, strings.Join(reviewLabels, ", "))
	case bl.State == "":
		return fmt.Errorf("%s, %s: нет поля «состояние»", file, name)
	case !oneOfReview(bl.State, reviewStates):
		return fmt.Errorf("%s, %s: состояние %q не из списка (%s)", file, name, bl.State, strings.Join(reviewStates, ", "))
	case bl.Text == "":
		return fmt.Errorf("%s, %s: нет текста реплики", file, name)
	case bl.Line != "" && bl.File == "":
		return fmt.Errorf("%s, %s: строка %s без файла, тред в MR привязать не к чему", file, name, bl.Line)
	}
	if bl.Line != "" {
		if _, err := strconv.Atoi(bl.Line); err != nil {
			return fmt.Errorf("%s, %s: строка %q не число", file, name, bl.Line)
		}
	}
	return nil
}

// openReviewDraft читает файл замечаний, а на первом замечании заводит его: шапка
// собирается из файла задачи, куда ссылку на MR кладёт trackctl review, а
// строку уровня taskctl review level. Пустая шапка не отказ на этом шаге:
// замечания копятся и до того, как уровень записан, отказывает публикация.
func openReviewDraft(root, id string, create bool) (*reviewDraft, error) {
	path := reviewDraftAbs(root, id)
	d, err := loadReviewDraft(path, id)
	if err == nil {
		return d, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if !create {
		return nil, fmt.Errorf("у %s нет файла замечаний %s: начни с review draft", id, reviewDraftRel(id))
	}
	d = &reviewDraft{path: path, id: id}
	d.MR, d.Level, d.Sha = reviewHeadFromTask(root, id)
	return d, nil
}

// reviewHeadFromTask вытаскивает из файла задачи то, что шапке нужно: ссылку на
// MR строкой «MR: <url>» и уровень со sha из строки уровня ревью.
func reviewHeadFromTask(root, id string) (mr, level, sha string) {
	data, err := os.ReadFile(taskFileAbs(root, id))
	if err != nil {
		return "", "", ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(ln)
		if mr == "" && strings.HasPrefix(t, "MR: ") {
			mr = strings.TrimSpace(strings.TrimPrefix(t, "MR: "))
		}
		if level == "" && taskform.IsReviewLevel(t) {
			if m := reviewLevelLineRe.FindStringSubmatch(t); m != nil {
				level, sha = m[1], m[2]
			}
		}
	}
	return mr, level, sha
}

// reviewMark отдаёт пометку строки ревью для печати доски. Файла замечаний у
// обычной строки нет, и молчание тут норма, а не находка: битый файл тоже
// молчит, о нём скажет любая команда ревью, а список доски ломать нечем.
func reviewMark(root, id string) string {
	d, err := loadReviewDraft(reviewDraftAbs(root, id), id)
	if err != nil {
		return ""
	}
	return d.Mark
}

// openIssues считает опубликованные блокирующие замечания: пока такое стоит,
// апрув не ставится ни в каком режиме (LLD DK-756, решение 6), а слитый MR
// поверх них это повод позвать человека (решение 7).
func (d *reviewDraft) openIssues() int {
	n := 0
	for _, bl := range d.Blocks {
		if bl.Label == reviewLabelIssue && bl.published() {
			n++
		}
	}
	return n
}

// normalizeReviewLabel принимает метку так, как её пишет человек: короткое
// «suggestion» разворачивается в метку conventional comments целиком.
func normalizeReviewLabel(label string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", reviewLabelIssue:
		return reviewLabelIssue, nil
	case "suggestion", reviewLabelSuggestion:
		return reviewLabelSuggestion, nil
	case reviewLabelSummary, "summary":
		return reviewLabelSummary, nil
	}
	return "", fmt.Errorf("метка %q не из списка, жду issue, suggestion или итог", label)
}

type reviewDraftParams struct {
	File  string
	Line  int
	Label string
}

// cmdReviewDraft кладёт замечание в файл черновиком. В MR оно уйдёт только
// после одобрения (или само, когда ключ publish в .devkit/review.conf стоит в
// auto): собранное здесь человек читает целиком, а не по одному треду в чужом
// MR.
func cmdReviewDraft(root, id, text string, p reviewDraftParams, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("жду текст замечания: review draft <ID> \"суть\"")
	}
	label, err := normalizeReviewLabel(p.Label)
	if err != nil {
		return "", err
	}
	if p.Line > 0 && p.File == "" {
		return "", fmt.Errorf("--line без --file: тред в MR привязать не к чему")
	}
	if label == reviewLabelSummary && (p.File != "" || p.Line > 0) {
		return "", fmt.Errorf("итоговый комментарий уровня идёт без файла и строки, он про ревью целиком")
	}
	if err := requireRow(root, id); err != nil {
		return "", err
	}
	d, err := openReviewDraft(root, id, true)
	if err != nil {
		return "", err
	}
	bl := reviewBlock{File: p.File, Label: label, State: reviewStateNew, Text: text}
	if p.Line > 0 {
		bl.Line = strconv.Itoa(p.Line)
	}
	d.Blocks = append(d.Blocks, bl)
	if err := d.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{reviewDraftRel(id)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: замечание %d записано черновиком в %s", id, len(d.Blocks), reviewDraftRel(id)) + tail, nil
}

// requireRow держит файл замечаний привязанным к строке доски: файл без строки
// уехал бы мимо архива и мимо ворот, а имя опечатанного ID иначе завело бы
// второй журнал молча.
func requireRow(root, id string) error {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return err
	}
	if b.find(id) == nil {
		return fmt.Errorf("%s нет на доске", id)
	}
	return nil
}

// cmdReviewApprove переводит блок в «одобрено»: в режиме confirm это и есть
// слово человека, после которого публикация несёт замечание в чужой MR.
func cmdReviewApprove(root, id string, num int, c CommitOpts) (string, error) {
	return reviewDraftSetState(root, id, num, reviewStateApproved, "одобрено", c)
}

// cmdReviewDrop снимает блок: человек прочитал и решил не нести его автору.
// Снятый блок остаётся в файле журналом, из публикации он выпадает.
func cmdReviewDrop(root, id string, num int, c CommitOpts) (string, error) {
	return reviewDraftSetState(root, id, num, reviewStateDropped, "снято", c)
}

func reviewDraftSetState(root, id string, num int, state, word string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	d, err := openReviewDraft(root, id, false)
	if err != nil {
		return "", err
	}
	if num < 1 || num > len(d.Blocks) {
		return "", fmt.Errorf("замечания %d нет, в %s их %d", num, reviewDraftRel(id), len(d.Blocks))
	}
	bl := &d.Blocks[num-1]
	if bl.published() {
		return "", fmt.Errorf("замечание %d уже опубликовано (тред %s), в чужом MR его снимает ответ в треде, а не файл", num, bl.Thread)
	}
	if bl.State == state {
		return "", fmt.Errorf("замечание %d уже %s", num, word)
	}
	bl.State = state
	if err := d.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{reviewDraftRel(id)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: замечание %d %s (%s)", id, num, word, firstReviewLine(bl.Text)) + tail, nil
}

// firstReviewLine это первая строка текста для эха команды: подтверждение показывает,
// что именно одобрено, а не только номер.
func firstReviewLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

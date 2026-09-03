package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

const reviewHeading = "## Ревью"

type reviewNote struct {
	LineIdx int
	Text    string // текст замечания без маркера списка, продолжения прирастают через пробел
	Span    int    // число строк элемента: маркерная плюс строки продолжения
}

// resolvedTailRe узнаёт исход по месту, где его пишет cmdReviewResolve
// ("<текст>: исправлено" либо "<текст>: отклонено, причина"), а не по факту
// появления слова где-то в тексте: цитата замечания, пересказывающая формат
// команды («...словами «исправлено»»), этому хвосту не соответствует и
// замечание не закрывает (DK-514).
var resolvedTailRe = regexp.MustCompile(`: (исправлено|отклонено)(,.*)?$`)

// cleanVerdictRe узнаёт чистый вердикт по голове элемента, куда его пишет
// ревьювер («Вердикт: без замечаний. пояснение» либо «замечаний нет» без
// пояснения), а не по факту оборота где-то в тексте: суть замечания,
// кончающаяся словами «про вердикт без замечаний нет ничего», голове не
// соответствует и замечание не закрывает (DK-469). За фразой идёт точка,
// двоеточие или конец текста, поэтому «замечаний неточностей» и продолжение
// фразы перечислением вердиктом не считаются. Маркер списка терпится ради
// копии в shipctl, где элементы разбираются вместе с маркером.
var cleanVerdictRe = regexp.MustCompile(`^(?:[-*] )?(?:(?:вердикт|ревью):\s*)?(?:без замечаний|замечаний нет)(?:[.:]|$)`)

// outcome возвращает исход замечания: «исправлено», «отклонено», «чисто»
// (ревью без замечаний, не требующее исхода) или пустую строку у открытого.
// Критерий тот же, каким shipctl merge решает, закрыто ли ревью. Порядок
// проверок исход -> чистый итог -> открыто, поэтому «гибрид: исправлено,
// теперь без замечаний» остаётся закрытым, а не уходит в чистый вердикт.
func (n reviewNote) outcome() string {
	low := strings.ToLower(n.Text)
	if m := resolvedTailRe.FindStringSubmatch(low); m != nil {
		return m[1]
	}
	if cleanVerdictRe.MatchString(low) {
		return "чисто"
	}
	return ""
}

// reviewFile это файл задачи с разобранным разделом «Ревью». Правки, как и у
// доски, точечные: нетронутые строки сохраняются байт в байт.
type reviewFile struct {
	path       string
	lines      []string
	notes      []reviewNote
	hasSection bool
	insertAt   int // строка, перед которой вставлять новое замечание
	bodyAt     int // первая строка тела раздела: туда встаёт строка уровня
	levelIdx   int // строка уровня ревью, -1 если её нет
}

func loadReview(path string) (*reviewFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rf := &reviewFile{path: path, lines: strings.Split(string(data), "\n"), levelIdx: -1}
	in := false
	contOpen := false
	for i, ln := range rf.lines {
		if strings.HasPrefix(ln, "## ") {
			if in {
				break
			}
			if strings.HasPrefix(ln, reviewHeading) {
				in, rf.hasSection = true, true
				rf.insertAt = i + 1
				if i+1 < len(rf.lines) && strings.TrimSpace(rf.lines[i+1]) == "" {
					rf.insertAt = i + 2
				}
				rf.bodyAt = rf.insertAt
			}
			continue
		}
		t := strings.TrimSpace(ln)
		if !in {
			continue
		}
		// Строка уровня стоит первой в разделе и списком не является: её
		// пишет review level, а замечания идут ниже. Считается только первая,
		// повторный вызов её же и переписывает. insertAt сдвигается следом за
		// неё: без этого новое замечание, добавленное после level, легло бы
		// перед строкой уровня, и следующий парс принял бы уровень за
		// продолжение замечания, слив их в одну строку (DK-760).
		if rf.levelIdx < 0 && len(rf.notes) == 0 && taskform.IsReviewLevel(t) {
			rf.levelIdx = i
			rf.insertAt = i + 1
			if rf.insertAt < len(rf.lines) && strings.TrimSpace(rf.lines[rf.insertAt]) == "" {
				rf.insertAt++
			}
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			rf.notes = append(rf.notes, reviewNote{LineIdx: i, Text: strings.TrimSpace(t[2:]), Span: 1})
			rf.insertAt = i + 1
			contOpen = true
			continue
		}
		// Пустая строка закрывает текущий элемент: абзац после неё к замечанию
		// не прирастает, иначе исход из чужого абзаца закрыл бы замечание и
		// разошёлся бы с shipctl, чей reviewItems сбрасывает элемент на пустой
		// строке.
		if t == "" {
			contOpen = false
			continue
		}
		// Строка продолжения прирастает к последнему замечанию, пока элемент
		// не закрыт пустой строкой или новым маркером: длинное замечание,
		// перенесённое на несколько строк, судится целиком, и исход на строке
		// переноса закрывает его. Абзац без маркера вне элемента игнорируется
		// (чистый вердикт абзацем замечанием не считается).
		if contOpen && len(rf.notes) > 0 {
			n := &rf.notes[len(rf.notes)-1]
			if n.Text != "" {
				n.Text += " "
			}
			n.Text += t
			n.Span++
			rf.insertAt = i + 1
		}
	}
	return rf, nil
}

// ensureSection заводит раздел «Ревью», когда первое замечание пришло на файл
// без него. Место разделу выбирает форма (TASKFORM.md), а не конец файла:
// сценарий проверки к первому замечанию обычно уже написан, и приклеенный за
// ним раздел ловил бы сторож порядка на каждой отревьюенной задаче.
func (rf *reviewFile) ensureSection() {
	if rf.hasSection {
		return
	}
	rf.hasSection = true
	if at := formSectionAt(rf.lines, reviewHeading); at >= 0 {
		head := []string{reviewHeading, "", ""}
		if at > 0 && strings.TrimSpace(rf.lines[at-1]) != "" {
			head = append([]string{""}, head...)
		}
		tail := append([]string{}, rf.lines[at:]...)
		rf.lines = append(rf.lines[:at], append(head, tail...)...)
		rf.insertAt = at + len(head) - 1
		rf.bodyAt = rf.insertAt
		return
	}
	// Разделов формы ниже «Ревью» в файле нет, значит место ему в конце, и
	// хвостовые пустые строки перед ним лишние.
	for len(rf.lines) > 0 && strings.TrimSpace(rf.lines[len(rf.lines)-1]) == "" {
		rf.lines = rf.lines[:len(rf.lines)-1]
	}
	rf.lines = append(rf.lines, "", reviewHeading, "", "")
	rf.insertAt = len(rf.lines) - 1
	rf.bodyAt = rf.insertAt
}

func (rf *reviewFile) insert(idx int, line string) {
	rf.lines = append(rf.lines[:idx], append([]string{line}, rf.lines[idx:]...)...)
}

func (rf *reviewFile) save() error {
	return os.WriteFile(rf.path, []byte(strings.Join(rf.lines, "\n")), 0o644)
}

func taskFileAbs(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// reviewItem дописывает в раздел «Ревью» готовую строку элемента и заводит
// файл задачи, когда его ещё нет. Общий ход у add и clean: разница между ними
// только в тексте элемента и в проверках до записи.
func reviewItem(root, id, line string, c CommitOpts) (*reviewFile, bool, string, []string, error) {
	return reviewEdit(root, id, func(rf *reviewFile) {
		rf.insert(rf.insertAt, line)
		// Первое замечание, вставленное сразу после строки уровня, отделяется
		// от следующего раздела пустой строкой: insertAt в этом случае уже
		// перескочил через пробел-разделитель между уровнем и списком, и без
		// новой пустой строки список слипся бы со следующим заголовком
		// (DK-760).
		if rf.levelIdx >= 0 && len(rf.notes) == 0 &&
			rf.insertAt+1 < len(rf.lines) && strings.TrimSpace(rf.lines[rf.insertAt+1]) != "" {
			rf.insert(rf.insertAt+1, "")
		}
	}, c)
}

// reviewEdit готовит раздел «Ревью» к правке (строка доски, файл задачи,
// ссылка в строке) и отдаёт саму правку вызывающему: add и clean дописывают
// элемент списка, level правит строку уровня в голове раздела.
func reviewEdit(root, id string, edit func(*reviewFile), c CommitOpts) (rf *reviewFile, created bool, linkHint string, paths []string, err error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return nil, false, "", nil, err
	}
	row := b.find(id)
	if row == nil {
		return nil, false, "", nil, fmt.Errorf("%s нет на доске", id)
	}
	paths = []string{filepath.Join("docs", "tasks", id+".md")}
	created, err = ensureTaskFile(root, id, row)
	if err != nil {
		return nil, false, "", nil, err
	}
	// Запись ревью остаётся разрешена из worktree (в отличие от file), а вот
	// ссылку в строке доски там не чинит: доску правит только диспетчер в
	// основном чекауте (RULES.board.md, «Доска в руках диспетчера»).
	if created {
		rel := fmt.Sprintf("tasks/%s.md", id)
		switch {
		case linkedWorktree(root):
			linkHint = fmt.Sprintf(", ссылку на docs/%s поправь в основном чекауте: taskctl file %s", rel, id)
		default:
			if want := fmt.Sprintf("[%s](%s)", rel, rel); row.Link != want {
				row.Link = want
				b.Lines[row.LineIdx] = formatRow(row)
				if err := b.Save(); err != nil {
					return nil, false, "", nil, err
				}
				paths = append(paths, filepath.Join("docs", "TASKS.md"))
			}
		}
	}
	rf, err = loadReview(taskFileAbs(root, id))
	if err != nil {
		return nil, false, "", nil, err
	}
	rf.ensureSection()
	edit(rf)
	if err := rf.save(); err != nil {
		return nil, false, "", nil, err
	}
	return rf, created, linkHint, paths, nil
}

func cmdReviewAdd(root, id, note string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return "", fmt.Errorf("жду текст замечания: review add <ID> \"суть\"")
	}
	if strings.Contains(note, "\n") {
		return "", fmt.Errorf("замечание пишется одной строкой")
	}
	rf, created, linkHint, paths, err := reviewItem(root, id, "- "+note, c)
	if err != nil {
		return "", err
	}
	tail, err := c.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s: замечание %d записано", id, len(rf.notes)+1)
	if created {
		msg += ", файл задачи создан"
	}
	return msg + linkHint + tail, nil
}

// headSha это HEAD дерева, где идёт ревью. Строка уровня несёт его не для
// красоты: второй круг ревью диффует от него, поэтому запись без sha
// бессмысленна и нечитаемый HEAD это отказ, а не строка без хвоста.
func headSha(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short=7", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("в %s не читается HEAD (%v), а строка уровня несёт коммит, по которому шло ревью", root, err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("в %s не читается HEAD, а строка уровня несёт коммит, по которому шло ревью", root)
	}
	return sha, nil
}

// cmdReviewLevel пишет уровень тщательности ревью первой строкой раздела
// «Ревью»: «Уровень 2 до a1b2c3d: неопределённость 1, тронут tools/shipctl».
// Уровень выбирает скилл review до чтения диффа, и запись эта машинная, по ней
// ворот слияния отличает ревью, прошедшее мимо скилла, от прошедшего по нему.
// Уровень 0 значит осознанный пропуск, поэтому причина обязательна на всех
// уровнях: незаписанный пропуск неотличим от забытого ревью. Повторный вызов
// переписывает строку, а не кладёт вторую: пересмотр уровня по ходу ревью это
// та же запись, только с новым основанием.
func cmdReviewLevel(root, id string, level int, reason string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if level < 0 || level > 3 {
		return "", fmt.Errorf("уровень %d вне шкалы, жду 0-3 (шкала в скилле review)", level)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", fmt.Errorf("жду причину уровня: review level <ID> <0-3> \"причина\"")
	}
	if strings.Contains(reason, "\n") {
		return "", fmt.Errorf("причина пишется одной строкой")
	}
	sha, err := headSha(root)
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("Уровень %d до %s: %s", level, sha, reason)
	rewritten := false
	_, created, linkHint, paths, err := reviewEdit(root, id, func(rf *reviewFile) {
		if rf.levelIdx >= 0 {
			rf.lines[rf.levelIdx] = line
			rewritten = true
			return
		}
		rf.insert(rf.bodyAt, line)
		// Замечания ниже отделяются пустой строкой: абзац, слипшийся с первым
		// элементом списка, разметка читает одним куском.
		if rf.bodyAt+1 < len(rf.lines) && strings.TrimSpace(rf.lines[rf.bodyAt+1]) != "" {
			rf.insert(rf.bodyAt+1, "")
		}
	}, c)
	if err != nil {
		return "", err
	}
	tail, err := c.apply(root, paths)
	if err != nil {
		return "", err
	}
	verb := "записан"
	if rewritten {
		verb = "переписан"
	}
	msg := fmt.Sprintf("%s: уровень ревью %d до %s %s", id, level, sha, verb)
	if created {
		msg += ", файл задачи создан"
	}
	return msg + linkHint + tail, nil
}

// cleanVerdictLine это канон формы записи: голова «Вердикт: без замечаний.»
// узнаётся критерием исхода (cleanVerdictRe и его копия в shipctl), а
// пояснение живёт за ней и на разбор не влияет.
const cleanVerdictHead = "Вердикт: без замечаний."

// cmdReviewClean записывает вердикт ревью, прошедшего без замечаний. Раздел
// «Ревью» с таким элементом машинно отличим от отсутствия ревью, ради чего
// команда и заводится (LLD DK-460, «Что меняется в строках», п. 1): раньше
// чистый исход изображали замечанием с текстом «замечаний нет».
func cmdReviewClean(root, id, note string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if strings.Contains(note, "\n") {
		return "", fmt.Errorf("пояснение пишется одной строкой")
	}
	// Открытое замечание и чистый вердикт в одном разделе противоречат друг
	// другу: ворот замечаний увидел бы открытый пункт, а ворот следа ревью
	// чистый итог. Замечание закрывают резолвом, а не вердиктом поверх.
	if rf, err := loadReview(taskFileAbs(root, id)); err == nil {
		for i, n := range rf.notes {
			switch n.outcome() {
			case "":
				return "", fmt.Errorf("замечание %d в ревью %s открыто, чистый вердикт ему противоречит: закрой его через review resolve", i+1, id)
			case "чисто":
				return "", fmt.Errorf("чистый вердикт в ревью %s уже записан: %s", id, n.Text)
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	line := "- " + cleanVerdictHead
	if note != "" {
		line += " " + note
	}
	rf, created, linkHint, paths, err := reviewItem(root, id, line, c)
	if err != nil {
		return "", err
	}
	tail, err := c.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s: вердикт без замечаний записан, элементов в ревью %d", id, len(rf.notes)+1)
	if created {
		msg += ", файл задачи создан"
	}
	return msg + linkHint + tail, nil
}

var outcomeNames = map[string]string{"fixed": "исправлено", "rejected": "отклонено"}

func cmdReviewResolve(root, id string, num int, outcome, reason string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	word, ok := outcomeNames[outcome]
	if !ok {
		return "", fmt.Errorf("неизвестный исход %q, жду fixed или rejected", outcome)
	}
	reason = strings.TrimSpace(reason)
	if outcome == "rejected" && reason == "" {
		return "", fmt.Errorf("для rejected обязателен --reason: отклонение фиксируется с причиной")
	}
	rf, err := loadReview(taskFileAbs(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("у %s нет файла задачи, ревью пусто", id)
		}
		return "", err
	}
	if num < 1 || num > len(rf.notes) {
		return "", fmt.Errorf("замечания %d нет, в ревью %s их %d (номера смотрит review show)", num, id, len(rf.notes))
	}
	n := rf.notes[num-1]
	if o := n.outcome(); o != "" {
		return "", fmt.Errorf("замечание %d уже закрыто (%s): %s", num, o, n.Text)
	}
	line := "- " + n.Text + ": " + word
	if reason != "" {
		line += ", " + reason
	}
	// Многострочный элемент схлопываем в одну строку: loadReview собрал его
	// продолжения в n.Text, и строки переноса из файла уходят, иначе резолвнутая
	// строка повторяла бы их после себя.
	span := n.Span
	if span < 1 {
		span = 1
	}
	rf.lines = append(rf.lines[:n.LineIdx], append([]string{line}, rf.lines[n.LineIdx+span:]...)...)
	if err := rf.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "tasks", id+".md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: замечание %d %s", id, num, word) + tail, nil
}

func cmdReviewShow(root, id string) (string, error) {
	rf, err := loadReview(taskFileAbs(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("у %s нет файла задачи, ревью пусто", id)
		}
		return "", err
	}
	var out []string
	// Уровень идёт над замечаниями, как и в файле: с него ревьювер начинает и
	// по нему второй круг понимает, от какого коммита диффовать.
	if rf.levelIdx >= 0 {
		out = append(out, strings.TrimSpace(rf.lines[rf.levelIdx]))
	}
	if len(rf.notes) == 0 {
		if len(out) > 0 {
			return out[0] + "\nзамечаний нет", nil
		}
		return fmt.Sprintf("в ревью %s замечаний нет", id), nil
	}
	for i, n := range rf.notes {
		st := n.outcome()
		if st == "" {
			st = "открыто"
		}
		out = append(out, fmt.Sprintf("%d. [%s] %s", i+1, st, n.Text))
	}
	return strings.Join(out, "\n"), nil
}

func cmdReviewStats(root string) (string, error) {
	type agg struct{ tasks, notes, fixed, rejected, open int }
	var live, arch agg
	var openList []string
	count := func(files []string, a *agg) error {
		for _, f := range files {
			rf, err := loadReview(f)
			if err != nil {
				return err
			}
			if len(rf.notes) == 0 {
				continue
			}
			a.tasks++
			for i, n := range rf.notes {
				if n.outcome() == "чисто" {
					continue
				}
				a.notes++
				switch n.outcome() {
				case "исправлено":
					a.fixed++
				case "отклонено":
					a.rejected++
				default:
					a.open++
					id := strings.TrimSuffix(filepath.Base(f), ".md")
					openList = append(openList, fmt.Sprintf("%s, замечание %d: %s", id, i+1, n.Text))
				}
			}
		}
		return nil
	}
	liveFiles, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "*.md"))
	archFiles, _ := filepath.Glob(filepath.Join(root, "docs", "tasks", "archive", "*", "*.md"))
	if err := count(liveFiles, &live); err != nil {
		return "", err
	}
	if err := count(archFiles, &arch); err != nil {
		return "", err
	}
	total := agg{live.tasks + arch.tasks, live.notes + arch.notes,
		live.fixed + arch.fixed, live.rejected + arch.rejected, live.open + arch.open}
	if total.tasks == 0 {
		return "разделов «Ревью» пока нет ни в живых задачах, ни в архиве", nil
	}
	out := []string{
		fmt.Sprintf("задач с ревью: %d (живых %d, в архиве %d)", total.tasks, live.tasks, arch.tasks),
		fmt.Sprintf("замечаний %d: исправлено %d, отклонено %d, открыто %d",
			total.notes, total.fixed, total.rejected, total.open),
	}
	if closed := total.fixed + total.rejected; closed > 0 {
		out = append(out, fmt.Sprintf("доля исправленных среди закрытых: %d%%", total.fixed*100/closed))
	}
	if len(openList) > 0 {
		out = append(out, "открытые:")
		out = append(out, openList...)
	}
	return strings.Join(out, "\n"), nil
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Исходов у разобранного черновика четыре (docs/lld/DK-129-groom-process.md,
// решение 4): строка на доске, приписка к стоящей строке, пометка с причиной и
// удаление протухшего. Первый доводит add --id, остальные три живут здесь.
// Кодом они стали потому, что результат разбора обязан пережить сессию на
// диске; второй довод решения, отбой ручной правки docs/tasks/ в pre-push,
// после DK-119 отпал (пуш с диффом только по доске рубеж пропускает сам),
// но исходы остаются командами.

// draftHeadingRe находит заголовок черновика любого уровня ниже первого.
var draftHeadingRe = regexp.MustCompile(`^#{2,} `)

// draftText это файл черновика построчно: правки точечные, чужие разделы
// сохраняются как есть.
type draftText struct {
	path  string
	lines []string
}

func loadDraftText(path string) (*draftText, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &draftText{path: path, lines: strings.Split(string(data), "\n")}, nil
}

func (t *draftText) save() error {
	return os.WriteFile(t.path, []byte(strings.Join(t.lines, "\n")), 0o644)
}

// groomRange находит границы раздела «Грумминг»: от его заголовка до следующего
// заголовка того же уровня или до конца файла.
func (t *draftText) groomRange() (int, int, bool) {
	for i, ln := range t.lines {
		if strings.TrimSpace(ln) != draftGroomHeading {
			continue
		}
		end := len(t.lines)
		for j := i + 1; j < len(t.lines); j++ {
			if strings.HasPrefix(t.lines[j], "## ") {
				end = j
				break
			}
		}
		return i, end, true
	}
	return 0, 0, false
}

// trimTail срезает пустой хвост, чтобы дописанный раздел не уехал через две
// пустые строки, а файл кончался ровно одним переводом строки.
func (t *draftText) trimTail() {
	for len(t.lines) > 0 && strings.TrimSpace(t.lines[len(t.lines)-1]) == "" {
		t.lines = t.lines[:len(t.lines)-1]
	}
}

// setDefer ставит пометку: раздела не было, значит он дописывается в конец, а
// был, значит заменяется целиком. Повторный defer причину меняет, а не копит
// вторую строку: разбору нужна текущая причина, а не история отказов.
func (t *draftText) setDefer(line string) {
	block := []string{draftGroomHeading, "", line}
	if s, e, ok := t.groomRange(); ok {
		tail := append([]string{}, t.lines[e:]...)
		t.lines = append(t.lines[:s], block...)
		if len(tail) > 0 {
			t.lines = append(t.lines, "")
			t.lines = append(t.lines, tail...)
			return
		}
		t.lines = append(t.lines, "")
		return
	}
	t.trimTail()
	t.lines = append(t.lines, "")
	t.lines = append(t.lines, block...)
	t.lines = append(t.lines, "")
}

// clearDefer снимает раздел целиком, вместе с заголовком: оставленный пустой
// заголовок читался бы как «черновик всё ещё отложен».
func (t *draftText) clearDefer() bool {
	s, e, ok := t.groomRange()
	if !ok {
		return false
	}
	t.lines = append(t.lines[:s], t.lines[e:]...)
	t.trimTail()
	t.lines = append(t.lines, "")
	return true
}

// ensureWritten проставляет дату записи старому черновику, у которого её нет.
// Зовётся до первой записи в файл, пока время правки ещё то самое: иначе
// пометка сбивала бы ровно тот возраст, ради которого черновик и показывают.
// Строка ищется только в шапке: «записан ...» в теле идеи это текст, и он
// не мешает шапке получить дату.
func (t *draftText) ensureWritten(mod time.Time) bool {
	if mod.IsZero() {
		return false
	}
	for _, ln := range t.lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "## ") {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(ln), draftWrittenPrefix) {
			return false
		}
	}
	at := 0
	for i, ln := range t.lines {
		if strings.TrimSpace(ln) != "" {
			at = i + 1
			break
		}
	}
	ins := []string{"", draftWrittenPrefix + mod.Format(draftDateLayout)}
	if at < len(t.lines) && strings.TrimSpace(t.lines[at]) != "" {
		ins = append(ins, "")
	}
	t.lines = append(t.lines[:at], append(ins, t.lines[at:]...)...)
	return true
}

// setPrio ставит строку метки разбора в шапку, рядом со строкой «записан», а
// была метка, значит заменяется: повторная простановка уровень меняет, а не
// копит вторую строку. Ищется только шапка: строка «приоритет:» в теле идеи
// это текст, и команда её не трогает.
func (t *draftText) setPrio(word string) {
	for i, ln := range t.lines {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "## ") {
			break
		}
		if strings.HasPrefix(ln, draftPrioPrefix) {
			t.lines[i] = draftPrioPrefix + word
			return
		}
	}
	at := 0
	for i, ln := range t.lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "## ") {
			break
		}
		if strings.HasPrefix(strings.TrimSpace(ln), draftWrittenPrefix) {
			at = i + 1
			break
		}
		if at == 0 && strings.TrimSpace(ln) != "" {
			at = i + 1
		}
	}
	ins := []string{draftPrioPrefix + word}
	t.lines = append(t.lines[:at], append(ins, t.lines[at:]...)...)
}

// clearPrio снимает строку метки разбора из шапки и отвечает, была ли она.
// Пустую строку, оставшуюся на её месте, файл и так носит между шапкой и
// разделами, отдельной уборки она не требует.
func (t *draftText) clearPrio() bool {
	out := t.lines[:0]
	head, removed := true, false
	for _, ln := range t.lines {
		trimmed := strings.TrimSpace(ln)
		if head && strings.HasPrefix(trimmed, "## ") {
			head = false
		}
		if head && strings.HasPrefix(trimmed, draftPrioPrefix) {
			removed = true
			continue
		}
		out = append(out, ln)
	}
	t.lines = out
	return removed
}

// findDraftFor находит черновик по ID и отказывает понятной строкой: команды
// разбора зовутся по списку draft list, и опечатка в ID там обычное дело.
func findDraftFor(root, id string) (*Draft, error) {
	drafts, err := loadDrafts(root)
	if err != nil {
		return nil, err
	}
	d := findDraft(drafts, id)
	if d == nil {
		return nil, fmt.Errorf("%s нет в накопителе черновиков (список: taskctl draft list)", id)
	}
	return d, nil
}

// cmdDraftDefer помечает черновик отложенным с причиной либо снимает пометку.
// Живёт она в самом файле черновика: так она едет с ним в гит, видна человеку
// без утилиты, а при оформлении переезжает в docs/tasks/<ID>.md вместе с
// остальным текстом и остаётся историей разбора.
func cmdDraftDefer(root, id, reason string, clear bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "draft defer"); err != nil {
		return "", err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	switch {
	case clear && reason != "":
		return "", fmt.Errorf("--clear снимает пометку целиком, причина ему не нужна")
	case !clear && reason == "":
		return "", fmt.Errorf("жду причину: taskctl draft defer %s \"чего ждём\" либо taskctl draft defer %s --clear", id, id)
	}
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	t, err := loadDraftText(d.Path)
	if err != nil {
		return "", err
	}
	rel := draftRel(id)
	if clear {
		if !t.clearDefer() {
			return fmt.Sprintf("%s: пометки об отложенном не было, файл не тронут", id), nil
		}
		if err := t.save(); err != nil {
			return "", err
		}
		tail, err := c.apply(root, []string{rel})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: пометка снята%s", id, tail), nil
	}
	stamped := t.ensureWritten(d.Mod)
	date := time.Now().Format(draftDateLayout)
	t.setDefer(fmt.Sprintf("- %s, отложен: %s", date, reason))
	if err := t.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{rel})
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s: отложен %s, причина в разделе «Грумминг» файла %s", id, date, filepath.ToSlash(rel))
	if stamped {
		msg += fmt.Sprintf(", дата записи проставлена (%s)", d.Mod.Format(draftDateLayout))
	}
	return msg + tail, nil
}

// cmdDraftPrio помечает черновик уровнем разбора high / mid / low либо снимает
// метку. Шкала грубая и на глаз: RANKING.md на черновик не ложится, потому что
// оценивает задачу, которой ещё нет, а накопителю нужен ответ «что разбирать
// следующим». Метка живёт в шапке файла рядом со строкой «записан» и переживает
// откладывание, но не оформление.
func cmdDraftPrio(root, id, level string, clear bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "draft prio"); err != nil {
		return "", err
	}
	word := draftPrioWords[level]
	switch {
	case clear && level != "":
		return "", fmt.Errorf("--clear снимает метку целиком, уровень ему не нужен")
	case !clear && level == "":
		return "", fmt.Errorf("жду уровень: taskctl draft prio %s high|mid|low либо taskctl draft prio %s --clear", id, id)
	case !clear && word == "":
		return "", fmt.Errorf("уровень %q не из шкалы: taskctl draft prio %s high|mid|low, снимается метка через --clear", level, id)
	}
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	t, err := loadDraftText(d.Path)
	if err != nil {
		return "", err
	}
	rel := draftRel(id)
	if clear {
		if !t.clearPrio() {
			return fmt.Sprintf("%s: метки разбора не было, файл не тронут", id), nil
		}
		if err := t.save(); err != nil {
			return "", err
		}
		tail, err := c.apply(root, []string{rel})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: метка разбора снята%s", id, tail), nil
	}
	stamped := t.ensureWritten(d.Mod)
	t.setPrio(word)
	if err := t.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{rel})
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s: приоритет разбора %s", id, word)
	if stamped {
		msg += fmt.Sprintf(", дата записи проставлена (%s)", d.Mod.Format(draftDateLayout))
	}
	return msg + tail, nil
}

// draftSection собирает раздел для файла задачи: текст черновика целиком, без
// своего заголовка первого уровня и с разделами уровнем ниже, чтобы они не
// разрывали раздел приписки. Заголовок черновика и дата записи уезжают в
// заголовок раздела: в теле первой строки нет (форма кладёт её только в H1),
// а дата повисла бы голой строкой без всякого контекста, тогда как знать,
// о чём и когда мысль записана, по ней потом и нужно.
func draftSection(id, text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var body []string
	head, written, draftTitle, top, fence := true, "", "", true, ""
	for _, ln := range lines {
		if head {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			head = false
			if strings.HasPrefix(strings.TrimSpace(ln), "# ") {
				draftTitle = draftTitleLine(strings.TrimSpace(ln))
				continue
			}
		}
		// Забор блока кода считается тем же порядком, что в
		// rewriteLinksSkippingCodeBlocks: решётка внутри примера команды это
		// текст, а не заголовок, и опускать её незачем.
		if m := fenceRe.FindStringSubmatch(ln); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(ln[len(m[0]):]) == "":
				fence = ""
			}
		}
		// Опускается любой заголовок черновика, а не только второго уровня:
		// иначе его «### » встал бы вровень с опущенным «## » и разделы
		// перепутались бы местами.
		if fence == "" && draftHeadingRe.MatchString(ln) {
			ln, top = "#"+ln, false
		}
		if top && written == "" && strings.HasPrefix(strings.TrimSpace(ln), draftWrittenPrefix) {
			written = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), draftWrittenPrefix))
			continue
		}
		// Метка разбора уезжает тем же правилом, что дата записи: метаданные в
		// файле задачи не дублируются, и метка не переживает черновик.
		if top && strings.HasPrefix(strings.TrimSpace(ln), draftPrioPrefix) {
			continue
		}
		body = append(body, ln)
	}
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	title := fmt.Sprintf("## Из черновика %s", id)
	if draftTitle != "" {
		title += " «" + draftTitle + "»"
	}
	if written != "" {
		title += fmt.Sprintf(" (%s%s)", draftWrittenPrefix, written)
	}
	out := append([]string{title, ""}, body...)
	return strings.Join(out, "\n") + "\n"
}

// cmdDraftAttach приписывает черновик к стоящей строке: текст уезжает разделом
// в файл задачи, а сам черновик удаляется. Содержимое так не теряется (у дубля
// бывает своя репродукция, которой в задаче нет), а ссылка «уехал в <TASK-ID>»
// живёт сообщением коммита.
func cmdDraftAttach(root, id, taskID string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "draft attach"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(taskID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске, приписывать некуда", taskID)
	}
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	text, err := os.ReadFile(d.Path)
	if err != nil {
		return "", err
	}
	paths := []string{filepath.Join("docs", "tasks", taskID+".md")}
	// Файл задачи заводится общим ensureTaskFile, и вместе с ним берётся всё
	// поведение прецедента cmdReviewAdd, включая ссылку в строке доски: приписка
	// это ровно тот случай, когда у задачи появилось содержимое, а файла ещё нет.
	created, err := ensureTaskFile(root, taskID, row)
	if err != nil {
		return "", err
	}
	if created {
		if want := fmt.Sprintf("[tasks/%s.md](tasks/%s.md)", taskID, taskID); row.Link != want {
			row.Link = want
			b.Lines[row.LineIdx] = formatRow(row)
			if err := b.Save(); err != nil {
				return "", err
			}
			paths = append(paths, filepath.Join("docs", "TASKS.md"))
		}
	}
	abs := taskFileAbs(root, taskID)
	old, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	body := strings.TrimRight(string(old), "\n") + "\n\n" + draftSection(id, string(text))
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return "", err
	}
	staged, err := removeDraft(root, d.Path)
	if err != nil {
		return "", err
	}
	if staged {
		paths = append(paths, draftRel(id))
	}
	tail, err := c.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s: текст приписан к %s разделом «Из черновика %s», черновик удалён", id, taskID, id)
	if created {
		msg += fmt.Sprintf(", файл docs/tasks/%s.md создан и ссылка в строке обновлена", taskID)
	}
	return msg + tail, nil
}

// cmdDraftDrop удаляет протухший черновик. Причина обязательна и печатается:
// файла после команды нет, и в гите она живёт только сообщением коммита,
// поэтому молча удалить черновик команда не даёт.
func cmdDraftDrop(root, id, reason string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "draft drop"); err != nil {
		return "", err
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return "", fmt.Errorf("жду причину: taskctl draft drop %s --reason \"чего больше нет\"", id)
	}
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	staged, err := removeDraft(root, d.Path)
	if err != nil {
		return "", err
	}
	var paths []string
	if staged {
		paths = append(paths, draftRel(id))
	}
	tail := ""
	if len(paths) > 0 {
		tail, err = c.apply(root, paths)
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s удалён как протухший: %s%s", id, reason, tail), nil
}

// removeDraft убирает файл черновика и отвечает, попало ли удаление в индекс.
// Незакоммиченный черновик гиту неизвестен, git rm по нему падает, и такой путь
// в pathspec коммита ронял бы всю команду: он удаляется обычным rm и в коммит
// не едет.
func removeDraft(root, path string) (bool, error) {
	cmd := exec.Command("git", "-C", root, "rm", "-q", "-f", "--", path)
	if _, err := cmd.CombinedOutput(); err == nil {
		return true, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return false, nil
}

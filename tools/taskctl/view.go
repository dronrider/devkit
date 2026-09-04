package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// Сколько строк Backlog показывает list без аргумента. Верх отсортирован по R,
// хвост редко нужен, а доска на десятки задач съедает контекст сессии.
const listBacklogTop = 10

var sectTitles = map[string]string{
	SectInProgress: "In progress",
	SectCheck:      "Check",
	SectBacklog:    "Backlog",
	SectBlocked:    "Blocked",
}

// ensureTaskFile создаёт файл задачи docs/tasks/<ID>.md со строкой-заголовком,
// если его ещё нет, и отвечает, был ли он создан. Общая часть между cmdAdd,
// cmdFile и cmdReviewAdd (LLD DK-133, решение 4): файл обязан стоять у строки
// с минуты заведения, а ссылке в ячейке эти команды находят своё место
// по-разному (см. cmdReviewAdd). Болванка несёт заголовки формы у task/bug
// (TASKFORM.md, taskFormSkeleton): наполняет их тот, кто заводит строку, и они
// уживаются с «Приёмкой», которую add дописывает неагентскому виду своим
// заголовком на его место по форме.
func ensureTaskFile(root, id string, row *Row) (bool, error) {
	abs := taskFileAbs(root, id)
	if _, err := os.Stat(abs); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	base, deps, _, _, _ := splitTitle(row.Title)
	title := joinTitle(base, deps, "", "", "")
	body := fmt.Sprintf("# %s: %s\n", id, title)
	if needsDoD(row.Type, row.Title) {
		body += taskFormSkeleton()
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// dodHeading это пустой заголовок болванки файла задачи: пометка «конец работы
// назван», которую наполняет тот, кто заводит строку (LLD DK-133, решение 4).
const dodHeading = "## DoD"

// goalRow узнаёт строку цели по заголовку от слова «Цель:» (goal-start, п. 3):
// признак цели в доске один, и DoD такой строке не положен, он живёт в разделе
// «Цель» файла цели.
func goalRow(title string) bool {
	base, _, _, _, _ := splitTitle(title)
	return strings.HasPrefix(base, "Цель:")
}

// needsDoD говорит, просит ли строка заголовок DoD: типам task и bug ворота
// заведения спрашивают, чем кончается работа, а LLD описывает конец работы
// в дизайн-документе, на который ссылается файл.
func needsDoD(typ, title string) bool {
	if goalRow(title) {
		return false
	}
	for _, part := range strings.Split(typ, "/") {
		if part == "task" || part == "bug" {
			return true
		}
	}
	return false
}

// needTaskFile это машинная опора перехода для строк, заведённых до рубежа
// (LLD DK-133, решение 4): изменяющая команда отказывает, пока у строки нет
// файла задачи, и отказ называет команду, которая его заведёт. У строк,
// заведённых после рубежа, отказ мёртв: файл кладёт сам add.
func needTaskFile(root, id string) error {
	if _, err := os.Stat(taskFileAbs(root, id)); err == nil {
		return nil
	}
	return fmt.Errorf("у %s нет файла задачи, заведите его: taskctl file %s", id, id)
}

// appendUnderHeading вписывает строку в файл задачи сразу под заголовок
// «# <ID>: ...»: ссылку на файл цели add кладёт туда через --link, чтобы связь
// читалась с первой строки файла, а не из глубины текста.
func appendUnderHeading(abs, line string) error {
	body, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	text := string(body)
	nl := strings.IndexByte(text, '\n')
	if nl < 0 {
		return fmt.Errorf("%s без строки заголовка", abs)
	}
	text = text[:nl+1] + "\n" + line + "\n" + text[nl+1:]
	return os.WriteFile(abs, []byte(text), 0o644)
}

// appendSection дописывает раздел в конец файла задачи через пустую строку:
// к ненулевому телу раздел иначе приклеился бы вплотную к последней строке.
func appendSection(body []byte, section string) []byte {
	return []byte(strings.TrimRight(string(body), "\n") + "\n\n" + section)
}

// cmdFile создаёт файл задачи docs/tasks/<ID>.md со строкой-заголовком и
// ставит ссылку на него в строку доски. Обе части идемпотентны: существующий
// файл не трогается, совпавшая ссылка не переписывается, а если на входе уже
// есть и то и другое, команда об этом сообщает и выходит с нулём, не пытаясь
// закоммитить пустое изменение (при -m/--push второй подряд вызов не создаёт
// нового коммита).
func cmdFile(root, id string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "file"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	rel := fmt.Sprintf("tasks/%s.md", id)
	var done []string
	created, err := ensureTaskFile(root, id, row)
	if err != nil {
		return "", err
	}
	if created {
		done = append(done, fmt.Sprintf("docs/%s создан", rel))
	} else if needsDoD(row.Type, row.Title) {
		// Файл, заведённый до формы или из черновика без болванки, получает
		// недостающий «Ход работы» на месте по форме: иначе пакет этапов от
		// первого перехода вставал бы за разделами, которые по форме идут
		// позже. Другие контрактные разделы заводят их утилиты сами.
		if _, found, ok := readSectionFromPath(taskFileAbs(root, id), stageSection); ok && !found {
			data, err := os.ReadFile(taskFileAbs(root, id))
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(taskFileAbs(root, id), []byte(taskform.InsertSection(string(data), stageSection, "")), 0o644); err != nil {
				return "", err
			}
			done = append(done, "раздел «Ход работы» дописан по форме")
		}
	}
	if want := fmt.Sprintf("[%s](%s)", rel, rel); row.Link != want {
		row.Link = want
		b.Lines[row.LineIdx] = formatRow(row)
		if err := b.Save(); err != nil {
			return "", err
		}
		done = append(done, "ссылка в строке обновлена")
	}
	if len(done) == 0 {
		return fmt.Sprintf("у %s уже есть и файл, и ссылка на него", id), nil
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "TASKS.md"), filepath.Join("docs", rel)})
	if err != nil {
		return "", err
	}
	return strings.Join(done, ", ") + tail, nil
}

// cmdList печатает доску по секциям без прозы; без аргумента Backlog обрезан
// до listBacklogTop строк, с аргументом секция выводится целиком.
func cmdList(root, sect string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	clean := boardClean(root)
	var times map[int]int64
	if clean {
		times = boardTimes(root)
	}
	var out []string
	if note := staleBoardNote(root); note != "" {
		out = append(out, note)
	}
	section := func(key string, limit int) {
		sec := b.Sects[key]
		head := fmt.Sprintf("%s (%d)", sectTitles[key], len(sec.Rows))
		rows := sec.Rows
		if limit > 0 && len(rows) > limit {
			head = fmt.Sprintf("%s (%d, первые %d; целиком: taskctl list %s)",
				sectTitles[key], len(rows), limit, key)
			rows = rows[:limit]
		}
		out = append(out, head)
		if len(rows) == 0 {
			out = append(out, "Нет.")
		}
		for _, r := range rows {
			out = append(out, b.Lines[r.LineIdx])
			out = append(out, rowNotes(root, key, r, times, clean)...)
		}
	}
	if sect != "" {
		key := normalizeStatus(sect)
		if _, ok := b.Sects[key]; !ok {
			return "", fmt.Errorf("неизвестная секция %q, жду backlog / in-progress / check / blocked", sect)
		}
		section(key, 0)
	} else {
		for _, key := range []string{SectInProgress, SectCheck, SectBacklog, SectBlocked} {
			limit := 0
			if key == SectBacklog {
				limit = listBacklogTop
			}
			section(key, limit)
		}
		drafts, err := loadDrafts(root)
		if err != nil {
			return "", err
		}
		if line := draftsLine(drafts); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), nil
}

// rowNotes собирает пометки строки, которые list и show печатают отдельной
// строкой под самой строкой таблицы: они выводятся на лету и в саму доску
// не пишутся (RULES.board.md запрещает заводить под них колонку). Метки
// Check печатаются только в её секции, возраст, когда его удалось посчитать,
// в любой; нет ни того, ни другого, значит nil, и вывод не меняется вовсе.
func rowNotes(root, sect string, r *Row, times map[int]int64, clean bool) []string {
	notes := rowNoteParts(root, sect, r, times, clean)
	if len(notes) == 0 {
		return nil
	}
	return []string{"  " + strings.Join(notes, ", ")}
}

// rowNoteParts отдаёт те же пометки списком без вёрстки: печать склеивает их
// в строку с отступом, --json кладёт как есть.
func rowNoteParts(root, sect string, r *Row, times map[int]int64, clean bool) []string {
	var notes []string
	kind := acceptOf(r.Title)
	if sect == SectCheck {
		// В Check вид печатается всегда: это момент приёмки, и кто её
		// принимает (агент, пользователь или оба) здесь главный вопрос.
		notes = append(notes, checkMarkLabel(root, r.ID, r.Title))
	} else if kind != acceptAgent {
		// В остальных секциях вид печатается только у user и mixed: агентский
		// это умолчание, и пометка на четырёх строках из пяти глаза не стоит
		// (LLD DK-292, решение 2).
		notes = append(notes, "вид "+kind)
	}
	if a := ageLabel(times, r.LineIdx, clean); a != "" {
		notes = append(notes, a)
	}
	// Пометка строки ревью чужого MR: её ведёт опрос тредов, и без неё
	// «автор ответил» и «MR закрыт» видны только тому, кто откроет журнал
	// ревью (LLD DK-756, решения 5 и 7).
	if m := reviewMark(root, r.ID); m != "" {
		notes = append(notes, m)
	}
	return notes
}

// cmdShow печатает строку задачи, её секцию и путь файла задачи; закрытые
// задачи ищутся в архиве.
func cmdShow(root, id string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	note := staleBoardNote(root)
	if row := b.find(id); row != nil {
		out := []string{fmt.Sprintf("%s в %s", id, row.Sect), b.Lines[row.LineIdx]}
		if note != "" {
			out = append([]string{note}, out...)
		}
		out = append(out, rowNotes(root, row.Sect, row, showTimes(root), true)...)
		sides := depSides(b)
		s := sides[id]
		if s == nil {
			s = &struct{ after, blocks []string }{}
		}
		out = append(out, fmt.Sprintf("после: %s", joinOrDash(s.after)), fmt.Sprintf("держит: %s", joinOrDash(s.blocks)))
		rel := fmt.Sprintf("tasks/%s.md", id)
		if _, err := os.Stat(filepath.Join(root, "docs", rel)); err == nil {
			out = append(out, "файл задачи: docs/"+rel)
		} else {
			out = append(out, fmt.Sprintf("файла задачи нет (создаст taskctl file %s)", id))
		}
		return strings.Join(out, "\n"), nil
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	for _, r := range arch.Rows {
		if r.ID == id {
			text := fmt.Sprintf("%s в архиве (закрыта %s)\n%s", id, r.Cells[4], arch.Lines[r.LineIdx])
			if note != "" {
				text = note + "\n" + text
			}
			return text, nil
		}
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	if d := findDraft(drafts, id); d != nil {
		text, err := os.ReadFile(d.Path)
		if err != nil {
			return "", err
		}
		head := fmt.Sprintf("%s черновик (записан %s), docs/tasks/drafts/%s.md, оформить: taskctl add --id %s ...",
			id, ageWords(d.Age), id, id)
		if note != "" {
			head = note + "\n" + head
		}
		return head + "\n" + strings.TrimRight(string(text), "\n"), nil
	}
	return "", fmt.Errorf("%s нет ни на доске, ни в архиве, ни в черновиках", id)
}

// showTimes считает даты строк для одной задачи тем же путём, что и list:
// blame один на файл, поэтому отдельной дешёвой ветки под show не нужно.
// Грязная доска гасит возраст там же, где и в list, через пустую карту.
func showTimes(root string) map[int]int64 {
	if !boardClean(root) {
		return nil
	}
	return boardTimes(root)
}

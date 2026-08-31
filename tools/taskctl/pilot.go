package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// taskctl pilot сводит счётчики полосы base (DK-661): дешёвая модель взяла
// задачи ценой M, и просадку качества надо ловить числами, а не ощущением.
// Читается то, что уже лежит на диске: разделы «Выкат» и «Ход работы» файлов
// задач, живые записи ~/.devkit/runs и история коммитов доски. Команда отчёт,
// а не ворота: решение по вердикту отката принимает человек.

// pilotRollbackFactor это порог отката полосы: доля возвратов с ревью в окне
// пилота, поделённая на ту же долю в истории до пилота. Выше порога полоса
// возвращается на прежнее место, задачи ценой M уходят обратно на pro. Порог
// из файла задачи переведён в код, чтобы вердикт считала команда, а не глаз
// читателя вывода.
const pilotRollbackFactor = 1.5

// pilotDateLayout это форма дат среза и слияния: день без времени.
const pilotDateLayout = "2006-01-02"

// pilotMergeRe узнаёт строку слияния в разделе «Выкат»: «- 2026-08-31 слито:
// abe3213f, ...». Форму пишет shipctl record, список коммитов счётчику не
// нужен: слияние уже событие.
var pilotMergeRe = regexp.MustCompile(`^-\s*\d{4}-\d{2}-\d{2} слито:`)

// pilotDateRe вынимает дату из строки «Ход работы». Запись этапа кончается
// датой, а заметка вердикта впереди несёт свои числа, поэтому берётся
// последняя дата строки.
var pilotDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// pilotTask это задача счёта: даты слияний, даты заходов разработки и события
// провала и отката из истории доски.
type pilotTask struct {
	id      string
	merges  []time.Time // даты строк «слито», первая делит окно и историю
	devs    []time.Time // даты заходов: строки «Разработка» и живые этапы runs
	fails   int         // коммиты «провал проверки» с этим ID
	reverts int         // коммиты-откаты выката с этим ID
}

// firstMerge отдаёт дату первого слияния: по ней задача попадает в окно пилота
// или остаётся в истории.
func (t pilotTask) firstMerge() (time.Time, bool) {
	if len(t.merges) == 0 {
		return time.Time{}, false
	}
	return t.merges[0], true
}

// returned говорит, возвращалась ли задача: вторая строка «слито» это круг
// доработки после возврата из Check, формулировка файла задачи DK-661.
func (t pilotTask) returned() bool { return len(t.merges) >= 2 }

// attempts считает заходы до слияния: разработка, начатая не позже первого
// слияния. Время внутри дня не сравнивается, у строки слияния его нет, и
// заход в день слияния считается прошедшим до него.
func (t pilotTask) attempts() int {
	first, ok := t.firstMerge()
	if !ok {
		return 0
	}
	n := 0
	for _, d := range t.devs {
		if !d.After(first) {
			n++
		}
	}
	return n
}

// pilotShare это доля задач окна, у которых что-то есть.
type pilotShare struct {
	count int
	total int
}

// ratio отдаёт долю числом; пустое окно это ноль, а не паника деления.
func (s pilotShare) ratio() float64 {
	if s.total == 0 {
		return 0
	}
	return float64(s.count) / float64(s.total)
}

// shareOf считает долю по признаку.
func shareOf(tasks []pilotTask, of func(pilotTask) bool) pilotShare {
	s := pilotShare{total: len(tasks)}
	for _, t := range tasks {
		if of(t) {
			s.count++
		}
	}
	return s
}

// shareText печатает долю исходом и процентом, пустое окно честно называется
// пустым.
func shareText(s pilotShare) string {
	if s.total == 0 {
		return "нет задач"
	}
	return fmt.Sprintf("%d/%d (%.1f%%)", s.count, s.total, 100*s.ratio())
}

// growthText печатает рост доли окна против доли истории. Нулевая история
// это деление на ноль: возвраты в окне растут без предела, а их отсутствие
// даёт рост 0.0, NaN на живой доске неотличим от поломки.
func growthText(w, h pilotShare) string {
	if h.ratio() == 0 && w.ratio() > 0 {
		return "не ограничен"
	}
	if h.ratio() == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", w.ratio()/h.ratio())
}

// attemptsText сводит заходы до слияния средним на задачу. Считаются задачи
// с записанными заходами: у закрытых до DK-338 строк «Хода работы» нет, и
// втягивать их нулём значило бы занижать среднее.
func attemptsText(tasks []pilotTask) string {
	var attempts []int
	for _, t := range tasks {
		if len(t.devs) == 0 {
			continue
		}
		attempts = append(attempts, t.attempts())
	}
	if len(attempts) == 0 {
		return "нет записанных заходов"
	}
	sum := 0
	for _, n := range attempts {
		sum += n
	}
	return fmt.Sprintf("%.1f на задачу (%d %s с записанными заходами)", float64(sum)/float64(len(attempts)), len(attempts), pluralTasks(len(attempts)))
}

// revertCount суммирует откаты выката по задачам окна.
func revertCount(tasks []pilotTask) int {
	n := 0
	for _, t := range tasks {
		n += t.reverts
	}
	return n
}

// cmdPilot печатает четыре счётчика по окну задач и вердикт отката. Окно
// делится датой начала пилота: в окно идут задачи с первым слиянием не раньше
// неё, в историю столько же задач с первым слиянием до неё, ближайших к
// границе. История зеркальна окну по числу задач: сравнивать доли на выборках
// разного размера значило бы мерить разом и полосу, и глубину архива.
func cmdPilot(root, sinceArg, runsArg string) (string, error) {
	since, err := time.Parse(pilotDateLayout, sinceArg)
	if err != nil {
		return "", fmt.Errorf("--since ждёт дату вида 2026-09-01: %v", err)
	}
	tasks, err := collectPilotTasks(root)
	if err != nil {
		return "", err
	}
	runsDir := runsArg
	if runsDir == "" {
		runsDir = stage.Dir(stage.Home())
	}
	attachLiveStages(tasks, root, runsDir)
	if err := attachBoardEvents(root, tasks); err != nil {
		return "", err
	}
	sort.Slice(tasks, func(i, j int) bool {
		a, _ := tasks[i].firstMerge()
		b, _ := tasks[j].firstMerge()
		return a.Before(b)
	})
	var window, history []pilotTask
	for _, t := range tasks {
		first, _ := t.firstMerge()
		if !first.Before(since) {
			window = append(window, t)
		} else {
			history = append(history, t)
		}
	}
	// История берётся с границы навстречу окну: у свежих задач те же практики
	// ревью и выката, а глубина архива несёт старые правила.
	tail := len(window)
	if tail > len(history) {
		tail = len(history)
	}
	history = history[len(history)-tail:]

	out := []string{fmt.Sprintf("полоса base, счётчики пилота с %s", since.Format(pilotDateLayout))}
	if len(window) == 0 {
		out = append(out, "в окне нет слитых задач, счётчики пусты, сравнивать не с чем")
		return strings.Join(out, "\n"), nil
	}
	wRet, hRet := shareOf(window, pilotTask.returned), shareOf(history, pilotTask.returned)
	out = append(out, fmt.Sprintf("окно: %d %s, история до старта: %d %s",
		len(window), pluralTasks(len(window)), len(history), pluralTasks(len(history))))
	out = append(out, fmt.Sprintf("возвраты с ревью: %s против %s у истории, рост %s",
		shareText(wRet), shareText(hRet), growthText(wRet, hRet)))
	out = append(out, fmt.Sprintf("заходы до слияния: %s против %s у истории", attemptsText(window), attemptsText(history)))
	out = append(out, fmt.Sprintf("краснота Check: %s против %s у истории",
		shareText(shareOf(window, func(t pilotTask) bool { return t.fails > 0 })),
		shareText(shareOf(history, func(t pilotTask) bool { return t.fails > 0 }))))
	out = append(out, fmt.Sprintf("откаты выката: %d против %d у истории", revertCount(window), revertCount(history)))
	out = append(out, pilotVerdict(wRet, hRet))
	return strings.Join(out, "\n"), nil
}

// pilotVerdict сводит порог отката: доля возвратов окна, поделённая на долю
// истории, выше порога значит полоса просела и её убирают. Пустая история это
// нулевой знаменатель: возвраты в окне при ней растут без предела, и вердикт
// честнее отдать откату, чем молчанию.
func pilotVerdict(w, h pilotShare) string {
	switch {
	case h.total == 0:
		return "вердикт: истории до старта нет, сравнивать не с чем"
	case w.ratio() == 0:
		return "вердикт: возвратов в окне нет, полоса держится"
	case h.ratio() == 0:
		return fmt.Sprintf("вердикт: история без возвратов, в окне их %d, полоса возвращается на прежнее место", w.count)
	}
	growth := w.ratio() / h.ratio()
	if growth > pilotRollbackFactor {
		return fmt.Sprintf("вердикт: возвраты выросли в %.1f раза против истории, порог %.1f, полоса возвращается на прежнее место", growth, pilotRollbackFactor)
	}
	return fmt.Sprintf("вердикт: рост %.1f в пределах порога %.1f, полоса держится", growth, pilotRollbackFactor)
}

// collectPilotTasks обходит файлы задач и собирает слияния и заходы. Задача
// без строки «слито» в счёт не входит: окно пилота меряет слитые задачи, у
// неслитых нет ни возвратов, ни красноты Check.
func collectPilotTasks(root string) ([]pilotTask, error) {
	base := filepath.Join(root, "docs", "tasks")
	var tasks []pilotTask
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "drafts" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		t := pilotTask{id: strings.TrimSuffix(d.Name(), ".md")}
		if text, found, ok := readSectionFromPath(path, taskform.Merged); ok && found {
			for _, ln := range strings.Split(text, "\n") {
				ln = strings.TrimSpace(ln)
				if !pilotMergeRe.MatchString(ln) {
					continue
				}
				if when, err := time.Parse(pilotDateLayout, pilotDateRe.FindString(ln)); err == nil {
					t.merges = append(t.merges, when)
				}
			}
		}
		if len(t.merges) == 0 {
			return nil
		}
		if text, found, ok := readSectionFromPath(path, taskform.Stages); ok && found {
			for _, ln := range strings.Split(text, "\n") {
				ln = strings.TrimSpace(ln)
				if !strings.HasPrefix(ln, "- Разработка:") {
					continue
				}
				dates := pilotDateRe.FindAllString(ln, -1)
				if len(dates) == 0 {
					continue
				}
				if when, err := time.Parse(pilotDateLayout, dates[len(dates)-1]); err == nil {
					t.devs = append(t.devs, when)
				}
			}
		}
		tasks = append(tasks, t)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("каталога docs/tasks нет, читать нечего: %v", err)
		}
		return nil, err
	}
	return tasks, nil
}

// attachLiveStages дописывает задачам живые этапы разработки из записи runs.
// Пакет этапов уезжает в «Ход работы» только на смене статуса, и у задачи,
// слияние которой уже записано, а перевода ещё не было, заходы живут только в
// записи: без неё счёт их терял бы. Дата этапа обрезается до дня: строки
// «Хода работы» несут только день, и сравнение с днём слияния идёт по тем же
// весам.
func attachLiveStages(tasks []pilotTask, root, runsDir string) {
	suffix := "-" + stage.Slug(stage.MainRoot(root)) + ".run"
	for i := range tasks {
		rec, err := stage.Load(filepath.Join(runsDir, tasks[i].id+suffix))
		if err != nil {
			continue
		}
		for _, s := range rec.Stages {
			if s.Kind != stage.Dev {
				continue
			}
			tasks[i].devs = append(tasks[i].devs, time.Date(s.Start.Year(), s.Start.Month(), s.Start.Day(), 0, 0, 0, 0, time.UTC))
		}
	}
}

// attachBoardEvents разносит по задачам события истории доски. Провал
// проверки ставит taskctl fail коммитом «docs(tasks): <ID> провал проверки,
// ...», откат выката оставляет shipctl revert коммитом «revert: <ID> откат
// ...» (конвенция isRevertSubject в shipctl). Оба события в файле задачи не
// живут: признак провала гасится снятием, откат файл не трогает, поэтому
// единственный след остаётся в subject коммитов.
func attachBoardEvents(root string, tasks []pilotTask) error {
	out, err := exec.Command("git", "-C", root, "log", "--pretty=format:%s").Output()
	if err != nil {
		return fmt.Errorf("история коммитов не прочитана: %v", err)
	}
	at := map[string]int{}
	for i, t := range tasks {
		at[t.id] = i
	}
	for _, subj := range strings.Split(string(out), "\n") {
		fields := strings.Fields(subj)
		matched := map[int]bool{}
		for _, f := range fields {
			f = strings.Trim(f, ",;:()")
			if i, ok := at[f]; ok {
				matched[i] = true
			}
		}
		if len(matched) == 0 {
			continue
		}
		fail := strings.HasPrefix(subj, "docs(tasks): ") && strings.Contains(subj, "провал проверки")
		revert := strings.HasPrefix(subj, "revert: ") || pilotWordIn(fields, "откат")
		if !fail && !revert {
			continue
		}
		for i := range matched {
			switch {
			case fail:
				tasks[i].fails++
			default:
				tasks[i].reverts++
			}
		}
	}
	return nil
}

// pilotWordIn ищет слово среди полей строки целиком, без обрезки по корню:
// «откатом» и «отката» словом «откат» не являются.
func pilotWordIn(fields []string, word string) bool {
	for _, f := range fields {
		if f == word {
			return true
		}
	}
	return false
}

// pluralTasks склоняет «задача» по числу.
func pluralTasks(n int) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return "задач"
	}
	switch n % 10 {
	case 1:
		return "задача"
	case 2, 3, 4:
		return "задачи"
	default:
		return "задач"
	}
}

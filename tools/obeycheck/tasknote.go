package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/taskform"
)

// minTaskRepeats это нижняя граница повторов, при которой прогон считается
// замером, а не разведкой. Одна и две сессии на раскладку не отличают правку от
// случайности ни при каком тесте, и след такого прогона обманывал бы ворота
// слияния.
const minTaskRepeats = 3

// note это всё, что стенд знает о прогоне к моменту записи в файл задачи.
type note struct {
	Task      string
	Devkit    string // дерево, из которого читаются тексты предметов
	Tier      string // ярус или модель, на которой шёл прогон
	Base      string
	Repeats   int
	Command   string
	Table     string
	Scenarios []Scenario
	Failed    bool
	Warnings  []string
	Now       time.Time
}

// taskFile ищет файл задачи в корне репозитория текущей директории, тем же
// путём, каким стенд находит журнал прогонов. Файла нет, значит стенд зовут не
// из дерева задачи, и записывать след некуда.
func taskFile(startDir, id string) (string, error) {
	root, err := gitOut(startDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("--task %s: текущая директория не в репозитории, запускайся из дерева задачи", id)
	}
	p := filepath.Join(root, "docs", "tasks", id+".md")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("--task %s: файла %s нет, запускайся из дерева задачи", id, p)
	}
	return p, nil
}

// runSubjects собирает предметы всех сценариев прогона: без повторов и в
// порядке путей и разделов, чтобы отпечаток не зависел от порядка сценариев в
// командной строке.
func runSubjects(scen []Scenario) []obey.Subject {
	var groups [][]obey.Subject
	for _, s := range scen {
		groups = append(groups, s.Subjects)
	}
	return obey.Union(groups...)
}

// liveScenarios отбирает сценарии, которые в прогоне участвовали: пропущенный
// по концу прогона сценарий в след не едет, доказывать им нечего.
func liveScenarios(rows []row) []Scenario {
	var out []Scenario
	for _, r := range rows {
		if !r.Skipped {
			out = append(out, r.Scenario)
		}
	}
	return out
}

// scenarioIDs отдаёт ID сценариев прогона в порядке таблицы: вместе с базой они
// образуют ключ записи в файле задачи.
func scenarioIDs(scen []Scenario) []string {
	var out []string
	for _, s := range scen {
		out = append(out, s.ID)
	}
	return out
}

// missingInLayout ищет тексты предметов в раскладке-кандидате. Правила и
// страницы корня едут в прогон не из дерева, а из раскладки, и раскладка,
// собранная руками до последней правки, дала бы свежий след на прогоне старого
// текста. Отказом это не делается: генератор вправе переверстать файл, и
// сравнение идёт по тексту со схлопнутыми пробелами. Скиллы и определения
// субагентов не проверяются, их стенд берёт из дерева сам.
func missingInLayout(layout, devkit string, subs []obey.Subject) []string {
	var out []string
	for _, s := range subs {
		if strings.HasPrefix(s.Path, "kit/") {
			continue
		}
		text, err := obey.Text(devkit, s)
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		if !layoutHas(layout, collapse(text)) {
			out = append(out, fmt.Sprintf("текст предмета %s в раскладке-кандидате не найден, раскладка собрана до правки?", s))
		}
	}
	return out
}

// layoutHas отвечает, лежит ли текст в каком-нибудь файле раскладки, включая
// home/.
func layoutHas(layout, needle string) bool {
	found := false
	filepath.WalkDir(layout, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.Size() > 4<<20 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(collapse(string(data)), needle) {
			found = true
		}
		return nil
	})
	return found
}

// collapse схлопывает пробелы и переносы в один пробел: перевёрстка абзаца по
// другой ширине не должна выглядеть как другой текст. Та же мера, что у
// команды phrase в проверках сценариев.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// record складывает запись прогона для раздела «Проверка»: строку отметки, по
// которой ворота узнают прогон, и под ней ограждённый блок с командой и
// таблицей. Таблица идёт ограждённой, иначе разметка файла задачи ломается о
// первую же её строку.
func (n note) record(mark taskform.StandMark) []string {
	tail := "зачтён"
	if n.Failed {
		tail = "ворота закрыты"
	}
	out := []string{"", taskform.StandLine(mark, n.Now, tail)}
	for _, w := range n.Warnings {
		out = append(out, "", taskform.WarnLine+w)
	}
	out = append(out, "", "```console", "$ "+n.Command)
	out = append(out, strings.Split(strings.TrimRight(n.Table, "\n"), "\n")...)
	out = append(out, "```")
	return out
}

// write кладёт отметку стенда в раздел «Проверка» файла задачи. Прошлая запись
// с тем же ключом (те же сценарии и та же база) уносится: прогон по другим
// файлам и прогоны с разными базами друг друга не затирают, а повторный прогон
// одного и того же не растит раздел дублями.
func (n note) write(path string) error {
	if n.Repeats < minTaskRepeats {
		return fmt.Errorf("--task %s при -k %d это разведка, а не замер: следу в файле задачи нужно "+
			"хотя бы %d повтора на раскладку", n.Task, n.Repeats, minTaskRepeats)
	}
	fp, err := obey.Print(n.Devkit, runSubjects(n.Scenarios))
	if err != nil {
		return err
	}
	tree, err := gitOut(n.Devkit, "rev-parse", "--short", "HEAD")
	if err != nil {
		tree = "нет"
	}
	ids := scenarioIDs(n.Scenarios)
	mark := taskform.StandMark{
		Failed:    n.Failed,
		Tree:      tree,
		Print:     fp,
		Base:      n.Base,
		Tier:      n.Tier,
		Repeats:   n.Repeats,
		Scenarios: ids,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc := taskform.DropStandRecord(string(data), ids, n.Base)
	doc = taskform.InsertIntoSection(doc, taskform.Verification, n.record(mark)...)
	return os.WriteFile(path, []byte(strings.TrimRight(doc, "\n")+"\n"), 0o644)
}

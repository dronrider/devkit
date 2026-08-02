package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const runLogPath = ".devkit/log"

// regcheckWarning подсказывает при слиянии bug-задачи, если в журнале
// запусков нет зелёного regcheck за время жизни ветки: регрессионный тест
// обязан краснеть на старом коде (RULES.md, «Тесты обязательны»), а по
// статистике этот шаг пропускается чаще других. Именно подсказка, не отказ:
// где правка и тест живут в одном файле, regcheck не применим и проверяется
// руками. Без журнала (нет .devkit) подсказывать не по чему. Журнал берётся
// из logRoot: при работе через worktree прогоны regcheck оседают в дереве
// задачи, а не в основном чекауте.
func regcheckWarning(root, logRoot, main, branch, taskType string) []string {
	if !strings.Contains(taskType, "bug") {
		return nil
	}
	if fi, err := os.Stat(filepath.Join(logRoot, ".devkit")); err != nil || !fi.IsDir() {
		return nil
	}
	// Начало жизни ветки это коммит, где она отошла от main. regcheck прошлой
	// задачи остаётся за границей: после её слияния main уехал вперёд и
	// merge-base новой ветки свежее того прогона.
	base, err := git(root, "merge-base", main, branch)
	if err != nil {
		return nil
	}
	ctStr, err := git(root, "log", "-1", "--format=%ct", base)
	if err != nil {
		return nil
	}
	ct, err := strconv.ParseInt(ctStr, 10, 64)
	if err != nil {
		return nil
	}
	if regcheckLogged(filepath.Join(logRoot, runLogPath), time.Unix(ct, 0)) {
		return nil
	}
	return []string{"предупреждение: задача типа bug, а в " + runLogPath +
		" нет зелёного regcheck за время жизни ветки; регрессионный тест обязан краснеть на старом коде (RULES.md, «Тесты обязательны»)"}
}

// regcheckLogged ищет в журнале успешный запуск regcheck не старше since.
func regcheckLogged(path string, since time.Time) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, ln := range strings.Split(string(data), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) < 4 || f[1] != "regcheck" || f[3] != "0" {
			continue
		}
		ts, err := time.ParseInLocation("2006-01-02T15:04:05", f[0], time.Local)
		if err != nil {
			continue
		}
		if !ts.Before(since) {
			return true
		}
	}
	return false
}

// trainWarnings проговаривает мягкие критерии поезда из RULES.board.md
// («Ветки, ревью и деплой» п. 9): цена S или M, не больше 3-5 задач, задачи
// не трогают одни файлы. Нарушение не валит merge, критерии на суждении
// (связность «по смыслу» git не видит), но в отчёте оно обязано прозвучать.
func trainWarnings(root, reviewRoot, main, branch string, b *board, id string, train []string) []string {
	var warns []string
	// Бескодовая ветка считается до ребейза по тому же предикату, что и после
	// слияния: множество коммитов у main..branch и у слитого диапазона одно,
	// ребейз его не меняет. Ошибку git тут глотаем, подсказке хватит общего
	// случая, а слияние из-за неё падать не должно.
	docsBranch, err := rangeDocsOnly(root, main, branch)
	if err != nil {
		docsBranch = false
	}
	warns = append(warns, scenarioWarning(reviewRoot, id, docsBranch)...)
	if r := b.rowOf(id); r != nil {
		switch r.Cost {
		case "", "S", "M":
		case "-":
			warns = append(warns, "предупреждение: цена "+id+" не оценена, в поезд берут S или M со снятой неопределённостью (RULES.board.md п. 9)")
		default:
			warns = append(warns, "предупреждение: цена "+id+" это "+r.Cost+", в поезд берут S или M, крупное едет одиночным выкатом (RULES.board.md п. 9)")
		}
	}
	if len(train) >= 5 {
		warns = append(warns, fmt.Sprintf("предупреждение: в поезде уже %d задач(и), больше 3-5 не копят, регресс без сценария ищется перебором состава: пора shipctl ship", len(train)))
	}
	if overlaps, err := trainOverlap(root, main, branch, train); err == nil && len(overlaps) > 0 {
		warns = append(warns, "предупреждение: ветка трогает файлы задач поезда ("+strings.Join(overlaps, "; ")+"), в поезд берут независимые задачи")
	}
	return warns
}

// scenarioWarning подсказывает при поездном слиянии, что у задачи нет сценария
// проверки. Одиночный выкат везёт одну задачу, и её проверяют по горячим
// следам, а поезд переводит в Check всю пачку разом и уже после деплоя:
// задача без сценария всплывёт там, где проверять её нечем, а прод уже
// поменялся. Признак это заголовок «Сценарий проверки» в файле задачи, файл
// читается в дереве ветки: исполнитель и ревьювер пишут его туда.
// Кто переведёт задачу в Check, зависит от ветки, и подсказка обязана называть
// того же: бескодовую задачу переводит этот самый merge, ещё до всякого ship.
func scenarioWarning(reviewRoot, id string, docsBranch bool) []string {
	if data, err := os.ReadFile(taskFilePath(reviewRoot, id)); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(ln, "#") && strings.Contains(ln, "Сценарий проверки") {
				return nil
			}
		}
	}
	who := "ship переведёт задачу в Check разом со всем поездом: проверять выкат будет нечем"
	if docsBranch {
		who = "merge переведёт бескодовую задачу в Check прямо сейчас, без выката: подтверждать её будет нечем"
	}
	return []string{"предупреждение: в docs/tasks/" + id + ".md нет раздела «Сценарий проверки», а " + who + " (RULES.board.md, «Трекинг задач» п. 6)"}
}

// trainOverlap находит пересечение файлов ветки с файлами коммитов задач
// поезда. Коммиты задачи берутся так же, как в составе поезда: из записи
// «Выкат» и по ID в subject, иначе подсказка молчала бы ровно на той ветке,
// где ID в сообщения не попал. Правки под docs/ (доска, файлы задач, LLD)
// пересечением не считаются: файл задачи и доску трогает почти каждая ветка.
func trainOverlap(root, main, branch string, train []string) ([]string, error) {
	if len(train) == 0 {
		return nil, nil
	}
	branchFiles, err := git(root, "diff", "--name-only", main+"..."+branch)
	if err != nil {
		return nil, err
	}
	mine := map[string]bool{}
	for _, f := range strings.Split(branchFiles, "\n") {
		if f = strings.TrimSpace(f); f != "" && !strings.HasPrefix(f, "docs/") {
			mine[f] = true
		}
	}
	if len(mine) == 0 {
		return nil, nil
	}
	log, err := git(root, "log", deployTag+".."+main, "--format=%H%x09%s")
	if err != nil || log == "" {
		return nil, err
	}
	var out []string
	for _, id := range train {
		rec, err := mergedShas(root, id)
		if err != nil {
			return nil, err
		}
		var hit []string
		for _, ln := range strings.Split(log, "\n") {
			sha, subj, ok := strings.Cut(ln, "\t")
			if !ok || isRevertSubject(subj) || (!containsWord(subj, id) && !inRecord(rec, sha)) {
				continue
			}
			files, err := git(root, "show", "--name-only", "--pretty=", sha)
			if err != nil {
				return nil, err
			}
			for _, f := range strings.Split(files, "\n") {
				if f = strings.TrimSpace(f); f != "" && mine[f] && !slices.Contains(hit, f) {
					hit = append(hit, f)
				}
			}
		}
		if len(hit) > 0 {
			out = append(out, id+": "+strings.Join(hit, ", "))
		}
	}
	return out, nil
}

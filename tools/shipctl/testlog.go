package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Журнал итогов прогона test по компонентам (DK-1125, держит третью строку
// DoD цели DK-1084). Живёт машинным файлом .devkit/test-runs.log, гитигнорнут
// тем же порядком, что .devkit/log (logRun): откат и повторное слияние задачи
// правят историю main, а журнал вне git и их не замечает, переживая оба.

// testLogPath это путь журнала внутри .devkit.
const testLogPath = ".devkit/test-runs.log"

// componentOutcome это итог одного именованного компонента прогона: имя,
// исход и длительность.
type componentOutcome struct {
	Name string  `json:"name"`
	OK   bool    `json:"ok"`
	Secs float64 `json:"secs"`
}

// testRunRecord это одна запись журнала: итог прогона test при слиянии
// задачи ID, разбор по компонентам и состав диффа задачи (пути ветки против
// main, без docs/, тот же список, каким merge проверял чистоту дерева и
// раскладку компонентов выката).
type testRunRecord struct {
	Time       time.Time          `json:"time"`
	ID         string             `json:"id"`
	OK         bool               `json:"ok"`
	Diff       []string           `json:"diff"`
	Components []componentOutcome `json:"components"`
}

// componentLineRe разбирает строку итога компонента общего вида
// «<имя> <секунды>s ok|FAIL»: тот же формат, каким parallel.py печатает
// каждый компонент своей строкой (tools/devkitctl/parallel.py). Контракт
// общий, а не завязанный на конкретный проект: команда test любого проекта,
// печатающая построчный итог в этом виде, дробится в журнале на компоненты.
var componentLineRe = regexp.MustCompile(`^(\S+)\s+([0-9]+(?:\.[0-9]+)?)s\s+(ok|FAIL)\s*$`)

// parseComponentOutcomes вытягивает из вывода команды test построчные итоги
// компонентов. Пустой результат значит, что команда не дробится на
// компоненты (одиночный `go test ./...` или `make check`), и вызывающий
// заводит один синтетический компонент на весь прогон.
func parseComponentOutcomes(out string) []componentOutcome {
	var res []componentOutcome
	for _, line := range strings.Split(out, "\n") {
		m := componentLineRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		secs, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		res = append(res, componentOutcome{Name: m[1], OK: m[3] == "ok", Secs: secs})
	}
	return res
}

// writeTestLog дописывает в журнал итог прогона test: компоненты, разобранные
// из вывода команды (либо один синтетический "test" без разбора), и состав
// диффа задачи. Журнал ведётся только там, где есть .devkit (как .devkit/log
// у logRun): каталог заводит devkitctl, без него журналу писать некуда.
// Провал записи прогон не роняет, журнал это наблюдение, а не предусловие.
func writeTestLog(root, id string, diff []string, out string, ok bool, dur time.Duration) {
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	comps := parseComponentOutcomes(out)
	if len(comps) == 0 {
		comps = []componentOutcome{{Name: "test", OK: ok, Secs: dur.Seconds()}}
	}
	rec := testRunRecord{Time: time.Now(), ID: id, OK: ok, Diff: diff, Components: comps}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "test-runs.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(line))
}

// readTestLog читает записи журнала не старше since (от текущего момента).
// Нечитаемая строка (журнал только дописывается, но формат мог смениться по
// ходу разработки) молча пропускается: считать он должен по тому, что сумел
// разобрать, а не отказывать целиком на одной порченой строке.
func readTestLog(root string, since time.Duration) ([]testRunRecord, error) {
	data, err := os.ReadFile(filepath.Join(root, testLogPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cutoff := time.Now().Add(-since)
	var recs []testRunRecord
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec testRunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if !rec.Time.Before(cutoff) {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

// bareName убирает префикс до последнего ":" включительно: составное имя
// компонента вида "kind:name" (как go-модули parallel.py, "go:shipctl")
// называет себя через двоеточие, а сравнению с диффом задачи нужен голый
// хвост.
func bareName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// touchesDiff отвечает, лежит ли хоть один путь diff внутри компонента name:
// голое имя компонента встречается отдельным сегментом пути. Раскладка
// deploy.<имя>.paths тут не судья: она не обязана быть заведена (autonomous
// не поднят или раскладки нет вовсе), а имя компонента журнал уже знает из
// собственного вывода команды test.
func touchesDiff(name string, diff []string) bool {
	bare := bareName(name)
	if bare == "" {
		return false
	}
	for _, p := range diff {
		for _, seg := range strings.Split(p, "/") {
			if seg == bare {
				return true
			}
		}
	}
	return false
}

// foreignFails считает слияния, отбитые компонентом вне диффа задачи, за
// срок since: запись в счёт идёт, когда прогон целиком красный и ни один из
// провалившихся компонентов диффа задачи не касается. Своя краснота (диффа
// касается хотя бы один из провалившихся) отбивает слияние законно и в счёт
// не идёт.
func foreignFails(root string, since time.Duration) (int, error) {
	recs, err := readTestLog(root, since)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, rec := range recs {
		if rec.OK {
			continue
		}
		failedAny, own := false, false
		for _, c := range rec.Components {
			if c.OK {
				continue
			}
			failedAny = true
			if touchesDiff(c.Name, rec.Diff) {
				own = true
			}
		}
		if failedAny && !own {
			n++
		}
	}
	return n, nil
}

// parseSince разбирает срок команды foreign-fails: длительность вида 24h,
// 30m (time.ParseDuration) либо число суток или недель с суффиксом d/w
// (7d, 2w). Раздел «Итог» цели DK-1084 ведёт недельное наблюдение этим
// сроком, 7d.
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("нужен срок: длительность вида 24h, 30m либо число суток или недель с суффиксом d/w (7d, 2w)")
	}
	if n := len(s); n > 1 {
		unit := s[n-1]
		if unit == 'd' || unit == 'w' {
			if num, err := strconv.Atoi(s[:n-1]); err == nil && num > 0 {
				days := num
				if unit == 'w' {
					days *= 7
				}
				return time.Duration(days) * 24 * time.Hour, nil
			}
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q не читается как срок: длительность вида 24h, 30m либо число суток или недель с суффиксом d/w (7d, 2w)", s)
	}
	return d, nil
}

// cmdForeignFails печатает число слияний, отбитых компонентом вне диффа
// задачи, за названный срок.
func cmdForeignFails(root, since string) (string, error) {
	d, err := parseSince(since)
	if err != nil {
		return "", err
	}
	n, err := foreignFails(root, d)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("слияний, отбитых компонентом вне диффа задачи за %s: %d", since, n), nil
}

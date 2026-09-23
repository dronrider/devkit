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
// исход, длительность и разбор принадлежности. Root это корень компонента
// (путь от корня проекта), когда команда test печатает его прямо в строке
// итога, тем же приёмом, каким parallel.py уже показывает его в --list.
// Resolved говорит, нашлась ли граница компонента вообще, раскладкой
// deploy.<имя>.paths в .devkit/deploy.local либо этим полем Root (решение
// DK-1125 по замечаниям ревью круга 1 и круга 2). Голый сегмент пути врёт в
// обе стороны. Бакет parallel.py вроде «skills» матчит любой чужой файл
// внутри kit/skills, а компонент-скрипт вроде «check-skills» не матчит
// собственный файл из-за расширения. Own действует только при Resolved:
// компонент без заведённой раскладки и без Root не признаётся своим
// никогда, его краснота идёт в счёт foreign-fails как неопознанная, а не
// как молчаливо прощённая.
type componentOutcome struct {
	Name     string  `json:"name"`
	OK       bool    `json:"ok"`
	Secs     float64 `json:"secs"`
	Root     string  `json:"root,omitempty"`
	Resolved bool    `json:"resolved"`
	Own      bool    `json:"own"`
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
// «<имя> [(<корень>)] <секунды>s ok|FAIL»: тот же формат, каким parallel.py
// печатает каждый компонент своей строкой (tools/devkitctl/parallel.py).
// Корень в скобках необязателен: команда без разбора по компонентам его не
// печатает, а команда, которая его знает, называет им область, которую
// компонент реально задевает (решение DK-1125 по замечанию ревью круга 2,
// раскладка deploy.<имя>.paths не обязана быть заведена, а корень у раннера
// есть всегда). Контракт общий, а не завязанный на конкретный проект:
// команда test любого проекта, печатающая построчный итог в этом виде,
// дробится в журнале на компоненты.
var componentLineRe = regexp.MustCompile(`^(\S+)\s+(?:\(([^()]*)\)\s+)?([0-9]+(?:\.[0-9]+)?)s\s+(ok|FAIL)\s*$`)

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
		secs, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			continue
		}
		res = append(res, componentOutcome{Name: m[1], Root: m[2], OK: m[4] == "ok", Secs: secs})
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
	// Принадлежность компонента диффу задачи разбирается прямо тут, в
	// момент записи, раскладкой deploy.<имя>.paths проекта: журнал это
	// стабильная историческая запись, и следующая правка раскладки не
	// должна переигрывать смысл уже слитых слияний.
	cfg, _ := loadDeployConfig(root)
	for i := range comps {
		comps[i].Resolved, comps[i].Own = resolveOwnership(cfg, comps[i].Name, comps[i].Root, diff)
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

// resolveOwnership ищет границу компонента name и проверяет, лежит ли хоть
// один путь diff внутри неё. Два источника границы, в порядке приоритета:
//
//  1. Раскладка deploy.<имя>.paths проекта (cfg), когда она заведена. Это
//     осознанный выбор проекта и уточнение: он же вправе объявить границу
//     точнее, чем знает о себе сам компонент (деление бакета parallel.py на
//     кусок помельче, скажем).
//  2. Root, когда раскладки для имени нет. Это корень компонента, который
//     пришёл строкой итога самой команды test (componentLineRe, скобки).
//     Раскладка выката не обязана быть заведена вовсе - деплой девкита сам
//     катится одной командой без деления на deploy.<имя> (живой пример:
//     .devkit/deploy.local самого devkit), и опора только на неё оставляла
//     бы любой компонент неопознанным всегда, а значит own=false всегда, и
//     каждое красное слияние проекта без раскладки садилось бы в
//     foreign-fails как чужое, включая честно своё (замечание ревью круга
//     2, DK-1125). Root у раннера есть всегда, независимо от раскладки
//     выката: он и берёт эту роль.
//
// Ни раскладки, ни Root нет - resolved остаётся false, и own тоже: без
// единого источника границы шипctl её не угадывает по голому имени, оно
// раньше врало в обе стороны (замечание ревью круга 1, DK-1125).
//
// Раскладка, заведённая для имени без единой deploy.<имя>.paths строки
// (deployconf.Load тогда отдаёт Component с пустым Paths вместе с ошибкой,
// а её здесь молча роняют), честного ответа дать не может: цикл по Paths
// пуст, own навсегда застрял бы в false, а до Root, который мог бы решить
// честно, дело бы не дошло. Такая раскладка приравнена к отсутствующей, и
// резолв идёт дальше к Root (замечание ревью круга 3, запись 4, DK-1125).
func resolveOwnership(cfg deployConfig, name, root string, diff []string) (resolved, own bool) {
	for _, comp := range cfg.Components {
		if comp.Name != name {
			continue
		}
		if len(comp.Paths) == 0 {
			break
		}
		resolved = true
		for _, p := range diff {
			for _, prefix := range comp.Paths {
				if pathUnder(p, prefix) {
					return true, true
				}
			}
		}
		return true, false
	}
	if root == "" {
		return false, false
	}
	resolved = true
	for _, p := range diff {
		if pathUnder(p, root) {
			return true, true
		}
	}
	return true, false
}

// pathUnder проверяет, лежит ли path под prefix: как файл целиком либо
// внутри каталога, который тот называет. Копия приёма deployconf.pathUnder:
// та версия не экспортирована, а тянуть отдельный пакет ради одной проверки
// на шесть строк незачем. Корень-точка это отдельный случай: он называет не
// подкаталог, а весь репозиторий целиком (живой пример - компонент doctor,
// tools/devkitctl/parallel.py, cwd="."), а git diff --name-only не отдаёт
// путей вида "./..." или голого ".", так что общее правило (path==prefix
// либо HasPrefix(path, prefix+"/")) для этого корня не сработало бы ни на
// одном реальном пути (замечание ревью круга 3, запись 3, DK-1125).
func pathUnder(path, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return false
	}
	if prefix == "." {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// foreignFails считает слияния, отбитые компонентом вне диффа задачи, за
// срок since: запись в счёт идёт, когда прогон целиком красный и ни один из
// провалившихся компонентов не признан своим (Own при Resolved). Своя
// краснота отбивает слияние законно и в счёт не идёт. Неопознанный компонент
// (раскладка для его имени не заведена) своим не считается никогда и уходит
// в счёт вместе с точно чужим: ложный ноль опаснее ложной единицы, потому
// что прячет реальную чужую красноту (замечание ревью круга 1, DK-1125).
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
			if c.Resolved && c.Own {
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

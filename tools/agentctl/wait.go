package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/attime"
	"github.com/dronrider/devkit/internal/merged"
)

// Отметка машинного ожидания. Сессия конвейера кончает ход не только сделанной
// работой: слияние отбивается грязной доской соседа, фоновое дело идёт своим
// чередом, и ход тогда кончается коротким отчётом «жду». Оболочка task-run.py
// такой проход до DK-899 не отличала от головы, вылетевшей на подъёме, и
// снимала окно воронкой на третьем таком проходе вместе с работой (DK-893,
// DK-510 за одну ночь).
//
// Отметка это и есть разница. Исполнитель говорит ею, чего ждёт и сколько, а
// оболочка читает условие с диска и проверяет его сама. Словам хода тут места
// нет: разбирать прозу оболочке нечем, а условие вида «дерево чисто» она
// проверяет одной командой.
//
// Запись лежит файлом на задачу, а не на сессию. Ждать умеет и голова окна, и
// субагент, и печатный проход, ID сессии есть не у всех троих, а задачу знает
// каждый; оболочка же знает свою задачу всегда.

const (
	waitDirEnv  = "DEVKIT_WAIT_DIR"
	waitSessEnv = "CLAUDE_CODE_SESSION_ID"
	waitStamp   = "2006-01-02T15:04:05"
	// Потолок срока, когда профиль харнеса его не называет. Ключ профиля
	// читает и оболочка task-run.py, и профиль у них выбирается одинаково:
	// DEVKIT_HARNESS, иначе claude-code.
	waitCapDefault     = "2h"
	waitCapKey         = "wait_cap"
	waitHarnessDefault = "claude-code"
)

// Виды условия. Растёт словарь по нужде: каждый вид оболочка обязана уметь
// проверить без вопросов к сессии. «Слита» и «закрыта» оболочка спрашивает у
// этой же команды с флагом --check, остальные проверяет сама.
const (
	waitTimer  = "срок"
	waitHere   = "есть"
	waitGone   = "нет"
	waitClean  = "чисто"
	waitMerged = "слита"
	waitClosed = "закрыта"
	waitProc   = "процесс"
	waitHour   = "час"
)

// waitKinds перечисляет виды в порядке справки: сначала голый срок, следом
// условия с целью.
var waitKinds = []string{waitTimer, waitHere, waitGone, waitClean, waitMerged, waitClosed, waitProc, waitHour}

// waitIDRe это форма ID задачи. Проверяется она отдельно от доски: ID едет в
// имя файла отметки, и всё, что на ID не похоже, отбивается до записи.
var waitIDRe = regexp.MustCompile(`^[A-Za-z]{1,10}-\d{1,6}$`)

// waitMark это запись ожидания в том виде, в каком она лежит в файле. Читает
// её оболочка конвейера (kit/skills/board-task/task-run.py), и имена полей у
// них общие.
type waitMark struct {
	Task    string `json:"task"`
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Since   string `json:"since"`
	Until   string `json:"until"`
	Note    string `json:"note"`
}

// waitLimit это потолок срока и место, откуда он взят. Место идёт в отказ:
// человеку, упёршемуся в потолок, надо знать, какой файл его держит.
type waitLimit struct {
	Span time.Duration
	Text string
	From string
}

// waitDir это каталог отметок машины. Подмена ключом нужна стенду: настоящий
// каталог общий на машину, и самопроверке в нём не место. Дом спрашивается
// только там, где подмены нет: у стенда своего дома может не быть вовсе.
func waitDir(env func(string) string) (string, error) {
	if dir := env(waitDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("не видно дома пользователя, отметке некуда лечь: %v", err)
	}
	return filepath.Join(home, ".devkit", "waits"), nil
}

// waitCapOf берёт потолок из секции [head] профиля харнеса. Профиль называет
// DEVKIT_HARNESS, иначе claude-code: так же его выбирает оболочка, и потолок
// у писателя отметки и у её читателя один. Профиль без ключа и машина без
// каталога профилей получают двухчасовое умолчание, а кривое значение даёт
// отказ: молча подменённый потолок неотличим от настоящего.
func waitCapOf(start string, env func(string) string) (waitLimit, error) {
	def, _ := time.ParseDuration(waitCapDefault)
	name := strings.TrimSpace(env(harnessEnv))
	if name == "" {
		name = waitHarnessDefault
	}
	dir, err := harnessDir(start)
	if err != nil {
		return waitLimit{def, waitCapDefault, "умолчание, каталога профилей харнесов не нашёл"}, nil
	}
	p, err := loadProfile(dir, name)
	if err != nil {
		return waitLimit{}, fmt.Errorf("потолок срока не прочитан: %v", err)
	}
	raw := ""
	if t := p.section("head"); t != nil {
		raw = t.str(waitCapKey)
	}
	if raw == "" {
		return waitLimit{def, waitCapDefault, fmt.Sprintf("умолчание, в профиле %s нет [head] %s", p.Path, waitCapKey)}, nil
	}
	span, err := time.ParseDuration(raw)
	if err != nil || span <= 0 {
		return waitLimit{}, fmt.Errorf("профиль %s: [head] %s = %q не разобран, жду вид 90m, 2h", p.Path, waitCapKey, raw)
	}
	return waitLimit{span, raw, "профиль " + p.Path}, nil
}

func waitKnown(kind string) bool {
	for _, k := range waitKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// waitTarget проверяет цель по виду условия и приводит её к записи, которую
// оболочка проверит без догадок: путь абсолютным, час абсолютным временем.
func waitTarget(kind, target string, now time.Time) (string, error) {
	if kind == waitTimer {
		if target != "" {
			return "", fmt.Errorf("условию %s цель не нужна, а пришло %q: жду только срок", kind, target)
		}
		return "", nil
	}
	if target == "" {
		return "", fmt.Errorf("условию %s нужна цель: %s", kind, waitUsage(kind))
	}
	switch kind {
	case waitHere, waitGone, waitClean:
		abs, err := filepath.Abs(target)
		if err != nil {
			return "", err
		}
		if kind == waitClean {
			if fi, serr := os.Stat(abs); serr != nil || !fi.IsDir() {
				return "", fmt.Errorf("дерева %s нет: условию %s нужна директория рабочего дерева", abs, waitClean)
			}
		}
		return abs, nil
	case waitMerged, waitClosed:
		if !waitIDRe.MatchString(target) {
			return "", fmt.Errorf("цель условия %s это ID задачи, а пришло %q: %s", kind, target, waitUsage(kind))
		}
		return target, nil
	case waitProc:
		pid, err := strconv.Atoi(target)
		if err != nil || pid <= 0 {
			return "", fmt.Errorf("цель условия %s это pid числом, а пришло %q: %s", kind, target, waitUsage(kind))
		}
		return strconv.Itoa(pid), nil
	case waitHour:
		at, err := attime.Parse(target, now)
		if err != nil {
			return "", err
		}
		return at.Format(waitStamp), nil
	}
	return "", fmt.Errorf("условие %q неизвестно, виды: %s", kind, strings.Join(waitKinds, ", "))
}

// waitUsage это строка вызова для вида: её называют отказы.
func waitUsage(kind string) string {
	switch kind {
	case waitMerged, waitClosed:
		return "wait <ID> " + kind + " <ID задачи> --until <срок>"
	case waitProc:
		return "wait <ID> процесс <pid> --until <срок>"
	case waitHour:
		return "wait <ID> час <02:00 | 2026-09-12 02:00>"
	}
	return "wait <ID> " + kind + " <путь> --until <срок>"
}

// waitBuild собирает отметку и отбивает то, чего оболочка не поймёт: чужой вид
// условия, цель не по виду, срок за потолком. Сроку потолок нужен по делу:
// ожидание длиннее него это уже не пауза внутри работы, а парковка, и
// ставится она строке доски, а не оболочке (DK-400). Час сам себе срок, и
// потолок меряется до него.
func waitBuild(id, kind, target, until, note string, lim waitLimit, env func(string) string, now time.Time) (waitMark, error) {
	var m waitMark
	if !waitIDRe.MatchString(id) {
		return m, fmt.Errorf("%q не похоже на ID задачи, жду вид DK-899", id)
	}
	if !waitKnown(kind) {
		return m, fmt.Errorf("условие %q неизвестно, виды: %s", kind, strings.Join(waitKinds, ", "))
	}
	target, err := waitTarget(kind, target, now)
	if err != nil {
		return m, err
	}
	var end time.Time
	if kind == waitHour {
		if until != "" {
			return m, fmt.Errorf("у условия %s срок это сам час, --until не нужен", waitHour)
		}
		end, _ = time.ParseInLocation(waitStamp, target, now.Location())
		if span := end.Sub(now); span > lim.Span {
			return m, fmt.Errorf("до часа %s дольше потолка %s (%s): ожидание длиннее этого паркует строку доски, а не оболочку", target, lim.Text, lim.From)
		}
	} else {
		if until == "" {
			return m, fmt.Errorf("жду срок ожидания: --until 10m. Без срока оболочка ждала бы событие вечно")
		}
		span, err := time.ParseDuration(until)
		if err != nil {
			return m, fmt.Errorf("срок %q не разобран, жду вид 90s, 10m, 1h", until)
		}
		if span <= 0 {
			return m, fmt.Errorf("срок %s не больше нуля: ждать нечего", until)
		}
		if span > lim.Span {
			return m, fmt.Errorf("срок %s больше потолка %s (%s): ожидание длиннее этого паркует строку доски, а не оболочку", until, lim.Text, lim.From)
		}
		end = now.Add(span)
	}
	m = waitMark{Task: id, Session: env(waitSessEnv), Kind: kind, Target: target,
		Since: now.Format(waitStamp), Until: end.Format(waitStamp), Note: note}
	return m, nil
}

// errNoEvent: у голого срока события нет, и проверять его нечем.
var errNoEvent = errors.New("у условия срок события нет, оно кончается временем")

// waitCheck отвечает, пришло ли событие отметки, и называет ответ словами.
// Один и тот же разбор служит двум случаям. Команда до записи отбивает
// ожидание, чьё событие уже пришло, а с флагом --check отвечает оболочке про
// «слита» и «закрыта»: признак «слита» лежит в internal/merged, и
// переписывать его на python значило бы вторую копию, расходящуюся с первой.
func waitCheck(root string, m waitMark, now time.Time) (bool, string, error) {
	switch m.Kind {
	case waitHere, waitGone:
		_, err := os.Stat(m.Target)
		here := err == nil
		said := "путь " + m.Target + " есть"
		if !here {
			said = "пути " + m.Target + " нет"
		}
		return here == (m.Kind == waitHere), said, nil
	case waitClean:
		out, err := exec.Command("git", "-C", m.Target, "status", "--porcelain").Output()
		if err != nil {
			return false, "дерево " + m.Target + " git не прочитал", nil
		}
		if strings.TrimSpace(string(out)) == "" {
			return true, "дерево " + m.Target + " чисто", nil
		}
		return false, "в дереве " + m.Target + " есть незакоммиченное", nil
	case waitMerged:
		main, err := merged.Main(root)
		if err != nil {
			return false, "", err
		}
		v, err := merged.Task(root, main, m.Target)
		if err != nil {
			return false, "", err
		}
		return v.Merged, v.Said(m.Target), nil
	case waitClosed:
		main, _ := merged.Main(root)
		ok, err := merged.Closed(root, main, m.Target)
		if err != nil {
			return false, "", err
		}
		if ok {
			return true, m.Target + " в архиве доски", nil
		}
		return false, m.Target + " не в архиве доски", nil
	case waitProc:
		pid, _ := strconv.Atoi(m.Target)
		if procAlive(pid) {
			return false, "процесс " + m.Target + " жив", nil
		}
		return true, "процесса " + m.Target + " нет", nil
	case waitHour:
		at, err := time.ParseInLocation(waitStamp, m.Target, now.Location())
		if err != nil {
			return false, "", fmt.Errorf("час %q не разобран", m.Target)
		}
		if now.Before(at) {
			return false, "час " + m.Target + " ещё не наступил", nil
		}
		return true, "час " + m.Target + " наступил", nil
	}
	return false, "", errNoEvent
}

// procAlive отвечает, жив ли процесс. Сигнал 0 проверяет только наличие, а
// отказ в правах значит чужой, но живой процесс. Зомби сигнал тоже принимает,
// хотя процесс уже кончился и ждёт только, чтобы родитель его прибрал, поэтому
// состояние спрашивается у ps.
func procAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
		return false
	}
	return true
}

// waitWrite кладёт отметку файлом на задачу. Запись идёт через временный файл:
// оболочка читает каталог каждые несколько секунд, и половина записи попадалась
// бы ей нечитаемым файлом.
func waitWrite(dir string, m waitMark) (string, error) {
	data, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(dir, m.Task+".json")
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, file); err != nil {
		return "", err
	}
	return file, nil
}

// waitSaid это строка человеку и в чат: чего ждём, до какого часа и что из
// этого следует конвейеру. Слова тут не украшение. Ход, кончившийся ожиданием,
// снаружи неотличим от хода, кончившегося ничем, и эта строка единственное
// место, где ожидание названо в ленте разговора.
func waitSaid(m waitMark, file string) string {
	what := m.Kind
	if m.Target != "" {
		what += " " + m.Target
	}
	out := fmt.Sprintf("%s ждёт: %s, срок до %s", m.Task, what, m.Until)
	if m.Note != "" {
		out += " (" + m.Note + ")"
	}
	return out + fmt.Sprintf("\nотметка %s: оболочка конвейера заказ на этот проход не подаст, "+
		"в воронку и в потолок проходов его не посчитает и вернётся к работе по событию либо по сроку", file)
}

// cmdWait отмечает машинное ожидание. Ход после команды кончается штатно:
// команда только кладёт запись и возвращается, ждёт за сессию оболочка.
func cmdWait(root, id, kind, target, until, note string, env func(string) string, now time.Time) (string, error) {
	lim, err := waitCapOf(root, env)
	if err != nil {
		return "", err
	}
	m, err := waitBuild(id, kind, target, until, note, lim, env, now)
	if err != nil {
		return "", err
	}
	// Строка сверяется с доской: отметка на задачу, которой нет, легла бы
	// молча, а ход кончился бы ожиданием, которого никто не ждёт.
	rows, err := loadRows(root)
	if err != nil {
		return "", err
	}
	if rowOf(rows, id) == nil {
		return "", fmt.Errorf("строки %s на доске нет: ожидание некому читать", id)
	}
	// Событие, которое уже пришло, ждать нечего. Отметка легла бы, оболочка
	// тут же вернула бы заказ, и проход ушёл бы впустую. Отказ говорит
	// голове, что можно идти дальше в том же ходе.
	if kind != waitTimer {
		came, said, cerr := waitCheck(root, m, now)
		if cerr != nil {
			return "", fmt.Errorf("условие %s %s не проверить: %v", kind, m.Target, cerr)
		}
		if came {
			return "", fmt.Errorf("событие уже пришло, ждать нечего: %s", said)
		}
		if kind == waitMerged || kind == waitClosed {
			if err := waitRowKnown(root, rows, kind, m.Target); err != nil {
				return "", err
			}
		}
	}
	dir, err := waitDir(env)
	if err != nil {
		return "", err
	}
	file, err := waitWrite(dir, m)
	if err != nil {
		return "", err
	}
	return waitSaid(m, file), nil
}

// waitRowKnown отбивает ожидание строки, которой событие не грозит. Строки нет
// ни на доске, ни в архиве, значит ID выдуман или опечатан. Строка в архиве,
// а работы её в main нет, значит слияния уже не будет.
func waitRowKnown(root string, rows []row, kind, target string) error {
	if rowOf(rows, target) != nil {
		return nil
	}
	main, _ := merged.Main(root)
	closed, err := merged.Closed(root, main, target)
	if err != nil {
		return err
	}
	if !closed {
		return fmt.Errorf("строки %s нет ни на доске, ни в архиве: ждать её нечего", target)
	}
	return fmt.Errorf("%s уже в архиве, а работы её в main нет: слияния закрытой строки не будет", target)
}

// cmdWaitCheck отвечает на вопрос «пришло ли событие» сейчас, отметки не кладя.
// Выход 0 значит пришло, 1 не пришло, ошибка это выход 2: так оболочка
// отличает «ещё нет» от «проверить нечем».
func cmdWaitCheck(root, id, kind, target string, now time.Time) (string, bool, error) {
	if !waitIDRe.MatchString(id) {
		return "", false, fmt.Errorf("%q не похоже на ID задачи, жду вид DK-899", id)
	}
	if !waitKnown(kind) {
		return "", false, fmt.Errorf("условие %q неизвестно, виды: %s", kind, strings.Join(waitKinds, ", "))
	}
	if kind == waitTimer {
		return "", false, errNoEvent
	}
	norm, err := waitTarget(kind, target, now)
	if err != nil {
		return "", false, err
	}
	came, said, err := waitCheck(root, waitMark{Task: id, Kind: kind, Target: norm}, now)
	return said, came, err
}

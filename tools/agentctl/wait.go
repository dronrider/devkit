package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
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
	waitDirEnv   = "DEVKIT_WAIT_DIR"
	waitSessEnv  = "CLAUDE_CODE_SESSION_ID"
	waitStamp    = "2006-01-02T15:04:05"
	waitDeadline = 2 * time.Hour
)

// Виды условия. Их четыре, и растёт словарь по нужде: каждый вид оболочка
// обязана уметь проверить сама, без вопросов к сессии.
const (
	waitTimer = "срок"
	waitHere  = "есть"
	waitGone  = "нет"
	waitClean = "чисто"
)

// waitKinds перечисляет виды в порядке справки: сначала голый срок, следом
// условия с целью.
var waitKinds = []string{waitTimer, waitHere, waitGone, waitClean}

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

// waitNeedsTarget говорит, нужна ли виду цель. Голый срок цели не имеет, у
// остальных она обязательна: условие без цели оболочке не проверить.
func waitNeedsTarget(kind string) bool {
	return kind != waitTimer
}

// waitBuild собирает отметку и отбивает то, чего оболочка не поймёт: чужой вид
// условия, цель не по виду, срок за потолком. Сроку потолок нужен по делу:
// ожидание длиннее двух часов это уже не пауза внутри работы, а парковка, и
// ставится она строке доски, а не оболочке (DK-400).
func waitBuild(id, kind, target, until, note string, env func(string) string, now time.Time) (waitMark, error) {
	var m waitMark
	if !waitIDRe.MatchString(id) {
		return m, fmt.Errorf("%q не похоже на ID задачи, жду вид DK-899", id)
	}
	known := false
	for _, k := range waitKinds {
		if k == kind {
			known = true
		}
	}
	if !known {
		return m, fmt.Errorf("условие %q неизвестно, виды: %v", kind, waitKinds)
	}
	if waitNeedsTarget(kind) && target == "" {
		return m, fmt.Errorf("условию %s нужна цель: wait <ID> %s <путь> --until <срок>", kind, kind)
	}
	if !waitNeedsTarget(kind) && target != "" {
		return m, fmt.Errorf("условию %s цель не нужна, а пришло %q: жду только срок", kind, target)
	}
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
	if span > waitDeadline {
		return m, fmt.Errorf("срок %s больше потолка %s: ожидание длиннее этого паркует строку доски, а не оболочку", until, waitDeadline)
	}
	if target != "" {
		abs, aerr := filepath.Abs(target)
		if aerr != nil {
			return m, aerr
		}
		target = abs
	}
	if kind == waitClean {
		if fi, serr := os.Stat(target); serr != nil || !fi.IsDir() {
			return m, fmt.Errorf("дерева %s нет: условию %s нужна директория рабочего дерева", target, waitClean)
		}
	}
	m = waitMark{Task: id, Session: env(waitSessEnv), Kind: kind, Target: target,
		Since: now.Format(waitStamp), Until: now.Add(span).Format(waitStamp), Note: note}
	return m, nil
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
	m, err := waitBuild(id, kind, target, until, note, env, now)
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

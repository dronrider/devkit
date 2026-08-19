// Package works собирает живые работы машины: занятость задачи живой сессией
// нужна и дашборду (признак Run), и планировщику слота taskctl (ворота
// «дерево занято», LLD DK-400, решение 3). Правда о признаке одна и живёт
// здесь, чтобы утилита и экран не держали две копии одного разбора.
package works

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// idRe это форма ID строки доски: префикс буквами, номер цифрами. Производная
// сессия конвейера (task-DK-208_1_1786532648) под неё не ложится, и работой
// не считается.
var idRe = regexp.MustCompile(`^[A-Za-z]+-[0-9]+$`)

// procTimeout ограничивает tmux сроком: зависший вызов не должен держать ни
// запрос дашборда, ни запуск утилиты.
const procTimeout = 30 * time.Second

// Session это строка списка tmux: имя, число окон и время создания в
// unix-секундах, как их отдаёт формат tmux.
type Session struct {
	Name    string
	Windows int
	Created int64
}

// Sessions отдаёт сессии tmux машины. Ненулевой код ls это штатное «сессий
// нет»: без единой сессии tmux не держит сервера. Отсутствие tmux и ошибка
// тоже дают пустой список; различие «нет tmux» и «нет сессий» держит
// вызывающий, здесь его нечем получить.
func Sessions() []Session {
	ctx, cancel := context.WithTimeout(context.Background(), procTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "ls", "-F",
		"#{session_name}"+SessionSep+"#{session_windows}"+SessionSep+"#{session_created}")
	// Без WaitDelay Output ждёт трубу, которую после снятия процесса может
	// держать его потомок: срок обязан вернуть управление, а не вечное ожидание.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return ParseSessions(out)
}

// SessionSep это разделитель полей в формате tmux ls. Табуляция тут не годится
// вовсе: без UTF-8 локали в окружении tmux считает её непечатным знаком и
// подменяет подчёркиванием, отчего вся строка приезжает одним полем, а имя
// сессии выходит вида «chat-1_1_1787148735». Ловится это только там, где
// окружение бедное, то есть под launchd, и стоило разбора живьём: планировщик
// слота и дашборд разом переставали видеть чужие работы. Печатный разделитель
// от локали не зависит.
const SessionSep = "|"

// ParseSessions разбирает вывод tmux ls; вынесен из Sessions, чтобы тест
// гонял разбор без tmux. Пустой список, а не nil: «сессий нет» и «спросить
// не удалось» здесь неотличимы, а клиент ждёт список всегда. Табуляция
// понимается по-прежнему: формат сменился, а на диске и в чужих тестах могли
// остаться старые строки.
func ParseSessions(out []byte) []Session {
	sessions := []Session{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sep := SessionSep
		if !strings.Contains(ln, sep) && strings.Contains(ln, "\t") {
			sep = "\t"
		}
		fields := strings.Split(ln, sep)
		if fields[0] == "" {
			continue
		}
		s := Session{Name: fields[0]}
		if len(fields) > 1 {
			s.Windows, _ = strconv.Atoi(fields[1])
		}
		if len(fields) > 2 {
			s.Created, _ = strconv.ParseInt(fields[2], 10, 64)
		}
		sessions = append(sessions, s)
	}
	return sessions
}

// SessionTask узнаёт в имени сессии работу конвейера: task-<ID> и goal-<ID>
// с префиксом доски. Доска без префикка работ из tmux не получает: сессии
// машины общие, и без префикка чужая сессия опознавалась бы как своя.
func SessionTask(name, prefix string) (id, kind string) {
	if prefix == "" {
		return "", ""
	}
	for _, k := range []string{"task", "goal"} {
		rest, ok := strings.CutPrefix(name, k+"-")
		if ok && strings.HasPrefix(rest, prefix+"-") && idRe.MatchString(rest) {
			return rest, k
		}
	}
	return "", ""
}

// ReadEntry читает файл «ключ = значение», как записи реестра целей.
func ReadEntry(path string) map[string]string {
	data := map[string]string{}
	text, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	for _, ln := range strings.Split(string(text), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if k, v, ok := strings.Cut(ln, "="); ok {
			data[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return data
}

// RegistryGoals возвращает ID целей из реестра ~/.devkit/goals, чей цикл ведёт
// живая сессия в корне projectRoot: цель без tmux-сессии узнаётся только
// записью. Порядок сортированный, чтобы два вызова на одном реестре печатали
// одно и то же.
func RegistryGoals(home, projectRoot string) []string {
	var goals []string
	paths, _ := filepath.Glob(filepath.Join(home, ".devkit", "goals", "*.watch"))
	sort.Strings(paths)
	for _, path := range paths {
		entry := ReadEntry(path)
		if entry["goal"] == "" || entry["root"] == "" ||
			filepath.Clean(entry["root"]) != filepath.Clean(projectRoot) {
			continue
		}
		goals = append(goals, entry["goal"])
	}
	return goals
}

// Busy собирает занятые ID работ по машинным источникам: tmux-сессии конвейера
// и записи реестра целей. Третий источник дашборда, свежие транскрипты
// интерактивных окон, здесь не читается: он живёт у сервера экрана вместе с
// кэшем транскриптов, а планировщику довольно того, что видит tmux.
func Busy(prefix, home, projectRoot string) map[string]bool {
	busy := map[string]bool{}
	for _, sess := range Sessions() {
		if id, _ := SessionTask(sess.Name, prefix); id != "" {
			busy[id] = true
		}
	}
	for _, goal := range RegistryGoals(home, projectRoot) {
		busy[goal] = true
	}
	return busy
}

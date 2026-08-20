package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/works"
)

// Разговор с целью со стороны дашборда (LLD DK-136, «Что видит человек»): отметки
// доставки, которые кладёт подхват реплики hooks/chat-in.py, и правило живости
// витка, по которому пишется плашка чата. Своего состояния тут нет, читаются те
// же файлы, что пишут подхват с оболочкой.

// mailStamp это формат времени в отметке и в записи реестра целей: тот же
// «%Y-%m-%dT%H:%M:%S» без пояса, каким его пишут hooks/chat-in.py и
// watchRegister (tools/agentctl/watch.go). Пояс местный у обеих сторон.
const mailStamp = "2006-01-02T15:04:05"

// mailMoved это порог метки движения цели, и он обязан совпадать с MOVED
// подхвата реплики: разъедься они, дашборд обещал бы доставку там, где подхват
// её уже не делает. Сведёнными их держат разбор в hooks/README.md и тест
// TestGoalLiveRuleMatchesChatHook.
const mailMoved = 3 * time.Hour

// mailSayWord это начало строки доставки в журнале цикла. Свои строки подхват
// реплики движением цели не считает, и дашборд не считает тоже: иначе доставка
// держала бы цель живой сама за себя.
const mailSayWord = "чат"

// mailSayWordOld это прежнее слово той же строки: журналы целей его помнят, и
// принять старую строку за движение цели значило бы держать цель живой её же
// доставкой (разъезд слова с подхватом, находка разбора красноты).
const mailSayWordOld = "разговор"

// mailMark это отметка доставки: время, сессия витка и доставленная строка
// «Входящих» целиком. Сессия бывает пустой у строки, съеденной ключом --ask в
// живом чате, где имени витку никто не выдавал, и пустота эта штатная.
type mailMark struct {
	Line    string `json:"line"`
	At      string `json:"at"`
	Session string `json:"session,omitempty"`
}

func mailPath(projectPath, id string) string {
	return filepath.Join(projectPath, ".devkit", "goal-"+id+".mail")
}

// readMarks читает файл отметок. Запись это две строки, «время сессия» и сама
// доставленная строка: строка держится отдельной, потому что в ней живут
// пробелы, ёлочки и что угодно ещё, что написал человек. Битый хвост
// пропускается молча, отметки это не источник правды, а подпись под лежащей
// строкой «Входящих».
func readMarks(path string) []mailMark {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	rows := strings.Split(string(data), "\n")
	var out []mailMark
	for i := 0; i+1 < len(rows); i += 2 {
		head, line := strings.TrimSpace(rows[i]), rows[i+1]
		if head == "" || line == "" {
			continue
		}
		stamp, session, _ := strings.Cut(head, " ")
		session = strings.TrimSpace(session)
		if session == "-" {
			session = ""
		}
		out = append(out, mailMark{Line: line, At: stamp, Session: session})
	}
	return out
}

// delivered отбирает отметки лежащих строк. Порядок состояний тот же, в каком
// живут отметки: строки нет во «Входящих», значит её подхватил виток, и
// отставшая отметка этого не перебивает.
func delivered(pending []string, marks []mailMark) []mailMark {
	lying := map[string]bool{}
	for _, line := range pending {
		lying[line] = true
	}
	out := []mailMark{}
	for _, m := range marks {
		if lying[m.Line] {
			out = append(out, m)
		}
	}
	return out
}

// pidAlive это тот же kill -0, каким меряют живость оболочка (lock_busy,
// kit/skills/goal-loop/goal-run.py) и подхват реплики. Отказ по правам значит чужой
// живой процесс, а не отсутствие: замок мог взять другой пользователь.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// lockAlive отвечает, держит ли замок цели живая оболочка.
func lockAlive(projectPath, id string) bool {
	data, err := os.ReadFile(filepath.Join(projectPath, ".devkit", "goal-"+id+".lock", "pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	return pidAlive(pid)
}

// stampAt разбирает метку времени. Нулевое время значит, что метки нет:
// отсутствующий файл и пустое поле это именно отсутствие, а не нулевое время,
// иначе цель, которую ведут первый раз, оказалась бы протухшей с рождения.
func stampAt(text string) time.Time {
	at, err := time.ParseInLocation(mailStamp, strings.TrimSpace(text), time.Local)
	if err != nil {
		return time.Time{}
	}
	return at
}

// journalAt отдаёт время последней чужой строки журнала цикла. Чужая тут
// значит не своя: строку доставки подхват кладёт в тот же журнал.
func journalAt(path string) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	rows := strings.Split(string(data), "\n")
	for i := len(rows) - 1; i >= 0; i-- {
		head, rest, _ := strings.Cut(strings.TrimSpace(rows[i]), " ")
		if strings.HasPrefix(rest, mailSayWord) || strings.HasPrefix(rest, mailSayWordOld) {
			continue
		}
		if at := stampAt(head); !at.IsZero() {
			return at
		}
	}
	return time.Time{}
}

// goalSeen достаёт метку seen записи реестра целей. Ищется запись по цели и
// корню, как её ищет liveWorks, а вот отметка stopped не читается вовсе:
// сторожок меряет ею движение дерева, а не цели, и у живого витка, висящего
// на ожидании исполнителей, она стоит наравне с брошенным.
// Список записей реестра читается общим разбором каркаса (internal/works):
// формат один с занятостью работ, и своя копия жила бы в двух местах.
func (s *server) goalSeen(projectPath, id string) time.Time {
	for _, path := range globSorted(filepath.Join(s.cfg.Home, ".devkit", "goals", "*.watch")) {
		entry := works.ReadEntry(path)
		if entry["goal"] != id || entry["root"] == "" {
			continue
		}
		if filepath.Clean(entry["root"]) != filepath.Clean(projectPath) {
			continue
		}
		return stampAt(entry["seen"])
	}
	return time.Time{}
}

// goalLive отвечает, доедет ли реплика до идущего витка. Правило то же, каким
// живёт подхват: живой pid в замке цели, а без замка свежая метка движения
// (seen записи реестра и последняя чужая строка журнала цикла) с тем же
// порогом. Записью реестра, которой живёт liveWorks, вопрос не решается:
// убитая оболочка запись не снимает, и цель с мёртвым циклом держала бы
// обещание минут, хотя доставлять некому.
func (s *server) goalLive(projectPath, id string) bool {
	if lockAlive(projectPath, id) {
		return true
	}
	moved := s.goalSeen(projectPath, id)
	if log := journalAt(filepath.Join(projectPath, ".devkit", "goal-"+id+".log")); log.After(moved) {
		moved = log
	}
	if moved.IsZero() {
		return false
	}
	return s.now().Sub(moved) < mailMoved
}

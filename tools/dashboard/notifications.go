package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Лента уведомлений: GET /api/notifications отдаёт хвост журнала уведомителя
// ~/.devkit/notify.log, с ?stream=1 дописывает события по мере записи. Свой
// канал наружу дашборд не заводит (LLD DK-112): отправку держит
// hooks/notify.py, а лента это окно в его журнал. Стоп цикла, зов человека и
// завершение задачи попадают туда потому, что уведомитель зовут виток
// goal-run, taskctl move и стоп из самого дашборда.

// notifyRel это путь журнала уведомителя от дома. Журнал машинный, а не
// проектный: уведомитель один на все доски, и лента показывает всё, что на
// машине происходило.
const notifyRel = "notify.log"

// Метка текста в строке журнала: заголовок и тело баннера идут хвостом в
// ёлочках, а до них лежат поля, разделённые пробелами. Старые строки хвоста не
// имеют, и разбор их не теряет.
const notifyTextMark = " текст «"

// Ключевые слова полей строки журнала в порядке записи (hooks/notify.py, log).
// Разбор держится за них, а не за одни позиции: строка со сбитыми словами это
// чужая или битая строка, и ленту она обрушать не должна.
var notifyKeys = [...]string{1: "сессия", 3: "повод", 5: "уровень", 7: "бэкенд", 9: "цель"}

// notifyKinds сводит повод к типу события ленты: по типу идут фильтры экрана
// и параметр ?kind=. Поводы хуков сессии (конец хода, запрос разрешения,
// отработавший субагент) в три типа DoD не входят и остаются прочими.
var notifyKinds = map[string]string{
	"goal_stop":    "stop",
	"run_stop":     "stop",
	"wait_human":   "wait",
	"task_check":   "task",
	"task_blocked": "task",
	"task_fail":    "task",
}

// notifyLabels дают слова строке без текста: у старых строк журнала и у хуков
// сессии заголовка в журнале нет, а лента без слов ничего не рассказывает.
var notifyLabels = map[string]string{
	"permission_prompt":  "сессия ждёт разрешения",
	"agent_needs_input":  "сессия ждёт ответа",
	"elicitation_dialog": "диалог MCP",
	"idle_prompt":        "сессия ждёт ввода",
	"turn_done":          "сессия закончила ход",
	"subagent_stop":      "субагент отработал",
	"self-test":          "самопроверка уведомителя",
}

// notifyIDRe вылавливает ID доски из текста уведомления: по нему у стопа есть
// кнопка «Поднять виток», а у задачи переход на её экран. Своего поля с ID у
// журнала нет, и брать его больше неоткуда.
var notifyIDRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9]+\b`)

// Notification это строка ленты: время и повод от уведомителя, текст баннера
// и признак того, дошёл ли баннер. Молчание тут различимо не хуже пустоты:
// «баннера не было» и «баннер не ушёл» это разные строки.
type Notification struct {
	Time    string `json:"time"`
	Reason  string `json:"reason"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Level   string `json:"level,omitempty"`
	Session string `json:"session,omitempty"`
	Backend string `json:"backend,omitempty"`
	Result  string `json:"result,omitempty"`
	Sent    bool   `json:"sent"`
	ID      string `json:"id,omitempty"`
}

// splitNotifyText отрезает хвост с текстом баннера: заголовок и тело в
// ёлочках. Хвоста нет у строк, писанных до ленты, и это не поломка.
func splitNotifyText(line string) (head, title, body string) {
	cut := strings.Index(line, notifyTextMark)
	if cut < 0 {
		return line, "", ""
	}
	head = line[:cut]
	tail := strings.TrimSuffix(line[cut+len(notifyTextMark):], "»")
	if mid := strings.Index(tail, "» «"); mid >= 0 {
		return head, tail[:mid], tail[mid+len("» «"):]
	}
	return head, tail, ""
}

// parseNotifyLine разбирает строку журнала уведомителя. Непонятая строка
// пропускается без обрушения ленты, как битая строка транскрипта.
func parseNotifyLine(line string) (Notification, bool) {
	head, title, body := splitNotifyText(strings.TrimSpace(line))
	f := strings.Fields(head)
	if len(f) < 12 {
		return Notification{}, false
	}
	for i, want := range notifyKeys {
		if want != "" && f[i] != want {
			return Notification{}, false
		}
	}
	if _, err := time.Parse("2006-01-02T15:04:05", f[0]); err != nil {
		return Notification{}, false
	}
	n := Notification{
		Time: f[0], Reason: f[4], Level: dashless(f[6]), Session: dashless(f[2]),
		Backend: dashless(f[8]), Result: strings.Join(f[11:], " "),
		Title: title, Body: body,
	}
	n.Kind = notifyKinds[n.Reason]
	if n.Kind == "" {
		n.Kind = "other"
	}
	if n.Title == "" {
		n.Title = notifyLabels[n.Reason]
	}
	if n.Title == "" {
		n.Title = "уведомление"
	}
	// Дошёл баннер или нет, видно по коду возврата бэкенда; пропуски по окну,
	// по фокусу и по песочнице событие не отменяют, но и доставкой не были.
	n.Sent = n.Result == "код возврата: 0"
	n.ID = notifyIDRe.FindString(n.Title + " " + n.Body)
	return n, true
}

func dashless(v string) string {
	if v == "-" {
		return ""
	}
	return v
}

// notifyFilter это набор типов из ?kind=: пустой берёт всё.
func notifyFilter(raw string) map[string]bool {
	set := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			set[part] = true
		}
	}
	return set
}

func (n Notification) passes(filter map[string]bool) bool {
	return len(filter) == 0 || filter[n.Kind]
}

// parseNotifications разбирает строки хвоста в события ленты, оставляя
// подходящие под фильтр. Второе значение говорит, были ли в журнале события
// вообще: пустой фильтр и пустой журнал это разные пустоты.
func parseNotifications(lines []string, filter map[string]bool) (items []Notification, seen bool) {
	items = []Notification{}
	for _, ln := range lines {
		n, ok := parseNotifyLine(ln)
		if !ok {
			continue
		}
		seen = true
		if n.passes(filter) {
			items = append(items, n)
		}
	}
	return items, seen
}

func (s *server) notifyPath() string {
	return filepath.Join(s.cfg.Home, ".devkit", notifyRel)
}

const notifyMissingNote = "журнала уведомителя ~/.devkit/notify.log нет: на этой машине уведомитель ещё ни разу не срабатывал"

const notifyEmptyNote = "журнал уведомителя пуст: событий в нём ещё не было"

func notifyFilterNote(raw string) string {
	return fmt.Sprintf("под фильтр %s не попало ни одного события: в журнале они есть, но других типов", raw)
}

func (s *server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	path := s.notifyPath()
	filter := notifyFilter(r.URL.Query().Get("kind"))
	if r.URL.Query().Get("stream") == "1" {
		s.streamNotifications(w, r, path, filter)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists": false, "note": notifyMissingNote, "items": []Notification{}})
		return
	}
	items, seen := parseNotifications(tailLines(data, intParam(r, "n", tailDefault, tailMax)), filter)
	resp := map[string]any{"exists": true, "items": items}
	if len(items) == 0 {
		// Пустоты различимы: журнал без событий, фильтр без попаданий и
		// отсутствие журнала это три разных ответа, а не одинаковый пустой
		// экран.
		if seen {
			resp["note"] = notifyFilterNote(r.URL.Query().Get("kind"))
		} else {
			resp["note"] = notifyEmptyNote
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// streamNotifications шлёт хвост ленты и дальше дописывает события по мере
// записи в журнал: экран получает стоп цикла без перезагрузки страницы.
// Отсутствие журнала называется событием note, появление подхватит опрос.
func (s *server) streamNotifications(w http.ResponseWriter, r *http.Request, path string, filter map[string]bool) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	var offset int64
	if data, err := os.ReadFile(path); err == nil {
		data = lastComplete(data)
		items, seen := parseNotifications(tailLines(data, tailDefault), filter)
		for _, n := range items {
			sseEvent(w, f, "", marshalNotification(n))
		}
		if len(items) == 0 {
			if seen {
				sseEvent(w, f, "note", notifyFilterNote(r.URL.Query().Get("kind")))
			} else {
				sseEvent(w, f, "note", notifyEmptyNote)
			}
		}
		offset = int64(len(data))
	} else {
		sseEvent(w, f, "note", notifyMissingNote)
	}
	t := time.NewTicker(tailPoll)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			var lines []string
			lines, offset = newLines(path, offset)
			items, _ := parseNotifications(lines, filter)
			for _, n := range items {
				sseEvent(w, f, "", marshalNotification(n))
			}
		}
	}
}

func marshalNotification(n Notification) string {
	data, err := json.Marshal(n)
	if err != nil {
		return "{}"
	}
	return string(data)
}

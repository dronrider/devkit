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

	"github.com/dronrider/devkit/internal/sessions"
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
	"goal_stop":  "stop",
	"run_stop":   "stop",
	"wait_human": "wait",
	// idle_prompt шлёт харнес: ход кончился, а окно стоит с пустым
	// приглашением. Это запасной источник состояния «ждёт человека»
	// (waiting.go), и в прочих событиях он прятал бы ровно тот случай, ради
	// которого строка доски и загорается ожиданием.
	"idle_prompt": "wait",
	// task_ask шлёт инструмент ожидания taskctl ask: вопрос задан, и заход
	// стоит с ним прямо сейчас. Тип тот же, что у стопа с вопросом, потому что
	// ждут тут человека, а не машину.
	"task_ask": "wait",
	// task_nolead шлёт сам дашборд: сессия конвейера кончилась, а строка
	// осталась стоять в разработке (nolead.go).
	"task_nolead":  "task",
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

// Поля задачи и проекта стоят в строке журнала после цели перехода
// (hooks/notify.py, log). По ним лента ведёт от события к строке доски и к
// журналу агента, а «Поднять виток» бьёт в проект события, а не в открытый на
// экране.
const (
	notifyTaskKey    = "задача"
	notifyProjectKey = "проект"
	// notifyHeadOld это позиция результата в строке без полей задачи и
	// проекта, notifyHeadNew в строке с полями.
	notifyHeadOld = 11
	notifyHeadNew = 15
)

// notifyIDRe вылавливает ID доски из текста уведомления. Путь этот старый: так
// лента брала задачу, пока своего поля у события не было (DK-323), и держится
// он ради строк, написанных до полей. У строки с полями текст не разбирается.
var notifyIDRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9]+\b`)

// notifyTarget читает поля задачи и проекта и говорит, с какого поля начинается
// результат. Строка без полей узнаётся по ключевому слову: событие тогда
// разбирается по-старому и ленту не роняет.
func notifyTarget(f []string) (id, project string, head int) {
	if len(f) > notifyHeadNew && f[notifyHeadOld] == notifyTaskKey && f[notifyHeadOld+2] == notifyProjectKey {
		return dashless(f[notifyHeadOld+1]), dashless(f[notifyHeadOld+3]), notifyHeadNew
	}
	return "", "", notifyHeadOld
}

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
	Project string `json:"project,omitempty"`
	// Note это подпись события, оставшегося без задачи не по своей воле:
	// обрезанный ID сессии носят две записи реестра чатов, и выбирать между
	// ними наугад лента не берётся (nameWaitTasks в waiting.go).
	Note string `json:"note,omitempty"`
	// Chat это полный ID сессии, если событие удалось свести с чатом: по
	// короткому ID из журнала панель чат не откроет, а вести туда надо.
	Chat string `json:"chat,omitempty"`
}

// waitKind это тип события «ждут человека»: по нему идёт фильтр экрана
// «Ожидание пользователя» и параметр ?kind=wait.
const waitKind = "wait"

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
	id, project, at := notifyTarget(f)
	n := Notification{
		Time: f[0], Reason: f[4], Level: dashless(f[6]), Session: dashless(f[2]),
		Backend: dashless(f[8]), Result: strings.Join(f[at:], " "),
		Title: title, Body: body, ID: id, Project: project,
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
	if at == notifyHeadOld {
		// Строка написана до полей: задача берётся из текста, как бралась
		// раньше. У строки с полями пустая задача это честная пустота
		// (самопроверка, авария контура), и вылавливать ID из слов баннера
		// поверх неё было бы враньём.
		n.ID = notifyIDRe.FindString(n.Title + " " + n.Body)
	}
	return n, true
}

// bindNotifyTasks дописывает событиям задачу по реестру чатов. Событие рождается
// в сессии и несёт её ID, а задачу называет не всегда: ход агента в разговоре
// про задачу задачей не подписан вовсе. Привязка тут один ко многим, как везде
// после переделки реестра, и берётся свежайшая: сессия за заход трогает
// несколько строк, а событию нужна та, над которой она работала последней
// (замечание 5 седьмого круга POC).
func (s *server) bindNotifyTasks(list []Notification) {
	recs := s.bindsAll()
	if len(recs) == 0 {
		return
	}
	// Реестр держит полный ID сессии, а событие обрезанный до восьми знаков:
	// уведомитель пишет короткий. Сводится это одним проходом, а не поиском по
	// каждому событию.
	short := map[string][]string{}
	for sid := range recs {
		key := sid
		if len(key) > 8 {
			key = key[:8]
		}
		short[key] = append(short[key], sid)
	}
	for i := range list {
		n := &list[i]
		if n.Session == "" {
			continue
		}
		if full := short[n.Session]; len(full) == 1 {
			n.Chat = full[0]
		}
		if n.ID != "" {
			continue
		}
		hits := short[n.Session]
		if len(hits) != 1 {
			// Два разговора под одним обрезанным ID: выбирать наугад нельзя,
			// и событие остаётся без задачи, как и было.
			continue
		}
		if tasks := sessions.Touched(recs[hits[0]]); len(tasks) > 0 {
			n.ID = tasks[0]
		}
	}
}

// notifySandboxSkip это префикс результата у строк, которые уведомитель сам
// пометил пропуском по песочнице (log() в hooks/notify.py, DK-196): корень
// прогона лежит под TMPDIR, баннер про него ложный, и лента не должна
// смешивать его с живыми событиями (DK-283).
const notifySandboxSkip = "пропуск: песочница"

// sandboxSkipped узнаёт строку песочницы по результату: событие в журнале
// осталось, а до ленты доезжать ему незачем.
func (n Notification) sandboxSkipped() bool {
	return strings.HasPrefix(n.Result, notifySandboxSkip)
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
		if n.sandboxSkipped() {
			// Строка песочницы: событие было, но не своё, и лента ведёт себя
			// так, будто строки в журнале не было вовсе (DK-283).
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
	s.nameWaitTasks(items)
	s.bindNotifyTasks(items)
	items = s.dropTitleNoise(items)
	s.waitTitles(items)
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
		s.nameWaitTasks(items)
		s.bindNotifyTasks(items)
		items = s.dropTitleNoise(items)
		s.waitTitles(items)
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
			s.nameWaitTasks(items)
			s.bindNotifyTasks(items)
			items = s.dropTitleNoise(items)
			s.waitTitles(items)
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

// Мусор от суммаризации заголовков (баг девятого круга POC). Пока служебный
// вызов клиента не был помечен молчащим, каждая суммаризация писала в журнал
// «ход закончен» с заголовком того чата, который она называла. Новых таких
// строк больше нет, а насыпанные лежат в журнале и лезут в список. Узнаются они
// дёшево и точно: событие без задачи и без проекта, чей текст дословно совпал с
// заголовком, лежащим в кеше чатов. Совпасть случайно тут нечему, кеш заполнен
// ровно ответами haiku на эти же вызовы.
func (s *server) titleNoise() []string {
	var out []string
	entries, err := os.ReadDir(chatStoreDir(s.cfg.Home))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(chatStoreDir(s.cfg.Home), e.Name()))
		if err != nil {
			continue
		}
		var st chatStore
		if json.Unmarshal(data, &st) != nil || st.Title == "" {
			continue
		}
		// Заголовок бывает обрезан по числу слов, и в журнале лежит полный
		// текст той же реплики: сверка идёт по началу, а не по равенству.
		out = append(out, strings.TrimSuffix(st.Title, "..."))
	}
	return out
}

// dropTitleNoise выбрасывает из выдачи события, оставшиеся от суммаризации.
func (s *server) dropTitleNoise(list []Notification) []Notification {
	noise := s.titleNoise()
	if len(noise) == 0 {
		return list
	}
	junk := func(body string) bool {
		for _, t := range noise {
			if t != "" && strings.HasPrefix(body, t) {
				return true
			}
		}
		return false
	}
	out := list[:0]
	for _, n := range list {
		if n.Reason == "turn_done" && n.ID == "" && n.Project == "" && junk(n.Body) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// waitTitle переписывает событие ожидания на человеческий язык. Харнес шлёт
// сырое английское «Claude is waiting for your input», и в ленте это стояло
// строкой ни о чём: непонятно, какой чат ждёт и куда идти (замечание 19
// двенадцатого круга POC). Заголовок берётся той же лестницей, что у списка
// чатов, поэтому подпись у события и у чата одна.
func (s *server) waitTitles(list []Notification) {
	paths := map[string]string{}
	projects, _ := s.projects()
	for _, p := range projects {
		paths[p.Name] = p.Path
	}
	for i := range list {
		n := &list[i]
		if n.Reason != "idle_prompt" || n.Chat == "" {
			continue
		}
		projPath := paths[n.Project]
		if projPath == "" {
			n.Body = "чат ждёт вашего ответа"
			continue
		}
		info, ok := findSession(s.transcriptRoots(), projPath, n.Chat)
		if !ok {
			n.Body = "чат ждёт вашего ответа"
			continue
		}
		head := s.sessionHeadCached(info.path, info.stamp)
		said, _ := s.titleFor(n.Chat, head.Summary, head.First, false)
		if said == "" {
			said = "без заголовка"
		}
		n.Body = "Чат «" + said + "» ждёт вашего ответа"
	}
}

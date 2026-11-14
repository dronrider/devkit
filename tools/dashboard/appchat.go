package main

// Разговор чужого протокола (дорога app-server). Подъём чата семейства
// claude-code идёт tmux-окном с клавиатурным входом, а у клиента чужого
// протокола терминальной механики нет: его сервер приложения поднимается
// дочерним процессом этого демона, реплики уходят запросами, а лента
// собирается из уведомлений. Раскладку дороги называет профиль подписки ключом
// protocol, дашборд имён протоколов не сочиняет.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// appChat это живой разговор дороги app-server: процесс сервера, нить клиента
// и состояние хода. Живёт он в памяти демона, а разговор переживает перезапуск:
// лента лежит файлом, нить клиента продолжается resume, и следующий ход
// поднимает процесс заново сам.
type appChat struct {
	sid     string
	h       *Harness
	model   string
	effort  string
	dir     string
	proc    *appServer
	thread  string
	project string

	mu    sync.Mutex
	busy  bool
	turnAt time.Time
}

func (c *appChat) busyNow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busy
}

// setEffort и effortNow держат уровень глубины под тем же замком, что и
// занятость: реплика пишет его из HTTP-горутины, а ход читает при отправке,
// и две одновременные реплики без замка гоняли бы строковое поле.
func (c *appChat) setEffort(e string) {
	c.mu.Lock()
	c.effort = e
	c.mu.Unlock()
}

func (c *appChat) effortNow() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.effort
}

// appBusyCap это потолок одного хода без новостей: дольше он не «идёт», а
// висит, и состояние чата обязано это назвать, а не молчать часами.
const appBusyCap = 30 * time.Minute

// appFeedDir это каталог лент дороги app-server, рядом с записями разговоров.
func appFeedDir(home string) string {
	return filepath.Join(chatStoreDir(home), "appserver")
}

func (s *server) appFeedFile(sid string) string {
	return filepath.Join(appFeedDir(s.cfg.Home), sid+".jsonl")
}

// appFeedAppend дописывает записи в ленту разговора. Ключ записи это её номер
// в файле: он устойчив, как «источник:номер» транскриптной ленты, и по нему же
// режется история.
func (s *server) appFeedAppend(sid string, items []reply) error {
	file := s.appFeedFile(sid)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	lock, err := chat.Lock(file + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, it := range items {
		body, err := json.Marshal(it)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(body, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// appFeedRead читает ленту разговора целиком: файл это и есть разговор, он
// растёт хвостом и не переписывается. Ключ и номер записи ставятся при чтении.
func (s *server) appFeedRead(sid string) []reply {
	data, err := os.ReadFile(s.appFeedFile(sid))
	if err != nil {
		return nil
	}
	return appFeedParse(data)
}

// appFeedParse разбирает ленту из прочитанного файла: ключ и номер записи это
// её строка в файле, и разбор отделён от чтения, чтобы поток брал записи и
// смещение хвоста из одного снимка.
func appFeedParse(data []byte) []reply {
	out := []reply{}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var it reply
		if json.Unmarshal([]byte(line), &it) != nil {
			continue
		}
		it.Seq, it.Key = i, fmt.Sprintf("app:%d", i)
		out = append(out, it)
	}
	return out
}

// appLive отдает живой процесс разговора. Мёртвый процесс вычищается на месте:
// смерть могла прийти между заходами, и лента узнаёт о ней здесь же.
func (s *server) appLive(sid string) *appChat {
	s.appMu.Lock()
	c := s.appChats[sid]
	if c == nil {
		s.appMu.Unlock()
		return nil
	}
	if !c.proc.gone() {
		s.appMu.Unlock()
		return c
	}
	s.appMu.Unlock()
	s.appDead(sid)
	return nil
}

// appDead убирает мёртвый процесс из реестра и, если ход был открыт, называет
// его обрыв в ленте: вечного ожидания ответа дорога не оставляет.
func (s *server) appDead(sid string) {
	s.appMu.Lock()
	c := s.appChats[sid]
	delete(s.appChats, sid)
	s.appMu.Unlock()
	if c == nil {
		return
	}
	c.mu.Lock()
	busy := c.busy
	c.busy = false
	why := c.proc.errWhyFmt()
	c.mu.Unlock()
	if busy {
		s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true,
			Text: "ход прерван: процесс клиента вышел (" + why + ")",
			Time: s.now().UTC().Format(time.RFC3339)}})
		s.logf("процесс app-server разговора %s вышел посреди хода", sid)
	}
}

// appSpawn поднимает процесс сервера приложения и готовит нить: свежую либо
// продолжением. Отказ на любом шаге убивает процесс и называется словами с
// хвостом stderr, потому что причины входа клиент пишет именно туда.
func (s *server) appSpawn(sid string, h *Harness, model, dir, project, thread string) (*appChat, error) {
	c := &appChat{sid: sid, h: h, model: model, dir: dir, project: project, thread: thread}
	proc, err := appServerStart(h, dir, binPath(agentctlBin), func(method string, params json.RawMessage) {
		s.appNote(sid, method, params)
	})
	if err != nil {
		return nil, err
	}
	c.proc = proc
	fail := func(err error) (*appChat, error) {
		proc.kill()
		// Хвост stderr читается после смерти процесса: клиент пишет причину
		// отказа туда, и гонять чтение раньше выхода значило бы терять её.
		time.Sleep(200 * time.Millisecond)
		if tail := strings.TrimSpace(proc.errTail()); tail != "" {
			return nil, fmt.Errorf("%w (%s)", err, tail)
		}
		return nil, err
	}
	if _, err := proc.call("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "devkit-dashboard", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return fail(fmt.Errorf("клиент не поднялся: %w", err))
	}
	if err := proc.notify("initialized", map[string]any{}); err != nil {
		return fail(fmt.Errorf("клиент не принял приветствие: %w", err))
	}
	if thread != "" {
		if _, err := proc.call("thread/resume", map[string]any{
			"threadId": thread, "excludeTurns": true,
		}); err != nil {
			return fail(fmt.Errorf("нить разговора не продолжилась: %w", err))
		}
	} else {
		raw, err := proc.call("thread/start", map[string]any{
			"cwd": dir, "model": model,
			"sandbox": "workspace-write", "approvalPolicy": "never",
		})
		if err != nil {
			return fail(fmt.Errorf("клиент не завёл разговор: %w", err))
		}
		var got struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(raw, &got) != nil || got.Thread.ID == "" {
			return fail(fmt.Errorf("клиент не назвал нить разговора"))
		}
		c.thread = got.Thread.ID
	}
	s.appMu.Lock()
	s.appChats[sid] = c
	s.appMu.Unlock()
	// Смерть процесса после подъёма: о ней узнаёт лента сразу же, а не следующим
	// заходом человека в разговор.
	go func() {
		<-proc.dead
		s.appDead(sid)
	}()
	return c, nil
}

// appEffort переводит уровень глубины дашборда в уровень клиента: верхний
// уровень клиента ниже дашбордного, и всё, что выше, идёт его потолком.
func appEffort(effort string) string {
	switch effort {
	case "low", "medium", "high", "xhigh":
		return effort
	default:
		return "xhigh"
	}
}

// appTurn отправляет реплику в нить и открывает ход. Занятость ставится до
// запроса: конец хода приходит уведомлением того же потока, вплотную за
// ответом и даже раньше него, и занятость, открытая после ответа, опаздывает
// за собственным погашением и висит вечно. Ручка отвечает сразу: реплика у
// клиента.
func (s *server) appTurn(c *appChat, text string) error {
	c.mu.Lock()
	c.busy, c.turnAt = true, s.now()
	c.mu.Unlock()
	_, err := c.proc.call("turn/start", map[string]any{
		"threadId": c.thread,
		"input":    []map[string]string{{"type": "text", "text": text}},
		"effort":   appEffort(c.effortNow()),
	})
	if err != nil {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
		return err
	}
	return nil
}

// appNote разбирает уведомления сервера: элементы хода ложатся в ленту, конец
// хода гасит занятость, а ошибка хода приходит словами в тот же хвост ленты.
func (s *server) appNote(sid, method string, params json.RawMessage) {
	if method != "item/completed" && method != "turn/completed" {
		return
	}
	now := s.now().UTC().Format(time.RFC3339)
	if method == "turn/completed" {
		var p struct {
			Turn struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"turn"`
		}
		json.Unmarshal(params, &p)
		s.appMu.Lock()
		if c := s.appChats[sid]; c != nil {
			c.mu.Lock()
			c.busy = false
			c.mu.Unlock()
		}
		s.appMu.Unlock()
		if p.Turn.Error != "" {
			s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true,
				Text: "ход кончился отказом клиента: " + p.Turn.Error, Time: now}})
		}
		return
	}
	var p struct {
		Item map[string]any `json:"item"`
	}
	if json.Unmarshal(params, &p) != nil || p.Item == nil {
		return
	}
	items := appItemReplies(p.Item, now)
	if len(items) > 0 {
		s.appFeedAppend(sid, items)
	}
}

// appItemReplies переводит элемент хода клиента в записи ленты. Проверенный
// контракт называет agentMessage и commandExecution, а прочие виды остаются
// воротами инструментов: неизвестное не выбрасывается молча, а ложится ходом с
// именем вида.
func appItemReplies(item map[string]any, now string) []reply {
	str := func(k string) string {
		v, _ := item[k].(string)
		return v
	}
	switch str("type") {
	case "agentMessage":
		if text := str("text"); strings.TrimSpace(text) != "" {
			return []reply{{Role: "assistant", Text: text, Time: now}}
		}
		return nil
	case "commandExecution":
		out := reply{Role: "tool", Tool: "Bash", Time: now,
			Note: str("command"), Text: str("aggregatedOutput")}
		if code, ok := item["exitCode"].(float64); ok && code != 0 {
			out.Fail = true
		}
		return []reply{out}
	case "reasoning":
		if text := str("text"); strings.TrimSpace(text) != "" {
			return []reply{{Role: "thinking", Text: text, Time: now}}
		}
		return nil
	default:
		kind := str("type")
		if kind == "" {
			return nil
		}
		note := strings.TrimSpace(str("command") + " " + str("name"))
		return []reply{{Role: "tool", Tool: kind, Note: note,
			Text: str("output") + str("aggregatedOutput"), Time: now}}
	}
}

// appSayRecord кладёт реплику человека в ленту: она и есть эхо отправки,
// транскрипта клиента дорога не читает.
func (s *server) appSayRecord(sid, text string) {
	s.appFeedAppend(sid, []reply{{Role: "user", Text: text,
		Wire: text, Time: s.now().UTC().Format(time.RFC3339)}})
}

// appSay это дорога реплики: живой процесс получает ход, мёртвый или новый
// разговор поднимается на месте. Отказ подъёма, занятость и пропажа входа
// называются словами, молчания тут не бывает.
func (s *server) appSay(w http.ResponseWriter, found *Project, sid, text, claim, effort string) {
	st := s.chatStoreRead(sid)
	model, harness := st.Model, st.Harness
	if model == "" {
		model, harness = s.chatChoice(sid, "")
	}
	h := s.chatHarnessOf(model, harness)
	if h == nil || !h.foreign() {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"подписка разговора не определилась (модель %s): разложить слой: devkitctl doctor --fix",
			chatModelName(model, harness))})
		return
	}
	// Уровень глубины ложится в запись разговора каждым изменением, а не только
	// сменой модели или нити: процесс смерти переживает, и без записи выбор
	// ползунка молча возвращался к умолчанию после каждого обрыва.
	effortSet := false
	if effort != "" && effort != st.CxEffort {
		st.CxEffort, effortSet = effort, true
	}
	if st.CxEffort == "" {
		st.CxEffort = "medium"
	}
	dir := chatTree(found.Path, st.Task)
	c := s.appLive(sid)
	if c == nil {
		var err error
		c, err = s.appSpawn(sid, h, model, dir, found.Name, st.Thread)
		if err != nil {
			msg := err.Error()
			s.logf("разговор %s не поднялся дорогой app-server: %s", sid, msg)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "чат не поднялся: " + msg})
			return
		}
		if st.Thread != c.thread {
			st.Thread = c.thread
		}
	}
	c.setEffort(st.CxEffort)
	if st.Model != model || st.Harness != harness || st.Thread != c.thread || effortSet {
		st.Model, st.Harness, st.Protocol = model, harness, h.Protocol
		if err := s.chatStoreWrite(sid, st); err != nil {
			s.logf("настройки разговора %s не записались: %v", sid, err)
		}
	}
	if c.busyNow() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ход уже идёт: дождитесь ответа либо остановите ход кнопкой стоп",
			"stuck": "ход чужого протокола идёт, реплика не принята"})
		return
	}
	s.appSayRecord(sid, text)
	if err := s.appTurn(c, text); err != nil {
		msg := err.Error()
		s.logf("реплика разговора %s не ушла в app-server: %s", sid, msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "реплика не ушла: " + msg})
		return
	}
	s.chatSayDone(sid, claim, "appserver")
	s.logf("реплика подана в app-server разговор %s (модель %s)", sid, chatModelName(model, harness))
	writeJSON(w, http.StatusOK, map[string]any{"way": "appserver", "session": sid,
		"message": "реплика ушла клиенту чужого протокола: ответ придёт в ленту"})
}

// appStart это подъём нового разговора с первой реплики: свежий ID, запись
// настроек и тот же ход, что у живого разговора. tmux-механики дороге не нужны,
// и ответ называет адрес ленты сразу, ждать родившейся сессии нечего.
func (s *server) appStart(w http.ResponseWriter, found *Project, id, blank, text, model, harness, effort string) {
	h := s.chatHarnessOf(model, harness)
	if h == nil || !h.foreign() {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"модель %s не определилась подпиской чужого протокола", chatModelName(model, harness))})
		return
	}
	sid := "as" + msgID()
	st := chatStore{Model: model, Harness: harness, Protocol: h.Protocol,
		Task: id, Project: found.Name, Born: s.now().Unix(), CxEffort: effort}
	if st.CxEffort == "" {
		st.CxEffort = "medium"
	}
	if err := s.chatStoreWrite(sid, st); err != nil {
		s.logf("настройки разговора %s не записались: %v", sid, err)
	}
	// Незачатая запись выросла этим разговором: панель, стоящая на её адресе,
	// переедет на живой разговор по полю Grown, как у tmux-дороги.
	if blank != "" && chatKeyRe.MatchString(blank) {
		if bs := s.chatStoreRead(blank); bs.Blank {
			bs.Blank, bs.Grown, bs.Lifted = false, sid, s.now().Unix()
			if err := s.chatStoreWrite(blank, bs); err != nil {
				s.logf("незачатая запись %s не пришилась к разговору %s: %v", blank, sid, err)
			}
		}
	}
	s.appSayRecord(sid, text)
	c, err := s.appSpawn(sid, h, model, chatTree(found.Path, id), found.Name, "")
	if err != nil {
		msg := err.Error()
		s.logf("разговор %s не поднялся дорогой app-server: %s", sid, msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "чат не поднялся: " + msg})
		return
	}
	c.setEffort(st.CxEffort)
	st.Thread = c.thread
	if err := s.chatStoreWrite(sid, st); err != nil {
		s.logf("настройки разговора %s не записались: %v", sid, err)
	}
	if err := s.appTurn(c, text); err != nil {
		msg := err.Error()
		s.logf("первая реплика разговора %s не ушла: %s", sid, msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "чат поднялся, но реплика не ушла: " + msg})
		return
	}
	s.chatSayDone(sid, "", "appserver")
	s.logf("чат поднят дорогой app-server в %s (модель %s, адрес %s)",
		found.Name, chatModelName(model, harness), sid)
	writeJSON(w, http.StatusOK, map[string]any{"way": "appserver", "session": sid,
		"model": model, "harness": harness,
		"message": "чат поднят дорогой app-server: ответ придёт в ленту этого разговора"})
}

// appStop останавливает ход: сперва уведомлением прерывания, а клиент,
// который не ответил на него за пару секунд, снимается процессом. Ход не
// остаётся вечным ни в одном исходе, и лента называет случившееся словами.
func (s *server) appStop(w http.ResponseWriter, sid string) {
	c := s.appLive(sid)
	if c == nil || !c.busyNow() {
		writeJSON(w, http.StatusOK, map[string]any{"way": "gone",
			"message": "ход не идёт: останавливать нечего"})
		return
	}
	err := c.proc.notify("turn/interrupt", map[string]any{"threadId": c.thread})
	for i := 0; i < 20 && err == nil; i++ {
		if !c.busyNow() {
			writeJSON(w, http.StatusOK, map[string]any{"way": "stopped",
				"message": "ход остановлен: реплика клиента оборвана"})
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	c.proc.kill()
	s.appMu.Lock()
	delete(s.appChats, sid)
	s.appMu.Unlock()
	c.mu.Lock()
	c.busy = false
	c.mu.Unlock()
	s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true,
		Text: "ход остановлен снятием процесса: клиент не принял прерывание",
		Time: s.now().UTC().Format(time.RFC3339)}})
	writeJSON(w, http.StatusOK, map[string]any{"way": "killed",
		"message": "клиент не принял прерывание, процесс снят: разговор продолжится новой репликой"})
}

// appDrop снимает процесс разговора целиком: под смену модели, уборку в архив
// и перезапуск. Лента остаётся, нить клиента продолжится следующим подъёмом.
func (s *server) appDrop(sid, why string) {
	s.appMu.Lock()
	c := s.appChats[sid]
	delete(s.appChats, sid)
	s.appMu.Unlock()
	if c == nil {
		return
	}
	c.proc.kill()
	s.logf("процесс app-server разговора %s снят: %s", sid, why)
}

// appStatus это состояние разговора для опроса панели: жив ли процесс, идёт
// ли ход и сколько он длится. Возраст хода говорит сервер, не браузер.
func (s *server) appStatus(w http.ResponseWriter, sid string) {
	c := s.appLive(sid)
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "live": false, "busy": false})
		return
	}
	busy := c.busyNow()
	out := map[string]any{"session": sid, "live": true, "busy": busy,
		"where": "app-server " + c.h.Name}
	if busy {
		out["status"] = "ход идёт"
		c.mu.Lock()
		at := c.turnAt
		c.mu.Unlock()
		if !at.IsZero() {
			age := s.now().Sub(at)
			out["since"] = at.UnixMilli()
			out["sec"] = int(age / time.Second)
			if age > appBusyCap {
				out["status"] = "ход идёт подозрительно долго"
			}
		}
	} else {
		out["status"] = "ждёт реплики"
		out["idle"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// appListRows собирает строки списка у дороги app-server: лента разговора это
// файл, и приходят они из своего каталога, а не из обхода транскриптов. Пустое
// имя проекта значит общий список машины.
func (s *server) appListRows(proj string) []chatEntry {
	ents, err := os.ReadDir(appFeedDir(s.cfg.Home))
	if err != nil {
		return nil
	}
	out := []chatEntry{}
	for _, de := range ents {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(name, ".jsonl")
		st := s.chatStoreRead(sid)
		if st.Protocol == "" || st.Hidden {
			continue
		}
		if proj != "" && st.Project != proj {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		e := chatEntry{ID: sid, Model: st.Model, PickHarness: st.Harness,
			Archived: st.Archived}
		e.State = chatDead
		if s.appLive(sid) != nil {
			e.State = chatLive
		}
		e.Harness = st.Harness
		if proj == "" {
			e.Project = st.Project
		}
		if st.Task != "" {
			e.Tasks = []string{st.Task}
		}
		e.Mtime = fi.ModTime().UTC().Format(time.RFC3339)
		e.Protocol = st.Protocol
		items := s.appFeedRead(sid)
		for _, it := range items {
			if it.Role == "user" && strings.TrimSpace(it.Text) != "" {
				e.First = truncate(strings.TrimSpace(it.Text), 200)
				e.Title = e.First
				break
			}
		}
		out = append(out, e)
	}
	return out
}

// appFeedServe отвечает лентой разговора чужого протокола той же формой, что
// лента транскрипта: шапка, окно записей и постраничная история по ключу. Лента
// это файл, целиком собранный чтением, и «раньше» режется по его номерам.
func (s *server) appFeedServe(w http.ResponseWriter, r *http.Request, sid string) {
	st := s.chatStoreRead(sid)
	file := s.appFeedFile(sid)
	fi, err := os.Stat(file)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("ленты разговора %s нет: она появится первой репликой", sid)})
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		s.appFeedStream(w, r, file)
		return
	}
	items := s.appFeedRead(sid)
	head := sessionInfo{ID: sid, Mtime: fi.ModTime().UTC().Format(time.RFC3339),
		Task: st.Task, Reply: "session", Harness: st.Harness, Protocol: st.Protocol,
		Live: s.appLive(sid) != nil}
	for _, it := range items {
		if it.Role == "user" && strings.TrimSpace(it.Text) != "" {
			head.First = truncate(strings.TrimSpace(it.Text), 200)
			break
		}
	}
	n := intParam(r, "n", repliesDefault, repliesMax)
	total := len(items)
	kept := beforeCut(items, r.URL.Query().Get("before"))
	start := len(kept) > 0 && len(kept) <= n
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	resp := map[string]any{"session": sid, "head": head, "total": total,
		"items": kept, "start": start}
	if total == 0 {
		resp["note"] = "лента разговора пуста: ответ клиента появится здесь ходом"
	}
	s.chatSeenMark(sid)
	writeJSON(w, http.StatusOK, resp)
}

// appFeedStream это лента живым потоком: хвост файла уезжает событиями по
// мере записи, как у транскриптной ленты. Ключ записи это её номер в файле, и
// счётчик продолжает нумерацию чтения без разрыва.
func (s *server) appFeedStream(w http.ResponseWriter, r *http.Request, file string) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	// Записи и смещение хвоста берутся из одного снимка файла: смещение из
	// раннего os.Stat опаздывало за записью, дописанной между Stat и открытием
	// потока, и она уезжала в панель дважды под двумя ключами.
	data, _ := os.ReadFile(file)
	items := appFeedParse(data)
	off := int64(len(data))
	if len(items) > repliesDefault {
		items = items[len(items)-repliesDefault:]
	}
	for _, it := range items {
		sseEvent(w, f, "", marshalReply(it))
	}
	if len(items) == 0 {
		sseEvent(w, f, "note", "лента разговора пуста: ответ клиента появится здесь ходом")
	}
	// Номер следующей записи файла: ключи потока продолжают нумерацию чтения,
	// чтобы панель, сменившая поток окном, не увидела одну запись под двумя
	// ключами.
	line := 0
	if len(data) > 0 {
		line = strings.Count(string(data), "\n")
		if !strings.HasSuffix(string(data), "\n") {
			line++
		}
	}
	t := time.NewTicker(tailPoll)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			lines, end := newLines(file, off)
			off = end
			for _, ln := range lines {
				var it reply
				if json.Unmarshal([]byte(ln), &it) != nil {
					line++
					continue
				}
				it.Seq, it.Key = line, fmt.Sprintf("app:%d", line)
				line++
				sseEvent(w, f, "", marshalReply(it))
			}
		}
	}
}

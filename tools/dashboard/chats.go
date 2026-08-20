package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Разговор с агентом (POC ветки poc-chat, переделка после отбитой приёмки
// DK-397). Диалог это сессия харнеса, один к одному с транскриптом: список
// диалогов собирается из каталогов транскриптов проекта, а реестр
// ~/.devkit/sessions.log говорит, каких задач сессия касалась, какой моделью
// она поднята и в какой tmux-сессии идёт.
//
// Прежнее устройство моделировало разговор файлами и отложенной доставкой:
// реплика ложилась строкой во вход и ждала хода инструмента. Отсюда четыре
// «чата» на одну цель и реплика, ушедшая чужому окну. Теперь доставка идёт в
// сам процесс: живой сессии реплика подаётся через tmux send-keys, кончившейся
// поднимается продолжение `claude --resume`, а новый диалог это новая
// tmux-сессия с репликой первым аргументом.

// chatModelDefault это модель нового диалога, пока человек не выбрал другую.
const chatModelDefault = "opus"

// chatModel это ступень выбора модели в панели: имя модели, ярус и подписка,
// чьей квотой она платится. Список собирается из раскладки подписок
// (agentctl harness --json), а не пишется в коде: имён поставщиков в дашборде
// нет ни одного, и новая подписка появляется в выборе сама.
type chatModelOpt struct {
	Model   string `json:"model"`
	Tier    string `json:"tier"`
	Harness string `json:"harness"`
	Default bool   `json:"default,omitempty"`
}

// chatModelOpts разворачивает лестницы всех включённых подписок в плоский
// список выбора. Повторы модели в одной подписке отсеиваются: у второй
// подписки верхние ярусы сложены одной моделью, и три одинаковых строки в
// выпадающем списке читались бы как ошибка.
func (s *server) chatModelOpts() []chatModelOpt {
	out := []chatModelOpt{}
	for _, h := range s.harnesses().Harnesses {
		seen := map[string]bool{}
		for _, m := range h.Models {
			if seen[m.Model] {
				continue
			}
			seen[m.Model] = true
			out = append(out, chatModelOpt{Model: m.Model, Tier: m.Tier,
				Harness: h.Name, Default: h.Default && m.Tier == "pro"})
		}
	}
	return out
}

// chatHarnessOf называет подписку, чьей моделью просят поднять разговор: у
// второй подписки клиент поднимается своим каталогом конфигурации, и без этого
// сессия ушла бы на чужую квоту.
func (s *server) chatHarnessOf(model string) *Harness {
	view := s.harnesses()
	for i := range view.Harnesses {
		for _, m := range view.Harnesses[i].Models {
			if m.Model == model {
				return &view.Harnesses[i]
			}
		}
	}
	return nil
}

// Состояния диалога. live это живой процесс в tmux, которым правит дашборд;
// vscode это свежий транскрипт без своей tmux-сессии, то есть окно человека, и
// писать туда с дашборда нечем; dead это кончившийся процесс, его продолжает
// резюм.
const (
	chatLive   = "live"
	chatVscode = "vscode"
	chatDead   = "dead"
)

// chatEntry это строка списка диалогов. Заголовок берётся из первой реплики
// человека, обрезанной, как это делает расширение Claude Code для vscode:
// имени диалог не требует, а первая реплика узнаётся глазом.
type chatEntry struct {
	ID      string   `json:"id"`
	Title   string   `json:"title,omitempty"`
	Mtime   string   `json:"mtime,omitempty"`
	Tasks   []string `json:"tasks,omitempty"`
	Model   string   `json:"model,omitempty"`
	Tmux    string   `json:"tmux,omitempty"`
	State   string   `json:"state"`
	Tree    string   `json:"tree,omitempty"`
	Branch  string   `json:"branch,omitempty"`
	Harness string   `json:"harness,omitempty"`
	// Живая сессия из реестра клиента: сокет канала, pid, слово про то, где она
	// идёт, и её состояние (idle значит «ждёт ввода»). Пусто значит, что
	// процесса у диалога нет вовсе.
	Sock  string `json:"sock,omitempty"`
	PID   int    `json:"pid,omitempty"`
	Where string `json:"where,omitempty"`
	Idle  bool   `json:"idle,omitempty"`
	// Summary это заголовок от самого харнеса: он старше и эвристики, и haiku.
	Summary string `json:"-"`
	// Live это модель, которой сессия работает на самом деле (из транскрипта).
	// Model рядом это сохранённый выбор дашборда, то есть чем её поднимать в
	// следующий раз. Расходятся они у чужого окна: там модель выбрана в самом
	// клиенте, и выбор дашборда до резюма не действует.
	LiveModel string `json:"liveModel,omitempty"`
	// Own говорит, дашбордова ли это сессия: только у своей смена модели
	// действует сразу следующим подъёмом, чужую до резюма не переубедить.
	Own bool `json:"own,omitempty"`
}

// chatStoreDir это каталог с настройками диалогов: модель живёт файлом при
// диалоге, а не полем реестра, потому что реестр дописывается строками от
// нескольких писателей, и правка одного поля там стоила бы перезаписи журнала.
func chatStoreDir(home string) string {
	return filepath.Join(home, ".devkit", "chats")
}

type chatStore struct {
	Model string `json:"model,omitempty"`
	// Title это заголовок разговора, названный haiku: харнес пишет summary не
	// всякому транскрипту, а первая реплика заголовком не годится. Считается он
	// один раз и живёт тут навсегда.
	Title string `json:"title,omitempty"`
	// Hidden убирает чат из списков насовсем: им помечены пробные чаты, поднятые
	// ради проверки дашборда, у которых метки в промпте не было.
	Hidden bool `json:"hidden,omitempty"`
	// From называет диалог, продолжением которого поднят этот: `claude --resume`
	// заводит новую сессию со своим транскриптом, и без этой ссылки история
	// разговора рвалась бы на две строки списка.
	From string `json:"from,omitempty"`
}

func (s *server) chatStoreRead(key string) chatStore {
	var st chatStore
	data, err := os.ReadFile(filepath.Join(chatStoreDir(s.cfg.Home), key+".json"))
	if err != nil {
		return st
	}
	json.Unmarshal(data, &st)
	return st
}

func (s *server) chatStoreWrite(key string, st chatStore) error {
	dir := chatStoreDir(s.cfg.Home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, key+".json"), data, 0o644)
}

// chatKeyRe сито ключа настроек: ключом бывает ID сессии либо имя tmux-сессии,
// и ни то, ни другое не вправе уводить запись из своего каталога.
var chatKeyRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,120}$`)

// chatModel называет модель диалога. Порядок такой: своя запись при сессии,
// запись при имени tmux-сессии (её кладёт подъём нового диалога, когда ID
// сессии ещё не родился), дальше умолчание.
func (s *server) chatModel(sid, tmux string) string {
	if sid != "" && chatKeyRe.MatchString(sid) {
		if st := s.chatStoreRead(sid); st.Model != "" {
			return st.Model
		}
	}
	if tmux != "" && chatKeyRe.MatchString(tmux) {
		if st := s.chatStoreRead("tmux-" + tmux); st.Model != "" {
			return st.Model
		}
	}
	return chatModelDefault
}

// chatEntries собирает список диалогов проекта. Транскрипты идут свежими
// сверху, привязка к задачам приходит из реестра по факту работы плюс хвост
// имени бокового дерева: дерево заводится ровно под одну задачу и врать не
// умеет. Угадывания по первой реплике тут нет вовсе, оно и разводило один
// разговор на четыре карточки.
func (s *server) chatEntries(projPath string, limit int) []chatEntry {
	recs := sessions.LoadAll(s.cfg.Home)
	alive := tmuxAliveFn()
	names := harnessRoots(s.harnesses())
	live := s.peers()
	cutoff := s.now().Add(-sessionLiveTTL)
	out := []chatEntry{}
	for i, f := range sessionFiles(s.transcriptRoots(), projPath) {
		if limit > 0 && i >= limit {
			break
		}
		head := s.sessionHeadCached(f.path, f.stamp)
		// Служебная сессия суммаризации чатом не является: её завёл сам
		// дашборд ради заголовка, и в списке ей делать нечего.
		if titleSession(head.First) || s.chatStoreRead(f.ID).Hidden {
			continue
		}
		last := sessions.Last(recs[f.ID])
		tasks := sessions.Touched(recs[f.ID])
		if id := taskIDInName(f.suffix); id != "" && !hasTask(tasks, id) {
			tasks = append([]string{id}, tasks...)
		}
		e := chatEntry{
			ID: f.ID, Title: head.First, Summary: head.Summary, Mtime: f.Mtime, Tasks: tasks,
			LiveModel: modelShort(readSessionModel(f.path)),
			Own:       last.Tmux != "",
			Tmux:      last.Tmux, Tree: f.suffix, Branch: head.Branch,
			Harness: names[f.root],
			Model:   s.chatModel(f.ID, last.Tmux),
		}
		// Мера состояния: реестр живых сессий клиента старше всего остального.
		// Есть запись с живым процессом, значит диалог идёт и ему есть куда
		// писать, чем бы он ни был поднят, окном vscode или tmux-сессией
		// дашборда. Прежняя мера (своё имя tmux плюс свежесть транскрипта)
		// осталась запасной: реестр появился не во всякой версии клиента.
		if p, ok := live[f.ID]; ok {
			e.Sock, e.PID, e.Where = p.Sock, p.PID, peerWord(p)
			// Простой мерится транскриптом: поле реестра у сессий vscode
			// пустое всегда, и по нему работающий агент выходил простаивающим.
			e.Idle = !sessionBusy(f.path, s.now())
			if p.Status == "busy" {
				e.Idle = false
			}
			if p.Tmux != "" && e.Tmux == "" {
				e.Tmux = strings.SplitN(p.Tmux, ":", 2)[0]
			}
		}
		switch {
		case e.Sock != "":
			e.State = chatLive
		case e.Tmux != "" && alive(e.Tmux):
			e.State = chatLive
		case e.Tmux != "":
			e.State = chatDead
		case f.mod.After(cutoff):
			e.State = chatVscode
		default:
			e.State = chatDead
		}
		out = append(out, e)
	}
	return out
}

func hasTask(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// chatListLimit держит число транскриптов, у которых читается шапка на один
// заход списка: заголовок диалога это первая реплика, и её чтение платится
// один раз на транскрипт, дальше шапка лежит в памяти процесса.
const chatListLimit = 80

func (s *server) handleChatList(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "чаты")
	if found == nil {
		return
	}
	list := s.chatEntries(found.Path, chatListLimit)
	// Поиск по имени tmux-сессии: им дашборд узнаёт ID сессии, поднятой минуту
	// назад, когда хук старта уже успел записать строку реестра.
	if name := strings.TrimSpace(r.URL.Query().Get("tmux")); name != "" {
		var hit []chatEntry
		for _, e := range list {
			if e.Tmux == name {
				hit = append(hit, e)
			}
		}
		list = hit
	}
	if want := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("task"))); want != "" {
		var hit []chatEntry
		for _, e := range list {
			if hasTask(e.Tasks, want) {
				hit = append(hit, e)
			}
		}
		list = hit
	}
	if list == nil {
		list = []chatEntry{}
	}
	s.titleFill(list)
	resp := map[string]any{"project": found.Name, "chats": list, "models": s.chatModelOpts()}
	if len(list) == 0 {
		resp["note"] = "чатов тут пока нет: заведите новый кнопкой «+»"
	}
	writeJSON(w, http.StatusOK, resp)
}

// chatNewName выбирает имя tmux-сессии диалога: chat-<ID>-<n> у диалога с
// задачей, chat-<n> у диалога без неё. Номер не растёт вечно, снятый диалог
// отдаёт имя следующему.
func chatNewName(id string, alive func(string) bool) string {
	base := "chat"
	if id != "" {
		base = "chat-" + id
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", base, n)
		if !alive(name) {
			return name
		}
	}
}

// chatCmd собирает команду клиента для tmux. Реплика человека едет первым
// аргументом: интерактивный клиент берёт её как первый вопрос и остаётся
// стоять, дальше реплики подаются в тот же процесс через send-keys.
func chatCmd(env, model, resume, text string, h *Harness, agentctl string) string {
	client := defaultClient
	head := env
	if h != nil && !h.Default {
		// Модель чужой подписки поднимается её же обвязкой: пары окружения
		// (каталог конфигурации, endpoint, токен) кладёт agentctl exec, и
		// собирать их тут самому нельзя, они поселились бы в процессе, который
		// раздаёт экраны (LLD DK-328, решение 3).
		client = shQuote(agentctl) + " exec --harness " + shQuote(h.Name) + " -- " + shQuote(h.Bin)
	}
	cmd := head + client + " --model " + shQuote(model)
	if resume != "" {
		cmd += " --resume " + shQuote(resume)
	}
	if text != "" {
		cmd += " " + shQuote(text)
	}
	return cmd
}

// chatVars это пары окружения диалога: задачу и имя tmux-сессии поднятая сессия
// называет о себе в реестре сама, хуком старта.
func chatVars(id, sess string) string {
	env := "DEVKIT_TMUX=" + shQuote(sess) + " "
	if id != "" {
		env = "DEVKIT_TASK=" + shQuote(id) + " " + env
	}
	// Дом ставится явно: tmux-сервер, поднятый самим демоном, наследует его
	// подложный HOME, и клиент в такой сессии не находит ни хуков, ни логина.
	// Уже поднятый сервер держит настоящий дом сам, и лишним это не будет.
	if home := realHome(); home != "" {
		env = "HOME=" + shQuote(home) + " " + env
	}
	// Опрос фокуса в сессии, поднятой дашбордом, не нужен вовсе: он ходит в
	// System Events, а macOS приписывает это дашборду и просит у него
	// разрешение на управление компьютером, заново после каждой пересборки
	// (находка одиннадцатого круга POC). Уведомления от такой сессии идут как
	// при неопределённом фокусе, то есть приходят.
	return noFocusEnv + " " + env
}

// noFocusEnv гасит опрос фокуса в хуке уведомителя. Ставится он всему, что
// поднимает дашборд; интерактивных сессий человека это не касается.
const noFocusEnv = "DEVKIT_NO_FOCUS=1"

// handleChatStart поднимает новый диалог: первая реплика человека и есть его
// начало, заголовок берётся из неё. Задача необязательна: разговор без задачи
// это обычное дело, и заводится он тем же порядком.
func (s *server) handleChatStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "подъём чата")
	if found == nil {
		return
	}
	var body struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	id := strings.ToUpper(strings.TrimSpace(body.ID))
	if id != "" && !taskParamRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на ID задачи", body.ID)})
		return
	}
	text := chatText(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустая реплика чата не поднимает: чат начинается со слов человека"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = chatModelDefault
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Дерево задачи предпочитается корню проекта: разговор про задачу, у которой
	// заведено боковое дерево, идёт там же, где её работа.
	dir := found.Path
	if id != "" {
		if tree := filepath.Join(filepath.Dir(found.Path), filepath.Base(found.Path)+"-"+strings.ToLower(id)); isDir(tree) {
			dir = tree
		}
	}
	sess := chatNewName(id, tmuxAliveFn())
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model}); err != nil {
		s.logf("модель чата %s не записалась: %v", sess, err)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, "", text, s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("подъём чата в %s не удался: %s", found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.logf("чат поднят в %s (tmux-сессия %s, модель %s, дерево %s)", found.Name, sess, model, dir)
	writeJSON(w, http.StatusOK, map[string]string{
		"tmux": sess, "model": model, "tree": dir,
		"message": fmt.Sprintf("чат поднят в tmux-сессии %s моделью %s: ID сессии встанет в списке первым её ходом", sess, model)})
}

// chatText готовит реплику человека к отправке. Переносы строк тут священны:
// человек пишет списком и абзацами, а прежняя сборка гнала текст через
// strings.Fields и склеивала всё в одну строку, отчего нумерованный список
// приезжал агенту кашей. Схлопывается только лишнее: возврат каретки, пробелы
// по краям строк и хвостовые пустые строки.
func chatText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	lines := strings.Split(raw, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n \t")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// chatSendPause это пауза между текстом и переводом строки при подаче реплики
// в живой процесс. Клиент читает ввод построчно и рисует его в поле; Enter,
// пришедший в том же пакете, что и текст, обгоняет отрисовку, и в поле
// остаётся половина реплики.
var chatSendPause = 250 * time.Millisecond

// chatSend подаёт реплику в живой процесс tmux-сессии. Текст идёт литералом
// (-l), иначе tmux разобрал бы слова вроде «Enter» и «C-c» как имена клавиш.
// Многострочная реплика едет в скобках вставки (bracketed paste): без них
// перенос строки внутри текста клиент читает как нажатие Enter и отправляет
// первую строку, а остальные разбирает как отдельные реплики.
func chatSend(name, text string) error {
	body := text
	if strings.Contains(text, "\n") {
		body = "\x1b[200~" + text + "\x1b[201~"
	}
	if _, err := runProc("tmux", "send-keys", "-t", "="+name+":", "-l", body); err != nil {
		return err
	}
	time.Sleep(chatSendPause)
	_, err := runProc("tmux", "send-keys", "-t", "="+name+":", "Enter")
	return err
}

// handleChatSay доставляет реплику диалогу. Правило одно на три состояния:
// живому процессу реплика подаётся прямо в него, кончившемуся поднимается
// продолжение той же сессии, а окно vscode дашборду не принадлежит, и туда
// человек пишет сам.
func (s *server) handleChatSay(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "реплика чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	text := chatText(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустая реплика никуда не едет"})
		return
	}
	info, ok := findSession(s.transcriptRoots(), found.Path, sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf(
			"транскрипта %s нет среди сессий проекта %s: разговаривать не с кем", sid, found.Name)})
		return
	}
	recs := sessions.LoadAll(s.cfg.Home)
	last := sessions.Last(recs[sid])
	// Первым делом канал самого клиента: живая сессия принимает реплику прямо в
	// свой сокет и просыпается за секунды, чем бы она ни была поднята. Окно
	// vscode отсюда тоже слышно, и отказывать ему больше не за что.
	if p, ok := s.peers()[sid]; ok {
		if err := peerSay(p.Sock, text); err == nil {
			s.saidSay(saidSessionKey(sid), text, "socket")
			s.logf("реплика ушла в сокет чата %s (pid %d, %s)", sid, p.PID, peerWord(p))
			writeJSON(w, http.StatusOK, map[string]any{"way": "socket", "pid": p.PID,
				"where": peerWord(p)})
			return
		} else {
			// Сокет есть, а разговора по нему не вышло: сессия могла умереть
			// между чтением реестра и записью. Дальше идут запасные дороги, и
			// причина остаётся в журнале, а не пропадает молча.
			s.logf("сокет чата %s не взял реплику, иду запасной дорогой: %v", sid, err)
		}
	}
	alive := tmuxAliveFn()
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if last.Tmux != "" && alive(last.Tmux) {
		if err := chatSend(last.Tmux, text); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
				"реплика не подалась в tmux-сессию %s: %s", last.Tmux, procErr(err))})
			return
		}
		s.saidSay(saidSessionKey(sid), text, "send-keys")
		s.logf("реплика подана в чат %s (tmux-сессия %s)", sid, last.Tmux)
		writeJSON(w, http.StatusOK, map[string]any{"way": "send-keys", "tmux": last.Tmux,
			"message": "реплика подана прямо в процесс агента: ответ придёт в ленту"})
		return
	}
	// Отказа «разговор идёт в vscode» тут больше нет: канал клиента достаёт
	// любое живое окно, и раз до сюда дошло, живого процесса у диалога нет ни в
	// реестре, ни в tmux. Свежий транскрипт без сокета значит клиента старой
	// версии либо сессию, умершую только что, и обоим годится резюм.
	// Процесса нет: поднимается продолжение той же сессии. История не рвётся,
	// клиент дочитывает её сам по --resume.
	task := ""
	if len(recs[sid]) > 0 {
		if t := sessions.Touched(recs[sid]); len(t) > 0 {
			task = t[0]
		}
	}
	if id := taskIDInName(info.suffix); id != "" {
		task = id
	}
	dir, okTree := sessionTree(found.Path, info.suffix)
	if !okTree {
		dir = found.Path
	}
	model := s.chatModel(sid, last.Tmux)
	sess := chatNewName(task, alive)
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, From: sid}); err != nil {
		s.logf("настройки чата %s не записались: %v", sess, err)
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(task, sess), model, sid, text, s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		msg := fmt.Sprintf("tmux не поднял продолжение чата %s: %s", sid, procErr(err))
		s.logf("%s", msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	// Имя tmux-сессии ложится в реестр на старый ID сразу: хук старта запишет
	// свою строку, когда клиент родится, а до того мера живости диалога уже
	// обязана работать, иначе вторая реплика подряд подняла бы второй резюм.
	sessions.Append(sessions.Path(s.cfg.Home),
		sessions.Line(s.now(), sid, sessions.Bind{Task: task, Source: "заказ",
			Project: found.Name, Tree: dir, Tmux: sess}, "резюм чата"))
	s.saidSay(saidSessionKey(sid), text, "resume")
	s.logf("чат %s продолжен резюмом в tmux-сессии %s (модель %s)", sid, sess, model)
	writeJSON(w, http.StatusOK, map[string]any{"way": "resume", "tmux": sess, "model": model,
		"message": fmt.Sprintf(
			"процесса у чата не было: поднят claude --resume в tmux-сессии %s, история продолжена", sess)})
}

// handleChatModel меняет модель диалога. Смена действует на следующий подъём
// или резюм: у идущего процесса модель уже выбрана его запуском, и подменить
// её со стороны нечем.
func (s *server) handleChatModel(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "модель чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"model\": \"sonnet\"}"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустая модель ничего не значит"})
		return
	}
	st := s.chatStoreRead(sid)
	st.Model = model
	if err := s.chatStoreWrite(sid, st); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("модель не записалась: %v", err)})
		return
	}
	s.logf("модель чата %s в %s теперь %s", sid, found.Name, model)
	writeJSON(w, http.StatusOK, map[string]string{"session": sid, "model": model,
		"message": fmt.Sprintf("модель чата теперь %s: она возьмётся на следующем подъёме или резюме сессии", model)})
}

// sortEntries держит список свежими сверху и при равном времени по ID: порядок
// обязан быть устойчивым, иначе выпадающий список прыгает под пальцем.
func sortEntries(list []chatEntry) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Mtime != list[j].Mtime {
			return list[i].Mtime > list[j].Mtime
		}
		return list[i].ID < list[j].ID
	})
}

// Продолжение работы задачи (замечание 9 второго круга POC). Кнопка
// «Продолжить» на экране задачи поднимала нового агента конвейером, и прежний
// разговор с его контекстом оставался в стороне. Теперь она продолжает
// последнюю сессию задачи: живой её будит канал, кончившейся поднимает резюм, и
// только там, где разговора нет вовсе, заводится новый.

// taskChat находит свежий диалог задачи. Свежесть тут по времени транскрипта:
// список уже отсортирован, и первый совпавший и есть последний разговор.
func (s *server) taskChat(projPath, id string) (chatEntry, bool) {
	for _, e := range s.chatEntries(projPath, chatListLimit) {
		if hasTask(e.Tasks, id) {
			return e, true
		}
	}
	return chatEntry{}, false
}

// continuePrompt это заказ продолжения. Он разговорный: сессия уже знает
// задачу, и пересказывать ей постановку незачем.
func continuePrompt(id string) string {
	return "Продолжай работу по " + id + " с того места, где остановился."
}

func (s *server) handleTaskContinue(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, row, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	// Цель продолжается так же, как задача, только правило про живую сессию у
	// неё другое: диспетчерская сессия цели это долгий цикл, и подгонять его
	// репликой незачем, он и так идёт (замечание про пункт 10 для целей).
	goal := isGoalTitle(row.Title)
	text := continuePrompt(id)
	if goal {
		text = "Продолжай цель " + id + "."
	}
	e, has := s.taskChat(found.Path, id)
	if !has {
		// Чата нет ни одного: поднимается новый, с той же репликой. Раньше тут
		// экран откатывался на подъём конвейера, и у цели это был не тот
		// механизм вовсе.
		s.startFresh(w, found, id, text)
		return
	}
	if e.Sock != "" {
		if goal {
			s.logf("цель %s уже идёт в живом чате %s (pid %d)", id, e.ID, e.PID)
			writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "live",
				"session": e.ID, "where": e.Where,
				"message": "цель " + id + " уже идёт: будить её нечем и незачем"})
			return
		}
		if err := peerSay(e.Sock, text); err == nil {
			s.logf("работа %s продолжена в живом чате %s (pid %d)", id, e.ID, e.PID)
			writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "socket",
				"session": e.ID, "where": e.Where})
			return
		}
	}
	sid := e.ID
	info, okS := findSession(s.transcriptRoots(), found.Path, sid)
	if !okS {
		s.startFresh(w, found, id, text)
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	dir, okTree := sessionTree(found.Path, info.suffix)
	if !okTree {
		dir = found.Path
	}
	model := s.chatModel(sid, e.Tmux)
	sess := chatNewName(id, tmuxAliveFn())
	s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, From: sid})
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, sid, text, s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("tmux не поднял продолжение работы %s: %s", id, procErr(err))})
		return
	}
	sessions.Append(sessions.Path(s.cfg.Home),
		sessions.Line(s.now(), sid, sessions.Bind{Task: id, Source: "заказ",
			Project: found.Name, Tree: dir, Tmux: sess}, "продолжение работы"))
	s.logf("работа %s продолжена резюмом чата %s в tmux-сессии %s", id, sid, sess)
	writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "resume",
		"session": sid, "tmux": sess})
}

// Живая работа агента (замечание третьего круга POC). После отправки реплики в
// ленте была тишина до готового ответа: агент думает и зовёт инструменты
// минутами, а панель показывала пустоту, неотличимую от непрошедшей отправки.
// Реестр клиента держит состояние сессии полем status (busy против idle) и
// обновляет его на каждой смене, отсюда индикатор и берёт правду.

func (s *server) handleChatStatus(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "состояние чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	p, ok := s.peers()[sid]
	if !ok {
		// Процесса нет вовсе: работать некому, и это не ошибка, а ответ.
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "live": false, "busy": false})
		return
	}
	// Пустой status это клиент, который его не пишет: занятость тогда неизвестна,
	// и врать про неё нечем. Индикатор в таком случае живёт лентой, а не опросом.
	busy := false
	if info, ok := findSession(s.transcriptRoots(), found.Path, sid); ok {
		busy = sessionBusy(info.path, s.now())
	}
	if p.Status == "busy" {
		busy = true
	}
	said := p.Status
	if said == "" {
		said = "по транскрипту"
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sid, "live": true,
		"busy": busy, "status": said, "where": peerWord(p)})
}

// Заголовок диалога (замечание 4 четвёртого круга POC). Первая реплика целиком
// заголовком не годится: «Привет. Ответь одной строкой: как называется этот
// проект? Ничего не делай, только скажи.» растягивалось на весь экран. Порядок
// такой, от дешёвого к дорогому: запись summary самого харнеса (её пишет
// Claude Code и ею же подписывает разговоры список `claude --resume`),
// сохранённый ранее заголовок из ~/.devkit/chats/<sid>.json, дальше эвристика
// первого предложения. Haiku зовётся фоном и только там, где эвристика
// работает плохо, а результат оседает в том же файле навсегда.

// titleWords это потолок заголовка словами: пять-семь слов читаются глазом
// целиком, длиннее уже не заголовок, а сама реплика.
const titleWords = 7

// titleTrim режет реплику до заголовка эвристикой: первое предложение без
// вежливых зачинов и без вопросительного хвоста. Ошибиться тут дёшево, а
// стоит она нисколько.
// Снимается только вежливый зачин: «ответь», «скажи» и родня несут сам заказ,
// и без них заголовок теряет смысл.
var titleDropRe = regexp.MustCompile(`^(?i)(привет|здравствуй\S*|слушай|окей|ок)[,!.\s]+`)

func titleTrim(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	// Первая строка: многострочная реплика это заказ, и заголовок ему даёт
	// первая строка, а не вся простыня.
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	for {
		cut := titleDropRe.ReplaceAllString(t, "")
		if cut == t {
			break
		}
		t = strings.TrimSpace(cut)
	}
	// Первое предложение: дальше идут уточнения вроде «ничего не делай».
	if i := strings.IndexAny(t, ".?!"); i > 0 {
		t = strings.TrimSpace(t[:i])
	}
	words := strings.Fields(t)
	if len(words) > titleWords {
		words = words[:titleWords]
		return strings.Join(words, " ") + "..."
	}
	return strings.Join(words, " ")
}

// titleMark это метка служебного вызова в самом начале промпта. По ней
// транскрипт суммаризации узнаётся в списках и выбрасывается из них: клиент
// пишет журнал всякому вызову, в том числе одноразовому, и без метки эти
// сессии всплывали чатами наравне с разговорами человека (баг девятого круга
// POC). Метка стоит первой строкой, потому что список читает только начало
// первой реплики.
const titleMark = "[devkit-title]"

// titleLegacy это начало промпта, каким он был до метки: уже написанные
// транскрипты узнаются по нему, и старый мусор уходит с экранов сам, без
// удаления файлов.
const titleLegacy = "Назови диалог заголовком"

// probeMark помечает пробный чат, поднятый ради проверки самого дашборда.
// Такие чаты не разговор человека, и в списках им делать нечего ровно по той же
// причине, по которой там нет сессий суммаризации (замечание 20).
const probeMark = "[devkit-probe]"

// titleSession узнаёт служебную сессию по первой реплике.
func titleSession(first string) bool {
	first = strings.TrimSpace(first)
	return strings.HasPrefix(first, titleMark) || strings.HasPrefix(first, titleLegacy) ||
		strings.HasPrefix(first, probeMark)
}

// titleDir это рабочая директория служебного вызова: каталог вне всех проектов,
// чтобы транскрипт лёг в свой угол и не попал ни в один список. Не создался,
// значит вызов пойдёт из директории процесса, и его подберёт фильтр по метке.
func (s *server) titleDir() string {
	dir := filepath.Join(s.cfg.Home, ".devkit", "titles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// titleAsk просит haiku назвать чат. Модель тут самая дешёвая нарочно:
// заголовок это украшение списка, и платить за него ярусом выше некому.
func (s *server) titleAsk(text string) string {
	if m := clientMissing(defaultClient); m != "" {
		return ""
	}
	prompt := titleMark + " Назови диалог заголовком в 5-7 слов по первой реплике человека. " +
		"Ответь только заголовком, без кавычек и пояснений. Реплика: " + truncate(text, 600)
	// Вызов служебный: заголовок это украшение списка, а не работа человека.
	// Хуки devkit на нём молчат по метке окружения, а транскрипт уезжает в свой
	// каталог вне проектов, чтобы не всплыть чатом (баг девятого круга POC).
	out, err := runProcQuiet(s.titleDir(), true, defaultClient, "-p", "--model", "haiku", prompt)
	if err != nil {
		return ""
	}
	said := strings.TrimSpace(string(out))
	if titleJunk(said) {
		return ""
	}
	return titleTrim(said)
}

// titleJunk узнаёт служебный ответ вместо заголовка. Клиент отвечает своим
// текстом и на отказ хука, и на несостоявшийся логин, а заголовок из такого
// ответа оседал в кеше навсегда и вставал в шапку чата («UserPromptSubmit
// operation blocked by hook»). Признаки грубые нарочно: заголовок это пять-семь
// слов одной строкой, и всё, что на него не похоже, лучше выбросить, оставшись
// с эвристикой.
func titleJunk(said string) bool {
	if said == "" {
		return true
	}
	if strings.Contains(said, "\n") {
		return true
	}
	low := strings.ToLower(said)
	for _, mark := range []string{
		"blocked by hook", "operation blocked", "not logged in", "please run /login",
		"userpromptsubmit", "pretooluse", "posttooluse", "invalid api key",
		"execution error", "traceback", "no such file",
	} {
		if strings.Contains(low, mark) {
			return true
		}
	}
	// Заголовок длиннее двух строк текста это уже не заголовок, а рассказ.
	return len([]rune(said)) > 200
}

// titleJobs держит счёт идущих суммаризаций: заголовок нужен списку, а не
// человеку прямо сейчас, и очередь на восемьдесят транскриптов сожгла бы
// квоту на украшение.
var titleJobs = make(chan struct{}, 1)

// titleAskLimit это сколько заголовков заказывается за один заход списка.
// Список открывают часто, и за несколько заходов свежие разговоры обрастают
// заголовками сами, без единого ожидания на экране.
const titleAskLimit = 2

// titleFor это одна лестница заголовка разговора на всех потребителей: список
// диалогов, раздел «Агенты», всякий следующий. Порядок от дешёвого к дорогому:
// summary самого харнеса, сохранённый заголовок, эвристика первого предложения
// на месте. Haiku зовётся фоном и правит эвристику к следующему заходу; ask
// говорит, можно ли его заказывать, потому что счёт заказов держит вызывающий.
// Второй такой лестницы заводить нельзя: разойдясь, они дали бы одному
// разговору два разных имени на соседних экранах (замечание 1 восьмого круга).
// Второй ответ говорит, ушёл ли заказ haiku: счёт заказов держит вызывающий, а
// знает про заказ только эта лестница.
func (s *server) titleFor(sid, summary, first string, ask bool) (string, bool) {
	if summary != "" {
		return summary, false
	}
	if sid != "" && chatKeyRe.MatchString(sid) {
		if st := s.chatStoreRead(sid); st.Title != "" {
			return st.Title, false
		}
	}
	said := titleTrim(first)
	if ask && first != "" && sid != "" && chatKeyRe.MatchString(sid) {
		s.titleOrder(sid, first)
		return said, true
	}
	return said, false
}

// titleOrder заказывает заголовок фоном. Заказ идёт по одному на машину:
// параллельные вызовы клиента стоят дороже, чем ожидание заголовка до
// следующего открытия экрана.
func (s *server) titleOrder(sid, text string) {
	go func() {
		select {
		case titleJobs <- struct{}{}:
		default:
			return
		}
		defer func() { <-titleJobs }()
		said := s.titleAsk(text)
		if said == "" {
			return
		}
		cur := s.chatStoreRead(sid)
		cur.Title = said
		s.chatStoreWrite(sid, cur)
		s.logf("заголовок чата %s назван haiku: %s", sid, said)
	}()
}

// titleFill дописывает заголовки списку диалогов той же лестницей. Счёт заказов
// держится тут: список приходит на восемьдесят транскриптов, и заказывать
// заголовок каждому значило бы сжечь квоту на украшение.
func (s *server) titleFill(list []chatEntry) {
	asked := 0
	for i := range list {
		e := &list[i]
		said, ordered := s.titleFor(e.ID, e.Summary, e.Title, asked < titleAskLimit)
		e.Title = said
		if ordered {
			asked++
		}
	}
}

// Вставка картинки в чат (замечание 4 двенадцатого круга POC). Бинарной
// передачи через сокет тут не заводится нарочно: канал живых сессий носит
// текст, а картинку агент читает сам, своим Read. Дашборд кладёт файл в свой
// каталог и дописывает к реплике ссылку на путь, то есть делает ровно то же,
// что человек, перетащивший файл в окно клиента.

// shotDir это каталог вложений чата: свой на сессию, чтобы файлы не смешивались
// и чтобы чат можно было вычистить целиком.
func (s *server) shotDir(sid string) string {
	return filepath.Join(s.cfg.Home, ".devkit", "uploads", sid)
}

// shotLimit держит вложение в берегах: скриншот экрана это единицы мегабайт, а
// всё, что больше, в реплику по ошибке.
const shotLimit = 12 << 20

var shotKind = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}

func (s *server) handleChatShot(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "вложение чата")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id чата", sid)})
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Data string `json:"data"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, shotLimit+(shotLimit/3))).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON {\"kind\": \"image/png\", \"data\": \"<base64>\"}"})
		return
	}
	ext, ok := shotKind[strings.TrimSpace(body.Kind)]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("вид %q не картинка: беру png, jpeg, gif, webp", body.Kind)})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "данные не разобрались как base64"})
		return
	}
	if len(raw) == 0 || len(raw) > shotLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("картинка пустая или длиннее предела %d МБ", shotLimit>>20)})
		return
	}
	dir := s.shotDir(sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("каталог вложений не создался: %v", err)})
		return
	}
	name := fmt.Sprintf("%s%s", s.now().Format("20060102T150405"), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("вложение не записалось: %v", err)})
		return
	}
	s.logf("вложение чата %s легло в %s (%d КБ)", sid, path, len(raw)/1024)
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "name": name, "bytes": len(raw)})
}

// handleChatShotGet отдаёт вложение чата картинкой: лента показывает миниатюру,
// а браузеру файл с диска иначе не достать. Путь проверяется по каталогу
// вложений, чтобы ручка не превратилась в чтение произвольного файла.
func (s *server) handleChatShotGet(w http.ResponseWriter, r *http.Request) {
	if s.findProject(w, r, "картинка чата") == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "битый id чата"})
		return
	}
	name := filepath.Base(r.URL.Query().Get("name"))
	if name == "." || name == string(filepath.Separator) || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "битое имя вложения"})
		return
	}
	path := filepath.Join(s.shotDir(sid), name)
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "вложения нет"})
		return
	}
	http.ServeFile(w, r, path)
}

// startFresh поднимает новый чат работы и отвечает тем же телом, что и
// продолжение: экрану всё равно, продолжили ему сессию или завели первую, ему
// нужен адрес, куда идти смотреть.
func (s *server) startFresh(w http.ResponseWriter, found *Project, id, text string) {
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	dir := found.Path
	if tree := filepath.Join(filepath.Dir(found.Path), filepath.Base(found.Path)+"-"+strings.ToLower(id)); isDir(tree) {
		dir = tree
	}
	model := chatModelDefault
	sess := chatNewName(id, tmuxAliveFn())
	s.chatStoreWrite("tmux-"+sess, chatStore{Model: model})
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, "", text, s.chatHarnessOf(model), binPath(agentctlBin))); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("tmux не поднял новый чат %s: %s", id, procErr(err))})
		return
	}
	s.logf("работа %s поднята новым чатом в tmux-сессии %s", id, sess)
	writeJSON(w, http.StatusOK, map[string]any{"task": id, "way": "fresh", "tmux": sess,
		"message": "чата не было: поднят новый в tmux-сессии " + sess})
}

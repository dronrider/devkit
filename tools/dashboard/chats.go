package main

import (
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

// chatModels это модели, которые панель предлагает списком. Список короткий
// нарочно: имена яруса берутся из раскладки подписок (agentctl harness), а тут
// стоят те три, которыми говорят с агентом руками.
var chatModels = []string{"opus", "sonnet", "haiku"}

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
}

// chatStoreDir это каталог с настройками диалогов: модель живёт файлом при
// диалоге, а не полем реестра, потому что реестр дописывается строками от
// нескольких писателей, и правка одного поля там стоила бы перезаписи журнала.
func chatStoreDir(home string) string {
	return filepath.Join(home, ".devkit", "chats")
}

type chatStore struct {
	Model string `json:"model,omitempty"`
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
	cutoff := s.now().Add(-sessionLiveTTL)
	out := []chatEntry{}
	for i, f := range sessionFiles(s.transcriptRoots(), projPath) {
		if limit > 0 && i >= limit {
			break
		}
		head := s.sessionHeadCached(f.path, f.stamp)
		last := sessions.Last(recs[f.ID])
		tasks := sessions.Touched(recs[f.ID])
		if id := taskIDInName(f.suffix); id != "" && !hasTask(tasks, id) {
			tasks = append([]string{id}, tasks...)
		}
		e := chatEntry{
			ID: f.ID, Title: head.First, Mtime: f.Mtime, Tasks: tasks,
			Tmux: last.Tmux, Tree: f.suffix, Branch: head.Branch,
			Harness: names[f.root],
			Model:   s.chatModel(f.ID, last.Tmux),
		}
		// Мера состояния: своё имя tmux-сессии старше свежести транскрипта.
		// Диалог, поднятый дашбордом, кончился ровно тогда, когда его сессии
		// нет в списке tmux, и свежий транскрипт тут ничего не значит: минуту
		// назад он писался живым процессом, которого уже нет. Окном vscode
		// считается только разговор, у которого своей tmux-сессии не было
		// вовсе, а транскрипт при этом свежий.
		switch {
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
	found := s.findProject(w, r, "разговоры")
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
	resp := map[string]any{"project": found.Name, "chats": list, "models": chatModels}
	if len(list) == 0 {
		resp["note"] = "разговоров тут пока нет: заведите новый кнопкой «+»"
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
func chatCmd(env, model, resume, text string) string {
	cmd := env + defaultClient + " --model " + shQuote(model)
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
	return env
}

// handleChatStart поднимает новый диалог: первая реплика человека и есть его
// начало, заголовок берётся из неё. Задача необязательна: разговор без задачи
// это обычное дело, и заводится он тем же порядком.
func (s *server) handleChatStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "подъём разговора")
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
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустая реплика разговора не поднимает: диалог начинается со слов человека"})
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
		s.logf("модель разговора %s не записалась: %v", sess, err)
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(id, sess), model, "", text)); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("подъём разговора в %s не удался: %s", found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.logf("разговор поднят в %s (tmux-сессия %s, модель %s, дерево %s)", found.Name, sess, model, dir)
	writeJSON(w, http.StatusOK, map[string]string{
		"tmux": sess, "model": model, "tree": dir,
		"message": fmt.Sprintf("разговор поднят в tmux-сессии %s моделью %s: ID сессии встанет в списке первым её ходом", sess, model)})
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
func chatSend(name, text string) error {
	if _, err := runProc("tmux", "send-keys", "-t", "="+name+":", "-l", text); err != nil {
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
	found := s.findProject(w, r, "реплика разговора")
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
	text := strings.Join(strings.Fields(body.Text), " ")
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
		s.logf("реплика подана в разговор %s (tmux-сессия %s)", sid, last.Tmux)
		writeJSON(w, http.StatusOK, map[string]any{"way": "send-keys", "tmux": last.Tmux,
			"message": "реплика подана прямо в процесс агента: ответ придёт в ленту"})
		return
	}
	// Своей tmux-сессии не было вовсе, а транскрипт свежий: разговор идёт в
	// чужом окне, и подать туда реплику нечем. Молчаливый резюм тут завёл бы
	// второго агента на том же дереве. Диалог с известным именем tmux-сессии
	// сюда не попадает: его сессии нет в списке, значит процесс кончился, и
	// свежесть транскрипта про это ничего не говорит.
	if last.Tmux == "" && s.now().Sub(info.mod) < sessionLiveTTL {
		writeJSON(w, http.StatusConflict, map[string]any{"way": chatVscode,
			"error": fmt.Sprintf(
				"разговор идёт в окне vscode (транскрипт писался %s назад, своей tmux-сессии у него нет): пишите там",
				s.now().Sub(info.mod).Truncate(time.Minute))})
		return
	}
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
		s.logf("настройки разговора %s не записались: %v", sess, err)
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		chatCmd(chatVars(task, sess), model, sid, text)); err != nil {
		msg := fmt.Sprintf("tmux не поднял продолжение разговора %s: %s", sid, procErr(err))
		s.logf("%s", msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": msg})
		return
	}
	// Имя tmux-сессии ложится в реестр на старый ID сразу: хук старта запишет
	// свою строку, когда клиент родится, а до того мера живости диалога уже
	// обязана работать, иначе вторая реплика подряд подняла бы второй резюм.
	sessions.Append(sessions.Path(s.cfg.Home),
		sessions.Line(s.now(), sid, sessions.Bind{Task: task, Source: "заказ",
			Project: found.Name, Tree: dir, Tmux: sess}, "резюм разговора"))
	s.logf("разговор %s продолжен резюмом в tmux-сессии %s (модель %s)", sid, sess, model)
	writeJSON(w, http.StatusOK, map[string]any{"way": "resume", "tmux": sess, "model": model,
		"message": fmt.Sprintf(
			"процесса у разговора не было: поднят claude --resume в tmux-сессии %s, история продолжена", sess)})
}

// handleChatModel меняет модель диалога. Смена действует на следующий подъём
// или резюм: у идущего процесса модель уже выбрана его запуском, и подменить
// её со стороны нечем.
func (s *server) handleChatModel(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "модель разговора")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !chatKeyRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id разговора", sid)})
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
	s.logf("модель разговора %s в %s теперь %s", sid, found.Name, model)
	writeJSON(w, http.StatusOK, map[string]string{"session": sid, "model": model,
		"message": fmt.Sprintf("модель разговора теперь %s: она возьмётся на следующем подъёме или резюме сессии", model)})
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

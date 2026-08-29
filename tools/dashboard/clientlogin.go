package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Вход в клиента с телефона (DK-577). Разлогиненный разговор дашборд узнаёт и
// чинит перезапуском (DK-466), но сам вход оставался ручным: плашка отправляла
// человека на машину делать /login в терминале, а с телефона хода нет вовсе.
// Здесь вход идёт отдельной одноразовой tmux-сессией: чужой транскрипт не
// засоряется диалогом входа, свежий токен ложится в связку ключей и достаётся
// каждому новому процессу, а живые разговоры поднимаются той же кнопкой
// перезапуска, что уже стоит на плашке.

// loginRun это состояние одноразовой сессии входа. Вход на машине один, и
// сессия тоже одна: вторая вкладка получает ту же ссылку, а не соседнюю
// сессию. Raising значит, что сессия поднята, но ссылка ещё ждётся.
type loginRun struct {
	Tmux    string
	URL     string
	Started time.Time
	Raising bool
}

// loginRunTTL это сколько живёт поднятая сессия входа без исхода. Клиент ждёт
// код минуты, а не часы, и забытая сессия не должна мешать следующей попытке.
const loginRunTTL = 10 * time.Minute

// loginSweepEvery это шаг фоновой уборки входа. Проверка дешёвая: пока срок
// не истёк, она смотрит только память процесса, до tmux дело не доходит.
const loginSweepEvery = time.Minute

// Ожидания входа. Поллинг панели стоит подпроцесса, поэтому шаг редкий, а в
// тестах стенд подменяет его на миллисекунды.
var (
	loginLinkWait   = 20 * time.Second
	loginCodeWait   = 20 * time.Second
	loginSettleWait = 2 * time.Second
	loginPollEvery  = 500 * time.Millisecond
)

// loginLinkRe узнаёт ссылку авторизации в панели входа. Клиент печатает её
// одной строкой, какой бы длинной та ни вышла (переносы пейна клеит loginPane),
// и первой такой строкой в одноразовой сессии бывает именно она: панели больше
// нечего показывать.
var loginLinkRe = regexp.MustCompile(`(?m)^[[:space:]]*(https://[^[:space:]]+)[[:space:]]*$`)

// loginLinkOf достаёт ссылку авторизации из снимка панели. Пустая строка
// значит, что ссылки в панели нет.
func loginLinkOf(pane string) string {
	if m := loginLinkRe.FindStringSubmatch(pane); m != nil {
		return m[1]
	}
	return ""
}

// loginCodeWords узнают поле, в котором клиент ждёт код авторизации. Своих кодов
// отказа клиент наружу не отдаёт, и мера тут по его же строке приглашения.
var loginCodeWords = []string{"authorization code", "paste the code", "enter the code"}

// loginWantsCode отвечает, ждёт ли панель ввода кода.
func loginWantsCode(pane string) bool {
	low := strings.ToLower(pane)
	for _, word := range loginCodeWords {
		if strings.Contains(low, word) {
			return true
		}
	}
	return false
}

// loginRejectWords узнают отказ кода: клиент перерисовывает панель и печатает
// свою строку про неверный код рядом с новым полем.
var loginRejectWords = []string{"invalid", "incorrect", "try again", "expired"}

// loginSaysRejected отвечает, сказал ли клиент, что код не принят.
func loginSaysRejected(pane string) bool {
	low := strings.ToLower(pane)
	for _, word := range loginRejectWords {
		if strings.Contains(low, word) {
			return true
		}
	}
	return false
}

// loginSessName подбирает свободное имя одноразовой сессии входа. Имена свои,
// отдельные от чатов: список разговоров и реестр сессий не должны считать
// сессию входа чьим-то разговором.
func loginSessName(alive func(string) bool) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("login-%d", n)
		if !alive(name) {
			return name
		}
	}
}

// loginPane снимает панель сессии входа клеем переносов. Ссылка авторизации
// длиннее пейна, tmux рвёт её по ширине без пробела на стыке, и без -J разбор
// забрал бы первый обрывок как готовую ссылку. Ошибка значит, что сессии уже
// нет.
func loginPane(name string) (string, error) {
	out, err := runProc("tmux", "capture-pane", "-J", "-p", "-t", "="+name+":")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// loginDrop снимает сессию входа и забывает её состояние. Сессия одноразовая:
// после успеха, отказа и забвения по сроку ей стоять нечего.
func (s *server) loginDrop(run *loginRun, why string) {
	if why != "" {
		s.logf("сессия входа %s снята: %s", run.Tmux, why)
	}
	runProc("tmux", "kill-session", "-t", "="+run.Tmux)
	s.mu.Lock()
	if s.loginRun == run {
		s.loginRun = nil
	}
	s.mu.Unlock()
}

// loginSweep снимает просроченную сессию входа. Срок раньше проверялся только
// лениво, при следующем нажатии «Войти», и человек, открывший ссылку и не
// вернувшийся, оставлял tmux с живым клиентом стоять бессрочно.
func (s *server) loginSweep() {
	s.mu.Lock()
	run := s.loginRun
	s.mu.Unlock()
	if run == nil || s.now().Sub(run.Started) < loginRunTTL {
		return
	}
	s.loginDrop(run, "срок истёк: к ссылке не вернулись")
}

// loginKeeper снимает просроченные сессии входа по кругу, пока жив демон.
// Вызов служебный: журнальная строка одна на снятие, ленту он не наполняет.
func (s *server) loginKeeper(stop <-chan struct{}) {
	t := time.NewTicker(loginSweepEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.loginSweep()
		}
	}
}

// handleClientLogin поднимает вход клиента и отдаёт ссылку авторизации.
// Ручка зовётся с плашки разлогина и занимает до loginLinkWait: пока клиент
// печатает ссылку, запрос стоит на поллинге панели.
func (s *server) handleClientLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "подъём входа клиента")
	if found == nil {
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if m := clientMissing(defaultClient); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	s.mu.Lock()
	run := s.loginRun
	s.mu.Unlock()
	if run != nil {
		if s.now().Sub(run.Started) < loginRunTTL {
			if _, err := loginPane(run.Tmux); err == nil {
				if run.URL != "" {
					s.logf("вход клиента в %s: ссылка отдана повторно", found.Name)
					writeJSON(w, http.StatusOK, map[string]string{
						"tmux": run.Tmux, "url": run.URL,
						"message": "вход уже поднят: откройте ссылку и введите код"})
					return
				}
				if run.Raising {
					writeJSON(w, http.StatusConflict, map[string]string{
						"error": "вход уже поднимается: подождите и нажмите кнопку снова"})
					return
				}
			}
		}
		// Прежняя сессия мертва или просрочена: снятия она уже не стоит,
		// состояние забывается молча.
		s.loginDrop(run, "")
	}
	sess := loginSessName(tmuxAliveFn())
	// Каталог входа это дом машины, а не проект: вход не принадлежит разговору,
	// токен лежит в связке ключей машины, и REPL клиента в проекте ему не нужен.
	dir := realHomeOr(s.cfg.Home)
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", dir,
		s.launchEnv("", sess)+" "+defaultClient); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию входа %s: %s", sess, procErr(err))
		s.logf("подъём входа клиента в %s не удался: %s", found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	if err := tmuxAnswerText("="+sess+":", "/login"); err != nil {
		runProc("tmux", "kill-session", "-t", "="+sess)
		text := fmt.Sprintf("команда /login не подалась в сессию входа %s: %s", sess, procErr(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.mu.Lock()
	s.loginRun = &loginRun{Tmux: sess, Started: s.now(), Raising: true}
	s.mu.Unlock()
	url, fail := s.loginAwaitLink(sess)
	if fail != "" {
		s.mu.Lock()
		run = s.loginRun
		s.mu.Unlock()
		if run != nil {
			s.loginDrop(run, fail)
		}
		s.logf("вход клиента в %s не дал ссылки: %s", found.Name, fail)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fail})
		return
	}
	s.mu.Lock()
	if s.loginRun != nil && s.loginRun.Tmux == sess {
		s.loginRun.URL = url
		s.loginRun.Raising = false
	}
	s.mu.Unlock()
	s.logf("вход клиента поднят в %s (tmux-сессия %s), ссылка авторизации отдана",
		found.Name, sess)
	writeJSON(w, http.StatusOK, map[string]string{"tmux": sess, "url": url,
		"message": "откройте ссылку, войдите и введите код в поле на плашке"})
}

// loginAwaitLink ждёт от панели ссылку авторизации. До ссылки клиент стоит на
// выборе способа входа, и выбор проходится за человека: подписка по умолчанию
// это первый пункт, стоящий на нём курсором. Пустая вторая строка значит успех.
func (s *server) loginAwaitLink(sess string) (string, string) {
	deadline := s.now().Add(loginLinkWait)
	chosen := false
	for {
		pane, err := loginPane(sess)
		if err != nil {
			return "", fmt.Sprintf("сессия входа умерла, не дойдя до ссылки авторизации: %s", procErr(err))
		}
		if url := loginLinkOf(pane); url != "" {
			return url, ""
		}
		if !chosen {
			if ask := tmuxAskOf(sess); len(ask.Options) > 0 {
				if err := tmuxAnswer(sess, ask, loginPickOf(ask), ""); err != nil {
					return "", fmt.Sprintf("способ входа не выбрался: %s", procErr(err))
				}
				chosen = true
			}
		}
		if !s.now().Before(deadline) {
			return "", fmt.Sprintf("клиент не напечатал ссылку авторизации за %s: "+
				"вид панели входа, видимо, сменился, разбор надо чинить", loginLinkWait)
		}
		time.Sleep(loginPollEvery)
	}
}

// loginPickOf выбирает пункт способа входа: со словом subscription, а нет его,
// первый. Подписка по умолчанию у клиента в списке первая, и слова её называют.
func loginPickOf(ask tmuxAsk) int {
	for i, o := range ask.Options {
		if strings.Contains(strings.ToLower(o.Text), "subscription") {
			return i + 1
		}
	}
	return 1
}

// handleClientLoginCode принимает код авторизации и отдаёт его клиенту
// нажатиями. Код это одноразовый ключ учётной записи, и в журнал он не пишется:
// ни в журнал дашборда, ни в ленту разговора, поле на плашке после отправки
// очищается, а сам код уезжает только в сессию входа.
func (s *server) handleClientLoginCode(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "отправка кода входа клиента")
	if found == nil {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if r.Body != nil {
		json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body)
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустой код отправлять нечего: код печатает страница авторизации"})
		return
	}
	s.mu.Lock()
	run := s.loginRun
	s.mu.Unlock()
	if run == nil || run.URL == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "вход ещё не поднят: сперва нажмите кнопку входа на плашке разлогина"})
		return
	}
	if err := tmuxAnswerText("="+run.Tmux+":", code); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("код не подался в сессию входа %s: %s", run.Tmux, procErr(err))})
		return
	}
	kind, words := s.loginAwaitCode(run.Tmux)
	switch kind {
	case "ok":
		s.loginDrop(run, "вход сделан")
		s.logf("код входа принят в %s: токен у клиента в связке ключей", found.Name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true,
			"message": "вход сделан: свежий токен лёг в связку ключей, " +
				"перезапустите разговор кнопкой на плашке"})
	case "again":
		// Сессия входа жива и снова ждёт код: человек может ввести другой.
		s.logf("код входа отклонён клиентом в %s", found.Name)
		writeJSON(w, http.StatusConflict, map[string]string{"error": words})
	default:
		s.loginDrop(run, words)
		s.logf("вход клиента оборвался в %s: %s", found.Name, words)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": words})
	}
}

// loginAwaitCode узнаёт исход отправленного кода по панели. Поле кода,
// пропавшее и не вернувшееся за loginSettleWait, это успех: клиент
// перерисовывает панель, и мера не должна читать мигание как исход. Поле,
// вернувшееся со словами отклонения, это отказ кода, а молчащее поле до
// таймаута это тишина клиента. Ошибка снимка значит, что сессия умерла.
func (s *server) loginAwaitCode(sess string) (string, string) {
	deadline := s.now().Add(loginCodeWait)
	for {
		pane, err := loginPane(sess)
		if err != nil {
			return "gone", fmt.Sprintf("сессия входа умерла на середине входа: %s", procErr(err))
		}
		if !loginWantsCode(pane) {
			time.Sleep(loginSettleWait)
			pane, err = loginPane(sess)
			if err != nil {
				return "gone", fmt.Sprintf("сессия входа умерла на середине входа: %s", procErr(err))
			}
			if loginWantsCode(pane) && loginSaysRejected(pane) {
				return "again", "код не принят: клиент снова ждёт код авторизации, введите другой"
			}
			if loginWantsCode(pane) {
				return "again", "клиент вернулся к полю кода без слов: введите код заново"
			}
			return "ok", ""
		}
		if loginSaysRejected(pane) {
			return "again", "код не принят: клиент снова ждёт код авторизации, введите другой"
		}
		if !s.now().Before(deadline) {
			return "again", fmt.Sprintf("клиент не ответил на код за %s: панель всё ещё ждёт его, "+
				"введите код заново", loginCodeWait)
		}
		time.Sleep(loginPollEvery)
	}
}

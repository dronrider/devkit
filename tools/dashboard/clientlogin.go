package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"regexp"
	"sort"
	"strconv"
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
	Tmux string
	URL  string
	// Alive это последний признак жизни входа: момент подъёма, а дальше каждый
	// заход человека по ручкам входа. Срок уборки считается от него, а не от
	// подъёма: человек, взявший ссылку заново или промахнувшийся кодом, никуда
	// не делся, и отсчёт ему идёт с этой минуты.
	Alive   time.Time
	Raising bool
}

// loginRunTTL это сколько живёт поднятая сессия входа без признаков жизни:
// заходов человека по ручкам входа. Срок мерян по самой долгой честной дороге,
// а не по терпению разработчика за своей машиной: со ссылкой человек уходит на
// телефон, там его ждут страница входа, пароль из менеджера и второй фактор из
// другого приложения, и полчаса на это уходит без всякой забывчивости. Забытую
// сессию тот же срок снимает: следующей попытке она мешать не должна.
const loginRunTTL = 30 * time.Minute

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

// loginLinkRe узнаёт первую строку ссылки авторизации: строка, где кроме ссылки
// нет ничего. Первой такой строкой в одноразовой сессии бывает именно она:
// панели больше нечего показывать.
var loginLinkRe = regexp.MustCompile(`^[[:space:]]*(https://[^[:space:]]+)[[:space:]]*$`)

// loginURLRunes узнаёт продолжение ссылки: строка целиком из знаков, которые в
// URL бывают. Ссылка авторизации длиннее пейна, и клиент рвёт её сам, своими
// переводами строк, а не мягким переносом терминала, поэтому клеить обрывки
// приходится разбору, а не capture-pane (живая проверка: четыре обрывка без
// пробела на стыке). Слова вокруг ссылки всегда несут пробел и сюда не попадают.
var loginURLRunes = regexp.MustCompile(`^[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+$`)

// loginSchemes называет схемы чужой ссылки в обрывке. Внутри настоящей ссылки
// своей полной схемы не встречается: вложенные адреса клиент кодирует
// процентами, и строка, начинающаяся схемой, это соседняя ссылка, а не кусок
// найденной.
var loginSchemes = []string{"https://", "http://"}

// loginSibling отвечает, начинает ли строка соседнюю ссылку, а не продолжает
// найденную.
func loginSibling(line string) bool {
	for _, sch := range loginSchemes {
		if strings.HasPrefix(line, sch) {
			return true
		}
	}
	return false
}

// loginLinkMax ограничивает сборку обрывков сверху: живая ссылка входа это
// сотни знаков, а разросшаяся за тысячи значит склейку чужих строк.
const loginLinkMax = 2048

// loginLinkOf достаёт ссылку авторизации из снимка панели и собирает её из
// обрывков по ширине пейна. Родство обрывка проверяется, а не предполагается
// по одним URL-знакам: клиент рвёт ссылку в одной и той же колонке пейна, и
// обрывок узнаётся по ней, а не похожестью. Границ три. Чужая схема в начале
// строки. Строка шире колонки разрыва. И конец: закончившуюся ссылку клиент
// отбивает пустой строкой, поэтому склейка любой ширины, за которой пустой
// строки не видно, не отдаётся вовсе. Оборванная склейка молчит, а не отдаёт
// первый кусок: неполная ссылка на вид такая же живая, как целая, и человек
// узнал бы о подмене, только не сумев открыть её. Молчание тут дешевле,
// поллинг подъёма спросит панель снова через полсекунды.
// Пустая строка значит, что ссылки в панели нет.
func loginLinkOf(pane string) string {
	lines := strings.Split(pane, "\n")
	for i, ln := range lines {
		m := loginLinkRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		url := m[1]
		torn := len(m[1])
		glued, ended := false, false
		for j := i + 1; j < len(lines); j++ {
			piece := strings.TrimSpace(lines[j])
			if piece == "" {
				ended = true
				break
			}
			if !loginURLRunes.MatchString(piece) || loginSibling(piece) ||
				len(piece) > torn || len(url)+len(piece) > loginLinkMax {
				break
			}
			if len(piece) < torn {
				// Обрывок короче колонки разрыва последний. Обрывком он
				// считается, только когда за ним стоит пустая строка; иначе
				// это чужая строка, а ссылка кончилась перед ней.
				if j+1 < len(lines) && strings.TrimSpace(lines[j+1]) == "" {
					url += piece
					glued, ended = true, true
				}
				break
			}
			url, glued = url+piece, true
		}
		if glued && !ended {
			// Обрывки склеились, а конца ссылки в панели не видно: либо она
			// ещё дорисовывается, либо следом идёт чужая строка той же
			// ширины. Отличить их видом нельзя, и склейка не отдаётся.
			return ""
		}
		return loginLinkCheck(url)
	}
	return ""
}

// loginLinkCheck меряет собранную ссылку перед отдачей: разбор обязан увидеть
// один адрес, а не кашу из склеенных строк. Вторая схема внутри или адрес
// без хоста значит, что родство не поймано, и честнее не найти ссылку вовсе,
// чем отдать человеку ссылку, которая не открывается.
func loginLinkCheck(url string) string {
	if len(url) > loginLinkMax {
		return ""
	}
	schemes := 0
	for _, sch := range loginSchemes {
		schemes += strings.Count(url, sch)
	}
	if schemes != 1 {
		return ""
	}
	u, err := neturl.Parse(url)
	if err != nil || u.Host == "" {
		return ""
	}
	return url
}

// loginCodeWords узнают поле, в котором клиент ждёт код авторизации. Своих кодов
// отказа клиент наружу не отдаёт, и мера тут по его же строке приглашения.
var loginCodeWords = []string{"authorization code", "paste code", "enter the code"}

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

// loginFailWords узнают отказ входа словами самого клиента. Список нарочно
// широк: цена пропуска отказа это доклад «вход сделан» там, где входа не было.
var loginFailWords = []string{"oauth error", "error", "failed", "invalid",
	"incorrect", "expired", "denied", "unauthorized"}

// loginSaysFailed отдаёт строку клиента про отказ. Пустая строка значит, что
// отказа в панели не видно. Строка нужна целиком: человеку разбираться с
// «Request failed with status code 400», а наш пересказ отнял бы у него разбор.
func loginSaysFailed(pane string) string {
	for _, ln := range strings.Split(pane, "\n") {
		line := loginPlainLine(ln)
		low := strings.ToLower(line)
		for _, word := range loginFailWords {
			if strings.Contains(low, word) {
				return line
			}
		}
	}
	return ""
}

// loginScreenWords узнают, что панель всё ещё держит экран входа. Экран входа
// после кода значит, что вход не кончился ничем понятным: успех уводит клиента
// обратно к работе, а не оставляет на своих подсказках.
var loginScreenWords = []string{"esc to cancel", "press enter to retry",
	"login method", "browser didn't open"}

// loginScreenUp отвечает, стоит ли на панели экран входа клиента.
func loginScreenUp(pane string) bool {
	low := strings.ToLower(pane)
	for _, word := range loginScreenWords {
		if strings.Contains(low, word) {
			return true
		}
	}
	return false
}

// loginPlainLine чистит строку панели от рамки и курсора: наружу идут слова
// клиента, а не его рисунок.
func loginPlainLine(ln string) string {
	line := strings.Map(func(r rune) rune {
		if r < 0x2000 {
			return r
		}
		return ' '
	}, ln)
	return strings.TrimSpace(line)
}

// loginLastWords достаёт последнюю живую строку панели. Она едет человеку,
// когда исход не узнан: пусть решает он, чем дашборд соврёт про успех.
func loginLastWords(pane string) string {
	lines := strings.Split(pane, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := loginPlainLine(lines[i])
		if line == "" || !strings.ContainsFunc(line, func(r rune) bool {
			return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		}) {
			continue
		}
		if len(line) > 160 {
			line = line[:160]
		}
		return line
	}
	return "панель пуста"
}

// loginFwdHeads это заголовки, которыми посредник признаётся посредником.
var loginFwdHeads = []string{"X-Forwarded-Host", "X-Forwarded-For", "Forwarded"}

// loginLoopback отвечает, петлевой ли это адрес. Имя localhost считается своим
// наравне с числом: браузер на машине ходит и так, и так.
func loginLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loginFromMachine отвечает, открыт ли дашборд браузером самой машины. Вопрос
// в том, дотянется ли браузер до петлевого адреса клиента, и одного адреса
// соединения тут мало. Заход извне приходит на ту же машину через клиента шар,
// и петлевым выглядит тоже (README, «Заход извне»): телефон получил бы режим
// без кода и ссылку на localhost, которого у него нет, то есть ровно тот путь,
// ради которого задача заведена, и ломался бы (замечание ревью). Посредник
// узнаётся по своим заголовкам, а имя, по которому пришёл браузер, обязано
// быть петлевым: сверка та же, что у sameOrigin, и берётся тем же
// externalHost. Сомнение тут решается в пользу кода: лишний код человек
// наберёт, а ссылка в никуда останавливает вход совсем.
func loginFromMachine(r *http.Request) bool {
	if !loginLoopback(r.RemoteAddr) {
		return false
	}
	for _, head := range loginFwdHeads {
		if r.Header.Get(head) != "" {
			return false
		}
	}
	return loginLoopback(externalHost(r))
}

// loginLsof называет утилиты, которыми ищется порт клиента. Полный путь стоит
// первым: служба поднята launchd, а его PATH каталога /usr/sbin не несёт.
var loginLsof = []string{"/usr/sbin/lsof", "lsof"}

// loginPortRe вытаскивает порт из строки lsof вида «TCP 127.0.0.1:53535 (LISTEN)».
var loginPortRe = regexp.MustCompile(`127\.0\.0\.1:([0-9]+) \(LISTEN\)`)

// loginLocalPort находит порт, на котором клиент ждёт возврата браузера. Пока
// вход поднят, клиент держит петлевой слушатель и сам ловит код: проверено
// живьём, его /callback отвечает на чужой state отказом 400. Ноль значит, что
// петли не нашлось, и вход пойдёт кодом.
func loginLocalPort(sess string) int {
	out, err := runProc("tmux", "list-panes", "-t", "="+sess+":", "-F", "#{pane_pid}")
	if err != nil {
		return 0
	}
	pid := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if pid == "" {
		return 0
	}
	for _, name := range loginLsof {
		out, err := runProc(name, "-nP", "-iTCP", "-sTCP:LISTEN", "-a", "-p", pid)
		if err != nil {
			continue
		}
		if m := loginPortRe.FindSubmatch(out); m != nil {
			port, err := strconv.Atoi(string(m[1]))
			if err == nil {
				return port
			}
		}
	}
	return 0
}

// loginLoopURL перекладывает ссылку авторизации на возврат в самого клиента.
// Клиент печатает ссылку ручного вида: code=true и страница-посредник, которая
// показывает код человеку. Петлевой вид той же ссылки ведёт браузер обратно в
// клиент, и код никто не набирает. Всё, что вход связывает (client_id, state,
// code_challenge), остаётся клиентово: подменяется один адрес возврата.
func loginLoopURL(raw string, port int) string {
	u, err := neturl.Parse(raw)
	if err != nil || port == 0 {
		return ""
	}
	q := u.Query()
	q.Del("code")
	q.Set("redirect_uri", fmt.Sprintf("http://localhost:%d/callback", port))
	u.RawQuery = q.Encode()
	return u.String()
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
	// Снимается только своё. Имя login-N могло уйти чужой сессии, пока наша
	// умирала, и снятие по одному имени убило бы соседа.
	if s.loginOwns(run.Tmux) {
		if why != "" {
			s.logf("сессия входа %s снята: %s", run.Tmux, why)
		}
		runProc("tmux", "kill-session", "-t", "="+run.Tmux)
	} else if why != "" {
		s.logf("сессия входа %s забыта без снятия, имя занято не нами: %s", run.Tmux, why)
	}
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
	if run == nil {
		// Память пуста, а на машине могли остаться сессии входа без учёта:
		// перезапуск службы стирает состояние из памяти, и уборка заодно
		// узнаёт живые сессии и снимает сирот.
		s.loginRecover()
		return
	}
	if s.now().Sub(run.Alive) < loginRunTTL {
		return
	}
	s.loginDrop(run, "срок истёк: к ссылке не вернулись")
}

// loginNameRe узнаёт имя сессии входа в списке tmux: имена входа отдельны от
// разговоров, и принадлежат только входу.
var loginNameRe = regexp.MustCompile(`^login-[0-9]+$`)

// loginOwnerVar это переменная окружения tmux-сессии, которой дашборд метит
// поднятый им вход. Имя login-N и вид панели о происхождении сессии не говорят
// ничего: имя свободно берёт кто угодно, а адрес в панели бывает любой. Метка
// же ставится тем же вызовом, что поднимает сессию, и живёт ровно столько,
// сколько сессия. По ней узнавание после перезапуска отличает свой вход от
// соседнего, и это не косметика: в узнанную сессию уходит код авторизации
// нажатиями, а он одноразовый ключ от учётной записи.
const loginOwnerVar = "DEVKIT_LOGIN_OWNER"

// loginMark это метка владельца. Она обязана пережить перезапуск службы и
// отличать один экземпляр дашборда от другого на той же машине: путь конфига
// даёт и то, и другое, а два экземпляра (боевой и POC) на одном tmux-сервере
// живут рядом каждый день.
func (s *server) loginMark() string {
	return s.cfg.Path
}

// loginStamp метит сессию входа сразу после подъёма.
func (s *server) loginStamp(sess string) error {
	_, err := runProc("tmux", "set-environment", "-t", "="+sess,
		loginOwnerVar, s.loginMark())
	return err
}

// loginOwns отвечает, наша ли это сессия входа. Чужая и безымянная равно не
// наши: в первую нельзя слать код, вторую нельзя снимать.
func (s *server) loginOwns(sess string) bool {
	out, err := runProc("tmux", "show-environment", "-t", "="+sess, loginOwnerVar)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == loginOwnerVar+"="+s.loginMark()
}

// loginRecover узнаёт сессии входа, оставшиеся без учёта. Состояние подъёма
// живёт в памяти процесса и умирает перезапуском службы, а это штатный шаг
// выката, тогда как tmux-сессия входа остаётся на машине. Разбирается только
// помеченное этим экземпляром: чужой login-N не усыновляется и не снимается,
// его дело чужое. Своя сессия, которая моложе срока и несёт в панели ссылку,
// поднимается в память заново, срок считается от её рождения. Свои прочие
// снимаются как сироты: вставшие на полпути никто не доведёт, а человеку проще
// нажать «Войти» снова. Вызов идёт из уборки и перед подъёмом нового входа:
// вторая сессия не заводится, пока жива первая.
func (s *server) loginRecover() {
	if m := tmuxMissingCheck(); m != "" {
		return
	}
	sessions := tmuxList()
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].Created > sessions[j].Created
	})
	for _, sess := range sessions {
		if !loginNameRe.MatchString(sess.Name) || !s.loginOwns(sess.Name) {
			continue
		}
		born := time.Unix(sess.Created, 0)
		pane, err := loginPane(sess.Name)
		s.mu.Lock()
		taken := s.loginRun != nil
		s.mu.Unlock()
		if !taken && err == nil && s.now().Sub(born) < loginRunTTL {
			if url := loginLinkOf(pane); url != "" {
				s.mu.Lock()
				if s.loginRun == nil {
					s.loginRun = &loginRun{Tmux: sess.Name, URL: url, Alive: born}
					s.mu.Unlock()
					s.logf("сессия входа %s узнана после перезапуска службы", sess.Name)
					continue
				}
				s.mu.Unlock()
			}
		}
		s.logf("сессия входа %s снята: осталась без учёта после перезапуска службы", sess.Name)
		runProc("tmux", "kill-session", "-t", "="+sess.Name)
	}
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

// loginAnswer отдаёт экрану ссылку и дорогу входа. Дорог две. С самой машины
// браузер возвращается в клиент петлёй, и человек не набирает ничего: шаг один,
// открыть ссылку. С другого устройства петля ведёт в никуда (адрес возврата
// указывает на сам телефон), и код остаётся единственной дорогой.
func (s *server) loginAnswer(w http.ResponseWriter, r *http.Request, run *loginRun) {
	url, way := run.URL, "code"
	message := "откройте ссылку, войдите и введите код в поле на плашке"
	if loginFromMachine(r) {
		if loop := loginLoopURL(run.URL, loginLocalPort(run.Tmux)); loop != "" {
			url, way = loop, "local"
			message = "откройте ссылку и войдите: код клиент возьмёт сам"
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"tmux": run.Tmux, "url": url, "way": way, "message": message})
}

// handleClientLoginWait ждёт исхода входа, который идёт петлёй. Кода тут нет,
// и ждать нечего, кроме самого клиента: экран зовёт ручку по кругу, пока она
// не назовёт исход. Ожидание это признак жизни входа.
func (s *server) handleClientLoginWait(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "ожидание входа клиента")
	if found == nil {
		return
	}
	s.mu.Lock()
	run := s.loginRun
	s.mu.Unlock()
	if run == nil || run.URL == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "вход ещё не поднят: сперва нажмите кнопку входа"})
		return
	}
	if !s.loginOwns(run.Tmux) {
		s.mu.Lock()
		if s.loginRun == run {
			s.loginRun = nil
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("сессия входа %s сменилась под своим именем: поднимите вход заново", run.Tmux)})
		return
	}
	s.mu.Lock()
	run.Alive = s.now()
	s.mu.Unlock()
	kind, words := s.loginAwaitCode(run.Tmux, false)
	switch kind {
	case "waiting":
		writeJSON(w, http.StatusAccepted, map[string]any{"waiting": true,
			"message": "вход ещё идёт: пройдите его в открывшейся вкладке"})
	case "ok":
		s.loginDrop(run, "вход сделан")
		s.logf("вход клиента сделан петлёй в %s: токен у клиента в связке ключей", found.Name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true,
			"message": "вход сделан: свежий токен лёг в связку ключей"})
	case "fail":
		s.loginDrop(run, "клиент отверг вход")
		s.logf("вход клиента отвергнут в %s: %s", found.Name, words)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("вход не прошёл: %s. Начните вход заново.", words)})
	case "stuck":
		s.logf("исход входа не узнан в %s: %s", found.Name, words)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("клиент кончил вход не тем, чего ждёт дашборд. "+
				"Последнее, что он сказал: «%s». Вход не сделан.", words)})
	default:
		s.loginDrop(run, words)
		s.logf("вход клиента оборвался в %s: %s", found.Name, words)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": words})
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
	if run == nil {
		// Память пуста (перезапуск службы), а сессия входа могла остаться на
		// машине: прежде чем поднимать новую, узнаётся живая.
		s.loginRecover()
		s.mu.Lock()
		run = s.loginRun
		s.mu.Unlock()
	}
	if run != nil {
		if s.now().Sub(run.Alive) < loginRunTTL {
			if _, err := loginPane(run.Tmux); err == nil {
				if run.URL != "" {
					// Повторный заход это признак жизни: срок входа считается
					// от него, а не от подъёма.
					s.mu.Lock()
					run.Alive = s.now()
					s.mu.Unlock()
					s.logf("вход клиента в %s: ссылка отдана повторно", found.Name)
					s.loginAnswer(w, r, run)
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
	if err := s.loginStamp(sess); err != nil {
		// Без метки сессия ничья: после перезапуска её не узнать и не снять,
		// а код авторизации ушёл бы в сессию, о которой известно одно имя.
		runProc("tmux", "kill-session", "-t", "="+sess)
		text := fmt.Sprintf("сессия входа %s не пометилась владельцем: %s", sess, procErr(err))
		s.logf("подъём входа клиента в %s не удался: %s", found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.mu.Lock()
	s.loginRun = &loginRun{Tmux: sess, Alive: s.now(), Raising: true}
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
	s.mu.Lock()
	run = s.loginRun
	s.mu.Unlock()
	if run == nil {
		run = &loginRun{Tmux: sess, URL: url}
	}
	s.loginAnswer(w, r, run)
}

// loginAwaitLink ждёт от панели ссылку авторизации. До ссылки клиент стоит на
// вопросах, и они проходятся за человека: вопрос доверия каталогу отвечается
// пунктом доверия (его поднимает машина, где дом ещё не доверен клиенту, а
// команда /login уже съедена этим вопросом и подаётся заново), выбор способа
// входа берёт подписку по умолчанию, первый пункт. Команда /login подаётся лишь
// на нарисованную панель: пока клиент разгоняется, панель пуста, и нажатия,
// поданные вслепую, съедает первый вставший виджет, а Enter в нём подтверждает
// пункт на курсоре и губит клиента. Съеденной бывает и поданная вовремя: после
// ответа о доверии клиент поднимает REPL заново и вычищает ввод, поэтому команда
// повторяется с отступом, пока панель не отзовётся виджетом выбора способа.
// Пустая вторая строка значит успех.
func (s *server) loginAwaitLink(sess string) (string, string) {
	deadline := s.now().Add(loginLinkWait)
	chosen, trusted := false, false
	var sent time.Time
	for {
		pane, err := loginPane(sess)
		if err != nil {
			return "", fmt.Sprintf("сессия входа умерла, не дойдя до ссылки авторизации: %s", procErr(err))
		}
		if url := loginLinkOf(pane); url != "" {
			return url, ""
		}
		ask := tmuxAskOf(sess)
		switch {
		case loginAskIsTrust(ask) && !trusted:
			if err := tmuxAnswer(sess, ask, loginPickOf(ask), ""); err != nil {
				return "", fmt.Sprintf("вопрос доверия каталогу не отвечен: %s", procErr(err))
			}
			trusted = true
		case len(ask.Options) > 0 && !chosen && !loginAskIsTrust(ask):
			if err := tmuxAnswer(sess, ask, loginPickOf(ask), ""); err != nil {
				return "", fmt.Sprintf("способ входа не выбрался: %s", procErr(err))
			}
			chosen = true
		case strings.TrimSpace(pane) == "":
			// Клиент ещё не нарисовал панель: нажатия вслепую не подаются.
		case len(ask.Options) == 0 && (sent.IsZero() || s.now().Sub(sent) >= 2*loginPollEvery):
			if err := tmuxAnswerText("="+sess+":", "/login"); err != nil {
				return "", fmt.Sprintf("команда /login не подалась в сессию входа: %s", procErr(err))
			}
			sent = s.now()
		}
		if !s.now().Before(deadline) {
			return "", fmt.Sprintf("клиент не напечатал ссылку авторизации за %s: "+
				"вид панели входа, видимо, сменился, разбор надо чинить", loginLinkWait)
		}
		time.Sleep(loginPollEvery)
	}
}

// loginAskIsTrust узнаёт вопрос доверия каталогу. Он встаёт первым на машине,
// где клиент ещё не доверял свой дом: до ответа REPL не поднимется, а пункт,
// стоящий на курсоре, это выход.
func loginAskIsTrust(ask tmuxAsk) bool {
	for _, o := range ask.Options {
		if strings.Contains(strings.ToLower(o.Text), "trust") {
			return true
		}
	}
	return false
}

// loginPickOf выбирает пункт виджета входа. Способ входа узнаётся словом
// subscription, а нет его, берётся первый: подписка по умолчанию у клиента в
// списке первая, и слова её называют. Вопрос доверия отвечается пунктом
// доверия: стоящий на курсоре пункт это «No, exit».
func loginPickOf(ask tmuxAsk) int {
	for i, o := range ask.Options {
		low := strings.ToLower(o.Text)
		if strings.Contains(low, "subscription") || strings.Contains(low, "trust") {
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
	// Метка сессии проверяется перед самой подачей, а не только при узнавании.
	// Имя login-N держится в памяти с подъёма, и за это время сессия могла
	// умереть, а имя достаться соседу. Код авторизации одноразовый ключ от
	// учётной записи, и нажатия в чужую панель ему дорога в один конец.
	if !s.loginOwns(run.Tmux) {
		s.mu.Lock()
		if s.loginRun == run {
			s.loginRun = nil
		}
		s.mu.Unlock()
		s.logf("код входа не подан: имя %s занято сессией не нашего подъёма", run.Tmux)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("сессия входа %s сменилась под своим именем: поднимите вход заново", run.Tmux)})
		return
	}
	// Попытка кода это признак жизни: даже неверный код значит, что человек
	// дошёл до поля и пробует, и срок входа считается от попытки.
	s.mu.Lock()
	run.Alive = s.now()
	s.mu.Unlock()
	if err := tmuxAnswerText("="+run.Tmux+":", code); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("код не подался в сессию входа %s: %s", run.Tmux, procErr(err))})
		return
	}
	kind, words := s.loginAwaitCode(run.Tmux, true)
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
	case "fail":
		// Клиент назвал отказ своими словами, и они едут человеку целиком.
		// Ссылка после отказа мертва: сессия снимается, следующий заход берёт
		// свежую.
		s.loginDrop(run, "клиент отверг код")
		s.logf("код входа отвергнут клиентом в %s: %s", found.Name, words)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("клиент отверг код: %s. Ссылка больше не годится, "+
				"начните вход заново.", words)})
	case "stuck":
		// Исход не узнан. Сессия остаётся стоять: успехом это звать нельзя, а
		// снимать то, чего мы не поняли, значит терять свидетельство.
		s.logf("исход кода входа не узнан в %s: %s", found.Name, words)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("клиент ответил на код не тем, чего ждёт дашборд. "+
				"Последнее, что он сказал: «%s». Вход не сделан.", words)})
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
func (s *server) loginAwaitCode(sess string, sent bool) (string, string) {
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
			// Успех узнаётся своими признаками, а не тем, что поле кода ушло.
			// По уходу поля успехом считался любой экран отказа: клиент писал
			// «OAuth error: Request failed with status code 400», дашборд
			// докладывал «вход сделан», снимал сессию, и человек получал
			// свежую ссылку вместо слов о том, что случилось (жалоба
			// пользователя на приёмке).
			if said := loginSaysFailed(pane); said != "" {
				return "fail", said
			}
			if loginScreenUp(pane) {
				return "stuck", loginLastWords(pane)
			}
			return "ok", ""
		}
		if sent && loginSaysRejected(pane) {
			return "again", "код не принят: клиент снова ждёт код авторизации, введите другой"
		}
		if !s.now().Before(deadline) {
			if !sent {
				// Ждём браузер, а не ответ на код: поле кода стоит всё время,
				// пока человек проходит вход, и это не исход, а «ещё идёт».
				return "waiting", ""
			}
			return "again", fmt.Sprintf("клиент не ответил на код за %s: панель всё ещё ждёт его, "+
				"введите код заново", loginCodeWait)
		}
		time.Sleep(loginPollEvery)
	}
}

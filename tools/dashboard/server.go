package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// server держит всё, что нужно обработчикам: конфиг с секретом, статику,
// журнал и часы. Вход обязателен для всего, кроме /healthz, страницы логина
// с её обвязкой и самого /api/login: без входа не отдаётся ни одна строка
// данных, а статика логина данных не содержит.
type server struct {
	cfg     *Config
	static  fs.FS
	logf    func(format string, args ...any)
	now     func() time.Time
	started time.Time

	// Память процесса на обход корней и на ответы taskctl (cache.go). Запросы
	// идут разом, и своими горутинами ходит каждый из них, поэтому под замком.
	mu     sync.Mutex
	scan   scanEntry
	boards map[string]boardEntry
	heads  map[string]headEntry
	// Память разбора хвостов транскриптов (busyEntry): по ней считается, идёт
	// ли ход в сессии. Разбор стоит чтения и парсинга хвоста файла, а спрашивают
	// его и сборка работ, и живой опрос таба сессий.
	busy map[string]busyEntry
	// Когда о молчащем виджете последний раз сказано в журнал: панель
	// переспрашивает вопрос каждые несколько секунд, и без этой памяти журнал
	// забило бы одной и той же строкой (askQuietLog).
	askQuiet map[string]time.Time
	// Память снимка виджета (tmuxAsking): tmux-сессия -> на каком вопросе
	// стоит её клиент. Снимок стоит подпроцесса, а спрашивают его и список
	// чатов, и строка доски, то есть по разу на каждую живую сессию машины.
	askSeen map[string]askEntry
	// Память зонда живости канала (peerProbe): сокет -> ответил ли клиент.
	// Зонд стоит миллисекунды у живого и целый таймаут у клина, и дёргать его
	// на каждую сборку списка чатов было бы дорого ровно там, где больно.
	deaf map[string]deafEntry
	// Память о самолечении клина: разговор -> когда его перезапустили. По ней
	// клин лечится один раз подряд. Без памяти повторившийся клин заводил бы
	// цикл перезапусков, а снятие процесса необратимо.
	heal map[string]healEntry
	// Память снимка пейна на ретрай кончившейся подписки (quotawait.go):
	// tmux-сессия -> узнан ли ретрай. Снимок стоит подпроцесса, а спрашивают
	// его и список чатов, и каждая реплика.
	quotaSeen map[string]quotaWaitEntry
	// Состояние одноразовой сессии входа клиента (clientlogin.go). Вход на
	// машине один, и сессия одна: повторные экраны получают ту же ссылку,
	// а не соседнюю сессию.
	loginRun *loginRun
	// Шов зонда для тестов: боевой сервер зовёт peerProbe, тест подставляет
	// свой ответ и не трогает настоящие сокеты машины.
	probe func(sock string, wait time.Duration) error
	// Отпечаток сборки для адресов статики: считается один раз на процесс.
	stamp string
	// Раскладка подписок машины (harnesses.go): её спрашивает и экран, и
	// каждый запуск, а стоит она подпроцесса agentctl.
	harn     *HarnessView
	harnBorn time.Time
	// Исход последнего обновления снимка квоты (quota.go). Причина отказа едет
	// в плашку квоты: молчание оставляло человека со старым снимком и без
	// объяснения, а в журнале та же строка повторялась каждым тиком.
	quotaErr   string
	quotaErrAt time.Time
	quotaSaid  bool

	// Память о записях очереди исходящих, уже уехавших в сессию (outbox.go):
	// по ней повтор отправителя узнаётся и второй раз не доставляется.
	says map[string]sayClaim
	// Замки записи во «Входящие», по одному на репозиторий: сверка с лежащим и
	// запись это одно действие, и разводить их по разным горутинам нельзя
	// (messages.go). Общего замка на весь дашборд тут не годится: под ним
	// держится и коммит с пушем, а недоступный origin одного проекта запирал
	// бы отправку на всех досках разом.
	inboxes map[string]*sync.Mutex
	// Шов для теста гонки: тест ставит сюда встречу горутин между сверкой и
	// записью, в работе поле пустое. Корень репозитория приходит аргументом,
	// чтобы тест мог держать один проект и смотреть на остальные.
	inboxProbe func(root string)

	// Секрет входа перечитывается из cfg.Path по mtime (DK-481): dashboard
	// secret --rotate меняет только файл, и без переоценки живой демон сверял
	// бы вход и куки со стартовым токеном до рестарта. secretTok это
	// последний удачно прочитанный секрет, secretAt его mtime на тот момент,
	// secretErr причина последнего провала перечитывания (пусто, когда всё
	// хорошо). Свой замок, а не общий s.mu: проверка идёт на каждом запросе, и
	// незачем ждать за ней сканы корней и harnesses.
	secretMu  sync.Mutex
	secretTok string
	secretAt  time.Time
	secretErr string

	// Сторож поднятых сессий (chatwatch.go): watch это имена под присмотром,
	// tails последний снимок панели каждой. Снимок держится в памяти нарочно:
	// нужен он ровно до смерти сессии, а перезапуск демона снимет панель
	// заново первым же обходом. Свой замок, а не общий s.mu: обход ходит
	// каждые несколько секунд и незачем ему ждать за сканами корней.
	watchMu sync.Mutex
	watch   map[string]bool
	tails   map[string]chatTail
	// Отдельный замок смерти: под ним идёт сверка записи и запись строки в
	// ленту, и общий watchMu тут не годится, его берёт снимок панели.
	deathMu sync.Mutex

	// Вопросы помощника пароля askpass (askpass.go, DK-772): ID запроса ->
	// ожидание. Пароль живёт тут, в памяти, и в ответе помощнику; в журнал,
	// транскрипт и ленту он не едет. Свой замок: длинный опрос помощника не
	// должен ждать за сканами корней и harnesses.
	askpassMu    sync.Mutex
	askpassWaits map[string]*askpassWait
	// Секрет локального входа помощника: демон рождает его на каждом старте,
	// пишет в файл под настоящим домом машины (writeAskpassSecret) и держит
	// тут же, чтобы сверять заголовок запроса без похода на диск. Куки
	// панели он не заменяет: заголовком его предъявляет только помощник,
	// живущий на той же машине.
	askpassSecret string
}

// inboxLock отдаёт замок «Входящих» этого репозитория, заводя его при первом
// обращении. Карта живёт под общим замком памяти процесса: раздача идёт без
// обращений к диску, и держать её здесь дешевле, чем заводить второй замок
// ради самих замков.
func (s *server) inboxLock(root string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inboxes == nil {
		s.inboxes = map[string]*sync.Mutex{}
	}
	mu, ok := s.inboxes[root]
	if !ok {
		mu = &sync.Mutex{}
		s.inboxes[root] = mu
	}
	return mu
}

func newServer(cfg *Config, static fs.FS, logf func(string, ...any)) *server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &server{cfg: cfg, static: static, logf: logf, now: time.Now, started: time.Now(),
		boards: map[string]boardEntry{}, heads: map[string]headEntry{}, deaf: map[string]deafEntry{},
		busy:      map[string]busyEntry{},
		heal:      map[string]healEntry{},
		probe:     peerProbe,
		secretTok: cfg.Token}
}

// refreshToken отдаёт действующий секрет входа, при нужде перечитывая его из
// cfg.Path: dashboard secret --rotate пишет новый токен только в файл, и без
// этого демон сверял бы вход со стартовым секретом до рестарта. Stat дешёвый
// и идёт на каждой проверке, а сам LoadConfig (дороже: разбор всего файла)
// зовётся только при изменившемся mtime. Пустой Path это синтетический
// конфиг теста без файла на диске: перечитывать нечего, отдаётся то, что
// заведено при старте.
func (s *server) refreshToken() string {
	if s.cfg.Path == "" {
		return s.secretTok
	}
	fi, err := os.Stat(s.cfg.Path)
	if err != nil {
		s.secretMu.Lock()
		defer s.secretMu.Unlock()
		s.noteSecretErrLocked(fmt.Sprintf(
			"secret: %s недоступен (%v), действует прежний токен", s.cfg.Path, err))
		return s.secretTok
	}
	s.secretMu.Lock()
	if fi.ModTime().Equal(s.secretAt) {
		tok := s.secretTok
		s.secretMu.Unlock()
		return tok
	}
	s.secretMu.Unlock()

	// Файл менялся: полный разбор дороже stat, но только тут он и нужен.
	// Между snapshot mtime и этим чтением файл может обновиться ещё раз (две
	// быстрые ротации подряд), тогда следующая проверка увидит новый mtime
	// и перечитает снова, потерь не будет.
	cfg, err := LoadConfig(s.cfg.Home)
	s.secretMu.Lock()
	defer s.secretMu.Unlock()
	if err != nil {
		s.noteSecretErrLocked(fmt.Sprintf(
			"secret: перечитать %s не удалось (%v), действует прежний токен", s.cfg.Path, err))
		return s.secretTok
	}
	s.secretAt = fi.ModTime()
	s.secretErr = ""
	if cfg.Token != "" {
		s.secretTok = cfg.Token
	}
	return s.secretTok
}

// noteSecretErrLocked запоминает причину провала перечитывания секрета для
// /healthz и журнала; зовётся под s.secretMu. Лог пишется только при смене
// сообщения, иначе каждая проверка входа заливала бы журнал одной и той же
// строкой.
func (s *server) noteSecretErrLocked(msg string) {
	if s.secretErr == msg {
		return
	}
	s.secretErr = msg
	s.logf("%s", msg)
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	// Помощник askpass живёт на той же машине и куки панели не носит: свой
	// вход у него по секрету заголовком, проверка внутри ручки (askpass.go,
	// DK-772). Заворачивать в s.auth нельзя, кука тут никогда не придёт.
	mux.HandleFunc("POST /api/askpass", s.handleAskpassRequest)
	mux.HandleFunc("POST /api/logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /api/projects", s.auth(s.handleProjects))
	mux.HandleFunc("GET /api/projects/{p}/board", s.auth(s.handleBoard))
	mux.HandleFunc("GET /api/projects/{p}/works", s.auth(s.handleWorks))
	mux.HandleFunc("GET /api/projects/{p}/search", s.auth(s.handleSearch))
	mux.HandleFunc("GET /api/projects/{p}/doc", s.auth(s.handleDoc))
	mux.HandleFunc("PUT /api/projects/{p}/doc", s.auth(s.handleDocPut))
	mux.HandleFunc("GET /api/projects/{p}/lld", s.auth(s.handleLldList))
	mux.HandleFunc("POST /api/projects/{p}/tasks", s.auth(s.handleTaskCreate))
	mux.HandleFunc("POST /api/projects/{p}/drafts", s.auth(s.handleDraftPost))
	mux.HandleFunc("GET /api/projects/{p}/drafts", s.auth(s.handleDrafts))
	mux.HandleFunc("GET /api/projects/{p}/drafts/{id}", s.auth(s.handleDraft))
	mux.HandleFunc("PUT /api/projects/{p}/drafts/{id}", s.auth(s.handleDraftPut))
	mux.HandleFunc("DELETE /api/projects/{p}/drafts/{id}", s.auth(s.handleDraftDrop))
	mux.HandleFunc("POST /api/projects/{p}/drafts/{id}/groom", s.auth(s.handleDraftGroom))
	mux.HandleFunc("GET /api/projects/{p}/tasks/{id}", s.auth(s.handleTask))
	mux.HandleFunc("PATCH /api/projects/{p}/tasks/{id}", s.auth(s.handleTaskPatch))
	mux.HandleFunc("POST /api/projects/{p}/tasks/{id}/file", s.auth(s.handleTaskFilePost))
	mux.HandleFunc("PUT /api/projects/{p}/tasks/{id}/file", s.auth(s.handleTaskFilePut))
	mux.HandleFunc("POST /api/projects/{p}/tasks/{id}/message", s.auth(s.handleTaskMessagePost))
	mux.HandleFunc("DELETE /api/projects/{p}/tasks/{id}/message", s.auth(s.handleTaskMessageDelete))
	mux.HandleFunc("POST /api/projects/{p}/tasks/{id}/deps", s.auth(s.handleTaskDepAdd))
	mux.HandleFunc("DELETE /api/projects/{p}/tasks/{id}/deps/{dep}", s.auth(s.handleTaskDepRm))
	mux.HandleFunc("POST /api/projects/{p}/tasks/{id}/continue", s.auth(s.handleTaskContinue))
	mux.HandleFunc("POST /api/projects/{p}/runs", s.auth(s.handleRunStart))
	mux.HandleFunc("POST /api/projects/{p}/chats", s.auth(s.handleChatStart))
	mux.HandleFunc("POST /api/projects/{p}/chats/blank", s.auth(s.handleChatBlank))
	mux.HandleFunc("GET /api/projects/{p}/chats", s.auth(s.handleChatList))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/say", s.auth(s.handleChatSay))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/stop", s.auth(s.handleChatStop))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/heal", s.auth(s.handleChatHeal))
	mux.HandleFunc("GET /api/projects/{p}/chats/{sid}/status", s.auth(s.handleChatStatus))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/shot", s.auth(s.handleChatShot))
	mux.HandleFunc("GET /api/projects/{p}/chats/{sid}/shot", s.auth(s.handleChatShotGet))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/model", s.auth(s.handleChatModel))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/archive", s.auth(s.handleChatArchive))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/drop", s.auth(s.handleChatDrop))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/draft", s.auth(s.handleChatDraft))
	mux.HandleFunc("POST /api/projects/{p}/chats/login", s.auth(s.handleClientLogin))
	mux.HandleFunc("POST /api/projects/{p}/chats/login/code", s.auth(s.handleClientLoginCode))
	mux.HandleFunc("POST /api/projects/{p}/chats/login/wait", s.auth(s.handleClientLoginWait))
	mux.HandleFunc("DELETE /api/projects/{p}/runs/{id}", s.auth(s.handleRunStop))
	mux.HandleFunc("GET /api/projects/{p}/goals/{id}/log", s.auth(s.handleGoalLog))
	mux.HandleFunc("GET /api/projects/{p}/goals/{id}/tasks", s.auth(s.handleGoalTasks))
	mux.HandleFunc("GET /api/projects/{p}/goals/{id}/message", s.auth(s.handleGoalMessageGet))
	mux.HandleFunc("POST /api/projects/{p}/goals/{id}/message", s.auth(s.handleGoalMessagePost))
	mux.HandleFunc("GET /api/projects/{p}/pulse", s.auth(s.handlePulse))
	mux.HandleFunc("GET /api/projects/{p}/sessions", s.auth(s.handleSessions))
	mux.HandleFunc("GET /api/projects/{p}/sessions/{sid}", s.auth(s.handleSession))
	mux.HandleFunc("POST /api/projects/{p}/sessions/{sid}/message", s.auth(s.handleSessionMessagePost))
	mux.HandleFunc("POST /api/projects/{p}/sessions/{sid}/task", s.auth(s.handleSessionTaskPost))
	mux.HandleFunc("GET /api/notifications", s.auth(s.handleNotifications))
	mux.HandleFunc("GET /api/quota", s.auth(s.handleQuota))
	mux.HandleFunc("GET /api/harnesses", s.auth(s.handleHarnesses))
	mux.HandleFunc("GET /api/projects/{p}/chats/{sid}/ask", s.auth(s.handleChatAsk))
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/ask", s.auth(s.handleChatAskAnswer))
	// Ответ на вопрос помощника пароля идёт своей ручкой, а не общей ask: там
	// разбор вариантов и текст эха ленты, а пароль в эту дорогу попадать не
	// должен (DK-772).
	mux.HandleFunc("POST /api/projects/{p}/chats/{sid}/askpass", s.auth(s.handleChatAskpassAnswer))
	mux.HandleFunc("GET /api/tmux", s.auth(s.handleTmuxList))
	mux.HandleFunc("GET /api/tmux/{name}", s.auth(s.handleTmuxPane))
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		s.serveStatic(w, r, "login.html")
	})
	mux.HandleFunc("GET /assets/{file}", s.handleAsset)
	mux.HandleFunc("GET /{$}", s.authPage(func(w http.ResponseWriter, r *http.Request) {
		s.serveStatic(w, r, "index.html")
	}))
	return headers(mux)
}

// headers ставит общие заголовки на каждый ответ: CSP запрещает инлайн и
// внешние источники (решение LLD про XSS), nosniff бережёт типы.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Картинке разрешён ещё и data:. Вставленный из буфера снимок панель
		// показывает до отправки прямо из dataURL, файла на диске тогда ещё
		// нет вовсе, и под default-src 'self' браузер такую картинку не грузил:
		// в блоке превью оставался значок битого изображения (замечание
		// тринадцатого круга POC). Остальным источникам ничего не добавлено.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *server) loggedIn(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && cookieValid(s.refreshToken(), c.Value, s.now())
}

// auth заворачивает API-ручку: без входа 401 и ни одной строки данных.
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.loggedIn(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "нужен вход"})
			return
		}
		next(w, r)
	}
}

// authPage заворачивает страницу: без входа редирект на /login.
func (s *server) authPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.loggedIn(r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

// openAssets это файлы статики, доступные до входа: обвязка страницы логина.
// Экран доски (index.html, app.js) остаётся за входом вместе с данными.
var openAssets = map[string]bool{"style.css": true, "login.js": true}

func (s *server) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.PathValue("file"))
	if !openAssets[name] && !s.loggedIn(r) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	s.serveStatic(w, r, name)
}

var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".svg":  "image/svg+xml",
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct, ok := contentTypes[path.Ext(name)]; ok {
		w.Header().Set("Content-Type", ct)
	}
	if name == "index.html" {
		data = stampAssets(data, s.assetStamp())
	}
	w.Write(data)
}

// Отбойник кеша браузера. Статика лежит по вечным адресам /assets/app.js, и
// браузер честно держит её в кеше: выкаченная правка доезжала только жёсткой
// перезагрузкой, а до тех пор человек смотрел старый экран и считал правку
// несделанной. Метка версии дописывается к адресам в самой странице, поэтому
// новая сборка меняет адрес и кеш промахивается сам.
var assetRefRe = regexp.MustCompile(`(/assets/[A-Za-z0-9._-]+)`)

func stampAssets(data []byte, stamp string) []byte {
	if stamp == "" {
		return data
	}
	return assetRefRe.ReplaceAll(data, []byte("$1?v="+stamp))
}

// assetStamp это отпечаток сборки: версия с коммитом у выпущенного бинаря,
// а у собранного из исходников время правки самой статики, чтобы правка без
// пересборки версии тоже доезжала.
func (s *server) assetStamp() string {
	s.mu.Lock()
	got := s.stamp
	s.mu.Unlock()
	if got != "" {
		return got
	}
	// Отпечаток считается по самому содержимому статики, а не по версии с
	// временем правки: у вшитой файловой системы время правки нулевое у всех
	// файлов, а версия у сборки из исходников постоянная, и метка не менялась
	// между пересборками вовсе. То есть отбойник кеша не отбивал ничего, и
	// человек продолжал смотреть старый экран (регресс тринадцатого круга POC).
	h := sha256.New()
	h.Write([]byte(version + "-" + commit))
	for _, name := range []string{"app.js", "style.css", "index.html"} {
		if data, err := fs.ReadFile(s.static, name); err == nil {
			h.Write(data)
		}
	}
	got = fmt.Sprintf("%x", h.Sum(nil))[:12]
	s.mu.Lock()
	s.stamp = got
	s.mu.Unlock()
	return got
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// healthErrs собирает ошибки конфига и окружения: их называет /healthz,
// потому что тихая деградация неотличима от «проектов нет».
func (s *server) healthErrs() ([]Project, []string) {
	errs := append([]string{}, s.cfg.Errs...)
	s.refreshToken()
	s.secretMu.Lock()
	if s.secretErr != "" {
		errs = append(errs, s.secretErr)
	}
	s.secretMu.Unlock()
	projects, scanErrs := s.projects()
	errs = append(errs, scanErrs...)
	if m := taskctlMissing(); m != "" {
		errs = append(errs, m)
	}
	if m := tmuxMissingCheck(); m != "" {
		errs = append(errs, m)
	}
	return projects, errs
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	projects, errs := s.healthErrs()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"version":  fmt.Sprintf("%s %s (%s)", toolName, version, commit),
		"uptime_s": int(s.now().Sub(s.started).Seconds()),
		"projects": len(projects),
		"errors":   errs,
	})
}

// externalHost называет имя, по которому дашборд открыт у браузера. Дома это
// Host запроса, а за посредником захода извне Host занят адресом апстрима
// (клиент шар xr-proxy переписывает его, чтобы сервис за публикацией видел
// свой адрес), и внешнее имя приезжает в X-Forwarded-Host, как у любого
// сервиса за прокси. Подставить заголовок из браузера чужая страница не может:
// форма своих заголовков не ставит, а fetch со своим упирается в предполётный
// запрос, на который дашборд не отвечает, так что сверка Origin остаётся
// сверкой.
func externalHost(r *http.Request) string {
	fwd := r.Header.Get("X-Forwarded-Host")
	// Цепочка посредников пишет имена через запятую, ближайшее к браузеру идёт
	// первым: оно и стоит в Origin.
	if i := strings.IndexByte(fwd, ','); i >= 0 {
		fwd = fwd[:i]
	}
	if fwd = strings.TrimSpace(fwd); fwd != "" {
		return fwd
	}
	return r.Host
}

// sameOrigin сверяет Origin на изменяющих методах: вторая половина защиты от
// CSRF рядом с SameSite=Lax. Запрос без Origin (curl, smoke-сценарий) честнее
// пропустить: CSRF это атака браузером, а браузер Origin ставит всегда.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == externalHost(r)
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		s.logf("вход отклонён: чужой Origin %q", r.Header.Get("Origin"))
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"token\": \"...\"}"})
		return
	}
	// Секрет спрашивается один раз на запрос: перечитан он или нет, вход и
	// подпись куки идут с тем же значением.
	token := s.refreshToken()
	if !tokenMatch(token, body.Token) {
		// Пауза гасит перебор: провал входа стоит секунду, а не тысячи попыток
		// в секунду.
		time.Sleep(loginPause)
		s.logf("провал входа с %s", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "токен не подошёл"})
		return
	}
	expiry := s.now().Add(cookieAge)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: cookieValue(token, expiry),
		Path: "/", Expires: expiry, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	s.logf("вход с %s", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	s.logf("выход с %s", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// projectInfo это строка списка проектов: счётчики секций и живые работы,
// либо причина, почему доска не прочиталась, вместо них.
type projectInfo struct {
	Project
	// Prefix это префикс ID доски (DK, XR): им подписан проект на главной, и
	// брать его оттуда дешевле, чем читать доску второй раз.
	Prefix   string         `json:"prefix,omitempty"`
	Sections map[string]int `json:"sections,omitempty"`
	// Drafts это число записей накопителя: им подписан таб черновиков на доске.
	// Считается оно чтением каталога, а не подпроцессом taskctl: список
	// проектов спрашивают на каждом обходе экрана, и лишняя утилита на проект
	// стоила бы дороже самого ответа.
	Drafts int    `json:"drafts,omitempty"`
	Works  []Work `json:"works"`
	Error  string `json:"error,omitempty"`
}

func (s *server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, errs := s.healthErrs()
	// Проекты опрашиваются разом: каждый стоит своих подпроцессов (taskctl на
	// доску, tmux на работы), и по очереди стартовая ждала бы их сумму.
	infos := make([]projectInfo, len(projects))
	broken := inParallel(projectWorkers, len(projects), func(i int) { infos[i] = s.projectSummary(projects[i]) })
	for i, err := range broken {
		if err == nil {
			continue
		}
		// Сломанный проект остаётся строкой с причиной: список не редеет молча
		// и не уносит с собой соседей.
		infos[i] = projectInfo{Project: projects[i], Works: []Work{},
			Error: s.notePanic("опрос проекта "+projects[i].Name, err)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": infos, "errors": errs})
}

// projectSummary собирает строку списка проектов: счётчики секций и живые
// работы, а не прочитанная доска остаётся причиной вместо них.
func (s *server) projectSummary(p Project) projectInfo {
	info := projectInfo{Project: p, Works: []Work{}}
	raw, err := s.projectBoard(p.Path)
	if err != nil {
		info.Error = err.Error()
		s.logf("доска %s: %v", p.Name, err)
		return info
	}
	view, err := parseBoardView(raw)
	if err != nil {
		info.Error = fmt.Sprintf("ответ taskctl не разобрался: %v", err)
		return info
	}
	info.Prefix = view.Prefix
	info.Sections = map[string]int{}
	for _, sec := range view.Sections {
		info.Sections[sec.Key] = len(sec.Rows)
	}
	info.Works = s.liveWorks(p.Path, view.Prefix, raw)
	info.Drafts = countDrafts(p.Path)
	return info
}

// countDrafts считает записи накопителя. Каталога может не быть вовсе, и это
// обычный случай: проект без единого черновика.
func countDrafts(projectPath string) int {
	entries, err := os.ReadDir(filepath.Join(projectPath, "docs", "tasks", "drafts"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			n++
		}
	}
	return n
}

// findProject находит проект из пути запроса; не найдя, сам отвечает 404 и
// возвращает nil.
func (s *server) findProject(w http.ResponseWriter, r *http.Request, action string) *Project {
	name := r.PathValue("p")
	projects, _ := s.projects()
	for i := range projects {
		if projects[i].Name == name {
			return &projects[i]
		}
	}
	s.logf("%s отклонён: проект %s не найден 404", action, name)
	writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("проекта %s нет в корнях конфига", name)})
	return nil
}

// handleWorks отдаёт живые работы одного проекта и ничего больше. Ручка эта
// заведена под живой опрос таба сессий: список там перестраивается по ходу
// работы агентов, и спрашивать ради этого весь /api/projects значило бы
// каждые несколько секунд опрашивать все доски машины разом. Тут доска
// читается из памяти по отпечатку файла, а разбор хвостов транскриптов из
// памяти по отпечатку транскрипта, поэтому повторный заход стоит немногого.
func (s *server) handleWorks(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "работы проекта")
	if found == nil {
		return
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		s.logf("работы %s: %v", found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	view, err := parseBoardView(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": found.Name,
		"works":   s.liveWorks(found.Path, view.Prefix, raw),
	})
}

func (s *server) handleBoard(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "доска")
	if found == nil {
		return
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		s.logf("доска %s: %v", found.Name, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	view, err := parseBoardView(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return
	}
	// Живые работы едут и отдельным списком (его рисует полоса наверху экрана),
	// и признаком в самих строках доски: полосе нужен список работ, а строке
	// нужно знать про себя, и сводить одно к другому на клиенте значило
	// собирать состояние строки из двух ответов сразу (DK-317).
	works := s.liveWorks(found.Path, view.Prefix, raw)
	// Исполнители строк: сессии, привязанные к задаче реестром. Второго разбора,
	// поиска исполнителя по хвостам транскриптов, тут больше нет: он спасал
	// строки от признака other, а тот снят вместе с плашкой «исполнителя не
	// видно», и хвосты живых сессий читались зря.
	mine := s.taskChats(found.Path)
	resp := map[string]any{
		"project": found.Name,
		"path":    found.Path,
		"board": boardRuns(raw, works, mine, s.liveStages(found.Path),
			s.waitLookup(found.Path)),
		"works":  works,
		"errors": []string{},
	}
	// Пустой список работ при ненайденном tmux это не «агенты не работают»,
	// причина называется и здесь, а не только в /healthz.
	if m := tmuxMissingCheck(); m != "" {
		resp["errors"] = []string{m}
	}
	writeJSON(w, http.StatusOK, resp)
}

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
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
	// Раскладка подписок машины (harnesses.go): её спрашивает и экран, и
	// каждый запуск, а стоит она подпроцесса agentctl.
	harn     *HarnessView
	harnBorn time.Time

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
		boards: map[string]boardEntry{}, heads: map[string]headEntry{}}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /api/projects", s.auth(s.handleProjects))
	mux.HandleFunc("GET /api/projects/{p}/board", s.auth(s.handleBoard))
	mux.HandleFunc("GET /api/projects/{p}/search", s.auth(s.handleSearch))
	mux.HandleFunc("POST /api/projects/{p}/tasks", s.auth(s.handleTaskCreate))
	mux.HandleFunc("POST /api/projects/{p}/drafts", s.auth(s.handleDraftPost))
	mux.HandleFunc("GET /api/projects/{p}/drafts", s.auth(s.handleDrafts))
	mux.HandleFunc("GET /api/projects/{p}/drafts/{id}", s.auth(s.handleDraft))
	mux.HandleFunc("DELETE /api/projects/{p}/drafts/{id}", s.auth(s.handleDraftDrop))
	mux.HandleFunc("GET /api/projects/{p}/drafts/{id}/outcome", s.auth(s.handleDraftOutcome))
	mux.HandleFunc("POST /api/projects/{p}/drafts/{id}/groom", s.auth(s.handleDraftGroom))
	mux.HandleFunc("GET /api/projects/{p}/tasks/{id}", s.auth(s.handleTask))
	mux.HandleFunc("PATCH /api/projects/{p}/tasks/{id}", s.auth(s.handleTaskPatch))
	mux.HandleFunc("POST /api/projects/{p}/tasks/{id}/file", s.auth(s.handleTaskFilePost))
	mux.HandleFunc("PUT /api/projects/{p}/tasks/{id}/file", s.auth(s.handleTaskFilePut))
	mux.HandleFunc("POST /api/projects/{p}/tasks/{id}/deps", s.auth(s.handleTaskDepAdd))
	mux.HandleFunc("DELETE /api/projects/{p}/tasks/{id}/deps/{dep}", s.auth(s.handleTaskDepRm))
	mux.HandleFunc("POST /api/projects/{p}/runs", s.auth(s.handleRunStart))
	mux.HandleFunc("DELETE /api/projects/{p}/runs/{id}", s.auth(s.handleRunStop))
	mux.HandleFunc("GET /api/projects/{p}/goals/{id}/log", s.auth(s.handleGoalLog))
	mux.HandleFunc("GET /api/projects/{p}/goals/{id}/tasks", s.auth(s.handleGoalTasks))
	mux.HandleFunc("GET /api/projects/{p}/goals/{id}/message", s.auth(s.handleGoalMessageGet))
	mux.HandleFunc("POST /api/projects/{p}/goals/{id}/message", s.auth(s.handleGoalMessagePost))
	mux.HandleFunc("GET /api/projects/{p}/sessions", s.auth(s.handleSessions))
	mux.HandleFunc("GET /api/projects/{p}/sessions/{sid}", s.auth(s.handleSession))
	mux.HandleFunc("POST /api/projects/{p}/sessions/{sid}/message", s.auth(s.handleSessionMessagePost))
	mux.HandleFunc("GET /api/notifications", s.auth(s.handleNotifications))
	mux.HandleFunc("GET /api/quota", s.auth(s.handleQuota))
	mux.HandleFunc("GET /api/harnesses", s.auth(s.handleHarnesses))
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
		h.Set("Content-Security-Policy",
			"default-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *server) loggedIn(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && cookieValid(s.cfg.Token, c.Value, s.now())
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
	w.Write(data)
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
	if !tokenMatch(s.cfg.Token, body.Token) {
		// Пауза гасит перебор: провал входа стоит секунду, а не тысячи попыток
		// в секунду.
		time.Sleep(loginPause)
		s.logf("провал входа с %s", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "токен не подошёл"})
		return
	}
	expiry := s.now().Add(cookieAge)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: cookieValue(s.cfg.Token, expiry),
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
	Works    []Work         `json:"works"`
	Error    string         `json:"error,omitempty"`
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
	return info
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
	resp := map[string]any{
		"project": found.Name,
		"path":    found.Path,
		"board":   boardRuns(raw, works, s.liveStages(found.Path)),
		"works":   works,
		"errors":  []string{},
	}
	// Пустой список работ при ненайденном tmux это не «агенты не работают»,
	// причина называется и здесь, а не только в /healthz.
	if m := tmuxMissingCheck(); m != "" {
		resp["errors"] = []string{m}
	}
	writeJSON(w, http.StatusOK, resp)
}

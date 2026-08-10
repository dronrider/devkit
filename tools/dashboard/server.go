package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"path"
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
}

func newServer(cfg *Config, static fs.FS, logf func(string, ...any)) *server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &server{cfg: cfg, static: static, logf: logf, now: time.Now, started: time.Now()}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /api/projects", s.auth(s.handleProjects))
	mux.HandleFunc("GET /api/projects/{p}/board", s.auth(s.handleBoard))
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
	projects, scanErrs := scanProjects(s.cfg.Roots)
	errs = append(errs, scanErrs...)
	if m := taskctlMissing(); m != "" {
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

// sameOrigin сверяет Origin на изменяющих методах: вторая половина защиты от
// CSRF рядом с SameSite=Lax. Запрос без Origin (curl, smoke-сценарий) честнее
// пропустить: CSRF это атака браузером, а браузер Origin ставит всегда.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
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
	Sections map[string]int `json:"sections,omitempty"`
	Works    []Work         `json:"works"`
	Error    string         `json:"error,omitempty"`
}

func (s *server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, errs := s.healthErrs()
	infos := []projectInfo{}
	for _, p := range projects {
		info := projectInfo{Project: p, Works: []Work{}}
		raw, err := boardJSON(p.Path)
		if err != nil {
			info.Error = err.Error()
			s.logf("доска %s: %v", p.Name, err)
			infos = append(infos, info)
			continue
		}
		view, err := parseBoardView(raw)
		if err != nil {
			info.Error = fmt.Sprintf("ответ taskctl не разобрался: %v", err)
			infos = append(infos, info)
			continue
		}
		info.Sections = map[string]int{}
		for _, sec := range view.Sections {
			info.Sections[sec.Key] = len(sec.Rows)
		}
		info.Works = liveWorks(p.Path, view.Prefix, s.cfg.Home)
		infos = append(infos, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": infos, "errors": errs})
}

func (s *server) handleBoard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("p")
	projects, _ := scanProjects(s.cfg.Roots)
	var found *Project
	for i := range projects {
		if projects[i].Name == name {
			found = &projects[i]
			break
		}
	}
	if found == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("проекта %s нет в корнях конфига", name)})
		return
	}
	raw, err := boardJSON(found.Path)
	if err != nil {
		s.logf("доска %s: %v", name, err)
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
		"path":    found.Path,
		"board":   raw,
		"works":   liveWorks(found.Path, view.Prefix, s.cfg.Home),
	})
}

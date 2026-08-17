package main

import (
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"

	"github.com/dronrider/devkit/internal/works"
)

// Живой статус агента, сторона tmux: список сессий машины и снимок пейна
// через capture-pane. tmux это подпроцесс со сроком (runProc), как все чужие
// программы сервера: зависший снимок не должен держать горутину запроса.
// Список общий на машину, а не на проект: сессии tmux к корням не привязаны,
// привязку к работе делает клиент по имени goal-<ID>/task-<ID>.

// tmuxSession это строка списка: имя, окна и время создания unix-секундами,
// как их отдаёт формат tmux.
type tmuxSession struct {
	Name    string `json:"name"`
	Windows int    `json:"windows"`
	Created int64  `json:"created"`
}

// tmuxList отдаёт сессии; разбор вывода живёт в общем каркасе
// (internal/works), потому что занятость задач по именам сессий читает и
// планировщик слота taskctl. Ненулевой код ls это штатное «сессий нет».
func tmuxList() []tmuxSession {
	sessions := []tmuxSession{}
	for _, s := range works.Sessions() {
		sessions = append(sessions, tmuxSession(s))
	}
	return sessions
}

func (s *server) handleTmuxList(w http.ResponseWriter, r *http.Request) {
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	sessions := tmuxList()
	if sessions == nil {
		sessions = []tmuxSession{}
	}
	resp := map[string]any{"sessions": sessions}
	if len(sessions) == 0 {
		resp["note"] = "tmux-сессий нет"
	}
	writeJSON(w, http.StatusOK, resp)
}

var tmuxNameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

func (s *server) handleTmuxPane(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !tmuxNameRe.MatchString(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на имя tmux-сессии", name)})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	// Знак = требует точного имени сессии, без него tmux берёт её по префиксу
	// и снимок пришёл бы от соседки; capture-pane ждёт цель-пейн, поэтому
	// после имени стоит двоеточие: активное окно точной сессии.
	out, err := runProc("tmux", "capture-pane", "-p", "-t", "="+name+":")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Ненулевой код это «сессии нет»: пустота различима, снимок без
			// сессии называется словами, а не пустым экраном.
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": fmt.Sprintf("tmux-сессия %s не найдена: %s", name, procErr(err))})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": procErr(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "text": string(out)})
}

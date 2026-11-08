package main

import (
	"net/http"
	"path/filepath"

	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskstate"
)

func (s *server) recoveryStopped(w http.ResponseWriter, root, id string, stopped bool) bool {
	if err := taskstate.SetStopped(s.cfg.Home, root, id, stopped); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "состояние ручной остановки не сохранено: " + err.Error()})
		return false
	}
	return true
}

func (s *server) recoveryChatStopped(w http.ResponseWriter, found *Project, sid, action string) bool {
	records := s.bindsAll()[sid]
	tasks := sessions.Works(records)
	if id := sessions.LastTask(records); id != "" {
		tasks = append(tasks, id)
	}
	stored := s.chatStoreRead(sid)
	if stored.Task != "" {
		tasks = append(tasks, stored.Task)
	}
	roots := []string{found.Path}
	last := sessions.Last(records)
	if filepath.IsAbs(last.Tree) && isFile(filepath.Join(last.Tree, "docs", "TASKS.md")) && stage.MainRoot(last.Tree) == stage.MainRoot(found.Path) {
		roots = append(roots, last.Tree)
	}
	seen := map[string]bool{}
	for _, root := range roots {
		for _, id := range tasks {
			key := root + "/" + id
			if seen[key] {
				continue
			}
			seen[key] = true
			var err error
			if action == "invalidate" {
				err = taskstate.Invalidate(s.cfg.Home, root, id)
			} else {
				err = taskstate.SetStopped(s.cfg.Home, root, id, action == "stop")
			}
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "состояние запуска не сохранено: " + err.Error()})
				return false
			}
		}
	}
	return true
}

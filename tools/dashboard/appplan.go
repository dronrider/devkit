package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dronrider/devkit/internal/chat"
)

type appPlan struct {
	Thread string        `json:"threadId"`
	Turn   string        `json:"turnId"`
	Plan   []appPlanStep `json:"plan"`
}

type appPlanStep struct {
	Text   string `json:"step"`
	Status string `json:"status"`
}

func (s *server) appPlanPath(sid string) string {
	return filepath.Join(appFeedDir(s.cfg.Home), sid+".plan.json")
}

func (s *server) appPlanUpdate(sid string, raw json.RawMessage) error {
	var p appPlan
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.Thread == "" || p.Turn == "" {
		return fmt.Errorf("в событии плана нет нити или хода")
	}
	for _, step := range p.Plan {
		if step.Status != "pending" && step.Status != "inProgress" && step.Status != "completed" {
			return fmt.Errorf("неизвестное состояние пункта плана %q", step.Status)
		}
	}
	s.appMu.Lock()
	defer s.appMu.Unlock()
	c := s.appChats[sid]
	if c == nil || c.thread != p.Thread {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn != "" && c.turn != p.Turn {
		return nil
	}
	c.turn = p.Turn
	file := s.appPlanPath(sid)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	lock, err := chat.Lock(file + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".plan-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), file)
}

func (s *server) appPlanRead(sid string) []planItem {
	data, err := os.ReadFile(s.appPlanPath(sid))
	if os.IsNotExist(err) {
		return nil
	}
	var p appPlan
	if err != nil || json.Unmarshal(data, &p) != nil {
		return []planItem{{Text: "План Codex не прочитался", State: "pending", Src: planSrcErr}}
	}
	out := make([]planItem, 0, len(p.Plan))
	for _, step := range p.Plan {
		state := step.Status
		if state == "inProgress" {
			state = "in_progress"
		}
		out = append(out, planItem{Text: step.Text, State: state})
	}
	return out
}

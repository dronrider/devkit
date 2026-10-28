package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

type scenarioAnswer struct {
	Task     string `json:"task"`
	Fork     string `json:"fork"`
	Snapshot string `json:"snapshot"`
	Tree     string `json:"tree,omitempty"`
}

func (s *server) handleScenarioSay(w http.ResponseWriter, p *Project, sid, claim, text string, answer scenarioAnswer) {
	if !goalIDRe.MatchString(answer.Task) || answer.Fork == "" || answer.Snapshot == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ответу сценариев нужны ID задачи, имя и снимок"})
		return
	}
	recs := s.bindsAll()
	root, name, who := "", "", ""
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		if h.Ask.Task != answer.Task {
			continue
		}
		for _, q := range h.Ask.Questions {
			if q.Scenario != nil && q.Scenario.Fork == answer.Fork {
				tree := h.Tree
				if q.Scenario.Tree != "" {
					tree = q.Scenario.Tree
				}
				if answer.Tree != "" && tree != answer.Tree {
					continue
				}
				root, name, who = tree, h.Name, h.Ask.Session
				if sessions.Last(recs[sid]).Task != answer.Task {
					who = ""
				}
			}
		}
	}
	if root == "" && answer.Tree != "" {
		if s.scenarioTree(answer.Tree, answer.Task) {
			root, name = answer.Tree, chat.TaskName(answer.Task)
			if sessions.Last(recs[sid]).Task == answer.Task && p.Path == root {
				who = sid
			}
		}
	}
	if root == "" && answer.Tree == "" && sessions.Last(recs[sid]).Task == answer.Task {
		root, name, who = p.Path, chat.TaskName(answer.Task), sid
	}
	if root == "" || !s.scenarioTree(root, answer.Task) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ответу сценариев нужен показанный блок этой задачи, имя и снимок"})
		return
	}
	term := s.sayTermOf(sid, recs)
	result, code, err := s.taskctlWrite(root, "decide", answer.Task, answer.Fork, "--snapshot", answer.Snapshot, "--answer", text)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	// Ожиданием управляет CLI, в том числе при точном повторе и свободном ответе.
	payload, _ := json.Marshal(map[string]string{"text": text, "snapshot": answer.Snapshot, "result": result})
	wire := "Ответ панели обработан taskctl decide: " + string(payload)
	if term != "" {
		err = chatSend(term, wire)
	} else {
		line := chat.TaskLine(s.now(), wire)
		if who != "" {
			line = chat.Line(s.now(), who, wire)
		}
		_, _, err = putChat(root, name, wire, line)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CLI принял ответ; доставка агенту: " + procErr(err)})
		return
	}
	s.chatSayDone(sid, claim, "ask")
	s.saidSay(saidSessionKey(sid), text, "ask")
	writeJSON(w, http.StatusOK, map[string]any{"way": "ask", "task": answer.Task, "message": result})
}

func (s *server) scenarioTree(tree, id string) bool {
	if !filepath.IsAbs(tree) || !goalIDRe.MatchString(id) {
		return false
	}
	main := stage.MainRoot(tree)
	mainInfo, err := os.Stat(main)
	if err != nil {
		return false
	}
	projects, _ := s.projects()
	for _, project := range projects {
		info, err := os.Stat(project.Path)
		if err == nil && os.SameFile(mainInfo, info) {
			return true
		}
	}
	return false
}

func scenarioRecord(root, id string) bool {
	for _, dir := range []string{"tasks", "tasks/drafts"} {
		data, err := os.ReadFile(filepath.Join(root, "docs", dir, id+".md"))
		if err != nil {
			continue
		}
		for _, f := range taskform.ParseForks(string(data)) {
			if f.Mode == taskform.ScenarioMode {
				return true
			}
		}
	}
	return false
}

func (s *server) scenarioContinuation(root, sid string) string {
	items := [][2]string{}
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		tree := h.Tree
		for _, q := range h.Ask.Questions {
			if q.Scenario != nil && q.Scenario.Tree != "" {
				tree = q.Scenario.Tree
			}
		}
		items = append(items, [2]string{tree, h.Ask.Task})
	}
	last := sessions.Last(s.bindsAll()[sid])
	if last.Tree != "" && s.scenarioTree(last.Tree, last.Task) {
		root = last.Tree
	}
	items = append(items, [2]string{root, last.Task})
	seen := map[[2]string]bool{}
	var blocks []string
	for _, item := range items {
		root, id := item[0], item[1]
		if seen[item] || id == "" || !scenarioRecord(root, id) {
			continue
		}
		seen[item] = true
		out, _, err := taskctlDo(root, "decide", id, "--open")
		if err != nil {
			blocks = append(blocks, fmt.Sprintf("Продолжение %s не прочитано: %s", id, err))
			continue
		}
		blocks = append(blocks, "Продолжение "+id+"\n\n"+strings.TrimSpace(out))
	}
	return strings.Join(blocks, "\n\n")
}

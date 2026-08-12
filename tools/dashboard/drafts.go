package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// Раздел «Черновики»: список накопителя, текст записи и действие «Оформить».
// Список берётся у taskctl (draft list --json), как и доска: правду про
// накопитель держит утилита, а разбирать её печать грепом хрупко. Текст
// читается файлом напрямую, как текст задачи. «Оформить» поднимает сессию
// грумминга той же механикой, что и конвейер задачи (DK-250): tmux-сессия с
// headless-сессией конвейера, свой разбор накопителя у дашборда не заводится.

// groomPrompt это заказ сессии грумминга. Слова те же, какими эту работу
// заказывают в чате: по ним скилл board-groom и разводит разбор черновика.
func groomPrompt(id string) string {
	return "Проведи груминг " + id
}

// draftSession это имя tmux-сессии грумминга. Префикс task- взят не для
// красоты: грумминг кончается строкой доски с тем же ID (taskctl add --id), и
// работа видна там же, где остальные работы проекта (liveWorks привязывает их
// префиксом доски), а стоп ей достаётся тот же самый.
func draftSession(id string) string { return "task-" + id }

func (s *server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	out, code, err := taskctlDo(found.Path, "draft", "list", "--json")
	if err != nil {
		s.logf("накопитель черновиков %s: %v", found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	var v struct {
		Drafts []json.RawMessage `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("ответ taskctl draft list --json не разобрался: %v", err)})
		return
	}
	resp := map[string]any{"project": found.Name, "drafts": v.Drafts}
	if v.Drafts == nil {
		resp["drafts"] = []json.RawMessage{}
	}
	if len(v.Drafts) == 0 {
		// Пустой список без слов неотличим от неотрисованного раздела.
		resp["note"] = fmt.Sprintf("накопитель черновиков %s пуст: записанных мимо доски идей нет", found.Name)
	}
	writeJSON(w, http.StatusOK, resp)
}

// draftPath отдаёт путь файла черновика и его путь от корня проекта.
func draftPathOf(projectPath, id string) (abs, rel string) {
	rel = draftFileRel(id)
	return filepath.Join(projectPath, filepath.FromSlash(rel)), rel
}

func (s *server) handleDraft(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	path, rel := draftPathOf(found.Path, id)
	text, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("черновика %s в %s нет: файла %s не видно, грумминг мог уже завести по нему задачу", id, found.Name, rel)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "file": rel, "text": string(text)})
}

// handleDraftGroom поднимает сессию грумминга черновика. Проверки те же и в том
// же порядке, что у запуска задачи: без tmux и без claude сессия умерла бы
// молча, а поверх живой работы с тем же ID вторую поднимать нельзя.
func (s *server) handleDraftGroom(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	path, rel := draftPathOf(found.Path, id)
	if !isFile(path) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("черновика %s в %s нет: файла %s не видно, оформлять нечего", id, found.Name, rel)})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	sess := draftSession(id)
	for _, name := range tmuxSessions() {
		if name == sess || name == "goal-"+id {
			s.logf("грумминг %s в %s отклонён: tmux-сессия %s уже идёт", id, found.Name, name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("работа %s уже идёт (tmux-сессия %s): поднимать грумминг поверх живой сессии нельзя, сначала стоп", id, name)})
			return
		}
	}
	if m := claudeMissing(); m != "" {
		s.logf("грумминг %s в %s не удался: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		"claude -p "+shQuote(groomPrompt(id))); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("грумминг %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.logf("грумминг %s в %s поднят (tmux-сессия %s)", id, found.Name, sess)
	writeJSON(w, http.StatusOK, map[string]string{
		"id": id, "kind": "task", "session": sess, "prompt": groomPrompt(id),
		"message": fmt.Sprintf("грумминг %s поднят в tmux-сессии %s: разбор доведёт черновик до строки Backlog либо снимет его с причиной", id, sess),
	})
}

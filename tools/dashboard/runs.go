package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Запуск и стоп работ: POST /api/projects/{p}/runs и DELETE
// /api/projects/{p}/runs/{id}. Запуск зовёт тот же механизм, что и руками
// (LLD DK-112): цель поднимает оболочка goal-run.py в её tmux-сессии
// goal-<ID>, одиночная задача это tmux-сессия task-<ID> с headless-сессией
// конвейера доски. Стоп это стоп сессии, ручки «пауза» нет: возобновление
// это новый запуск, читающий состояние с диска.

// goalRunRel это путь оболочки цикла цели внутри чекаута devkit. Сама
// оболочка не ставится в PATH: она python-скрипт при скилле goal-loop, и
// сервер ищет её по корням конфига, где среди проектов лежит и чекаут devkit.
const goalRunRel = "kit/skills/goal-loop/goal-run.py"

// goalRunMissing называет ненайденную оболочку: без неё цель поднимать нечем,
// и молча отвечать «не вышло» нельзя.
const goalRunMissing = "goal-run.py не нашёлся в корнях конфига (" + goalRunRel +
	"): поднимать цикл цели нечем, нужен чекаут devkit в одном из корней"

// inRoots ищет файл чекаута devkit по тем же адресам, где ищутся проекты: сам
// корень или его прямой подкаталог. Пусто, если чекаута devkit в корнях нет.
func inRoots(roots []string, rel string) string {
	for _, root := range roots {
		if p := filepath.Join(root, filepath.FromSlash(rel)); isFile(p) {
			return p
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(root, e.Name(), filepath.FromSlash(rel))
			if isFile(p) {
				return p
			}
		}
	}
	return ""
}

// goalRunPath ищет оболочку цикла цели.
func goalRunPath(roots []string) string {
	return inRoots(roots, goalRunRel)
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// notifyRel это путь уведомителя внутри чекаута devkit. Ищется он там же, где
// оболочка цикла: своего канала наружу дашборд не заводит, отправку держит
// hooks/notify.py (LLD DK-112), и стоп из дашборда зовёт его тем же порядком,
// что taskctl move зовёт его на Check.
const notifierRel = "hooks/notify.py"

// notifierMissing называет ненайденный уведомитель: стоп от этого не
// отменяется, но и молчать про непосланное нельзя, иначе лента показывает
// работающий цикл там, где его уже сняли.
const notifierMissing = "hooks/notify.py не нашёлся в корнях конфига: стоп остался без строки в ленте уведомлений"

// notifierPath ищет уведомитель по тем же адресам, что и оболочку цикла.
func notifierPath(roots []string) string {
	return inRoots(roots, notifierRel)
}

// sayStop зовёт уведомитель поводом run_stop: снятая сессия это то же событие,
// что стоп цикла маркером, и в ленте оно обязано стоять рядом с ним. Пустая
// строка значит, что сказать не вышло, и это приписка к ответу, а не отказ:
// сессия уже снята, и отменять стоп из-за уведомления нельзя.
func (s *server) sayStop(root, project, id, kind string) string {
	np := notifierPath(s.cfg.Roots)
	if np == "" {
		return notifierMissing
	}
	what := "цикл цели"
	if kind != "goal" {
		what = "конвейер задачи"
	}
	title := fmt.Sprintf("%s: %s стоп из дашборда", project, id)
	body := fmt.Sprintf("%s снят из дашборда; возобновление это новый запуск, состояние он прочтёт с диска", what)
	cmd := exec.Command("python3", np, "--reason", "run_stop", title, body)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("уведомление о стопе не отправлено: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return ""
}

// taskPrompt это промпт headless-сессии конвейера одиночной задачи: свежая
// сессия-диспетчер ведёт строку по обычным скиллам доски, как это делается
// руками, своих шагов конвейера у дашборда нет.
func taskPrompt(id string) string {
	return fmt.Sprintf("возьми задачу %s в работу и доведи её конвейером по скиллам board-task и board-ship", id)
}

// shQuote квотит строку для shell: tmux склеивает хвостовые аргументы
// new-session пробелами и отдаёт их шеллу, без кавычек промпт рассыпался бы
// на отдельные слова, как это уже решено в goal-run.py через shlex.quote.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// findRow ищет строку доски в ответе taskctl: по ней решается, цель это или
// одиночная задача. Цель узнаётся заголовком от слова «Цель:», как везде на
// доске.
func findRow(raw json.RawMessage, id string) (title string, ok bool) {
	var v struct {
		Sections []struct {
			Rows []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	for _, sec := range v.Sections {
		for _, row := range sec.Rows {
			if row.ID == id {
				return row.Title, true
			}
		}
	}
	return "", false
}

func isGoalTitle(title string) bool {
	return strings.HasPrefix(title, "Цель:")
}

// procErr разворачивает ошибку подпроцесса в слова: stderr процесса, если он
// что-то сказал, иначе сама ошибка.
func procErr(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return strings.TrimSpace(string(ee.Stderr))
	}
	return err.Error()
}

// claudeMissing называет ненайденный claude до подъёма tmux-сессии: сессия с
// ненайденной командой умерла бы молча, и стоп был бы неотличим от запуска.
func claudeMissing() string {
	if _, err := exec.LookPath("claude"); err != nil {
		return "claude не нашёлся в PATH: headless-сессию конвейера поднять нечем; " +
			"PATH launchd-агенту дописывает devkitctl doctor --fix"
	}
	return ""
}

func (s *server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"id\": \"DK-NNN\"}"})
		return
	}
	id := body.ID
	raw, err := boardJSON(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	title, ok := findRow(raw, id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("на доске %s нет строки %s", found.Name, id)})
		return
	}
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	kind := "task"
	if isGoalTitle(title) {
		kind = "goal"
	}
	sess := kind + "-" + id
	for _, name := range tmuxSessions() {
		if name == "goal-"+id || name == "task-"+id {
			s.logf("запуск %s в %s отклонён: tmux-сессия %s уже идёт", id, found.Name, name)
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("работа %s уже идёт (tmux-сессия %s): запускать поверх живой сессии нельзя, сначала стоп", id, name)})
			return
		}
	}
	if kind == "goal" {
		gr := goalRunPath(s.cfg.Roots)
		if gr == "" {
			s.logf("запуск цели %s в %s не удался: %s", id, found.Name, goalRunMissing)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": goalRunMissing})
			return
		}
		out, err := runProc("python3", gr, id, "-C", found.Path)
		if err != nil {
			text := procErr(err)
			code := http.StatusBadGateway
			var ee *exec.ExitError
			// Код 3 у оболочки это занятый замок или живая сессия: цикл уже
			// идёт, и это конфликт, а не поломка.
			if errors.As(err, &ee) && ee.ExitCode() == 3 {
				code = http.StatusConflict
			}
			s.logf("запуск цели %s в %s не удался: %s", id, found.Name, text)
			writeJSON(w, code, map[string]string{"error": text})
			return
		}
		s.logf("цель %s поднята в %s (tmux-сессия %s)", id, found.Name, sess)
		writeJSON(w, http.StatusOK, map[string]string{
			"id": id, "kind": kind, "session": sess,
			"message": strings.TrimSpace(string(out)),
		})
		return
	}
	if m := claudeMissing(); m != "" {
		s.logf("запуск задачи %s в %s не удался: %s", id, found.Name, m)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	if _, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", found.Path,
		"claude -p "+shQuote(taskPrompt(id))); err != nil {
		text := fmt.Sprintf("tmux не поднял сессию %s: %s", sess, procErr(err))
		s.logf("запуск задачи %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	s.logf("задача %s поднята в %s (tmux-сессия %s)", id, found.Name, sess)
	writeJSON(w, http.StatusOK, map[string]string{
		"id": id, "kind": kind, "session": sess,
		"message": fmt.Sprintf("конвейер задачи %s поднят в tmux-сессии %s", id, sess),
	})
}

func (s *server) handleRunStop(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if m := tmuxMissingCheck(); m != "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": m})
		return
	}
	raw, err := boardJSON(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	view, err := parseBoardView(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return
	}
	// Стоп симметричен запуску: работа ищется среди живых работ этого
	// проекта, где tmux-сессии привязаны префиксом его доски (liveWorks,
	// правило DK-217), а не в общем списке сессий машины. Иначе стоп на
	// доске demo снимал бы чужую goal-DK-777 и заводил через --say журнал
	// в чужом корне.
	var work *Work
	works := liveWorks(found.Path, view.Prefix, s.cfg.Home)
	for i := range works {
		if works[i].ID == id {
			work = &works[i]
			break
		}
	}
	if work == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("работа %s в проекте %s не идёт: нет ни tmux-сессии с префиксом его доски, ни записи в реестре целей", id, found.Name)})
		return
	}
	if work.Via == "registry" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("цикл цели %s ведётся снаружи, без tmux-сессии дашборда: стоп там, где цикл поднят", id)})
		return
	}
	kind := work.Kind
	sess := kind + "-" + id
	resp := map[string]string{"id": id, "kind": kind, "session": sess, "state": "стоп"}
	if kind == "goal" {
		// Строка про стоп идёт в журнал цикла до убийства сессии: стоп виден
		// там же, где виден ход. Провал записи стоп не отменяет, но называется.
		if gr := goalRunPath(s.cfg.Roots); gr == "" {
			resp["note"] = goalRunMissing
		} else if _, err := runProc("python3", gr, id, "-C", found.Path, "--say", "стоп из дашборда"); err != nil {
			resp["note"] = fmt.Sprintf("строка про стоп в журнал цикла не записалась: %s", procErr(err))
		}
		if resp["note"] != "" {
			s.logf("стоп %s в %s: %s", id, found.Name, resp["note"])
		}
	}
	if _, err := runProc("tmux", "kill-session", "-t", "="+sess); err != nil {
		text := fmt.Sprintf("tmux не снял сессию %s: %s", sess, procErr(err))
		s.logf("стоп %s в %s не удался: %s", id, found.Name, text)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": text})
		return
	}
	// Стоп говорит о себе тем же уведомителем, что виток и taskctl move:
	// снятая сессия иначе видна только тому, кто нажал кнопку, а лента для
	// того и заведена, чтобы стоп доехал до второго устройства.
	if note := s.sayStop(found.Path, found.Name, id, kind); note != "" {
		resp["note"] = strings.TrimPrefix(resp["note"]+"; "+note, "; ")
		s.logf("стоп %s в %s: %s", id, found.Name, note)
	}
	if kind == "goal" {
		resp["message"] = fmt.Sprintf("стоп: tmux-сессия %s снята, замок оболочки отпадёт сам по мёртвому pid; возобновление это новый запуск, виток прочтёт состояние с диска", sess)
	} else {
		resp["message"] = fmt.Sprintf("стоп: tmux-сессия %s снята; возобновление это новый запуск, конвейер прочтёт состояние с диска", sess)
	}
	s.logf("стоп %s в %s: tmux-сессия %s снята", id, found.Name, sess)
	writeJSON(w, http.StatusOK, resp)
}

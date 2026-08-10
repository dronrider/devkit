package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Сквозной прогон агентской части DoD цели DK-112: dashboard smoke поднимает
// сервер на синтетическом окружении и проходит по API ту самую цепочку, что
// названа целью, - доска со статусами и работами, запуск работы, сообщение
// цели и его подхват витком, стоп и уведомление о стопе в живой ленте, - а
// пройдя, печатает «dashboard smoke: ok».
//
// Живого ничего не задевается: свой дом во временной директории, свой корень
// с синтетическим проектом, подпроцессы (taskctl, tmux, claude, оболочка
// цикла) изображают фикстуры того же прогона, а бэкенд уведомлений подменён
// пустышкой, так что баннера на машине не появляется. Настоящий тут
// hooks/notify.py: формат его журнала это контракт ленты, и держать его
// проверенным сквозным прогоном дешевле, чем ловить расхождение глазами.
//
// Гоняется руками (dashboard smoke) и тестом (smoke_test.go): сторож остаётся
// сторожем только пока его зовут.

const smokeOK = "dashboard smoke: ok"

// smokeToken это секрет входа синтетического стенда: сервер поднимается со
// своим конфигом, и живой ~/.devkit/dashboard.local в прогоне не участвует.
const smokeToken = "smoke-token"

// smokeGoal и smokeTask это строки синтетической доски: цель поднимается
// оболочкой цикла, задача остаётся соседкой по доске, чтобы список секций был
// не из одной строки.
const (
	smokeGoal = "XR-100"
	smokeTask = "XR-002"
)

// smokeBoardJSON изображает ответ taskctl list --json: доска с целью в работе
// и задачей в Backlog.
const smokeBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[{"id":"XR-100","title":"Цель: пробный цикл smoke","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"docs/tasks/XR-100.md"}]},` +
	`{"key":"check","title":"Check","rows":[]},` +
	`{"key":"backlog","title":"Backlog","rows":[{"id":"XR-002","title":"Соседка по доске","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"S","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[]}]}`

const smokeBoardDoc = `# Синтетическая доска smoke (префикс XR)

Доска стенда: строки прогон берёт фикстурой taskctl, а этот файл держит
признак проекта, по которому дашборд ищет доски в корнях конфига.

## In progress

## Check

## Backlog

## Blocked
`

const smokeGoalDoc = `# XR-100: Цель: пробный цикл smoke

## Чего хотим

Синтетическая цель прогона smoke: на ней проверяется переписка через файл
цели и подхват сообщения витком.

## Журнал

- 2026-01-01, цель заведена прогоном smoke; continue
`

// smokeStep это шаг цепочки DoD: имя для вывода и сама проверка, которая
// возвращает строку с тем, что увидела.
type smokeStep struct {
	name string
	run  func() (string, error)
}

type smoke struct {
	dir, home, root, proj, bin string
	url                        string
	client                     *http.Client
	restore                    []func()
	items                      chan Notification
	notes                      chan string
	feedErr                    chan error
}

func (s *smoke) close() {
	for i := len(s.restore) - 1; i >= 0; i-- {
		s.restore[i]()
	}
}

func smokeWrite(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), mode)
}

// setEnv ставит переменные окружения на время прогона и отдаёт откат: смок
// гоняется и в процессе теста, и оставлять за собой чужой HOME ему нельзя.
// Пустое значение значит снять переменную.
func setEnv(kv map[string]string) func() {
	old := map[string]*string{}
	for k, v := range kv {
		if prev, ok := os.LookupEnv(k); ok {
			p := prev
			old[k] = &p
		} else {
			old[k] = nil
		}
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	return func() {
		for k, v := range old {
			if v == nil {
				os.Unsetenv(k)
				continue
			}
			os.Setenv(k, *v)
		}
	}
}

// devkitCheckout ищет чекаут devkit: настоящий hooks/notify.py прогону нужен,
// потому что формат его журнала и есть контракт ленты. Порядок тот же, что у
// taskctl: DEVKIT_HOME, дерево рядом с рабочей директорией (так смок находит
// себя, когда его зовёт go test из tools/dashboard) и ~/projects/devkit.
func devkitCheckout() string {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, v)
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "..", ".."))
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "projects", "devkit"))
	}
	for _, c := range cands {
		if isFile(filepath.Join(c, "hooks", "notify.py")) {
			return c
		}
	}
	return ""
}

// smokeFixtures кладёт исполняемые фикстуры чужих программ. tmux помнит свои
// сессии файлом: без памяти запуск и стоп не связались бы, а работа не
// появилась бы в списке живых.
func (s *smoke) fixtures() error {
	sessions := filepath.Join(s.dir, "tmux.sessions")
	for name, body := range map[string]string{
		"taskctl": fmt.Sprintf("printf '%%s\\n' %s\n", shQuote(smokeBoardJSON)),
		"tmux": fmt.Sprintf(`list=%s
case "$1" in
ls)
  [ -s "$list" ] || exit 1
  cat "$list"
  ;;
new-session)
  prev=""
  for a in "$@"; do
    [ "$prev" = "-s" ] && printf '%%s\n' "$a" >> "$list"
    prev="$a"
  done
  ;;
kill-session)
  name=${3#=}
  grep -v -x "$name" "$list" > "$list.new"
  mv "$list.new" "$list"
  ;;
esac
exit 0
`, shQuote(sessions)),
		// claude только ищется в PATH до подъёма сессии задачи, звать его
		// прогону незачем.
		"claude": "exit 0\n",
		// Бэкенд уведомлений: баннер на машине прогона не появляется, а
		// строка в журнале уведомителя пишется как при живой отправке.
		"notify-fake": "exit 0\n",
	} {
		if err := smokeWrite(filepath.Join(s.bin, name), "#!/bin/sh\n"+body, 0o755); err != nil {
			return err
		}
	}
	// Оболочка цикла: подъём заводит tmux-сессию и журнал цели, ключ --say
	// дописывает строку про стоп туда же, где виден ход.
	goalRun := fmt.Sprintf(`#!/usr/bin/env python3
import os
import sys
import time

args = sys.argv[1:]
gid = args[0]
root = args[args.index("-C") + 1] if "-C" in args else "."
log = os.path.join(root, ".devkit", "goal-%%s.log" %% gid)
os.makedirs(os.path.dirname(log), exist_ok=True)
stamp = time.strftime("%%Y-%%m-%%dT%%H:%%M:%%S")
if "--say" in args:
    line = args[args.index("--say") + 1]
    with open(log, "a", encoding="utf-8") as f:
        f.write("%%s %%s\n" %% (stamp, line))
    print(line)
else:
    with open(%s, "a", encoding="utf-8") as f:
        f.write("goal-%%s\n" %% gid)
    with open(log, "a", encoding="utf-8") as f:
        f.write("%%s цикл цели %%s начат прогоном smoke\n" %% (stamp, gid))
    print("цикл цели %%s поднят в tmux-сессии goal-%%s" %% (gid, gid))
`, pyQuote(sessions))
	return smokeWrite(filepath.Join(s.root, "devkit", "kit", "skills", "goal-loop", "goal-run.py"),
		goalRun, 0o755)
}

// pyQuote квотит строку для python-фикстуры.
func pyQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func newSmoke(dir string) (*smoke, error) {
	s := &smoke{
		dir:  dir,
		home: filepath.Join(dir, "home"),
		bin:  filepath.Join(dir, "bin"),
	}
	s.root = filepath.Join(s.home, "projects")
	s.proj = filepath.Join(s.root, "demo")
	if err := smokeWrite(filepath.Join(s.proj, "docs", "TASKS.md"), smokeBoardDoc, 0o644); err != nil {
		return nil, err
	}
	if err := smokeWrite(filepath.Join(s.proj, "docs", "tasks", smokeGoal+".md"), smokeGoalDoc, 0o644); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.bin, 0o755); err != nil {
		return nil, err
	}
	if err := s.fixtures(); err != nil {
		return nil, err
	}
	// Уведомитель настоящий: он лежит в чекауте devkit, а сервер ищет его в
	// корнях конфига, поэтому чекаут смотрит в синтетический корень своей
	// частью hooks.
	checkout := devkitCheckout()
	if checkout == "" {
		return nil, fmt.Errorf("чекаут devkit не нашёлся: прогон гоняет настоящий hooks/notify.py, " +
			"указать чекаут: DEVKIT_HOME=<путь>")
	}
	if err := os.Symlink(filepath.Join(checkout, "hooks"), filepath.Join(s.root, "devkit", "hooks")); err != nil {
		return nil, err
	}
	s.restore = append(s.restore, setEnv(map[string]string{
		"HOME": s.home,
		"PATH": s.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		// Уведомитель обязан дойти до журнала: выключатель и опрос фокуса
		// сняты, а баннер уходит в пустышку.
		"DEVKIT_NOTIFY_OFF":     "",
		"DEVKIT_NOTIFY_BACKEND": "notify-fake",
		"DEVKIT_NOTIFY_FOCUS":   "off",
	}))
	// Живые утилиты машины не зовутся: taskctl сервер ищет сначала рядом с
	// собственным бинарём, и рядом на время прогона лежит фикстура.
	oldExe := exeDir
	exeDir = func() string { return s.bin }
	s.restore = append(s.restore, func() { exeDir = oldExe })

	static, err := fs.Sub(embedded, "static")
	if err != nil {
		return nil, err
	}
	cfg := &Config{Home: s.home, Roots: []string{s.root}, Token: smokeToken}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	srv := httpServer(newServer(cfg, static, nil).handler())
	go srv.Serve(ln)
	s.restore = append(s.restore, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	s.url = "http://" + ln.Addr().String()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	s.client = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	return s, nil
}

// call ходит в API и разбирает ответ в v; не тот код ответа это ошибка шага со
// словами сервера, а не молчаливое несовпадение.
func (s *smoke) call(method, path, body string, want int, v any) error {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.url+path, rdr)
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return fmt.Errorf("%s %s: код %d, ждал %d (%s)", method, path, resp.StatusCode, want, strings.TrimSpace(string(data)))
	}
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s %s: ответ не разобрался: %v (%s)", method, path, err, strings.TrimSpace(string(data)))
	}
	return nil
}

func (s *smoke) login() error {
	return s.call("POST", "/api/login", fmt.Sprintf(`{"token": %q}`, smokeToken), http.StatusOK, nil)
}

type smokeBoard struct {
	Board struct {
		Prefix   string `json:"prefix"`
		Sections []struct {
			Key  string `json:"key"`
			Rows []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"rows"`
		} `json:"sections"`
	} `json:"board"`
	Works []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Via  string `json:"via"`
	} `json:"works"`
	Errors []string `json:"errors"`
}

func (s *smoke) board() (smokeBoard, error) {
	var v smokeBoard
	err := s.call("GET", "/api/projects/demo/board", "", http.StatusOK, &v)
	return v, err
}

// stepBoard: доска со статусами и текущими работами, первый пункт DoD.
func (s *smoke) stepBoard() (string, error) {
	var list struct {
		Projects []struct {
			Name     string         `json:"name"`
			Sections map[string]int `json:"sections"`
			Error    string         `json:"error"`
		} `json:"projects"`
	}
	if err := s.call("GET", "/api/projects", "", http.StatusOK, &list); err != nil {
		return "", err
	}
	if len(list.Projects) != 1 || list.Projects[0].Name != "demo" {
		return "", fmt.Errorf("в корне конфига ждал один проект demo, пришло %d", len(list.Projects))
	}
	if e := list.Projects[0].Error; e != "" {
		return "", fmt.Errorf("доска demo не прочиталась: %s", e)
	}
	v, err := s.board()
	if err != nil {
		return "", err
	}
	if v.Board.Prefix != "XR" {
		return "", fmt.Errorf("префикс доски %q, ждал XR", v.Board.Prefix)
	}
	rows := map[string]string{}
	for _, sec := range v.Board.Sections {
		for _, row := range sec.Rows {
			rows[row.ID] = sec.Key
		}
	}
	if rows[smokeGoal] != "in-progress" || rows[smokeTask] != "backlog" {
		return "", fmt.Errorf("строки доски не по секциям: %v", rows)
	}
	if len(v.Works) != 0 {
		return "", fmt.Errorf("до запуска работ быть не должно, пришло %d", len(v.Works))
	}
	return fmt.Sprintf("секции %v, строк %d, работ нет", list.Projects[0].Sections, len(rows)), nil
}

// stepStart: запуск работы, второй пункт DoD.
func (s *smoke) stepStart() (string, error) {
	var v struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
		Message string `json:"message"`
	}
	if err := s.call("POST", "/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, smokeGoal),
		http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.Kind != "goal" || v.Session != "goal-"+smokeGoal {
		return "", fmt.Errorf("запуск поднял не цель: kind=%q session=%q", v.Kind, v.Session)
	}
	return v.Message, nil
}

// stepWorks: поднятая работа видна доской как живая, и остановить её можно
// только зная это.
func (s *smoke) stepWorks() (string, error) {
	v, err := s.board()
	if err != nil {
		return "", err
	}
	for _, w := range v.Works {
		if w.ID == smokeGoal && w.Kind == "goal" && w.Via == "tmux" {
			return fmt.Sprintf("работа %s ведётся tmux-сессией goal-%s", w.ID, w.ID), nil
		}
	}
	return "", fmt.Errorf("поднятой работы %s в списке живых нет: %+v", smokeGoal, v.Works)
}

const smokeMessage = "прогон smoke: проверь ленту уведомлений"

// stepMessage: сообщение человека уходит в состояние цели, третий пункт DoD.
func (s *smoke) stepMessage() (string, error) {
	var v struct {
		Line    string `json:"line"`
		Message string `json:"message"`
		Note    string `json:"note"`
	}
	if err := s.call("POST", "/api/projects/demo/goals/"+smokeGoal+"/message",
		fmt.Sprintf(`{"text": %q}`, smokeMessage), http.StatusOK, &v); err != nil {
		return "", err
	}
	var pending struct {
		Pending []string `json:"pending"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/message", "",
		http.StatusOK, &pending); err != nil {
		return "", err
	}
	if len(pending.Pending) != 1 || !strings.Contains(pending.Pending[0], smokeMessage) {
		return "", fmt.Errorf("сообщение не легло в «Входящие»: %v", pending.Pending)
	}
	// Виток читает файл цели, а не ответ API: сообщение обязано лежать там,
	// откуда он его берёт первым шагом.
	doc, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	if lines := inboxLines(string(doc)); len(lines) != 1 {
		return "", fmt.Errorf("во «Входящих» файла цели %d строк, ждал одну", len(lines))
	}
	note := "коммит прошёл"
	if v.Note != "" {
		// Синтетический проект не репозиторий, и провал коммита тут штатен:
		// важно, что сервер называет его словами, а не глотает.
		note = "правка на месте, git назван словами"
	}
	return fmt.Sprintf("строка «%s» ждёт витка, %s", v.Line, note), nil
}

func (s *smoke) goalPath() string {
	return filepath.Join(s.proj, "docs", "tasks", smokeGoal+".md")
}

// stepTurn: подхват сообщения витком, четвёртый пункт DoD. Живого витка тут
// нет, его изображает та же правка файла цели, которую делает шаг 5 скилла
// goal-loop: строка уходит из «Входящих», а в «Журнал» встаёт запись про
// подхват. Проверяется этим ровно стык дашборда с витком: сообщение лежит
// там, откуда виток его читает, и убранное перестаёт числиться ждущим.
func (s *smoke) stepTurn() (string, error) {
	doc, err := os.ReadFile(s.goalPath())
	if err != nil {
		return "", err
	}
	taken := inboxLines(string(doc))
	if len(taken) != 1 {
		return "", fmt.Errorf("витку нечего подхватывать: %v", taken)
	}
	out := []string{}
	for _, ln := range strings.Split(string(doc), "\n") {
		if strings.TrimSpace(ln) == "- "+taken[0] {
			continue
		}
		out = append(out, ln)
	}
	turn := fmt.Sprintf("- %s, виток прогона smoke подхватил сообщение с дашборда; continue",
		time.Now().Format("2006-01-02"))
	doc2 := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n" + turn + "\n"
	if err := os.WriteFile(s.goalPath(), []byte(doc2), 0o644); err != nil {
		return "", err
	}
	var pending struct {
		Pending []string `json:"pending"`
		Note    string   `json:"note"`
	}
	if err := s.call("GET", "/api/projects/demo/goals/"+smokeGoal+"/message", "",
		http.StatusOK, &pending); err != nil {
		return "", err
	}
	if len(pending.Pending) != 0 {
		return "", fmt.Errorf("подхваченное сообщение всё ещё ждёт витка: %v", pending.Pending)
	}
	if pending.Note == "" {
		return "", fmt.Errorf("опустевшие «Входящие» остались без слов: пустота неотличима от поломки")
	}
	return "сообщение подхвачено записью витка, дашборд говорит: " + pending.Note, nil
}

// openFeed открывает живую ленту и разбирает её поток в фоне: события и
// пустоты складываются по своим каналам, чтобы шаг стопа ждал именно
// уведомления, а не любой строки.
func (s *smoke) openFeed() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.restore = append(s.restore, cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", s.url+"/api/notifications?stream=1&kind=stop", nil)
	if err != nil {
		return err
	}
	// Своего срока у потока нет: он живёт до конца прогона.
	client := &http.Client{Jar: s.client.Jar}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		resp.Body.Close()
		return fmt.Errorf("лента ответила не потоком, а %q", ct)
	}
	s.items = make(chan Notification, 32)
	s.notes = make(chan string, 8)
	s.feedErr = make(chan error, 1)
	go func() {
		defer resp.Body.Close()
		r := bufio.NewReader(resp.Body)
		event, data := "", ""
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				s.feedErr <- err
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				if event == "note" {
					s.notes <- data
				} else {
					var n Notification
					if json.Unmarshal([]byte(data), &n) == nil {
						s.items <- n
					}
				}
				event, data = "", ""
			}
		}
	}()
	return nil
}

// stepFeedOpen: лента открыта потоком до стопа, иначе «без перезагрузки
// страницы» проверить нечем.
func (s *smoke) stepFeedOpen() (string, error) {
	if err := s.openFeed(); err != nil {
		return "", err
	}
	select {
	case note := <-s.notes:
		return "поток открыт, пустота названа словами: " + note, nil
	case n := <-s.items:
		return "поток открыт, в хвосте уже лежит " + n.Title, nil
	case err := <-s.feedErr:
		return "", fmt.Errorf("поток оборвался: %v", err)
	case <-time.After(10 * time.Second):
		return "", fmt.Errorf("лента молчит: за 10с не пришло ни события, ни слов про пустоту")
	}
}

// stepStop: стоп работы, пятый пункт DoD. Стоп идёт по тому же API, что и
// кнопкой с телефона.
func (s *smoke) stepStop() (string, error) {
	var v struct {
		State   string `json:"state"`
		Message string `json:"message"`
		Note    string `json:"note"`
	}
	if err := s.call("DELETE", "/api/projects/demo/runs/"+smokeGoal, "", http.StatusOK, &v); err != nil {
		return "", err
	}
	if v.State != "стоп" {
		return "", fmt.Errorf("стоп ответил состоянием %q", v.State)
	}
	if v.Note != "" {
		return "", fmt.Errorf("стоп прошёл с припиской: %s", v.Note)
	}
	log, err := os.ReadFile(filepath.Join(s.proj, ".devkit", "goal-"+smokeGoal+".log"))
	if err != nil {
		return "", fmt.Errorf("журнал цикла не прочитался: %v", err)
	}
	if !strings.Contains(string(log), "стоп из дашборда") {
		return "", fmt.Errorf("строки про стоп нет в журнале цикла: %s", log)
	}
	board, err := s.board()
	if err != nil {
		return "", err
	}
	if len(board.Works) != 0 {
		return "", fmt.Errorf("после стопа работа всё ещё живая: %+v", board.Works)
	}
	return "сессия снята, строка про стоп в журнале цикла, живых работ нет", nil
}

// stepFeedStop: уведомление о стопе доезжает в открытую ленту, последний
// пункт DoD.
func (s *smoke) stepFeedStop() (string, error) {
	deadline := time.After(30 * time.Second)
	for {
		select {
		case n := <-s.items:
			if n.Kind != "stop" || n.ID != smokeGoal {
				continue
			}
			delivered := "баннер ушёл бэкендом " + n.Backend
			if !n.Sent {
				// Прогон живёт в песочнице, и уведомитель туда баннера не
				// шлёт: событие в ленте от этого не пропадает, а причина
				// стоит рядом с ним.
				delivered = "баннера не было: " + n.Result
			}
			return fmt.Sprintf("«%s» (повод %s, %s)", n.Title, n.Reason, delivered), nil
		case err := <-s.feedErr:
			return "", fmt.Errorf("поток оборвался до уведомления: %v", err)
		case <-deadline:
			return "", fmt.Errorf("уведомления о стопе %s в ленте не дождались за 30с", smokeGoal)
		}
	}
}

// cmdSmoke проходит цепочку DoD и печатает ход по шагам: провалившийся шаг
// называет себя и причину, а не оставляет один ненулевой код.
func cmdSmoke(out io.Writer, keep bool) error {
	dir, err := os.MkdirTemp("", "dashboard-smoke-")
	if err != nil {
		return err
	}
	if keep {
		fmt.Fprintf(out, "окружение прогона остаётся в %s\n", dir)
	} else {
		defer os.RemoveAll(dir)
	}
	s, err := newSmoke(dir)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.login(); err != nil {
		return fmt.Errorf("вход: %v", err)
	}
	steps := []smokeStep{
		{"доска со статусами и работами", s.stepBoard},
		{"запуск работы", s.stepStart},
		{"работа видна живой", s.stepWorks},
		{"сообщение цели", s.stepMessage},
		{"подхват сообщения витком", s.stepTurn},
		{"живая лента открыта", s.stepFeedOpen},
		{"стоп работы", s.stepStop},
		{"уведомление о стопе в ленте", s.stepFeedStop},
	}
	for i, st := range steps {
		note, err := st.run()
		if err != nil {
			return fmt.Errorf("шаг %d (%s): %v", i+1, st.name, err)
		}
		fmt.Fprintf(out, "шаг %d, %s: %s\n", i+1, st.name, note)
	}
	fmt.Fprintln(out, smokeOK)
	return nil
}

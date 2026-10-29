package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/chat"

	"github.com/dronrider/devkit/internal/drafttx"
)

// Разговор цели с её задачами поверх основных чатов (LLD DK-1422, стык
// с DK-1214): решение цели едет диспетчерам затронутых задач безадресной
// строкой во вход записи, а ответ диспетчера задачи возвращается адресно
// голове носителя цели. Отдельного транспорта у связи нет: входы записей
// и «Входящие» файла цели это и есть канал, и подхват реплики доставляет
// строки тем же порядком, что и реплики человека.

// relayFromTask это подпись строки от диспетчера задачи в «Входящих» цели:
// по ней строка отличается от сообщений дашборда и рукописных, а виток
// видит, откуда приехал ответ.
const relayFromTask = ", от задачи "

// goalRowOf называет цель задачи: строка цели, чья нарезка в «Задачи цели»
// файла цели называет эту задачу. Состав живёт в файле цели и нигде больше,
// поэтому и связь читается оттуда. Порядок обзора целей алфавитный: задача
// в двух целях редкость, а детерминированный выбор лучше случайного.
func (s *server) goalRowOf(projectPath, id string) string {
	raw, err := s.projectBoard(projectPath)
	if err != nil {
		return ""
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return ""
	}
	view, err := parseBoardView(raw)
	if err != nil {
		return ""
	}
	var goals []string
	for rowID, row := range rows {
		if isGoalTitle(row.Title) {
			goals = append(goals, rowID)
		}
	}
	sort.Strings(goals)
	for _, goal := range goals {
		for _, t := range s.goalTaskIDs(projectPath, goal, view.Prefix) {
			if t == id {
				return goal
			}
		}
	}
	return ""
}

// goalTaskIDs называет задачи цели из её файла: тот же разбор нарезки, каким
// живёт состав на экране цели, без второго мнения о формате.
func (s *server) goalTaskIDs(projectPath, goal, prefix string) []string {
	doc := s.goalDocPath(projectPath, goal)
	if !doc.seen {
		return nil
	}
	data, err := os.ReadFile(doc.path)
	if err != nil {
		return nil
	}
	tasks, _ := goalTasksFromDoc(string(data), prefix)
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

// handleGoalDecide доставляет решение цели диспетчерам затронутых задач:
// безадресная строка ложится во вход записи task-<ID> в обоих деревьях
// задачи, и её забирает голова-носитель основного чата (LLD DK-1422,
// решение 4). Состав затронутых задач называется запросом, а без него
// берутся все задачи цели со строки доски: закрытой задаче диспетчера нет,
// и решение ей везти некому. Подтверждением получения служит подъём строки
// подхватом, а пересмотр поручения делает сам диспетчер задачи.
func (s *server) handleGoalDecide(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, path, rel, proof, ok := s.goalFile(w, r)
	if !ok {
		return
	}
	var body struct {
		Text  string   `json:"text"`
		Tasks []string `json:"tasks"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("решение длиннее предела %d КБ: задачам везётся короткая строка", msgBodyLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\", \"tasks\": [\"...\"]}"})
		return
	}
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустое решение доставлять некому: жду JSON {\"text\": \"...\"}"})
		return
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return
	}
	view, _ := parseBoardView(raw)
	tx, err := drafttx.Acquire(found.Path)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if !goalFileStillSelected(found.Path, id, rel, proof) {
		tx.Close()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "путь цели изменился, откройте запись снова"})
		return
	}
	data, err := os.ReadFile(path)
	tx.Close()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл цели не прочитался: %v", err)})
		return
	}
	fromDoc, _ := goalTasksFromDoc(string(data), view.Prefix)
	member := map[string]bool{}
	for _, t := range fromDoc {
		member[t.ID] = true
	}
	// Затронутые задачи: названные запросом либо весь состав цели со строками
	// доски. Названная задача проверяется членством: решение чужой задаче
	// везла бы связь, которой нет, и человек узнал бы об этом молчанием.
	want := make([]string, 0, len(body.Tasks))
	for _, t := range body.Tasks {
		t = strings.ToUpper(strings.TrimSpace(t))
		if t == "" || wantHas(want, t) {
			continue
		}
		if !member[t] {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("задача %s не из состава цели %s: «Задачи цели» её не называет", t, id)})
			return
		}
		// Строка доски спрашивается и у названной задачи: закрытой и не
		// нарезанной задаче диспетчера нет, и строка во входе ждала бы
		// читателя, которого не будет.
		if _, hit := rows[t]; !hit {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("задача %s без строки доски: диспетчера у неё нет, решение везти некому", t)})
			return
		}
		want = append(want, t)
	}
	if len(want) == 0 {
		for _, t := range fromDoc {
			if _, hit := rows[t.ID]; hit {
				want = append(want, t.ID)
			}
		}
	}
	if len(want) == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("у цели %s нет задач со строкой доски: доставлять решение некому", id)})
		return
	}
	line := "решение цели " + id + ": " + text
	sent := []map[string]any{}
	for _, tid := range want {
		delivered, dup, note := s.decideDeliver(found.Path, tid, line)
		item := map[string]any{"id": tid}
		switch {
		case note != "":
			item["note"] = note
		case dup && len(delivered) == 0:
			item["dup"] = true
		default:
			item["trees"] = decideTreeNames(delivered)
		}
		sent = append(sent, item)
	}
	ids := make([]string, 0, len(sent))
	for _, item := range sent {
		ids = append(ids, item["id"].(string))
	}
	s.logf("решение цели %s в %s доставлено задачам %s", id, found.Name, strings.Join(ids, ", "))
	writeJSON(w, http.StatusOK, map[string]any{
		"goal": id, "text": line, "sent": sent,
		"message": fmt.Sprintf("решение легло во входы записей %s: подтверждением служит подъём строки диспетчером задачи, ответ диспетчеру цели идёт ручкой задачи",
			strings.Join(ids, ", "))})
}

// decideDeliver кладёт строку решения во вход записи задачи обоих её деревьев.
// Возвращаются деревья, куда строка легла, признак уже лежащей строки и
// причина отказа, когда строка не легла нигде. Уже лежащая строка это
// дедуп-успех, как у ручек чата: второй раз строка не заводится, а подхват
// заберёт одну. Боковое дерево спрашивается по имени: дерево задачи это
// <проект>-<id>, и без него строка лежит в основном чекауте, где её читает
// голова, поднятая панелью.
func (s *server) decideDeliver(projectPath, id, text string) ([]string, bool, string) {
	var put []string
	dup := false
	for _, tree := range askTrees(projectPath, id) {
		if fi, err := os.Stat(tree); err != nil || !fi.IsDir() {
			continue
		}
		lying, _, err := putChat(tree, chat.TaskName(id), text, chat.TaskLine(s.now(), text))
		if err != nil {
			return nil, false, fmt.Sprintf("вход записи в %s не взял строку: %v", tree, err)
		}
		if lying != "" {
			dup = true
			continue
		}
		put = append(put, tree)
	}
	if len(put) == 0 && !dup {
		return nil, false, "дерева задачи нет: строки ждать некому"
	}
	return put, dup, ""
}

func wantHas(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// handleTaskRelay несёт строку диспетчера задачи диспетчеру цели: вопрос
// о составе цели, соседних задачах и общем подходе, подтверждение получения
// решения, пересмотр поручения. У идущей цели строка ложится адресно в личный
// вход витка sess-<sid>: её берёт только названный виток, и уезжающей голове
// она не достаётся (LLD DK-1422, решение 4). Стоящей цели строка ложится
// в «Входящие» файла цели, откуда её читает следующий виток.
func (s *server) handleTaskRelay(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, ok := s.taskChatRow(w, r)
	if !ok {
		return
	}
	var body struct {
		Goal string `json:"goal"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, msgBodyLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("строка длиннее предела %d КБ: диспетчеру цели везётся короткий текст", msgBodyLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"goal\": \"...\", \"text\": \"...\"}"})
		return
	}
	text := strings.Join(strings.Fields(body.Text), " ")
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустую строку диспетчеру цели не везти: жду JSON {\"text\": \"...\"}"})
		return
	}
	goal := strings.ToUpper(strings.TrimSpace(body.Goal))
	if goal == "" {
		goal = s.goalRowOf(found.Path, id)
	}
	if goal == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("задача %s ни в одной цели: строке некому ехать, прямой вопрос остаётся в чате задачи", id)})
		return
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	row, hit := findRow(raw, goal)
	if !hit {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("на доске %s нет строки цели %s", found.Name, goal)})
		return
	}
	if !isGoalTitle(row.Title) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%s не цель: заголовок не начинается с «Цель:», а строка диспетчера задачи едет только цели", goal)})
		return
	}
	said := fmt.Sprintf("от диспетчера задачи %s: %s", id, text)
	if turn := goalTurn(found.Path, goal); turn != "" {
		name := "sess-" + turn
		if _, _, err := putChat(found.Path, name, said, chat.Line(s.now(), turn, said)); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": fmt.Sprintf("вход витка цели %s не взял строку: %v", goal, err)})
			return
		}
		s.logf("строка от задачи %s уехала диспетчеру цели %s: вход витка %s адресно", id, goal, turn)
		writeJSON(w, http.StatusOK, map[string]any{"task": id, "goal": goal, "way": "turn", "session": turn,
			"message": fmt.Sprintf("строка легла адресно во вход витка цели %s: её возьмёт виток %s, и только он", goal, turn)})
		return
	}
	doc := s.goalDocPath(found.Path, goal)
	if !doc.seen {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("файла цели %s в %s нет: строке диспетчера задачи некуда лечь", doc.rel, found.Name)})
		return
	}
	lock := s.inboxLock(found.Path)
	lock.Lock()
	defer lock.Unlock()
	tx, err := drafttx.Acquire(found.Path)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	defer func() {
		if tx != nil {
			tx.Close()
		}
	}()
	data, err := os.ReadFile(doc.path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл цели не прочитался: %v", err)})
		return
	}
	line := "- " + s.now().Format("2006-01-02 15:04") + relayFromTask + id + ": " + text
	for _, lying := range inboxLines(string(data)) {
		if strings.HasSuffix(lying, text) {
			writeJSON(w, http.StatusOK, map[string]any{"task": id, "goal": goal, "way": "inbox", "dup": true,
				"message": fmt.Sprintf("такая строка уже лежит во «Входящих» цели %s: второй не завожу, виток прочитает одну", goal)})
			return
		}
	}
	if err := os.WriteFile(doc.path, []byte(addInboxLine(string(data), line)), 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл цели не записался: %v", err)})
		return
	}
	if err := tx.Close(); err != nil {
		tx = nil
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	tx = nil
	resp := map[string]any{"task": id, "goal": goal, "way": "inbox", "line": line,
		"message": fmt.Sprintf("строка легла во «Входящие» файла цели %s: её прочитает следующий виток", goal)}
	if s.goalIdle(found.Path, goal) {
		resp["idle"] = true
		resp["message"] = idlePut(goal)
	}
	if note := commitDocs(found.Path,
		fmt.Sprintf("docs(tasks): %s строка от задачи %s во «Входящие»", goal, id), doc.rel); note != "" {
		resp["note"] = note
		s.logf("строка от задачи %s к цели %s: %s", id, goal, note)
	}
	s.logf("строка от задачи %s легла во «Входящие» цели %s", id, goal)
	writeJSON(w, http.StatusOK, resp)
}

// decideTreeNames красит деревья доставки короткими именами для ответа ручки:
// абсолютный путь в сообщении человеку ни к чему, а различить основной
// чекаут и дерево задачи по basename можно.
func decideTreeNames(trees []string) []string {
	out := make([]string, 0, len(trees))
	for _, t := range trees {
		out = append(out, filepath.Base(t))
	}
	return out
}

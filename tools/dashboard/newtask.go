package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

// Заведение задачи и черновика с дашборда (DK-245). Путь по умолчанию это
// черновик: мысль приходит вне машины, а полная строка требует ранга, и
// считать его с телефона неудобно. Обе команды гоняет taskctl подпроцессом,
// ID новой строки и черновика выдаёт он же (нумерация сквозная, и своей у
// сервера быть не может), а коммит доски идёт тем же порядком, что у правок.

// draftTextLimit ограничивает мысль, записываемую на ходу: это несколько
// абзацев в накопитель, а не вложение.
const draftTextLimit = 16 << 10

// newIDRe вынимает ID из ответа утилиты: и add, и draft начинают сообщение с
// выданного ID, и брать его больше неоткуда, номер выдаётся при записи.
var newIDRe = regexp.MustCompile(`^[A-Za-z]+-[0-9]+`)

func draftFileRel(id string) string {
	return filepath.ToSlash(filepath.Join("docs", "tasks", "drafts", id+".md"))
}

// handleDraftPost записывает сырую мысль черновиком. Каталога накопителя на
// проекте может не быть вовсе, и это не отказ: заводит его первая же команда
// draft сама.
func (s *server) handleDraftPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftTextLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("текст длиннее предела %d КБ: в черновик кладётся мысль, а не вложение", draftTextLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустой черновик записывать нечего: жду JSON {\"text\": \"...\"}"})
		return
	}
	// Текст едет на вход подпроцесса, а не аргументом: аргумент проходит разбор
	// флагов и стража подкоманд taskctl, и мысль из одного слова латиницей либо
	// начатая с дефиса потерялась бы там целиком.
	out, code, err := taskctlDoIn(found.Path, text, "draft")
	if err != nil {
		s.logf("черновик в %s не записался: %v", found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"message": out}
	id := newIDRe.FindString(out)
	if id == "" {
		// ID выдаёт утилита, и без него нечего ни коммитить, ни открывать:
		// файл на месте, а сказать про это надо словами.
		resp["note"] = "ID черновика не нашёлся в ответе taskctl: файл записан, но коммит доски не сделан"
		s.logf("черновик в %s записан, но ID не разобрался: %s", found.Name, out)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	rel := draftFileRel(id)
	resp["id"], resp["file"] = id, rel
	if note := commitDocs(found.Path, boardCommitMsg(id, "черновик записан с дашборда"), rel); note != "" {
		resp["note"] = note
		s.logf("черновик %s в %s: %s", id, found.Name, note)
	}
	s.logf("черновик %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// handleTaskCreate заводит полную строку доски: заголовок, тип, цена и
// слагаемые ранга уезжают в taskctl add, а ранг, бакет P и место строки в
// Backlog утилита считает сама. Файл задачи заводится тут же по флагу, той же
// командой, что и кнопка «Завести файл»: она чинит и ссылку в строке.
func (s *server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	var body struct {
		Title  string `json:"title"`
		Type   string `json:"type"`
		Rank   string `json:"rank"`
		RParts []*int `json:"r_parts"`
		Cost   string `json:"cost"`
		File   bool   `json:"file"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON с полями title, type, r_parts (или rank), cost, file"})
		return
	}
	rank := body.Rank
	if body.RParts != nil {
		if rank != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "жду либо rank строкой «а+б+в+г+д», либо r_parts списком слагаемых, но не оба сразу"})
			return
		}
		// У новой строки прежних значений нет, и пропущенное слагаемое это
		// ноль, а не «оставить как было».
		parts, err := rankArg(body.RParts, nil)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rank = parts
	}
	typ := body.Type
	if typ == "" {
		typ = "task"
	}
	if refusal := bugPartRefusal(typ, parseRank(rank)); refusal != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": refusal})
		return
	}
	args := []string{"add", "--title", strings.TrimSpace(body.Title), "--type", typ, "--rank", rank}
	if body.Cost != "" {
		args = append(args, "--cost", body.Cost)
	}
	// Кривой ранг, пустой заголовок и незнакомый тип отбивает утилита своими
	// словами: правду про формат строки держит она.
	out, code, err := taskctlDo(found.Path, args...)
	if err != nil {
		s.logf("строка в %s не завелась: %v", found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"message": out}
	id := newIDRe.FindString(out)
	if id == "" {
		resp["note"] = "ID новой строки не нашёлся в ответе taskctl: строка на доске, но коммит доски не сделан"
		s.logf("строка в %s заведена, но ID не разобрался: %s", found.Name, out)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["id"] = id
	paths := []string{filepath.ToSlash(filepath.Join("docs", "TASKS.md"))}
	if body.File {
		// Файл заводится после строки: не завёлся он, строка всё равно на
		// доске, и молчать про это нельзя.
		if fileOut, _, ferr := taskctlDo(found.Path, "file", id); ferr != nil {
			resp["note"] = "строка заведена, а файл задачи нет: " + ferr.Error()
			s.logf("файл задачи %s в %s не завёлся: %v", id, found.Name, ferr)
		} else {
			resp["file"] = taskFileRel(id)
			paths = append(paths, taskFileRel(id))
			s.logf("файл задачи %s в %s: %s", id, found.Name, fileOut)
		}
	}
	what := "строка заведена с дашборда"
	if _, hit := resp["file"]; hit {
		what = "строка и файл задачи заведены с дашборда"
	}
	if note := commitDocs(found.Path, boardCommitMsg(id, what), paths...); note != "" {
		// Провал коммита не затирает причину, по которой не завёлся файл: обе
		// беды называются вместе.
		if had := resp["note"]; had != "" {
			note = had + "; " + note
		}
		resp["note"] = note
		s.logf("заведение %s в %s: %s", id, found.Name, note)
	}
	s.logf("заведение %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

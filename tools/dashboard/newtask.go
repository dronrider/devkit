package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

// draftPrios это шкала уровня разбора черновика: латинские имена те же, что у
// флага taskctl draft --prio, русские слова живут на экране накопителя.
var draftPrios = map[string]bool{"high": true, "mid": true, "low": true}

// handleDraftPost записывает сырую мысль черновиком. Каталога накопителя на
// проекте может не быть вовсе, и это не отказ: заводит его первая же команда
// draft сама.
func (s *server) handleDraftPost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "черновик")
	if found == nil {
		return
	}
	var body struct {
		Text string `json:"text"`
		Prio string `json:"prio"`
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
			"error": "пустой черновик записывать нечего: жду JSON {\"text\": \"...\", \"prio\": \"mid\"}"})
		return
	}
	// Уровень разбора спрашивается на записи, а не в грумминге (DK-520): без
	// него taskctl отказал бы уже из подпроцесса, и отказ пришёл бы на экран
	// строкой про форму команды, которую с дашборда никто не набирает.
	prio := strings.TrimSpace(body.Prio)
	if !draftPrios[prio] {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду уровень разбора: prio high|mid|low, оценка грубая и на глаз"})
		return
	}
	// Текст едет на вход подпроцесса, а не аргументом: аргумент проходит разбор
	// флагов и стража подкоманд taskctl, и мысль из одного слова латиницей либо
	// начатая с дефиса потерялась бы там целиком.
	out, code, err := s.taskctlWriteIn(found.Path, text, "draft", "--prio", prio)
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

// writeAcceptReason вписывает причину непригодности обхода в строку барьера
// раздела «Приёмка» файла задачи: «- барьер «глаза»:» получает текст причины.
// Файл с разделом заводит taskctl add для не агентского вида, и без него
// вписывать нечего.
func writeAcceptReason(proj, id, reason string) error {
	path := filepath.Join(proj, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("файл задачи не читается, причина приёмки не записана: %w", err)
	}
	at := bytes.Index(data, []byte("## Приёмка"))
	if at < 0 {
		return errors.New("в файле задачи нет раздела «Приёмка»: причина не записана")
	}
	// Ищется строка барьера после заголовка раздела, а не с начала файла:
	// слово «барьер» в постановке задачи встречается и раньше приёмки.
	lineAt := bytes.Index(data[at:], []byte("- барьер «"))
	if lineAt < 0 {
		return errors.New("в разделе «Приёмка» нет строки барьера: причина не записана")
	}
	line := at + lineAt
	rest := data[line:]
	end := bytes.IndexByte(rest, '\n')
	if end < 0 {
		end = len(rest)
	}
	updated := append([]byte{}, data[:line]...)
	updated = append(updated, rest[:end]...)
	updated = append(updated, []byte(" "+reason)...)
	updated = append(updated, data[line+end:]...)
	return os.WriteFile(path, updated, 0o644)
}

// handleTaskCreate заводит полную строку доски: заголовок, тип, цена и
// слагаемые ранга уезжают в taskctl add, а ранг, бакет P и место строки в
// Backlog утилита считает сама. Файл задачи заводит тот же add (DK-394):
// строка без файла это дыра, и ручка не предлагает её заведение без файла.
func (s *server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "заведение задачи")
	if found == nil {
		return
	}
	var body struct {
		Title   string `json:"title"`
		Type    string `json:"type"`
		Rank    string `json:"rank"`
		RParts  []*int `json:"r_parts"`
		Cost    string `json:"cost"`
		Accept  string `json:"accept"`
		Barrier string `json:"barrier"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON с полями title, type, r_parts (или rank), cost"})
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
	// Вид приёмки обязателен (DK-301), и умолчание агентское стоит на ручке:
	// форма шлёт его сама, а кривой вид отбивает утилита своими словами.
	accept := body.Accept
	if accept == "" {
		accept = "agent"
	}
	args := []string{"add", "--title", strings.TrimSpace(body.Title), "--type", typ,
		"--rank", rank, "--accept", accept}
	if accept != "agent" {
		args = append(args, "--barrier", body.Barrier)
	}
	if body.Cost != "" {
		args = append(args, "--cost", body.Cost)
	}
	// Кривой ранг, пустой заголовок и незнакомый тип отбивает утилита своими
	// словами: правду про формат строки держит она.
	out, code, err := s.taskctlWrite(found.Path, args...)
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
	rel := taskFileRel(id)
	resp["file"] = rel
	paths := []string{filepath.ToSlash(filepath.Join("docs", "TASKS.md")), rel}
	// Причина непригодности обхода стоит в разделе «Приёмка» рядом с барьером
	// (LLD DK-292, решение 1): перебор обходов допишет исполнитель, а эту
	// строку уже на месте читает ревьювер. У агентского вида барьера нет, и
	// причина не пишется.
	if reason := strings.TrimSpace(body.Reason); reason != "" && accept != "agent" {
		if ferr := writeAcceptReason(found.Path, id, reason); ferr != nil {
			resp["note"] = ferr.Error()
			s.logf("причина приёмки %s в %s не записалась: %v", id, found.Name, ferr)
		}
	}
	what := "строка и файл задачи заведены с дашборда"
	if note := commitDocs(found.Path, boardCommitMsg(id, what), paths...); note != "" {
		// Провал коммита не затирает причину непригодности обхода, которая
		// не записалась в раздел «Приёмка»: обе беды называются вместе.
		if had := resp["note"]; had != "" {
			note = had + "; " + note
		}
		resp["note"] = note
		s.logf("заведение %s в %s: %s", id, found.Name, note)
	}
	s.logf("заведение %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

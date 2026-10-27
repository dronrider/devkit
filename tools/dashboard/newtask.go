package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
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

// titleNewlineRe ловит перенос строки внутри заголовка черновика: поле формы
// это textarea без запрета на Enter (DK-447, ревью), и заголовок с переносом
// посередине проходит и подсветку формы, и порог ниже, а до накопителя
// доезжает первой строкой только обрывок, остальное утекает в тело перед
// «### Ситуация» без предупреждения, потому что taskctl (form.go,
// draftTitleGuard) берёт заголовком текст только до первого \n.
var titleNewlineRe = regexp.MustCompile(`\r\n|\r|\n`)

// paraSplitRe режет текст на абзацы по пустой строке: пробелы и табуляция в
// строке-разделителе не мешают, телефон оставляет их на концах строк.
var paraSplitRe = regexp.MustCompile(`\n[ \t]*\n`)

// quickEntrySplit режет сырой текст быстрой записи на заголовок и тело.
// Заголовок это первый абзац: мысль с телефона приходит одним куском, и
// спрашивать у неё отдельное поле заголовка значило терять запись на пороге
// первой строки (находка DK-447). Абзац длиннее потолка не отбивается, а
// режется: концом предложения, если оно уместилось, иначе краем слова, иначе
// потолком, и отрезанное остаётся телом. Так запись проходит целиком, а
// утилита записи получает заголовок в её собственном потолке
// (taskform.DraftTitleLimit).
func quickEntrySplit(text string) (title, body string) {
	text = strings.TrimSpace(titleNewlineRe.ReplaceAllString(text, "\n"))
	if text == "" {
		return "", ""
	}
	paras := paraSplitRe.Split(text, -1)
	first := strings.Join(strings.Fields(strings.ReplaceAll(paras[0], "\n", " ")), " ")
	rest := strings.TrimSpace(strings.Join(paras[1:], "\n\n"))
	runes := []rune(first)
	if len(runes) <= taskform.DraftTitleLimit {
		return first, rest
	}
	limit := runes[:taskform.DraftTitleLimit]
	cut := -1
	for i, r := range limit {
		if r == '.' || r == '!' || r == '?' {
			cut = i + 1
		}
	}
	if cut <= 0 {
		for i := len(limit) - 1; i > 0; i-- {
			if limit[i] == ' ' {
				cut = i
				break
			}
		}
	}
	if cut <= 0 {
		cut = taskform.DraftTitleLimit
	}
	title = strings.TrimSpace(string(runes[:cut]))
	tail := strings.TrimSpace(string(runes[cut:]))
	if tail == "" {
		return title, rest
	}
	if rest == "" {
		return title, tail
	}
	return title, tail + "\n\n" + rest
}

func draftFileRel(id string) string {
	return filepath.ToSlash(filepath.Join("docs", "tasks", "drafts", id+".md"))
}

// draftPrios это шкала уровня разбора черновика: латинские имена те же, что у
// флага taskctl draft --prio, русские слова живут на экране накопителя.
var draftPrios = map[string]bool{"high": true, "mid": true, "low": true}

// handleDraftPost записывает сырую мысль черновиком. Каталога накопителя на
// проекте может не быть вовсе, и это не отказ: заводит его первая же команда
// draft сама. Заголовок и тело приходят с формы отдельными полями (DK-447,
// черновик DK-1252) и собираются тут в тот же вид, что ждёт taskctl draft на
// stdin: первая строка заголовок, дальше пустая строка и тело. Порог
// заголовка читается из internal/taskform, общего с утилитой записи, а не
// дублируется числом здесь.
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
		Title string `json:"title"`
		Body  string `json:"body"`
		Prio  string `json:"prio"`
		// Text это сырой текст быстрой записи с главной (DK-1465): один
		// абзац мысли без полей. Заголовок и тело собираются из него самим
		// сервером (quickEntrySplit), и порог первой строки запись не
		// отбивает: с телефона мысль приходит одним куском, и требование
		// уложить её в строку заголовка теряло запись целиком (DK-447,
		// находка из макета). Пусто у формы экрана заведения.
		Text string `json:"text"`
		// Chat это разговор панели, из которого черновик оформили: свободная
		// беседа становится основным чатом записи источником «оформление»
		// (LLD DK-1422). Пустое поле значит, что беседы не было, и назначение
		// не пишется.
		Chat string `json:"chat"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftTextLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("текст длиннее предела %d КБ: в черновик кладётся мысль, а не вложение", draftTextLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"title\": \"...\", \"body\": \"...\"}"})
		return
	}
	// Быстрая запись собирается в заголовок с телом до всех проверок ниже.
	// Уровень разбора она не спрашивает вовсе: вопрос «разбирать ли сегодня»
	// решает вторая кнопка формы, а без ответа уровень середина, та же, что
	// умолчание формы заведения.
	if strings.TrimSpace(body.Text) != "" {
		body.Title, body.Body = quickEntrySplit(body.Text)
		if body.Prio == "" {
			body.Prio = "mid"
		}
	}
	// Перенос строки внутри заголовка схлопывается в пробел тут же, до всех
	// проверок ниже: заголовок остаётся одной строкой и не роняет хвост в
	// тело записи.
	title := strings.TrimSpace(titleNewlineRe.ReplaceAllString(body.Title, " "))
	// Фразы отказа те же, что подсвечивает форма на самом поле (макет DK-447):
	// гонка мимо подсветки (второй клиент, отключённый JS) отвечает теми же
	// словами, и человек читает один рубеж, а не два разных.
	if title == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "поле обязательно для заполнения"})
		return
	}
	if n := len([]rune(title)); n > taskform.DraftTitleLimit {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("превышена допустимая длина поля (%d из %d)",
				n, taskform.DraftTitleLimit)})
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
	text := title
	if b := strings.TrimSpace(body.Body); b != "" {
		text += "\n\n" + b
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
	// Назначение основного чата стоит после коммита: запись уже существует,
	// и разговор, из которого её оформили, привязывается к настоящему ID.
	if note := s.draftDecorChat(found, id, body.Chat, title); note != "" {
		if had := resp["note"]; had != "" {
			resp["note"] = had + "; " + note
		} else {
			resp["note"] = note
		}
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
	lines := strings.Split(string(data), "\n")
	visible := strings.Split(taskform.VisibleText(string(data)), "\n")
	mask, _ := taskform.FenceMask(visible)
	in, found := false, false
	for i, line := range visible {
		if mask[i] {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(line) == taskform.Acceptance
			found = found || in
			continue
		}
		if in && strings.HasPrefix(line, "- барьер «") {
			lines[i] += " " + reason
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	if !found {
		return errors.New("в файле задачи нет раздела «Приёмка»: причина не записана")
	}
	return errors.New("в разделе «Приёмка» нет строки барьера: причина не записана")
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
		Body    string `json:"body"`
		Type    string `json:"type"`
		Rank    string `json:"rank"`
		RParts  []*int `json:"r_parts"`
		Cost    string `json:"cost"`
		Accept  string `json:"accept"`
		Barrier string `json:"barrier"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, draftTextLimit)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON с полями title, body, type, r_parts (или rank), cost"})
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
	if strings.TrimSpace(body.Body) != "" {
		args = append(args, "--body-file", "-")
	}
	out, code, err := s.taskctlWriteIn(found.Path, body.Body, args...)
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

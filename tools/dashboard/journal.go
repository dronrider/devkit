package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Живой статус агента, сторона журнала цикла: GET
// /api/projects/{p}/goals/{id}/log отдаёт хвост .devkit/goal-<ID>.log, с
// ?stream=1 держит соединение и дописывает новые строки по мере записи.
// Журнал пишут оболочка goal-run и строки --say витка, читается он с диска
// напрямую: утилиты чтения у него нет (LLD DK-112, «Откуда сервер берёт
// данные»). Живой поток едет через SSE, как решено в LLD: EventSource сам
// переподключается, серверная сторона это цикл записи в ответ.

// tailPoll это шаг опроса растущего файла: fsnotify в stdlib нет, а секундный
// опрос пары файлов стоит копейки (LLD). Переменная, чтобы тест не ждал
// секунды настоящего времени.
var tailPoll = time.Second

// tailDefault ограничивает хвост по умолчанию: журнал за долгий цикл весит
// много, а экрану нужны последние строки, за старыми есть ?n=.
const tailDefault = 200

const tailMax = 2000

var goalIDRe = regexp.MustCompile(`^[A-Za-z]+-[0-9]+$`)

// intParam читает числовой параметр запроса с умолчанием и потолком.
func intParam(r *http.Request, name string, def, max int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// tailLines отдаёт последние n строк файла без пустого хвоста от финального
// перевода строки.
func tailLines(data []byte, n int) []string {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// lastComplete отсекает недописанную последнюю строку: живой писатель мог
// остановиться посреди неё, и разбирать её рано.
func lastComplete(data []byte) []byte {
	cut := bytes.LastIndexByte(data, '\n')
	if cut < 0 {
		return nil
	}
	return data[:cut+1]
}

// newLines читает новые целые строки файла от offset. Усечение файла (размер
// меньше offset) начинает чтение заново: у журналов цикла это не штатный ход,
// но вечно молчать после него нельзя.
func newLines(path string, offset int64) ([]string, int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, offset
	}
	if fi.Size() < offset {
		offset = 0
	}
	if fi.Size() == offset {
		return nil, offset
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, offset
	}
	buf = lastComplete(buf)
	if len(buf) == 0 {
		return nil, offset
	}
	return tailLines(buf, tailMax), offset + int64(len(buf))
}

// sseOpen переводит ответ в поток событий; без Flusher поток не живёт.
func sseOpen(w http.ResponseWriter) (http.Flusher, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ответ не умеет поток"})
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	return f, true
}

// sseEvent пишет одно событие; данные с переводами строк раскладываются по
// нескольким data-строкам, как велит формат SSE.
func sseEvent(w io.Writer, f http.Flusher, name, data string) {
	if name != "" {
		fmt.Fprintf(w, "event: %s\n", name)
	}
	for _, ln := range strings.Split(data, "\n") {
		fmt.Fprintf(w, "data: %s\n", ln)
	}
	io.WriteString(w, "\n")
	f.Flush()
}

// Журнал у цели бывает из двух источников. Цикл под оболочкой goal-run пишет
// .devkit/goal-<ID>.log, а цель, которую ведёт живой чат, такого файла не
// заводит и не заведёт: её виток пишет ход в раздел «Журнал» файла цели
// (скилл goal-loop). Второй источник и читается, когда первого нет, с
// подписью, откуда взяты строки (DK-255).

// journalDoc это раздел «Журнал» файла цели как источник строк для экрана.
type journalDoc struct {
	path string // файл цели
	rel  string // его путь от корня проекта, он же подпись источника
	seen bool   // файл цели нашёлся
}

// journalSources сводит оба источника одной цели: имена для подписей и слова про
// каждую из пустот.
type journalSources struct {
	id      string
	project string
	logName string
	doc     journalDoc
}

func (j journalSources) logSign() string { return "журнал оболочки .devkit/" + j.logName }

func (j journalSources) docSign() string { return "журнал из файла цели " + j.doc.rel }

// docLines читает строки раздела «Журнал» файла цели. Пустота тут не одна, и
// три её причины называются по-разному: файла цели нет вовсе, раздела в нём
// нет, раздел заведён и пуст.
func (j journalSources) docLines() ([]string, string) {
	none := fmt.Sprintf("журнала %s нет и файла цели %s в %s нет: цель %s не гонялась ни оболочкой goal-run, ни живым чатом",
		j.logName, j.doc.rel, j.project, j.id)
	if !j.doc.seen {
		return nil, none
	}
	data, err := os.ReadFile(j.doc.path)
	if err != nil {
		return nil, none
	}
	lines, section := journalLines(string(data))
	switch {
	case !section:
		return nil, fmt.Sprintf("журнала %s нет, а в файле цели %s нет раздела «Журнал»: писать ход цели пока некуда", j.logName, j.doc.rel)
	case len(lines) == 0:
		return nil, fmt.Sprintf("раздел «Журнал» файла цели %s пуст: витков у цели %s ещё не было", j.doc.rel, j.id)
	}
	return lines, ""
}

// journalLines вынимает строки списка из раздела «Журнал» файла цели. Прочая
// проза раздела (строка-заготовка от goal-start) на экран не едет: журнал это
// строки витков и снимков квоты.
func journalLines(doc string) (lines []string, section bool) {
	in := false
	for _, ln := range strings.Split(doc, "\n") {
		trimmed := strings.TrimRight(ln, " ")
		if trimmed == journalHeader {
			in, section = true, true
			continue
		}
		if in && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !in {
			continue
		}
		if item, ok := strings.CutPrefix(trimmed, "- "); ok {
			lines = append(lines, item)
		}
	}
	return lines, section
}

// linkTargetRe вынимает путь из markdown-ссылки: в колонке доски ссылка стоит
// разметкой целиком («[tasks/DK-112.md](tasks/DK-112.md)»), и путём тут
// работает только адрес в скобках.
var linkTargetRe = regexp.MustCompile(`\]\(([^)]+)\)`)

// linkPath приводит ссылку строки доски к пути от каталога docs: вынимает
// адрес из разметки (голый путь остаётся как есть), отрезает якорь и отбивает
// всё, чем ходить по диску нельзя. Пути в доске относительны docs/, как и
// считает их линтер taskctl.
func linkPath(link string) string {
	target := strings.TrimSpace(link)
	if m := linkTargetRe.FindStringSubmatch(target); m != nil {
		target = strings.TrimSpace(m[1])
	}
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	if !safeRel(target) {
		return ""
	}
	return target
}

// goalDocPath ищет файл цели: сначала ссылка со строки доски, потом обычное
// место docs/tasks/<ID>.md. Ссылка, которая никуда не ведёт (разметка без
// адреса, путь наружу, файла по ней нет), откатывается на это же обычное
// место: экрану честнее прочитать лежащий рядом файл цели, чем сказать
// «журнала нет» из-за колонки доски.
func (s *server) goalDocPath(projectPath, id string) journalDoc {
	rel := taskFileRel(id)
	if raw, err := s.projectBoard(projectPath); err == nil {
		if row, ok := findRow(raw, id); ok {
			if target := linkPath(row.Link); target != "" {
				linked := filepath.ToSlash(filepath.Join("docs", target))
				if isFile(filepath.Join(projectPath, filepath.FromSlash(linked))) {
					rel = linked
				}
			}
		}
	}
	path := filepath.Join(projectPath, filepath.FromSlash(rel))
	return journalDoc{path: path, rel: rel, seen: isFile(path)}
}

// safeRel отбивает пути, по которым ходить незачем: пусто, прочерк пустой
// колонки, абсолютный путь, выход вверх, чужая схема (http, mailto).
func safeRel(link string) bool {
	if link == "" || link == "-" || strings.HasPrefix(link, "/") || filepath.IsAbs(link) {
		return false
	}
	if i := strings.IndexByte(link, ':'); i >= 0 {
		return false
	}
	for _, part := range strings.Split(link, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func (s *server) handleGoalLog(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "журнал цели")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	path := filepath.Join(found.Path, ".devkit", "goal-"+id+".log")
	j := journalSources{id: id, project: found.Name, logName: "goal-" + id + ".log",
		doc: s.goalDocPath(found.Path, id)}
	if r.URL.Query().Get("stream") == "1" {
		s.streamGoalLog(w, r, path, j)
		return
	}
	if data, err := os.ReadFile(path); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"goal": id, "exists": true, "source": "goal-log", "source_note": j.logSign(),
			"lines": tailLines(data, intParam(r, "n", tailDefault, tailMax)),
		})
		return
	}
	// Пустота различима: отсутствие строк называется словами, а не пустым
	// списком, «цель не гонялась» и «цикл молчит» это разные состояния.
	lines, note := j.docLines()
	if note != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"goal": id, "exists": false, "source": "none", "note": note, "lines": []string{}})
		return
	}
	if n := intParam(r, "n", tailDefault, tailMax); len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"goal": id, "exists": true, "source": "goal-file", "source_note": j.docSign(), "lines": lines})
}

// docTail дострачивает раздел «Журнал» файла цели. Файл цели переписывается
// целиком, и смещением в байтах, как у растущего журнала оболочки, тут не
// обойтись: новые строки узнаются по изменившемуся файлу и по числу уже
// отданных строк.
type docTail struct {
	mod  time.Time
	size int64
	sent int
}

func (d *docTail) stamp(path string) {
	if fi, err := os.Stat(path); err == nil {
		d.mod, d.size = fi.ModTime(), fi.Size()
	}
}

func (d *docTail) next(j journalSources) []string {
	fi, err := os.Stat(j.doc.path)
	if err != nil {
		return nil
	}
	if fi.ModTime().Equal(d.mod) && fi.Size() == d.size {
		return nil
	}
	d.mod, d.size = fi.ModTime(), fi.Size()
	j.doc.seen = true
	lines, _ := j.docLines()
	if len(lines) <= d.sent {
		// Раздел переписали короче: гнать заново уже показанное незачем, счёт
		// продолжается от нынешней длины.
		d.sent = len(lines)
		return nil
	}
	out := lines[d.sent:]
	d.sent = len(lines)
	return out
}

// streamGoalLog шлёт хвост журнала и дальше дописывает новые строки по мере
// записи. Источник называется событием source, пустота событием note, а
// появление журнала оболочки подхватит опрос: цикл могли поднять уже при
// открытом экране, и строки дальше идут оттуда. Соединение живёт до ухода
// клиента.
func (s *server) streamGoalLog(w http.ResponseWriter, r *http.Request, path string, j journalSources) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	var offset int64
	var doc *docTail
	if data, err := os.ReadFile(path); err == nil {
		sseEvent(w, f, "source", j.logSign())
		data = lastComplete(data)
		for _, ln := range tailLines(data, tailDefault) {
			sseEvent(w, f, "", ln)
		}
		offset = int64(len(data))
	} else {
		lines, note := j.docLines()
		sseEvent(w, f, "source", j.docSign())
		for _, ln := range lines {
			sseEvent(w, f, "", ln)
		}
		if note != "" {
			sseEvent(w, f, "note", note)
		}
		doc = &docTail{sent: len(lines)}
		doc.stamp(j.doc.path)
	}
	t := time.NewTicker(tailPoll)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			var lines []string
			lines, offset = newLines(path, offset)
			if len(lines) > 0 && doc != nil {
				doc = nil
				sseEvent(w, f, "source", j.logSign())
			}
			if doc != nil {
				lines = doc.next(j)
			}
			for _, ln := range lines {
				sseEvent(w, f, "", ln)
			}
		}
	}
}

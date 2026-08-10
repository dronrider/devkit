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

func (s *server) handleGoalLog(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	path := filepath.Join(found.Path, ".devkit", "goal-"+id+".log")
	// Пустота различима: отсутствие журнала называется словами, а не пустым
	// списком, «цель не гонялась» и «цикл молчит» это разные состояния.
	note := fmt.Sprintf("журнала %s нет: цель %s в %s не гонялась оболочкой goal-run", "goal-"+id+".log", id, found.Name)
	if r.URL.Query().Get("stream") == "1" {
		s.streamGoalLog(w, r, path, note)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"goal": id, "exists": false, "note": note, "lines": []string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"goal": id, "exists": true,
		"lines": tailLines(data, intParam(r, "n", tailDefault, tailMax)),
	})
}

// streamGoalLog шлёт хвост журнала и дальше дописывает новые строки по мере
// записи; отсутствие файла называется событием note, а появление подхватит
// опрос. Соединение живёт до ухода клиента.
func (s *server) streamGoalLog(w http.ResponseWriter, r *http.Request, path, note string) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	var offset int64
	if data, err := os.ReadFile(path); err == nil {
		data = lastComplete(data)
		for _, ln := range tailLines(data, tailDefault) {
			sseEvent(w, f, "", ln)
		}
		offset = int64(len(data))
	} else {
		sseEvent(w, f, "note", note)
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
			for _, ln := range lines {
				sseEvent(w, f, "", ln)
			}
		}
	}
}

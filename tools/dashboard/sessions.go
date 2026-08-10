package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Живой статус агента, сторона транскрипта: сессии проекта из
// ~/.claude/projects и лента реплик одной сессии с пагинацией назад и живым
// дострением через SSE. Транскрипт весит мегабайты и пишется чужим процессом,
// поэтому режет его сервер, а клиент получает последние реплики готовым
// JSON (LLD DK-112, «Граница сервер-клиент»). Разбор реплик держится общим
// куском: переписка DK-220 переиспользует его как есть.

// claudeDirName кодирует путь проекта в имя каталога транскриптов, как это
// делает Claude Code: всё, что не буква, не цифра и не дефис, становится
// дефисом (/Users/x/dev -> -Users-x-dev), точки и подчёркивания тоже.
func claudeDirName(projPath string) string {
	var b strings.Builder
	for _, r := range projPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// sessionDirs собирает каталоги транскриптов проекта: сам каталог по пути с
// дефисами плюс те, чьё имя продолжает его дефисом, так в список попадают
// боковые деревья задач (../<проект>-<id>) и копия окна (LLD).
func sessionDirs(home, projPath string) []string {
	base := filepath.Join(home, ".claude", "projects")
	name := claudeDirName(projPath)
	dirs := []string{filepath.Join(base, name)}
	entries, err := os.ReadDir(base)
	if err != nil {
		return dirs
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), name+"-") {
			dirs = append(dirs, filepath.Join(base, e.Name()))
		}
	}
	return dirs
}

// sessionInfo это строка списка сессий: id, время последней записи, ветка и
// первая реплика человека, по ней сессию узнают в списке.
type sessionInfo struct {
	ID     string `json:"id"`
	Mtime  string `json:"mtime"`
	Branch string `json:"branch,omitempty"`
	First  string `json:"first,omitempty"`
	path   string
	mod    time.Time
}

// sessionFiles обходит каталоги транскриптов; сортировка свежие сверху, при
// равном времени по id, чтобы порядок был устойчив.
func sessionFiles(home, projPath string) []sessionInfo {
	var out []sessionInfo
	for _, dir := range sessionDirs(home, projPath) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".jsonl")
			if !ok || e.IsDir() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, sessionInfo{
				ID: name, Mtime: fi.ModTime().UTC().Format(time.RFC3339),
				path: filepath.Join(dir, e.Name()), mod: fi.ModTime(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].mod.Equal(out[j].mod) {
			return out[i].mod.After(out[j].mod)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// metaScanLimit ограничивает чтение шапки транскрипта: ветка и первая реплика
// лежат в первых записях, тянуть мегабайты ради них незачем.
const metaScanLimit = 256 * 1024

// sessionMeta вычитывает из шапки транскрипта ветку и первую реплику
// человека; служебные вставки в угловых скобках (<ide_opened_file> и родня)
// репликой не считаются.
func sessionMeta(path string) (branch, first string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	buf := make([]byte, metaScanLimit)
	n, _ := f.Read(buf)
	for _, ln := range strings.Split(string(buf[:n]), "\n") {
		var rec struct {
			Type      string `json:"type"`
			GitBranch string `json:"gitBranch"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		if branch == "" {
			branch = rec.GitBranch
		}
		if first == "" && rec.Type == "user" {
			for _, text := range contentTexts(rec.Message.Content) {
				if !strings.HasPrefix(text, "<") {
					first = firstLine(text)
					break
				}
			}
		}
		if branch != "" && first != "" {
			break
		}
	}
	return branch, first
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return truncate(line, 160)
}

func truncate(text string, n int) string {
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n]) + "..."
}

// contentTexts отдаёт текстовые куски содержимого реплики: строку как есть
// либо text-блоки из списка.
func contentTexts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			out = append(out, b.Text)
		}
	}
	return out
}

// reply это одна строка ленты: реплика человека или агента, свёрнутый вызов
// инструмента либо пометка о размышлениях. Вызовы инструментов сворачиваются
// в одну строку, тянуть их содержимое на телефон незачем (LLD); размышления
// сворачиваются в пометку без текста.
type reply struct {
	Seq  int    `json:"seq"`
	Role string `json:"role"` // user | assistant | thinking | tool
	Time string `json:"time,omitempty"`
	Text string `json:"text,omitempty"`
	Tool string `json:"tool,omitempty"`
	Note string `json:"note,omitempty"`
}

// toolNoteKeys это порядок полей ввода, из которых собирается однострочная
// подпись вызова: первое найденное и есть суть вызова.
var toolNoteKeys = []string{"command", "file_path", "path", "skill", "pattern", "url", "prompt", "id", "query", "description"}

func toolNote(input map[string]any) string {
	for _, key := range toolNoteKeys {
		if v, ok := input[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncate(strings.Join(strings.Fields(v), " "), 160)
		}
	}
	return ""
}

// parseReplies разбирает строки jsonl в реплики, нумеруя с startSeq: живое
// дострение продолжает счёт, и пагинация назад не съезжает. Битая строка
// пропускается без обрушения ленты, служебные записи (queue-operation,
// attachment и родня) и ветки субагентов (isSidechain) в ленту не попадают.
func parseReplies(data []byte, startSeq int) []reply {
	var out []reply
	seq := startSeq
	add := func(item reply) {
		item.Seq = seq
		seq++
		out = append(out, item)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Timestamp   string `json:"timestamp"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		if rec.IsSidechain || (rec.Type != "user" && rec.Type != "assistant") {
			continue
		}
		var s string
		if json.Unmarshal(rec.Message.Content, &s) == nil {
			add(reply{Role: rec.Type, Time: rec.Timestamp, Text: s})
			continue
		}
		var blocks []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				add(reply{Role: rec.Type, Time: rec.Timestamp, Text: b.Text})
			case "thinking":
				add(reply{Role: "thinking", Time: rec.Timestamp})
			case "tool_use":
				add(reply{Role: "tool", Time: rec.Timestamp, Tool: b.Name, Note: toolNote(b.Input)})
			}
		}
	}
	return out
}

const repliesDefault = 40

const repliesMax = 500

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	files := sessionFiles(s.cfg.Home, found.Path)
	if n := intParam(r, "n", 20, 100); len(files) > n {
		files = files[:n]
	}
	sessions := []sessionInfo{}
	for _, f := range files {
		f.Branch, f.First = sessionMeta(f.path)
		sessions = append(sessions, f)
	}
	resp := map[string]any{"project": found.Name, "sessions": sessions}
	if len(sessions) == 0 {
		// Пустота различима: транскриптов нет это слова с адресом, где они
		// искались, а не пустой список без причины.
		resp["note"] = fmt.Sprintf("транскриптов нет: в ~/.claude/projects нет сессий с путём %s", found.Path)
	}
	writeJSON(w, http.StatusOK, resp)
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)

// sessionPath находит файл транскрипта по id среди каталогов проекта.
func sessionPath(home, projPath, sid string) string {
	for _, dir := range sessionDirs(home, projPath) {
		p := filepath.Join(dir, sid+".jsonl")
		if isFile(p) {
			return p
		}
	}
	return ""
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r)
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	path := sessionPath(s.cfg.Home, found.Path, sid)
	if path == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("транскрипта %s нет среди сессий проекта %s", sid, found.Name)})
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		s.streamSession(w, r, path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("транскрипт не прочитался: %v", err)})
		return
	}
	items := parseReplies(data, 0)
	total := len(items)
	before := intParam(r, "before", total, total)
	if before < total {
		items = items[:before]
	}
	n := intParam(r, "n", repliesDefault, repliesMax)
	if len(items) > n {
		items = items[len(items)-n:]
	}
	if items == nil {
		items = []reply{}
	}
	resp := map[string]any{"session": sid, "total": total, "items": items}
	if total == 0 {
		resp["note"] = emptyTranscriptNote
	}
	writeJSON(w, http.StatusOK, resp)
}

// emptyTranscriptNote называет пустую ленту словами и в обычном ответе, и
// первым событием потока: молчащий стрим неотличим от оборвавшегося
// (замечание ревью DK-219).
const emptyTranscriptNote = "в транскрипте пока нет реплик"

// streamSession шлёт последние реплики и дальше дострение по мере записи:
// каждое событие это одна реплика в JSON. Нумерация продолжается с конца
// файла, разбираются только целые строки.
func (s *server) streamSession(w http.ResponseWriter, r *http.Request, path string) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	var offset int64
	seq := 0
	if data, err := os.ReadFile(path); err == nil {
		data = lastComplete(data)
		items := parseReplies(data, 0)
		seq = len(items)
		if len(items) > repliesDefault {
			items = items[len(items)-repliesDefault:]
		}
		for _, item := range items {
			sseEvent(w, f, "", marshalReply(item))
		}
		offset = int64(len(data))
	}
	// Пустая лента называется первым событием, как в обычном ответе: молчащий
	// поток неотличим от оборвавшегося. Дострение дальше идёт как обычно.
	if seq == 0 {
		sseEvent(w, f, "note", emptyTranscriptNote)
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
			if len(lines) == 0 {
				continue
			}
			for _, item := range parseReplies([]byte(strings.Join(lines, "\n")), seq) {
				seq = item.Seq + 1
				sseEvent(w, f, "", marshalReply(item))
			}
		}
	}
}

func marshalReply(item reply) string {
	data, err := json.Marshal(item)
	if err != nil {
		return "{}"
	}
	return string(data)
}

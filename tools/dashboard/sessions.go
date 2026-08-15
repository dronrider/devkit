package main

import (
	"encoding/json"
	"fmt"
	"io"
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
// первая реплика человека, по ней сессию узнают в списке. Tree это боковое
// дерево, в котором шёл разговор (у главного дерева пусто): двух разговоров
// одной задачи ветка не разводит, груминг и исполнение идут по одной, а
// деревья у них разные (DK-290). Task с подписью TaskNote говорят, чью работу
// сессия ведёт и чем она узнана; нераспознанная сессия остаётся в списке с
// подписью, а в ленту задачи не идёт (DK-252).
type sessionInfo struct {
	ID       string `json:"id"`
	Mtime    string `json:"mtime"`
	Branch   string `json:"branch,omitempty"`
	Tree     string `json:"tree,omitempty"`
	First    string `json:"first,omitempty"`
	Task     string `json:"task,omitempty"`
	TaskNote string `json:"taskNote,omitempty"`
	path     string
	suffix   string
	stamp    string
	mod      time.Time
}

// sessionFiles обходит каталоги транскриптов; сортировка свежие сверху, при
// равном времени по id, чтобы порядок был устойчив. Вместе с файлом берётся
// хвост имени каталога (боковое дерево задачи называет им задачу) и отпечаток
// файла для памяти процесса на шапку.
func sessionFiles(home, projPath string) []sessionInfo {
	var out []sessionInfo
	base := claudeDirName(projPath)
	for _, dir := range sessionDirs(home, projPath) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(filepath.Base(dir), base), "-")
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
				path:   filepath.Join(dir, e.Name()),
				suffix: suffix,
				stamp:  fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size()),
				mod:    fi.ModTime(),
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

// metaScanLimit ограничивает чтение шапки транскрипта: ветка, первая реплика
// и названная ею задача лежат в первых записях, тянуть мегабайты ради них
// незачем.
const metaScanLimit = 256 * 1024

// sessionHead это всё, что читается из шапки транскрипта: ветка, первая
// реплика человека для списка и ID задачи, названный этой репликой.
type sessionHead struct {
	Branch string
	First  string
	Named  string
}

// readSessionHead вычитывает шапку транскрипта; служебные вставки в угловых
// скобках (<ide_opened_file> и родня) репликой не считаются. Второй ответ
// говорит, дочитана ли голова до предела: дальше файл только дописывается, и
// такая голова больше не меняется, на этом стоит память процесса.
func readSessionHead(path string) (sessionHead, bool) {
	var head sessionHead
	f, err := os.Open(path)
	if err != nil {
		return head, false
	}
	defer f.Close()
	buf := make([]byte, metaScanLimit)
	// Чтение до заполнения буфера, а не одним Read: короткий ответ ядра выдал
	// бы недочитанную голову за дочитанную, и память процесса поверила бы ей.
	n, err := io.ReadFull(f, buf)
	full := err == nil
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
		if head.Branch == "" {
			head.Branch = rec.GitBranch
		}
		if head.First == "" && rec.Type == "user" {
			for _, text := range contentTexts(rec.Message.Content) {
				if !strings.HasPrefix(text, "<") {
					head.First = firstLine(text)
					// ID ищется по всей реплике, а не по обрезанной для списка
					// строке: заказ работы бывает длиннее ста шестидесяти знаков.
					head.Named = taskIDInText(text)
					break
				}
			}
		}
		if head.Branch != "" && head.First != "" {
			break
		}
	}
	return head, full
}

// headTTL это потолок доверия к запомненной шапке. Отпечаток файла ловит
// правку сам, а дочитанной голове верят и после дописывания, но переписанный
// с нуля файл не поймать ни тем, ни другим, поэтому доверие кончается по
// сроку.
const headTTL = 5 * time.Minute

// headEntry это запомненная шапка одного транскрипта.
type headEntry struct {
	head  sessionHead
	stamp string
	full  bool
	born  time.Time
}

// sessionHeadCached отдаёт шапку транскрипта, по возможности из памяти
// процесса (образец кэша DK-242). Экран агента ходит за сессиями на каждом
// открытии, а сессий у проекта десятки: без памяти каждый запрос перечитывал
// бы по четверти мегабайта на сессию.
func (s *server) sessionHeadCached(path, stamp string) sessionHead {
	now := s.now()
	s.mu.Lock()
	e, hit := s.heads[path]
	s.mu.Unlock()
	if hit && now.Sub(e.born) < headTTL && (e.full || e.stamp == stamp) {
		return e.head
	}
	head, full := readSessionHead(path)
	s.mu.Lock()
	s.heads[path] = headEntry{head: head, stamp: stamp, full: full, born: now}
	s.mu.Unlock()
	return head
}

// taskIDTextRe ловит ID задачи в тексте первой реплики. Префикс только
// прописными: так «Выполни цель DK-112» отличается от «top-10» и прочих слов
// с числом через дефис.
var taskIDTextRe = regexp.MustCompile(`\b([A-Z]{2,10})-(\d{1,6})\b`)

// taskIDNameRe узнаёт ID в имени: ветка dk-252, хвост каталога бокового
// дерева dk-252, имя с приставкой вроде dk-252-fix. Регистр тут любой, имена
// пишутся строчными.
var taskIDNameRe = regexp.MustCompile(`^([A-Za-z]{2,10})-(\d{1,6})(?:-|$)`)

func taskIDInText(text string) string {
	return upperID(taskIDTextRe.FindStringSubmatch(text))
}

func taskIDInName(name string) string {
	return upperID(taskIDNameRe.FindStringSubmatch(name))
}

func upperID(m []string) string {
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1]) + "-" + m[2]
}

// unknownTaskNote подписывает сессию, чья задача не узнана. Молча выкидывать
// такую сессию из списка нельзя: пропавшая работа неотличима от несделанной.
const unknownTaskNote = "задача не распознана"

// sessionTask называет задачу сессии и говорит, чем она узнана. Связи
// «сессия -> задача» в транскриптах нет, поэтому источника три, и порядок их
// не случаен: боковое дерево заводится ровно под одну задачу и врать не
// умеет; первая реплика это заказ работы («Выполни цель DK-112», «возьми
// задачу DK-207 в работу») и узнаёт сессию в главном дереве; ветка идёт
// последней, потому что окно главного дерева часто стоит на ветке, оставшейся
// от прошлой работы (решение DK-252).
func sessionTask(dirSuffix string, head sessionHead) (task, note string) {
	if id := taskIDInName(dirSuffix); id != "" {
		return id, "по дереву задачи"
	}
	if head.Named != "" {
		return head.Named, "по первой реплике"
	}
	if id := taskIDInName(head.Branch); id != "" {
		return id, "по ветке"
	}
	return "", unknownTaskNote
}

// sessionLiveTTL это порог живости интерактивной сессии. Транскрипт
// дописывается каждым ходом, но ход бывает долгим: одиночный прогон в
// foreground харнес держит до десяти минут, и вокруг него идут ещё
// размышления и правка, поэтому паузу в записи короче этого срока считать
// концом работы нельзя. Пятнадцать минут держали бы закрытое окно живым
// четверть часа, двенадцать оставляют запас над самым долгим ходом и убирают
// доделанную работу с глаз тем же ходом, что человек уходит от окна.
const sessionLiveTTL = 12 * time.Minute

// foreignTaskNote подписывает сессию, чья задача узнана, но принадлежит чужой
// доске: ходить по ней на экран задачи некуда, а сама работа идёт и в списке
// остаётся.
const foreignTaskNote = "задача не с доски проекта"

// groomOrderPrefix это начало заказа headless-сессии, поднимаемой самим
// дашбордом (groomPrompt в drafts.go). Транскрипт такой сессии узнаётся им:
// первая реплика там не разговор человека, а заказ, и жить этим транскриптом
// работа не может. Узнавание держится парой признаков, заказа и живой
// tmux-сессии, поэтому интерактивному окну с теми же словами на чужой машине
// (два человека, один накопитель) вера не отнимается.
const groomOrderPrefix = "Проведи груминг "

// sessionWorks собирает работы из транскриптов: интерактивное окно агента не
// заводит ни tmux-сессии, ни записи в реестре, и единственный его след это
// свежий транскрипт. Доска приходит уже разобранной: строка ищется на каждую
// сессию, а разбирать ответ taskctl заново на каждой из них незачем. Занятые задачи (busy) сюда не идут: headless-сессия
// конвейера тоже пишет транскрипт, и её работа уже собрана из tmux, а вторая
// карточка о той же задаче читалась бы как два агента вместо одного.
func (s *server) sessionWorks(projPath, prefix string, rows map[string]boardRow, busy map[string]bool) []Work {
	works := []Work{}
	cutoff := s.now().Add(-sessionLiveTTL)
	for _, f := range sessionFiles(s.cfg.Home, projPath) {
		// Список идёт свежими сверху, дальше первого протухшего смотреть нечего.
		if f.mod.Before(cutoff) {
			break
		}
		head := s.sessionHeadCached(f.path, f.stamp)
		// Транскрипт с заказом дашборда в первой реплике жив, пока жива его
		// tmux-сессия: claude -p умирает вместе с ней, а транскрипт остаётся
		// свежим, и без этой проверки законченный разбор висел бы работой до
		// порога протухания (DK-358). Живость здесь не сверяется: живую
		// сессию уже забрал список tmux меткой busy, до сюда она не доходит.
		if strings.HasPrefix(head.First, groomOrderPrefix) {
			continue
		}
		task, note := sessionTask(f.suffix, head)
		if task != "" && (prefix == "" || !strings.HasPrefix(task, prefix+"-")) {
			task, note = "", foreignTaskNote
		}
		kind, title, sect := "session", "", ""
		if task != "" {
			if busy[task] {
				continue
			}
			busy[task] = true
			note = ""
			// Цель это цель и в интерактивном окне: переписка и журнал цикла
			// открываются только у неё, и вид работы берётся со строки доски,
			// а не из того, что окно вообще живо.
			kind = "task"
			if row, ok := rows[task]; !ok {
				kind = "session"
			} else {
				title, sect = row.Title, row.Sect
				if isGoalTitle(row.Title) {
					kind = "goal"
				}
			}
		}
		works = append(works, Work{ID: task, Kind: kind, Title: title, Sect: sect,
			Via: "session", Session: f.ID, Note: note})
	}
	return works
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

// headScanMax держит число сессий, у которых читается шапка в общем списке:
// список показывают десятком свежих строк, а проект с сотней транскриптов
// иначе перечитывал бы их все на первый же запрос.
const headScanMax = 50

// taskScanBudget это потолок ожидания при поиске сессий одной задачи. Поиск по
// ID идёт по всем транскриптам проекта: разговор законченной работы лежит тем
// глубже, чем дольше она закончилась, и под окном свежих его как раз и не
// находили (DK-280). Цена обхода это чтение шапок, и она платится один раз:
// дочитанная голова оседает в памяти процесса, второй заход укладывается в
// миллисекунды. Бюджет остаётся страховкой на дерево, где транскриптов накопились
// тысячи: экран получает найденное и слова о том, что обход не дошёл до конца,
// вместо долгого ожидания.
const taskScanBudget = 3 * time.Second

// taskParamRe сито параметра ?task=: ID задачи, а не любая строка из адреса.
var taskParamRe = regexp.MustCompile(`^[A-Z]{2,10}-\d{1,6}$`)

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "сессии")
	if found == nil {
		return
	}
	want := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("task")))
	if want != "" && !taskParamRe.MatchString(want) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q не похоже на ID задачи", want)})
		return
	}
	files := sessionFiles(s.cfg.Home, found.Path)
	limit := headScanMax
	if want != "" {
		limit = len(files)
	}
	deadline := s.now().Add(taskScanBudget)
	sessions := []sessionInfo{}
	var scanned, others, unknown int
	cut := false
	for _, f := range files {
		if scanned >= limit {
			break
		}
		if want != "" && s.now().After(deadline) {
			cut = true
			break
		}
		scanned++
		head := s.sessionHeadCached(f.path, f.stamp)
		f.Branch, f.First = head.Branch, head.First
		f.Tree = f.suffix
		f.Task, f.TaskNote = sessionTask(f.suffix, head)
		if f.Task == "" {
			unknown++
		} else if want != "" && f.Task != want {
			others++
		}
		// Чужая сессия в ленту задачи не идёт: до DK-252 экран брал свежую по
		// mtime, и при двух живых окнах под заголовком одной задачи шёл ход
		// соседней.
		if want != "" && f.Task != want {
			continue
		}
		sessions = append(sessions, f)
	}
	if n := intParam(r, "n", 20, 100); len(sessions) > n {
		sessions = sessions[:n]
	}
	resp := map[string]any{"project": found.Name, "sessions": sessions}
	// Пустоты различимы: транскриптов нет вовсе это слова с адресом, где они
	// искались, а «сессий этой задачи нет» это счёт по чужим и нераспознанным.
	// Обрыв обхода по бюджету называется словами и при найденной сессии: она
	// может оказаться не единственной, и молчание тут выдало бы неполный поиск
	// за полный.
	switch {
	case len(files) == 0:
		resp["note"] = fmt.Sprintf("транскриптов нет: в ~/.claude/projects нет сессий с путём %s", found.Path)
	case want != "" && cut:
		resp["note"] = fmt.Sprintf(
			"обход прерван по времени: просмотрено %d транскриптов из %d, разговор задачи %s мог остаться дальше",
			scanned, len(files), want)
	case len(sessions) == 0 && want != "":
		resp["note"] = fmt.Sprintf(
			"сессий задачи %s нет: просмотрены все %d транскриптов проекта, %d о других задачах, %d без распознанной задачи",
			want, scanned, others, unknown)
	}
	writeJSON(w, http.StatusOK, resp)
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)

// findSession находит транскрипт по id среди каталогов проекта и собирает про
// него ту же строку, что стоит в списке сессий: путь с отпечатком для памяти
// процесса и хвост имени каталога, которым задача подписана в боковом дереве.
// Полный обход каталогов ради одной сессии не нужен, имя файла известно.
func findSession(home, projPath, sid string) (sessionInfo, bool) {
	base := claudeDirName(projPath)
	for _, dir := range sessionDirs(home, projPath) {
		p := filepath.Join(dir, sid+".jsonl")
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		return sessionInfo{
			ID: sid, Mtime: fi.ModTime().UTC().Format(time.RFC3339),
			path:   p,
			suffix: strings.TrimPrefix(strings.TrimPrefix(filepath.Base(dir), base), "-"),
			stamp:  fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size()),
			mod:    fi.ModTime(),
		}, true
	}
	return sessionInfo{}, false
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "сессия")
	if found == nil {
		return
	}
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на id сессии", sid)})
		return
	}
	info, ok := findSession(s.cfg.Home, found.Path, sid)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("транскрипта %s нет среди сессий проекта %s", sid, found.Name)})
		return
	}
	path := info.path
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
	// Шапка едет вместе с лентой: экран агента открывается и по id сессии, а
	// строки доски у такого захода нет, и заголовок ему брать больше неоткуда
	// (DK-294). Задача тут названа так же, как в списке: узнанная с подписью,
	// чем узнана, либо пустая с подписью «задача не распознана».
	head := s.sessionHeadCached(path, info.stamp)
	info.Branch, info.First = head.Branch, head.First
	info.Tree = info.suffix
	info.Task, info.TaskNote = sessionTask(info.suffix, head)
	resp := map[string]any{"session": sid, "head": info, "total": total, "items": items}
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

package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Поиск задач (LLD DK-328, решение 2): один запрос отвечает четырьмя группами,
// «Доска», «Черновики», «Архив» и «В тексте задач». Индекса на сервере нет,
// каждый запрос обходит docs/tasks целиком вместе с подкаталогами archive/<год>
// и drafts: файлов там сотни, они мелкие и лежат в горячем кэше файловой
// системы, а индекс пришлось бы инвалидировать на каждой правке файла задачи.
// Доска берётся из кэша сервера, накопитель у taskctl, архив разбирается
// файлом тем же archiveRows, что и состав цели: своего разбора доски у сервера
// по-прежнему нет.

const (
	// Потолки выдачи: короткий запрос совпадает с половиной архива, и
	// отвечать на него сотней строк значит грузить телефон списком, в котором
	// всё равно ничего не найти. Хвост считается и называется словами, чтобы
	// урезанная выдача не выглядела полной.
	searchMinQuery = 2
	searchRowsMax  = 20
	searchTextMax  = 30
	// Файл задачи это постановка на пару экранов; всё, что толще, в поиск по
	// тексту не берётся, иначе один залитый в docs/tasks дамп съедал бы обход.
	searchFileMax = 512 << 10
	// Цитата строки файла: длинную строку урезаем окном вокруг совпадения, на
	// экране всё равно видно строку, а не абзац.
	searchQuoteMax = 160
)

// Пустые случаи говорят, где искали: молчаливая пустота неотличима от «архив в
// поиск не входит». Слова живут на сервере, а не на клиенте, потому что состав
// источников знает он.
const (
	searchShortNote = "Ждём двух символов."
	searchNoneNote  = "По запросу ничего нет. Ищем по номеру, заголовку и тексту файлов: " +
		"живая доска, черновики, архив."
)

// searchRow это строка выдачи. Тип один на все четыре группы: строки рисуются
// одним и тем же путём, а разница между ними это набор заполненных полей.
// Архивной строке ранг и цену взять неоткуда (в архиве этих колонок нет), и
// пустые поля тут не забывчивость, а сказанное «неизвестно».
type searchRow struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// Строка доски.
	Sect    string `json:"sect,omitempty"`
	Section string `json:"section,omitempty"`
	Type    string `json:"type,omitempty"`
	P       string `json:"p,omitempty"`
	R       int    `json:"r,omitempty"`
	RParts  []int  `json:"r_parts,omitempty"`
	Cost    string `json:"cost,omitempty"`
	// Черновик накопителя.
	Age string `json:"age_words,omitempty"`
	// Архивная строка.
	Closed string `json:"closed,omitempty"`
	// Найденное в тексте файла: путь от корня проекта, номер строки, сама
	// строка и место файла (архив, накопитель либо живая задача).
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	Quote string `json:"quote,omitempty"`
	Where string `json:"where,omitempty"`
}

// searchGroup это группа выдачи. Порядок групп постоянный, и пустая группа из
// ответа не выпадает: клиент решает, показывать ли её, а разъехаться в порядке
// две стороны не могут.
type searchGroup struct {
	Key   string      `json:"key"`
	Title string      `json:"title"`
	Rows  []searchRow `json:"rows"`
	More  int         `json:"more,omitempty"`
	Note  string      `json:"note,omitempty"`
}

type searchResp struct {
	Project string        `json:"project"`
	Q       string        `json:"q"`
	Groups  []searchGroup `json:"groups"`
	Note    string        `json:"note,omitempty"`
}

// searchNumRe узнаёт в запросе номер задачи: «324», «dk-324» и «DK324» это
// один и тот же номер, и держать в голове префикс своей доски человеку не
// надо.
var searchNumRe = regexp.MustCompile(`^\p{L}*-?(\d{1,7})$`)

// searchNum это номер из запроса, похожего на ID. Ноль значит, что запрос на
// номер не похож.
func searchNum(q string) int {
	m := searchNumRe.FindStringSubmatch(strings.TrimSpace(q))
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// rowNum это номер строки доски: хвост ID после дефиса. По нему запрос «324»
// и находит DK-324, не зная про префикс.
func rowNum(id string) int {
	at := strings.LastIndex(id, "-")
	if at < 0 {
		return 0
	}
	n, err := strconv.Atoi(id[at+1:])
	if err != nil {
		return 0
	}
	return n
}

// searchMatch это правило совпадения: подстрока в ID или в заголовке, регистр
// не важен. Запрос уже приведён к нижнему регистру.
func searchMatch(q, id, title string) bool {
	if strings.Contains(strings.ToLower(id), q) || strings.Contains(strings.ToLower(title), q) {
		return true
	}
	n := searchNum(q)
	return n != 0 && rowNum(id) == n
}

// searchExact поднимает точное попадание по номеру первой строкой группы:
// набрав номер, человек ищет ровно эту задачу, а подстрокой она стоит где
// придётся.
func searchExact(q, id string) bool {
	n := searchNum(q)
	return n != 0 && rowNum(id) == n
}

// searchQuote это первая строка файла с совпадением, её номер и сама строка
// цитатой. Длинная строка урезается окном вокруг совпадения: на экране стоит
// строка, а не абзац.
func searchQuote(text, q string) (string, int) {
	for i, line := range strings.Split(text, "\n") {
		at := strings.Index(strings.ToLower(line), q)
		if at < 0 {
			continue
		}
		line = strings.TrimRight(line, "\r")
		if utf8.RuneCountInString(line) <= searchQuoteMax {
			return strings.TrimSpace(line), i + 1
		}
		runes := []rune(line)
		from := utf8.RuneCountInString(line[:at]) - searchQuoteMax/3
		if from < 0 {
			from = 0
		}
		to := from + searchQuoteMax
		if to > len(runes) {
			to, from = len(runes), len(runes)-searchQuoteMax
		}
		cut := strings.TrimSpace(string(runes[from:to]))
		if from > 0 {
			cut = "..." + cut
		}
		if to < len(runes) {
			cut += "..."
		}
		return cut, i + 1
	}
	return "", 0
}

// searchDocTitle это заголовок файла задачи: первая строка вида «# DK-325:
// поиск задач». Нужен файлам, у которых ни строки на доске, ни записи в
// архиве нет вовсе (черновик, задача чужой доски), иначе строка выдачи
// осталась бы без имени.
func searchDocTitle(id, text string) string {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		head := strings.TrimSpace(strings.TrimPrefix(line, "# "))
		return strings.TrimSpace(strings.TrimPrefix(head, id+":"))
	}
	return ""
}

// searchWhere называет место файла задачи словом, каким о нём говорят: архив,
// накопитель либо живая доска. Слово едет строкой выдачи, чтобы найденная в
// тексте закрытая задача не выглядела стоящей в очереди.
func searchWhere(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		switch part {
		case "archive":
			return "архив"
		case "drafts":
			return "черновик"
		case "lld":
			return "LLD"
		}
	}
	return ""
}

// searchOrder ставит выдачу в постоянный порядок: точное попадание по номеру
// первым, дальше живые задачи, черновики и архив, а внутри места номера
// сверху вниз. Порядок обхода каталога тут не годится: archive/ идёт раньше
// живых файлов по алфавиту и забирал бы себе весь потолок.
func searchOrder(rows []searchRow, q string) {
	place := map[string]int{"": 0, "черновик": 1, "архив": 2}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ex := searchExact(q, a.ID); ex != searchExact(q, b.ID) {
			return ex
		}
		if place[a.Where] != place[b.Where] {
			return place[a.Where] < place[b.Where]
		}
		if rowNum(a.ID) != rowNum(b.ID) {
			return rowNum(a.ID) > rowNum(b.ID)
		}
		return a.ID < b.ID
	})
}

// searchCut режет группу по потолку и говорит, сколько осталось за краем.
func searchCut(rows []searchRow, max int) ([]searchRow, int) {
	if len(rows) <= max {
		return rows, 0
	}
	return rows[:max], len(rows) - max
}

// searchBoard это группа «Доска»: строки живой доски, найденные по номеру и
// заголовку. Секция едет со строкой, по ней строка выдачи и подписана
// статусом.
func searchBoard(raw json.RawMessage, q string) ([]searchRow, error) {
	sects, err := parseBoardSects(raw)
	if err != nil {
		return nil, err
	}
	var rows []searchRow
	for _, sec := range sects {
		for _, row := range sec.Rows {
			if !searchMatch(q, row.ID, row.Title) {
				continue
			}
			rows = append(rows, searchRow{
				ID: row.ID, Title: row.Title, Sect: sec.Key, Section: sec.Title,
				Type: row.Type, P: row.P, R: row.R, RParts: row.RParts, Cost: row.Cost,
			})
		}
	}
	return rows, nil
}

// searchDrafts это группа «Черновики»: накопитель у taskctl теми же словами,
// какими он рисуется своим экраном. Отказ утилиты остаётся при группе, а не
// рушит весь поиск: доска с архивом читаются мимо неё.
func (s *server) searchDrafts(dir, q string) ([]searchRow, string) {
	out, _, err := taskctlDo(dir, "draft", "list", "--json")
	if err != nil {
		return nil, "накопитель черновиков не прочитался: " + err.Error()
	}
	var v struct {
		Drafts []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Age   string `json:"age_words"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, "ответ taskctl draft list --json не разобрался: " + err.Error()
	}
	var rows []searchRow
	for _, d := range v.Drafts {
		if !searchMatch(q, d.ID, d.Title) {
			continue
		}
		rows = append(rows, searchRow{ID: d.ID, Title: d.Title, Age: d.Age})
	}
	return rows, ""
}

// searchArchive это группа «Архив»: закрытые задачи с датой закрытия. Ранга и
// цены у архивной строки нет, и выдумывать их не из чего.
func searchArchive(dir, q string) []searchRow {
	var rows []searchRow
	for id, row := range archiveRows(dir) {
		if !searchMatch(q, id, row.Title) {
			continue
		}
		rows = append(rows, searchRow{ID: id, Title: row.Title, Closed: row.Closed, Where: "архив"})
	}
	return rows
}

// docIDRe вынимает ID задачи из имени LLD-документа: у него, в отличие от
// файла задачи, за номером идёт смысловой хвост (DK-306-merge-queue.md).
var docIDRe = regexp.MustCompile(`^[A-Za-z]+-\d+`)

// searchFiles это группа «В тексте задач»: обход docs/tasks с подкаталогами
// и docs/lld. Задача, найденная по номеру или заголовку, сюда не попадает:
// она уже стоит выше своей группой, и вторая её строка была бы шумом. К LLD
// это не относится: дизайн это отдельный документ, и совпадение в нём не
// повторяет строку доски.
func searchFiles(dir, q string, seen map[string]bool, titles map[string]string) []searchRow {
	var rows []searchRow
	walk := func(base string, lld bool) {
		filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			id := strings.TrimSuffix(d.Name(), ".md")
			if lld {
				id = docIDRe.FindString(id)
			} else if seen[id] {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() > searchFileMax {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			quote, num := searchQuote(string(data), q)
			if num == 0 {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return nil
			}
			title := titles[id]
			if title == "" {
				title = searchDocTitle(id, string(data))
			}
			rows = append(rows, searchRow{
				ID: id, Title: title, File: filepath.ToSlash(rel), Line: num,
				Quote: quote, Where: searchWhere(rel),
			})
			return nil
		})
	}
	walk(filepath.Join(dir, "docs", "tasks"), false)
	walk(filepath.Join(dir, "docs", "lld"), true)
	return rows
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "поиск задач")
	if found == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	resp := searchResp{Project: found.Name, Q: q, Groups: []searchGroup{
		{Key: "board", Title: "Доска", Rows: []searchRow{}},
		{Key: "drafts", Title: "Черновики", Rows: []searchRow{}},
		{Key: "archive", Title: "Архив", Rows: []searchRow{}},
		{Key: "text", Title: "В тексте задач", Rows: []searchRow{}},
	}}
	// Запрос короче двух символов ищется по половине доски, и отвечать на него
	// выдачей значит показать всё подряд ещё до того, как человек договорил.
	if utf8.RuneCountInString(q) < searchMinQuery {
		resp.Note = searchShortNote
		writeJSON(w, http.StatusOK, resp)
		return
	}
	needle := strings.ToLower(q)
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	board, err := searchBoard(raw, needle)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "ответ taskctl list --json не разобрался: " + err.Error()})
		return
	}
	drafts, note := s.searchDrafts(found.Path, needle)
	archive := searchArchive(found.Path, needle)
	// Заголовки найденного по номеру и имени: ими подписывается и текстовая
	// группа, чтобы строка файла не осталась одной цитатой без имени задачи.
	seen := map[string]bool{}
	titles := map[string]string{}
	for _, rows := range [][]searchRow{board, drafts, archive} {
		for _, row := range rows {
			seen[row.ID] = true
			if titles[row.ID] == "" {
				titles[row.ID] = row.Title
			}
		}
	}
	text := searchFiles(found.Path, needle, seen, titles)
	total := 0
	for i, rows := range [][]searchRow{board, drafts, archive, text} {
		max := searchRowsMax
		if resp.Groups[i].Key == "text" {
			max = searchTextMax
		}
		searchOrder(rows, needle)
		cut, more := searchCut(rows, max)
		resp.Groups[i].Rows, resp.Groups[i].More = cut, more
		if resp.Groups[i].Rows == nil {
			resp.Groups[i].Rows = []searchRow{}
		}
		total += len(rows)
	}
	if note != "" {
		resp.Groups[1].Note = note
	}
	if total == 0 {
		resp.Note = searchNoneNote
	}
	writeJSON(w, http.StatusOK, resp)
}

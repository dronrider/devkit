package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Раздел LLD (круг 2 POC DK-470): дизайны это отдельная сущность со своим
// списком и своей формой, а не хвост экрана задачи. Список отдаёт задачу,
// заголовок документа и дату правки, поиск идёт по имени файла, заголовку и
// тексту; форма читает документ ручкой GET /doc и правит его ручкой PUT /doc.

// handleLldList отдаёт документы docs/lld, свежеправленные сверху. Запрос q
// сначала ищется в имени и заголовке, потом по тексту, и совпадение по тексту
// едет цитатой строкой выдачи, как в поиске задач.
func (s *server) handleLldList(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "список LLD")
	if found == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	needle := strings.ToLower(q)
	rows := []map[string]any{}
	entries, err := os.ReadDir(filepath.Join(found.Path, "docs", "lld"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "docs/lld не прочитался: " + err.Error()})
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(found.Path, "docs", "lld", e.Name()))
		if err != nil {
			continue
		}
		id := docIDRe.FindString(e.Name())
		title := searchDocTitle(id, string(data))
		row := map[string]any{
			"id": id, "file": "lld/" + e.Name(), "title": title,
			"mtime": info.ModTime().Unix(), "date": info.ModTime().Format("02.01.2006"),
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Name()+"\n"+title), needle) {
			quote, num := searchQuote(string(data), q)
			if num == 0 {
				continue
			}
			row["quote"], row["line"] = quote, num
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["mtime"].(int64) > rows[j]["mtime"].(int64) })
	writeJSON(w, http.StatusOK, map[string]any{"project": found.Name, "q": q, "rows": rows})
}

// handleDocPut правит документ с формы LLD. Пишутся только docs/lld: у файла
// задачи своя ручка со своими правилами, а остальное в docs/ дашборд не
// трогает. Правка коммитится и едет в origin тем же путём, что правка файла
// задачи (commitDocs).
func (s *server) handleDocPut(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found := s.findProject(w, r, "правка документа")
	if found == nil {
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if !safeRel(rel) || !strings.HasPrefix(rel, "lld/") || !strings.HasSuffix(rel, ".md") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на документ docs/lld: правится с этой формы только LLD", rel)})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, searchFileMax)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "пустой текст затёр бы документ"})
		return
	}
	full := filepath.Join(found.Path, "docs", filepath.FromSlash(rel))
	if !isFile(full) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "документа docs/" + rel + " нет, новые LLD заводятся в репозитории"})
		return
	}
	text := strings.TrimRight(body.Text, "\n") + "\n"
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("документ не записался: %v", err)})
		return
	}
	relRepo := "docs/" + rel
	subj := "правка " + path.Base(rel) + " с дашборда"
	if id := docIDRe.FindString(path.Base(rel)); id != "" {
		subj = id + " " + subj
	}
	resp := map[string]string{"path": relRepo, "message": "текст " + relRepo + " записан"}
	if note := commitDocs(found.Path, "docs(lld): "+subj, relRepo); note != "" {
		resp["note"] = note
		s.logf("правка %s в %s: %s", relRepo, found.Name, note)
	}
	s.logf("документ %s в %s переписан с дашборда", relRepo, found.Name)
	writeJSON(w, http.StatusOK, resp)
}

// taskIDMentionRe ловит упоминание задачи в тексте файла: и голое DK-NNN, и
// внутри пути tasks/DK-NNN.md.
var taskIDMentionRe = regexp.MustCompile(`[A-Za-z]+-[0-9]+`)

// taskNum вытаскивает номер из ID для сортировки связей: список стоит по
// номеру, а не в порядке упоминания, порядок текста постановки случайный.
func taskNum(id string) int {
	_, num, _ := strings.Cut(id, "-")
	n, _ := strconv.Atoi(num)
	return n
}

// taskLinks собирает блок «Связи» экрана задачи. Порядок пересмотрен
// пользователем: сверху артефакты самой задачи (её LLD, упомянутые чужие
// после собственных), затем открытые задачи со связью держит/после, затем
// остальные открытые по убыванию ранга, закрытые в самом низу; внутри группы
// ранг по убыванию, без ранга (закрытые, архив, упоминание без строки) по
// номеру; мёртвые упоминания в самом низу. Название ищется лестницей: строка
// доски, архив доски (вместе с датой закрытия), файл задачи, запись
// накопителя. За черновиком строки доски нет вовсе, и связь зовётся
// черновиком с признаком draft: показ уводит по нему на экран записи, а не на
// форму несуществующей задачи. Не нашлось ничего, и связь помечается мёртвой
// (gone): хранилища у связей нет, они выводятся из упоминаний ID в тексте, и
// снятая запись сама собой из чужой постановки не уходит. Род связи (после,
// держит) приезжает из зависимостей строки; прочие упоминания идут без рода,
// источник его не различает, и выдумывать нечего.
func taskLinks(projectPath, id, link, text string, rows map[string]boardRow, after, blocks []string) map[string]any {
	seen := map[string]bool{}
	lld := []map[string]any{}
	addDoc := func(rel string, own bool) {
		if rel == "" || seen[rel] || !strings.HasPrefix(rel, "lld/") {
			return
		}
		full := filepath.Join(projectPath, "docs", filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			return
		}
		seen[rel] = true
		docID := docIDRe.FindString(path.Base(rel))
		row := map[string]any{"file": rel, "title": searchDocTitle(docID, string(data))}
		if own {
			row["own"] = true
		}
		lld = append(lld, row)
	}
	// LLD самой задачи ищется по имени файла с её ID: обычай каталога
	// docs/lld это DK-NNN-<slug>.md.
	if own, _ := filepath.Glob(filepath.Join(projectPath, "docs", "lld", id+"-*.md")); len(own) > 0 {
		sort.Strings(own)
		for _, p := range own {
			addDoc("lld/"+filepath.Base(p), true)
		}
	}
	if target := linkPath(link); strings.HasPrefix(target, "lld/") {
		addDoc(target, true)
	}
	for _, rel := range lldRefs(projectPath, text) {
		addDoc(rel, false)
	}
	prefix, _, _ := strings.Cut(id, "-")
	rel := map[string]string{}
	for _, b := range blocks {
		rel[b] = "держит"
	}
	for _, a := range after {
		rel[a] = "после"
	}
	// Архив читается лениво: у задачи без упоминаний закрытых соседей файл
	// docs/TASKS-archive.md разбирать незачем.
	var arch map[string]archiveRow
	tasks := []map[string]any{}
	seenTask := map[string]bool{id: true}
	for _, m := range taskIDMentionRe.FindAllString(text, -1) {
		if seenTask[m] || !strings.HasPrefix(m, prefix+"-") {
			continue
		}
		seenTask[m] = true
		row := map[string]any{"id": m, "kind": "задача"}
		br, here := rows[m]
		title := br.Title
		if !here {
			if arch == nil {
				arch = archiveRows(projectPath)
			}
			if a, hit := arch[m]; hit {
				here, title = true, a.Title
				if a.Closed != "" {
					row["closed"] = a.Closed
				}
			} else if _, txt, hit := archiveFile(projectPath, m); hit {
				here, title = true, searchDocTitle(m, txt)
			} else if t, hit := draftTitleOf(projectPath, m); hit {
				// Последняя ступень лестницы это накопитель: до грумминга ID
				// живёт одним файлом в docs/tasks/drafts/, и прежде такое
				// упоминание доезжало сюда задачей без названия.
				here, title = true, t
				row["kind"] = "черновик"
				row["draft"] = true
			}
		}
		if title != "" {
			row["title"] = title
			if row["kind"] == "задача" && isGoalTitle(title) {
				row["kind"] = "цель"
			}
		} else if here {
			// Запись на месте, а заголовка в ней нет: голым ID связь не
			// остаётся и тут, но мёртвой её звать не за что.
			row["note"] = "названия у записи нет"
		}
		if !here {
			// Ни строки, ни архива, ни записи накопителя: за ID не стоит
			// ничего. Хранилища у связей нет, они выводятся из упоминаний в
			// тексте, поэтому снятый ID из чужой постановки не пропадает, и
			// честно тут не молчать, а назвать связь мёртвой. Слова «удалена»
			// сервер себе не позволяет: он не отличает снятую запись от
			// опечатки в номере, и выдумывать нечего.
			row["kind"] = "нет записи"
			row["gone"] = true
			row["note"] = "ни строки на доске, ни архива, ни черновика"
		}
		if r := rel[m]; r != "" {
			row["rel"] = r
		}
		tasks = append(tasks, row)
	}
	// Группа строки: 0 это открытая со связью держит/после (блокировки важнее
	// прочего), 1 это остальные открытые вместе с черновиками, 2 это закрытые,
	// 3 это мёртвые упоминания. Ранг берётся со строки доски; у закрытых,
	// черновиков и упоминаний без строки его нет, ноль честно уводит их за
	// ранжированных соседей. Мёртвая связь лежит ниже закрытой даже с родом
	// держит/после: делать нечего ни с той, ни с другой, а место наверху стоит
	// держать за живыми.
	group := func(row map[string]any) int {
		switch {
		case row["gone"] != nil:
			return 3
		case row["closed"] != nil:
			return 2
		case row["rel"] != nil:
			return 0
		}
		return 1
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if gi, gj := group(tasks[i]), group(tasks[j]); gi != gj {
			return gi < gj
		}
		ri, rj := rows[tasks[i]["id"].(string)].R, rows[tasks[j]["id"].(string)].R
		if ri != rj {
			return ri > rj
		}
		return taskNum(tasks[i]["id"].(string)) < taskNum(tasks[j]["id"].(string))
	})
	if len(lld) == 0 && len(tasks) == 0 {
		return nil
	}
	return map[string]any{"lld": lld, "tasks": tasks}
}

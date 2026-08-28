package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Состав цели сабтасками (макет «06 Цель»): GET
// /api/projects/{p}/goals/{id}/tasks отдаёт задачи цели со статусом и судьбой.
// Своего реестра состава у цели нет и заводить его незачем: нарезка живёт
// разделом «Задачи цели» файла цели, его пишет скилл goal-loop, и читается он
// оттуда же, откуда журнал (goalDocPath, DK-255). Статус берётся со строки
// доски, а закрытая задача с доски уезжает, поэтому вторым источником идёт
// архив: без него половина состава долгой цели выглядела бы потерянной.

const goalTasksHeader = "## Задачи цели"

// goalTask это одна задача состава: ID с судьбой из файла цели и статус со
// строки доски либо из архива.
type goalTask struct {
	ID   string `json:"id"`
	Fate string `json:"fate,omitempty"`
	// Title, Sect, Section, R и P приезжают со строки доски, Closed из архива.
	Title   string `json:"title,omitempty"`
	Sect    string `json:"sect,omitempty"`
	Section string `json:"section,omitempty"`
	R       int    `json:"r,omitempty"`
	P       string `json:"p,omitempty"`
	Closed  string `json:"closed,omitempty"`
	Done    bool   `json:"done"`
	// Draft говорит, что за ID стоит запись накопителя: строки на доске нет,
	// потому что задача ещё черновик, и это не потеря, а этап.
	Draft bool   `json:"draft,omitempty"`
	Note  string `json:"note,omitempty"`
}

var taskIDRe = regexp.MustCompile(`[A-Za-z]+-[0-9]+`)

// docSection вырезает текст раздела markdown до следующего заголовка того же
// уровня.
func docSection(doc, header string) (string, bool) {
	var out []string
	in := false
	for _, ln := range strings.Split(doc, "\n") {
		trimmed := strings.TrimRight(ln, " ")
		if trimmed == header {
			in = true
			continue
		}
		if in && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if in {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n"), in
}

// docUnits режет раздел на смысловые куски: элемент списка вместе с его
// переносами и отдельный абзац прозы. ID ищется внутри куска, а не в строке:
// нарезка пишется и списком, и абзацем («дорезка вторая: DK-252 (...), ...»),
// и построчный разбор терял бы половину состава на переносах.
func docUnits(text string) []string {
	var units []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			units = append(units, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, ln := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			flush()
		}
		cur = append(cur, trimmed)
	}
	flush()
	return units
}

// unitMember решает, задача это цели или просто упомянутый ID. Членом состава
// считается ID, который открывает элемент списка либо назван с метаданными в
// скобках («DK-252 (bug, M, R=39, ...)»), включая перечисление через запятую
// перед общими скобками. Так из состава выпадают чужие упоминания вроде «ждёт
// XR-252» и «остаётся черновиком DK-136 вне цели»: они про соседей, а не про
// нарезку.
func unitMember(unit string, loc []int, nextMember bool) (fate string, ok bool) {
	tail := strings.TrimSpace(unit[loc[1]:])
	bullet := strings.HasPrefix(unit, "- ") && loc[0] == 2
	if bullet {
		return strings.TrimSpace(strings.TrimLeft(tail, ".,: ")), true
	}
	if strings.HasPrefix(tail, "(") {
		if cut := strings.IndexByte(tail, ')'); cut > 0 {
			return strings.TrimSpace(tail[1:cut]), true
		}
		return strings.TrimSpace(tail), true
	}
	// Перечисление через запятую: судьба у всей связки одна и лежит в скобках
	// последнего ID («DK-249, DK-250, DK-251 (task, M, R=34, ...)»).
	if strings.HasPrefix(tail, ",") && nextMember {
		return "", true
	}
	return "", false
}

// goalTasksFromDoc разбирает раздел «Задачи цели» файла цели. Второй результат
// говорит, нашёлся ли сам раздел: цель без нарезки и цель без раздела это
// разные пустоты, и на экране они называются по-разному.
func goalTasksFromDoc(doc, prefix string) (tasks []goalTask, section bool) {
	text, section := docSection(doc, goalTasksHeader)
	if !section {
		return nil, false
	}
	seen := map[string]bool{}
	for _, unit := range docUnits(text) {
		hits := taskIDRe.FindAllStringIndex(unit, -1)
		fates := make([]string, len(hits))
		member := make([]bool, len(hits))
		// Справа налево: судьба перечисления лежит у последнего его ID, и
		// узнать её левым соседям можно только после него.
		for i := len(hits) - 1; i >= 0; i-- {
			nextMember := i+1 < len(hits) && member[i+1]
			fate, ok := unitMember(unit, hits[i], nextMember)
			if ok && fate == "" && nextMember {
				fate = fates[i+1]
			}
			fates[i], member[i] = fate, ok
		}
		for i, loc := range hits {
			id := unit[loc[0]:loc[1]]
			if !member[i] || seen[id] {
				continue
			}
			if prefix != "" && !strings.HasPrefix(id, prefix+"-") {
				continue
			}
			seen[id] = true
			tasks = append(tasks, goalTask{ID: id, Fate: fates[i]})
		}
	}
	return tasks, true
}

// archiveRow это строка архива, из которой состав берёт судьбу закрытой
// задачи: заголовок и дату закрытия. Архив читается файлом, а не утилитой:
// своего --json у него нет, а спрашивать taskctl show по каждой закрытой
// задаче значит поднимать подпроцесс на строку состава.
type archiveRow struct {
	Title  string
	Closed string
}

func archiveRows(projectPath string) map[string]archiveRow {
	rows := map[string]archiveRow{}
	data, err := os.ReadFile(filepath.Join(projectPath, "docs", "TASKS-archive.md"))
	if err != nil {
		return rows
	}
	for _, ln := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(ln)
		if !strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "|-") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) < 5 {
			continue
		}
		id := strings.TrimSpace(cells[0])
		if !goalIDRe.MatchString(id) {
			continue
		}
		rows[id] = archiveRow{Title: strings.TrimSpace(cells[1]), Closed: strings.TrimSpace(cells[4])}
	}
	return rows
}

// goalCounts это счётчики шапки состава: закрыто, в работе, впереди. Задача
// без строки и без архива считается впереди: она названа нарезкой, но на доску
// ещё не встала.
type goalCounts struct {
	Total   int `json:"total"`
	Closed  int `json:"closed"`
	Running int `json:"running"`
	Ahead   int `json:"ahead"`
}

// fillGoalTasks дописывает задачам цели то, что о них знают доска и архив, и
// считает счётчики шапки. Вынесено из обработчика, потому что тем же счётом
// живёт кольцо в шапке разговора (pulse.go): у цели прогресс это доля закрытых
// задач, и второй счёт разъехался бы с экраном цели на первой же правке.
func fillGoalTasks(projectPath string, tasks []goalTask, rows map[string]boardRow, arch map[string]archiveRow) goalCounts {
	counts := goalCounts{Total: len(tasks)}
	for i := range tasks {
		t := &tasks[i]
		if row, hit := rows[t.ID]; hit {
			t.Title, t.Sect, t.Section = row.Title, row.Sect, row.Section
			t.R, t.P = row.R, row.P
			if row.Sect == sectRun {
				counts.Running++
			} else {
				counts.Ahead++
			}
			continue
		}
		if a, hit := arch[t.ID]; hit {
			t.Title, t.Closed, t.Done = a.Title, a.Closed, true
			t.Sect, t.Section = "archive", "закрыта"
			counts.Closed++
			continue
		}
		// Нарезка называет и то, что пока лежит записью накопителя. Прежде такая
		// задача подписывалась фразой про отсутствие строки, и человек читал её
		// как поломку состава (замечание пользователя). Черновик тут метка, а
		// заголовок берётся из самой записи: открыть её экран есть чем.
		if title, hit := draftTitleOf(projectPath, t.ID); hit {
			t.Draft, t.Title = true, title
		} else {
			t.Note = "ни на доске, ни в архиве: строкой задача ещё не заведена"
		}
		counts.Ahead++
	}
	return counts
}

// goalProgress отдаёт счётчики задач цели, ничего не рисуя: файла цели может не
// быть, раздела «Задачи цели» в нём может не быть, и раздел бывает пуст. Во
// всех трёх случаях второй ответ false, и прогресса у цели нет, а не ноль
// процентов: пустая шкала и нечитаемый файл это разные вещи.
func (s *server) goalProgress(projectPath, id, prefix string, rows map[string]boardRow) (goalCounts, bool) {
	doc := s.goalDocPath(projectPath, id)
	if !doc.seen {
		return goalCounts{}, false
	}
	data, err := os.ReadFile(doc.path)
	if err != nil {
		return goalCounts{}, false
	}
	tasks, section := goalTasksFromDoc(string(data), prefix)
	if !section || len(tasks) == 0 {
		return goalCounts{}, false
	}
	return fillGoalTasks(projectPath, tasks, rows, archiveRows(projectPath)), true
}

func (s *server) handleGoalTasks(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "задачи цели")
	if found == nil {
		return
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	view, _ := parseBoardView(raw)
	rows, _ := parseBoardRows(raw)
	doc := s.goalDocPath(found.Path, id)
	resp := map[string]any{"goal": id, "file": doc.rel, "tasks": []goalTask{},
		"counts": goalCounts{}}
	// Пустота различима: файла цели нет вовсе, раздела в нём нет, раздел есть и
	// пуст. Пустой список без слов неотличим от неотрисованного состава.
	if !doc.seen {
		resp["note"] = fmt.Sprintf("файла цели %s в %s нет: состав читать неоткуда", doc.rel, found.Name)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	data, err := os.ReadFile(doc.path)
	if err != nil {
		resp["note"] = fmt.Sprintf("файл цели %s не прочитался: %v", doc.rel, err)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	tasks, section := goalTasksFromDoc(string(data), view.Prefix)
	if !section {
		resp["note"] = fmt.Sprintf("в файле цели %s нет раздела «Задачи цели»: цель ещё не нарезана на задачи", doc.rel)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if len(tasks) == 0 {
		resp["note"] = fmt.Sprintf("раздел «Задачи цели» файла %s есть, но задач в нём не названо", doc.rel)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	counts := fillGoalTasks(found.Path, tasks, rows, archiveRows(found.Path))
	resp["tasks"], resp["counts"] = tasks, counts
	writeJSON(w, http.StatusOK, resp)
}

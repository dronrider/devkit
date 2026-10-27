package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Межпроектный обзор главной (DK-1465). Прежде главная была списком проектов
// с карточкой квоты, и вопрос «что сейчас горит» приходилось собирать руками
// по вкладкам: просроченное искать на доске каждого проекта, ждущих держать
// в полке шапки, сделанное сверять с архивом. Обзор собирает всё это одной
// машинной ручкой, как полку ждущих: у главной своего проекта нет, и просить
// у неё имя проекта в пути неоткуда.
//
// Источники те же, что у экранов проектов: доска через projectBoard (с памятью
// по отпечатку файла), архив через archiveRows, записи накопителя каталогом,
// прогресс цели тем же счётом, что кольцо разговора (goalProgress). Своего
// разбора markdown обзор не заводит.

// Порции виджетов. Обзор это экран «всё ли в порядке», а не новая доска: полные
// списки живут на своих экранах, и порция здесь отвечает на вопрос «заглянуть
// в проект или пройти мимо». Вершина это тройка, потому что больше строк
// глаза не охватывают одним взглядом; вехи и сделанное шире, их читают лентой.
const (
	overviewTopLimit       = 3
	overviewMilestoneLimit = 6
	overviewDraftLimit     = 5
	overviewDoneLimit      = 7
)

// overviewDoneWeek это глубина виджета сделанного: неделя, как в неделе
// обзора у человека, которому отчёт про большую дату ничего не говорит.
const overviewDoneWeek = 7 * 24 * time.Hour

// overviewRow это строка доски одного проекта, какой её видит главная: полей
// ровно столько, сколько рисуют виджеты, вся строка целиком живёт на доске.
type overviewRow struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Sect    string `json:"sect,omitempty"`
	P       string `json:"p,omitempty"`
	R       int    `json:"r,omitempty"`
	// Due это срок строки датой 2006-01-02, тем же полем, что у доски
	// (DK-1464). Пусто у строки без срока.
	Due string `json:"due,omitempty"`
}

// overviewGoal это цель в работе с прогрессом состава: доля закрытых задач
// цели тем же счётом, что кольцо в шапке разговора (fillGoalTasks), чтобы
// главная и разговор не расходились числами.
type overviewGoal struct {
	overviewRow
	Total   int `json:"total"`
	Closed  int `json:"closed"`
	Running int `json:"running"`
	Ahead   int `json:"ahead"`
}

// overviewDraft это запись накопителя без разбора: ID, заголовок из файла и
// дата правки. Дата едет тем же видом, что у строки накопителя на экране
// проекта (день, не возраст).
type overviewDraft struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Moved   string `json:"moved,omitempty"`
}

// overviewDone это закрытая задача недели: дата закрытия берётся из архива,
// и по ней же виджет отбирает строку.
type overviewDone struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Closed  string `json:"closed"`
}

// overviewView это ответ ручки: списки по виджетам и причины отказов досок.
// Полки ждущих тут нет: она живёт своей ручкой /api/waiting и уже опрашивается
// с главной, второй сбор гонял бы те же доски второй раз.
type overviewView struct {
	Overdue    []overviewRow   `json:"overdue"`
	Today      []overviewRow   `json:"today"`
	Top        []overviewRow   `json:"top"`
	Milestones []overviewRow   `json:"milestones"`
	Goals      []overviewGoal  `json:"goals"`
	Drafts     []overviewDraft `json:"drafts"`
	Done       []overviewDone  `json:"done"`
	Errors     []string        `json:"errors"`
}

// overviewBundle это вклад одного проекта в обзор, собранный параллельно с
// соседями. Отказ доски не отменяет вклада остальных: причина едет строкой
// ошибки, а пустой список при нечитаемой доске это не «просроченного нет».
type overviewBundle struct {
	overdue, today, top, milestones []overviewRow
	goals                           []overviewGoal
	drafts                          []overviewDraft
	done                            []overviewDone
	err                             string
}

// overviewProject собирает вклад одного проекта. Сбор идёт по живой доске:
// закрытые строки уехали в архив и попадают в обзор только виджетом сделанного.
func (s *server) overviewProject(p Project, now time.Time) (overviewBundle, error) {
	var out overviewBundle
	raw, err := s.projectBoard(p.Path)
	if err != nil {
		return out, err
	}
	sects, err := parseBoardSects(raw)
	if err != nil {
		return out, err
	}
	rows, _ := parseBoardRows(raw)
	view, _ := parseBoardView(raw)
	today := now.Format(draftDateLayout)
	for _, sec := range sects {
		for _, row := range sec.Rows {
			item := overviewRow{Project: p.Name, ID: row.ID, Title: row.Title,
				Sect: sec.Key, P: row.P, R: row.R, Due: row.Due}
			switch {
			case row.Due != "" && row.Due < today:
				out.overdue = append(out.overdue, item)
			case row.Due == today:
				out.today = append(out.today, item)
			case row.Due != "":
				out.milestones = append(out.milestones, item)
			}
			// Цель живёт своим виджетом с прогрессом состава, и в вершину
			// очереди она не встаёт: у очереди стоят задачи, а цель это
			// контейнер задач, и двойное место на главной говорило бы о ней
			// дважды.
			if !strings.HasPrefix(row.Title, goalPrefix) {
				out.top = append(out.top, item)
			}
		}
	}
	out.goals = s.overviewGoals(p, sects, view.Prefix, rows)
	out.drafts = overviewDrafts(p.Name, p.Path, overviewDraftLimit)
	out.done = overviewDoneWeekRows(p.Name, s.archiveRows(p.Path), now, overviewDoneLimit)
	return out, nil
}

// goalPrefix узнаёт цель по заголовку строки: слово «Цель:» стоит в нём
// первым, отдельного признака у цели на доске нет. Тем же правилом цель
// узнают экран задачи и список работ.
const goalPrefix = "Цель:"

// overviewGoals собирает цели в работе с прогрессом состава. «В работе» это
// секция In progress: цель в Backlog ещё не начата, и прогресс у неё был бы
// событием чужой работы. Прогресс читается тем же счётом, что кольцо
// разговора цели, а не пересчитывается здесь.
func (s *server) overviewGoals(p Project, sects []boardSect, prefix string, rows map[string]boardRow) []overviewGoal {
	var out []overviewGoal
	for _, sec := range sects {
		if sec.Key != sectRun {
			continue
		}
		for _, row := range sec.Rows {
			if !strings.HasPrefix(row.Title, goalPrefix) {
				continue
			}
			g := overviewGoal{overviewRow: overviewRow{Project: p.Name, ID: row.ID,
				Title: row.Title, Sect: sec.Key, P: row.P, R: row.R}}
			if counts, ok := s.goalProgress(p.Path, row.ID, prefix, rows); ok {
				g.Total, g.Closed, g.Running, g.Ahead =
					counts.Total, counts.Closed, counts.Running, counts.Ahead
			}
			out = append(out, g)
		}
	}
	return out
}

// overviewDrafts читает записи накопителя каталогом, а не подпроцессом
// taskctl: накопитель это просто файлы, истина о числе записей у каталога та
// же, а прогул по каталогу дешевле похода в утилиту на каждый заход главной.
// Порядок по времени файла: «новое без разбора» спрашивает, что записалось
// последним, а не что стоит первым по ID.
func overviewDrafts(name, projectPath string, limit int) []overviewDraft {
	dir := filepath.Join(projectPath, "docs", "tasks", "drafts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type held struct {
		item overviewDraft
		at   time.Time
	}
	var out []held
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		item := overviewDraft{Project: name, ID: id}
		if title, ok := draftTitleOf(projectPath, id); ok {
			item.Title = title
		}
		if fi, err := e.Info(); err == nil {
			item.Moved = fi.ModTime().Format(draftDateLayout)
			out = append(out, held{item: item, at: fi.ModTime()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.After(out[j].at) })
	items := make([]overviewDraft, 0, len(out))
	for _, h := range out {
		if len(items) >= limit {
			break
		}
		items = append(items, h.item)
	}
	return items
}

// overviewDoneWeekRows отбирает из архива закрытое за неделю. Дата закрытия
// это поле архива, сравнение идёт строкой: обе даты в одном виде 2006-01-02,
// и лексический порядок у него совпадает с хронологическим.
func overviewDoneWeekRows(name string, arch map[string]archiveRow, now time.Time, limit int) []overviewDone {
	since := now.Add(-overviewDoneWeek).Format(draftDateLayout)
	var out []overviewDone
	for id, row := range arch {
		if row.Closed < since {
			continue
		}
		out = append(out, overviewDone{Project: name, ID: id, Title: row.Title, Closed: row.Closed})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Closed != out[j].Closed {
			return out[i].Closed > out[j].Closed
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// handleOverview отдаёт обзор целиком. Ручка машинная, как полка ждущих:
// место одно на дашборд и своего проекта у него нет.
func (s *server) handleOverview(w http.ResponseWriter, r *http.Request) {
	resp := s.overview()
	writeJSON(w, http.StatusOK, resp)
}

// overview собирает ответ по всем проектам машины. Доски читаются параллельно
// тем же числом рабочих, что список проектов: каждый проект стоит своего
// подпроцесса taskctl, и по очереди главная ждала бы их сумму.
func (s *server) overview() overviewView {
	projects, errs := s.projects()
	bundles := make([]overviewBundle, len(projects))
	broken := inParallel(projectWorkers, len(projects), func(i int) {
		b, err := s.overviewProject(projects[i], s.now())
		if err != nil {
			b.err = err.Error()
		}
		bundles[i] = b
	})
	// Паника воркера не оставляет виджет молчанием: проект назван причиной,
	// как его называет список проектов.
	for i, err := range broken {
		if err != nil {
			bundles[i].err = s.notePanic("обзор "+projects[i].Name, err)
		}
	}
	view := overviewView{
		Overdue: []overviewRow{}, Today: []overviewRow{}, Top: []overviewRow{},
		Milestones: []overviewRow{}, Goals: []overviewGoal{},
		Drafts: []overviewDraft{}, Done: []overviewDone{},
	}
	for i := range bundles {
		b := &bundles[i]
		if b.err != "" {
			errs = append(errs, projects[i].Name+": "+b.err)
			continue
		}
		view.Overdue = append(view.Overdue, b.overdue...)
		view.Today = append(view.Today, b.today...)
		view.Top = append(view.Top, b.top...)
		view.Milestones = append(view.Milestones, b.milestones...)
		view.Goals = append(view.Goals, b.goals...)
		view.Drafts = append(view.Drafts, b.drafts...)
		view.Done = append(view.Done, b.done...)
	}
	view.Errors = errs
	// Просроченное стоит по сроку: дольше всех горящее первым. Сегодня без
	// своего порядка ранжируется: день один, и внутри него очередь по рангу.
	sort.Slice(view.Overdue, func(i, j int) bool {
		if view.Overdue[i].Due != view.Overdue[j].Due {
			return view.Overdue[i].Due < view.Overdue[j].Due
		}
		return rowLess(view.Overdue[i], view.Overdue[j])
	})
	sort.SliceStable(view.Today, func(i, j int) bool {
		return rankFirst(view.Today[i], view.Today[j])
	})
	sort.Slice(view.Top, func(i, j int) bool {
		return rankFirst(view.Top[i], view.Top[j])
	})
	if len(view.Top) > overviewTopLimit {
		view.Top = view.Top[:overviewTopLimit]
	}
	// Вехи лентой по датам: ближайшая дата первой, дальние уезжают за порцию.
	sort.Slice(view.Milestones, func(i, j int) bool {
		if view.Milestones[i].Due != view.Milestones[j].Due {
			return view.Milestones[i].Due < view.Milestones[j].Due
		}
		return rowLess(view.Milestones[i], view.Milestones[j])
	})
	if len(view.Milestones) > overviewMilestoneLimit {
		view.Milestones = view.Milestones[:overviewMilestoneLimit]
	}
	// Накопитель и сделанное сортируются и режутся после слияния: порция на
	// проект это только глубина чтения, а виджет держит порцию по всей машине.
	// Без общего реза десяток проектов растягивал бы оба списка во столько же
	// раз, и «новое без разбора» переставало быть одним взглядом.
	sort.SliceStable(view.Drafts, func(i, j int) bool {
		if view.Drafts[i].Moved != view.Drafts[j].Moved {
			return view.Drafts[i].Moved > view.Drafts[j].Moved
		}
		if view.Drafts[i].Project != view.Drafts[j].Project {
			return view.Drafts[i].Project < view.Drafts[j].Project
		}
		return view.Drafts[i].ID < view.Drafts[j].ID
	})
	if len(view.Drafts) > overviewDraftLimit {
		view.Drafts = view.Drafts[:overviewDraftLimit]
	}
	sort.SliceStable(view.Done, func(i, j int) bool {
		if view.Done[i].Closed != view.Done[j].Closed {
			return view.Done[i].Closed > view.Done[j].Closed
		}
		if view.Done[i].Project != view.Done[j].Project {
			return view.Done[i].Project < view.Done[j].Project
		}
		return view.Done[i].ID < view.Done[j].ID
	})
	if len(view.Done) > overviewDoneLimit {
		view.Done = view.Done[:overviewDoneLimit]
	}
	return view
}

// rankFirst ставит строку с большим рангом раньше: и вершина, и «сегодня»
// отвечают на вопрос, за что браться, а порядок очереди держит ранг.
func rankFirst(a, b overviewRow) bool {
	if a.R != b.R {
		return a.R > b.R
	}
	return rowLess(a, b)
}

// rowLess это устойчивый конец сортировок: равные по главному порядку строки
// идут по проекту и ID, и два захода подряд рисуют главную одинаково.
func rowLess(a, b overviewRow) bool {
	if a.Project != b.Project {
		return a.Project < b.Project
	}
	return a.ID < b.ID
}

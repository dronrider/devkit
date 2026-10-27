package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Межпроектный обзор главной (DK-1465). Стенд держит два проекта с разными
// досками: обзор собирается по всем доскам машины, и один проект ничего о
// втором не знает. Проверяются сами виджеты: просроченное и сегодня по сроку
// строки (DK-1464), вершина по рангу без целей, вехи лентой по датам, цели в
// работе с прогрессом состава, новое накопителя и закрытое за неделю, а рядом
// с этим причина отказа нечитаемой доски.

// ovWrite кладёт файл стенда, заведя каталоги по пути.
func ovWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ovDate это час стенда: все сроки отсчитываются от него, чтобы тест не зависел
// от дня прогона.
var ovDate = time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local)

// ovBoardDemo это доска demo: просроченная строка в работе, строка на сегодня
// в Check, две вехи в Backlog и цель в работе. Ранги расставлены так, что
// вершина из трёх строк собирается из обоих проектов, а цель с рангом выше
// соседок в неё не встаёт.
const ovBoardDemo = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-101","title":"Горит дольше всех","type":"task","p":"P1","r":50,"due":"2026-10-25","r_parts":[45,2,1,0,2],"cost":"M","link":"-"},` +
	`{"id":"XR-105","title":"Цель: пробный цикл обзора","type":"task","p":"P2","r":60,"r_parts":[40,10,3,0,7],"cost":"XL","link":"[tasks/XR-105.md](tasks/XR-105.md)"}]},` +
	`{"key":"check","title":"Check","rows":[{"id":"XR-102","title":"Сдаётся сегодня","type":"task","p":"P2","r":40,"due":"2026-10-27","r_parts":[30,5,2,0,3],"cost":"M","link":"-"}]},` +
	`{"key":"backlog","title":"Backlog","rows":[` +
	`{"id":"XR-103","title":"Ближняя веха","type":"task","p":"P2","r":30,"due":"2026-10-29","r_parts":[25,2,1,0,2],"cost":"S","link":"-"},` +
	`{"id":"XR-104","title":"Дальняя веха","type":"task","p":"P3","r":20,"due":"2026-11-02","r_parts":[15,2,1,0,2],"cost":"S","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[]}]}`

// ovBoardOther это доска второго проекта: вершина по рангу и своя просрочка,
// чтобы сортировка виджетов показывалась на стыке проектов.
const ovBoardOther = `{"prefix":"OR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[]},` +
	`{"key":"check","title":"Check","rows":[]},` +
	`{"key":"backlog","title":"Backlog","rows":[` +
	`{"id":"OR-201","title":"Своя просрочка","type":"bug","p":"P1","r":70,"due":"2026-10-26","r_parts":[60,5,2,0,3],"cost":"M","link":"-"},` +
	`{"id":"OR-202","title":"Вершина машины","type":"task","p":"P1","r":90,"due":"","r_parts":[80,5,2,0,3],"cost":"L","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[]}]}`

// ovEnv поднимает стенд с двумя проектами и фикстурой taskctl, раздающей
// доскам разные ответы по каталогу из -C.
func ovEnv(t *testing.T, otherBoard string) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	mkProject(t, filepath.Join(e.home, "projects", "other"))
	script := fmt.Sprintf(`case "$*" in
  *%q*) echo '%s' ;;
  *) echo '%s' ;;
esac`, filepath.Join(e.home, "projects", "other"), otherBoard, ovBoardDemo)
	writeScript(t, e.bin, "taskctl", script)
	writeScript(t, e.bin, "tmux", "exit 1")
	e.s.now = func() time.Time { return ovDate }

	// Состав цели: одна строка в работе и одна закрытая на неделе.
	goalDoc := "# XR-105: Цель: пробный цикл обзора\n\n## Задачи цели\n\n" +
		"- XR-101 (task, M, R=50). Просроченная строка в работе.\n" +
		"- XR-001 (task, S, R=20). Закрытая на неделе, лежит в архиве.\n"
	ovWrite(t, filepath.Join(e.proj, "docs", "tasks", "XR-105.md"), goalDoc)
	archive := "# Архив demo\n\n" +
		"| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n" +
		"| XR-001 | Закрытая на неделе | task | P2 | 2026-10-26 | - |\n" +
		"| XR-000 | Закрытая давно | task | P3 | 2026-09-01 | - |\n"
	ovWrite(t, filepath.Join(e.proj, "docs", "TASKS-archive.md"), archive)
	return e, e.loggedClient(t)
}

func ovWriteDraft(t *testing.T, proj, id, title string, at time.Time) {
	t.Helper()
	dir := filepath.Join(proj, "docs", "tasks", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	ovWrite(t, path, "# "+id+": "+title+"\n\n### Ситуация\n\nтекст записи.\n")
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// ovView это машинный вид ответа обзора, выписанный тестом своими типами, а не
// типами сервера: регчек гоняет этот файл на старом коде, где обзора ещё нет, и
// краснота обязана выйти отказом ручки, а не отсутствием символов.
type ovView struct {
	Overdue, Today, Top, Milestones []ovRow
	Goals                           []ovGoal
	Drafts                          []ovDraft
	Done                            []ovDone
	Errors                          []string
}

type ovRow struct {
	Project string `json:"project"`
	ID      string `json:"id"`
}

type ovGoal struct {
	ID      string `json:"id"`
	Total   int    `json:"total"`
	Closed  int    `json:"closed"`
	Running int    `json:"running"`
}

type ovDraft struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ovDone struct {
	ID     string `json:"id"`
	Closed string `json:"closed"`
}

func getOverview(t *testing.T, e *testEnv, c *http.Client) ovView {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/overview", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("обзор: %d %s", resp.StatusCode, text)
	}
	var got ovView
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ обзора не разобрался: %v\n%s", err, text)
	}
	return got
}

// ovIDs сворачивает список строк в строку ID через запятую: сравнение списков
// одним взглядом называет и пропавшую, и лишнюю.
func ovIDs[T interface{ ~[]E }, E interface{ id() string }](list T) string {
	var ids []string
	for _, it := range list {
		ids = append(ids, it.id())
	}
	return strings.Join(ids, ",")
}

func (r ovRow) id() string   { return r.ID }
func (g ovGoal) id() string  { return g.ID }
func (d ovDraft) id() string { return d.ID }
func (d ovDone) id() string  { return d.ID }

// Обзор собирает все семь виджетов с двух досок: сроки расходятся по своим
// спискам, вершина ранжируется по рангу и не держит цель, вехи идут по датам,
// цель несёт прогресс состава, накопитель и архив отдают свои порции.
func TestOverviewWidgets(t *testing.T) {
	e, c := ovEnv(t, ovBoardOther)
	ovWriteDraft(t, e.proj, "XR-009", "старая запись", ovDate.Add(-48*time.Hour))
	ovWriteDraft(t, e.proj, "XR-010", "свежая запись", ovDate.Add(-1*time.Hour))

	got := getOverview(t, e, c)
	if len(got.Errors) != 0 {
		t.Fatalf("обзор пожаловался на живые доски: %v", got.Errors)
	}
	if ids := ovIDs(got.Overdue); ids != "XR-101,OR-201" {
		t.Errorf("просроченное %s, ожидал XR-101,OR-201 по сроку", ids)
	}
	if ids := ovIDs(got.Today); ids != "XR-102" {
		t.Errorf("сегодня %s, ожидал одну строку XR-102", ids)
	}
	// Вершина это тройка по рангу: цель XR-105 с рангом 60 в неё не встаёт.
	if ids := ovIDs(got.Top); ids != "OR-202,OR-201,XR-101" {
		t.Errorf("вершина %s, ожидал OR-202,OR-201,XR-101", ids)
	}
	if ids := ovIDs(got.Milestones); ids != "XR-103,XR-104" {
		t.Errorf("вехи %s, ожидал XR-103,XR-104 по датам", ids)
	}
	if ids := ovIDs(got.Drafts); ids != "XR-010,XR-009" {
		t.Errorf("накопитель %s, ожидал свежую запись первой", ids)
	}
	if len(got.Drafts) == 2 && got.Drafts[0].Title != "свежая запись" {
		t.Errorf("заголовок записи накопителя не прочитался: %+v", got.Drafts[0])
	}
	if ids := ovIDs(got.Done); ids != "XR-001" {
		t.Errorf("сделанное за неделю %s, ожидал одну закрытую на неделе строку", ids)
	}
	if len(got.Goals) != 1 {
		t.Fatalf("целей в работе %d, ожидал одну: %+v", len(got.Goals), got.Goals)
	}
	goal := got.Goals[0]
	if goal.ID != "XR-105" || goal.Total != 2 || goal.Closed != 1 || goal.Running != 1 {
		t.Errorf("прогресс цели не тот: %+v", goal)
	}
}

// Отказ одной доски не отменяет обзора: строки живого проекта остаются, а
// сломанный назван причиной рядом, потому что пустой виджет при нечитаемой
// доске читался бы как «просроченного нет».
func TestOverviewNamesBrokenBoard(t *testing.T) {
	e, c := ovEnv(t, "exit 1")

	got := getOverview(t, e, c)
	if len(got.Errors) != 1 || !strings.Contains(got.Errors[0], "other") {
		t.Fatalf("отказ доски other не назван: %v", got.Errors)
	}
	if ids := ovIDs(got.Overdue); ids != "XR-101" {
		t.Errorf("просроченное живого проекта %s, ожидал XR-101", ids)
	}
	if ids := ovIDs(got.Top); ids != "XR-101,XR-102,XR-103" {
		t.Errorf("вершина %s, ожидал тройку живой доски по рангу", ids)
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Поиск задач. Стенд тот же, что у правки строки: настоящий taskctl на
// фикстурной доске, накопитель заводится его же командой, архив кладётся
// файлом, файлы задач лежат в docs/tasks вместе с подкаталогами archive/<год>
// и drafts. Предмет проверки это выдача целиком: состав групп, порядок,
// потолки и слова пустых случаев.

// searchEnv поднимает доску с четырьмя строками, накопителем, архивом и
// текстами файлов задач.
func searchEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e, c, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "add", "--id", "XR-010", "--title", "Поиск задач по доске и архиву",
		"--rank", "25+7+3+0+2", "--cost", "M", "--accept", "agent")
	runTaskctl(t, e.proj, "draft", "--prio", "mid", "поиск с телефона: лупа рядом с колокольчиком")
	arch := "# Архив (префикс XR)\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n" +
		"| XR-900 | Поиск по архиву | task | P2 | 2026-08-01 | - |\n"
	writeAt(t, filepath.Join(e.proj, "docs", "TASKS-archive.md"), arch)
	// Файлы задач: живая, архивная и лишняя. Слово «колокольчик» стоит только
	// в тексте, ни в одном заголовке строки его нет.
	writeAt(t, filepath.Join(e.proj, "docs", "tasks", "XR-004.md"),
		"# XR-004: Четвёртая\n\nПоле стоит слева, колокольчик справа.\n")
	writeAt(t, filepath.Join(e.proj, "docs", "tasks", "archive", "2026", "XR-900.md"),
		"# XR-900: Поиск по архиву\n\nЗакрытая задача, колокольчик тут же.\n")
	writeAt(t, filepath.Join(e.proj, "docs", "tasks", "XR-002.md"),
		"# XR-002: Вторая\n\nРанг 25+7+3+0+2, слагаемые по RANKING.md.\n")
	return e, c
}

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// searched спрашивает ручку поиска и разбирает выдачу.
func searched(t *testing.T, c *http.Client, e *testEnv, q string) searchResp {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/search?q="+url.QueryEscape(q), "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("поиск %q: %d %s", q, resp.StatusCode, text)
	}
	var v searchResp
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("выдача поиска не разобралась: %v\n%s", err, text)
	}
	return v
}

func group(t *testing.T, got searchResp, key string) searchGroup {
	t.Helper()
	for _, g := range got.Groups {
		if g.Key == key {
			return g
		}
	}
	t.Fatalf("в выдаче нет группы %q: %+v", key, got.Groups)
	return searchGroup{}
}

func rowIDs(g searchGroup) []string {
	ids := []string{}
	for _, row := range g.Rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// Выдача идёт четырьмя группами в постоянном порядке, и каждая группа отвечает
// своим источником: живая доска, накопитель, архив и текст файлов задач.
// Группы нужны все четыре сразу: без архива и накопителя человеку неоткуда
// узнать, задачи нет вовсе или искали не там.
func TestSearchGroups(t *testing.T) {
	e, c := searchEnv(t)
	got := searched(t, c, e, "поиск")
	var keys, titles []string
	for _, g := range got.Groups {
		keys = append(keys, g.Key)
		titles = append(titles, g.Title)
	}
	if strings.Join(keys, ",") != "board,drafts,archive,text" {
		t.Errorf("порядок групп %v, ждал board,drafts,archive,text", keys)
	}
	if strings.Join(titles, ",") != "Доска,Черновики,Архив,В тексте задач" {
		t.Errorf("заголовки групп %v", titles)
	}
	if ids := rowIDs(group(t, got, "board")); strings.Join(ids, ",") != "XR-010" {
		t.Errorf("группа «Доска» по запросу «поиск»: %v, ждал XR-010", ids)
	}
	board := group(t, got, "board").Rows[0]
	if board.Sect != "backlog" || board.Section == "" || board.Cost != "M" || board.R != 38 {
		t.Errorf("строка доски приехала без секции, цены или ранга: %+v", board)
	}
	drafts := group(t, got, "drafts")
	if len(drafts.Rows) != 1 || drafts.Rows[0].Age == "" {
		t.Errorf("группа «Черновики»: %+v, ждал одну запись с возрастом", drafts)
	}
	arch := group(t, got, "archive")
	if len(arch.Rows) != 1 || arch.Rows[0].ID != "XR-900" || arch.Rows[0].Closed != "2026-08-01" {
		t.Errorf("группа «Архив»: %+v, ждал XR-900 с датой закрытия", arch.Rows)
	}
	// Архивная строка не выдумывает ранг и цену: в архиве этих колонок нет.
	if arch.Rows[0].R != 0 || arch.Rows[0].Cost != "" || arch.Rows[0].RParts != nil {
		t.Errorf("архивная строка принесла ранг или цену: %+v", arch.Rows[0])
	}
	if got.Note != "" {
		t.Errorf("нашедшийся запрос подписан пустотой: %q", got.Note)
	}
}

// Найденное в тексте едет цитатой с номером строки и местом файла, а задача,
// найденная по номеру или заголовку, второй строкой в текстовой группе не
// стоит: она уже показана своей группой.
func TestSearchTextQuotes(t *testing.T) {
	e, c := searchEnv(t)
	got := searched(t, c, e, "колокольчик")
	text := group(t, got, "text")
	if ids := rowIDs(text); strings.Join(ids, ",") != "XR-004,XR-900" {
		t.Fatalf("текстовая группа: %v, ждал живую XR-004 перед архивной XR-900", ids)
	}
	live := text.Rows[0]
	if live.File != "docs/tasks/XR-004.md" || live.Line != 3 {
		t.Errorf("строка текста без пути или номера строки: %+v", live)
	}
	if !strings.Contains(live.Quote, "колокольчик справа") {
		t.Errorf("цитата не показывает найденную строку: %q", live.Quote)
	}
	if live.Title != "Четвёртая" {
		t.Errorf("строка текста без заголовка задачи: %+v", live)
	}
	if live.Where != "" || text.Rows[1].Where != "архив" {
		t.Errorf("место файла названо неверно: %+v", text.Rows)
	}
	// XR-010 нашлась заголовком, и в тексте её файла слово «поиск» тоже есть:
	// второй строкой она не дублируется.
	got = searched(t, c, e, "поиск")
	for _, row := range group(t, got, "text").Rows {
		if row.ID == "XR-010" || row.ID == "XR-900" {
			t.Errorf("найденная по имени задача %s продублирована в тексте: %+v", row.ID, row)
		}
	}
}

// Запрос, похожий на номер, поднимает точное попадание первой строкой, и
// «004» с «xr-004» распознаются одинаково: держать в голове префикс своей
// доски человеку не надо.
func TestSearchByNumber(t *testing.T) {
	e, c := searchEnv(t)
	for _, q := range []string{"004", "04", "xr-004", "XR-004"} {
		got := searched(t, c, e, q)
		ids := rowIDs(group(t, got, "board"))
		if len(ids) == 0 || ids[0] != "XR-004" {
			t.Errorf("поиск %q: группа «Доска» %v, ждал XR-004 первой строкой", q, ids)
		}
	}
}

// Короткий запрос выдачи не поднимает и говорит, чего ждёт: молчаливая
// пустота неотличима от поломки поиска.
func TestSearchShortQuery(t *testing.T) {
	e, c := searchEnv(t)
	for _, q := range []string{"", " ", "п"} {
		got := searched(t, c, e, q)
		if got.Note != searchShortNote {
			t.Errorf("поиск %q подписан %q, ждал %q", q, got.Note, searchShortNote)
		}
		for _, g := range got.Groups {
			if len(g.Rows) != 0 {
				t.Errorf("поиск %q поднял выдачу в группе %s: %v", q, g.Key, rowIDs(g))
			}
		}
	}
}

// Ненашедшийся запрос говорит, где искали: иначе непонятно, то ли задачи нет,
// то ли архив в поиск не входит.
func TestSearchNothingFound(t *testing.T) {
	e, c := searchEnv(t)
	got := searched(t, c, e, "тарабарщина")
	if got.Note != searchNoneNote {
		t.Errorf("пустая выдача подписана %q, ждал %q", got.Note, searchNoneNote)
	}
	for _, g := range got.Groups {
		if len(g.Rows) != 0 {
			t.Errorf("по «тарабарщина» нашлось в группе %s: %v", g.Key, rowIDs(g))
		}
	}
}

// Запрос ищется подстрокой, а не выражением: спецсимволы регулярок и путей
// приходят с телефонной клавиатуры походя, и отвечать на них отказом или
// выдачей чужого каталога ручка не должна.
func TestSearchSpecialChars(t *testing.T) {
	e, c := searchEnv(t)
	for _, q := range []string{"[поиск]", "*", ".*", "(", "../../etc/passwd", "XR-010'"} {
		got := searched(t, c, e, q)
		for _, g := range got.Groups {
			if len(g.Rows) != 0 {
				t.Errorf("поиск %q нашёл в группе %s: %v", q, g.Key, rowIDs(g))
			}
		}
	}
	// Плюс в запросе это плюс, а не повтор предыдущего символа: слагаемые ранга
	// в тексте задачи ищутся ровно так.
	got := searched(t, c, e, "25+7+3")
	if ids := rowIDs(group(t, got, "text")); strings.Join(ids, ",") != "XR-002" {
		t.Errorf("поиск «25+7+3» в тексте: %v, ждал XR-002", ids)
	}
}

// Потолки выдачи: широкий запрос режется по группам, а хвост считается, и
// урезанная выдача не выглядит полной.
func TestSearchLimits(t *testing.T) {
	e, c := searchEnv(t)
	for i := 1; i <= searchRowsMax+3; i++ {
		id := fmt.Sprintf("XR-1%02d", i)
		runTaskctl(t, e.proj, "add", "--id", id, "--title", "Широкая строка доски",
			"--rank", "25+1+0+0+0", "--accept", "agent")
	}
	for i := 1; i <= searchTextMax+2; i++ {
		writeAt(t, filepath.Join(e.proj, "docs", "tasks", fmt.Sprintf("XR-2%02d.md", i)),
			"# Файл задачи\n\nШирокая строка текста задачи.\n")
	}
	got := searched(t, c, e, "широкая")
	board := group(t, got, "board")
	if len(board.Rows) != searchRowsMax || board.More != 3 {
		t.Errorf("группа «Доска»: %d строк, хвост %d; ждал %d и 3",
			len(board.Rows), board.More, searchRowsMax)
	}
	text := group(t, got, "text")
	if len(text.Rows) != searchTextMax || text.More != 2 {
		t.Errorf("группа «В тексте задач»: %d строк, хвост %d; ждал %d и 2",
			len(text.Rows), text.More, searchTextMax)
	}
}

// Пропавший taskctl остаётся бедой одной группы: доска приходит из кэша
// сервера, архив разбирается файлом, и обе группы отвечают как ни в чём не
// бывало, а вот накопитель без утилиты не прочитать, и группа черновиков несёт
// причину словами. Уронив на этом весь поиск, ручка отняла бы у человека и то,
// что у неё на руках уже есть (замечание ревью DK-325).
func TestSearchDraftsFailureStaysInGroup(t *testing.T) {
	e, c := searchEnv(t)
	// Кэш доски греется первым запросом: он держит ответ утилиты, пока файл
	// доски не тронут, и второму запросу taskctl для доски уже не нужен.
	if len(group(t, searched(t, c, e, "поиск"), "board").Rows) != 1 {
		t.Fatal("стенд не нашёл строку доски до пропажи taskctl")
	}
	t.Setenv("PATH", t.TempDir())
	if taskctlPath() != "" {
		t.Fatalf("taskctl всё ещё находится: %s", taskctlPath())
	}
	got := searched(t, c, e, "поиск")
	if ids := rowIDs(group(t, got, "board")); strings.Join(ids, ",") != "XR-010" {
		t.Errorf("группа «Доска» без taskctl: %v, ждал XR-010 из кэша", ids)
	}
	if ids := rowIDs(group(t, got, "archive")); strings.Join(ids, ",") != "XR-900" {
		t.Errorf("группа «Архив» без taskctl: %v, ждал XR-900 из файла архива", ids)
	}
	drafts := group(t, got, "drafts")
	if len(drafts.Rows) != 0 {
		t.Errorf("накопитель без утилиты приехал строками: %+v", drafts.Rows)
	}
	if !strings.Contains(drafts.Note, "накопитель черновиков не прочитался") ||
		!strings.Contains(drafts.Note, "taskctl не нашёлся") {
		t.Errorf("группа черновиков молчит о причине: %q", drafts.Note)
	}
	if got.Note != "" {
		t.Errorf("нашедшийся запрос подписан пустотой: %q", got.Note)
	}
}

// Без входа поиск не отдаёт ни строки: ручка стоит за той же дверью, что
// доска.
func TestSearchNeedsAuth(t *testing.T) {
	e := newTestEnv(t)
	resp, err := plainClient().Get(e.srv.URL + "/api/projects/demo/search?q=поиск")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("поиск без входа: %d", resp.StatusCode)
	}
}

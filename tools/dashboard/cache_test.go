package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Экран стоит своих подпроцессов, и цена его в том, сколько их зовётся на
// каждый заход. Здесь проверяется память процесса на эти ответы: она отдаёт
// доску, пока файл доски тот же и не вышел потолок срока, обход корней держит
// свой срок, а причина отказа не запоминается вовсе. Рядом стоят замеры того,
// что кандидаты и проекты спрашиваются разом, а не по очереди.

// countingScript это тело фикстуры, которая считает свои запуски строкой в
// журнале: по нему видно, сколько раз ручка сходила в подпроцесс.
func countingScript(log, body string) string {
	return fmt.Sprintf("echo вызов >> %s\n%s", log, body)
}

func calls(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

// getStatus ходит на ручку и отдаёт код ответа с телом.
func getStatus(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body(t, resp)
}

// advance двигает часы сервера: срок памяти проверяется временем, а не
// ожиданием в тесте.
func (e *testEnv) advance(d time.Duration) {
	base := time.Now().Add(d)
	e.s.now = func() time.Time { return base }
}

// Повторный заход на тот же экран не гоняет taskctl заново: доска лежит в
// памяти процесса, пока её файл не тронут.
func TestBoardReadOnceForRepeatedScreens(t *testing.T) {
	e := newTestEnv(t)
	log := filepath.Join(e.home, "taskctl.log")
	writeScript(t, e.bin, "taskctl", countingScript(log, fmt.Sprintf("echo '%s'", boardFixtureJSON)))
	c := e.loggedClient(t)

	for i := 0; i < 3; i++ {
		if code, text := getStatus(t, c, e.srv.URL+"/api/projects/demo/board"); code != http.StatusOK {
			t.Fatalf("доска: %d %s", code, text)
		}
	}
	if n := calls(t, log); n != 1 {
		t.Fatalf("три захода на доску стоили %d запусков taskctl, жду один: доска не легла в память", n)
	}
}

// Правка файла доски чужой рукой доезжает до экрана сразу: память узнаёт её
// по отпечатку файла, а не ждёт срока.
func TestBoardRereadAfterBoardChange(t *testing.T) {
	e := newTestEnv(t)
	log := filepath.Join(e.home, "taskctl.log")
	writeScript(t, e.bin, "taskctl", countingScript(log, fmt.Sprintf("echo '%s'", boardFixtureJSON)))
	c := e.loggedClient(t)
	getStatus(t, c, e.srv.URL+"/api/projects/demo/board")

	board := filepath.Join(e.proj, "docs", "TASKS.md")
	data, err := os.ReadFile(board)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(board, append(data, []byte("\n## Ещё секция\n\nНет.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	getStatus(t, c, e.srv.URL+"/api/projects/demo/board")
	if n := calls(t, log); n != 2 {
		t.Fatalf("правка доски стоила %d запусков taskctl, жду два: правка мимо дашборда не доехала", n)
	}
}

// Потолок срока держит то, что taskctl считает не по файлу доски: возраст
// строки и метки слитого кода меняются от коммита, а файл при этом остаётся
// нетронутым.
func TestBoardCacheCeiling(t *testing.T) {
	e := newTestEnv(t)
	log := filepath.Join(e.home, "taskctl.log")
	writeScript(t, e.bin, "taskctl", countingScript(log, fmt.Sprintf("echo '%s'", boardFixtureJSON)))
	c := e.loggedClient(t)
	getStatus(t, c, e.srv.URL+"/api/projects/demo/board")

	// Потолок памяти на доску 10 секунд (cache.go, boardTTL).
	e.advance(11 * time.Second)
	getStatus(t, c, e.srv.URL+"/api/projects/demo/board")
	if n := calls(t, log); n != 2 {
		t.Fatalf("заход после потолка срока стоил %d запусков taskctl, жду два: память держит доску дольше срока", n)
	}
}

// Причина отказа не запоминается: поднятый taskctl доезжает до экрана
// следующим запросом, а не по выходе срока. Молчание тут различимо и без
// памяти, и с ней.
func TestBoardErrorNotRemembered(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", "echo 'доска не читается' >&2; exit 1")
	c := e.loggedClient(t)
	if code, _ := getStatus(t, c, e.srv.URL+"/api/projects/demo/board"); code != http.StatusBadGateway {
		t.Fatalf("сломанный taskctl: %d, ожидал 502", code)
	}
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", boardFixtureJSON))
	code, text := getStatus(t, c, e.srv.URL+"/api/projects/demo/board")
	if code != http.StatusOK || !strings.Contains(text, "XR-005") {
		t.Fatalf("починенный taskctl: %d %s, ожидал доску сразу", code, text)
	}
}

// Обход корней не повторяется на каждый запрос: цепочка экрана это три
// запроса подряд, и каждый гонял git по всем кандидатам заново.
func TestScanReadOnceForRepeatedRequests(t *testing.T) {
	e := newTestEnv(t)
	log := filepath.Join(e.home, "git.log")
	writeScript(t, e.bin, "git", countingScript(log, "printf '.git\\n.git\\n'"))
	c := e.loggedClient(t)

	getStatus(t, c, e.srv.URL+"/api/projects")
	first := calls(t, log)
	if first == 0 {
		t.Fatal("обход корней не позвал git ни разу: отсев боковых деревьев не работает")
	}
	getStatus(t, c, e.srv.URL+"/api/projects/demo/board")
	getStatus(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005")
	if n := calls(t, log); n != first {
		t.Fatalf("цепочка экрана стоила %d запусков git против %d за первый запрос: обход корней не лёг в память", n, first)
	}
}

// Срок обхода короток: заведённый рядом проект показывается через секунды, а
// не через перезапуск демона.
func TestScanCacheExpires(t *testing.T) {
	e := newTestEnv(t)
	log := filepath.Join(e.home, "git.log")
	writeScript(t, e.bin, "git", countingScript(log, "printf '.git\\n.git\\n'"))
	c := e.loggedClient(t)
	getStatus(t, c, e.srv.URL+"/api/projects")
	first := calls(t, log)

	mkProject(t, filepath.Join(e.home, "projects", "beta"))
	// Срок памяти на обход 5 секунд (cache.go, scanTTL).
	e.advance(6 * time.Second)
	_, text := getStatus(t, c, e.srv.URL+"/api/projects")
	if calls(t, log) <= first {
		t.Fatal("обход после срока не пошёл в git: память держит корни дольше срока")
	}
	if !strings.Contains(text, `"beta"`) {
		t.Fatalf("заведённый проект не показался после срока: %s", text)
	}
}

// barrierScript это тело фикстуры, которая обязана быть позванной разом n раз:
// каждый запуск отмечается своим файлом в общем каталоге и ждёт остальных.
// Дождавшись, фикстура отвечает одно, не дождавшись другое, и «спрашиваются
// разом» проверяется исходом ответа, а не секундомером, который на занятой
// машине врёт.
func barrierScript(dir string, n int, met, missed string) string {
	return fmt.Sprintf(`mkdir -p '%[1]s'
touch '%[1]s'/$$
i=0
while [ "$(ls '%[1]s' | wc -l)" -lt %[2]d ] && [ $i -lt 60 ]; do sleep 0.05; i=$((i+1)); done
if [ "$(ls '%[1]s' | wc -l)" -ge %[2]d ]; then
%[3]s
else
%[4]s
fi`, dir, n, met, missed)
}

// Кандидаты обхода спрашиваются разом: git на каждого стоит своего процесса, и
// по очереди обход складывал бы их сроки. Дождавшийся соседей git отвечает
// признаком бокового дерева, и разом спрошенные кандидаты все отсеиваются.
func TestScanCandidatesInParallel(t *testing.T) {
	root := t.TempDir()
	const cands = 6
	for i := 0; i < cands; i++ {
		mkProject(t, filepath.Join(root, fmt.Sprintf("proj%d", i)))
	}
	bin := t.TempDir()
	writeScript(t, bin, "git", barrierScript(filepath.Join(t.TempDir(), "разом"), cands,
		"printf '/repo/.git/worktrees/side\\n/repo/.git\\n'", "printf '/repo/.git\\n/repo/.git\\n'"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	projects, errs := scanProjects([]string{root})
	if len(errs) != 0 {
		t.Fatalf("ошибки обхода: %v", errs)
	}
	if len(projects) != 0 {
		t.Fatalf("кандидаты не спрошены разом: %d из %d ответили в одиночку", cands-len(projects), cands)
	}
}

// Проекты стартовой опрашиваются разом: доска каждого это свой taskctl, и по
// очереди экран ждал бы их сумму. Не дождавшийся соседей taskctl отвечает
// отказом, и он приезжает в ответ причиной вместо счётчиков секций.
func TestProjectsQueriedInParallel(t *testing.T) {
	e := newTestEnv(t)
	names := []string{"beta", "gamma"}
	for _, name := range names {
		mkProject(t, filepath.Join(e.home, "projects", name))
	}
	writeScript(t, e.bin, "git", "printf '.git\\n.git\\n'")
	writeScript(t, e.bin, "taskctl", barrierScript(filepath.Join(t.TempDir(), "разом"), len(names)+1,
		fmt.Sprintf("echo '%s'", boardFixtureJSON), "echo 'сосед не подошёл' >&2; exit 1"))
	c := e.loggedClient(t)

	code, text := getStatus(t, c, e.srv.URL+"/api/projects")
	if code != http.StatusOK {
		t.Fatalf("список проектов: %d %s", code, text)
	}
	if strings.Contains(text, "сосед не подошёл") {
		t.Fatalf("проекты опрошены по очереди: %s", text)
	}
	for _, name := range append(names, "demo") {
		if !strings.Contains(text, `"`+name+`"`) {
			t.Fatalf("в списке нет проекта %s: %s", name, text)
		}
	}
}

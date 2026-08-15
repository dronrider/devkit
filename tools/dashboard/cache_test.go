package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	// Часы заморожены по той же причине, что и в тесте обхода корней ниже:
	// проверяется память, а не скорость цепочки запросов.
	now := time.Now()
	e.s.now = func() time.Time { return now }
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
	// Часы заморожены: тест проверяет саму память, а не скорость цепочки, а
	// под параллельным прогоном репозитория запросы растягиваются за срок
	// памяти, и обход честно пошёл бы в git заново.
	now := time.Now()
	e.s.now = func() time.Time { return now }
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

// Паника подзадачи стоит одной подзадачи, а не демона. Пока опрос шёл в
// горутине http-обработчика, её гасил recover самого net/http; в своей
// горутине она уносит процесс целиком, и тестовый прогон валится дампом. Здесь
// проверяется, что паникует одно дело, соседи доходят, а паника приезжает
// ошибкой со словами и стеком.
func TestInParallelSurvivesPanic(t *testing.T) {
	const n = 5
	done := make([]bool, n)
	errs := inParallel(scanWorkers, n, func(i int) {
		if i == 2 {
			panic("доска рассыпалась")
		}
		done[i] = true
	})
	for i := range done {
		if (i != 2) != done[i] {
			t.Fatalf("дело %d: сделано %v, а паниковало дело 2", i, done[i])
		}
	}
	for i, err := range errs {
		if (i == 2) != (err != nil) {
			t.Fatalf("дело %d вернуло ошибку %v: ошибка ждётся ровно у паниковавшего", i, err)
		}
	}
	var rec *recovered
	if !errors.As(errs[2], &rec) {
		t.Fatalf("паника приехала как %T, жду ошибку с пойманной паникой", errs[2])
	}
	if !strings.Contains(rec.Error(), "доска рассыпалась") {
		t.Fatalf("паника без слов: %q", rec.Error())
	}
	if !strings.Contains(rec.Stack(), "TestInParallelSurvivesPanic") {
		t.Fatalf("стек паники не собрался: %q", rec.Stack())
	}
}

// Сломанный проект называется словами в ответе, а стек уезжает в журнал: на
// экране от потрохов пользы нет, а разбирать поломку по журналу.
func TestPanicNamedInAnswerStackInLog(t *testing.T) {
	var log strings.Builder
	s := newServer(&Config{Home: t.TempDir()}, os.DirFS("static"),
		func(format string, args ...any) { fmt.Fprintf(&log, format+"\n", args...) })
	errs := inParallel(1, 1, func(int) { panic("доска рассыпалась") })

	note := s.notePanic("опрос проекта demo", errs[0])
	if !strings.Contains(note, "доска рассыпалась") {
		t.Fatalf("в ответ уехало %q: причина не названа словами", note)
	}
	if strings.Contains(note, "goroutine") {
		t.Fatalf("в ответ уехал стек: %q", note)
	}
	if !strings.Contains(log.String(), "опрос проекта demo") || !strings.Contains(log.String(), "goroutine") {
		t.Fatalf("в журнале %q: жду строку с проектом и стеком", log.String())
	}
}

// Потолок разом живущих подпроцессов держится: корень с сотней досок иначе
// поднял бы сотню git одним запросом. Считает их сама подзадача.
func TestInParallelKeepsWorkerCap(t *testing.T) {
	var mu sync.Mutex
	live, most := 0, 0
	inParallel(scanWorkers, 4*scanWorkers, func(int) {
		mu.Lock()
		live++
		if live > most {
			most = live
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		live--
		mu.Unlock()
	})
	if most > scanWorkers {
		t.Fatalf("разом жили %d дел при потолке %d: семафор не держит", most, scanWorkers)
	}
	if most < 2 {
		t.Fatalf("разом жило %d дело: дела идут по очереди", most)
	}
}

// Тот же потолок на живых подпроцессах обхода: фикстура git отмечается на
// время своей работы, и обход не должен поднимать её сверх потолка разом.
func TestScanKeepsWorkerCap(t *testing.T) {
	root := t.TempDir()
	cands := 3 * scanWorkers
	for i := 0; i < cands; i++ {
		mkProject(t, filepath.Join(root, fmt.Sprintf("proj%d", i)))
	}
	bin, live := t.TempDir(), filepath.Join(t.TempDir(), "живые")
	log := filepath.Join(t.TempDir(), "разом.log")
	writeScript(t, bin, "git", fmt.Sprintf(`mkdir -p '%[1]s'
touch '%[1]s'/$$
ls '%[1]s' | wc -l >> '%[2]s'
sleep 0.2
rm -f '%[1]s'/$$
printf '.git\n.git\n'`, live, log))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if projects, _ := scanProjects([]string{root}); len(projects) != cands {
		t.Fatalf("проектов %d, жду %d", len(projects), cands)
	}
	most := 0
	for _, f := range strings.Fields(readFileString(t, log)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		if n > most {
			most = n
		}
	}
	if most > scanWorkers {
		t.Fatalf("разом жили %d процессов git при потолке %d: обход не держит потолок", most, scanWorkers)
	}
	if most < 2 {
		t.Fatalf("разом жил %d процесс git: кандидаты спрашиваются по очереди", most)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

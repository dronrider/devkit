package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Замер экранов дашборда (DK-242). Стартовая и карточка задачи стоят ровно
// столько, сколько стоят подпроцессы под ними, поэтому окружение замера
// повторяет живую машину числами, а не составом: корень с четырьмя проектами и
// девятью боковыми деревьями задач, git и taskctl подменены фикстурами,
// которые спят замеренную цену вызова. Числа печатает команда
//
//	GOWORK=off go test -run '^$' -bench Screen -benchtime 5x .
//
// Холодный замер это первый заход на пустой памяти процесса, тёплый это
// повторный заход того же экрана.

// Цена подпроцессов, снятая на живой машине 2026-08-11: rev-parse в каталоге
// проекта 6-12 мс, taskctl list --json на проект 50-270 мс, dep list 60 мс,
// tmux ls 8 мс.
const (
	benchGitCost       = "0.010"
	benchBoardCost     = "0.100"
	benchDepCost       = "0.060"
	benchTmuxCost      = "0.008"
	benchProjectCount  = 4
	benchWorktreeCount = 9
	benchTaskID        = "XR-005"
	benchProject       = "proj1"
)

// benchGitBody изображает git обхода корней: боковое дерево задачи узнаётся по
// имени каталога, как на живой машине оно узнаётся расхождением git-dir и
// git-common-dir.
const benchGitBody = `sleep ` + benchGitCost + `
dir=$2
for a in "$@"; do
  case "$a" in
  --git-dir)
    case "$dir" in
    *-wt*) echo /repo/.git/worktrees/side ;;
    *) echo /repo/.git ;;
    esac
    ;;
  --git-common-dir) echo /repo/.git ;;
  esac
done`

// benchTaskctlBody изображает taskctl: доска и зависимости стоят по-разному, и
// замер это различает.
const benchTaskctlBody = `case "$1" in
dep) sleep ` + benchDepCost + `; echo '{"after":[],"blocks":[]}' ;;
*) sleep ` + benchBoardCost + `; echo '` + boardFixtureJSON + `' ;;
esac`

// benchEnv раскладывает окружение замера: корень с проектами и боковыми
// деревьями, фикстуры подпроцессов в PATH.
func benchEnv(b *testing.B) *Config {
	b.Helper()
	loginPause = 0
	home := b.TempDir()
	root := filepath.Join(home, "projects")
	for i := 1; i <= benchProjectCount; i++ {
		mkProject(b, filepath.Join(root, fmt.Sprintf("proj%d", i)))
	}
	for i := 1; i <= benchWorktreeCount; i++ {
		mkProject(b, filepath.Join(root, fmt.Sprintf("proj1-wt%d", i)))
	}
	bin := b.TempDir()
	writeScript(b, bin, "git", benchGitBody)
	writeScript(b, bin, "taskctl", benchTaskctlBody)
	writeScript(b, bin, "tmux", "sleep "+benchTmuxCost)
	b.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &Config{Home: home, Roots: []string{root}, Port: defaultPort, Token: "test-token"}
}

// benchServer поднимает сервер с пустой памятью: холодный замер меряет первый
// заход, и кэш прошлого захода в него попадать не должен.
func benchServer(b *testing.B, cfg *Config) (*httptest.Server, *http.Client) {
	b.Helper()
	s := newServer(cfg, os.DirFS("static"), nil)
	srv := httptest.NewServer(s.handler())
	b.Cleanup(srv.Close)
	e := &testEnv{srv: srv, s: s, cfg: cfg}
	return srv, e.loggedClient(b)
}

// getOK ходит на ручку и роняет замер, если ответ не 200: замер молча мимо
// данных не считается.
func getOK(b *testing.B, c *http.Client, url string) {
	b.Helper()
	resp, err := c.Get(url)
	if err != nil {
		b.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b.Fatalf("%s: %d", url, resp.StatusCode)
	}
}

// startScreen это стартовая: клиент рисует её ответом /api/projects.
func startScreen(b *testing.B, c *http.Client, base string) {
	getOK(b, c, base+"/api/projects")
}

// taskScreen это карточка задачи так, как её открывает браузер: список
// проектов, доска проекта, сама строка.
func taskScreen(b *testing.B, c *http.Client, base string) {
	getOK(b, c, base+"/api/projects")
	getOK(b, c, base+"/api/projects/"+benchProject+"/board")
	getOK(b, c, base+"/api/projects/"+benchProject+"/tasks/"+benchTaskID)
}

// reportMS печатает то же время миллисекундами: порог задачи назван в них, и
// пересчитывать наносекунды глазами при каждом замере незачем.
func reportMS(b *testing.B) {
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "мс/экран")
}

func benchCold(b *testing.B, screen func(*testing.B, *http.Client, string)) {
	cfg := benchEnv(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		srv, c := benchServer(b, cfg)
		b.StartTimer()
		screen(b, c, srv.URL)
	}
	reportMS(b)
}

func benchWarm(b *testing.B, screen func(*testing.B, *http.Client, string)) {
	cfg := benchEnv(b)
	srv, c := benchServer(b, cfg)
	screen(b, c, srv.URL)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		screen(b, c, srv.URL)
	}
	reportMS(b)
}

func BenchmarkStartScreenCold(b *testing.B) { benchCold(b, startScreen) }
func BenchmarkStartScreenWarm(b *testing.B) { benchWarm(b, startScreen) }
func BenchmarkTaskScreenCold(b *testing.B)  { benchCold(b, taskScreen) }
func BenchmarkTaskScreenWarm(b *testing.B)  { benchWarm(b, taskScreen) }

// BenchmarkQuota меряет чтение снимков подписок: каталог с двумя файлами по
// сотне байт, как на живой машине. По этому числу принято решение не заводить
// снимкам памяти процесса (DK-253): рядом с подпроцессами экрана оно теряется.
//
//	GOWORK=off go test -run '^$' -bench Quota -benchtime 200x .
func BenchmarkQuota(b *testing.B) {
	home := b.TempDir()
	dir := filepath.Join(home, ".devkit", "quota")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	for name, body := range map[string]string{"harness-one": quotaFixtureA, "harness-two": quotaFixtureB} {
		if err := os.WriteFile(filepath.Join(dir, name+".local"), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		readQuota(home, now)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e3, "мкс/чтение")
}

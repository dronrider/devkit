package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Стенд догона это два клона с общим origin (DoD DK-1414): demo это проект
// дашборда на «второй машине», первый клон играет чужую машину, которая пушит
// доску. Ручного git во втором клоне тест не делает: доску двигает только круг
// догона.
func runGitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v в %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

type catchupEnv struct {
	*testEnv
	origin string
	first  string
	log    strings.Builder
}

func mkCatchupEnv(t *testing.T) *catchupEnv {
	t.Helper()
	e := newTestEnv(t)
	origin := filepath.Join(e.home, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	runGitAt(t, e.proj, "init", "-q", "-b", "main")
	runGitAt(t, e.proj, "add", ".")
	runGitAt(t, e.proj, "commit", "-q", "-m", "доска")
	runGitAt(t, e.proj, "remote", "add", "origin", origin)
	runGitAt(t, e.proj, "push", "-q", "-u", "origin", "main")
	first := filepath.Join(e.home, "первый-клон")
	if out, err := exec.Command("git", "clone", "-q", origin, first).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	ce := &catchupEnv{testEnv: e, origin: origin, first: first}
	e.s.logf = func(format string, args ...any) { fmt.Fprintf(&ce.log, format+"\n", args...) }
	return ce
}

// pushFromFirst правит доску в первом клоне и пушит её в origin: так чужая
// машина оставляет правку, которую вторая машина обязана увидеть сама.
func (c *catchupEnv) pushFromFirst(t *testing.T, line string) {
	t.Helper()
	board := filepath.Join(c.first, "docs", "TASKS.md")
	if err := os.WriteFile(board, []byte(readFileString(t, board)+line), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, c.first, "commit", "-q", "-am", "правка доски")
	runGitAt(t, c.first, "push", "-q")
}

func (c *catchupEnv) note() string {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return c.s.catchup[c.proj]
}

// Пуш доски из первого клона доезжает вторым кругом догона без ручного git:
// main совпадает с origin/main, пометка пустая, событие названо в журнале
// (первая строка DoD).
func TestBoardCatchupFastForward(t *testing.T) {
	c := mkCatchupEnv(t)
	c.pushFromFirst(t, "\n- XR-900 строка другой машины\n")
	c.s.catchupRound()
	if head, ref := runGitAt(t, c.proj, "rev-parse", "HEAD"),
		runGitAt(t, c.proj, "rev-parse", "origin/main"); head != ref {
		t.Fatalf("HEAD %s не совпал с origin/main %s: fast-forward не прошёл", head, ref)
	}
	if note := c.note(); note != "" {
		t.Fatalf("пометка свежей доски не пустая: %q", note)
	}
	if !strings.Contains(c.log.String(), "догнано") {
		t.Fatalf("догон не назван в журнале: %q", c.log.String())
	}
	if !strings.Contains(readFileString(t, filepath.Join(c.proj, "docs", "TASKS.md")), "XR-900") {
		t.Fatal("чужая строка доски не приехала в файл")
	}
}

// Незакоммиченная правка держит догон: круг не меняет файлы дерева, витрина
// держит пометку «позади», после чистки дерева следующий круг дотягивает
// доску (вторая строка DoD).
func TestBoardCatchupDirtyTreeKeepsFiles(t *testing.T) {
	c := mkCatchupEnv(t)
	before := runGitAt(t, c.proj, "rev-parse", "HEAD")
	board := filepath.Join(c.proj, "docs", "TASKS.md")
	dirty := readFileString(t, board) + "\nместная незакоммиченная правка\n"
	if err := os.WriteFile(board, []byte(dirty), 0o644); err != nil {
		t.Fatal(err)
	}
	c.pushFromFirst(t, "\n- XR-901 ещё строка\n")
	c.s.catchupRound()
	if note := c.note(); !strings.Contains(note, "дерево не чистое") || !strings.Contains(note, "позади") {
		t.Fatalf("пометка грязного дерева: %q", note)
	}
	if runGitAt(t, c.proj, "rev-parse", "HEAD") != before {
		t.Fatal("круг догона двинул HEAD при грязном дереве")
	}
	if got := readFileString(t, board); got != dirty {
		t.Fatal("круг догона тронул файлы дерева")
	}
	// Пометка стоит в витрине: её читают и список проектов, и сама доска.
	c.s.boards = map[string]boardEntry{}
	client := c.loggedClient(t)
	for _, path := range []string{"/api/projects", "/api/projects/demo/board"} {
		resp, err := client.Get(c.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		page := body(t, resp)
		if !strings.Contains(page, "дерево не чистое") {
			t.Fatalf("пометка не приехала в %s: %s", path, page)
		}
	}
	runGitAt(t, c.proj, "checkout", "-q", "--", "docs/TASKS.md")
	c.s.catchupRound()
	if runGitAt(t, c.proj, "rev-parse", "HEAD") != runGitAt(t, c.proj, "rev-parse", "origin/main") {
		t.Fatal("после чистки дерева догон не дотянул доску")
	}
	if note := c.note(); note != "" {
		t.Fatalf("пометка не снялась после догона: %q", note)
	}
}

// Локальный коммит на main расходится с origin: догон отказывается, коммит
// остаётся на месте, пометка стоит (третья строка DoD).
func TestBoardCatchupDivergedRefuses(t *testing.T) {
	c := mkCatchupEnv(t)
	c.pushFromFirst(t, "\n- XR-902 чужая строка\n")
	board := filepath.Join(c.proj, "docs", "TASKS.md")
	if err := os.WriteFile(board, []byte(readFileString(t, board)+"\nместный коммит\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitAt(t, c.proj, "commit", "-q", "-am", "местный коммит")
	local := runGitAt(t, c.proj, "rev-parse", "HEAD")
	c.s.catchupRound()
	if note := c.note(); !strings.Contains(note, "разошёлся") || !strings.Contains(note, "догон отказан") {
		t.Fatalf("пометка расхождения: %q", note)
	}
	if runGitAt(t, c.proj, "rev-parse", "HEAD") != local {
		t.Fatal("догон двинул HEAD при расхождении: местный коммит пропал")
	}
	if !strings.Contains(c.log.String(), "разошёлся") {
		t.Fatalf("расхождение не названо в журнале: %q", c.log.String())
	}
}

// Недоступный origin не роняет круг: причина встаёт в пометку и журнал, доска
// отдаётся прежняя, сервер живёт (четвёртая строка DoD).
func TestBoardCatchupOriginGoneKeepsServing(t *testing.T) {
	c := mkCatchupEnv(t)
	if err := os.RemoveAll(c.origin); err != nil {
		t.Fatal(err)
	}
	c.s.catchupRound()
	if note := c.note(); !strings.Contains(note, "fetch не прошёл") {
		t.Fatalf("пометка недоступного origin: %q", note)
	}
	if !strings.Contains(c.log.String(), "fetch не прошёл") {
		t.Fatalf("недоступный origin не назван в журнале: %q", c.log.String())
	}
	client := c.loggedClient(t)
	resp, err := client.Get(c.srv.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	page := body(t, resp)
	if !strings.Contains(page, "demo") {
		t.Fatalf("витрина упала после отказа fetch: %s", page)
	}
}

// Круг догона ходит тиком из конфига: интервал ставится коротким, и пуш из
// первого клона доезжает следующим тиком без ручного повода (пятая строка
// DoD).
func TestBoardCatchupKeeperTick(t *testing.T) {
	c := mkCatchupEnv(t)
	c.s.cfg.CatchupInterval = 60 * time.Millisecond
	stop := make(chan struct{})
	defer close(stop)
	go c.s.boardCatchupKeeper(stop)
	c.pushFromFirst(t, "\n- XR-903 строка тика\n")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runGitAt(t, c.proj, "rev-parse", "HEAD") == runGitAt(t, c.proj, "rev-parse", "origin/main") {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("тик догона не дотянул доску за отведённый срок")
}

// Идущая операция git держит догон тем же отказом, что и грязное дерево:
// чистый rebase выглядит «позади origin» не хуже отставшего, а двигать файлы
// на середине операции нельзя.
func TestBoardCatchupBusyRefuses(t *testing.T) {
	c := mkCatchupEnv(t)
	c.pushFromFirst(t, "\n- XR-904 строка на середине rebase\n")
	if err := os.MkdirAll(filepath.Join(c.proj, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.s.catchupRound()
	if note := c.note(); !strings.Contains(note, "операция git") {
		t.Fatalf("пометка занятого дерева: %q", note)
	}
	if err := os.RemoveAll(filepath.Join(c.proj, ".git", "rebase-merge")); err != nil {
		t.Fatal(err)
	}
	c.s.catchupRound()
	if note := c.note(); note != "" {
		t.Fatalf("после конца операции догон не прошёл: %q", note)
	}
}

// Дерево на ветке задачи догоном не занимается: доска бокового состояния не
// его дело, и пометка там была бы шумом.
func TestBoardCatchupSkipsTaskBranch(t *testing.T) {
	c := mkCatchupEnv(t)
	runGitAt(t, c.proj, "checkout", "-q", "-b", "dk-1414")
	c.pushFromFirst(t, "\n- XR-905 строка мимо ветки\n")
	c.s.catchupRound()
	if note := c.note(); note != "" {
		t.Fatalf("ветка задачи получила пометку догона: %q", note)
	}
}

// Окружение сетевого вызова несёт ssh из конфига репозитория: GIT_SSH_COMMAND
// глушит core.sshCommand, и репозиторий с отдельным ключом и known_hosts
// иначе подменялся голым ssh с отказом «Host key verification failed».
// Конфиг репозитория важнее случайной переменной окружения демона.
func TestRemoteGitEnvPrefersRepoSSH(t *testing.T) {
	c := mkCatchupEnv(t)
	repo := "ssh -i /ключи/отдельный -o UserKnownHostsFile=/ключи/hosts"
	runGitAt(t, c.proj, "config", "core.sshCommand", repo)
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /случайный-из-окружения")
	env := remoteGitEnv(c.proj)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_SSH_COMMAND="+repo+" -o BatchMode=yes") {
		t.Fatalf("ssh репозитория не доехал в окружение: %s", joined)
	}
	if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("гигиена учётки gitrun не доехала: %s", joined)
	}
	runGitAt(t, c.proj, "config", "--unset", "core.sshCommand")
	joined = strings.Join(remoteGitEnv(c.proj), "\n")
	if !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -i /случайный-из-окружения -o BatchMode=yes") {
		t.Fatalf("без конфига репозитория живёт окружение демона: %s", joined)
	}
}

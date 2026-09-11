package taskhead

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Стенд лестницы. Настоящих tmux, клиента и оболочки тут нет: tmux играет
// шелл-стаб, клиент это пустой бинарь в PATH, оболочку task-run.py играет
// python-стаб, который принимает замок так же, как настоящая. PATH стенда
// собран из одного каталога стабов: системный tmux в него не попадает, и
// ступень «без tmux» разыгрывается отсутствием стаба.

// Стаб tmux ведёт состояние в каталоге $TMUX_STUB: sessions/<имя> это живая
// tmux-сессия, panes/<адрес> это ответ display-message («0 claude»), keys.log
// копит нажатия, calls.log все вызовы. new-session запускает команду окна
// фоном и печатает адрес панели, как с -P -F.
const tmuxStub = `#!/bin/sh
d="$TMUX_STUB"
echo "$*" >> "$d/calls.log"
case "$1" in
has-session)
  n="${3#=}"; [ -e "$d/sessions/$n" ] && exit 0; exit 1 ;;
display-message)
  f="${4#=}"; f="${f%:}"; f="${f#%}"
  [ -e "$d/panes/$f" ] || exit 1
  /bin/cat "$d/panes/$f"; exit 0 ;;
send-keys)
  shift; echo "$*" >> "$d/keys.log"; exit 0 ;;
new-session)
  if [ -e "$d/newfail" ]; then echo "сервер не встал" >&2; exit 1; fi
  : > "$d/sessions/$4"
  for last; do :; done
  /bin/sh -c "$last" > "$d/window.out" 2>&1 &
  echo "%9"; exit 0 ;;
kill-session)
  n="${3#=}"; /bin/rm -f "$d/sessions/$n"; exit 0 ;;
esac
exit 0
`

// Стаб оболочки: пишет свой вызов и окружение, принимает замок, когда в нём
// pid команды из DEVKIT_TASK_LOCK_FROM, и держит его RUNNER_HOLD секунд.
// RUNNER_MUTE играет оболочку, упавшую до замка.
const runnerStub = `import os, sys, time
home = os.environ["HOME"]
lock = os.path.join(home, ".devkit", "task-%s.lock" % sys.argv[1])
with open(os.path.join(home, "runner.log"), "a", encoding="utf-8") as f:
    f.write("%d %s\n" % (os.getpid(), " ".join(sys.argv[1:])))
    f.write("env tmux=%s headless=%s hidden=%s task=%s\n" % (
        os.environ.get("DEVKIT_TMUX", "-"), os.environ.get("DEVKIT_HEADLESS", "-"),
        os.environ.get("DEVKIT_HIDDEN", "-"), os.environ.get("DEVKIT_TASK", "-")))
if os.environ.get("RUNNER_MUTE"):
    sys.exit(0)
pid = os.path.join(lock, "pid")
with open(pid, encoding="utf-8") as f:
    owner = f.read().strip()
if owner == os.environ.get("DEVKIT_TASK_LOCK_FROM"):
    with open(pid + ".tmp", "w", encoding="utf-8") as f:
        f.write("%d\n" % os.getpid())
    os.replace(pid + ".tmp", pid)
time.sleep(float(os.environ.get("RUNNER_HOLD", "5")))
`

const notifyStub = `import os, sys
with open(os.path.join(os.environ["HOME"], "notify.log"), "a", encoding="utf-8") as f:
    f.write(" | ".join(sys.argv[1:]) + "\n")
`

const stubProfile = `[head]
client = ["claude", "--permission-mode", "auto"]
model = ["--model", "{model}"]
session = ["--session-id", "{session}"]
resume = ["claude", "--resume", "{session}"]
turn_end = "turn-mark"
`

type stand struct {
	t                          *testing.T
	home, devkit, root, bin, d string
}

func put(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// newStand собирает стенд. tmux и client говорят, есть ли стабы в PATH.
func newStand(t *testing.T, tmux, client bool) *stand {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 нет в PATH: стаб оболочки поднять нечем")
	}
	base := t.TempDir()
	s := &stand{t: t, home: filepath.Join(base, "home"), devkit: filepath.Join(base, "devkit"),
		root: filepath.Join(base, "proj"), bin: filepath.Join(base, "bin"), d: filepath.Join(base, "tmux")}
	for _, dir := range []string{s.home, s.root, filepath.Join(s.d, "sessions"), filepath.Join(s.d, "panes")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(s.bin, "python3"), "#!/bin/sh\nexec "+shQuote(py)+" \"$@\"\n", 0o755)
	if tmux {
		put(t, filepath.Join(s.bin, "tmux"), tmuxStub, 0o755)
	}
	if client {
		put(t, filepath.Join(s.bin, "claude"), "#!/bin/sh\nexit 0\n", 0o755)
	}
	put(t, filepath.Join(s.devkit, filepath.FromSlash(TaskRunRel)), runnerStub, 0o644)
	put(t, filepath.Join(s.devkit, filepath.FromSlash(NotifierRel)), notifyStub, 0o644)
	put(t, ProfilePath(s.devkit, "stub"), stubProfile, 0o644)
	t.Setenv("PATH", s.bin)
	// Дом стенда и для всего, что уйдёт в подпроцесс мимо заказа: настоящий
	// дом стенду трогать нельзя.
	t.Setenv("HOME", s.home)
	t.Setenv("TMUX_STUB", s.d)
	t.Setenv("RUNNER_MUTE", "")
	// Стабы оболочки живут дольше подъёма, и до уборки каталога их надо снять.
	t.Cleanup(func() {
		for _, l := range s.lines("runner.log") {
			if pid, err := strconv.Atoi(strings.Fields(l)[0]); err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		time.Sleep(50 * time.Millisecond)
	})
	return s
}

func (s *stand) req() Request {
	return Request{ID: "DK-1", Root: s.root, Project: "proj", Home: s.home, Devkit: s.devkit,
		Harness: "stub", Adopt: 10 * time.Second}
}

func (s *stand) raise() Result {
	s.t.Helper()
	res, err := Raise(s.req())
	if err != nil {
		s.t.Fatal(err)
	}
	return res
}

func (s *stand) read(rel string) string {
	data, _ := os.ReadFile(filepath.Join(s.home, rel))
	return string(data)
}

func (s *stand) lines(rel string) []string {
	var out []string
	for _, l := range strings.Split(s.read(rel), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func (s *stand) tmux(rel string) string {
	data, _ := os.ReadFile(filepath.Join(s.d, rel))
	return string(data)
}

// register кладёт в реестр чатов запись сессии, как её пишет хук старта.
func (s *stand) register(ago time.Duration, sid, task, src, name, pane string) {
	s.t.Helper()
	line := fmt.Sprintf("%s сессия %s задача %s проект proj дерево %s транскрипт - источник %s повод startup tmux %s панель %s родитель -\n",
		time.Now().Add(-ago).Format("2006-01-02T15:04:05"), sid, task, s.root, src, name, pane)
	f, err := os.OpenFile(filepath.Join(s.home, ".devkit", "sessions.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.MkdirAll(filepath.Join(s.home, ".devkit"), 0o755)
		f, err = os.OpenFile(filepath.Join(s.home, ".devkit", "sessions.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			s.t.Fatal(err)
		}
	}
	f.WriteString(line)
	f.Close()
}

func (s *stand) pane(addr, answer string) {
	put(s.t, filepath.Join(s.d, "panes", strings.TrimPrefix(addr, "%")), answer+"\n", 0o644)
}

func (s *stand) lockGone() bool {
	_, err := os.Stat(LockPath(s.home, "DK-1"))
	return os.IsNotExist(err)
}

func (s *stand) why(res Result) string {
	return fmt.Sprintf("код %d, ступень %s, ход:\n%s\nвызовы tmux:\n%s\nоболочка:\n%s",
		res.Code, res.Rung, strings.Join(res.Lines, "\n"), s.tmux("calls.log"), s.read("runner.log"))
}

// Ступень 1: живое окно задачи получает реплику, нового окна не заводится.
func TestLiveWindowGetsReply(t *testing.T) {
	s := newStand(t, true, true)
	s.register(time.Minute, "S1", "DK-1", "дерево", "-", "%3")
	s.pane("%3", "0 claude")
	res := s.raise()
	if res.Code != CodeRaised || res.Rung != RungLive {
		t.Fatal(s.why(res))
	}
	keys := s.tmux("keys.log")
	if !strings.Contains(keys, "-t %3 -l Продолжай выполнение DK-1") || !strings.Contains(keys, "-t %3 Enter") {
		t.Fatalf("реплика не подана в панель %%3:\n%s", keys)
	}
	if strings.Contains(s.tmux("calls.log"), "new-session") {
		t.Fatalf("при живом окне заведено новое:\n%s", s.why(res))
	}
	if !s.lockGone() {
		t.Fatal("после реплики замок остался висеть")
	}
}

// Окно, поднятое дашбордом, адресуется именем, когда панели в записи нет.
func TestLiveWindowByName(t *testing.T) {
	s := newStand(t, true, true)
	s.register(time.Minute, "S1", "DK-1", "заказ", "task-DK-1", "-")
	s.pane("task-DK-1", "0 python3")
	res := s.raise()
	if res.Rung != RungLive || !strings.Contains(s.tmux("keys.log"), "-t =task-DK-1: -l") {
		t.Fatal(s.why(res))
	}
}

// Голая оболочка в панели и панель, занятая другим разговором, живым окном
// задачи не считаются, и лестница идёт к новому окну.
func TestStaleWindowsAreSkipped(t *testing.T) {
	cases := map[string]func(s *stand){
		"оболочка": func(s *stand) {
			s.register(time.Minute, "S1", "DK-1", "дерево", "-", "%3")
			s.pane("%3", "0 zsh")
		},
		"чужой хозяин": func(s *stand) {
			s.register(2*time.Minute, "S1", "DK-1", "дерево", "-", "%3")
			s.register(time.Minute, "S2", "DK-7", "дерево", "-", "%3")
			s.pane("%3", "0 claude")
		},
		"мёртвая панель": func(s *stand) {
			s.register(time.Minute, "S1", "DK-1", "дерево", "-", "%3")
			s.pane("%3", "1 claude")
		},
		"работа диспетчера": func(s *stand) {
			s.register(time.Minute, "S1", "DK-1", "работа", "-", "%3")
			s.pane("%3", "0 claude")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStand(t, true, true)
			setup(s)
			res := s.raise()
			if res.Rung != RungWindow {
				t.Fatal(s.why(res))
			}
			if s.tmux("keys.log") != "" {
				t.Fatalf("реплика ушла в чужое окно:\n%s", s.tmux("keys.log"))
			}
		})
	}
}

// Ступень 2: новое окно с оболочкой, сессия в реестре с адресом окна, замок
// у оболочки.
func TestNewWindowRaisesShell(t *testing.T) {
	s := newStand(t, true, true)
	res := s.raise()
	if res.Code != CodeRaised || res.Rung != RungWindow {
		t.Fatal(s.why(res))
	}
	run := s.lines("runner.log")
	if len(run) < 2 || !strings.Contains(run[0], "DK-1 -C "+s.root+" --project proj -- claude --permission-mode auto --session-id ") {
		t.Fatalf("оболочка поднята не той командой:\n%s", s.why(res))
	}
	if !strings.Contains(run[1], "tmux=task-DK-1") || strings.Contains(run[0], "--headless") {
		t.Fatalf("окружение окна: %s", run[1])
	}
	pid, _ := strconv.Atoi(strings.Fields(run[0])[0])
	if Owner(LockPath(s.home, "DK-1")) != pid {
		t.Fatalf("замок не у оболочки: владелец %d, оболочка %d", Owner(LockPath(s.home, "DK-1")), pid)
	}
	reg := s.read(".devkit/sessions.log")
	sid := run[0][strings.Index(run[0], "--session-id ")+len("--session-id "):]
	if !strings.Contains(reg, "сессия "+sid+" задача DK-1 ") || !strings.Contains(reg, "tmux task-DK-1 панель %9") {
		t.Fatalf("реестр чатов без адреса поднятой сессии %s:\n%s", sid, reg)
	}
}

// Второй вызов при занятом замке это отказ, вторая голова не встаёт.
func TestBusyLockRefusesSecondHead(t *testing.T) {
	s := newStand(t, true, true)
	first := s.raise()
	if first.Rung != RungWindow {
		t.Fatal(s.why(first))
	}
	second := s.raise()
	if second.Code != CodeBusy || second.Rung != RungLock {
		t.Fatalf("второй вызов не отказал:\n%s", s.why(second))
	}
	if n := strings.Count(s.tmux("calls.log"), "new-session"); n != 1 {
		t.Fatalf("окон заведено %d, жду одно", n)
	}
	if len(s.lines("runner.log")) != 2 {
		t.Fatalf("оболочек поднято больше одной:\n%s", s.read("runner.log"))
	}
}

// Ступень 3: без tmux голова поднимается headless той же оболочкой.
func TestNoTmuxGoesHeadless(t *testing.T) {
	s := newStand(t, false, true)
	res := s.raise()
	if res.Code != CodeRaised || res.Rung != RungHeadless {
		t.Fatal(s.why(res))
	}
	run := s.lines("runner.log")
	if len(run) < 2 || !strings.Contains(run[0], "--headless -- claude --permission-mode auto") ||
		strings.Contains(run[0], "--session-id") || !strings.Contains(run[1], "headless=taskctl run") {
		t.Fatalf("headless поднят не той командой:\n%s", s.why(res))
	}
	if _, err := os.Stat(filepath.Join(s.home, ".devkit", "heads", "DK-1.log")); err != nil {
		t.Fatalf("журнала headless нет: %v", err)
	}
}

// Окно не поднялось, и лестница спускается на headless.
func TestWindowFailureFallsToHeadless(t *testing.T) {
	s := newStand(t, true, true)
	put(t, filepath.Join(s.d, "newfail"), "", 0o644)
	res := s.raise()
	if res.Rung != RungHeadless || !strings.Contains(strings.Join(res.Lines, "\n"), "tmux не поднял окно task-DK-1") {
		t.Fatal(s.why(res))
	}
}

// Ступень 4: без клиента зовётся человек строкой с готовой командой и
// командой продолжения прошлой сессии.
func TestNoClientCallsHuman(t *testing.T) {
	s := newStand(t, true, false)
	s.register(time.Hour, "S1", "DK-1", "дерево", "-", "-")
	res := s.raise()
	if res.Code != CodeCalled || res.Rung != RungCall {
		t.Fatal(s.why(res))
	}
	note := s.read("notify.log")
	for _, want := range []string{"--reason | run_stop", "--task | DK-1", "клиент claude не нашёлся в PATH",
		"taskctl run DK-1 -C " + s.root, "cd " + s.root + " && claude --resume S1"} {
		if !strings.Contains(note, want) {
			t.Fatalf("в зове нет %q:\n%s", want, note)
		}
	}
	if s.read("runner.log") != "" || strings.Contains(s.tmux("calls.log"), "new-session") {
		t.Fatalf("без клиента голова всё равно поднималась:\n%s", s.why(res))
	}
	if !s.lockGone() {
		t.Fatal("после зова замок остался висеть")
	}
}

// Оболочка, не принявшая замок, это провал ступени: окно снимается, headless
// тоже не встаёт, и дело кончается зовом.
func TestShellWithoutLockEndsInCall(t *testing.T) {
	s := newStand(t, true, true)
	t.Setenv("RUNNER_MUTE", "1")
	q := s.req()
	q.Adopt = 500 * time.Millisecond
	res, err := Raise(q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != CodeCalled || !strings.Contains(s.tmux("calls.log"), "kill-session -t =task-DK-1") {
		t.Fatal(s.why(res))
	}
	if !strings.Contains(s.read("notify.log"), "не приняла замок") {
		t.Fatalf("зов не называет причину:\n%s", s.read("notify.log"))
	}
}

// Сломанная раскладка это ошибка, а не зов: профиля без [head] нет смысла
// обходить лестницей.
func TestMissingHeadSectionIsSetupError(t *testing.T) {
	s := newStand(t, true, true)
	put(t, ProfilePath(s.devkit, "stub"), "[detect]\n", 0o644)
	if _, err := Raise(s.req()); err == nil || !strings.Contains(err.Error(), "нет секции [head]") {
		t.Fatalf("жду отказ раскладки, а пришло %v", err)
	}
}

// Предполёт обхода ждущих (DK-932) называет, чем голову поднимать нечего, до
// снятия парковки, и не трогает ни замка, ни tmux.
func TestPreflight(t *testing.T) {
	s := newStand(t, true, true)
	if err := Preflight(s.req()); err != nil {
		t.Fatalf("исправный стенд отбит предполётом: %v", err)
	}
	client := filepath.Join(s.bin, "claude")
	os.Remove(client)
	if err := Preflight(s.req()); err == nil || !strings.Contains(err.Error(), "клиент claude не нашёлся") {
		t.Fatalf("без клиента жду отказ, а пришло %v", err)
	}
	put(t, client, "#!/bin/sh\nexit 0\n", 0o755)
	runner := filepath.Join(s.devkit, filepath.FromSlash(TaskRunRel))
	os.Remove(runner)
	if err := Preflight(s.req()); err == nil || !strings.Contains(err.Error(), "оболочки") {
		t.Fatalf("без оболочки жду отказ, а пришло %v", err)
	}
	put(t, runner, runnerStub, 0o644)
	put(t, ProfilePath(s.devkit, "stub"), "[detect]\n", 0o644)
	if err := Preflight(s.req()); err == nil || !strings.Contains(err.Error(), "нет секции [head]") {
		t.Fatalf("без [head] жду отказ, а пришло %v", err)
	}
	if s.tmux("calls.log") != "" || !s.lockGone() {
		t.Fatalf("предполёт тронул tmux или замок: %q", s.tmux("calls.log"))
	}
}

func TestDevkitLookup(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	dk := filepath.Join(base, "devkit")
	put(t, filepath.Join(dk, filepath.FromSlash(TaskRunRel)), "", 0o644)
	os.MkdirAll(proj, 0o755)
	t.Setenv("DEVKIT_HOME", "")
	if got := Devkit(proj, filepath.Join(base, "home")); got != dk {
		t.Fatalf("сосед проекта не найден: %q", got)
	}
	if got := Devkit(filepath.Join(base, "other", "x"), filepath.Join(base, "home")); got != "" {
		t.Fatalf("devkit найден там, где его нет: %q", got)
	}
}

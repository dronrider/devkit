package taskhead

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// TaskRunRel это оболочка конвейера задачи внутри дерева devkit.
const TaskRunRel = "kit/skills/board-task/task-run.py"

// NotifierRel это уведомитель, которым зовётся человек.
const NotifierRel = "hooks/notify.py"

// AdoptEnv подменяет срок передачи замка оболочке, в секундах. Нужен стенду:
// оболочка-стаб, не принявшая замок, иначе держала бы тест полминуты.
const AdoptEnv = "DEVKIT_RUN_ADOPT_WAIT"

// DefaultAdopt это срок, за который оболочка обязана принять замок. Принимает
// она его первым делом, до разговора с доской, и срок тут с запасом на
// холодный старт tmux-сервера под нагрузкой.
const DefaultAdopt = 30 * time.Second

// AdoptWait читает срок передачи замка из AdoptEnv. Ноль значит, что
// переменной нет, и Raise возьмёт DefaultAdopt.
func AdoptWait() (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(AdoptEnv))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s ждёт число секунд, а пришло %q", AdoptEnv, v)
	}
	return time.Duration(n) * time.Second, nil
}

// Коды выхода команды подъёма.
const (
	CodeRaised = 0 // голова поднята либо реплика подана в живое окно
	CodeCalled = 1 // поднять нечем, позван человек
	CodeSetup  = 2 // ошибка вызова или раскладки
	CodeBusy   = 3 // замок занят, вторая голова не встаёт
)

// Ступени лестницы, словом для вывода и журнала.
const (
	RungLock     = "замок"
	RungLive     = "живое окно"
	RungWindow   = "новое окно"
	RungHeadless = "headless"
	RungCall     = "зов человеку"
)

// Слова источника привязки, по которым окно считается окном задачи: сессия
// поднята заказом задачи, родилась в её боковом дереве либо привязана к ней
// человеком. Запись по факту работы сюда не входит: командами доски двигает
// строку и диспетчер пачки, а заказ «продолжай» в его окне был бы чужим.
var ownSources = map[string]bool{sessions.ByOrder: true, sessions.ByTree: true, sessions.ByHand: true}

// Оболочки, в которые реплику не подают: панель жива, а клиент в ней уже
// вышел. Строка «продолжай» ушла бы командой в шелл.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "ksh": true, "tcsh": true, "csh": true, "login": true}

// Request это заказ подъёма.
type Request struct {
	ID      string // задача доски
	Root    string // корень проекта, где живёт доска
	Project string // имя проекта для журналов и уведомлений
	Home    string // дом, от которого считаются ~/.devkit и реестр
	Devkit  string // дерево devkit: оболочка, профили, уведомитель
	Harness string // имя профиля харнеса
	Model   string // ярус моделью, пусто значит спросить Pick
	// Pick называет модель вердиктом, когда Model пуст. Зовётся только перед
	// стартом нового клиента. Отказ оставляет клиенту его умолчание.
	Pick   func() (string, error)
	Order  string // заказ первого прохода, пусто значит умолчание оболочки
	Again  string // заказ следующих проходов
	Hidden bool   // поднято без человека (DK-847)
	Adopt  time.Duration
	// Prefix это готовая шелл-приставка зовущего перед оболочкой в новом окне:
	// чистка унаследованного окружения и свои пары. Стоит она после пар
	// команды и перекрывает их. Нужна дашборду (DK-935): его окно получает
	// настоящий дом, путь с утилитами кита и метку печатного режима, а собирает
	// их одна сборка на все его дороги подъёма.
	Prefix string
}

// Result это исход лестницы.
type Result struct {
	Code  int
	Rung  string
	Lines []string
}

func (r *Result) say(format string, a ...any) {
	r.Lines = append(r.Lines, fmt.Sprintf(format, a...))
}

// Word это реплика «продолжай» для живого окна.
func (q Request) Word() string {
	if q.Order != "" {
		return q.Order
	}
	return "Продолжай выполнение " + q.ID
}

// pickModel называет модель вердиктом, когда заказ её не назвал. Спрашивается
// он только перед стартом нового клиента: живому окну и занятому замку модель
// не нужна, а тик зовёт подъём на каждом заходе, и вердикт с записью этапа
// копил бы записи впустую. Исход ложится строкой вывода в обе стороны.
func (q *Request) pickModel(res *Result) {
	if q.Model != "" || q.Pick == nil {
		return
	}
	model, err := q.Pick()
	if err != nil {
		res.say("модель вердиктом не выбрана, клиент стартует на своём умолчании: %v", err)
		return
	}
	q.Model = model
	res.say("модель %s по вердикту agentctl pick", model)
}

// Raise ведёт лестницу носителей. Ошибка это сломанная раскладка (нет
// профиля, нет секции [head]), а всё, что случилось на ступенях, лежит в
// Result.
func Raise(q Request) (Result, error) {
	var res Result
	head, err := ReadHead(ProfilePath(q.Devkit, q.Harness))
	if err != nil {
		return res, err
	}
	if q.Adopt <= 0 {
		q.Adopt = DefaultAdopt
	}
	lock, me := LockPath(q.Home, q.ID), os.Getpid()
	if err := Take(lock, me); err != nil {
		if busy, ok := err.(*BusyError); ok {
			res.Code, res.Rung = CodeBusy, RungLock
			res.say("%s: %s", q.ID, busy.Error())
			return res, nil
		}
		return res, fmt.Errorf("замок %s не взят: %v", lock, err)
	}
	// Замок, переданный оболочке, команде уже не принадлежит, и Release его не
	// тронет. Снимается он тут только на ступенях без подъёма.
	defer Release(lock, me)

	tmux, _ := exec.LookPath("tmux")
	if tmux == "" {
		res.say("tmux не нашёлся в PATH: живого и нового окна не будет, лестница начинается с headless")
	} else if q.replyLive(tmux, &res) {
		res.Code, res.Rung = CodeRaised, RungLive
		return res, nil
	}
	runner := filepath.Join(q.Devkit, filepath.FromSlash(TaskRunRel))
	switch {
	case !isFile(runner):
		res.say("оболочки %s нет: поднимать голову нечем", runner)
	case !found(head.Bin):
		res.say("клиент %s не нашёлся в PATH: поднимать голову нечем", head.Bin)
	default:
		q.pickModel(&res)
		if tmux != "" && q.newWindow(tmux, runner, head, lock, me, &res) {
			res.Code, res.Rung = CodeRaised, RungWindow
			return res, nil
		}
		if q.headless(runner, head, lock, me, &res) {
			res.Code, res.Rung = CodeRaised, RungHeadless
			return res, nil
		}
	}
	res.Code, res.Rung = CodeCalled, RungCall
	q.callHuman(head, &res)
	return res, nil
}

// Preflight отвечает, есть ли чем поднять голову, не трогая ни замка, ни tmux:
// профиль с секцией [head], оболочка task-run.py и клиент в PATH. Нужна она
// тем, кто до подъёма снимает парковку строки (обход ждущих в taskctl, DK-932).
// Снятая впустую парковка оставила бы строку в работе без головы, а при
// отказе предполёта строка стоит в Blocked, и следующий обход повторит.
func Preflight(q Request) error {
	head, err := ReadHead(ProfilePath(q.Devkit, q.Harness))
	if err != nil {
		return err
	}
	runner := filepath.Join(q.Devkit, filepath.FromSlash(TaskRunRel))
	if !isFile(runner) {
		return fmt.Errorf("оболочки %s нет: поднимать голову нечем", runner)
	}
	if !found(head.Bin) {
		return fmt.Errorf("клиент %s не нашёлся в PATH: поднимать голову нечем", head.Bin)
	}
	return nil
}

// window это окно задачи из реестра: сессия, адрес и время записи.
type window struct{ sid, addr, at string }

// windows называет окна задачи из реестра, свежие первыми. Адрес это панель,
// а без неё имя tmux-сессии. Окно, чей адрес свежей записью занял другой
// разговор, в список не идёт.
func (q Request) windows() []window {
	recs := sessions.LoadAll(q.Home)
	id := strings.ToUpper(q.ID)
	var out []window
	for sid, rs := range recs {
		mine := false
		for _, r := range rs {
			if r.Task == id && ownSources[r.Source] {
				mine = true
			}
		}
		if !mine || sessions.Off(rs, id) {
			continue
		}
		last := sessions.Last(rs)
		addr, owner := last.Pane, sessions.PaneOwner(recs, last.Pane)
		if addr == "" {
			addr, owner = last.Tmux, sessions.TmuxOwner(recs, last.Tmux)
		}
		if addr == "" || owner != sid {
			continue
		}
		out = append(out, window{sid: sid, addr: addr, at: last.Time})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at > out[j].at })
	return out
}

// target это адрес для tmux -t: панель как есть, имя точным совпадением.
func target(addr string) string {
	if strings.HasPrefix(addr, "%") {
		return addr
	}
	return "=" + addr + ":"
}

// replyLive подаёт реплику в живое окно задачи. Правда значит, что реплика
// ушла.
func (q Request) replyLive(tmux string, res *Result) bool {
	for _, w := range q.windows() {
		t := target(w.addr)
		out, err := exec.Command(tmux, "display-message", "-p", "-t", t,
			"#{pane_dead} #{pane_current_command}").Output()
		if err != nil {
			res.say("окно %s сессии %s в tmux уже нет", w.addr, w.sid)
			continue
		}
		f := strings.Fields(string(out))
		if len(f) < 2 || f[0] != "0" {
			res.say("панель %s сессии %s мертва", w.addr, w.sid)
			continue
		}
		if shells[strings.TrimPrefix(f[1], "-")] {
			res.say("в панели %s сессии %s голая оболочка %s, клиент там уже не стоит", w.addr, w.sid, f[1])
			continue
		}
		if err := sendKeys(tmux, t, q.Word()); err != nil {
			res.say("реплика в окно %s не подана: %v", w.addr, err)
			continue
		}
		res.say("голова %s жива в окне %s (сессия %s): подана реплика «%s»", q.ID, w.addr, w.sid, q.Word())
		return true
	}
	return false
}

// sendKeys подаёт строку в панель клавиатурой: литералом, а перевод строки
// отдельным нажатием с паузой. Enter в том же пакете обгоняет отрисовку поля,
// и клиент получает половину реплики (chats.go, task-run.py).
func sendKeys(tmux, t, text string) error {
	if out, err := exec.Command(tmux, "send-keys", "-t", t, "-l", text).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	time.Sleep(250 * time.Millisecond)
	if out, err := exec.Command(tmux, "send-keys", "-t", t, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runnerArgs это оболочка с задачей, корнем и заказами, без клиента.
func (q Request) runnerArgs(runner string, headless bool) []string {
	args := []string{"python3", runner, q.ID, "-C", q.Root, "--project", q.Project}
	if q.Order != "" {
		args = append(args, "--order", q.Order)
	}
	if q.Again != "" {
		args = append(args, "--again", q.Again)
	}
	if headless {
		args = append(args, "--headless")
	}
	return args
}

// env это пары окружения головы. HOME называется явно: tmux-сервер, поднятый
// демоном, наследует чужой дом, а по нему оболочка ищет замок и реестр.
func (q Request) env(me int, name string) []string {
	pairs := []string{
		"HOME=" + q.Home,
		"PATH=" + os.Getenv("PATH"),
		"DEVKIT_TASK=" + q.ID,
		"DEVKIT_HARNESS=" + q.Harness,
		LockFromEnv + "=" + fmt.Sprint(me),
	}
	if name != "" {
		pairs = append(pairs, "DEVKIT_TMUX="+name)
	} else {
		pairs = append(pairs, "DEVKIT_HEADLESS=taskctl run")
	}
	if q.Hidden {
		pairs = append(pairs, "DEVKIT_HIDDEN=1")
	}
	return pairs
}

// newWindow поднимает окно tmux task-<ID> с оболочкой. Сессию живой головы
// команда называет сама и пишет её в реестр чатов с адресом окна, когда
// оболочка приняла замок.
func (q Request) newWindow(tmux, runner string, head Head, lock string, me int, res *Result) bool {
	name := "task-" + q.ID
	if exec.Command(tmux, "has-session", "-t", "="+name).Run() == nil {
		// Замок свободен, живого окна в реестре нет, а имя занято: это остаток
		// прошлой головы (оболочка убита, панель стоит). Второй головы тут нет.
		exec.Command(tmux, "kill-session", "-t", "="+name).Run()
		res.say("остаток окна %s снят: замок был свободен", name)
	}
	sid := ""
	if len(head.Session) > 0 {
		sid = newSessionID()
	}
	prefix := strings.TrimSpace(q.Prefix)
	if prefix != "" {
		prefix += " "
	}
	cmd := envJoin(q.env(me, name)) + " " + prefix +
		shellJoin(append(append(q.runnerArgs(runner, head.TurnEnd == TurnExit), "--"), head.Command(q.Model, sid)...))
	out, err := exec.Command(tmux, "new-session", "-d", "-s", name, "-c", q.Root,
		"-P", "-F", "#{pane_id}", cmd).CombinedOutput()
	if err != nil {
		res.say("tmux не поднял окно %s: %v %s", name, err, strings.TrimSpace(string(out)))
		return false
	}
	pane := strings.TrimSpace(string(out))
	gone := func() bool { return exec.Command(tmux, "has-session", "-t", "="+name).Run() != nil }
	owner := WaitHandoff(lock, me, q.Adopt, gone)
	if owner == 0 {
		exec.Command(tmux, "kill-session", "-t", "="+name).Run()
		res.say("окно %s поднялось, а оболочка не приняла замок за %s: окно снято", name, q.Adopt)
		return false
	}
	// Запись ложится после передачи замка: сессия окна, где оболочка упала,
	// так и не родилась, и зов не должен предлагать её продолжить.
	if sid != "" {
		line := sessions.Line(time.Now(), sid, sessions.Bind{Task: q.ID, Project: q.Project,
			Tree: q.Root, Source: sessions.ByOrder, Tmux: name, Pane: pane}, "taskctl run")
		if err := sessions.Append(sessions.Path(q.Home), line); err != nil {
			res.say("запись реестра чатов про сессию %s не легла: %v", sid, err)
		}
	}
	tail := ""
	if sid != "" {
		tail = ", сессия " + sid
	}
	res.say("голова %s поднята в новом окне tmux %s (панель %s%s), оболочка pid %d", q.ID, name, pane, tail, owner)
	return true
}

// headless поднимает оболочку отдельным процессом без окна: печатная череда,
// вывод в журнал ~/.devkit/heads/<ID>.log.
func (q Request) headless(runner string, head Head, lock string, me int, res *Result) bool {
	logPath := filepath.Join(q.Home, ".devkit", "heads", q.ID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		res.say("журнал headless %s не завёлся: %v", logPath, err)
		return false
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		res.say("журнал headless %s не открылся: %v", logPath, err)
		return false
	}
	defer f.Close()
	args := append(append(q.runnerArgs(runner, true), "--"), head.Command(q.Model, "")...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = q.Root
	cmd.Env = append(os.Environ(), q.env(me, "")...)
	cmd.Stdout, cmd.Stderr = f, f
	// Своя сессия процесса: голова переживает выход команды и не получает
	// сигналов её терминала.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		res.say("оболочка headless не запустилась: %v", err)
		return false
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	gone := func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	owner := WaitHandoff(lock, me, q.Adopt, gone)
	if owner == 0 {
		cmd.Process.Kill()
		res.say("оболочка headless не приняла замок за %s, журнал %s", q.Adopt, logPath)
		return false
	}
	res.say("голова %s поднята headless, оболочка pid %d, журнал %s", q.ID, owner, logPath)
	return true
}

// Command это готовая команда подъёма для текста зова.
func (q Request) Command() string {
	return "taskctl run " + q.ID + " -C " + shQuote(q.Root)
}

// callHuman зовёт человека уведомителем: головы нет, и молчать об этом нельзя.
func (q Request) callHuman(head Head, res *Result) {
	why := strings.Join(res.Lines, "; ")
	text := fmt.Sprintf("голову %s поднять нечем: %s. Поднять руками: %s", q.ID, why, q.Command())
	if sid, _ := sessions.Load(q.Home).Leads(q.ID); sid != "" {
		if resume := head.ResumeCommand(sid); len(resume) > 0 {
			text += "; продолжить прошлую сессию: cd " + shQuote(q.Root) + " && " + shellJoin(resume)
		}
	}
	res.say("%s", text)
	notifier := filepath.Join(q.Devkit, filepath.FromSlash(NotifierRel))
	if !isFile(notifier) {
		res.say("уведомителя %s нет: зов остался только в этом выводе", notifier)
		return
	}
	title := fmt.Sprintf("%s: %s голова не поднята", q.Project, q.ID)
	cmd := exec.Command("python3", notifier, "--reason", "run_stop", "--task", q.ID,
		"--project", q.Project, title, text)
	cmd.Dir = q.Root
	// Журнал уведомителя лежит в том же доме, что замок и реестр.
	cmd.Env = append(os.Environ(), "HOME="+q.Home)
	out, err := cmd.CombinedOutput()
	said := strings.TrimSpace(string(out))
	if err != nil {
		res.say("уведомитель отказал: %v %s", err, said)
		return
	}
	// Пропуск доставки (песочница, выключенный уведомитель) уведомитель
	// называет в stdout, и эти слова идут в вывод как есть.
	if said != "" {
		res.say("уведомитель: %s", said)
		return
	}
	res.say("уведомитель отработал, строка зова в ~/.devkit/notify.log")
}

// Devkit ищет дерево devkit с оболочкой конвейера: явная переменная
// DEVKIT_HOME, симлинк .devkit/devkit проекта (DK-193), сам корень, сосед
// проекта и путь из README. Пусто значит, что оболочки нет нигде.
func Devkit(root, home string) string {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, v)
	}
	cands = append(cands, filepath.Join(root, ".devkit", "devkit"), root,
		filepath.Join(filepath.Dir(root), "devkit"), filepath.Join(home, "projects", "devkit"))
	for _, c := range cands {
		if isFile(filepath.Join(c, filepath.FromSlash(TaskRunRel))) {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func found(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// shQuote квотит слово для шелла: команда окна уходит строкой, и пробел в
// пути рассыпал бы её на слова. Слово из безопасных знаков остаётся голым:
// готовую команду из зова читает и копирует человек.
func shQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:%@+=,-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = shQuote(w)
	}
	return strings.Join(out, " ")
}

// envJoin это пары окружения перед командой шелла: имя как есть, значение в
// кавычках.
func envJoin(pairs []string) string {
	out := make([]string, len(pairs))
	for i, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		out[i] = k + "=" + shQuote(v)
	}
	return strings.Join(out, " ")
}

// newSessionID это UUID четвёртой версии, как их ждёт клиент в --session-id.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

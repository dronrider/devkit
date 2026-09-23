package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/dronrider/devkit/internal/taskhead"
)

// runDevkit раскладывает дерево devkit стенда: оболочка и профиль. Сама
// лестница проверена в internal/taskhead, тут проверяется обвязка команды.
func runDevkit(t *testing.T) (root, home, dk string) {
	t.Helper()
	base := t.TempDir()
	root, home, dk = filepath.Join(base, "proj"), filepath.Join(base, "home"), filepath.Join(base, "devkit")
	for _, p := range []string{root, home, filepath.Join(dk, "kit", "harness"), filepath.Join(dk, "kit", "skills", "board-task")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dk, filepath.FromSlash(taskhead.TaskRunRel)), []byte(""), 0o644)
	os.WriteFile(taskhead.ProfilePath(dk, "claude-code"), []byte("[head]\nclient = [\"claude\"]\n"), 0o644)
	t.Setenv("DEVKIT_HOME", dk)
	t.Setenv("HOME", home)
	return root, home, dk
}

func TestRunRequestPicksHarness(t *testing.T) {
	root, home, dk := runDevkit(t)
	t.Setenv(taskhead.HarnessEnv, "")
	q, err := runRequest(root, home, "dk-7", runOpts{model: "opus", hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if q.ID != "DK-7" || q.Harness != taskhead.DefaultHarness || q.Devkit != dk || q.Project != "proj" ||
		q.Model != "opus" || !q.Hidden {
		t.Fatalf("заказ %+v", q)
	}
	t.Setenv(taskhead.HarnessEnv, "glm-code")
	if q, _ := runRequest(root, home, "DK-7", runOpts{}); q.Harness != "glm-code" {
		t.Fatalf("переменная харнеса не прочитана: %s", q.Harness)
	}
	if q, _ := runRequest(root, home, "DK-7", runOpts{harness: "x"}); q.Harness != "x" {
		t.Fatalf("флаг харнеса не старше переменной: %s", q.Harness)
	}
}

func TestRunRequestRefusals(t *testing.T) {
	root, home, _ := runDevkit(t)
	if _, err := runRequest(root, home, "не-ид", runOpts{}); err == nil {
		t.Fatal("кривой ID принят")
	}
	t.Setenv(taskhead.AdoptEnv, "полминуты")
	if _, err := runRequest(root, home, "DK-7", runOpts{}); err == nil || !strings.Contains(err.Error(), taskhead.AdoptEnv) {
		t.Fatalf("кривой срок передачи замка: %v", err)
	}
	t.Setenv(taskhead.AdoptEnv, "")
	t.Setenv("DEVKIT_HOME", filepath.Join(home, "нет"))
	if _, err := runRequest(filepath.Join(home, "далеко", "proj"), home, "DK-7", runOpts{}); err == nil ||
		!strings.Contains(err.Error(), "DEVKIT_HOME") {
		t.Fatalf("без дерева devkit: %v", err)
	}
}

// Занятый замок доходит до команды кодом 3 и словами про владельца.
func TestCmdRunBusyLock(t *testing.T) {
	root, home, _ := runDevkit(t)
	sleep := exec.Command("/bin/sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sleep.Process.Kill(); sleep.Wait() })
	if err := taskhead.Take(taskhead.LockPath(home, "DK-7"), sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}
	out, code, err := cmdRun(root, "DK-7", runOpts{})
	if err != nil || code != taskhead.CodeBusy || !strings.Contains(out, "уже поднята") {
		t.Fatalf("код %d, ошибка %v, вывод:\n%s", code, err, out)
	}
}

// headStub играет оболочку task-run.py: пишет свой вызов в runner.log, свой pid
// в heads.log, принимает замок задачи у команды и держит его, пока стенд не
// снимет голову.
//
// Приём замка тут это тот самый факт, которого ждёт лестница подъёма, и стенд
// стоит на нём, а не на секундах (DK-764). Прежняя заглушка выходила молча:
// передачи замка лестница не дожидалась никогда, ждала её стенные две секунды и
// по сроку убивала заглушку. Под нагрузкой полного прогона заглушка не успевала
// получить процессор за этот срок, умирала до первой своей строки, и вызов
// оболочки в runner.log не появлялся вовсе.
//
// Задержка STUB_HEAD_DELAY играет ту нехватку процессора, которую под полным
// прогоном получает оболочка на старте. Сценарий проверки задаёт её числом и
// смотрит, с какой задержки подъём краснеет. Запас тут боевой, DefaultAdopt, и
// своего потолка стенд не ставит. Умолчание нулевое.
//
// Утилиты названы путями: тест с урезанным PATH оставляет в нём одни заглушки.
const headStub = `#!/bin/sh
/bin/sleep "${STUB_HEAD_DELAY:-0}"
echo "$*" >> "$STUB_LOGS/runner.log"
echo "$$" >> "$STUB_LOGS/heads.log"
lock="$HOME/.devkit/task-$DEVKIT_TASK.lock"
owner=""
if [ -f "$lock/pid" ]; then
  read owner < "$lock/pid"
fi
if [ "$owner" = "$DEVKIT_TASK_LOCK_FROM" ]; then
  echo "$$" > "$lock/pid.tmp"
  /bin/mv "$lock/pid.tmp" "$lock/pid"
fi
exec /bin/sleep 300
`

// killHeads снимает оболочки, поднятые стендом: их pid лежат в heads.log. Замок
// они держат до конца теста, и без этого дерево стенда осталось бы со спящим
// процессом.
func killHeads(logs string) {
	for _, line := range strings.Fields(readStub(logs, "heads.log")) {
		if pid, err := strconv.Atoi(line); err == nil {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	os.Remove(filepath.Join(logs, "heads.log"))
}

// dropHead снимает голову задачи и освобождает её замок. Нужна тесту, который
// поднимает голову той же задаче второй раз: замок первой головы занят её
// оболочкой, и второй подъём упёрся бы в него.
func dropHead(t *testing.T, logs, home, id string) {
	t.Helper()
	killHeads(logs)
	if err := os.RemoveAll(taskhead.LockPath(home, id)); err != nil {
		t.Fatal(err)
	}
}

// wantHeadless требует ступени headless с принятым замком. Пока замок не
// передан, вызова оболочки в runner.log может и не быть: лестница снимает
// оболочку, не принявшую замок. Так что проверять вызов, не проверив ступени,
// значит читать журнал, которого ещё нет (DK-764).
func wantHeadless(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "поднята headless, оболочка pid") {
		t.Fatalf("оболочка стенда не приняла замок, лестница до headless не дошла:\n%s", out)
	}
}

// pickStand кладёт в PATH заглушки подъёма вместо tmux, клиента и agentctl
// машины. agentctl пишет свой вызов в pick.log и отвечает вердиктом с моделью
// verdict, а пустой verdict играет отказ вердикта. python3 играет оболочку
// task-run.py заглушкой headStub. tmux отказывает на всём, и лестница идёт
// headless. Профиль несёт флаги яруса, как у claude-code. Возврат это каталог
// журналов заглушек и каталог самих заглушек.
func pickStand(t *testing.T, dk, verdict string) (logs, bin string) {
	t.Helper()
	logs, bin = t.TempDir(), t.TempDir()
	profile := "[head]\nclient = [\"claude\", \"--permission-mode\", \"auto\"]\nmodel = [\"--model\", \"{model}\"]\n"
	if err := os.WriteFile(taskhead.ProfilePath(dk, "claude-code"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	answer := "echo 'вердикт сломан' >&2; exit 2"
	if verdict != "" {
		answer = "printf 'model: " + verdict + "\\neffort: high\\ntier: pro\\n'"
	}
	stubs := map[string]string{
		"agentctl": "#!/bin/sh\necho \"$DEVKIT_HARNESS $*\" >> \"$STUB_LOGS/pick.log\"\n" + answer + "\n",
		"python3":  headStub,
		"tmux":     "#!/bin/sh\nexit 1\n",
		"claude":   "#!/bin/sh\nexit 0\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("STUB_LOGS", logs)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Срок передачи замка остаётся производственным: стенд ждёт факта передачи,
	// а не срока, и своего потолка секунд ему не нужно.
	t.Setenv(taskhead.AdoptEnv, "")
	t.Setenv(taskhead.HarnessEnv, "")
	t.Cleanup(func() { killHeads(logs) })
	return logs, bin
}

func readStub(dir, name string) string {
	data, _ := os.ReadFile(filepath.Join(dir, name))
	return string(data)
}

// DK-932, замечание ревью: подъём без --model спрашивает модель вердиктом
// agentctl pick с потолком цели из шапки файла задачи, и модель доходит до
// команды клиента. Записи этапа вердикт не просит (DK-911).
func TestRunTakesModelFromPick(t *testing.T) {
	root, _, dk := runDevkit(t)
	logs, _ := pickStand(t, dk, "sonnet")
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(tasks, "DK-7.md"),
		[]byte("# DK-7: хвост\n\nЦель: [tasks/DK-900.md](tasks/DK-900.md)\n\n## Что происходит\n"), 0o644)
	os.WriteFile(filepath.Join(tasks, "DK-900.md"), []byte("# DK-900: цель\n"), 0o644)
	out, _, err := cmdRun(root, "DK-7", runOpts{})
	if err != nil {
		t.Fatal(err)
	}
	wantHeadless(t, out)
	if runner := readStub(logs, "runner.log"); !strings.Contains(runner, "-- claude --permission-mode auto --model sonnet") {
		t.Fatalf("модель вердикта не дошла до клиента:\n%s\nвывод:\n%s", runner, out)
	}
	if pick := strings.TrimSpace(readStub(logs, "pick.log")); pick != "claude-code pick DK-7 --goal docs/tasks/DK-900.md" {
		t.Fatalf("вердикт спрошен не так: %q", pick)
	}
	if !strings.Contains(out, "модель sonnet по вердикту agentctl pick") {
		t.Fatalf("вывод не называет, откуда модель:\n%s", out)
	}
}

// Явный --model сильнее вердикта: agentctl не спрашивается вовсе.
func TestRunModelFlagBeatsPick(t *testing.T) {
	root, _, dk := runDevkit(t)
	logs, _ := pickStand(t, dk, "sonnet")
	out, _, err := cmdRun(root, "DK-7", runOpts{model: "haiku"})
	if err != nil {
		t.Fatal(err)
	}
	wantHeadless(t, out)
	runner := readStub(logs, "runner.log")
	if !strings.Contains(runner, "-- claude --permission-mode auto --model haiku") || strings.Contains(runner, "sonnet") {
		t.Fatalf("явная модель не дошла до клиента:\n%s\nвывод:\n%s", runner, out)
	}
	if pick := readStub(logs, "pick.log"); pick != "" {
		t.Fatalf("вердикт спрошен при явной модели: %q", pick)
	}
}

// Отказ вердикта и agentctl, которого нет в PATH, подъём не валят: клиент
// стартует без модели, а журнал проекта называет причину.
func TestRunPickRefusalStartsWithoutModel(t *testing.T) {
	root, home, dk := runDevkit(t)
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	logs, bin := pickStand(t, dk, "")
	out, _, err := cmdRun(root, "DK-7", runOpts{})
	if err != nil {
		t.Fatal(err)
	}
	wantHeadless(t, out)
	runner := readStub(logs, "runner.log")
	if !strings.Contains(runner, "-- claude --permission-mode auto") || strings.Contains(runner, "--model") {
		t.Fatalf("клиент без модели не поднят:\n%s\nвывод:\n%s", runner, out)
	}
	if !strings.Contains(out, "клиент стартует на своём умолчании") {
		t.Fatalf("вывод молчит про отказ вердикта:\n%s", out)
	}
	journal := readStub(filepath.Join(root, ".devkit"), "log")
	if !strings.Contains(journal, "run DK-7 без модели: agentctl pick DK-7 отказал") || !strings.Contains(journal, "вердикт сломан") {
		t.Fatalf("журнал не называет отказ вердикта:\n%s", journal)
	}

	dropHead(t, logs, home, "DK-7")
	os.Remove(filepath.Join(bin, "agentctl"))
	t.Setenv("PATH", bin)
	again, _, err := cmdRun(root, "DK-7", runOpts{})
	if err != nil {
		t.Fatal(err)
	}
	wantHeadless(t, again)
	if n := strings.Count(readStub(logs, "runner.log"), "-- claude"); n != 2 {
		t.Fatalf("без agentctl клиент не поднят, подъёмов %d", n)
	}
	if !strings.Contains(readStub(filepath.Join(root, ".devkit"), "log"), "agentctl не нашёлся в PATH") {
		t.Fatalf("журнал молчит про agentctl вне PATH")
	}
}

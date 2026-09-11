package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

// raiseCall это вызов подъёма, записанный стендом.
type raiseCall struct {
	ID     string
	Hidden bool
}

// muteRaiseInTests подменяет подъём головы и предполёт на весь пакет: close с
// DK-932 зовёт обход ждущих, и без подмены тест дотянулся бы до tmux и клиента
// машины.
func muteRaiseInTests() {
	raiseHead = func(root, id string, o runOpts) (string, int, error) {
		return "стенд: голова не поднимается\nступень: стенд, код 0\n", 0, nil
	}
	wakePreflight = func(root, id string, o runOpts) error { return nil }
}

// recordRaise подменяет подъём записью вызовов на время теста.
func recordRaise(t *testing.T) *[]raiseCall {
	t.Helper()
	var calls []raiseCall
	old := raiseHead
	raiseHead = func(root, id string, o runOpts) (string, int, error) {
		calls = append(calls, raiseCall{ID: id, Hidden: o.hidden})
		return "голова " + id + " поднята стендом\nступень: стенд, код 0\n", 0, nil
	}
	t.Cleanup(func() { raiseHead = old })
	return &calls
}

// parkRow доводит строку до работы и паркует причиной reason.
func parkRow(t *testing.T, root, id, reason string) {
	t.Helper()
	if sectOf(t, root, id) != SectInProgress {
		if _, err := cmdMove(root, id, SectInProgress, "", CommitOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cmdMove(root, id, SectBlocked, reason, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

// archiveByHand увозит строку в архив мимо close: так выглядит событие,
// которое прошло мимо хвоста close и которое добирает тик.
func archiveByHand(t *testing.T, root, id string) {
	t.Helper()
	f, err := os.OpenFile(archivePath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintf(f, "| %s | Закрыта руками | task | P2 | 2026-09-11 | - |\n", id)
}

func replaceInBoard(t *testing.T, root, from, to string) {
	t.Helper()
	data, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("на доске нет %q", from)
	}
	if err := os.WriteFile(boardPath(root), []byte(strings.Replace(string(data), from, to, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wakeGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// DoD: разряды «слияние:» и «закрытие:» проходят с ID предпосылки. Разряд без
// ID, кривая форма разряда, ожидание самой себя, неизвестная предпосылка и уже
// случившееся событие дают отказ: такая строка спала бы в Blocked впустую.
func TestParkWaitClasses(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"слияние:", "слияние: потом", "закрытие:   ", "Закрытие: XR-005", "слияние : XR-005",
		"закрытие: XR-004", "закрытие: XR-999", "закрытие: XR-007",
	} {
		if _, err := cmdMove(root, "XR-004", SectBlocked, bad, CommitOpts{}); err == nil {
			t.Fatalf("причина %q должна падать", bad)
		}
	}
	if got := sectOf(t, root, "XR-004"); got != SectInProgress {
		t.Fatalf("отбитая парковка сдвинула строку в %s", got)
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "закрытие: XR-005, после неё правка", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	b, _ := LoadBoard(boardPath(root))
	if !strings.Contains(b.find("XR-004").Title, "[блок: закрытие: XR-005, после неё правка]") {
		t.Fatalf("причина не легла в заголовок: %q", b.find("XR-004").Title)
	}
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	// Вне git признак «слита» спросить нечем: парковку это не держит.
	if _, err := cmdMove(root, "XR-004", SectBlocked, "слияние: XR-005", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

// DoD: close поднимает строку, припаркованную на «закрытие: <ID>», и только её.
// Подъём идёт без человека и виден строкой .devkit/log с разрядом и ID
// предпосылки.
func TestCloseWakesParkedOnClose(t *testing.T) {
	root := setup(t)
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := recordRaise(t)
	parkRow(t, root, "XR-004", "закрытие: XR-005")
	parkRow(t, root, "XR-001", "закрытие: XR-002")
	parkRow(t, root, "XR-003", "окружение: нет стенда")
	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != (raiseCall{ID: "XR-004", Hidden: true}) {
		t.Fatalf("жду один скрытый подъём XR-004, а было %+v\n%s", *calls, msg)
	}
	for id, want := range map[string]string{"XR-004": SectInProgress, "XR-001": SectBlocked, "XR-003": SectBlocked} {
		if got := sectOf(t, root, id); got != want {
			t.Fatalf("%s в %s, жду %s", id, got, want)
		}
	}
	for _, want := range []string{"XR-004: ждала «закрытие: XR-005», XR-005 закрыта", "голова XR-004 поднята стендом", "разбужено 1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("в отчёте close нет %q:\n%s", want, msg)
		}
	}
	log, _ := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if !strings.Contains(string(log), "\ttaskctl\twake XR-004 закрытие: XR-005\t0\n") {
		t.Fatalf("подъёма нет в .devkit/log:\n%s", log)
	}
}

// Close без ждущих отчёт не меняет: хвост обхода молчит.
func TestCloseWithoutWaitersIsQuiet(t *testing.T) {
	root := setup(t)
	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-11"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "ждущ") {
		t.Fatalf("обход без ждущих заговорил в отчёте close:\n%s", msg)
	}
}

// DoD: строку на «слияние: <ID>» поднимает признак «слита», а не статус строки:
// коммит доски с ID задачи работой не считается, коммит кода считается.
// Проверяется на временном репозитории.
func TestWakeRaisesOnMerge(t *testing.T) {
	root := setup(t)
	wakeGit(t, root, "init", "-q", "-b", "main")
	wakeGit(t, root, "add", ".")
	wakeGit(t, root, "commit", "-qm", "seed")
	calls := recordRaise(t)
	parkRow(t, root, "XR-004", "слияние: XR-005")
	note := filepath.Join(root, "docs", "tasks", "XR-005.md")
	if err := os.WriteFile(note, []byte("# XR-005\nход работы\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wakeGit(t, root, "add", note)
	wakeGit(t, root, "commit", "-qm", "docs(tasks): XR-005 в работу")
	out, failed, err := cmdWake(root, nil, wakeOpts{})
	if err != nil || failed || len(*calls) != 0 {
		t.Fatalf("строка поднята до слияния кода: %v %v %+v\n%s", err, failed, *calls, out)
	}
	if !strings.Contains(out, "ждут событием 1, событие случилось у 0") {
		t.Fatalf("обход не назвал ждущую: %s", out)
	}
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wakeGit(t, root, "add", "code.go")
	wakeGit(t, root, "commit", "-qm", "feat(x): XR-005 правка")
	out, failed, err = cmdWake(root, nil, wakeOpts{})
	if err != nil || failed {
		t.Fatalf("обход упал: %v %v\n%s", err, failed, out)
	}
	if len(*calls) != 1 || (*calls)[0] != (raiseCall{ID: "XR-004", Hidden: true}) {
		t.Fatalf("жду подъём XR-004 после слияния, а было %+v\n%s", *calls, out)
	}
	if got := sectOf(t, root, "XR-004"); got != SectInProgress {
		t.Fatalf("XR-004 в %s после подъёма", got)
	}
	if !strings.Contains(out, "XR-005 слита") {
		t.Fatalf("отчёт не называет событие: %s", out)
	}
	// Слитую предпосылку ждать уже нечего.
	if _, err := cmdMove(root, "XR-001", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-001", SectBlocked, "слияние: XR-005", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "уже случилось") {
		t.Fatalf("парковка на слитую предпосылку должна падать, а пришло %v", err)
	}
}

// DoD: событие, прошедшее мимо close, добирает обход тика. Строку предпосылки
// увезли в архив правкой доски руками, и `taskctl wake` поднимает ждущую.
func TestWakeCatchesMissedClose(t *testing.T) {
	root := setup(t)
	calls := recordRaise(t)
	parkRow(t, root, "XR-004", "закрытие: XR-002")
	archiveByHand(t, root, "XR-002")
	out, failed, err := cmdWake(root, nil, wakeOpts{quiet: true})
	if err != nil || failed {
		t.Fatalf("обход упал: %v\n%s", err, out)
	}
	if len(*calls) != 1 || (*calls)[0].ID != "XR-004" {
		t.Fatalf("пропущенное закрытие не добрано: %+v\n%s", *calls, out)
	}
}

// Предполёт отказал: строка стоит в Blocked, подъёма нет, обход называет
// причину и отдаёт признак провала, по нему тик повторит заход.
func TestWakePreflightKeepsRowParked(t *testing.T) {
	root := setup(t)
	calls := recordRaise(t)
	old := wakePreflight
	wakePreflight = func(root, id string, o runOpts) error {
		return fmt.Errorf("клиент claude не нашёлся в PATH")
	}
	t.Cleanup(func() { wakePreflight = old })
	parkRow(t, root, "XR-004", "закрытие: XR-002")
	archiveByHand(t, root, "XR-002")
	out, failed, err := cmdWake(root, nil, wakeOpts{})
	if err != nil || !failed {
		t.Fatalf("жду признак провала без ошибки: %v %v\n%s", err, failed, out)
	}
	if len(*calls) != 0 || sectOf(t, root, "XR-004") != SectBlocked {
		t.Fatalf("строка сдвинута при отказе предполёта: %+v\n%s", *calls, out)
	}
	if !strings.Contains(out, "голову поднять нечем") || !strings.Contains(out, "клиент claude") {
		t.Fatalf("отказ не назван словами: %s", out)
	}
}

// Названный ID поднимает вопрос с лежащим ответом, так его будит тик. Признак
// ожидания снимается, а голова поднимается не скрытой: её начал ответ человека.
func TestWakeNamedQuestion(t *testing.T) {
	root := setup(t)
	calls := recordRaise(t)
	parkRow(t, root, "XR-004", "вопрос: нужна схема")
	ask := chat.AskPath(root, chat.TaskName("XR-004"))
	if err := os.MkdirAll(filepath.Dir(ask), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ask, []byte("-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, failed, err := cmdWake(root, []string{"xr-004"}, wakeOpts{})
	if err != nil || failed {
		t.Fatalf("подъём по ID упал: %v\n%s", err, out)
	}
	if len(*calls) != 1 || (*calls)[0] != (raiseCall{ID: "XR-004", Hidden: false}) {
		t.Fatalf("жду открытый подъём XR-004, а было %+v", *calls)
	}
	if _, err := os.Stat(ask); !os.IsNotExist(err) {
		t.Fatal("признак ожидания пережил пробуждение")
	}
	if sectOf(t, root, "XR-004") != SectInProgress {
		t.Fatal("строка осталась в Blocked")
	}
	if !strings.Contains(out, "ответ человека лежит во входе") {
		t.Fatalf("отчёт не называет повод: %s", out)
	}
}

// Строка цели выходит из Blocked, а голова задачи поверх её цикла не встаёт.
func TestWakeGoalRowOnlyUnparks(t *testing.T) {
	root := setup(t)
	calls := recordRaise(t)
	replaceInBoard(t, root, "| XR-004 | Хвост |", "| XR-004 | Цель: хвост |")
	parkRow(t, root, "XR-004", "закрытие: XR-002")
	archiveByHand(t, root, "XR-002")
	out, failed, err := cmdWake(root, nil, wakeOpts{})
	if err != nil || failed {
		t.Fatalf("обход упал: %v\n%s", err, out)
	}
	if len(*calls) != 0 || sectOf(t, root, "XR-004") != SectInProgress || !strings.Contains(out, "это цель") {
		t.Fatalf("цель жду снятой с парковки без головы: %+v\n%s", *calls, out)
	}
}

// Будить некого: с --quiet обход молчит, без него говорит словами. Кривая
// причина, поправленная руками мимо move, идёт строкой отчёта, а не падением.
func TestWakeQuietAndBrokenReason(t *testing.T) {
	root := setup(t)
	if out, failed, err := cmdWake(root, nil, wakeOpts{quiet: true}); out != "" || failed || err != nil {
		t.Fatalf("тихий обход без ждущих заговорил: %q %v %v", out, failed, err)
	}
	if out, _, _ := cmdWake(root, nil, wakeOpts{}); out != "ждущих событием нет" {
		t.Fatalf("обход без ждущих молчит: %q", out)
	}
	if _, _, err := cmdWake(root, nil, wakeOpts{push: true}); err == nil {
		t.Fatal("--push без коммита должен падать")
	}
	parkRow(t, root, "XR-004", "окружение: стенд")
	replaceInBoard(t, root, "[блок: окружение: стенд]", "[блок: слияние: потом]")
	out, failed, err := cmdWake(root, nil, wakeOpts{quiet: true})
	if err != nil || failed || !strings.Contains(out, "XR-004: разряд «слияние:» ждёт ID") {
		t.Fatalf("кривая причина не названа: %v %v %q", err, failed, out)
	}
}

// DK-932, замечание ревью: обход поднимает голову той же лестницей, что run, и
// модель головы берёт тем же вердиктом agentctl pick. До правки вопрос,
// разбуженный тиком, стартовал клиента без модели.
func TestWakeRaisesWithPickedModel(t *testing.T) {
	root := setup(t)
	_, _, dk := runDevkit(t)
	parkRow(t, root, "XR-004", "вопрос: нужна схема")
	logs, _ := pickStand(t, dk, "sonnet")
	old := raiseHead
	raiseHead = func(root, id string, o runOpts) (string, int, error) { return cmdRun(root, id, o) }
	t.Cleanup(func() { raiseHead = old })
	out, _, err := cmdWake(root, []string{"XR-004"}, wakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	runner := readStub(logs, "runner.log")
	if !strings.Contains(runner, "XR-004 -C ") || !strings.Contains(runner, "-- claude --permission-mode auto --model sonnet") {
		t.Fatalf("голова вопроса поднята без модели вердикта:\n%s\nотчёт:\n%s", runner, out)
	}
	if pick := strings.TrimSpace(readStub(logs, "pick.log")); pick != "claude-code pick XR-004 --record" {
		t.Fatalf("вердикт спрошен не так: %q", pick)
	}
}

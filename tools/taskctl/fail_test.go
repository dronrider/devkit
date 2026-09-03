package main

import (
	"os"
	"strings"
	"testing"
)

// title достаёт заголовок строки задачи с доски, включая суффиксы.
func title(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find(id)
	if row == nil {
		t.Fatalf("%s нет на доске", id)
	}
	return row.Title
}

func sectOf(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find(id)
	if row == nil {
		t.Fatalf("%s нет на доске", id)
	}
	return row.Sect
}

// toCheck доводит задачу до Check, откуда её и проверяют.
func toCheck(t *testing.T, root, id string) {
	t.Helper()
	if _, err := cmdMove(root, id, SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

func TestFailReturnsToWorkWithMark(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	msg, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "прод отдаёт 500 на входе"})
	if err != nil {
		t.Fatal(err)
	}
	if sect := sectOf(t, root, "XR-005"); sect != SectInProgress {
		t.Fatalf("после провала задача в %s, ждал In progress", sect)
	}
	if got := title(t, root, "XR-005"); got != "Задача в работе [провал: прод отдаёт 500 на входе]" {
		t.Fatalf("заголовок после провала: %q", got)
	}
	if !strings.Contains(msg, "shipctl revert XR-005") {
		t.Fatalf("в ответе нет способа починить прод: %q", msg)
	}
	if !strings.Contains(msg, "ревью шло без уровня") {
		t.Fatalf("в ответе нет отметки, что ревью прошло без уровня: %q", msg)
	}
}

// TestFailPrintsReviewLevel: провал называет уровень ревью и причину его
// выбора из строки уровня (DK-731), а не только сам факт провала.
func TestFailPrintsReviewLevel(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdReviewLevel(root, "XR-005", 2, "неопределённость 1, тронут tools/shipctl", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	// XR-005 в фикстуре уже стоит в In progress: провал берёт задачу и оттуда,
	// а полный ход через Check тут не при чём, дорогу ему сторожит обкатка.
	msg, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "прод отдаёт 500 на входе"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ревью было уровня 2", "неопределённость 1, тронут tools/shipctl"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("в ответе нет %q: %q", want, msg)
		}
	}
}

// TestFailWithoutReason и TestFailRejectsBracketInReason: причина обязательна
// и живёт в заголовке, поэтому скобка в ней запрещена так же, как у блокировки.
func TestFailWithoutReason(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005"}); err == nil {
		t.Fatal("провал без --reason должен падать")
	}
	if got := title(t, root, "XR-005"); got != "Задача в работе" {
		t.Fatalf("заголовок тронут отбитой командой: %q", got)
	}
}

func TestFailRejectsBracketInReason(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	before, _ := os.ReadFile(boardPath(root))
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "упал [DK-5]"}); err == nil {
		t.Fatal("причина со скобкой должна падать")
	}
	after, _ := os.ReadFile(boardPath(root))
	if string(before) != string(after) {
		t.Fatalf("доска изменилась после отбитого fail:\n%s", after)
	}
}

func TestFailTwiceRejected(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "500 на входе"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "ещё раз"}); err == nil {
		t.Fatal("второй провал поверх непогашенного должен падать")
	}
	if got := title(t, root, "XR-005"); got != "Задача в работе [провал: 500 на входе]" {
		t.Fatalf("заголовок после второго провала: %q", got)
	}
}

// TestFailFromBacklogRejected: провал это исход проверки, а проверяют задачу
// из Check (или уже вернувшуюся в работу), не из бэклога.
func TestFailFromBacklogRejected(t *testing.T) {
	root := setup(t)
	if _, err := cmdFail(root, FailParams{ID: "XR-004", Reason: "500 на входе"}); err == nil {
		t.Fatal("провал задачи из Backlog должен падать")
	}
}

func TestFailClearRemovesMark(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "500 на входе"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Clear: true}); err != nil {
		t.Fatal(err)
	}
	if got := title(t, root, "XR-005"); got != "Задача в работе" {
		t.Fatalf("заголовок после снятия признака: %q", got)
	}
	if sect := sectOf(t, root, "XR-005"); sect != SectInProgress {
		t.Fatalf("снятие признака увело задачу в %s", sect)
	}
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Clear: true}); err == nil {
		t.Fatal("снятие несуществующего признака должно падать")
	}
}

// TestMoveToCheckQuenchesFail: признак гасится сам, когда задача снова уходит
// на проверку. Этим же move переводят в Check shipctl merge и ship, поэтому
// после починки прода дочищать руками нечего.
func TestMoveToCheckQuenchesFail(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "500 на входе"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := title(t, root, "XR-005"); got != "Задача в работе" {
		t.Fatalf("признак провала пережил перевод в Check: %q", got)
	}
	if !strings.Contains(msg, "признак провала снят") {
		t.Fatalf("погашение прошло молча: %q", msg)
	}
}

// TestMoveOutOfCheckKeepsTitle: приёмка с замечаниями это обычный возврат в
// работу, он ничего к заголовку не приписывает и очередь не трогает.
func TestMoveOutOfCheckKeepsTitle(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdMove(root, "XR-005", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := title(t, root, "XR-005"); got != "Задача в работе" {
		t.Fatalf("обычный возврат тронул заголовок: %q", got)
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска после круга Check и обратно разъехалась с исходной:\n%s", board)
	}
}

// TestFailMarkSurvivesEdits: пометка живёт в заголовке рядом с зависимостью и
// блокировкой, и правки строки не должны её терять.
func TestFailMarkSurvivesEdits(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "500 на входе"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-005", Title: "Новый заголовок"}); err != nil {
		t.Fatal(err)
	}
	if got := title(t, root, "XR-005"); got != "Новый заголовок [провал: 500 на входе]" {
		t.Fatalf("set --title потерял признак провала: %q", got)
	}
	if _, err := cmdMove(root, "XR-005", SectBlocked, "ждём ответ хостера", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := title(t, root, "XR-005"); got != "Новый заголовок [провал: 500 на входе] [блок: ждём ответ хостера]" {
		t.Fatalf("блокировка съела признак провала: %q", got)
	}
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Clear: true}); err != nil {
		t.Fatal(err)
	}
	if got := title(t, root, "XR-005"); got != "Новый заголовок [блок: ждём ответ хостера]" {
		t.Fatalf("снятие признака задело причину блокировки: %q", got)
	}
}

// TestCloseRejectsFailed: закрытая задача уходит с доски, и вместе с ней
// пропал бы единственный след сломанного прода.
func TestCloseRejectsFailed(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "500 на входе"}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdClose(root, CloseParams{ID: "XR-005"})
	if err == nil {
		t.Fatal("закрытие задачи с непогашенным провалом должно падать")
	}
	if !strings.Contains(err.Error(), "taskctl fail XR-005 --clear") {
		t.Fatalf("в отказе нет ручного способа снять признак: %v", err)
	}
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Clear: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005"}); err != nil {
		t.Fatalf("после погашения закрытие должно проходить: %v", err)
	}
}

// TestLintFindsStrayFailMark: пометка в Backlog значит, что задачу отложили
// вместе со сломанным продом, и очередь выката стоит непонятно из-за чего.
func TestLintFindsStrayFailMark(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title += " [провал: 500 на входе]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, f := range finds {
		if strings.Contains(f, "XR-004") && strings.Contains(f, "провал") {
			found = f
		}
	}
	if found == "" {
		t.Fatalf("lint не заметил признак провала в Backlog: %v", finds)
	}
}

// TestFailNotifiesLoud: сломанный прод это повод подойти к машине, не меньший
// чем блокер (RULES.board.md, «Ветки, ревью и деплой» п. 8).
func TestFailNotifiesLoud(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	calls := writeNotifyStub(t, root, 0)
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "500 на входе"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("уведомитель не позван: %v", err)
	}
	line := strings.TrimSpace(string(got))
	if !strings.Contains(line, "XR-005") || !strings.Contains(line, "провал проверки") {
		t.Fatalf("заголовок уведомления не про провал: %q", line)
	}
	if !strings.HasPrefix(line, "--reason\ttask_fail\t") {
		t.Fatalf("уведомление ушло без повода task_fail: %q", line)
	}
	if !strings.Contains(line, "--task\tXR-005\t") {
		t.Fatalf("уведомление ушло без поля задачи: %q", line)
	}
	if !strings.HasSuffix(line, "500 на входе") {
		t.Fatalf("в теле нет причины провала: %q", line)
	}
}

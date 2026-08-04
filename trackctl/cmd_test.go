package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// take это переход плюс assign: тикет In progress без исполнителя в чужом
// процессе заметен сразу, поэтому одно без другого не считается.
func TestTakeTransitionsAndAssigns(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"fetch ABC-12",
		"transition ABC-12 In Progress [assignee=ivanov reason=плановая работа]",
		"assign ABC-12 ivanov",
	}
	if !reflect.DeepEqual(fakeState.calls, want) {
		t.Fatalf("след вызовов:\n%v\nжду:\n%v", fakeState.calls, want)
	}
	if !strings.Contains(msg, "«Open» -> «In Progress»") || !strings.Contains(msg, "ivanov") {
		t.Fatalf("вывод не рассказал, что сделано: %q", msg)
	}
}

// Один номер вместо ключа: префикс в проекте всё равно один, он берётся из
// привязки.
func TestTakeExpandsBareNumber(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	if _, err := cmdTake(root, "12"); err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "fetch ABC-12") {
		t.Fatalf("номер не развернулся в ключ: %v", fakeState.calls)
	}
}

// Секции без [fields_*] отдают переходу пустой набор: чего в секции нет, то и
// не передаётся, отказ трекера остаётся честным отказом.
func TestTakeSendsNoFieldsWithoutSection(t *testing.T) {
	text := strings.Replace(contourFile, "[fields_in_progress]\nassignee = \"{user}\"\nreason = \"плановая работа\"\n", "", 1)
	root := setupEnv(t, text, bindingFile)
	if _, err := cmdTake(root, "ABC-12"); err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "transition ABC-12 In Progress []") {
		t.Fatalf("незаданное поле подставлено само: %v", fakeState.calls)
	}
}

// Статуса нет ни в одном списке контура: это находка, а не ошибка разбора, и
// ни тикет, ни строка доски не двигаются.
func TestTakeUnknownStatusIsFinding(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	fakeState.ticket.Status = "Waiting for QA"
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "находка:") || !strings.Contains(msg, "Waiting for QA") {
		t.Fatalf("жду находку со статусом: %q", msg)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("после находки ничего не двигается, вижу %v", fakeState.calls)
	}
}

// Тикет в конечном статусе даёт подсказку про закрытие доски, а не находку:
// без своей секции done каждый закрытый тикет шумел бы «статус не расписан».
func TestTakeDoneHints(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	fakeState.ticket.Status = "Rejected"
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "taskctl close") {
		t.Fatalf("жду подсказку про закрытие: %q", msg)
	}
	if strings.Contains(msg, "находка") {
		t.Fatalf("конечный статус это не находка: %q", msg)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("в done автоматика не пишет, вижу %v", fakeState.calls)
	}
}

// Прямого перехода нет: адаптер отказывает и перечисляет доступные, а путь
// через промежуточные статусы никто не ищет.
func TestTakeRefusalListsAvailable(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	fakeState.available = []string{"Analysis", "Rejected"}
	_, err := cmdTake(root, "ABC-12")
	if err == nil || !strings.Contains(err.Error(), "Analysis, Rejected") {
		t.Fatalf("отказ обязан перечислить доступные переходы: %v", err)
	}
	if !strings.Contains(err.Error(), "синонимом") {
		t.Fatalf("отказ обязан назвать лекарство: %v", err)
	}
	if hasCall(fakeState, "assign") {
		t.Fatalf("после отказа перехода assign не зовётся: %v", fakeState.calls)
	}
}

// Тикет уже в работе: перехода нет, а исполнитель проставляется всё равно.
func TestTakeAlreadyInProgress(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	fakeState.ticket.Status = "Development"
	msg, err := cmdTake(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "transition") || !hasCall(fakeState, "assign ABC-12 ivanov") {
		t.Fatalf("жду assign без перехода: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "перехода не делаю") {
		t.Fatalf("вывод не объяснил отсутствие перехода: %q", msg)
	}
}

func TestIssueShowsBoardSection(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	fakeState.ticket = ticket{Status: "Testing", Type: "Bug", Title: "падает импорт", Estimate: "4h"}
	msg, err := cmdIssue(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ABC-12", "падает импорт", "Testing (секция доски: Check)", "Bug", "4h"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, msg)
		}
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("issue только читает, вижу %v", fakeState.calls)
	}
}

func TestIssueSaysFindingAndHint(t *testing.T) {
	for _, tc := range []struct{ name, status, want string }{
		{"неизвестный статус", "Waiting for QA", "находка:"},
		{"конечный статус", "Done", "taskctl close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupEnv(t, contourFile, bindingFile)
			fakeState.ticket.Status = tc.status
			msg, err := cmdIssue(root, "ABC-12")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("жду %q в выводе:\n%s", tc.want, msg)
			}
		})
	}
}

// Отсутствие необязательной операции это честное отсутствие оси: status
// называет её вслух вместе с тем, чего из-за неё не происходит.
func TestStatusNamesMissingOptional(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"corp", "fake", "ivanov", "ABC", "нет операции:\trank", "нет операции:\tcomment", "не гонялся ни разу", "токен:\tна месте"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, msg)
		}
	}
}

func TestStatusFullAdapterAndSyncAge(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	if err := os.WriteFile(filepath.Join(root, syncMarkPath), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "операции:\tвсе на месте") || strings.Contains(msg, "нет операции") {
		t.Fatalf("у полного адаптера отсутствующих осей нет:\n%s", msg)
	}
	if !strings.Contains(msg, "0 мин назад") {
		t.Fatalf("свежесть sync считается по отметке:\n%s", msg)
	}
}

// Наличие необязательной операции не повод по ней ходить: команды этой задачи
// шагов по rank и comment не делают ни при каком адаптере.
func TestCommandsDoNotTouchOptional(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	if _, err := cmdTake(root, "ABC-12"); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdIssue(root, "ABC-12"); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"rank", "comment", "estimate", "worklog"} {
		if hasCall(fakeState, op) {
			t.Fatalf("команда сходила в %s: %v", op, fakeState.calls)
		}
	}
}

func TestStatusSaysWhereContoursAre(t *testing.T) {
	root := setupEnv(t, contourFile, strings.Replace(bindingFile, "contour = corp", "contour = other", 1))
	_, err := cmdStatus(root)
	if err == nil || !strings.Contains(err.Error(), "есть контуры: corp") {
		t.Fatalf("отказ обязан назвать контуры машины: %v", err)
	}
}

// Журнал .devkit/log ведут все утилиты devkit: по нему видно, какие команды
// гоняются и как часто падают.
func TestLogRunWritesLine(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	logStart, logCmd = root, "take"
	t.Cleanup(func() { logStart, logCmd = "", "" })
	logRun(0)
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	if !strings.Contains(line, "\ttrackctl\ttake\t0") {
		t.Fatalf("строка журнала: %q", line)
	}
}

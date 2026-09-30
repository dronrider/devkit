package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

// draftCalls это след вызовов addDraft: подмена вместо живого taskctl тем же
// порядком, что captureReviewAdd у review и captureMoves у sync.
type draftCalls struct {
	texts []string
	prios []string
}

func captureDraft(t *testing.T, nextID string) *draftCalls {
	t.Helper()
	calls := &draftCalls{}
	saved := addDraft
	addDraft = func(root, text, prio string) (string, string, error) {
		calls.texts = append(calls.texts, text)
		calls.prios = append(calls.prios, prio)
		return nextID, fmt.Sprintf("%s: черновик в docs/tasks/drafts/%s.md, оформить: taskctl add --id %s", nextID, nextID, nextID), nil
	}
	t.Cleanup(func() { addDraft = saved })
	return calls
}

// Основной путь: тикет без строки на доске уезжает черновиком с постановкой и
// ссылкой, а сам тикет не трогается вовсе, как и у review.
func TestDraftPutsTicketIntoDraft(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureDraft(t, "XR-501")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "починить импорт конфига", Estimate: "1d",
		Description: "конфиг читается из чужого каталога"}

	msg, err := cmdDraft(root, "ABC-12", "mid")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("draft тронул трекер сверх fetch: %v", fakeState.calls)
	}
	if len(calls.texts) != 1 || calls.prios[0] != "mid" {
		t.Fatalf("уровень разбора не доехал до taskctl: %+v", calls)
	}
	text := calls.texts[0]
	first, rest, _ := strings.Cut(text, "\n")
	if first != "починить импорт конфига" {
		t.Fatalf("первая строка черновика не заголовок тикета: %q", first)
	}
	if !strings.Contains(rest, "конфиг читается из чужого каталога") {
		t.Fatalf("постановка тикета не попала в тело черновика:\n%s", text)
	}
	if !strings.Contains(rest, ticketLink("ABC-12")) {
		t.Fatalf("ссылки на тикет в тексте нет:\n%s", text)
	}
	if !strings.Contains(msg, "XR-501") {
		t.Fatalf("вывод не назвал заведённый черновик: %q", msg)
	}
	if !strings.Contains(msg, "--link") {
		t.Fatalf("вывод не сказал, чем ссылка встаёт в ячейку строки: %q", msg)
	}
}

// Тикет без описания это не повод отказывать: черновик заводится, а строка
// тела честно говорит, что постановку надо читать в тикете.
func TestDraftWithoutDescription(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureDraft(t, "XR-502")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "без описания"}

	if _, err := cmdDraft(root, "ABC-12", "low"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls.texts[0], "не дал описания") {
		t.Fatalf("тело черновика не сказало про пустую постановку:\n%s", calls.texts[0])
	}
}

// Заголовок тикета длиннее потолка формы: первая строка режется по слову,
// целиком заголовок остаётся в теле, и вывод про обрезку говорит вслух. Иначе
// taskctl отбил бы черновик отказом про потолок первой строки.
func TestDraftClipsLongTitle(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureDraft(t, "XR-503")
	long := strings.TrimSpace(strings.Repeat("длинный заголовок тикета ", 6))
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: long}

	msg, err := cmdDraft(root, "ABC-12", "high")
	if err != nil {
		t.Fatal(err)
	}
	first, rest, _ := strings.Cut(calls.texts[0], "\n")
	if n := len([]rune(first)); n > taskform.DraftTitleLimit {
		t.Fatalf("первая строка длиннее потолка формы (%d символов): %q", n, first)
	}
	if !strings.Contains(rest, long) {
		t.Fatalf("полный заголовок тикета не остался в теле:\n%s", calls.texts[0])
	}
	if !strings.Contains(msg, "обрезан") {
		t.Fatalf("вывод молчит про обрезку заголовка: %q", msg)
	}
}

// Строка тикета уже стоит на доске: черновик второй раз не заводится, команда
// называет стоящую строку и зовёт take.
func TestDraftFindsStandingRow(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", "L", ticketLink("ABC-12")})
	calls := captureDraft(t, "XR-504")

	msg, err := cmdDraft(root, "ABC-12", "mid")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.texts) != 0 {
		t.Fatalf("черновик заведён поверх стоящей строки: %+v", calls)
	}
	if !strings.Contains(msg, "XR-1") || !strings.Contains(msg, "take") {
		t.Fatalf("вывод не назвал стоящую строку и следующий шаг: %q", msg)
	}
}

// Уровень разбора обязателен, как у taskctl draft: без него черновик тонет в
// накопителе, и отказ говорит это словами до всякого разговора с трекером.
func TestDraftDemandsPrio(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	captureDraft(t, "XR-505")

	_, err := cmdDraft(root, "ABC-12", "")
	if err == nil {
		t.Fatal("черновик без уровня разбора прошёл")
	}
	if !strings.Contains(err.Error(), "high|mid|low") {
		t.Fatalf("отказ не назвал допустимые уровни: %v", err)
	}
	if len(fakeState.calls) != 0 {
		t.Fatalf("отказ по уровню разбора уже сходил в трекер: %v", fakeState.calls)
	}
}

// Отказ трекера (нет токена, нет тикета) едет отказом команды: черновика с
// пустой постановкой не заводится.
func TestDraftPassesTrackerRefusal(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureDraft(t, "XR-506")
	fakeState.fetchErr = fmt.Errorf("переменной окружения FAKE_TOKEN нет")

	if _, err := cmdDraft(root, "ABC-12", "mid"); err == nil {
		t.Fatal("отказ трекера не доехал до отказа команды")
	}
	if len(calls.texts) != 0 {
		t.Fatalf("черновик заведён на отказе трекера: %+v", calls)
	}
}

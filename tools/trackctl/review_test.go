package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// reviewCalls это след вызовов addReviewRow и setRowLink: подмена вместо
// живого taskctl тем же порядком, что captureMoves у sync (LLD DK-756,
// решение 1 держит trackctl отдельно от механики доски).
type reviewCalls struct {
	adds  []reviewRowParams
	links []string
}

// reviewSkeleton это болванка файла задачи, какой её кладёт настоящий
// `taskctl add --type task --accept mixed --barrier доступ`: заголовки формы
// и пустая причина барьера, которую дописывает fillReviewFile.
func reviewSkeleton(id, title string) string {
	return fmt.Sprintf(`# %s: %s

## Что происходит

## Чего хотим

## DoD

## Ранг

## Приёмка

- вид: mixed
- барьер «доступ»:

## Ход работы
`, id, title)
}

// captureReviewAdd подменяет addReviewRow и setRowLink записью вызовов:
// addReviewRow вдобавок кладёт файл задачи по образцу настоящего add, иначе
// fillReviewFile нечего было бы читать. Прогон не требует taskctl в PATH,
// тем же порядком, что captureMoves у sync.
func captureReviewAdd(t *testing.T, nextID string) *reviewCalls {
	t.Helper()
	calls := &reviewCalls{}
	savedAdd, savedLink := addReviewRow, setRowLink
	addReviewRow = func(root string, p reviewRowParams) (string, string, error) {
		calls.adds = append(calls.adds, p)
		abs := filepath.Join(root, "docs", "tasks", nextID+".md")
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(reviewSkeleton(nextID, p.title)), 0o644); err != nil {
			t.Fatal(err)
		}
		return nextID, fmt.Sprintf("%s заведена в Backlog: P3, R=3", nextID), nil
	}
	setRowLink = func(root, id, link string) error {
		calls.links = append(calls.links, id+" "+link)
		return nil
	}
	t.Cleanup(func() { addReviewRow, setRowLink = savedAdd, savedLink })
	return calls
}

func reviewFile(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Основной путь: чужой тикет без стоящей строки заводит зеркальную строку
// ревью и файл задачи, тикет при этом не трогается вовсе. Адаптер тут plain
// fake, у него нет mergeRequests (как у живой jira.go сегодня), поэтому MR
// строка просит ссылку флагом.
func TestReviewCreatesRowAndFile(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureReviewAdd(t, "XR-777")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "чужая задача", Estimate: "1d", Description: "нужно поправить импорт"}

	msg, err := cmdReview(root, "ABC-12", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("review тронул трекер сверх fetch: %v", fakeState.calls)
	}
	if len(calls.adds) != 1 || calls.adds[0].title != "Ревью ABC-12: чужая задача" {
		t.Fatalf("заголовок строки не по пометке сценария: %+v", calls.adds)
	}
	if calls.adds[0].cost != "M" {
		t.Fatalf("цена по оценке тикета 1d (cost_m) не сошлась: %+v", calls.adds[0])
	}
	wantLink := "XR-777 [ABC-12](https://tracker.example/browse/ABC-12)"
	if len(calls.links) != 1 || calls.links[0] != wantLink {
		t.Fatalf("ссылка строки не на тикет:\nхочу %q\nимею %v", wantLink, calls.links)
	}
	text := reviewFile(t, root, "XR-777")
	if !strings.Contains(text, "нужно поправить импорт") {
		t.Fatalf("постановка тикета не попала в «Что происходит»:\n%s", text)
	}
	if !strings.Contains(text, "барьер «доступ»: публикация в живой трекер требует токена") {
		t.Fatalf("причина барьера не дописана:\n%s", text)
	}
	if !strings.Contains(msg, "XR-777") {
		t.Fatalf("вывод не назвал заведённую строку: %q", msg)
	}
	if !strings.Contains(msg, "--mr") {
		t.Fatalf("вывод не попросил ссылку на MR руками: %q", msg)
	}
}

// Повторный вызов находит стоящую строку ревью по ссылке на тикет и пометке
// сценария, дубля не заводит и adapter не трогает вовсе.
func TestReviewIdempotentFindsExistingRow(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	corpWrite(t, filepath.Join(root, boardPath), reviewBoardText(
		boardRowText{sectBacklog, "XR-777", "3 (0+0+1+0+2)", "L", ticketLink("ABC-12")},
		"Ревью ABC-12: чужая задача",
	))
	calls := captureReviewAdd(t, "XR-778")

	msg, err := cmdReview(root, "ABC-12", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.adds) != 0 || len(calls.links) != 0 {
		t.Fatalf("повторный вызов не обязан заводить строку: %+v %v", calls.adds, calls.links)
	}
	if !reflect.DeepEqual(fakeState.calls, []string(nil)) {
		t.Fatalf("повторный вызов не обязан ходить в трекер: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "XR-777") {
		t.Fatalf("вывод не назвал стоящую строку: %q", msg)
	}
}

// Строка take того же тикета не сходит за строку ревью: признака нужно два,
// ссылка на тикет одна у обеих, пометка сценария есть только у ревью.
func TestReviewDoesNotConfuseWithTakeRow(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root, boardRowText{sectInProgress, "XR-005", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	calls := captureReviewAdd(t, "XR-777")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "чужая задача", Estimate: "1d"}

	if _, err := cmdReview(root, "ABC-12", ""); err != nil {
		t.Fatal(err)
	}
	if len(calls.adds) != 1 {
		t.Fatalf("строка take принята за строку ревью, новая не заведена: %+v", calls.adds)
	}
}

// reviewBoardText кладёт доску с одной обычной строкой и строкой с названным
// заголовком: writeBoard заголовок не параметризует (у него все строки
// «зеркало тикета»), а тесты ревью как раз про заголовок.
func reviewBoardText(row boardRowText, title string) string {
	return fmt.Sprintf(`# доска

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| %s | %s | task | P3 | %s | %s | %s |
`, row.id, title, row.rank, row.cost, row.link)
}

// MR ищется по ветке, когда адаптер операцию умеет (тут fake-full, живой
// пример был бы GitLab). Ровно одна ссылка уезжает и в строку доски, и в файл
// задачи.
func TestReviewFindsSingleMR(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	captureReviewAdd(t, "XR-777")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "чужая задача", Estimate: "4h"}
	fakeState.mrURLs = []string{"https://gitlab.example/proj/-/merge_requests/9"}

	msg, err := cmdReview(root, "ABC-12", "")
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "mr ABC-12-") {
		t.Fatalf("поиск MR не пошёл по префиксу ветки: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "merge_requests/9") {
		t.Fatalf("вывод не назвал найденный MR: %q", msg)
	}
	text := reviewFile(t, root, "XR-777")
	if !strings.Contains(text, "MR: https://gitlab.example/proj/-/merge_requests/9") {
		t.Fatalf("ссылка на MR не попала в файл:\n%s", text)
	}
}

// MR не нашёлся: отказ с просьбой дать ссылку, строка не заводится.
func TestReviewNoMRRefuses(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	calls := captureReviewAdd(t, "XR-777")

	_, err := cmdReview(root, "ABC-12", "")
	if err == nil || !strings.Contains(err.Error(), "--mr") {
		t.Fatalf("жду отказ с просьбой дать --mr, получил %v", err)
	}
	if len(calls.adds) != 0 {
		t.Fatalf("строка не должна заводиться без MR: %+v", calls.adds)
	}
}

// MR нашёлся не один: отказ с перечнем и просьбой дать --mr, строка не
// заводится.
func TestReviewMultipleMRRefuses(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	calls := captureReviewAdd(t, "XR-777")
	fakeState.mrURLs = []string{"https://gitlab.example/mr/1", "https://gitlab.example/mr/2"}

	_, err := cmdReview(root, "ABC-12", "")
	if err == nil || !strings.Contains(err.Error(), "mr/1") || !strings.Contains(err.Error(), "mr/2") || !strings.Contains(err.Error(), "--mr") {
		t.Fatalf("жду отказ с перечнем обеих ссылок и --mr, получил %v", err)
	}
	if len(calls.adds) != 0 {
		t.Fatalf("строка не должна заводиться при неоднозначном MR: %+v", calls.adds)
	}
}

// Флаг --mr перекрывает поиск целиком: адаптер про MR не спрашивается вовсе.
func TestReviewMRFlagSkipsSearch(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	calls := captureReviewAdd(t, "XR-777")
	fakeState.mrURLs = []string{"https://gitlab.example/mr/1", "https://gitlab.example/mr/2"}

	msg, err := cmdReview(root, "ABC-12", "https://gitlab.example/mr/руками")
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "mr ") {
		t.Fatalf("--mr обязан пропустить поиск, а адаптер спрошен: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "руками") {
		t.Fatalf("вывод не назвал ссылку из флага: %q", msg)
	}
	if len(calls.adds) != 1 {
		t.Fatalf("строка обязана завестись при данном флагом MR: %+v", calls.adds)
	}
}

// Оценки тикета нет ни одной ступени контура не совпало: цена остаётся «-» и
// об этом честно говорит вывод, а не молчит.
func TestReviewCostUnmapped(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureReviewAdd(t, "XR-777")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "чужая задача", Estimate: "2w"}

	msg, err := cmdReview(root, "ABC-12", "")
	if err != nil {
		t.Fatal(err)
	}
	if calls.adds[0].cost != "-" {
		t.Fatalf("не совпавшая оценка обязана дать цену «-»: %+v", calls.adds[0])
	}
	if !strings.Contains(msg, "не совпала") {
		t.Fatalf("вывод не объяснил, почему цена «-»: %q", msg)
	}
}

// Тип тикета Bug поднимает поправку на баг в разбивке ранга: единственное
// поле тикета, которое вообще читаемо для ранга.
func TestReviewRankBugCorrection(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	calls := captureReviewAdd(t, "XR-777")
	fakeState.ticket = ticket{Status: "Open", Type: "Bug", Title: "падает импорт", Estimate: "1d"}

	if _, err := cmdReview(root, "ABC-12", ""); err != nil {
		t.Fatal(err)
	}
	if calls.adds[0].rank != "0+0+1+5+2" {
		t.Fatalf("поправка на баг не пришла из типа тикета: %+v", calls.adds[0])
	}
}

// Даже у адаптера, который умеет rank/comment/update, review в тикет не
// пишет ни разу: шаг делается по факту команды, а не по наличию операции,
// тем же порядком, что и у TestCommandsDoNotTouchOptional для take и issue.
func TestReviewDoesNotTouchOptionalOps(t *testing.T) {
	root := setupEnv(t, strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1), bindingFile)
	captureReviewAdd(t, "XR-777")
	fakeState.ticket = ticket{Status: "Open", Type: "Task", Title: "чужая задача", Estimate: "1d"}
	fakeState.mrURLs = []string{"https://gitlab.example/mr/1"}

	if _, err := cmdReview(root, "ABC-12", ""); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"rank", "comment", "update", "transition", "assign", "estimate", "worklog"} {
		if hasCall(fakeState, op) {
			t.Fatalf("review сходил в %s: %v", op, fakeState.calls)
		}
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12", "mr ABC-12-"}) {
		t.Fatalf("review сходил в трекер сверх fetch и поиска MR: %v", fakeState.calls)
	}
}

// sync строку ревью по статусу тикета не двигает: у неё своя судьба (решение
// 7 LLD DK-756, часть про review).
func TestSyncDoesNotMoveReviewRow(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	corpWrite(t, filepath.Join(root, boardPath), reviewBoardText(
		boardRowText{sectBacklog, "XR-777", "3 (0+0+1+0+2)", "L", ticketLink("ABC-12")},
		"Ревью ABC-12: чужая задача",
	))
	moves := captureMoves(t)
	fakeState.ticket.Status = "Rejected"

	if _, err := cmdSync(root, false); err != nil {
		t.Fatal(err)
	}
	if len(*moves) != 0 {
		t.Fatalf("sync подвинул строку ревью: %v", *moves)
	}
}

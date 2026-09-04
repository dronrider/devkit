package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// errAPI это отказ трекера на стенде: живого API у прогона нет, а разбирать
// надо именно поведение опроса на отказе.
var errAPI = errors.New("curl: (7) не достучался до gl.example.com")

func readFileAt(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Опрос тредов чужого MR (LLD DK-756, решения 5, 6, 7 и 8). Живого трекера
// прогону не нужно: подпроцесс подменён, и стенд отвечает за GitLab сам.

// pollDraft кладёт журнал ревью с одним опубликованным блокирующим замечанием
// и одним неблокирующим: по ним и судятся события и судьба MR.
func pollDraft(t *testing.T, root string) {
	t.Helper()
	d := &reviewDraft{
		path:  reviewDraftAbs(root, "XR-005"),
		id:    "XR-005",
		MR:    "https://gl.example.com/group/proj/-/merge_requests/42",
		Sha:   "a1b2c3d",
		Level: "2",
		Blocks: []reviewBlock{
			{File: "tools/shipctl/ops.go", Line: "214", Label: reviewLabelIssue,
				State: reviewStatePublished, Thread: "d1", Text: "ворота merge не видят раздел"},
			{Label: reviewLabelSummary, State: reviewStatePublished, Thread: "d2", Text: "проверен живой путь"},
		},
	}
	if err := d.save(); err != nil {
		t.Fatal(err)
	}
}

// reviewRowTitle переписывает заголовок строки фикстуры в строку ревью: по
// пометке сценария ворклог узнаёт ключ чужого тикета.
func reviewRowTitle(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data), "Задача в работе", "Ревью ABC-12: чужая правка", 1)
	if err := os.WriteFile(boardPath(root), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// parkForAuthor паркует строку так, как её паркует ревьювер после публикации.
func parkForAuthor(t *testing.T, root string) {
	t.Helper()
	if _, err := cmdMove(root, "XR-005", SectBlocked, "автор: ждёт ответа в тредах MR", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

// stubPoll подменяет подпроцесс ответами GitLab: mr это тело ответа про сам
// MR, thread про каждый тред. Возврат это журнал команд.
func stubPoll(t *testing.T, mr, thread string) *[]string {
	t.Helper()
	var calls []string
	old := runPublish
	runPublish = func(script string) ([]byte, error) {
		calls = append(calls, script)
		if strings.Contains(script, "/discussions/") {
			return []byte(thread), nil
		}
		return []byte(mr), nil
	}
	t.Cleanup(func() { runPublish = old })
	return &calls
}

// stubTrackctl подменяет зов ворклога: журнал аргументов вместо живого trackctl.
func stubTrackctl(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := runTrackctl
	runTrackctl = func(root string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("ворклог: 2026-09-04 1h"), nil
	}
	t.Cleanup(func() { runTrackctl = old })
	return &calls
}

const (
	mrOpen   = `{"state":"opened","sha":"a1b2c3d"}`
	mrMoved  = `{"state":"opened","sha":"ff00ee1"}`
	mrMerged = `{"state":"merged","sha":"a1b2c3d"}`
	mrClosed = `{"state":"closed","sha":"a1b2c3d"}`

	threadQuiet    = `{"notes":[{"system":false,"resolved":false}]}`
	threadAnswered = `{"notes":[{"system":false,"resolved":false},{"system":false,"resolved":false}]}`
	threadResolved = `{"notes":[{"system":false,"resolved":true}]}`
)

func pollEnv(t *testing.T) (root, home string) {
	t.Helper()
	root = setupDraft(t)
	reviewRowTitle(t, root)
	pollDraft(t, root)
	parkForAuthor(t, root)
	return root, t.TempDir()
}

func poll(t *testing.T, root, home string, now time.Time) reviewPollVerdict {
	t.Helper()
	out, err := cmdReviewPoll(root, "XR-005", now, home, true, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return decodeVerdict(t, out)
}

func decodeVerdict(t *testing.T, out string) reviewPollVerdict {
	t.Helper()
	var v reviewPollVerdict
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("вердикт не разбирается: %v\n%s", err, out)
	}
	return v
}

func draftOf(t *testing.T, root string) *reviewDraft {
	t.Helper()
	d, err := loadReviewDraft(reviewDraftAbs(root, "XR-005"), "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// rowSect отличается от sectOf тем, что закрытая строка тут норма: судьба MR
// уносит строку ревью в архив, и это ожидаемый исход, а не провал стенда.
func rowSect(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find(id)
	if row == nil {
		return "архив"
	}
	return row.Sect
}

// Реплика автора в моём треде снимает парковку и ставит пометку. Пометку
// печатает `taskctl list`, и без неё второй круг виден только тому, кто
// откроет журнал ревью.
func TestReviewPollSeesAuthorReply(t *testing.T) {
	root, home := pollEnv(t)
	stubPoll(t, mrOpen, threadAnswered)
	v := poll(t, root, home, time.Now())
	if len(v.Events) == 0 || !strings.Contains(strings.Join(v.Events, " "), "реплика автора в треде d1") {
		t.Fatalf("события: %v", v.Events)
	}
	if got := rowSect(t, root, "XR-005"); got != SectInProgress {
		t.Fatalf("строка осталась в %s", got)
	}
	if got := draftOf(t, root).Mark; got != "автор ответил" {
		t.Fatalf("пометка: %q", got)
	}
}

// Резолв моего треда автором это то же событие: замечание принято, и второй
// круг начинается так же, как с реплики.
func TestReviewPollSeesResolve(t *testing.T) {
	root, home := pollEnv(t)
	stubPoll(t, mrOpen, threadResolved)
	v := poll(t, root, home, time.Now())
	if !strings.Contains(strings.Join(v.Events, " "), "автор закрыл тред d1") {
		t.Fatalf("события: %v", v.Events)
	}
	if got := rowSect(t, root, "XR-005"); got != SectInProgress {
		t.Fatalf("строка осталась в %s", got)
	}
}

// Новый коммит в ветке MR это событие и без единой реплики: автор чинит
// молча, и ревью обязано посмотреть на правку.
func TestReviewPollSeesNewCommit(t *testing.T) {
	root, home := pollEnv(t)
	stubPoll(t, mrMoved, threadQuiet)
	v := poll(t, root, home, time.Now())
	if !strings.Contains(strings.Join(v.Events, " "), "новый коммит") {
		t.Fatalf("события: %v", v.Events)
	}
}

// Опрошенная строка второй раз в сеть не идёт, пока не вышел шаг poll.
func TestReviewPollSkipsByStep(t *testing.T) {
	root, home := pollEnv(t)
	calls := stubPoll(t, mrOpen, threadQuiet)
	now := time.Now()
	poll(t, root, home, now)
	before := len(*calls)
	v := poll(t, root, home, now.Add(time.Minute))
	if !v.Skipped {
		t.Fatalf("шаг не сработал: %+v", v)
	}
	if len(*calls) != before {
		t.Fatalf("в трекер сходили лишний раз: %v", (*calls)[before:])
	}
	if !strings.Contains(v.text(), "шаг 5м ещё не вышел") {
		t.Fatalf("вывод не назвал шаг: %s", v.text())
	}
}

// Шаг правится ключом poll: пять минут это умолчание, а не закон.
func TestReviewPollStepFromConf(t *testing.T) {
	root, home := pollEnv(t)
	writeReviewConf(t, root, "poll = 30s\n")
	calls := stubPoll(t, mrOpen, threadQuiet)
	now := time.Now()
	poll(t, root, home, now)
	before := len(*calls)
	if v := poll(t, root, home, now.Add(time.Minute)); v.Skipped {
		t.Fatalf("шаг 30s не вышел за минуту: %+v", v)
	}
	if len(*calls) == before {
		t.Fatalf("в трекер не сходили: %v", *calls)
	}
}

// Молчание дольше порога это один зов человеку и пометка строке. Второй тик
// о том же молчании молчит: иначе уведомитель звонил бы каждые пять минут.
func TestReviewPollSilenceCallsOnce(t *testing.T) {
	root, home := pollEnv(t)
	writeReviewConf(t, root, "silence = 1d\n")
	stubPoll(t, mrOpen, threadQuiet)
	start := time.Now()
	if v := poll(t, root, home, start); v.Silence {
		t.Fatalf("молчание засчитано с первого опроса: %+v", v)
	}
	v := poll(t, root, home, start.Add(25*time.Hour))
	if !v.Silence || !v.Noise {
		t.Fatalf("молчание не замечено: %+v", v)
	}
	if got := draftOf(t, root).Mark; !strings.HasPrefix(got, "автор молчит с") {
		t.Fatalf("пометка: %q", got)
	}
	if again := poll(t, root, home, start.Add(50*time.Hour)); again.Silence {
		t.Fatalf("о том же молчании позвали второй раз: %+v", again)
	}
}

// Слитый MR закрывает строку: блоки снимаются припиской, пометка уходит в
// файл задачи, а открытое блокирующее это повод позвать человека.
func TestReviewPollClosesMergedMR(t *testing.T) {
	root, home := pollEnv(t)
	stubPoll(t, mrMerged, threadQuiet)
	v := poll(t, root, home, time.Now())
	if v.Fate != "слит" || !v.Noise {
		t.Fatalf("судьба MR: %+v", v)
	}
	if got := rowSect(t, root, "XR-005"); got != "архив" {
		t.Fatalf("строка осталась в %s", got)
	}
	arch := readFileAt(t, root, "docs/tasks/archive/"+time.Now().Format("2006")+"/XR-005.review.md")
	for _, want := range []string{"состояние: снято, тред d1, MR слит без апрува ревью", "пометка: MR слит без апрува ревью"} {
		if !strings.Contains(arch, want) {
			t.Errorf("в журнале ревью нет %q:\n%s", want, arch)
		}
	}
	task := readFileAt(t, root, "docs/tasks/archive/"+time.Now().Format("2006")+"/XR-005.md")
	if !strings.Contains(task, "MR слит без апрува ревью") {
		t.Errorf("в файле задачи нет пометки судьбы:\n%s", task)
	}
}

// Закрытый MR закрывает строку молча: шуметь тут не о чем, работа коллеги
// просто не поехала.
func TestReviewPollClosedMRIsQuiet(t *testing.T) {
	root, home := pollEnv(t)
	stubPoll(t, mrClosed, threadQuiet)
	v := poll(t, root, home, time.Now())
	if v.Fate != "закрыт" || v.Noise {
		t.Fatalf("судьба MR: %+v", v)
	}
	if got := rowSect(t, root, "XR-005"); got != "архив" {
		t.Fatalf("строка осталась в %s", got)
	}
}

// Ключ worklog = on пишет время ревью в чужой тикет при закрытии строки,
// умолчание off не пишет ничего.
func TestReviewPollWorklogByKey(t *testing.T) {
	for _, tc := range []struct {
		conf string
		want int
	}{{"worklog = on\n", 1}, {"", 0}} {
		root, home := pollEnv(t)
		if tc.conf != "" {
			writeReviewConf(t, root, tc.conf)
		}
		stubPoll(t, mrMerged, threadQuiet)
		calls := stubTrackctl(t)
		poll(t, root, home, time.Now())
		if len(*calls) != tc.want {
			t.Fatalf("конфиг %q: зовов ворклога %d, жду %d (%v)", tc.conf, len(*calls), tc.want, *calls)
		}
		if tc.want > 0 && !strings.Contains((*calls)[0], "submit ABC-12 --log-only") {
			t.Fatalf("ворклог позван не так: %v", *calls)
		}
	}
}

// Недоступный API оставляет строку в парковке, а находку тик слышит один раз:
// повтор той же находки идёт тихо («Нештат» LLD DK-756).
func TestReviewPollFindingSpeaksOnce(t *testing.T) {
	root, home := pollEnv(t)
	old := runPublish
	runPublish = func(string) ([]byte, error) { return nil, errAPI }
	t.Cleanup(func() { runPublish = old })
	now := time.Now()
	first := poll(t, root, home, now)
	if first.Finding == "" || first.Quiet {
		t.Fatalf("первая находка: %+v", first)
	}
	second := poll(t, root, home, now.Add(time.Hour))
	if !second.Quiet {
		t.Fatalf("та же находка сказана дважды: %+v", second)
	}
	if got := rowSect(t, root, "XR-005"); got != SectBlocked {
		t.Fatalf("строка ушла из парковки в %s", got)
	}
}

// Апрув при открытом блокирующем не ставится ни в каком режиме: это
// единственное место, где auto обязан остановиться.
func TestApproveMRRefusesOpenIssue(t *testing.T) {
	root, _ := pollEnv(t)
	writeReviewConf(t, root, "publish = auto\n")
	stubPoll(t, mrOpen, threadQuiet)
	_, err := cmdReviewApproveMR(root, "XR-005", false, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "блокирующих") {
		t.Fatalf("апрув поверх блокирующего: %v", err)
	}
}

// В режиме confirm апрув идёт после слова человека, и словом тут служит --yes.
func TestApproveMRConfirmNeedsWord(t *testing.T) {
	root, _ := pollEnv(t)
	d := draftOf(t, root)
	d.Blocks[0].State = reviewStateDropped
	if err := d.save(); err != nil {
		t.Fatal(err)
	}
	calls := stubPoll(t, mrOpen, threadQuiet)
	if _, err := cmdReviewApproveMR(root, "XR-005", false, CommitOpts{}); err == nil {
		t.Fatal("confirm поставил апрув без слова человека")
	}
	if len(*calls) != 0 {
		t.Fatalf("в трекер сходили до слова человека: %v", *calls)
	}
	if _, err := cmdReviewApproveMR(root, "XR-005", true, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "/approve") {
		t.Fatalf("апрув ушёл не так: %v", *calls)
	}
	if got := draftOf(t, root).Mark; got != "апрув поставлен" {
		t.Fatalf("пометка: %q", got)
	}
}

// В режиме auto апрув без открытых замечаний идёт сам, слова человека тут не
// спрашивают: режим для того и заведён.
func TestApproveMRAutoGoesAlone(t *testing.T) {
	root, _ := pollEnv(t)
	writeReviewConf(t, root, "publish = auto\n")
	d := draftOf(t, root)
	d.Blocks[0].State = reviewStateDropped
	if err := d.save(); err != nil {
		t.Fatal(err)
	}
	calls := stubPoll(t, mrOpen, threadQuiet)
	if _, err := cmdReviewApproveMR(root, "XR-005", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("зовов трекера %d: %v", len(*calls), *calls)
	}
}

// Пометка строки ревью печатается под строкой доски: по `taskctl list` видно,
// чего ждёт ревью, не открывая журнала.
func TestListShowsReviewMark(t *testing.T) {
	root, home := pollEnv(t)
	stubPoll(t, mrOpen, threadAnswered)
	poll(t, root, home, time.Now())
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "автор ответил") {
		t.Fatalf("в списке нет пометки:\n%s", out)
	}
}

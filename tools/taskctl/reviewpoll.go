package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// Опрос тредов чужого MR и судьба строки ревью (LLD DK-756, решения 5, 6, 7 и
// 8). Зовёт команду сторожок `devkitctl watch` по строкам, припаркованным
// причиной «автор: ...»: сама она решает, пора ли идти в трекер, читает треды
// и MR теми же запросами, что публикация, и двигает доску. Что делать с
// вердиктом дальше (разбудить живую сессию, позвать человека), решает
// сторожок: живых сессий и уведомителя утилита доски не знает.

// reviewPollDir это каталог машинного состояния опроса внутри ~/.devkit. В
// файл замечаний оно не идёт: отметка опроса меняется каждые пять минут, и
// коммитить её значило бы держать дерево доски грязным круглые сутки.
const reviewPollDir = "review"

// reviewState это то, что опрос помнит между прогонами.
type reviewState struct {
	path string
	// Polled это время последнего похода в трекер, по нему считается шаг.
	Polled string `json:"polled,omitempty"`
	// Waiting это начало ожидания автора, от него считается молчание.
	Waiting string `json:"waiting,omitempty"`
	// Nudged стоит, когда о молчании уже сказано: второго зова о том же
	// молчании не будет (DoD DK-759).
	Nudged string `json:"nudged,omitempty"`
	// Fail это последняя находка опроса. Повтор той же находки идёт тихо:
	// недоступный API иначе звал бы человека каждые пять минут («Нештат» LLD).
	Fail string `json:"fail,omitempty"`
	// Threads это счёт реплик и признак резолва по id треда.
	Threads map[string]reviewThreadState `json:"threads,omitempty"`
}

type reviewThreadState struct {
	Notes    int  `json:"notes"`
	Resolved bool `json:"resolved"`
}

func reviewStatePath(home, root, id string) string {
	return filepath.Join(home, ".devkit", reviewPollDir, id+"-"+stage.Slug(stage.MainRoot(root))+".json")
}

// loadReviewState читает состояние. Битый или пропавший файл это чистое
// состояние: опрос переживёт потерю памяти лишним походом в трекер, а вот
// отказом он остановил бы ревью насовсем.
func loadReviewState(path string) *reviewState {
	s := &reviewState{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, s); err != nil {
		return &reviewState{path: path}
	}
	s.path = path
	return s
}

func (s *reviewState) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}

func reviewStamp(t time.Time) string { return t.Format(time.RFC3339) }

func reviewTime(val string) (time.Time, bool) {
	if val == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// reviewPollVerdict это машинный ответ команды сторожку. Печатный вывод
// собирается из тех же полей, читает его человек.
type reviewPollVerdict struct {
	ID string `json:"id"`
	// Skipped значит, что шаг опроса ещё не вышел и в сеть команда не ходила.
	Skipped bool `json:"skipped"`
	// Events это события, снявшие парковку: реплика, резолв, новый коммит.
	Events []string `json:"events"`
	// Fate это судьба MR: «слит», «закрыт» или пусто.
	Fate string `json:"fate"`
	// Noise значит, что человека надо звать: слитый MR с открытыми
	// блокирующими либо молчание автора дольше порога.
	Noise   bool `json:"noise"`
	Silence bool `json:"silence"`
	// Finding это находка опроса (нет доступа к API, битый ответ), Quiet
	// значит, что о ней уже говорили прошлым прогоном.
	Finding string   `json:"finding"`
	Quiet   bool     `json:"quiet"`
	Lines   []string `json:"lines"`
}

func (v reviewPollVerdict) text() string { return strings.Join(v.Lines, "\n") }

// mrView это ответ трекера про сам MR, сведённый к трём полям. Формы у
// GitLab, GitHub и Gitea разные, а нужно от них одно и то же: жив ли MR и
// какой коммит у него на голове.
type mrView struct {
	State  string `json:"state"`
	Merged bool   `json:"merged"`
	Sha    string `json:"sha"`
	Head   struct {
		Sha string `json:"sha"`
	} `json:"head"`
}

// fate переводит состояние MR в судьбу строки: пусто значит «MR ещё живой».
func (v mrView) fate() string {
	switch {
	case v.Merged || v.State == "merged":
		return "слит"
	case v.State == "closed":
		return "закрыт"
	}
	return ""
}

func (v mrView) headSha() string {
	if v.Sha != "" {
		return v.Sha
	}
	return v.Head.Sha
}

// mrScript собирает запрос про сам MR. Формы те же, что у публикации: свой
// клиент трекера devkit не заводит, а curl и gh есть на любой машине.
func mrScript(t mrTarget) string {
	switch t.Kind {
	case "github":
		return fmt.Sprintf("gh api %s", shQuote(fmt.Sprintf("repos/%s/pulls/%s", t.Project, t.Number)))
	case "gitea":
		return fmt.Sprintf(`curl -sS --header "Authorization: token $%s" %s`, t.TokenEnv,
			shQuote(fmt.Sprintf("%s/api/v1/repos/%s/pulls/%s", t.Host, t.Project, t.Number)))
	}
	return fmt.Sprintf(`curl -sS --header "PRIVATE-TOKEN: $%s" %s`, t.TokenEnv,
		shQuote(fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s", t.Host, url.PathEscape(t.Project), t.Number)))
}

func fetchMR(t mrTarget) (mrView, error) {
	out, err := runPublish(mrScript(t))
	if err != nil {
		return mrView{}, fmt.Errorf("трекер не отдал MR %s: %v", t.Number, err)
	}
	var v mrView
	if err := json.Unmarshal(out, &v); err != nil {
		return mrView{}, fmt.Errorf("ответ трекера про MR %s не разбирается: %v", t.Number, err)
	}
	return v, nil
}

// gitlabThread это ответ про один тред: реплики и признак резолва. Своя
// реплика в треде первая, поэтому вторая и следующие это ответ автора.
type gitlabThread struct {
	Notes []struct {
		System   bool `json:"system"`
		Resolved bool `json:"resolved"`
	} `json:"notes"`
}

func threadScript(t mrTarget, id string) string {
	return fmt.Sprintf(`curl -sS --header "PRIVATE-TOKEN: $%s" %s`, t.TokenEnv,
		shQuote(fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s/discussions/%s",
			t.Host, url.PathEscape(t.Project), t.Number, url.PathEscape(id))))
}

// fetchThread отдаёт число живых реплик треда и признак резолва.
func fetchThread(t mrTarget, id string) (notes int, resolved bool, err error) {
	out, err := runPublish(threadScript(t, id))
	if err != nil {
		return 0, false, fmt.Errorf("трекер не отдал тред %s: %v", id, err)
	}
	var th gitlabThread
	if err := json.Unmarshal(out, &th); err != nil {
		return 0, false, fmt.Errorf("ответ трекера про тред %s не разбирается: %v", id, err)
	}
	for _, n := range th.Notes {
		if n.System {
			continue
		}
		notes++
		if n.Resolved {
			resolved = true
		}
	}
	return notes, resolved, nil
}

// reviewTicketRe достаёт ключ тикета из заголовка строки ревью: его пишет
// туда `trackctl review` пометкой сценария.
var reviewTicketRe = regexp.MustCompile(`^Ревью ([A-Za-z][A-Za-z0-9]*-\d+):`)

// runTrackctl зовёт trackctl подпроцессом: ворклог в чужой тикет пишет он, а
// не доска. Подменяется тестом, живого трекера прогону не нужно.
var runTrackctl = func(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("trackctl", append([]string{"-C", root}, args...)...)
	return cmd.CombinedOutput()
}

// reviewWorklog пишет время ревью в чужой тикет при закрытии строки (LLD
// DK-756, решение 8). Ключ `worklog = off` по умолчанию, и без него команда
// не зовётся вовсе. Провал записи не держит закрытие: строка ревью уже
// доработана, а неуехавшее время видно строкой отчёта.
func reviewWorklog(root, title string) string {
	m := reviewTicketRe.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil {
		return "ворклог не пишу: в заголовке строки нет ключа тикета"
	}
	out, err := runTrackctl(root, "submit", m[1], "--log-only")
	text := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Sprintf("ворклог %s не ушёл: %v (%s)", m[1], err, text)
	}
	return fmt.Sprintf("ворклог %s: %s", m[1], strings.Join(strings.Fields(text), " "))
}

// reviewMarkApproved это пометка строки после апрува: её же читает судьба MR.
const reviewMarkApproved = "апрув поставлен"

// reviewFateNote это строка в «Ход работы» файла задачи о судьбе MR: строка
// ревью уходит в архив, и по файлу видно, чем кончилось.
func reviewFateNote(fate string, approved bool) string {
	if fate == "слит" && !approved {
		return "MR слит без апрува ревью"
	}
	if fate == "слит" {
		return "MR слит"
	}
	return "MR закрыт"
}

// closeReviewRow доводит строку ревью до архива по судьбе MR: открытые блоки
// снимаются припиской, треды в чужом MR не трогаются (LLD DK-756, решение 7).
func closeReviewRow(root string, d *reviewDraft, row *Row, fate string, v *reviewPollVerdict, worklog bool, c CommitOpts) error {
	open := d.openIssues()
	// Апрув оставляет за собой ту же пометку, и по ней слитый MR отличается
	// от слитого мимо ревью: коллега мог влить и не дождавшись.
	note := reviewFateNote(fate, d.Mark == reviewMarkApproved)
	for i := range d.Blocks {
		if d.Blocks[i].published() {
			d.Blocks[i].State = reviewStateDropped
			d.Blocks[i].Note = note
		}
	}
	d.Mark = note
	if err := d.save(); err != nil {
		return err
	}
	if err := appendReviewStage(root, d.id, note); err != nil {
		return err
	}
	v.Fate = fate
	v.Lines = append(v.Lines, fmt.Sprintf("%s: %s, строка ревью закрывается, блоков снято %d", d.id, note, len(d.Blocks)))
	if fate == "слит" && open > 0 {
		v.Noise = true
		v.Lines = append(v.Lines, fmt.Sprintf("%s: MR слит, а блокирующих замечаний открыто %d: разбирается человек", d.id, open))
	}
	if worklog {
		v.Lines = append(v.Lines, reviewWorklog(root, row.Title))
	}
	out, err := cmdClose(root, CloseParams{ID: d.id, Commit: c})
	if err != nil {
		return err
	}
	v.Lines = append(v.Lines, strings.Split(strings.TrimSpace(out), "\n")...)
	return nil
}

// appendReviewStage дописывает строку о судьбе MR в «Ход работы» файла
// задачи. Файла может не быть у строки, заведённой руками, и это не отказ:
// пометка живёт ещё и в журнале ревью.
func appendReviewStage(root, id, note string) error {
	abs := taskFileAbs(root, id)
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	body := stage.InsertIntoSection(string(data), stageSection, "- "+note+", "+time.Now().Format("2006-01-02")+".")
	return os.WriteFile(abs, []byte(body), 0o644)
}

// cmdReviewPoll это один заход опроса по одной строке ревью.
func cmdReviewPoll(root, id string, now time.Time, home string, asJSON bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	conf, err := loadReviewConf(root)
	if err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	d, err := openReviewDraft(root, id, false)
	if err != nil {
		return "", err
	}
	if d.MR == "" {
		return "", fmt.Errorf("в шапке %s нет ссылки на MR, опрашивать нечего", reviewDraftRel(id))
	}
	target, err := trackerFromMR(d.MR)
	if err != nil {
		return "", err
	}
	if home == "" {
		home = stage.Home()
	}
	st := loadReviewState(reviewStatePath(home, root, id))
	v := reviewPollVerdict{ID: id}
	if last, ok := reviewTime(st.Polled); ok && now.Sub(last) < conf.Poll {
		v.Skipped = true
		v.Lines = append(v.Lines, fmt.Sprintf("%s: опрошен %s назад, шаг %s ещё не вышел, в трекер не хожу",
			id, humanSpan(now.Sub(last)), humanSpan(conf.Poll)))
		return renderReviewPoll(v, asJSON)
	}
	changed, err := pollReviewRow(root, target, d, row, st, conf, now, &v, c)
	if err != nil {
		return "", err
	}
	st.Polled = reviewStamp(now)
	if err := st.save(); err != nil {
		return "", err
	}
	if !changed && len(v.Lines) == 0 {
		v.Lines = append(v.Lines, fmt.Sprintf("%s: автор молчит, парковка стоит", id))
	}
	return renderReviewPoll(v, asJSON)
}

// pollReviewRow ходит в трекер и разбирает ответ. Возврат говорит, тронуто ли
// что-нибудь на доске или в журнале ревью.
func pollReviewRow(root string, target mrTarget, d *reviewDraft, row *Row, st *reviewState,
	conf reviewConf, now time.Time, v *reviewPollVerdict, c CommitOpts) (bool, error) {
	mr, err := fetchMR(target)
	if err != nil {
		return false, reviewFinding(st, v, err)
	}
	if fate := mr.fate(); fate != "" {
		if err := closeReviewRow(root, d, row, fate, v, conf.Worklog, c); err != nil {
			return true, err
		}
		st.Fail = ""
		return true, nil
	}
	var events []string
	if head := mr.headSha(); head != "" && d.Sha != "" && !strings.HasPrefix(head, d.Sha) && !strings.HasPrefix(d.Sha, head) {
		events = append(events, fmt.Sprintf("новый коммит в ветке MR: %s", shortSha(head)))
	}
	if target.Kind == "gitlab" {
		if st.Threads == nil {
			st.Threads = map[string]reviewThreadState{}
		}
		for _, bl := range d.Blocks {
			if !bl.published() || bl.Thread == "" {
				continue
			}
			notes, resolved, err := fetchThread(target, bl.Thread)
			if err != nil {
				return false, reviewFinding(st, v, err)
			}
			was := st.Threads[bl.Thread]
			// Своя реплика в треде первая: пока их одна, автор не отвечал.
			seen := was.Notes
			if seen < 1 {
				seen = 1
			}
			if notes > seen {
				events = append(events, fmt.Sprintf("реплика автора в треде %s", bl.Thread))
			}
			if resolved && !was.Resolved {
				events = append(events, fmt.Sprintf("автор закрыл тред %s", bl.Thread))
			}
			st.Threads[bl.Thread] = reviewThreadState{Notes: notes, Resolved: resolved}
		}
	} else {
		v.Lines = append(v.Lines, fmt.Sprintf("%s: реплики в тредах у %s опросом не читаются, вижу судьбу MR и новые коммиты",
			d.id, target.Kind))
	}
	st.Fail = ""
	if len(events) > 0 {
		return true, wakeReviewRow(root, d, row, events, st, v, c)
	}
	return silenceReviewRow(root, d, st, conf, now, v, c)
}

// wakeReviewRow снимает парковку и ставит пометку: второй круг дальше
// поднимает сторожок, доске остаётся строка в In progress.
func wakeReviewRow(root string, d *reviewDraft, row *Row, events []string, st *reviewState,
	v *reviewPollVerdict, c CommitOpts) error {
	v.Events = events
	d.Mark = "автор ответил"
	if err := d.save(); err != nil {
		return err
	}
	st.Waiting, st.Nudged = "", ""
	paths := []string{reviewDraftRel(d.id)}
	if row.Sect == SectBlocked {
		if _, err := cmdMove(root, d.id, SectInProgress, "", CommitOpts{}); err != nil {
			return err
		}
		paths = append(paths, filepath.Join("docs", "TASKS.md"))
		v.Lines = append(v.Lines, fmt.Sprintf("%s: парковка снята, строка вернулась в In progress", d.id))
	}
	v.Lines = append(v.Lines, fmt.Sprintf("%s: %s, пометка «автор ответил»", d.id, strings.Join(events, "; ")))
	tail, err := c.apply(root, paths)
	if err != nil {
		return err
	}
	if tail != "" {
		v.Lines = append(v.Lines, strings.TrimPrefix(strings.TrimSpace(tail), ", "))
	}
	return nil
}

// silenceReviewRow считает молчание автора: дольше порога это один зов
// человеку и пометка строке, а не окрик на каждом шаге.
func silenceReviewRow(root string, d *reviewDraft, st *reviewState, conf reviewConf,
	now time.Time, v *reviewPollVerdict, c CommitOpts) (bool, error) {
	since, ok := reviewTime(st.Waiting)
	if !ok {
		st.Waiting = reviewStamp(now)
		return false, nil
	}
	if now.Sub(since) < conf.Silence || st.Nudged != "" {
		return false, nil
	}
	st.Nudged = reviewStamp(now)
	v.Silence = true
	v.Noise = true
	d.Mark = fmt.Sprintf("автор молчит с %s", since.Format("2006-01-02"))
	if err := d.save(); err != nil {
		return true, err
	}
	v.Lines = append(v.Lines, fmt.Sprintf("%s: автор молчит %s (порог %s), пора толкать коллегу",
		d.id, humanSpan(now.Sub(since)), humanSpan(conf.Silence)))
	tail, err := c.apply(root, []string{reviewDraftRel(d.id)})
	if err != nil {
		return true, err
	}
	if tail != "" {
		v.Lines = append(v.Lines, strings.TrimPrefix(strings.TrimSpace(tail), ", "))
	}
	return true, nil
}

// reviewFinding кладёт находку опроса в вердикт. Та же находка второй раз
// идёт тихо: недоступный API иначе звал бы человека каждые пять минут.
func reviewFinding(st *reviewState, v *reviewPollVerdict, err error) error {
	v.Finding = err.Error()
	v.Quiet = st.Fail == v.Finding
	st.Fail = v.Finding
	v.Lines = append(v.Lines, fmt.Sprintf("%s: %s", v.ID, v.Finding))
	return nil
}

func renderReviewPoll(v reviewPollVerdict, asJSON bool) (string, error) {
	if !asJSON {
		return v.text(), nil
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// humanSpan печатает срок так, как его пишут в конфиге: «5m», «2ч 10м».
func humanSpan(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%dс", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dм", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dч", int(d.Hours()))
	}
	return fmt.Sprintf("%dд", int(d.Hours())/24)
}

// approveScript собирает запрос апрува MR по трекеру из ссылки.
func approveScript(t mrTarget) (string, error) {
	switch t.Kind {
	case "gitlab":
		return fmt.Sprintf(`curl -sS --request POST --header "PRIVATE-TOKEN: $%s" %s`, t.TokenEnv,
			shQuote(fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s/approve",
				t.Host, url.PathEscape(t.Project), t.Number))), nil
	case "github":
		return fmt.Sprintf(`gh api %s -f event=APPROVE`,
			shQuote(fmt.Sprintf("repos/%s/pulls/%s/reviews", t.Project, t.Number))), nil
	case "gitea":
		return fmt.Sprintf(`curl -sS --request POST --header "Authorization: token $%s"`+
			` --header 'Content-Type: application/json' %s --data '{"event":"APPROVED"}'`, t.TokenEnv,
			shQuote(fmt.Sprintf("%s/api/v1/repos/%s/pulls/%s/reviews", t.Host, t.Project, t.Number))), nil
	}
	return "", fmt.Errorf("трекер %q не знаю", t.Kind)
}

// cmdReviewApproveMR ставит апрув в чужом MR. Открытое блокирующее замечание
// отбивает апрув в любом режиме, это единственное место, где `auto` обязан
// остановиться (LLD DK-756, решение 6). В режиме `confirm` апрув идёт после
// слова человека, и словом тут служит флаг --yes: спрашивает человека сам
// ревьювер последней репликой хода, а утилита сторожит, что вопрос был.
func cmdReviewApproveMR(root, id string, yes bool, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	conf, err := loadReviewConf(root)
	if err != nil {
		return "", err
	}
	d, err := openReviewDraft(root, id, false)
	if err != nil {
		return "", err
	}
	if d.MR == "" {
		return "", fmt.Errorf("в шапке %s нет ссылки на MR, апрув ставить негде", reviewDraftRel(id))
	}
	if open := d.openIssues(); open > 0 {
		return "", fmt.Errorf("у %s открытых блокирующих замечаний %d: апрув при них не ставится ни при каком publish, сперва разбор с автором", id, open)
	}
	if conf.Publish == publishConfirm && !yes {
		return "", fmt.Errorf("publish = confirm: апрув идёт после слова человека, спроси его последней репликой хода и позови команду с --yes")
	}
	target, err := trackerFromMR(d.MR)
	if err != nil {
		return "", err
	}
	script, err := approveScript(target)
	if err != nil {
		return "", err
	}
	if _, err := runPublish(script); err != nil {
		return "", fmt.Errorf("апрув не ушёл в MR: %v", err)
	}
	d.Mark = reviewMarkApproved
	if err := d.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{reviewDraftRel(id)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: апрув поставлен в %s (%s)", id, target.Kind, d.MR) + tail, nil
}

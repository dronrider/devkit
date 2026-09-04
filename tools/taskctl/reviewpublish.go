package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/kvconf"
)

// Публикация замечаний в чужой MR идёт теми же запросами, что лежат в
// kit/skills/review/threads.md: своего клиента трекера у devkit нет, а curl и
// gh есть на любой машине разработчика. В команду едет имя переменной
// окружения, а не значение токена (RULES.md, «Секреты»): разворачивает его
// оболочка подпроцесса, сама утилита токена не читает и в файл замечаний он
// не попадает.

const reviewConfRel = ".devkit/review.conf"

const (
	publishConfirm = "confirm"
	publishAuto    = "auto"
)

// reviewConf это те ключи review.conf, которые читает утилита. Критичные пути
// и проектные вопросы из того же файла читает скилл, разбирать их тут незачем;
// бюджеты уровней читает review stats (DK-731), чтобы сверить накопленные ходы
// и минуты с потолком, а разбор конфига держать в одном месте.
type reviewConf struct {
	Publish  string
	PauseMin time.Duration
	PauseMax time.Duration
	// Poll это шаг опроса тредов сторожком, Silence порог молчания автора,
	// Worklog разрешение писать время ревью в чужой тикет (LLD DK-756,
	// решения 5 и 8).
	Poll    time.Duration
	Silence time.Duration
	Worklog bool
	// Budgets это бюджет ходов и минут первого круга по уровню (1-3): ключ
	// level1..level3. Уровня 0 в конфиге нет, ревью этого уровня бюджет не
	// считает. Пустая карта значит, что конфиг ключей уровня не назвал.
	Budgets map[int]levelBudget
}

// levelBudget это одна строка бюджета: минуты активной работы и ходы.
type levelBudget struct {
	Minutes, Turns int
}

// levelKeyRe узнаёт ключ бюджета уровня («level1», «level2», «level3») и
// достаёт из него номер уровня.
var levelKeyRe = regexp.MustCompile(`^level([1-3])$`)

// levelBudgetRe разбирает значение ключа: «5 минут, 20 ходов». Порядок пар
// фиксирован скиллом review (раздел «Конфиг»), свободный текст вокруг цифр не
// мешает разбору.
var levelBudgetRe = regexp.MustCompile(`(\d+)\s*минут\S*,\s*(\d+)\s*ход`)

func parseLevelBudget(val string) (levelBudget, error) {
	m := levelBudgetRe.FindStringSubmatch(val)
	if m == nil {
		return levelBudget{}, fmt.Errorf("не читается, жду «N минут, M ходов»: %q", val)
	}
	minutes, err1 := strconv.Atoi(m[1])
	turns, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return levelBudget{}, fmt.Errorf("не читается: %q", val)
	}
	return levelBudget{Minutes: minutes, Turns: turns}, nil
}

// Умолчания повторяют «Нештат» LLD DK-756: без конфига ничего в чужой трекер
// само не уходит, а пауза между публикациями держится десятками секунд, чтобы
// пачка тредов не выглядела в MR роботом и не упёрлась в лимит запросов.
const (
	defaultPauseMin = 20 * time.Second
	defaultPauseMax = 60 * time.Second
	// Шаг опроса и порог молчания оттуда же: пять минут это шаг самого
	// сторожка, чаще ходить в чужой трекер незачем, а сутки молчания это
	// повод потолкать коллегу, а не бросить ревью.
	defaultPoll    = 5 * time.Minute
	defaultSilence = 24 * time.Hour
)

func loadReviewConf(root string) (reviewConf, error) {
	c := reviewConf{Publish: publishConfirm, PauseMin: defaultPauseMin, PauseMax: defaultPauseMax,
		Poll: defaultPoll, Silence: defaultSilence}
	pairs, err := kvconf.Read(filepath.Join(root, filepath.FromSlash(reviewConfRel)))
	if err != nil {
		return c, err
	}
	for _, p := range pairs {
		switch p.Key {
		case "publish":
			// Опечатка в режиме не должна молча оборачиваться публикацией:
			// confirm и auto различаются тем, спрашивают ли человека вообще.
			if p.Value != publishConfirm && p.Value != publishAuto {
				return c, fmt.Errorf("%s: publish = %q не читается, жду confirm или auto", reviewConfRel, p.Value)
			}
			c.Publish = p.Value
		case "pause":
			min, max, err := parsePause(p.Value)
			if err != nil {
				return c, fmt.Errorf("%s: %v", reviewConfRel, err)
			}
			c.PauseMin, c.PauseMax = min, max
		case "poll", "silence":
			span, err := parseSpan(p.Value)
			if err != nil {
				return c, fmt.Errorf("%s: %s = %v", reviewConfRel, p.Key, err)
			}
			if p.Key == "poll" {
				c.Poll = span
			} else {
				c.Silence = span
			}
		case "worklog":
			// Ворклог в чужой тикет это договорённость с командой проекта, и
			// опечатка в ключе не должна оборачиваться записью времени коллеге.
			switch p.Value {
			case "on":
				c.Worklog = true
			case "off":
				c.Worklog = false
			default:
				return c, fmt.Errorf("%s: worklog = %q не читается, жду on или off", reviewConfRel, p.Value)
			}
		default:
			if m := levelKeyRe.FindStringSubmatch(p.Key); m != nil {
				lvl, _ := strconv.Atoi(m[1])
				b, berr := parseLevelBudget(p.Value)
				if berr != nil {
					return c, fmt.Errorf("%s: %s = %q %v", reviewConfRel, p.Key, p.Value, berr)
				}
				if c.Budgets == nil {
					c.Budgets = map[int]levelBudget{}
				}
				c.Budgets[lvl] = b
			}
		}
	}
	return c, nil
}

// parsePause читает паузу между публикациями: секунды числом («0») или вилкой
// («20-60»), из которой берётся случайное значение.
func parsePause(val string) (min, max time.Duration, err error) {
	lo, hi, ok := strings.Cut(strings.TrimSpace(val), "-")
	if !ok {
		hi = lo
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(lo))
	b, err2 := strconv.Atoi(strings.TrimSpace(hi))
	if err1 != nil || err2 != nil || a < 0 || b < a {
		return 0, 0, fmt.Errorf("pause = %q не читается, жду секунды числом (0) или вилкой (20-60)", val)
	}
	return time.Duration(a) * time.Second, time.Duration(b) * time.Second, nil
}

// spanRe узнаёт срок вида «5m», «12h», «1d». Суток у time.ParseDuration нет, а
// порог молчания в конфиге пишется именно сутками, поэтому разбор свой.
var spanRe = regexp.MustCompile(`^(\d+)([smhd])$`)

func parseSpan(val string) (time.Duration, error) {
	m := spanRe.FindStringSubmatch(strings.TrimSpace(val))
	if m == nil {
		return 0, fmt.Errorf("%q не читается, жду срок вида 30s, 5m, 12h или 1d", val)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q не читается, жду срок вида 30s, 5m, 12h или 1d", val)
	}
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit, nil
}

func (c reviewConf) pause() time.Duration {
	if c.PauseMax <= c.PauseMin {
		return c.PauseMin
	}
	return c.PauseMin + time.Duration(rand.Int63n(int64(c.PauseMax-c.PauseMin)))
}

// runPublish зовёт подпроцесс оболочкой: имя переменной окружения с токеном
// разворачивает она, а не Go. Сама переменная подменяется тестом, живого
// трекера прогону не нужно.
var runPublish = func(script string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// sleepPublish это пауза между публикациями отдельной переменной: тест считает
// вызовы, а не ждёт реальные десятки секунд.
var sleepPublish = time.Sleep

// shQuote заворачивает аргумент в одинарные кавычки для оболочки. Текст
// замечания пишет человек, и апостроф в нём («не буду, потому что») не должен
// разваливать команду.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// mrTarget это разобранная ссылка на MR: по ней выбирается трекер, а с ним
// команда публикации и имя переменной с токеном.
type mrTarget struct {
	Kind     string // gitlab, github, gitea
	Host     string
	Project  string // group/sub/proj у GitLab, owner/repo у остальных
	Number   string
	TokenEnv string // имя переменной окружения с токеном
}

func trackerFromMR(link string) (mrTarget, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Host == "" {
		return mrTarget{}, fmt.Errorf("ссылка на MR %q не разбирается, жду адрес вида https://host/group/proj/-/merge_requests/42", link)
	}
	path := strings.Trim(u.Path, "/")
	for _, m := range []struct{ kind, sep, tokenEnv string }{
		{"gitlab", "/-/merge_requests/", "GITLAB_TOKEN"},
		{"github", "/pull/", "GITHUB_TOKEN"},
		{"gitea", "/pulls/", "GITEA_TOKEN"},
	} {
		project, num, ok := strings.Cut(path, m.sep)
		if !ok || project == "" {
			continue
		}
		num = strings.Trim(strings.SplitN(num, "/", 2)[0], "/")
		if _, err := strconv.Atoi(num); err != nil {
			return mrTarget{}, fmt.Errorf("в ссылке %q не нашёл номер MR", link)
		}
		return mrTarget{Kind: m.kind, Host: u.Scheme + "://" + u.Host, Project: project, Number: num, TokenEnv: m.tokenEnv}, nil
	}
	return mrTarget{}, fmt.Errorf("по ссылке %q не понял трекер: жду merge_requests (GitLab), pull (GitHub) или pulls (Gitea)", link)
}

// body собирает текст реплики так, как он уйдёт в тред: метка conventional
// comments впереди. Блок «итог» до реплики не доходит, publish снимает его
// раньше, и ветка без префикса тут осталась страховкой на ручную правку файла.
func reviewBody(bl reviewBlock) string {
	if bl.Label == reviewLabelSummary {
		return bl.Text
	}
	return bl.Label + ": " + bl.Text
}

// diffRefs это три sha позиции GitLab. Тред на строку диффа без них API не
// принимает, поэтому они читаются одним запросом на всю публикацию.
type diffRefs struct {
	Base  string `json:"base_sha"`
	Start string `json:"start_sha"`
	Head  string `json:"head_sha"`
}

func gitlabDiffRefs(t mrTarget) (diffRefs, error) {
	script := fmt.Sprintf(`curl -sS --header "PRIVATE-TOKEN: $%s" %s`, t.TokenEnv,
		shQuote(fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s", t.Host, url.PathEscape(t.Project), t.Number)))
	out, err := runPublish(script)
	if err != nil {
		return diffRefs{}, fmt.Errorf("GitLab не отдал diff_refs MR: %v", err)
	}
	var resp struct {
		Refs diffRefs `json:"diff_refs"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return diffRefs{}, fmt.Errorf("ответ GitLab на запрос MR не разбирается: %v", err)
	}
	if resp.Refs.Head == "" {
		return diffRefs{}, fmt.Errorf("GitLab не назвал diff_refs MR %s, тред на строку диффа привязать не к чему", t.Number)
	}
	return resp.Refs, nil
}

// publishScript собирает команду публикации одного блока по трекеру из ссылки.
// Формы взяты из threads.md, разница в том, что тут берутся варианты, которые
// отдают JSON: id треда из ответа вписывается в файл замечаний.
func publishScript(t mrTarget, bl reviewBlock, sha string, refs diffRefs) (string, error) {
	body := reviewBody(bl)
	switch t.Kind {
	case "gitlab":
		api := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s/discussions", t.Host, url.PathEscape(t.Project), t.Number)
		s := fmt.Sprintf(`curl -sS --request POST --header "PRIVATE-TOKEN: $%s" %s --data-urlencode %s`,
			t.TokenEnv, shQuote(api), shQuote("body="+body))
		if bl.File != "" {
			s += fmt.Sprintf(` --data-urlencode 'position[position_type]=text'`+
				` --data-urlencode %s --data-urlencode %s --data-urlencode %s --data-urlencode %s`,
				shQuote("position[base_sha]="+refs.Base), shQuote("position[start_sha]="+refs.Start),
				shQuote("position[head_sha]="+refs.Head), shQuote("position[new_path]="+bl.File))
			if bl.Line != "" {
				s += fmt.Sprintf(" --data-urlencode %s", shQuote("position[new_line]="+bl.Line))
			}
		}
		return s, nil
	case "github":
		if bl.File != "" {
			return fmt.Sprintf(`gh api %s -f %s -f %s -f %s -F %s -f side=RIGHT`,
				shQuote(fmt.Sprintf("repos/%s/pulls/%s/comments", t.Project, t.Number)),
				shQuote("body="+body), shQuote("commit_id="+sha), shQuote("path="+bl.File),
				shQuote("line="+bl.Line)), nil
		}
		return fmt.Sprintf(`gh api %s -f %s -f event=COMMENT`,
			shQuote(fmt.Sprintf("repos/%s/pulls/%s/reviews", t.Project, t.Number)),
			shQuote("body="+body)), nil
	case "gitea":
		api := fmt.Sprintf("%s/api/v1/repos/%s/pulls/%s/reviews", t.Host, t.Project, t.Number)
		payload := map[string]any{"event": "COMMENT"}
		if bl.File != "" {
			comment := map[string]any{"path": bl.File, "body": body}
			if bl.Line != "" {
				n, _ := strconv.Atoi(bl.Line)
				comment["new_position"] = n
			}
			payload["commit_id"] = sha
			payload["comments"] = []any{comment}
		} else {
			payload["body"] = body
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`curl -sS --request POST --header "Authorization: token $%s"`+
			` --header 'Content-Type: application/json' %s --data %s`,
			t.TokenEnv, shQuote(api), shQuote(string(data))), nil
	}
	return "", fmt.Errorf("трекер %q не знаю", t.Kind)
}

// threadID вытаскивает id треда из ответа API. У GitLab это строка-хеш, у
// GitHub и Gitea число, поэтому число читается как есть, без float-хвоста.
func threadID(out []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	var resp map[string]any
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("ответ трекера не разбирается как JSON: %v", err)
	}
	switch v := resp["id"].(type) {
	case string:
		if v != "" {
			return v, nil
		}
	case json.Number:
		return v.String(), nil
	}
	return "", fmt.Errorf("в ответе трекера нет id треда")
}

// cmdReviewPublish несёт одобренные замечания в MR. При publish = auto своё
// одобрение ставит сама: режим для того и заведён, чтобы человека не
// спрашивать. При confirm черновики она не трогает вовсе, и без единого
// одобренного блока отказывает: молчаливая публикация всего подряд это ровно
// то, от чего режим сторожит.
func cmdReviewPublish(root, id string, c CommitOpts) (string, error) {
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
		return "", fmt.Errorf("в шапке %s нет ссылки на MR, публиковать некуда: допиши строку «- MR: <url>»", reviewDraftRel(id))
	}
	target, err := trackerFromMR(d.MR)
	if err != nil {
		return "", err
	}
	var out []string
	// Блок «итог» это отчёт владельцу строки, а не замечание автору MR: в
	// трекер он не едет ни при каком publish, а в файле получает снятое
	// состояние с припиской, чтобы повторный прогон его не поднимал.
	dropped := 0
	for i := range d.Blocks {
		bl := d.Blocks[i]
		if bl.Label != reviewLabelSummary || bl.published() {
			continue
		}
		if bl.State == reviewStateDropped && bl.Note == reviewSummaryNote {
			continue
		}
		d.Blocks[i].State = reviewStateDropped
		d.Blocks[i].Note = reviewSummaryNote
		d.Blocks[i].Thread = ""
		dropped++
	}
	if dropped > 0 {
		if err := d.save(); err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("итог в MR не публикуется, блоков снято %d", dropped))
	}
	if conf.Publish == publishAuto {
		n := 0
		for i := range d.Blocks {
			if d.Blocks[i].State == reviewStateNew {
				d.Blocks[i].State = reviewStateApproved
				n++
			}
		}
		if n > 0 {
			if err := d.save(); err != nil {
				return "", err
			}
			out = append(out, fmt.Sprintf("publish = auto: черновиков одобрено %d", n))
		}
	}
	var todo []int
	for i, bl := range d.Blocks {
		if bl.State == reviewStateApproved {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		if dropped > 0 {
			return strings.Join(out, "\n"), nil
		}
		if conf.Publish == publishAuto {
			return "", fmt.Errorf("публиковать нечего: в %s нет ни черновика, ни одобренного замечания", reviewDraftRel(id))
		}
		return "", fmt.Errorf("публиковать нечего: в %s нет одобренных замечаний, а publish = confirm черновики сам не одобряет (review approve %s <N> после слова человека)", reviewDraftRel(id), id)
	}
	var refs diffRefs
	positioned := false
	for _, i := range todo {
		if d.Blocks[i].File != "" {
			positioned = true
		}
	}
	if positioned {
		if target.Kind == "gitlab" {
			if refs, err = gitlabDiffRefs(target); err != nil {
				return "", err
			}
		} else if d.Sha == "" {
			return "", fmt.Errorf("в шапке %s нет sha ревью, а тред на строку диффа привязывается к коммиту: допиши «- ревью до: <sha>»", reviewDraftRel(id))
		}
	}
	published := 0
	for k, i := range todo {
		if k > 0 {
			sleepPublish(conf.pause())
		}
		script, err := publishScript(target, d.Blocks[i], d.Sha, refs)
		if err != nil {
			return "", err
		}
		raw, err := runPublish(script)
		if err != nil {
			return strings.Join(out, "\n"), fmt.Errorf("замечание %d не ушло в MR: %v (опубликовано до него %d)", i+1, err, published)
		}
		thread, err := threadID(raw)
		if err != nil {
			return strings.Join(out, "\n"), fmt.Errorf("замечание %d: %v (опубликовано до него %d)", i+1, err, published)
		}
		d.Blocks[i].State = reviewStatePublished
		d.Blocks[i].Thread = thread
		if err := d.save(); err != nil {
			return "", err
		}
		published++
		out = append(out, fmt.Sprintf("замечание %d опубликовано, тред %s", i+1, thread))
	}
	tail, err := c.apply(root, []string{reviewDraftRel(id)})
	if err != nil {
		return "", err
	}
	out = append(out, fmt.Sprintf("%s: в %s ушло замечаний %d", id, target.Kind, published))
	return strings.Join(out, "\n") + tail, nil
}

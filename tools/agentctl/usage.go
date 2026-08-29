package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Съёмщик панели /usage: одноразовая tmux-сессия, claude из PATH, команда,
// ожидание отрисовки, capture-pane, парсинг, запись снимка, уборка сессии.
// Программного доступа к остатку нет (расчёт серверный, headless-эквивалента
// /usage нет), поэтому единственный механический путь это прочитать то же, что
// видит человек.
const (
	usageReadyTimeout = 20 * time.Second
	usageEchoTimeout  = 5 * time.Second
	usagePollEvery    = 400 * time.Millisecond
	// Потолок ожидания панели. Прежние двадцать пять секунд отмеряли только
	// отрисовку, а ждать теперь приходится и запрос за цифрами: клиент даёт ему
	// пять секунд, повторяет после обновления токена и рисует разбивку по
	// моделям уже по ответу. Сорок пять это те же двадцать пять плюс две
	// попытки запроса с запасом на медленную сеть. Потолок тут страховка, а не
	// рабочий путь: ожидание кончается словом самой панели, и на живой машине
	// съёмка занимает прежние секунды.
	usagePanelTimeout = 45 * time.Second
)

// Размер окна съёмщика. Панель /usage подросла, под бакетами клиент рисует
// разбор расхода за сутки, и на шестидесяти строках недельный бакет уезжал выше
// видимой части, а capture-pane отдаёт только её. Высота взята с запасом на
// дальнейший рост разбора, лишние строки окна ничего не стоят.
const (
	usagePaneCols = 200
	usagePaneRows = 200
)

// usageCommand это то, что набирается в строке ввода клиента.
const usageCommand = "/usage"

// ansiRe снимает управляющие последовательности: capture-pane отдаёт текст
// панели вместе с ними, а парсеру нужны только буквы.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

var percentRe = regexp.MustCompile(`(\d{1,3})\s*%`)

var monthRe = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2})\b`)

// Время с суффиксом am/pm и оно же в 24-часовом виде: панель показывает первое,
// но локаль может дать и второе.
var clock12Re = regexp.MustCompile(`(?i)(\d{1,2})(?::(\d{2}))?\s*(am|pm)`)

var clock24Re = regexp.MustCompile(`(\d{1,2}):(\d{2})`)

// Обратный отсчёт до сброса: «in 4d 19h», «in 1h 43m», «in 6 days».
var countdownRe = regexp.MustCompile(`(?i)\bin\s+((?:\d+\s*[dhm][a-z]*\s*)+)`)

var countdownPartRe = regexp.MustCompile(`(?i)(\d+)\s*([dhm])`)

// Имя пояса в скобках: «(America/Los_Angeles)». Слэш обязателен, иначе под
// правило попали бы обычные скобки вроде «(all models)».
var tzRe = regexp.MustCompile(`\(([A-Za-z]+(?:/[A-Za-z_+-]+)+)\)`)

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"oct": time.October, "nov": time.November, "dec": time.December,
}

// parseUsagePanel вытаскивает недельные бакеты из текста панели. Разметка
// панели не контракт, она может измениться при редизайне, поэтому парсер ищет
// метки по ключевым словам и отказывает, не найдя ожидаемого: лучше отказ, чем
// записанный мусор.
func parseUsagePanel(q *quotaSpec, text string, now time.Time) (snapshot, error) {
	s := snapshot{Taken: now}
	found := map[string]*bucket{}
	// Признак «процент уже снят» держится отдельно: у нетронутого бакета
	// честные 0% used, и по самому полю первое значение от пустого не отличить,
	// а следующий процент в той же секции затёр бы его.
	gotPercent := map[string]bool{}
	current := ""
	for _, raw := range strings.Split(text, "\n") {
		line := ansiRe.ReplaceAllString(raw, "")
		low := strings.ToLower(line)
		if key, ok := panelSection(low); ok {
			current = key
			if key != "" {
				if _, seen := found[key]; !seen {
					found[key] = &bucket{Name: key}
				}
			}
		}
		if current == "" {
			continue
		}
		b := found[current]
		if !gotPercent[current] {
			if used, ok := panelUsed(line, low); ok {
				b.Used = used
				gotPercent[current] = true
			}
		}
		if b.Reset.IsZero() && strings.Contains(low, "reset") {
			if t, err := parseResetTime(line, now); err == nil {
				b.Reset = t
			}
		}
	}
	// Обязателен только общий бакет: дорогой показывают не все панели, у
	// клиента 2.1.220 вместо Opus идёт Fable, а на другом тарифе может не
	// оказаться и его.
	for _, name := range q.Buckets {
		b, ok := found[name]
		if !ok {
			continue
		}
		if b.Reset.IsZero() {
			return snapshot{}, fmt.Errorf("у бакета %s в панели нет даты сброса", name)
		}
		// Молча записанный ноль читался бы как нетронутый бакет, то есть как
		// профицит: непрочитанный процент честнее превратить в отказ.
		if !gotPercent[name] {
			return snapshot{}, fmt.Errorf("у бакета %s в панели не нашлось процента", name)
		}
		s.Buckets = append(s.Buckets, *b)
	}
	if _, ok := s.bucket(q.Required); !ok {
		return snapshot{}, fmt.Errorf("в панели не нашлось бакета %s", q.Required)
	}
	return s, nil
}

// panelSection узнаёт заголовок бакета. Слово «current» в заголовке
// обязательно: без него на роль заголовка проходят соседние строки панели, где
// «week» тоже встречается (промо «+50% weekly limits promo», подсказка
// «w to week»), и процент промо уехал бы в бакет вместо настоящего.
// Пятичасовая сессия узнаётся тоже, но сбрасывает разбор в пустой ключ: в
// снимок она не пишется. Имена бакетов тут уже каноничные, ярусные: панель
// называет добавочный бакет моделью, а снимок пишется именами профиля.
func panelSection(low string) (string, bool) {
	if !strings.Contains(low, "current") {
		return "", false
	}
	switch {
	case strings.Contains(low, "week") && strings.Contains(low, "opus"):
		return "week_opus", true
	case strings.Contains(low, "week") && strings.Contains(low, "fable"):
		return "week_max", true
	case strings.Contains(low, "week"):
		return "week_all", true
	case strings.Contains(low, "session"):
		return "", true
	}
	return "", false
}

// panelUsed достаёт долю потраченного. Процент берётся только со строки полоски
// («41% used»), а не с любой, где есть знак процента: панель печатает внутри
// секции и посторонние проценты вроде промо на недельные лимиты. Панель может
// показывать и остаток, тогда процент переворачивается.
func panelUsed(line, low string) (float64, bool) {
	if !strings.Contains(low, "used") && !strings.Contains(low, "left") && !strings.Contains(low, "remain") {
		return 0, false
	}
	m := percentRe.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	if strings.Contains(low, "left") || strings.Contains(low, "remain") {
		n = 100 - n
	}
	return float64(n) / 100, true
}

// panelLocation достаёт явный пояс из хвоста строки сброса. Панель показывает
// время в поясе аккаунта, а он не обязан совпадать с поясом машины: без этого
// сброс уезжает на часы. Зона не грузится (нет базы поясов), значит считаем
// местным временем, это ближе к правде, чем отказ от целого бакета.
func panelLocation(line string) *time.Location {
	m := tzRe.FindStringSubmatch(line)
	if m == nil {
		return time.Local
	}
	loc, err := time.LoadLocation(m[1])
	if err != nil {
		return time.Local
	}
	return loc
}

// parseResetTime разбирает строку сброса. Форм две: календарная («Resets Aug 4
// at 10:00am») и обратный отсчёт («Resets in 4d 19h»), панель показывала обе.
// Года в календарной форме нет, он достраивается ближайшим будущим: сброс
// недельного бакета всегда впереди, а окно короче года.
func parseResetTime(line string, now time.Time) (time.Time, error) {
	m := monthRe.FindStringSubmatch(line)
	if m == nil {
		if d, ok := parseCountdown(line); ok {
			return now.Add(d).Truncate(time.Minute), nil
		}
		return time.Time{}, fmt.Errorf("в строке сброса нет даты: %q", strings.TrimSpace(line))
	}
	hour, min, ok := parseClock(line)
	if !ok {
		return time.Time{}, fmt.Errorf("в строке сброса нет времени: %q", strings.TrimSpace(line))
	}
	day, err := strconv.Atoi(m[2])
	if err != nil {
		return time.Time{}, err
	}
	t := time.Date(now.Year(), months[strings.ToLower(m[1][:3])], day, hour, min, 0, 0, panelLocation(line))
	if t.Before(now.Add(-24 * time.Hour)) {
		t = t.AddDate(1, 0, 0)
	}
	return t, nil
}

// parseCountdown складывает обратный отсчёт вида «in 4d 19h» в длительность.
func parseCountdown(line string) (time.Duration, bool) {
	m := countdownRe.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	var d time.Duration
	for _, part := range countdownPartRe.FindAllStringSubmatch(m[1], -1) {
		n, err := strconv.Atoi(part[1])
		if err != nil {
			return 0, false
		}
		switch strings.ToLower(part[2]) {
		case "d":
			d += time.Duration(n) * 24 * time.Hour
		case "h":
			d += time.Duration(n) * time.Hour
		case "m":
			d += time.Duration(n) * time.Minute
		}
	}
	return d, d > 0
}

func parseClock(line string) (hour, min int, ok bool) {
	if m := clock12Re.FindStringSubmatch(line); m != nil {
		hour, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		half := strings.ToLower(m[3])
		if half == "pm" && hour != 12 {
			hour += 12
		}
		if half == "am" && hour == 12 {
			hour = 0
		}
		return hour, min, hour < 24 && min < 60
	}
	if m := clock24Re.FindStringSubmatch(line); m != nil {
		hour, _ = strconv.Atoi(m[1])
		min, _ = strconv.Atoi(m[2])
		return hour, min, hour < 24 && min < 60
	}
	return 0, 0, false
}

func tmuxRun(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// cmdQuotaRefresh снимает остаток и пишет снимок. Чем снимать, говорит [quota]
// snap профиля: usage-pane это встроенный цикл над панелью /usage, script это
// съёмщик рядом с профилем. Отказ честный в обоих случаях: файл не тронут, и
// pick живёт по прежнему снимку либо без корректора. Флаг ifStale это режим для
// хука старта сессии: снимать на каждой сессии незачем, а порог свежести
// остаётся здесь же, второй копии в хуке нет.
func cmdQuotaRefresh(q *quotaSpec, now time.Time, ifStale bool) (string, error) {
	if ifStale {
		s, err := q.read()
		if err == nil && !s.empty() && s.fresh(now) {
			return fmt.Sprintf("снимок свежий (возраст %s при пороге %s), не снимаем",
				humanAge(now.Sub(s.Taken)), humanAge(snapshotMaxAge)), nil
		}
	}
	var snap snapshot
	var err error
	switch q.Snap {
	case snapUsagePane:
		snap, err = snapUsagePanel(q, now)
	case snapScript:
		snap, err = snapByScript(q, now)
	default:
		err = fmt.Errorf("харнес %s объявил snap = %q, снимать таким способом devkit не умеет", q.Harness, q.Snap)
	}
	if err != nil {
		return "", err
	}
	if err := q.write(snap); err != nil {
		return "", err
	}
	return cmdQuota(q, now)
}

// snapByScript зовёт сменный съёмщик из kit/harness/snap/. Контракт разобран в
// docs/lld/DK-033-universal-kit.md, раздел «Контракт съёмщика»: stdin не даётся,
// в окружении имя харнеса, бюджет и каталог машинного хозяйства, stdout это
// готовый текст снимка, а отказ это ненулевой код с человеческой причиной в
// stderr. Файл пишет не скрипт: вывод разбирается тем же парсером, которым
// читается снимок, и негодный отказывает громко, оставляя прежний снимок на
// месте.
func snapByScript(q *quotaSpec, now time.Time) (snapshot, error) {
	path := filepath.Join(q.Dir, q.Script)
	if _, err := os.Stat(path); err != nil {
		return snapshot{}, fmt.Errorf("съёмщика %s нет (%v), снимок не тронут", path, err)
	}
	if q.BudgetBased && q.Budget <= 0 {
		return snapshot{}, fmt.Errorf("расход харнеса %s считается в деньгах, а бюджета в машинном конфиге нет; вписать budget в секцию [%s] файла %s",
			q.Harness, q.Harness, machineConfigPath())
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), "DEVKIT_HARNESS="+q.Harness)
	if q.Home != "" {
		cmd.Env = append(cmd.Env, "DEVKIT_HARNESS_HOME="+q.Home)
	}
	if q.BudgetBased {
		cmd.Env = append(cmd.Env, "DEVKIT_QUOTA_BUDGET="+strconv.Itoa(q.Budget))
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		why := strings.TrimSpace(errs.String())
		if why == "" {
			why = "причины в stderr нет"
		}
		return snapshot{}, fmt.Errorf("съёмщик %s отказал (%v): %s; снимок не тронут", path, err, why)
	}
	s := q.parse(out.String())
	// Разбор снимка прощает битую строку, а вывод съёмщика нет: файл на диске
	// пишется руками и переживает смену панели, а тут говорит машина, и её
	// «почти снимок» лучше отбросить целиком, чем записать наполовину.
	if len(s.Warns) > 0 {
		return snapshot{}, fmt.Errorf("вывод съёмщика %s не разобран (%s), снимок не тронут", path, strings.Join(s.Warns, "; "))
	}
	if s.Taken.IsZero() {
		return snapshot{}, fmt.Errorf("в выводе съёмщика %s нет момента снятия (строка taken =), снимок не тронут", path)
	}
	if _, ok := s.bucket(q.Required); !ok {
		return snapshot{}, fmt.Errorf("в выводе съёмщика %s нет обязательного бакета %s, снимок не тронут", path, q.Required)
	}
	return s, nil
}

// snapUsagePanel это встроенный съёмщик Claude Code: одноразовая tmux-сессия с
// клиентом, панель /usage, capture-pane.
func snapUsagePanel(q *quotaSpec, now time.Time) (snapshot, error) {
	path := q.Path
	if _, err := exec.LookPath("tmux"); err != nil {
		return snapshot{}, fmt.Errorf("tmux в PATH нет, снимать панель /usage нечем; снимок пишется и руками: %s", path)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return snapshot{}, fmt.Errorf("claude в PATH нет, снимать панель /usage нечем; снимок пишется и руками: %s", path)
	}
	session := fmt.Sprintf("agentctl-usage-%d", os.Getpid())
	if out, err := tmuxRun("new-session", "-d", "-s", session, "-x", strconv.Itoa(usagePaneCols), "-y", strconv.Itoa(usagePaneRows), "claude"); err != nil {
		return snapshot{}, fmt.Errorf("tmux не поднял сессию: %v %s", err, out)
	}
	defer tmuxRun("kill-session", "-t", session)

	var pane string
	if err := waitPane(session, usageReadyTimeout, func(text string) bool {
		pane = text
		return paneReady(text) || paneBlocker(text) != ""
	}); err != nil {
		return snapshot{}, fmt.Errorf("claude не отрисовал строку ввода за %s, снимок не тронут", usageReadyTimeout)
	}
	if why := paneBlocker(pane); why != "" {
		return snapshot{}, errors.New(why)
	}
	if _, err := tmuxRun("send-keys", "-t", session, "-l", usageCommand); err != nil {
		return snapshot{}, fmt.Errorf("не удалось набрать команду: %v", err)
	}
	// Enter уходит только после того, как набранное видно в панели. Слэш
	// открывает список команд, и в пустой список Enter попал бы мимо; а на
	// незнакомом экране, который перехватил ввод, он подтвердил бы подсвеченное
	// там, что бы это ни было.
	if err := waitPane(session, usageEchoTimeout, func(text string) bool {
		return strings.Contains(text, usageCommand)
	}); err != nil {
		return snapshot{}, fmt.Errorf("клиент не принял набранное %s за %s: Enter не отправлен, снимок не тронут", usageCommand, usageEchoTimeout)
	}
	if _, err := tmuxRun("send-keys", "-t", session, "Enter"); err != nil {
		return snapshot{}, fmt.Errorf("не удалось отправить команду: %v", err)
	}

	w := panelWaiter{}
	var why error
	err := waitPane(session, usagePanelTimeout, func(text string) bool {
		pane = text
		// Отказ клиента ждать бессмысленно, он держится до нажатия «r», так что
		// ожидание обрывается на нём и не съедает весь свой потолок. Подтвердить
		// его вторым кадром всё равно надо: разовое совпадение отбросило бы
		// годный снимок.
		if panelBlocked(text) != "" {
			return w.blocked(text)
		}
		s, perr := parseUsagePanel(q, text, now)
		why = perr
		if perr != nil {
			w.miss(text)
			return false
		}
		return w.accept(s, text)
	})
	if panelBlocked(pane) != "" {
		return snapshot{}, panelFailure(q, pane, why)
	}
	if err != nil {
		// Панель так и не досчитала за отпущенное время. Разобранный кадр с
		// общим бакетом всё равно лучше отказа: без снимка корректор выключен
		// целиком, а про неполноту снимок теперь говорит сам.
		if len(w.snap.Buckets) == 0 {
			return snapshot{}, panelFailure(q, pane, why)
		}
		return w.snap.markPartial(q, fmt.Sprintf("панель не досчитала разбивку за %s", usagePanelTimeout)), nil
	}
	return w.snap.markPartial(q, w.gap(pane)), nil
}

// panelFailure объясняет, почему съёмщик ушёл ни с чем. Отказ по одному
// таймауту бесполезен, разбор спотыкается о живой экран, которого в отказе не
// видно. Поэтому последний кадр панели ложится файлом рядом со снимком, путь к
// нему называется прямо в отказе, и дальше разбор чинится по кадру.
func panelFailure(q *quotaSpec, pane string, why error) error {
	var b strings.Builder
	switch blocked := panelBlocked(pane); {
	case blocked != "":
		b.WriteString(blocked)
	case !panelSeen(pane):
		fmt.Fprintf(&b, "клиент не нарисовал панель %s за %s.", usageCommand, usagePanelTimeout)
	case why == nil:
		fmt.Fprintf(&b, "панель %s открылась, но так и не устоялась за %s.", usageCommand, usagePanelTimeout)
	default:
		fmt.Fprintf(&b, "панель %s открылась, но за %s разобрать её не вышло, потому что %v.", usageCommand, usagePanelTimeout, why)
	}
	if panelCropped(pane) {
		b.WriteString(" Верх панели не поместился в окно съёмщика, и строки бакетов ушли выше видимой части.")
	}
	if path, err := saveFrame(q, pane); err == nil {
		fmt.Fprintf(&b, " Кадр панели лежит в %s, по нему видно, что съёмщик прочитал с экрана.", path)
	} else {
		fmt.Fprintf(&b, " Кадр панели сохранить не удалось (%v).", err)
	}
	b.WriteString(" Снимок не тронут.")
	return errors.New(b.String())
}

// panelBlocked узнаёт отказ, который клиент печатает вместо цифр, и переводит
// его на человеческий. Слова берутся точные. Строку про частоту обращений
// панель печатает и внутри целого экрана, когда не дождалась одной только
// разбивки по моделям, и там она отказом не является.
func panelBlocked(pane string) string {
	low := lowPane(pane)
	switch {
	case strings.Contains(low, "usage endpoint is rate limited"):
		return "клиент упёрся в частоту обращений к панели /usage и цифр не показал. Снимок встанет следующей попыткой, лимит подписки тут ни при чём."
	case strings.Contains(low, "showing last-known usage"):
		// Панель тут рисует цифры прошлого раза и честно это подписывает.
		// Записать их снимком нельзя: свежий момент снятия над старыми цифрами
		// это ровно та молчащая ложь, ради которой снимок и заводился.
		return "клиент показал цифры прошлого раза, а свежих не получил: писать их снимком нельзя, прежний снимок остаётся на месте. Повторить через минуту."
	}
	return ""
}

// lowPane чистит кадр от управляющих последовательностей и приводит к нижнему
// регистру: узнают панель по словам, а capture-pane отдаёт их вперемешку с
// разметкой цвета.
func lowPane(pane string) string {
	return strings.ToLower(ansiRe.ReplaceAllString(pane, ""))
}

// panelSeen отвечает, дорисовалась ли вообще панель расхода. Слова взяты те,
// что панель печатает и в целом виде, и когда верх уехал за край окна.
func panelSeen(pane string) bool {
	low := lowPane(pane)
	for _, mark := range []string{"current week", "current session", "of your usage", "your limits usage"} {
		if strings.Contains(low, mark) {
			return true
		}
	}
	return false
}

// panelCropped отвечает, срезан ли верх панели. Панель начинается полосой
// вкладок, и когда её на экране нет, а разбор расхода под бакетами есть, значит
// панель длиннее окна и бакеты уехали вверх.
func panelCropped(pane string) bool {
	if !panelSeen(pane) {
		return false
	}
	for _, raw := range strings.Split(pane, "\n") {
		low := lowPane(raw)
		if strings.Contains(low, "settings") && strings.Contains(low, "usage") && strings.Contains(low, "stats") {
			return false
		}
	}
	return true
}

// saveFrame кладёт кадр панели рядом со снимком, под своим именем. Снимок
// читают утилиты, кадр читает человек, и путать их нельзя.
func saveFrame(q *quotaSpec, pane string) (string, error) {
	if q.Path == "" {
		return "", errors.New("в профиле нет пути снимка")
	}
	if strings.TrimSpace(pane) == "" {
		return "", errors.New("экран оказался пустым")
	}
	path := strings.TrimSuffix(q.Path, filepath.Ext(q.Path)) + ".pane.txt"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(ansiRe.ReplaceAllString(pane, "")), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// panelWaiter решает, когда разобранной панели можно верить. Панель приезжает
// не одним кадром: сначала общий бакет из того, что клиент помнит с прошлого
// раза, разбивка по моделям позже, по ответу на запрос. Первый же успешный
// разбор дал бы снимок без разбивки, и такой снимок хуже отказа: он выглядит
// целым, а корректор по нему молча теряет сдвиг вверх.
//
// Мерить это временем значит гадать. Панель говорит о себе сама: пока запрос
// идёт, внизу висит «Refreshing», а как он сел, строка пропадает и на её месте
// оказывается либо разбивка, либо строка о том, что разбивки не будет.
// Ожидание кончается на этом слове.
//
// Правило подтверждения одно на весь цикл: ни один исход не принимается с
// одного кадра, ни готовая панель, ни отказной экран. capture-pane снимает
// экран в любой момент и может застать панель на середине перерисовки, и
// разовое совпадение стоит одинаково дорого в обе стороны.
//
// Отдельно waiter помнит, услышал ли он от панели хоть одно знакомое слово о
// её состоянии. Ожидание держится на словах клиента, а слова эти меняются с
// версией, и «ни refreshing, ни loading не нашлось» значит либо «панель
// досчитала», либо «панель заговорила по-другому». Различить эти два случая
// внутри кадра нечем, поэтому неуслышанная панель оставляет след в снимке.
type panelWaiter struct {
	snap  snapshot
	last  string
	same  int
	heard bool
}

const panelConfirmFrames = 2

// confirm считает подряд идущие кадры с одним исходом. Смена исхода сбрасывает
// счёт, и цикл начинает считать заново.
func (w *panelWaiter) confirm(kind string) bool {
	if w.last != kind {
		w.last, w.same = kind, 0
	}
	w.same++
	return w.same >= panelConfirmFrames
}

// hear отмечает, что панель назвала своё состояние знакомым словом.
func (w *panelWaiter) hear(pane string) {
	if panelSpeaking(pane) {
		w.heard = true
	}
}

// accept решает по кадру готовой панели, miss по кадру, который не разобрался,
// blocked по отказному экрану. Все трое идут через одно правило подтверждения.
func (w *panelWaiter) accept(s snapshot, pane string) bool {
	w.hear(pane)
	if !panelSettled(pane) {
		w.confirm("draw")
		return false
	}
	w.snap = s
	return w.confirm("ready")
}

func (w *panelWaiter) miss(pane string) {
	w.hear(pane)
	w.confirm("miss")
}

func (w *panelWaiter) blocked(pane string) bool {
	w.hear(pane)
	return w.confirm("blocked")
}

// gap объясняет, почему в снимке может не хватать разбивки по моделям. Слова
// панели идут первыми, они точные. Не услышав от панели ни одного знакомого
// слова, съёмщик про её состояние не знает ничего: снимок уходит с оговоркой,
// а не молча.
func (w *panelWaiter) gap(pane string) string {
	if why := panelNoBreakdown(pane); why != "" {
		return why
	}
	if !w.heard {
		return "панель не сказала о себе ни одного знакомого слова, разметка могла смениться"
	}
	return ""
}

// panelSpeaking отвечает, назвала ли панель своё состояние. Слова берутся
// точные, оба из самой панели: «Loading usage data» она пишет вместо бакетов,
// пока показывать нечего, «Refreshing» подписывает уже нарисованным, пока
// уточняющий запрос в пути. Отсюда же берёт слова panelSettled: словарь один,
// и разъехаться двум местам негде.
func panelSpeaking(pane string) bool {
	low := lowPane(pane)
	return strings.Contains(low, "loading usage data") || strings.Contains(low, "refreshing")
}

// panelSettled отвечает, досчитал ли клиент цифры панели.
func panelSettled(pane string) bool {
	return !panelSpeaking(pane)
}

// panelNoBreakdown переводит на человеческий строку, которой панель объявляет,
// что разбивки по моделям в этом кадре не будет. Пустая строка значит, что
// панель ни о чём таком не заявляла, и отсутствие дорогого бакета в ней надо
// понимать буквально: его нет у подписки.
func panelNoBreakdown(pane string) string {
	low := lowPane(pane)
	switch {
	case strings.Contains(low, "per-model breakdown unavailable"):
		return "панель отказала по частоте обращений"
	case strings.Contains(low, "could not refresh usage data"):
		return "панель не обновила цифры"
	}
	return ""
}

func capturePane(session string) (string, error) {
	return tmuxRun("capture-pane", "-p", "-t", session)
}

func waitPane(session string, limit time.Duration, ready func(string) bool) error {
	deadline := time.Now().Add(limit)
	for {
		text, err := capturePane(session)
		if err == nil && ready(text) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("не дождались панели")
		}
		time.Sleep(usagePollEvery)
	}
}

// paneReady отвечает, принимает ли клиент ввод. «В панели что-то появилось» на
// этот вопрос не отвечает: пока клиент дорисовывается, набранная команда
// пропадает без следа, и refresh уходит ждать панель, которую никто не
// открывал. Признак берётся структурный: строка ввода это непустая строка между
// двумя сплошными линейками, такой рамкой клиент обводит только её. Одной
// линейки мало, ею начинается и диалог доверия каталогу, а слова подсказок под
// рамкой не годятся тем более, клиент крутит их по очереди.
func paneReady(pane string) bool {
	lines := strings.Split(pane, "\n")
	for i := 0; i+2 < len(lines); i++ {
		if solidRule(lines[i]) && solidRule(lines[i+2]) && strings.TrimSpace(paneLine(lines[i+1])) != "" {
			return true
		}
	}
	return false
}

func paneLine(line string) string {
	return strings.TrimSpace(ansiRe.ReplaceAllString(line, ""))
}

func solidRule(line string) bool {
	r := []rune(paneLine(line))
	if len(r) < 40 {
		return false
	}
	for _, c := range r {
		if c != r[0] {
			return false
		}
	}
	return true
}

// paneBlocker узнаёт экраны, которые перехватывают ввод до того, как появится
// строка ввода, и возвращает человеческую причину отказа. Узнавать их надо
// именно поимённо: на таком экране набранная команда никуда не доедет, а Enter
// подтвердит подсвеченный там пункт (доверие каталогу, тему оформления), то
// есть тихо изменит настройки пользователя вместо съёма панели. Экраны
// узнаются по словам интерфейса, и это осознанная ставка: сменится
// формулировка, refresh отвалится по таймауту с отказом, а не нажмёт Enter
// вслепую.
func paneBlocker(pane string) string {
	low := strings.ToLower(pane)
	switch {
	case strings.Contains(low, "/login"), strings.Contains(low, "sign in"), strings.Contains(low, "log in to"):
		return "claude не залогинен, снимать нечего: пройти вход и повторить"
	case strings.Contains(low, "trust this folder"), strings.Contains(low, "trust the files"):
		return "claude спрашивает про доверие каталогу, панель за этим вопросом недоступна: подтвердить доверие руками (claude в этом каталоге) либо гонять refresh из каталога, которому клиент уже доверяет"
	case strings.Contains(low, "let's get started"):
		return "claude показывает мастер первого запуска, до панели он не пускает: пройти мастер руками и повторить"
	}
	return ""
}

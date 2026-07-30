package main

import (
	"fmt"
	"os"
	"os/exec"
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
	usagePanelTimeout = 25 * time.Second
	usagePollEvery    = 400 * time.Millisecond
)

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
func parseUsagePanel(text string, now time.Time) (snapshot, error) {
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
	for _, name := range knownBuckets {
		b, ok := found[name]
		if !ok || b.Reset.IsZero() {
			return snapshot{}, fmt.Errorf("в панели не нашлось бакета %s с датой сброса: панель могла измениться, снимок не тронут", name)
		}
		s.Buckets = append(s.Buckets, *b)
	}
	return s, nil
}

// panelSection узнаёт заголовок бакета. Пятичасовая сессия узнаётся тоже, но
// сбрасывает разбор в пустой ключ: в снимок она не пишется.
func panelSection(low string) (string, bool) {
	switch {
	case strings.Contains(low, "week") && strings.Contains(low, "opus"):
		return "week_opus", true
	case strings.Contains(low, "week"):
		return "week_all", true
	case strings.Contains(low, "session"):
		return "", true
	}
	return "", false
}

// panelUsed достаёт долю потраченного. Панель может показывать и остаток, тогда
// процент переворачивается.
func panelUsed(line, low string) (float64, bool) {
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

// cmdQuotaRefresh снимает панель /usage и пишет снимок. Отказ честный: нет
// tmux, нет claude, панель не узналась, значит файл не тронут и pick живёт по
// прежнему снимку либо без корректора.
func cmdQuotaRefresh(path string, now time.Time) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", fmt.Errorf("tmux в PATH нет, снимать панель /usage нечем; снимок пишется и руками: %s", path)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return "", fmt.Errorf("claude в PATH нет, снимать панель /usage нечем; снимок пишется и руками: %s", path)
	}
	session := fmt.Sprintf("agentctl-usage-%d", os.Getpid())
	if out, err := tmuxRun("new-session", "-d", "-s", session, "-x", "200", "-y", "60", "claude"); err != nil {
		return "", fmt.Errorf("tmux не поднял сессию: %v %s", err, out)
	}
	defer tmuxRun("kill-session", "-t", session)

	if err := waitPane(session, usageReadyTimeout, func(text string) bool {
		return strings.TrimSpace(text) != ""
	}); err != nil {
		return "", fmt.Errorf("claude не отрисовался за %s, снимок не тронут", usageReadyTimeout)
	}
	pane, _ := capturePane(session)
	if notLoggedIn(pane) {
		return "", fmt.Errorf("claude не залогинен, снимать нечего: пройти вход и повторить")
	}
	if _, err := tmuxRun("send-keys", "-t", session, "-l", "/usage"); err != nil {
		return "", fmt.Errorf("не удалось набрать команду: %v", err)
	}
	// Слэш открывает список команд, и Enter уходит отдельно: набранному надо
	// дать отрисоваться, иначе Enter попадает в ещё пустой список.
	time.Sleep(usagePollEvery)
	if _, err := tmuxRun("send-keys", "-t", session, "Enter"); err != nil {
		return "", fmt.Errorf("не удалось отправить команду: %v", err)
	}

	var snap snapshot
	if err := waitPane(session, usagePanelTimeout, func(text string) bool {
		s, err := parseUsagePanel(text, now)
		if err != nil {
			return false
		}
		snap = s
		return true
	}); err != nil {
		return "", fmt.Errorf("панель /usage не узналась за %s: разметка могла измениться, снимок не тронут (образцы панели лежат в agentctl/testdata)", usagePanelTimeout)
	}
	if err := writeSnapshot(path, snap); err != nil {
		return "", err
	}
	return cmdQuota(path, now)
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

func notLoggedIn(pane string) bool {
	low := strings.ToLower(pane)
	return strings.Contains(low, "/login") || strings.Contains(low, "sign in") || strings.Contains(low, "log in to")
}

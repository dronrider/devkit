package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Пороги нарезки фактов на сессии. Пауза длиннее sessionGap рвёт сессию, и всё
// внутри сессии считается работой целиком: wall-clock от старта до конца, где
// вместе с машинным временем агента идёт менеджмент (постановка, переписка,
// разбор отчётов, локальное ревью). Урезать интервал до чистого времени модели
// незачем, это реальные затраты инженера на задачу. Одинокая отметка даёт
// minSession: команда гонялась, значит время на неё ушло, а нулём оно не
// бывает.
const (
	sessionGap = 90 * time.Minute
	minSession = 30 * time.Minute
)

const dateLayout = "2006-01-02"
const stampLayout = "2006-01-02T15:04:05"

// workDay это факт одного календарного дня: сколько работы в нём набралось.
type workDay struct {
	Date  string
	Spent time.Duration
}

// interval это одна сессия работы.
type interval struct{ start, end time.Time }

// sessions режет отметки на сессии по паузе. Отметки приходят из разных
// источников (журнал запусков и коммиты), поэтому сортируются здесь же.
func sessions(events []time.Time) []interval {
	if len(events) == 0 {
		return nil
	}
	sorted := append([]time.Time(nil), events...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })
	var out []interval
	cur := interval{start: sorted[0], end: sorted[0]}
	for _, e := range sorted[1:] {
		if e.Sub(cur.end) > sessionGap {
			out = append(out, cur)
			cur = interval{start: e, end: e}
			continue
		}
		cur.end = e
	}
	return append(out, cur)
}

// workDays собирает факт работы над тикетом. Отметки журнала запусков сами по
// себе про задачу ничего не знают, привязку даёт коммит: в счёт идут только
// сессии, внутри которых есть коммит с ключом тикета, а работа задачи от
// первого коммита до последнего этими сессиями и накрывается. День, в который
// работа не шла, записи не получает вовсе.
func workDays(runs, commits []time.Time) []workDay {
	if len(commits) == 0 {
		return nil
	}
	all := append(append([]time.Time(nil), runs...), commits...)
	byDate := map[string]time.Duration{}
	for _, s := range sessions(all) {
		if !hasEventIn(commits, s) {
			continue
		}
		if s.end.Sub(s.start) < minSession {
			s.end = s.start.Add(minSession)
		}
		splitByDay(s, byDate)
	}
	out := make([]workDay, 0, len(byDate))
	for d, spent := range byDate {
		out = append(out, workDay{Date: d, Spent: spent})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func hasEventIn(events []time.Time, s interval) bool {
	for _, e := range events {
		if !e.Before(s.start) && !e.After(s.end) {
			return true
		}
	}
	return false
}

// splitByDay раскладывает сессию по календарным дням: ночной прогон, перешедший
// за полночь, делится по дням, а не приписывается одному.
func splitByDay(s interval, byDate map[string]time.Duration) {
	for s.start.Before(s.end) {
		midnight := time.Date(s.start.Year(), s.start.Month(), s.start.Day(), 0, 0, 0, 0, s.start.Location()).AddDate(0, 0, 1)
		end := s.end
		if midnight.Before(end) {
			end = midnight
		}
		byDate[s.start.Format(dateLayout)] += end.Sub(s.start)
		s.start = end
	}
}

// spentHours округляет день до часов вверх: точность тут грубая, ворклог идёт
// в часах, и спорить о минутах не с кем.
func spentHours(d time.Duration) string {
	h := int((d + time.Hour - time.Nanosecond) / time.Hour)
	if h < 1 {
		h = 1
	}
	return fmt.Sprintf("%dh", h)
}

// runTimes собирает отметки журнала запусков боковой директории. Строка журнала
// это «время \t утилита \t команда \t код», всё прочее пропускается, включая
// собственные записи ворклогов (у них колонок больше).
func runTimes(root string) ([]time.Time, error) {
	f, err := os.Open(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []time.Time
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 4 {
			continue
		}
		ts, err := time.ParseInLocation(stampLayout, parts[0], time.Local)
		if err != nil {
			continue
		}
		out = append(out, ts)
	}
	return out, sc.Err()
}

// Запись отправленного ворклога в журнале: «время \t trackctl \t worklog \t
// ключ \t дата \t списано». Колонок шесть, поэтому сводка запусков
// (devkitctl stats) её не считает командой, а submit по ней видит, какие дни
// уже уехали, и второй раз их не пишет.
const worklogMark = "worklog"

func markWorklog(root, key, date, spent string) error {
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("нет директории %s, записать отправленный ворклог некуда", dir)
	}
	f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\ttrackctl\t%s\t%s\t%s\t%s\n",
		time.Now().Format(stampLayout), worklogMark, key, date, spent)
	return err
}

// writtenDays отдаёт дни тикета, уже уехавшие в трекер. Отсюда идемпотентность
// обоих вариантов submit: повторный прогон видит записанное и его не дублирует.
func writtenDays(root, key string) (map[string]bool, error) {
	out := map[string]bool{}
	f, err := os.Open(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) < 6 || parts[1] != "trackctl" || parts[2] != worklogMark {
			continue
		}
		if strings.EqualFold(parts[3], key) {
			out[parts[4]] = true
		}
	}
	return out, sc.Err()
}

// commitTimes собирает отметки коммитов тикета. Ключ тикета стоит в subject по
// конвенции компании (docs/lld/DK-074-corp-contour.md, «Граница доски и
// тикета»), по нему коммиты и находятся: ветка к этому моменту бывает уже
// слита, а ключ из subject никуда не девается.
func commitTimes(repo, key string) ([]time.Time, error) {
	if fi, err := os.Stat(repo); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("не нашёл репозиторий %s, коммиты тикета брать негде", repo)
	}
	cmd := exec.Command("git", "-C", repo, "log", "--all", "--no-merges", "-F", "--grep="+key, "--pretty=format:%cI\t%s")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log в %s: %v", repo, err)
	}
	re, err := regexp.Compile(`(?i)(^|[^0-9A-Za-z])` + regexp.QuoteMeta(key) + `($|[^0-9])`)
	if err != nil {
		return nil, err
	}
	var times []time.Time
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		stamp, subject, ok := strings.Cut(line, "\t")
		if !ok || !re.MatchString(subject) {
			continue
		}
		ts, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			continue
		}
		times = append(times, ts.Local())
	}
	return times, nil
}

// repoDir отдаёт директорию корп-клона: ключ repo привязки, разрешённый от
// боковой директории. Ключа нет, значит проект домашний и коммиты лежат в самом
// корне.
func repoDir(root string, b *binding) string {
	if b.Repo == "" {
		return root
	}
	if filepath.IsAbs(b.Repo) {
		return b.Repo
	}
	return filepath.Join(root, b.Repo)
}

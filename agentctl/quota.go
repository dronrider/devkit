package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Снимок остатка лимитов лежит на уровне машины, а не проекта: лимиты общие на
// подписку, и per-project копии протухали бы вразнобой.
const quotaFileName = "quota.local"

// Момент снятия и даты сброса пишутся местным временем без секунд: столько же
// точности показывает панель /usage.
const quotaTimeLayout = "2006-01-02T15:04"

// Окно недельных бакетов, от него считается равномерный темп расхода.
const quotaWindow = 7 * 24 * time.Hour

// Профицит из снимка старше суток не применяется: остаток внутри окна только
// тратится, и вывод «есть запас» протухает быстрее вывода «запаса нет».
const surplusMaxAge = 24 * time.Hour

const (
	statusSurplus = "профицит"
	statusNormal  = "норма"
	statusDeficit = "дефицит"
	statusExpired = "протух"
)

// Известные бакеты панели /usage в порядке вывода. Пятичасовой сессионный сюда
// не попадает: он протухает быстрее, чем живёт задача, снимок его не догонит.
var knownBuckets = []string{"week_all", "week_opus"}

// Лестница ярусов остаётся данными, корректор двигает индекс: новая модель
// вставляется строкой сюда и строкой в таблицу трат, формула не меняется.
var tiers = []string{"haiku", "sonnet", "opus", "fable"}

// Из каких бакетов тратит ярус. week_all по смыслу панели учитывает все модели,
// week_opus добирает дорогие отдельно, поэтому opus и всё, что выше, жжёт оба
// сразу. Своего бакета у fable панель пока не показывает.
var tierBuckets = map[string][]string{
	"haiku":  {"week_all"},
	"sonnet": {"week_all"},
	"opus":   {"week_all", "week_opus"},
	"fable":  {"week_all", "week_opus"},
}

// bucket это строка снимка: сколько процентов бакета потрачено на момент
// снятия и когда он сбрасывается.
type bucket struct {
	Name  string
	Used  float64 // доля потраченного, 0..1
	Reset time.Time
}

// snapshot это разобранный файл снимка. Warns копятся вместо ошибок: битая
// строка или незнакомый ключ отбрасывают своё, а остальной снимок работает.
type snapshot struct {
	Taken   time.Time
	Buckets []bucket
	Warns   []string
}

func (s snapshot) bucket(name string) (bucket, bool) {
	for _, b := range s.Buckets {
		if b.Name == name {
			return b, true
		}
	}
	return bucket{}, false
}

// fresh отвечает только за сдвиг вверх. Момента снятия нет, значит возраст
// неизвестен, и профицит не применяется: неизвестность толкуется в пользу
// экономии.
func (s snapshot) fresh(now time.Time) bool {
	return !s.Taken.IsZero() && now.Sub(s.Taken) <= surplusMaxAge
}

// pace это отношение остатка к доле окна, которая осталась до сброса. Больше
// единицы значит, что остаток тратится медленнее равномерного темпа.
func (b bucket) pace(now time.Time) float64 {
	left := b.Reset.Sub(now)
	if left <= 0 {
		return 0
	}
	return (1 - b.Used) / (float64(left) / float64(quotaWindow))
}

// status у бакета. Зазор между порогами широкий сознательно: корректор должен
// срабатывать на выраженный перекос, а не дёргать вердикт на каждом колебании.
func (b bucket) status(now time.Time) string {
	if !b.Reset.After(now) {
		return statusExpired
	}
	switch p := b.pace(now); {
	case p <= 0.5:
		return statusDeficit
	case p >= 2:
		return statusSurplus
	default:
		return statusNormal
	}
}

// quotaPath это путь снимка. HOME берётся на каждый вызов, чтобы тесты
// подкладывали свой файл, не трогая настоящий.
func quotaPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".devkit", quotaFileName)
}

// readSnapshot разбирает файл снимка. Формат плоский, как в
// .devkit/deploy.local: строки key = value, # это комментарий. Нет файла,
// значит пустой снимок без ошибки: корректор тогда молчит.
func readSnapshot(path string) (snapshot, error) {
	var s snapshot
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			s.Warns = append(s.Warns, fmt.Sprintf("строка снимка %q не разобрана", line))
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if key == "taken" {
			t, err := parseQuotaTime(val)
			if err != nil {
				s.Warns = append(s.Warns, fmt.Sprintf("момент снятия %q не разобран", val))
				continue
			}
			s.Taken = t
			continue
		}
		if !known(key) {
			s.Warns = append(s.Warns, fmt.Sprintf("неизвестный ключ снимка %q, пропущен", key))
			continue
		}
		b, err := parseBucket(key, val)
		if err != nil {
			s.Warns = append(s.Warns, fmt.Sprintf("бакет %s не разобран: %v", key, err))
			continue
		}
		s.Buckets = append(s.Buckets, b)
	}
	return s, sc.Err()
}

func known(key string) bool {
	for _, k := range knownBuckets {
		if k == key {
			return true
		}
	}
	return false
}

// parseBucket разбирает значение вида «34% сброс 2026-08-04T10:00».
func parseBucket(name, val string) (bucket, error) {
	pct, rest, ok := strings.Cut(val, "%")
	if !ok {
		return bucket{}, fmt.Errorf("жду процент потраченного, вижу %q", val)
	}
	n, err := strconv.Atoi(strings.TrimSpace(pct))
	if err != nil || n < 0 || n > 100 {
		return bucket{}, fmt.Errorf("процент %q не в диапазоне 0-100", strings.TrimSpace(pct))
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "сброс"))
	reset, err := parseQuotaTime(rest)
	if err != nil {
		return bucket{}, fmt.Errorf("дата сброса %q не разобрана", rest)
	}
	return bucket{Name: name, Used: float64(n) / 100, Reset: reset}, nil
}

func parseQuotaTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{quotaTimeLayout, "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("не время: %q", s)
}

// writeSnapshot кладёт снимок тем же форматом, каким его пишут руками: refresh
// это автоматизация заполнения, а не второй формат.
func writeSnapshot(path string, s snapshot) error {
	var b strings.Builder
	fmt.Fprintf(&b, "taken = %s\n", s.Taken.Format(quotaTimeLayout))
	for _, bk := range s.Buckets {
		fmt.Fprintf(&b, "%s = %d%% сброс %s\n", bk.Name, int(math.Round(bk.Used*100)), bk.Reset.Format(quotaTimeLayout))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// correction это решение корректора: с какого яруса на какой съехал вердикт и
// почему. From равен Model, когда сдвига не было.
type correction struct {
	Model string
	From  string
	Note  string // «дефицит week_opus», причина в человеческом виде
	Warn  string // почему причина есть, а сдвига нет
	Down  bool
}

func (c correction) shifted() bool { return c.From != "" && c.From != c.Model }

// tail это хвост человеческой строки вердикта. Без причины хвоста нет: молчание
// корректора не должно занимать место в выводе.
func (c correction) tail() string {
	switch {
	case c.Note == "":
		return ""
	case !c.shifted():
		return "корректор: " + c.Note + ", " + c.Warn
	default:
		return fmt.Sprintf("корректор: %s, %s -> %s", c.Note, c.From, c.Model)
	}
}

// correctModel двигает модель вердикта по лестнице ярусов, опираясь на остаток
// лимитов. Порядок правил значим: дефицит проверяется раньше профицита, потому
// что бакет тратится взвешенной ценой модели и сдвиг вверх при дефиците прожёг
// бы его быстрее. Шаг максимум один, каскадов нет.
func correctModel(model string, groom bool, s snapshot, now time.Time) correction {
	c := correction{Model: model, From: model}
	// Грумминговый вердикт это про порядок работы, а не про расход: сначала
	// снять неопределённость либо разрезать задачу, и остаток тут ни при чём.
	if groom {
		return c
	}
	i := tierIndex(model)
	if i < 0 {
		return c
	}
	if name := firstWithStatus(s, tierBuckets[model], statusDeficit, now); name != "" {
		c.Note = statusDeficit + " " + name
		if i == 0 {
			c.Warn = "ниже haiku ярусов нет"
			return c
		}
		c.Model, c.Down = tiers[i-1], true
		return c
	}
	if i+1 >= len(tiers) || !s.fresh(now) {
		return c
	}
	need := extraBuckets(tiers[i+1], model)
	if len(need) == 0 {
		// У соседей с одинаковым набором трат (haiku и sonnet) добавочного
		// бакета нет, и вверх двигает профицит того, из чего ярус уже тратит.
		need = tierBuckets[model]
	}
	if len(need) == 0 || !allWithStatus(s, need, statusSurplus, now) {
		return c
	}
	c.Note = statusSurplus + " " + strings.Join(need, ", ")
	c.Model = tiers[i+1]
	return c
}

func tierIndex(model string) int {
	for i, t := range tiers {
		if t == model {
			return i
		}
	}
	return -1
}

// extraBuckets это то, что ярус выше добавляет к тратам текущего.
func extraBuckets(upper, lower string) []string {
	var extra []string
	for _, name := range tierBuckets[upper] {
		if !contains(tierBuckets[lower], name) {
			extra = append(extra, name)
		}
	}
	return extra
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func firstWithStatus(s snapshot, names []string, status string, now time.Time) string {
	for _, name := range names {
		if b, ok := s.bucket(name); ok && b.status(now) == status {
			return name
		}
	}
	return ""
}

func allWithStatus(s snapshot, names []string, status string, now time.Time) bool {
	for _, name := range names {
		b, ok := s.bucket(name)
		if !ok || b.status(now) != status {
			return false
		}
	}
	return true
}

// cmdQuota печатает разобранный снимок: это окно в решения корректора, сдвиг в
// вердикте всегда можно объяснить, не читая файл руками.
func cmdQuota(path string, now time.Time) (string, error) {
	s, err := readSnapshot(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if s.Taken.IsZero() && len(s.Buckets) == 0 {
		fmt.Fprintf(&b, "снимка нет: %s\nвердикты pick идут без корректора, снимок пишется руками с панели /usage либо командой agentctl quota refresh", path)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "снимок %s\n", path)
	if s.Taken.IsZero() {
		b.WriteString("снят: момента снятия в файле нет, вверх корректор не двинет\n")
	} else {
		fmt.Fprintf(&b, "снят %s, возраст %s\n", s.Taken.Format(quotaTimeLayout), humanAge(now.Sub(s.Taken)))
	}
	for _, bk := range s.Buckets {
		status := bk.status(now)
		note := ""
		if status == statusSurplus && !s.fresh(now) {
			note = " (снимок старше суток, вверх не двигает)"
		}
		fmt.Fprintf(&b, "%s: потрачено %d%%, сброс %s, pace %.1f, %s%s\n",
			bk.Name, int(math.Round(bk.Used*100)), bk.Reset.Format(quotaTimeLayout), bk.pace(now), status, note)
	}
	for _, w := range s.Warns {
		fmt.Fprintf(&b, "предупреждение: %s\n", w)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func humanAge(d time.Duration) string {
	if d < 0 {
		return "снят позже текущего времени"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h >= 24:
		return fmt.Sprintf("%dд %dч", h/24, h%24)
	case h > 0:
		return fmt.Sprintf("%dч %dм", h, m)
	default:
		return fmt.Sprintf("%dм", m)
	}
}

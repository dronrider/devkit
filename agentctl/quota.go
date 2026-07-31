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

// Снимок старше этого протух: pick говорит про возраст вслух, и профицит по
// такому снимку не применяется. Порог тут один на оба случая не случайно.
// Разведи их, и нашлось бы окно, где pick одной строкой зовёт переснять
// снимок, а другой поднимает по нему вердикт. Величина взята из темпа расхода:
// он доходит до 10% подписки в час, то есть pace уезжает примерно на 0.2 за
// час, и за сорок пять минут набегает десятая часть зазора между порогами
// статуса. Асимметрия при этом никуда не делась: дефицит возрастом не
// ограничен вовсе, снятый остаток это верхняя граница текущего, и вниз сдвиг
// идёт по снимку любой давности, лишь бы не прошла дата сброса.
const snapshotMaxAge = 45 * time.Minute

const (
	statusSurplus = "профицит"
	statusNormal  = "норма"
	statusDeficit = "дефицит"
	statusExpired = "протух"
)

// Известные бакеты панели /usage в порядке вывода. Пятичасовой сессионный сюда
// не попадает: он протухает быстрее, чем живёт задача, снимок его не догонит.
// week_opus остаётся ради снимков и панелей старых клиентов: с 2.1.220 панель
// показывает вместо него бакет Fable, но снимок пишется и руками, а панель у
// разных версий клиента и тарифов своя.
var knownBuckets = []string{"week_all", "week_fable", "week_opus"}

// Бакет, без которого снимка нет: общий показывает любая панель с недельными
// лимитами, и тратят из него все ярусы.
const requiredBucket = "week_all"

// Лестница ярусов остаётся данными, корректор двигает индекс: новая модель
// вставляется строкой сюда и строкой в таблицу трат, формула не меняется.
var tiers = []string{"haiku", "sonnet", "opus", "fable"}

// Из каких бакетов тратит ярус. week_all по смыслу панели учитывает все модели,
// а отдельным бакетом панель добирает самую дорогую: раньше это был Opus, с
// 2.1.220 это Fable. Отдельного бакета у opus больше нет, и от sonnet он
// отличается только взвешенной ценой расхода общего бакета; week_fable держит
// один верхний ярус. Старый week_opus в таблицу не входит: снимок с ним
// читается и показывается в quota, но лестницу трат он больше не задаёт.
var tierBuckets = map[string][]string{
	"haiku":  {"week_all"},
	"sonnet": {"week_all"},
	"opus":   {"week_all"},
	"fable":  {"week_all", "week_fable"},
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
	return !s.Taken.IsZero() && now.Sub(s.Taken) <= snapshotMaxAge
}

// empty это снимок, которого нет: ни момента снятия, ни бакетов. Отсутствие
// файла readSnapshot отдаёт именно так, ошибкой оно не считается.
func (s snapshot) empty() bool { return s.Taken.IsZero() && len(s.Buckets) == 0 }

// ageWarn говорит, что не так с самим снимком. Молчать тут нельзя: корректор
// без снимка выключен целиком, а вердикт выглядит совершенно штатным, и то,
// что модель выбрана без оглядки на остаток лимитов, ниоткуда не видно.
func (s snapshot) ageWarn(path string, now time.Time) string {
	switch age := now.Sub(s.Taken); {
	case s.empty():
		return fmt.Sprintf("снимка квоты нет (%s), вердикт идёт без корректора; снять: agentctl quota refresh", path)
	case s.Taken.IsZero():
		return "в снимке квоты нет момента снятия, возраст неизвестен, вверх корректор не двинет; переснять: agentctl quota refresh"
	case age > snapshotMaxAge:
		return fmt.Sprintf("снимок квоты снят %s назад при пороге %s, вверх корректор не двинет; переснять: agentctl quota refresh",
			humanAge(age), humanAge(snapshotMaxAge))
	}
	return ""
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
	Note  string // «дефицит week_all», причина в человеческом виде
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
	// Вверх двигает профицит всего, из чего будет тратить ярус выше, а не
	// одного добавочного бакета. Иначе подъём opus -> fable упирался бы в один
	// week_fable и случался при общем бакете у самой границы дефицита, то есть
	// на самой дорогой модели решение принималось бы, ничего не зная про запас
	// общего бакета.
	need := tierBuckets[tiers[i+1]]
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

// spentByTier отвечает, тратит ли из бакета хоть один ярус. Снимок переживает
// смену панели: week_opus остаётся и в старых файлах, и на машинах со старым
// клиентом. Молча печатать его наравне с рабочими нельзя, иначе пользователь
// видит «week_opus: дефицит», ждёт сдвига вниз и не получает его, а причины в
// выводе нет.
func spentByTier(name string) bool {
	for _, tier := range tiers {
		if contains(tierBuckets[tier], name) {
			return true
		}
	}
	return false
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
	if s.empty() {
		fmt.Fprintf(&b, "снимка нет: %s\nвердикты pick идут без корректора, снимок пишется руками с панели /usage либо командой agentctl quota refresh", path)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "снимок %s\n", path)
	switch age := now.Sub(s.Taken); {
	case s.Taken.IsZero():
		b.WriteString("снят: момента снятия в файле нет, вверх корректор не двинет\n")
	case age < 0:
		fmt.Fprintf(&b, "снят %s, это позже текущего времени: часы разошлись\n", s.Taken.Format(quotaTimeLayout))
	case age > snapshotMaxAge:
		fmt.Fprintf(&b, "снят %s, возраст %s при пороге %s: протух, вверх корректор не двинет\n",
			s.Taken.Format(quotaTimeLayout), humanAge(age), humanAge(snapshotMaxAge))
	default:
		fmt.Fprintf(&b, "снят %s, возраст %s\n", s.Taken.Format(quotaTimeLayout), humanAge(age))
	}
	for _, bk := range s.Buckets {
		status := bk.status(now)
		note := ""
		switch {
		case !spentByTier(bk.Name):
			note = " (лестницу трат не задаёт, панель его больше не показывает)"
		case status == statusSurplus && !s.fresh(now):
			note = " (снимок протух, вверх не двигает)"
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

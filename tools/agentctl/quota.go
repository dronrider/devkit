package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Снимок остатка лимитов лежит на уровне машины, а не проекта: лимиты общие на
// подписку, и per-project копии протухали бы вразнобой. Файл на харнес: лимиты
// у инструментов свои, и одним файлом их не описать.
const (
	quotaDirName    = "quota"
	quotaFileSuffix = ".local"
	// Одиночный снимок, каким он был до директории. Читается, пока переезд не
	// сделал devkitctl doctor --fix.
	legacyQuotaFileName = "quota.local"
	// Чей это был снимок: снять его было нечем, кроме панели Claude Code, и
	// чужой харнес со старым файлом читал бы остаток первой подписки под своим
	// именем. Тот же владелец назван в devkitctl (QUOTA_LEGACY_HARNESS).
	legacyHarness = "claude-code"
)

// Момент снятия и даты сброса пишутся местным временем без секунд: столько же
// точности показывает панель /usage.
const quotaTimeLayout = "2006-01-02T15:04"

// partialNote это комментарий, которым снимок помечает бакет, которого панель
// не показала. Комментарий, а не свой ключ, и это не мелочь оформления. Формат
// снимка объявляет бакетом всё, что не taken, и читатели помимо agentctl
// разбирают файл сами (дашборд по решению LLD DK-112 читает каталог с диска).
// Новый ключ они принимают за бакет и показывают человеку «бакет partial не
// разобран» вместо остатка. Строку с решёткой пропускают все, и место для
// пометки в этом формате ровно тут.
const partialNote = "# partial "

// borrowedNote это комментарий про бакет, который в снимке есть, но своей цифры
// не принёс: панель разбивку не показала, и число занято у кеша клиента. От
// partialNote пометка отделена сознательно. Тот говорит «бакета нет, и вот
// почему», и оба читателя, корректор и печать остатка, ведут себя по этому
// слову: печатают «в панели его не было» и подставляют пометку вместо цифры. Об
// присутствующий бакет обе фразы ломаются, человек видит «week_max 17%» и
// строкой ниже «week_max в панели его не было», а корректор возраст занятой
// цифры теряет вовсе. Комментарий, а не свой ключ, по тому же доводу, что у
// partialNote: сторонний читатель снимка примет новый ключ за бакет.
const borrowedNote = "# borrowed "

// Окно бакета, от него считается равномерный темп расхода. Берётся из префикса
// имени: недельные лимиты подписки это week_*, потокенный бюджет чаще месячный,
// и с недельным окном pace по нему врал бы всемеро. Префикс проверяет валидатор
// профиля, поэтому чужого тут не бывает.
//
// Третий префикс, с числом в имени, это пятичасовое окно второй подписки.
// DK-090 его не заводил с оговоркой «добавится осознанной правкой, когда
// пятичасовой цикл начнёт бить чаще недельного», и ровно это случилось: окно
// сгорело до потолка за одну задачу, а конвейер молчал, потому что снимка
// второй подписки не было вовсе.
const (
	weekWindow  = 7 * 24 * time.Hour
	monthWindow = 30 * 24 * time.Hour
	window5hLen = 5 * time.Hour
)

var bucketWindows = []struct {
	Prefix string
	Window time.Duration
}{
	{"week_", weekWindow},
	{"month_", monthWindow},
	{"window5h_", window5hLen},
}

// bucketPrefix отвечает, известен ли префикс имени; пустая строка значит, что
// окно по имени не выводится, и такое имя валидатор профиля не пропускает.
func bucketPrefix(name string) string {
	for _, w := range bucketWindows {
		if strings.HasPrefix(name, w.Prefix) {
			return w.Prefix
		}
	}
	return ""
}

func bucketWindow(name string) time.Duration {
	for _, w := range bucketWindows {
		if strings.HasPrefix(name, w.Prefix) {
			return w.Window
		}
	}
	return weekWindow
}

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

// Старые имена бакетов. Панель Claude Code до 2.1.220 добирала самой дорогой
// моделью Opus, потом Fable, а бакеты devkit зовутся ярусами лестницы, и
// снимок с прежним именем ломать об незнакомый ключ нельзя: снимки пишутся и
// руками, и лежат на машинах со старым клиентом. week_opus псевдонимом не
// стал, он остаётся отдельным известным бакетом вне лестницы трат: тот снимок
// описывает другой бакет, а не week_max под другим именем.
var bucketAliases = map[string]string{"week_fable": "week_max"}

func canonBucket(name string) string {
	if v, ok := bucketAliases[name]; ok {
		return v
	}
	return name
}

// quotaSpec это объявление квоты из [quota] профиля активного харнеса плюс
// путь снимка. Формула корректора работает по именам бакетов и про инструмент
// не знает ничего: какие бакеты бывают, какой обязателен и из каких тратит
// ярус, говорит профиль.
type quotaSpec struct {
	Harness  string
	Dir      string // директория профилей, от неё считается путь съёмщика
	Snap     string
	Script   string
	Buckets  []string
	Required string
	Spend    map[string][]string
	// Расход в деньгах: съёмщику передаётся бюджет из машинного конфига.
	BudgetBased bool
	Budget      int
	// Каталог машинного хозяйства харнеса: сменному съёмщику он передаётся
	// переменной окружения, токен подписки лежит в настройках клиента внутри
	// этого каталога, и разносить два места под один каталог нельзя.
	Home string
	// Path это куда refresh пишет снимок, From это откуда он читается. Пока
	// переезд не сделан, читается старый одиночный файл, а пишется всё равно
	// новый путь.
	Path string
	From string
}

const (
	snapUsagePane = "usage-pane"
	snapScript    = "script"
)

func (q *quotaSpec) legacy() bool { return q != nil && q.From != q.Path }

// legacyWarn говорит, что снимок читается по старому пути. Молчать нельзя:
// после переезда файл сменится, а вердикт выглядел бы прежним.
func (q *quotaSpec) legacyWarn() string {
	if !q.legacy() {
		return ""
	}
	return fmt.Sprintf("снимок квоты читается по старому пути %s, переезд в %s делает devkitctl doctor --fix",
		q.From, q.Path)
}

// known отвечает, знает ли профиль такой бакет. Имя приводится к каноничному:
// старый снимок читается наравне с сегодняшним.
func (q *quotaSpec) known(name string) bool {
	return inList(q.Buckets, canonBucket(name))
}

// spentByTier отвечает, тратит ли из бакета хоть один ярус. Известный бакет вне
// лестницы трат бывает штатно (week_opus со старой панели), и молча печатать его
// наравне с рабочими нельзя: пользователь видит «week_opus: дефицит», ждёт
// сдвига вниз и не получает его, а причины в выводе нет.
func (q *quotaSpec) spentByTier(name string) bool {
	for _, tier := range tierNames {
		if contains(q.Spend[tier], name) {
			return true
		}
	}
	return false
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
// Partial это бакеты, которых в снимке нет не потому, что их нет у подписки, а
// потому, что панель их не показала, и причина рядом с именем: без неё
// «week_max в снимке нет» звучит одинаково у подписки без дорогого бакета и у
// панели, которая отказала по частоте обращений.
// Borrowed это бакеты, которые в снимке есть, но цифру принесли не свою, и
// откуда она взялась: панель отдаёт разбивку по моделям не всегда, а терять её
// на каждом таком отказе значит возвращать человека в кабинет подписки.
type snapshot struct {
	Taken    time.Time
	Buckets  []bucket
	Partial  map[string]string
	Borrowed map[string]string
	Warns    []string
}

// partial отвечает, почему названного бакета нет в снимке. Пустая строка это
// «панель досчитала, и такого бакета у подписки просто нет».
func (s snapshot) partial(name string) string { return s.Partial[name] }

// partialNames это имена помеченных бакетов в порядке имени: снимок и вывод
// читают люди, и порядок строк не должен плясать от прохода по карте.
func (s snapshot) partialNames() []string { return sortedNames(s.Partial) }

// borrowed отвечает, откуда у названного бакета цифра, если она не своя. Пустая
// строка это «цифра своя».
func (s snapshot) borrowed(name string) string { return s.Borrowed[name] }

// borrowedNames это имена бакетов с занятой цифрой в порядке имени.
func (s snapshot) borrowedNames() []string { return sortedNames(s.Borrowed) }

// sortedNames это ключи карты пометок по алфавиту: снимок и вывод читают люди,
// и порядок строк не должен плясать от прохода по карте.
func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// markPartial помечает бакеты, которых в кадре не оказалось, когда панель сама
// сказала, что разбивки по моделям не покажет. Помечаются только бакеты
// лестницы трат: week_opus вне её, панель его давно не рисует, и пометка про
// него была бы шумом.
func (s snapshot) markPartial(q *quotaSpec, why string) snapshot {
	if why == "" {
		return s
	}
	for _, name := range q.Buckets {
		if !q.spentByTier(name) {
			continue
		}
		if _, ok := s.bucket(name); ok {
			continue
		}
		if s.Partial == nil {
			s.Partial = map[string]string{}
		}
		s.Partial[name] = why
	}
	return s
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
// экономии. Момент снятия позже текущего времени это та же неизвестность:
// часы разошлись, и «возраст ноль» тут ничего не значит, снимок мог быть снят
// когда угодно.
func (s snapshot) fresh(now time.Time) bool {
	if s.Taken.IsZero() || s.Taken.After(now) {
		return false
	}
	return now.Sub(s.Taken) <= snapshotMaxAge
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
	case age < 0:
		return fmt.Sprintf("снимок квоты снят %s, это позже текущего времени: часы разошлись, вверх корректор не двинет; переснять: agentctl quota refresh",
			s.Taken.Format(quotaTimeLayout))
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
	return (1 - b.Used) / (float64(left) / float64(bucketWindow(b.Name)))
}

// status у бакета. Зазор между порогами широкий сознательно: корректор должен
// срабатывать на выраженный перекос, а не дёргать вердикт на каждом колебании.
func (b bucket) status(now time.Time) string {
	// Окно без сброса это нетронутое окно, а не протухшее: отсчёт в нём ещё не
	// начат, остаток полный, и звать это протуханием значило бы резать вердикт
	// там, где квота цела.
	if b.Reset.IsZero() {
		return statusNormal
	}
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

// devkitDir это ~/.devkit. HOME берётся на каждый вызов, чтобы тесты
// подкладывали свои файлы, не трогая настоящие.
func devkitDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".devkit")
}

func quotaPath(harness string) string {
	return filepath.Join(devkitDir(), quotaDirName, harness+quotaFileSuffix)
}

func legacyQuotaPath() string {
	return filepath.Join(devkitDir(), legacyQuotaFileName)
}

// snapshotSource выбирает, откуда читать снимок: путь по имени харнеса, а пока
// переезда не было, старый одиночный файл, и только у его владельца: чужой
// харнес со старым файлом читал бы остаток первой подписки под пометкой своего
// имени. Нет ни того ни другого, значит читается новый путь, и вся речь про
// отсутствие снимка идёт про него.
func snapshotSource(harness string) (path, from string) {
	path = quotaPath(harness)
	if _, err := os.Stat(path); err == nil {
		return path, path
	}
	if harness != legacyHarness {
		return path, path
	}
	legacy := legacyQuotaPath()
	if _, err := os.Stat(legacy); err == nil {
		return path, legacy
	}
	return path, path
}

// read разбирает файл снимка. Формат плоский, как в .devkit/deploy.local:
// строки key = value, # это комментарий. Нет файла, значит пустой снимок без
// ошибки: корректор тогда молчит.
func (q *quotaSpec) read() (snapshot, error) {
	var s snapshot
	data, err := os.ReadFile(q.From)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	return q.parse(string(data)), nil
}

// parse разбирает текст снимка. Им же разбирается вывод сменного съёмщика:
// второго формата у снимка нет, и что попадёт в файл, решает не скрипт.
func (q *quotaSpec) parse(text string) snapshot {
	var s snapshot
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if rest, marked := strings.CutPrefix(line, partialNote); marked {
				if name, why, ok := q.parseNote(rest); ok {
					if s.Partial == nil {
						s.Partial = map[string]string{}
					}
					s.Partial[name] = why
				}
				continue
			}
			if rest, marked := strings.CutPrefix(line, borrowedNote); marked {
				if name, why, ok := q.parseNote(rest); ok {
					if s.Borrowed == nil {
						s.Borrowed = map[string]string{}
					}
					s.Borrowed[name] = why
				}
			}
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
		// Ключ partial это как снимок писала первая правка DK-584. Своих файлов
		// она наплодила на машинах, и читать их надо, пока refresh не перепишет
		// каждый; пишется пометка только комментарием.
		if key == "partial" {
			if name, why, ok := q.parseNote(val); ok {
				if s.Partial == nil {
					s.Partial = map[string]string{}
				}
				s.Partial[name] = why
			}
			continue
		}
		if !q.known(key) {
			s.Warns = append(s.Warns, fmt.Sprintf("неизвестный ключ снимка %q, пропущен", key))
			continue
		}
		b, err := parseBucket(canonBucket(key), val)
		if err != nil {
			s.Warns = append(s.Warns, fmt.Sprintf("бакет %s не разобран: %v", key, err))
			continue
		}
		s.Buckets = append(s.Buckets, b)
	}
	return s
}

// bucketWhen словами говорит про сброс окна. Нетронутое окно сброса не имеет
// вовсе, и печатать за него нулевую дату (0001-01-01) значило бы показывать
// человеку мусор вместо честного «отсчёт не начат».
func bucketWhen(b bucket) string {
	if b.Reset.IsZero() {
		return "отсчёт до сброса не начат"
	}
	return "сброс " + b.Reset.Format(quotaTimeLayout)
}

// parseNote разбирает «<бакет>: причина», тело обеих пометок. Незнакомое имя и
// пустая причина проходят молча: пометка это подпись для человека, и ломать об
// неё разбор снимка нельзя, бакеты в файле от неё не зависят.
func (q *quotaSpec) parseNote(val string) (name, why string, ok bool) {
	name, why, cut := strings.Cut(val, ":")
	name, why = canonBucket(strings.TrimSpace(name)), strings.TrimSpace(why)
	if !cut || why == "" || !q.known(name) {
		return "", "", false
	}
	return name, why, true
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
	// Нетронутое окно сброса не имеет вовсе: пока из него не потратили ни
	// кредита, отсчёт не начат, и съёмщик пишет один процент. Прежде такая
	// строка была поломкой разбора, и снимок подписки не обновлялся целиком,
	// пока в окне не появится расход (живой случай выката).
	if rest == "" {
		return bucket{Name: name, Used: float64(n) / 100}, nil
	}
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

// write кладёт снимок тем же форматом, каким его пишут руками: refresh это
// автоматизация заполнения, а не второй формат. Пишется всегда новый путь, даже
// когда читался старый: переезд идёт в одну сторону. Запись атомарная, файл
// читают параллельные сессии, и половина снимка читалась бы как битые строки.
func (q *quotaSpec) write(s snapshot) error {
	var b strings.Builder
	fmt.Fprintf(&b, "taken = %s\n", s.Taken.Format(quotaTimeLayout))
	for _, bk := range s.Buckets {
		if bk.Reset.IsZero() {
			fmt.Fprintf(&b, "%s = %d%%\n", bk.Name, int(math.Round(bk.Used*100)))
			continue
		}
		fmt.Fprintf(&b, "%s = %d%% сброс %s\n", bk.Name, int(math.Round(bk.Used*100)), bk.Reset.Format(quotaTimeLayout))
	}
	for _, name := range s.partialNames() {
		fmt.Fprintf(&b, "%s%s: %s\n", partialNote, name, s.Partial[name])
	}
	for _, name := range s.borrowedNames() {
		fmt.Fprintf(&b, "%s%s: %s\n", borrowedNote, name, s.Borrowed[name])
	}
	dir := filepath.Dir(q.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(q.Path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), q.Path); err != nil {
		return err
	}
	q.From = q.Path
	return nil
}

// keepNewer сторожит время снимка от хода назад. Источник расхода бывает
// откатным: кеш клиента переписывает всякая живая сессия своей копией в памяти,
// и долго висящее окно кладёт туда цифры трёхчасовой давности поверх
// пятиминутных. Записать такое значит на глазах у человека сменить 82% в 21:19
// на 79% в 17:39 и обвинить в этом экран (живой случай пользователя). Снятое
// старше лежащего на диске в файл не идёт, а причина уходит словами: молча
// пропущенная запись выглядела бы отказом снимать.
//
// Снимок из будущего сторожем не считается: часы могли разойтись, и такой файл
// иначе запер бы обновление до самого сброса.
func (q *quotaSpec) keepNewer(fresh snapshot, now time.Time) (snapshot, string) {
	old, err := q.read()
	if err != nil || old.empty() || old.Taken.IsZero() || fresh.Taken.IsZero() {
		return fresh, ""
	}
	if old.Taken.After(now) || !old.Taken.After(fresh.Taken) {
		return fresh, ""
	}
	return old, fmt.Sprintf("снятое (%s) старше снимка на диске (%s): источник расхода откатился назад, цифры на диске оставлены прежними",
		fresh.Taken.Format(quotaTimeLayout), old.Taken.Format(quotaTimeLayout))
}

// correction это решение корректора: с какого яруса на какой съехал вердикт и
// почему. From равен Tier, когда сдвига не было.
type correction struct {
	Tier string
	From string
	Note string // «дефицит week_all», причина в человеческом виде
	Warn string // почему причина есть, а сдвига нет
	Down bool
	// Сдвиг есть, а модель после разворачивания та же: маппинг харнеса сложил
	// соседние ярусы в одну модель. Ставит это cmdPick, корректор про модели
	// ничего не знает.
	SameModel bool
}

func (c correction) shifted() bool { return c.From != "" && c.From != c.Tier }

// tail это хвост человеческой строки вердикта. Без причины хвоста нет: молчание
// корректора не должно занимать место в выводе.
func (c correction) tail() string {
	switch {
	case c.Note == "":
		return ""
	case !c.shifted():
		return "корректор: " + c.Note + ", " + c.Warn
	case c.SameModel:
		return fmt.Sprintf("корректор: %s, сдвиг %s -> %s, модель та же", c.Note, c.From, c.Tier)
	default:
		return fmt.Sprintf("корректор: %s, %s -> %s", c.Note, c.From, c.Tier)
	}
}

// quotaFacts это состояние квоты на момент решения: проценты бакетов, из
// которых тратят исходный и итоговый ярусы, возраст снимка и был ли сдвиг. Со
// сдвигом эти данные и так видны хвостом корректора, а без сдвига до них
// добраться неоткуда, и три разных случая (корректора нет, снимка нет, снимок
// в норме) выглядят одинаково.
type quotaFacts struct {
	Off     string // почему остаток не смотрели; пусто, когда снимок читался
	Age     string // «снимок 1ч 5м назад», «снимок 3ч 0м назад, протух»
	Buckets []string
	Shifted bool
	Groom   bool
	// Про то же самое уже сказано предупреждением вердикта. В файл задачи строка
	// идёт всё равно: предупреждений там нет, а состояние нужно и там.
	Warned bool
}

func (f quotaFacts) note() string {
	if f.Off != "" {
		return "квота: " + f.Off
	}
	if f.Age == "" && len(f.Buckets) == 0 {
		return ""
	}
	parts := append([]string{}, f.Buckets...)
	parts = append(parts, f.Age)
	switch {
	case f.Groom:
		parts = append(parts, "вердикт грумминговый, корректор его не двигает")
	case f.Shifted:
		// Куда и почему съехал ярус, сказано хвостом корректора рядом, второй раз
		// это же не повторяем.
	default:
		parts = append(parts, "сдвига нет")
	}
	return "квота: " + strings.Join(parts, ", ")
}

// qualifyBucket приписывает бакету харнес, когда бакет не активного харнеса:
// на гетерогенной лестнице соседние ступени платят из разных подписок, и голое
// имя не отвечает, в какую из них вердикт упёрся. Домашние бакеты остаются
// голыми, и однородный вывод не двигается ни на байт.
func qualifyBucket(harness, active, name string) string {
	if harness == "" || harness == active {
		return name
	}
	return harness + "/" + name
}

// quotaFactsOf снимает состояние квоты для вердикта. Бакеты берутся те, из
// которых тратят исходный и итоговый ярусы: при сдвиге вниз причина лежит в
// бакете исходного, а знать надо и то, чем будет платить итоговый.
func quotaFactsOf(q *quotaSpec, s snapshot, c correction, groom bool, now time.Time, active string) quotaFacts {
	if s.empty() {
		return quotaFacts{Off: "снимка нет, корректор выключен"}
	}
	f := quotaFacts{Age: snapshotAge(s, now), Shifted: c.shifted(), Groom: groom}
	for _, name := range union(q.Spend[c.From], q.Spend[c.Tier]) {
		shown := qualifyBucket(q.Harness, active, name)
		b, ok := s.bucket(name)
		if !ok {
			miss := shown + " в снимке нет"
			if why := s.partial(name); why != "" {
				miss += ", " + why
			}
			f.Buckets = append(f.Buckets, miss)
			continue
		}
		part := fmt.Sprintf("%s %d%%", shown, int(math.Round(b.Used*100)))
		if st := b.status(now); st != statusNormal {
			part += " " + st
		}
		// Занятая цифра едет к корректору с возрастом: под свежим моментом
		// снятия она читалась бы свежей, и вердикт двигался бы по четырёхчасовым
		// процентам как по сегодняшним.
		if why := s.borrowed(name); why != "" {
			part += " (" + why + ")"
		}
		f.Buckets = append(f.Buckets, part)
	}
	return f
}

// snapshotAge это возраст снимка человеческими словами. Отдельно называются
// случаи, где возрасту верить нельзя: без них «сдвига нет» читалось бы как
// «остаток посмотрели и решили не двигать».
func snapshotAge(s snapshot, now time.Time) string {
	switch age := now.Sub(s.Taken); {
	case s.Taken.IsZero():
		return "момент снятия неизвестен"
	case age < 0:
		return "снимок из будущего, часы разошлись"
	case age > snapshotMaxAge:
		return "снимок " + humanAge(age) + " назад, протух"
	default:
		return "снимок " + humanAge(age) + " назад"
	}
}

func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, v := range b {
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// correctTier двигает ярус вердикта по лестнице, опираясь на остаток лимитов.
// Порядок правил значим: дефицит проверяется раньше профицита, потому что бакет
// тратится взвешенной ценой модели и сдвиг вверх при дефиците прожёг бы его
// быстрее. Шаг максимум один, каскадов нет.
func correctTier(q *quotaSpec, tier string, groom bool, s snapshot, now time.Time) correction {
	c := correction{Tier: tier, From: tier}
	// Грумминговый вердикт это про порядок работы, а не про расход: сначала
	// снять неопределённость либо разрезать задачу, и остаток тут ни при чём.
	if groom {
		return c
	}
	i := tierIndex(tier)
	if i < 0 {
		return c
	}
	if name := firstWithStatus(s, q.Spend[tier], statusDeficit, now); name != "" {
		c.Note = statusDeficit + " " + name
		if i == 0 {
			c.Warn = "ниже mini ярусов нет"
			return c
		}
		c.Tier, c.Down = tierNames[i-1], true
		return c
	}
	if i+1 >= len(tierNames) || !s.fresh(now) {
		return c
	}
	// Вверх двигает профицит всего, из чего будет тратить ярус выше, а не
	// одного добавочного бакета. Иначе подъём pro -> max упирался бы в один
	// week_fable и случался при общем бакете у самой границы дефицита, то есть
	// на самой дорогой ступени решение принималось бы, ничего не зная про запас
	// общего бакета.
	need := q.Spend[tierNames[i+1]]
	if len(need) == 0 || !allWithStatus(s, need, statusSurplus, now) {
		return c
	}
	c.Note = statusSurplus + " " + strings.Join(need, ", ")
	c.Tier = tierNames[i+1]
	return c
}

func tierIndex(tier string) int {
	for i, t := range tierNames {
		if t == tier {
			return i
		}
	}
	return -1
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
func cmdQuota(q *quotaSpec, now time.Time) (string, error) {
	s, err := q.read()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if s.empty() {
		fmt.Fprintf(&b, "снимка нет: %s\nвердикты pick идут без корректора, снимок пишется руками с панели /usage либо командой agentctl quota refresh", q.From)
		return b.String(), nil
	}
	fmt.Fprintf(&b, "снимок %s (харнес %s)\n", q.From, q.Harness)
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
		case !q.spentByTier(bk.Name):
			note = " (лестницу трат не задаёт, панель его больше не показывает)"
		case status == statusSurplus && !s.fresh(now):
			note = " (снимок протух, вверх не двигает)"
		}
		if why := s.borrowed(bk.Name); why != "" {
			note += " (" + why + ")"
		}
		fmt.Fprintf(&b, "%s: потрачено %d%%, %s, pace %.1f, %s%s\n",
			bk.Name, int(math.Round(bk.Used*100)), bucketWhen(bk), bk.pace(now), status, note)
	}
	for _, name := range s.partialNames() {
		fmt.Fprintf(&b, "%s: в панели его не было, %s\n", name, s.Partial[name])
	}
	if w := q.legacyWarn(); w != "" {
		fmt.Fprintf(&b, "предупреждение: %s\n", w)
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

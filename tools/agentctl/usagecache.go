package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Кеш расхода клиента Claude Code. Панель /usage рисует не свои цифры: ответ
// эндпоинта расхода клиент кладёт в свой .claude.json под ключом
// cachedUsageUtilization и оттуда же берёт разбивку по моделям, когда свежего
// ответа нет. Снимок читает тот же файл, и разметка панели ему больше не
// нужна: за сутки её правили дважды (DK-574, DK-584), а на экране всё это
// время стояли цифры из кеша.
const (
	usageCacheFile = ".claude.json"
	usageCacheKey  = "cachedUsageUtilization"
)

// usageCache это те поля ответа, которые нужны снимку. Файл чужой, полей в нём
// на порядок больше, и разбирать их целиком незачем: лишнее json пропускает
// сам, а пропажу нужного разбор называет словами.
type usageCache struct {
	FetchedAtMs int64           `json:"fetchedAtMs"`
	Utilization *usageCacheUtil `json:"utilization"`
}

type usageCacheUtil struct {
	Limits []usageCacheLimit `json:"limits"`
}

// usageCacheLimit это строка панели: чей лимит, сколько процентов съедено и
// когда он сбрасывается. Общий недельный приходит с kind = weekly_all,
// добавочный с kind = weekly_scoped и моделью в scope, пятичасовая сессия с
// kind = session.
type usageCacheLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// usageModelBuckets переводит имя модели в имя бакета. Словарь один на обоих
// съёмщиков: панель называет добавочный бакет моделью в заголовке секции, кеш
// тем же именем в scope.model.display_name, и разъехаться этим двум местам
// негде. Порядок важен: перебор идёт вхождением подстроки.
var usageModelBuckets = []struct{ Model, Bucket string }{
	{"opus", "week_opus"},
	{"fable", "week_max"},
}

func usageModelBucket(name string) (string, bool) {
	low := strings.ToLower(name)
	for _, m := range usageModelBuckets {
		if strings.Contains(low, m.Model) {
			return m.Bucket, true
		}
	}
	return "", false
}

// usageCachePath это файл клиента под домом харнеса. У первой подписки дом
// обычный, у второй свой каталог конфигурации, и кеш лежит там же.
func usageCachePath(q *quotaSpec) (string, error) {
	home := q.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	return filepath.Join(home, usageCacheFile), nil
}

// snapUsageCache собирает снимок из кеша клиента. Отказ тут не беда: дорога у
// снимка остаётся вторая, поэтому причина возвращается словами и уходит в
// вывод, а не теряется.
func snapUsageCache(q *quotaSpec) (snapshot, []string, error) {
	path, err := usageCachePath(q)
	if err != nil {
		return snapshot{}, nil, fmt.Errorf("дом харнеса %s не найден (%v), кеш клиента читать негде", q.Harness, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return snapshot{}, nil, fmt.Errorf("кеш расхода %s не прочитан (%v)", path, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return snapshot{}, nil, fmt.Errorf("%s разобрать как json не вышло (%v)", path, err)
	}
	blob, ok := top[usageCacheKey]
	if !ok {
		return snapshot{}, nil, fmt.Errorf("в %s нет ключа %s: клиент ещё не спрашивал расход либо назвал кеш иначе", path, usageCacheKey)
	}
	var c usageCache
	if err := json.Unmarshal(blob, &c); err != nil {
		return snapshot{}, nil, fmt.Errorf("ключ %s в %s разобран не был (%v): формат клиента сменился", usageCacheKey, path, err)
	}
	// Момент удачного запроса обязателен. Взять вместо него время чтения файла
	// значит поставить свежую метку над цифрами неизвестного возраста, а это та
	// самая молчащая ложь, от которой уходила DK-584.
	if c.FetchedAtMs <= 0 {
		return snapshot{}, nil, fmt.Errorf("в кеше %s нет момента удачного запроса (fetchedAtMs), возраст цифр неизвестен", path)
	}
	if c.Utilization == nil || len(c.Utilization.Limits) == 0 {
		return snapshot{}, nil, fmt.Errorf("в кеше %s нет разбора лимитов (utilization.limits): формат клиента сменился", path)
	}
	s := snapshot{Taken: time.UnixMilli(c.FetchedAtMs).Local().Truncate(time.Minute)}
	var notes []string
	seen := map[string]bool{}
	for _, l := range c.Utilization.Limits {
		name, why := cacheBucketName(l)
		if why != "" {
			notes = append(notes, fmt.Sprintf("кеш клиента: %s", why))
			continue
		}
		if name == "" || !q.known(name) || seen[name] {
			continue
		}
		if l.Percent == nil {
			notes = append(notes, fmt.Sprintf("кеш клиента: у бакета %s нет процента, в снимок он не пошёл", name))
			continue
		}
		reset, err := time.Parse(time.RFC3339, l.ResetsAt)
		if err != nil {
			notes = append(notes, fmt.Sprintf("кеш клиента: у бакета %s не разобрана дата сброса (%q), в снимок он не пошёл", name, l.ResetsAt))
			continue
		}
		seen[name] = true
		s.Buckets = append(s.Buckets, bucket{Name: name, Used: *l.Percent / 100, Reset: reset.Local().Truncate(time.Minute)})
	}
	if _, ok := s.bucket(q.Required); !ok {
		return snapshot{}, notes, fmt.Errorf("в кеше %s не нашлось обязательного бакета %s", path, q.Required)
	}
	// Порядок бакетов берётся из профиля, а не из порядка ключей чужого файла:
	// снимок читают люди, и строки в нём не должны плясать от версии клиента.
	sort.SliceStable(s.Buckets, func(i, j int) bool {
		return slices.Index(q.Buckets, s.Buckets[i].Name) < slices.Index(q.Buckets, s.Buckets[j].Name)
	})
	return s.markPartial(q, cacheNoBreakdown(notes)), notes, nil
}

// cacheNoBreakdown объясняет, почему разбивке из кеша нельзя верить целиком.
// Пустая строка значит, что кеш разобран до последней строки, и пропажу
// дорогого бакета надо понимать буквально: трат по этой модели на неделе не
// было либо бакета у подписки нет. Ровно так же читается пустая причина у
// панели (panelNoBreakdown): здоровый источник, которому нечего показать, от
// источника, заговорившего незнакомо, отличается тут и там одинаково.
func cacheNoBreakdown(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return "часть строк кеша клиента разобрать не вышло, разбивка неполная"
}

// cacheBucketName переводит строку лимита в имя бакета. Пустое имя без причины
// значит «это не наш бакет» (пятичасовая сессия, чужая поверхность), причина
// значит, что клиент заговорил незнакомо и молчать об этом нельзя.
func cacheBucketName(l usageCacheLimit) (name, why string) {
	switch l.Kind {
	case "weekly_all":
		return "week_all", ""
	case "weekly_scoped":
		if l.Scope == nil || l.Scope.Model == nil || l.Scope.Model.DisplayName == "" {
			return "", "недельный лимит с областью, но без имени модели, бакета под него нет"
		}
		b, ok := usageModelBucket(l.Scope.Model.DisplayName)
		if !ok {
			return "", fmt.Sprintf("недельный лимит модели %q, бакета под неё devkit не знает", l.Scope.Model.DisplayName)
		}
		return b, ""
	case "session", "":
		// Пятичасовое окно первой подписки снимок не пишет: оно протухает
		// быстрее, чем живёт задача. Пустой вид приходит от строки, которую
		// клиент дорисовать не успел.
		return "", ""
	default:
		return "", fmt.Sprintf("лимит вида %q, такого devkit не знает", l.Kind)
	}
}

// snapClaudeUsage снимает расход у клиента Claude Code. Дорогу выбирает не
// профиль, а состояние кеша: свежий кеш это ровно тот источник, который рисует
// экран человека, он несёт разбивку по моделям и свой момент снятия, и клиента
// ради него поднимать не надо. Кеш сам себя не обновляет: клиент кладёт туда
// ответ, только когда ходит за расходом сам, и в тихий час цифры застывают на
// часы. Поэтому дорог две, и порог у них общий со снимком (snapshotMaxAge):
// пока кеш моложе порога, он и есть снимок, а как перевалил, съёмщик идёт на
// панель, которая цифры спрашивает заново. Панель остаётся запасной дорогой и
// там, где кеша нет вовсе: клиент туда ещё не ходил, дом другой, формат файла
// сменился. Почему дорога сменилась, съёмщик говорит словами: молча уехавший на
// панель снимок от кешевого не отличить.
func snapClaudeUsage(q *quotaSpec, now time.Time) (snapshot, []string, error) {
	c, notes, err := snapUsageCache(q)
	if err == nil && c.fresh(now) {
		return c, notes, nil
	}
	if err != nil {
		notes = append(notes, fmt.Sprintf("%v, снимаем панелью %s", err, usageCommand))
	} else {
		notes = append(notes, fmt.Sprintf("кеш клиента снят %s при пороге %s и обновится, только когда клиент сам сходит за расходом; снимаем панелью %s",
			cacheAge(c, now), humanAge(snapshotMaxAge), usageCommand))
	}
	p, perr := snapUsagePanel(q, now)
	if perr != nil {
		if err != nil {
			return snapshot{}, notes, fmt.Errorf("%v Кеш клиента до этого тоже не дался: %v.", perr, err)
		}
		// Панель отказала, а протухший кеш есть. Он и уезжает в файл: свой
		// момент снятия он везёт с собой, и возраст цифр виден и корректору, и
		// человеку. Отказать тут значило бы оставить на диске снимок ещё старше.
		notes = append(notes, fmt.Sprintf("панель не далась (%v), в снимок идёт кеш клиента возрастом %s", perr, cacheAge(c, now)))
		return c, notes, nil
	}
	p, borrowed := borrowBreakdown(q, p, c, now, err == nil)
	return p, append(notes, borrowed...), nil
}

// borrowBreakdown добирает в панельный снимок бакеты, которых панель не дала, а
// протухший кеш держит. Общие цифры берутся у панели, они свежие, а разбивку по
// моделям панель отдаёт не всегда: свои цифры по моделям клиент просит тем же
// запросом, что и общие, и при отказе по частоте обращений рисует их из этого
// самого кеша. То есть заимствование не выдумывает числа, а повторяет то, что в
// этот момент стоит у человека на экране, и возраст занятой цифры едет рядом
// пометкой: без неё «week_max: 17%» под свежим taken читалось бы свежим.
func borrowBreakdown(q *quotaSpec, p, c snapshot, now time.Time, haveCache bool) (snapshot, []string) {
	if !haveCache || c.Taken.IsZero() || c.Taken.After(now) {
		return p, nil
	}
	var notes []string
	age := cacheAge(c, now)
	for _, name := range q.Buckets {
		if !q.spentByTier(name) {
			continue
		}
		if _, ok := p.bucket(name); ok {
			continue
		}
		b, ok := c.bucket(name)
		if !ok {
			continue
		}
		p.Buckets = append(p.Buckets, b)
		// Панель, отказавшая в разбивке, помечает недостающий бакет своим
		// «его тут не было». Цифру мы нашли, и пометка эта устарела: оставить её
		// значит напечатать под цифрой строку, что цифры нет.
		delete(p.Partial, name)
		if p.Borrowed == nil {
			p.Borrowed = map[string]string{}
		}
		p.Borrowed[name] = fmt.Sprintf("панель разбивку не дала, цифра из кеша клиента возрастом %s", age)
		notes = append(notes, fmt.Sprintf("бакет %s панель не показала, взят из кеша клиента возрастом %s", name, age))
	}
	sort.SliceStable(p.Buckets, func(i, j int) bool {
		return slices.Index(q.Buckets, p.Buckets[i].Name) < slices.Index(q.Buckets, p.Buckets[j].Name)
	})
	return p, notes
}

// cacheAge это возраст кеша словами. Часы машины и метка клиента расходятся
// штатно, и «-3м назад» в такой строке читалось бы поломкой, поэтому будущее
// называется отдельно.
func cacheAge(c snapshot, now time.Time) string {
	if c.Taken.IsZero() {
		return "неизвестного возраста"
	}
	if c.Taken.After(now) {
		return "временем позже текущего, часы разошлись"
	}
	return humanAge(now.Sub(c.Taken)) + " назад"
}

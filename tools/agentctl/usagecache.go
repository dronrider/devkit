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
	return s.markPartial(q, "кеш клиента этой модели не знает"), notes, nil
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
// профиль, а наличие кеша: это ровно тот источник, который рисует экран
// человека, он несёт разбивку по моделям и свой момент снятия, и клиента ради
// него поднимать не надо. Панель остаётся запасной дорогой для машин, где кеша
// нет: клиент туда ещё не ходил, дом другой, формат файла сменился. Почему
// дорога сменилась, съёмщик говорит словами: молча уехавший на панель снимок от
// кешевого не отличить.
func snapClaudeUsage(q *quotaSpec, now time.Time) (snapshot, []string, error) {
	s, notes, err := snapUsageCache(q)
	if err == nil {
		return s, notes, nil
	}
	notes = append(notes, fmt.Sprintf("%v, снимаем панелью %s", err, usageCommand))
	p, perr := snapUsagePanel(q, now)
	if perr != nil {
		return snapshot{}, notes, fmt.Errorf("%v Кеш клиента до этого тоже не дался: %v.", perr, err)
	}
	return p, notes, nil
}

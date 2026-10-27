package taskform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const ScenarioMode = "сценарии/1"

type UserScenario struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
	Title  string `json:"title"`
}

type ScenarioRecommendation struct {
	IDs    []string `json:"ids"`
	Reason string   `json:"reason"`
}

type ScenarioSnapshot struct {
	Version        int                    `json:"version"`
	Record         string                 `json:"record"`
	Name           string                 `json:"name"`
	Question       string                 `json:"question"`
	Scenarios      []UserScenario         `json:"scenarios"`
	Recommendation ScenarioRecommendation `json:"recommendation"`
	Cards          string                 `json:"cards"`
	Current        []string               `json:"current"`
	ChoiceEvent    int                    `json:"choiceEvent"`
}

type ScenarioReply struct {
	Text     string `json:"text"`
	Snapshot string `json:"snapshot"`
	Event    int    `json:"event"`
	Result   string `json:"result"`
}

var scenarioIDRe = regexp.MustCompile(`^СЦ-([1-9][0-9]*)$`)
var scenarioRe = regexp.MustCompile(`^(СЦ-[1-9][0-9]*) \((слово пользователя|вывод из примера|предложение агента)\):\s*(\S.*)$`)
var scenarioReviewRe = regexp.MustCompile(`^пересмотр человеком (\d{4}-\d{2}-\d{2}):\s*(\S.*)$`)
var forkReviewRe = regexp.MustCompile(`^требует сверки (\d{4}-\d{2}-\d{2}):\s*(\S.*)$`)

func ScenarioNumber(id string) (int, error) {
	m := scenarioIDRe.FindStringSubmatch(id)
	if m == nil {
		return 0, fmt.Errorf("%q не ID сценария: нужен СЦ-N без ведущих нулей", id)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n == int(^uint(0)>>1) {
		return 0, fmt.Errorf("слишком большой ID сценария %s", id)
	}
	return n, nil
}

func ParseScenario(text string) (UserScenario, error) {
	m := scenarioRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return UserScenario{}, fmt.Errorf("вариант %q: нужен СЦ-N (происхождение): название", text)
	}
	if _, err := ScenarioNumber(m[1]); err != nil {
		return UserScenario{}, err
	}
	return UserScenario{ID: m[1], Origin: m[2], Title: m[3]}, nil
}

func (s UserScenario) String() string { return fmt.Sprintf("%s (%s): %s", s.ID, s.Origin, s.Title) }

func ScenarioIDs(text string) ([]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range strings.Split(text, ",") {
		id = strings.TrimSpace(id)
		if _, err := ScenarioNumber(id); err != nil {
			return nil, err
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	SortScenarioIDs(ids)
	return ids, nil
}

func SortScenarioIDs(ids []string) {
	sort.Slice(ids, func(i, j int) bool { a, _ := ScenarioNumber(ids[i]); b, _ := ScenarioNumber(ids[j]); return a < b })
}

func (f Fork) Scenarios() ([]UserScenario, ScenarioRecommendation, error) {
	if f.Mode != ScenarioMode {
		return nil, ScenarioRecommendation{}, fmt.Errorf("«%s» не список сценариев/1", f.Name)
	}
	out := []UserScenario{}
	seen := map[string]bool{}
	max := 0
	for _, o := range f.Options {
		s, err := ParseScenario(o)
		if err != nil {
			return nil, ScenarioRecommendation{}, err
		}
		if seen[s.ID] {
			return nil, ScenarioRecommendation{}, fmt.Errorf("повтор ID %s в «%s»", s.ID, f.Name)
		}
		seen[s.ID] = true
		n, _ := ScenarioNumber(s.ID)
		if n > max {
			max = n
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, ScenarioRecommendation{}, fmt.Errorf("список «%s» пуст", f.Name)
	}
	for _, sub := range f.Sub {
		if m := forkDoneRe.FindStringSubmatch(sub); m != nil {
			ids, err := ScenarioIDs(m[3])
			if err != nil {
				return nil, ScenarioRecommendation{}, err
			}
			for _, id := range ids {
				n, _ := ScenarioNumber(id)
				if n > max {
					max = n
				}
			}
		}
		if strings.HasPrefix(sub, "снимок: ") {
			_, raw, ok := strings.Cut(strings.TrimPrefix(sub, "снимок: "), " ")
			if ok {
				var old ScenarioSnapshot
				if json.Unmarshal([]byte(raw), &old) == nil {
					for _, sc := range old.Scenarios {
						n, _ := ScenarioNumber(sc.ID)
						if n > max {
							max = n
						}
					}
				}
			}
		}
	}
	if f.NextID <= max {
		return nil, ScenarioRecommendation{}, fmt.Errorf("следующий ID СЦ-%d должен быть больше СЦ-%d", f.NextID, max)
	}
	ids, reason, ok := strings.Cut(f.Hint, ":")
	if !ok || strings.TrimSpace(reason) == "" {
		return nil, ScenarioRecommendation{}, fmt.Errorf("рекомендация сценариев: нужны ID через запятую и довод после двоеточия")
	}
	recommended, err := ScenarioIDs(ids)
	if err != nil {
		return nil, ScenarioRecommendation{}, err
	}
	for _, id := range recommended {
		if !seen[id] {
			return nil, ScenarioRecommendation{}, fmt.Errorf("рекомендованный %s отсутствует в списке", id)
		}
	}
	if f.Tie != "" || f.Who != ForkHuman {
		return nil, ScenarioRecommendation{}, fmt.Errorf("список сценариев требует решения человека и рекомендации набора")
	}
	if f.Decided() {
		current, err := ScenarioIDs(f.Answer)
		if err != nil {
			return nil, ScenarioRecommendation{}, err
		}
		for _, id := range current {
			if !seen[id] {
				return nil, ScenarioRecommendation{}, fmt.Errorf("выбранный %s отсутствует в списке", id)
			}
		}
	}
	return out, ScenarioRecommendation{IDs: recommended, Reason: strings.TrimSpace(reason)}, nil
}

func ScenarioCards(doc string) string {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	from := -1
	for i, line := range lines {
		if mask[i] {
			continue
		}
		if line == "## Пользовательские сценарии" {
			from = i + 1
			continue
		}
		if from >= 0 && strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(strings.Join(lines[from:i], "\n"))
		}
	}
	if from < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[from:], "\n"))
}

func NewScenarioSnapshot(record string, f Fork) (ScenarioSnapshot, error) {
	scenarios, rec, err := f.Scenarios()
	if err != nil {
		return ScenarioSnapshot{}, err
	}
	current := []string{}
	if f.Decided() {
		current, err = ScenarioIDs(f.Answer)
		if err != nil {
			return ScenarioSnapshot{}, err
		}
	}
	return ScenarioSnapshot{Version: 1, Record: record, Name: f.Name, Question: f.Question, Scenarios: scenarios, Recommendation: rec, Cards: f.Cards, Current: current, ChoiceEvent: f.ChoiceEvent}, nil
}

func (s ScenarioSnapshot) Encoded() (string, string) {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return "sc1-" + hex.EncodeToString(sum[:]), string(data)
}

func ReadScenarioSnapshot(f Fork, record, key string) (ScenarioSnapshot, error) {
	if key == "" {
		return ScenarioSnapshot{}, fmt.Errorf("ответ сценариев требует --snapshot ключа показанного блока")
	}
	for _, sub := range f.Sub {
		if !strings.HasPrefix(sub, "снимок: ") {
			continue
		}
		k, raw, ok := strings.Cut(strings.TrimPrefix(sub, "снимок: "), " ")
		if k != key {
			continue
		}
		sum := sha256.Sum256([]byte(raw))
		if !ok || key != "sc1-"+hex.EncodeToString(sum[:]) {
			return ScenarioSnapshot{}, fmt.Errorf("снимок %s повреждён: SHA-256 не совпадает", key)
		}
		var snapshot ScenarioSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return ScenarioSnapshot{}, fmt.Errorf("снимок %s: %w", key, err)
		}
		if snapshot.Version != 1 || snapshot.Record != record || snapshot.Name != f.Name {
			return ScenarioSnapshot{}, fmt.Errorf("снимок %s принадлежит другой записи или развилке", key)
		}
		return snapshot, nil
	}
	return ScenarioSnapshot{}, fmt.Errorf("снимок %s неизвестен для %s «%s»", key, record, f.Name)
}

func (f Fork) ScenarioReplies() ([]ScenarioReply, error) {
	var replies []ScenarioReply
	for _, sub := range f.Sub {
		if !strings.HasPrefix(sub, "ответ: ") {
			continue
		}
		var r ScenarioReply
		if err := json.Unmarshal([]byte(strings.TrimPrefix(sub, "ответ: ")), &r); err != nil {
			return nil, fmt.Errorf("след ответа «%s» повреждён: %w", f.Name, err)
		}
		if r.Event < 1 || r.Event > f.ChoiceEvent {
			return nil, fmt.Errorf("след ответа «%s»: неверное событие %d", f.Name, r.Event)
		}
		replies = append(replies, r)
	}
	return replies, nil
}

func ScenarioReplyLine(r ScenarioReply) string {
	data, _ := json.Marshal(r)
	return forkIndent + "ответ: " + string(data)
}

func SetForkField(doc, name, field, value string) (string, error) {
	f, ok := FindFork(doc, name)
	if !ok {
		return "", fmt.Errorf("нет развилки «%s»", name)
	}
	lines := strings.Split(doc, "\n")
	for i := f.Line; i < len(lines); i++ {
		sub := forkSubRe.FindStringSubmatch(lines[i])
		if sub == nil {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			break
		}
		if strings.HasPrefix(sub[1], field+":") {
			lines[i] = forkIndent + field + ": " + value
			return strings.Join(lines, "\n"), nil
		}
	}
	return AppendToFork(doc, name, forkIndent+field+": "+value)
}

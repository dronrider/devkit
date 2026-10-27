package taskform

import (
	"fmt"
	"strconv"
	"strings"
)

type ScenarioSelection struct {
	IDs      []string
	Added    []UserScenario
	Unparsed []string
	NextID   int
}

func scenarioPieces(text string) ([]string, error) {
	var parts []string
	start := 0
	quoted, escaped := false, false
	for i, r := range text {
		if escaped {
			escaped = false
			continue
		}
		if quoted && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
			continue
		}
		if !quoted && (r == ',' || r == ';') {
			parts = append(parts, strings.TrimSpace(text[start:i]))
			start = i + 1
		}
	}
	if quoted || escaped {
		return nil, fmt.Errorf("незакрытые двойные кавычки в ответе сценариев")
	}
	parts = append(parts, strings.TrimSpace(text[start:]))
	return parts, nil
}

func scenarioReference(text string, snapshot ScenarioSnapshot) (string, bool, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "СЦ-") {
		if _, err := ScenarioNumber(text); err != nil {
			return "", true, err
		}
		for _, s := range snapshot.Scenarios {
			if s.ID == text {
				return text, true, nil
			}
		}
		return "", true, fmt.Errorf("%s отсутствует в снимке сценариев", text)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(text, "№"))
	if err != nil {
		return "", false, nil
	}
	if n < 1 || n > len(snapshot.Scenarios) {
		return "", true, fmt.Errorf("позиция %d вне снимка сценариев (1..%d)", n, len(snapshot.Scenarios))
	}
	return snapshot.Scenarios[n-1].ID, true, nil
}

func ParseScenarioAnswer(f Fork, snapshot ScenarioSnapshot, text string) (ScenarioSelection, error) {
	result := ScenarioSelection{NextID: f.NextID}
	if strings.TrimSpace(text) == "" {
		return result, fmt.Errorf("ответ пуст: явный отказ от всех называется «ничего»")
	}
	parts, err := scenarioPieces(text)
	if err != nil {
		return result, err
	}
	explicit := false
	selection := map[string]bool{}
	type edit struct {
		add   bool
		id    string
		title string
	}
	var edits []edit
	listing := false
	operation := ""
	for _, piece := range parts {
		if piece == "" {
			continue
		}
		low := strings.ToLower(piece)
		if low == "ничего" {
			explicit = true
			operation = ""
			listing = false
			selection = map[string]bool{}
			continue
		}
		if low == "по рекомендации" {
			operation = ""
			explicit = true
			listing = false
			for _, id := range snapshot.Recommendation.IDs {
				selection[id] = true
			}
			continue
		}
		prefix := strings.TrimSpace(strings.TrimPrefix(piece, "«"))
		prefix = strings.Replace(prefix, f.Name+"»", f.Name, 1)
		if len(prefix) > len(f.Name) && strings.EqualFold(prefix[:len(f.Name)], f.Name) && prefix[len(f.Name)] == ' ' {
			piece = strings.TrimSpace(prefix[len(f.Name):])
			operation = ""
			explicit = true
			listing = true
		} else if strings.HasPrefix(low, "плюс ") || strings.HasPrefix(low, "минус ") {
			add := strings.HasPrefix(low, "плюс ")
			operation = "минус"
			if add {
				operation = "плюс"
			}
			tail := strings.TrimSpace(piece[len("плюс "):])
			if !add {
				tail = strings.TrimSpace(piece[len("минус "):])
			}
			listing = false
			if tail == "" {
				return result, fmt.Errorf("у правки сценариев нет пункта")
			}
			if strings.HasPrefix(tail, "\"") {
				title, err := strconv.Unquote(tail)
				if err != nil {
					return result, fmt.Errorf("текст сценария в кавычках: %w", err)
				}
				if !add || strings.TrimSpace(title) == "" {
					return result, fmt.Errorf("свой непустой сценарий добавляется словом «плюс»")
				}
				edits = append(edits, edit{add: true, title: title})
				continue
			}
			id, known, err := scenarioReference(tail, snapshot)
			if err != nil {
				return result, err
			}
			if known {
				edits = append(edits, edit{add: add, id: id})
				continue
			}
			if add {
				edits = append(edits, edit{add: true, title: tail})
				continue
			}
			result.Unparsed = append(result.Unparsed, piece)
			continue
		} else if operation != "" {
			id, known, err := scenarioReference(piece, snapshot)
			if err != nil {
				return result, err
			}
			if known {
				edits = append(edits, edit{add: operation == "плюс", id: id})
				continue
			}
		} else if !listing {
			// Перечень ID разрешён и для --by человек, где имя уже передано отдельно.
			if _, known, _ := scenarioReference(piece, snapshot); known {
				explicit = true
				listing = true
			}
		}
		if listing {
			id, known, err := scenarioReference(piece, snapshot)
			if err != nil {
				return result, err
			}
			if known {
				selection[id] = true
				continue
			}
		}
		result.Unparsed = append(result.Unparsed, piece)
	}
	if len(result.Unparsed) > 0 {
		return result, nil
	}
	if !explicit && len(edits) == 0 {
		return result, fmt.Errorf("ответ не содержит выбора сценариев")
	}
	if !explicit {
		if f.Reconsidered {
			return result, fmt.Errorf("после отказа от всех нужна основа: полный набор или «по рекомендации»")
		}
		base := snapshot.Current
		if snapshot.ChoiceEvent == 0 {
			base = snapshot.Recommendation.IDs
		}
		for _, id := range base {
			selection[id] = true
		}
	}
	for _, e := range edits {
		if strings.ContainsAny(e.title, "\r\n") {
			return result, fmt.Errorf("название сценария должно занимать одну строку")
		}
		id := e.id
		if e.title != "" {
			id = fmt.Sprintf("СЦ-%d", result.NextID)
			result.NextID++
			result.Added = append(result.Added, UserScenario{ID: id, Origin: "слово пользователя", Title: e.title})
		}
		if e.add {
			selection[id] = true
		} else {
			delete(selection, id)
		}
	}
	result.IDs = []string{}
	for id := range selection {
		result.IDs = append(result.IDs, id)
	}
	SortScenarioIDs(result.IDs)
	return result, nil
}

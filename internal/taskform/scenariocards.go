package taskform

import (
	"fmt"
	"regexp"
	"strings"
)

var scenarioCardHeadRe = regexp.MustCompile(`^### (СЦ-[1-9][0-9]*)\s*$`)
var scenarioCardBoundaryRe = regexp.MustCompile(`^#{1,6}\s+`)

var scenarioCardFields = []string{"Ситуация", "Действие", "Результат", "Польза", "Рекомендация", "Ограничение", "Источник"}

func ScenarioCardBodies(cards string) (map[string]string, error) {
	lines := strings.Split(cards, "\n")
	mask, _ := FenceMask(lines)
	bodies := map[string]string{}
	id := ""
	start := 0
	finish := func(end int) {
		if id != "" {
			bodies[id] = strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		}
	}
	for i, line := range lines {
		if mask[i] || !scenarioCardBoundaryRe.MatchString(line) {
			continue
		}
		finish(i)
		id = ""
		if head := scenarioCardHeadRe.FindStringSubmatch(line); head != nil {
			id = head[1]
			if _, exists := bodies[id]; exists {
				return nil, fmt.Errorf("карточка %s заведена второй раз", id)
			}
			start = i + 1
		}
	}
	finish(len(lines))
	return bodies, nil
}

func ValidateScenarioCards(cards string, scenarios []UserScenario) error {
	bodies, err := ScenarioCardBodies(cards)
	if err != nil {
		return err
	}
	for _, scenario := range scenarios {
		body, ok := bodies[scenario.ID]
		if !ok {
			return fmt.Errorf("нет готовой карточки ### %s в разделе «Пользовательские сценарии»", scenario.ID)
		}
		lines := strings.Split(body, "\n")
		mask, _ := FenceMask(lines)
		have := map[string]bool{}
		for i, line := range lines {
			if mask[i] {
				continue
			}
			label, value, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			for _, field := range scenarioCardFields {
				if strings.EqualFold(label, field) {
					have[field] = true
				}
			}
		}
		for _, field := range scenarioCardFields {
			if !have[field] {
				return fmt.Errorf("карточка %s: нет непустого поля «%s:» до следующего заголовка", scenario.ID, field)
			}
		}
	}
	return nil
}

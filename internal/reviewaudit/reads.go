package reviewaudit

import (
	"fmt"
	"strings"
)

type ReadScenario struct {
	Input     string   `json:"input"`
	InputAt   string   `json:"input_at"`
	CallAt    string   `json:"call_at"`
	Consumers []string `json:"consumers"`
	Observed  *int     `json:"observed"`
	Expected  *int     `json:"expected"`
	Failure   string   `json:"failure"`
	FailureAt string   `json:"failure_at"`
}

func checkReadScenario(entry Evidence, source *evidenceSource) error {
	if entry.Result == "pending" {
		return nil // The question is validated with the other facts.
	}
	scenario := entry.ReadScenario
	if scenario == nil || strings.TrimSpace(scenario.Input) == "" {
		return fmt.Errorf("read_scenario: укажите конкретный вход и место его проверки")
	}
	if scenario.CallAt != entry.Object {
		return fmt.Errorf("read_scenario.call_at должен совпадать с object, строкой вызова чтения")
	}
	for _, ref := range []string{scenario.InputAt, scenario.CallAt} {
		if _, err := source.line(ref); err != nil {
			return fmt.Errorf("read_scenario: %w", err)
		}
	}
	if entry.Result == "not_applicable" {
		if entry.Check != "without_consumer" || len(scenario.Consumers) == 0 {
			return fmt.Errorf("read_scenario: неприменимость требует ссылки на обязательного потребителя")
		}
		return checkReadConsumers(scenario, source)
	}
	if scenario.Observed == nil || scenario.Expected == nil || *scenario.Observed < 0 || *scenario.Expected < 0 {
		return fmt.Errorf("read_scenario: нужны числа вызовов observed и expected")
	}
	if entry.Check == "with_consumer" {
		if len(scenario.Consumers) == 0 {
			return fmt.Errorf("read_scenario: покажите потребителя результата чтения")
		}
		if err := checkReadConsumers(scenario, source); err != nil {
			return err
		}
	} else if len(scenario.Consumers) != 0 || *scenario.Expected != 0 {
		return fmt.Errorf("read_scenario: для входа без потребителя нужен пустой consumers и expected: 0")
	}
	if strings.TrimSpace(scenario.Failure) == "" {
		return fmt.Errorf("read_scenario: укажите исход операции при сбое чтения")
	}
	if _, err := source.line(scenario.FailureAt); err != nil {
		return fmt.Errorf("read_scenario.failure_at: %w", err)
	}
	if (*scenario.Observed == *scenario.Expected) != (entry.Result == "checked") {
		return fmt.Errorf("read_scenario: статус не соответствует observed и expected")
	}
	return nil
}

func checkReadConsumers(scenario *ReadScenario, source *evidenceSource) error {
	for _, ref := range scenario.Consumers {
		if ref == scenario.CallAt {
			return fmt.Errorf("read_scenario.consumers: укажите использование результата после чтения")
		}
		if _, err := source.line(ref); err != nil {
			return fmt.Errorf("read_scenario.consumers: %w", err)
		}
	}
	return nil
}

func checkReadInputs(rule Rule) error {
	if rule.ID != readRule {
		return nil
	}
	with := make(map[string]string)
	for _, entry := range rule.Evidence {
		if entry.Check == "with_consumer" && entry.ReadScenario != nil {
			with[entry.Object] = strings.TrimSpace(entry.ReadScenario.Input)
		}
	}
	for _, entry := range rule.Evidence {
		if entry.Check != "without_consumer" || entry.ReadScenario == nil || entry.Result == "not_applicable" || entry.Result == "pending" {
			continue
		}
		if input := with[entry.Object]; input != "" && input == strings.TrimSpace(entry.ReadScenario.Input) {
			return fmt.Errorf("%s / %s: with_consumer и without_consumer требуют разных входов", rule.ID, entry.Object)
		}
	}
	return nil
}

package draftproposal

import "fmt"

// Order returns selected proposals after their prerequisites without selecting
// anything on the caller's behalf. Existing board IDs are resolved by exists.
func Order(state State, selected []string, exists func(string) bool) ([]Proposal, error) {
	cards := make(map[string]Proposal, len(state.Proposals))
	for _, p := range state.Proposals {
		cards[p.ID] = p
	}
	chosen := map[string]bool{}
	for _, id := range selected {
		p, ok := cards[id]
		if !ok {
			return nil, fmt.Errorf("предложение %s не найдено", id)
		}
		if p.Status != "" {
			return nil, fmt.Errorf("%s: решение уже принято", id)
		}
		if chosen[id] {
			return nil, fmt.Errorf("%s: предложение выбрано дважды", id)
		}
		chosen[id] = true
	}
	visiting := map[string]bool{}
	done := map[string]bool{}
	var ordered []Proposal
	var visit func(string) error
	visit = func(id string) error {
		if done[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("цикл зависимостей у %s", id)
		}
		visiting[id] = true
		p := cards[id]
		for _, dep := range p.After {
			prev, ok := cards[dep]
			if !ok {
				if exists == nil || !exists(dep) {
					return fmt.Errorf("%s: зависимость %s не найдена", id, dep)
				}
				continue
			}
			if prev.Outcome != "add" {
				return fmt.Errorf("%s: предшественник %s не создаёт строку", id, dep)
			}
			if prev.Status == "accepted" {
				if prev.Result == nil || len(prev.Result.IDs) == 0 {
					return fmt.Errorf("%s: у %s нет ID результата", id, dep)
				}
				continue
			}
			if !chosen[dep] {
				return fmt.Errorf("%s: предшественник %s не принят и не выбран", id, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		done[id] = true
		ordered = append(ordered, p)
		return nil
	}
	for _, id := range selected {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/checkrun"
)

// cmdCheck называет ярус и модель того, кто прогонит сценарий проверки
// (DK-1116). Ярус берётся вердиктом роли ревью, а независимость несёт
// отдельная сессия прогона (DK-1360): совпадение с моделью разработки выбор
// не режет, и ступени по имени нет. Модель яруса, недоступную в текущей
// лестнице, заменяет другая допущенная автоматике подписка с того же яруса,
// а недопущенная включённая подписка в отбор не входит (DK-1453). Тем же
// кодом считает подъём после выката, и ручной спавн диспетчером теперь
// узнаёт модель до работы, а не ловит отказ taskctl close после неё.
func cmdCheck(root, id string) (string, error) {
	p, err := pickVerdict(root, id, roleReview, "")
	if err != nil {
		return "", err
	}
	dev, known := checkrun.DevExecutor(root, id)
	var ladders []checkrun.Ladder
	if p.HC.L == nil || p.HC.L.admitted(p.HC.Name) {
		ladders = append(ladders, checkerLadder(p.HC.Name, p.HC.Models))
	}
	if p.HC.L != nil {
		for _, name := range p.HC.L.Enabled {
			if name == p.HC.Name || !p.HC.L.admitted(name) {
				continue
			}
			s := p.HC.L.Setup[name]
			if s != nil {
				ladders = append(ladders, checkerLadder(name, tierModels{Map: s.Map, Active: name}))
			}
		}
	}
	if len(ladders) == 0 {
		return "", fmt.Errorf("%s: прогонять некем, в раскладке машины нет подписок, разрешённых"+
			" автоматическим назначениям (ключ auto в %s)", id, machineConfigPath())
	}
	ch := checkrun.ChooseAvailable(p.V.Tier, ladders)
	if ch.Refusal != "" {
		return "", fmt.Errorf("%s: прогонять некем, %s", id, ch.Refusal)
	}
	model := ch.Model
	var says []string
	via := ch.Harness
	if ch.Harness != p.HC.Name {
		says = append(says, "подписка "+ch.Harness+": модель яруса в текущей лестнице недоступна")
	}
	switch {
	case known:
		says = append(says, "разработку вёл "+dev)
	default:
		says = append(says, "исполнителя разработки записи не назвали, независимость сторожат ворота закрытия")
	}
	says = append(says, p.V.Reason)
	return fmt.Sprintf("model: %s\neffort: %s\ntier: %s\nharness: %s\nvia: %s\n%s: %s",
		model, p.V.Effort, ch.Tier, ch.Harness, via, id, strings.Join(says, "; ")), nil
}

// ladderOf это лестница подписки глазами отбора: ярусы активного контура снизу
// вверх, тем же порядком, каким их печатает `agentctl harness --json`. Ярус,
// который разворачивать нечем, в лестницу не идёт: ступенью с пустой моделью
// проверяющего не поднять.
func ladderOf(tm tierModels) []checkrun.Step {
	var out []checkrun.Step
	for _, tier := range tierNames {
		if a, ok := tm.assign(tier); ok {
			out = append(out, checkrun.Step{Tier: tier, Model: a.Model})
		}
	}
	return out
}

func checkerLadder(name string, tm tierModels) checkrun.Ladder {
	out := checkrun.Ladder{Name: name, Steps: ladderOf(tm), Via: map[string]string{}}
	for _, step := range out.Steps {
		out.Via[step.Tier] = tm.via(step.Tier)
	}
	return out
}

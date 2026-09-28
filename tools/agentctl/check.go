package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/checkrun"
)

// cmdCheck называет ярус и модель того, кто прогонит сценарий проверки
// (DK-1116). Ярус берётся вердиктом роли ревью, а независимость держит
// ступень: модель, совпавшую с моделью разработки, internal/checkrun.Choose
// меняет на первую ступень выше с другой моделью. Тем же кодом считает подъём
// после выката, и ручной спавн диспетчером теперь узнаёт модель до работы, а
// не ловит отказ taskctl close после неё.
func cmdCheck(root, id string) (string, error) {
	p, err := pickVerdict(root, id, roleReview, "")
	if err != nil {
		return "", err
	}
	dev, known := checkrun.DevExecutor(root, id)
	ch := checkrun.Choose(p.V.Tier, ladderOf(p.HC.Models), dev, known)
	if ch.Refusal != "" {
		return "", fmt.Errorf("%s: прогонять некем, %s", id, ch.Refusal)
	}
	model := ch.Model
	if model == "" {
		model = unmappedModel
	}
	var says []string
	if ch.Stepped {
		says = append(says, fmt.Sprintf("ярус %s поднят ступенью до %s: модель яруса %s вела разработку",
			ch.From, ch.Tier, ch.From))
	}
	switch {
	case known:
		says = append(says, "разработку вёл "+dev)
	default:
		says = append(says, "исполнителя разработки записи не назвали, независимость сторожат ворота закрытия")
	}
	says = append(says, p.V.Reason)
	return fmt.Sprintf("model: %s\neffort: %s\ntier: %s\nvia: %s\n%s: %s",
		model, p.V.Effort, ch.Tier, p.HC.Models.via(ch.Tier), id, strings.Join(says, "; ")), nil
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

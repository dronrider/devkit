package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/plans"
)

// Суффикс заголовка «[план: <имя>]» называет шаблон плана строки явно, минуя
// выбор по типу (LLD DK-972, решение 4): режим POC стоит суффиксом у строки
// любого типа. Ставит и снимает его команда plan, разбор живёт в dep.go рядом
// с остальными хвостами заголовка.

// PlanParams это аргументы команды plan.
type PlanParams struct {
	Name   string
	Off    bool
	Commit CommitOpts
}

// planSetLoad читает набор шаблонов обоих слоёв от корня доски.
func planSetLoad(root string) (*plans.Set, error) {
	home, _ := os.UserHomeDir()
	return plans.Load(plans.KitDir(root, home), plans.ProjectDir(root))
}

// planNameOf достаёт имя из разобранного хвоста « [план: имя]».
func planNameOf(planSuf string) string {
	m := planSufRe.FindStringSubmatch(planSuf)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// cmdPlan ставит и снимает суффикс шаблона плана. Имя сверяется с набором тут
// же: суффикс с чужим именем собрал бы агентctl отказ на каждом plan set, и
// находить это лучше в момент правки строки, а не в работе исполнителя.
func cmdPlan(root, id string, p PlanParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "plan"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	base, deps, debtSuf, armSuf, planSuf, dueSuf, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
	if p.Off {
		if planSuf == "" {
			return "", fmt.Errorf("%s и так без шаблона плана", id)
		}
		planSuf = ""
	} else {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return "", fmt.Errorf("имя шаблона обязательно: plan %s <имя> из agentctl plan templates либо --off", id)
		}
		if strings.ContainsAny(name, "[]") {
			return "", fmt.Errorf("имя шаблона %q не годится: скобок в нём быть не должно", name)
		}
		set, err := planSetLoad(root)
		if err != nil {
			return "", err
		}
		if _, ok := set.Get(name); !ok {
			return "", fmt.Errorf("шаблона %q в наборе нет, есть %s", name, strings.Join(set.Names, ", "))
		}
		if planNameOf(planSuf) == name {
			return fmt.Sprintf("%s: уже собирает план из шаблона %s", id, name), nil
		}
		planSuf = " [план: " + name + "]"
	}
	row.Title = joinTitle(base, deps, debtSuf, armSuf, planSuf, dueSuf, acceptSuf, failSuf, blockSuf)
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	if p.Off {
		return fmt.Sprintf("%s: шаблон плана снят, план собирается по типу строки%s", id, tail), nil
	}
	return fmt.Sprintf("%s: план собирается из шаблона %s%s", id, planNameOf(planSuf), tail), nil
}

// lintPlanNames ловит суффикс с именем, которого в наборе нет: шаблон могли
// переименовать или убрать, а строка продолжает называть его, и исполнитель
// узнаёт об этом отказом plan set вместо находки линта.
func lintPlanNames(root string, b *Board, bp string) []string {
	var withSuffix []*Row
	for _, r := range b.Rows {
		if _, _, _, _, planSuf, _, _, _, _ := splitTitle(r.Title); planSuf != "" {
			withSuffix = append(withSuffix, r)
		}
	}
	if len(withSuffix) == 0 {
		return nil
	}
	set, err := planSetLoad(root)
	if err != nil || len(set.Names) == 0 {
		// Набора не видно вовсе: сверять нечем, и шуметь на каждую строку
		// линт не будет.
		return nil
	}
	var finds []string
	for _, r := range withSuffix {
		_, _, _, _, planSuf, _, _, _, _ := splitTitle(r.Title)
		name := planNameOf(planSuf)
		if _, ok := set.Get(name); ok {
			continue
		}
		finds = append(finds, fmt.Sprintf("%s:%d: %s: суффикс [план: %s] называет шаблон, которого в наборе нет (есть %s): поправить taskctl plan %s <имя> либо снять --off",
			bp, r.LineIdx+1, r.ID, name, strings.Join(set.Names, ", "), r.ID))
	}
	return finds
}

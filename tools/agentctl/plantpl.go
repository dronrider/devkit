package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/plans"
)

// Набор шаблонов плана читается двумя слоями (LLD DK-972, решение 2): встроенный
// kit/plans едет с devkit, проектный .devkit/plans лежит в репозитории проекта и
// замещает одноимённый файл целиком. Команды тут только показывают набор, план
// из шаблона собирает DK-1143.

// planKitDirs считает оба каталога набора. Корень проекта необязателен: набор
// встроенного слоя находится и в сессии без доски.
func planKitDirs(start string) (string, string) {
	root, err := findRoot(start)
	if err != nil {
		root = ""
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		home = ""
	}
	return plans.KitDir(root, home), plans.ProjectDir(root)
}

// planLoadSet читает набор целиком.
func planLoadSet(start string) (*plans.Set, error) {
	kit, proj := planKitDirs(start)
	if kit == "" && proj == "" {
		return nil, fmt.Errorf("каталога шаблонов плана не видно ни во встроенном слое, ни в проекте; указать devkit: DEVKIT_HOME=<путь к devkit>")
	}
	return plans.Load(kit, proj)
}

// cmdPlanTemplates печатает набор списком либо отдаёт один файл как есть.
// Проектная копия заводится дампом встроенного файла, а правится она уже в
// репозитории проекта. Вместе со списком идут находки сверки слоёв: копия,
// отставшая от встроенного шаблона, видна тому, кто набор и смотрит, а не
// одному доктору.
func cmdPlanTemplates(start, dump string) (string, error) {
	set, err := planLoadSet(start)
	if err != nil {
		return "", err
	}
	kitDir, projDir := planKitDirs(start)
	if dump != "" {
		t, ok := set.Get(dump)
		if !ok {
			return "", fmt.Errorf("шаблона %q в наборе нет, есть %s", dump, strings.Join(set.Names, ", "))
		}
		data, err := os.ReadFile(t.File)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	if len(set.Names) == 0 {
		return "", fmt.Errorf("набор шаблонов пуст: каталог kit/plans не найден или в нём нет файлов")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "шаблонов плана %d", len(set.Names))
	for _, n := range set.Names {
		t := set.Templates[n]
		layer := "встроенный"
		if t.Project {
			layer = "проектный"
		}
		fmt.Fprintf(&b, "\n %-8s %s", t.Name, t.Title)
		fmt.Fprintf(&b, "\n %-8s слой %s, этапов %d", "", layer, len(t.Stages))
		if len(t.Types) > 0 {
			fmt.Fprintf(&b, ", типы %s", strings.Join(t.Types, ", "))
		}
		if len(t.Dropped) > 0 {
			fmt.Fprintf(&b, ", снято %d", len(t.Dropped))
		}
	}
	for _, w := range set.Warns {
		fmt.Fprintf(&b, "\nwarn: %s", w)
	}
	for _, f := range plans.Findings(kitDir, projDir) {
		fmt.Fprintf(&b, "\nнаходка: %s", f)
	}
	b.WriteString("\nвзять файл целиком: agentctl plan templates --dump <имя> > .devkit/plans/<имя>.toml")
	return b.String(), nil
}

// planTemplateShow печатает этапы шаблона по порядку: кто ведёт, какой след
// остаётся, какие ворота его спрашивают и при каком условии этап нужен.
func planTemplateShow(start, name string) (string, error) {
	set, err := planLoadSet(start)
	if err != nil {
		return "", err
	}
	t, ok := set.Get(name)
	if !ok {
		return "", fmt.Errorf("шаблона %q в наборе нет, есть %s", name, strings.Join(set.Names, ", "))
	}
	var b strings.Builder
	where := t.File
	if rel, err := filepath.Abs(t.File); err == nil {
		where = rel
	}
	fmt.Fprintf(&b, "шаблон %s: %s", t.Name, t.Title)
	fmt.Fprintf(&b, "\nфайл %s, этапов %d", where, len(t.Stages))
	if len(t.Types) > 0 {
		fmt.Fprintf(&b, ", типы %s", strings.Join(t.Types, ", "))
	}
	for i, s := range t.Stages {
		fmt.Fprintf(&b, "\n %2d %s: %s", i+1, s.Title, planStageTail(s))
	}
	for _, d := range t.Dropped {
		fmt.Fprintf(&b, "\nснят этап %s: %s", d.Name, d.Why)
	}
	for _, w := range t.Warns {
		fmt.Fprintf(&b, "\nwarn: %s", w)
	}
	return b.String(), nil
}

// planStageTail собирает хвост строки этапа. Этап без следа помечен «на слове
// агента»: слово это не изъятие, а честная надпись о том, что проверить его
// нечем.
func planStageTail(s plans.Stage) string {
	parts := []string{"ведёт " + s.By}
	if s.Agent != "" {
		parts[0] += " " + s.Agent
	}
	if s.Trace == "слово" {
		who := "агента"
		if s.By == "человек" {
			who = "человека"
		}
		parts = append(parts, "на слове "+who)
	} else {
		parts = append(parts, "след "+s.Trace)
	}
	if len(s.Gate) > 0 {
		parts = append(parts, "ворота "+strings.Join(s.Gate, ", "))
	}
	if s.Skill != "" {
		parts = append(parts, "скилл "+s.Skill)
	}
	if s.Slot {
		parts = append(parts, "слот своих пунктов")
	}
	if len(s.Types) > 0 {
		parts = append(parts, "типы "+strings.Join(s.Types, ", "))
	}
	if len(s.Paths) > 0 {
		parts = append(parts, "пути "+strings.Join(s.Paths, ", "))
	}
	if len(s.Accept) > 0 {
		parts = append(parts, "приёмка "+strings.Join(s.Accept, ", "))
	}
	if s.Scenario != "" {
		parts = append(parts, "сценарий стенда "+s.Scenario)
	}
	return strings.Join(parts, ", ")
}

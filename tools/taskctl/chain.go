package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Команда chain строит цепочку уровнями (LLD DK-933, решение 4): просьба
// «после DK-A сделай DK-B и DK-C, потом DK-D» вместо четырёх ребра и трёх
// взводов поштучно ставится одним вызовом. Проверки те же, что у dep add
// (reachable против цикла) и arm (armReadiness): второй копии этих правил
// файл не заводит, только собирает уровни в порядок вызовов.

// ChainParams это разбор аргументов chain: уровни позиционными, --after даёт
// рёбра первому уровню, --dry-run печатает план и доску не трогает.
type ChainParams struct {
	After  []string
	Levels [][]string
	DryRun bool
	Commit CommitOpts
}

// splitChainIDs разбирает список ID уровня или --after: через запятую или
// пробел, пустые куски отбрасываются.
func splitChainIDs(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// cmdChain кладёт на доску рёбра «после» и взвод по уровням. Каждая строка
// уровня N получает ребро на все строки уровня N-1, первый уровень получает
// рёбра на --after, если он назван. Ошибка на любом шаге (цикл, отказ
// armReadiness, отсутствующая строка) возвращает отказ раньше первой записи
// на диск: LoadBoard читает файл, а b.Save() зовётся только после того, как
// все уровни разобраны без ошибки.
func cmdChain(root string, p ChainParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if len(p.Levels) == 0 {
		return "", fmt.Errorf("нужен хотя бы один уровень: chain [--after ID,...] \"уровень 1\" [\"уровень 2\" ...]")
	}
	for i, level := range p.Levels {
		if len(level) == 0 {
			return "", fmt.Errorf("уровень %d пуст", i+1)
		}
	}
	if !p.DryRun {
		if err := boardGuard(root, "chain"); err != nil {
			return "", err
		}
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	for _, id := range p.After {
		if b.find(id) == nil && !arch.has(id) {
			return "", fmt.Errorf("%s нет ни на доске, ни в архиве", id)
		}
	}

	var plan []string
	prev := p.After
	for i, level := range p.Levels {
		levelNo := i + 1
		for _, id := range level {
			row := b.find(id)
			if row == nil {
				return "", fmt.Errorf("%s нет на доске", id)
			}
			if err := needTaskFile(root, id); err != nil {
				return "", err
			}
			base, deps, armSuf, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
			for _, dep := range prev {
				if dep == id {
					return "", fmt.Errorf("%s не может зависеть сам от себя", id)
				}
				if containsID(deps, dep) {
					continue
				}
				if reachable(b.Rows, dep, id) {
					return "", fmt.Errorf("%s после %s замкнёт цикл зависимостей", id, dep)
				}
				deps = append(deps, dep)
			}
			if armSuf == "" {
				if err := armReadiness(root, b, row); err != nil {
					return "", err
				}
				armSuf = armSuffix
			}
			row.Title = joinTitle(base, deps, armSuf, acceptSuf, failSuf, blockSuf)
			b.updateLine(row.LineIdx, formatRow(row))
			plan = append(plan, fmt.Sprintf("уровень %d: %s%s", levelNo, id, joinTitle("", deps, armSuf, "", "", "")))
		}
		prev = level
	}
	out := strings.Join(plan, "\n")
	if p.DryRun {
		return out, nil
	}

	moves, b, err := rehydrate(b)
	if err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return out + movesTail(moves) + tail, nil
}

// containsID это проверка ID в списке зависимостей: chain не дублирует ребро,
// если оно уже стоит с прошлого вызова.
func containsID(xs []string, id string) bool {
	for _, x := range xs {
		if x == id {
			return true
		}
	}
	return false
}

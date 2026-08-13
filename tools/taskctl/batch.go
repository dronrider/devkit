package main

import (
	"fmt"
	"strings"
)

// Верхняя граница правила поезда (RULES.board.md, «Ветки, ревью и деплой»
// п. 9). Меньшее число приходит флагом из agentctl budget.
const batchDefaultLimit = 5

const batchTail = "одно приложение и пересечение по файлам доска не видит: " +
	"сверить кандидатов по файлам задач, вторая страховка это предупреждение shipctl merge --train"

// batchGroups держит отказы по причинам в порядке, в котором причина впервые
// встретилась при обходе бэклога сверху вниз.
type batchGroups struct {
	order []string
	items map[string][]string
}

func (g *batchGroups) add(label, item string) {
	if g.items == nil {
		g.items = map[string][]string{}
	}
	if _, ok := g.items[label]; !ok {
		g.order = append(g.order, label)
	}
	g.items[label] = append(g.items[label], item)
}

func isLLD(t string) bool {
	for _, p := range strings.Split(t, "/") {
		if p == "LLD" {
			return true
		}
	}
	return false
}

// openDeps возвращает ID из «[после ...]», которых нет в архиве, то есть
// незакрытые зависимости задачи.
func openDeps(r *Row, arch *Archive) []string {
	_, deps, _, _, _ := splitTitle(r.Title)
	var open []string
	for _, d := range deps {
		if !arch.has(d) {
			open = append(open, d)
		}
	}
	return open
}

// cmdBatch отбирает кандидатов в поезд выката по тем критериям правила, что
// читаются с доски. Проверки идут фиксированным порядком, и задача попадает в
// отказ по первой непрошедшей, то есть ровно в одну группу.
func cmdBatch(root string, limit int) (string, error) {
	if limit < 1 {
		return "", fmt.Errorf("лимит пачки %d, жду положительное число", limit)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	var taken, takenIDs []string
	byLink := map[string]string{}
	g := &batchGroups{}
	for _, r := range b.Sects[SectBacklog].Rows {
		unc := r.RParts[2]
		switch {
		case isLLD(r.Type):
			g.add("тип LLD", r.ID)
		case r.Cost != "S" && r.Cost != "M":
			g.add("цена не S и не M", fmt.Sprintf("%s (%s)", r.ID, r.Cost))
		case unc > 1:
			g.add("неопределённость выше 1", fmt.Sprintf("%s (%d)", r.ID, unc))
		case len(openDeps(r, arch)) > 0:
			g.add("ждут незакрытых зависимостей", r.ID)
		case byLink[r.Link] != "":
			g.add("ссылка та же, что у отобранной", fmt.Sprintf("%s (как у %s)", r.ID, byLink[r.Link]))
		case len(takenIDs) == limit:
			g.add(fmt.Sprintf("лимит пачки %d исчерпан", limit), r.ID)
		default:
			takenIDs = append(takenIDs, r.ID)
			taken = append(taken, fmt.Sprintf("%s (%s, %s, неопр. %d)", r.ID, r.Type, r.Cost, unc))
			if r.Link != "-" {
				byLink[r.Link] = r.ID
			}
		}
	}
	out := []string{"batch: " + joinOrDash(takenIDs)}
	if len(taken) > 0 {
		out = append(out, "взято: "+strings.Join(taken, ", "))
	}
	for _, label := range g.order {
		out = append(out, label+": "+strings.Join(g.items[label], ", "))
	}
	if len(taken) > 0 {
		out = append(out, batchTail)
	}
	return strings.Join(out, "\n"), nil
}

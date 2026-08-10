package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Машинный вывод для дашборда (LLD DK-112): у list, show и dep list есть флаг
// --json, печатный вывод этих команд не меняется. Разбирать человеческий
// вывод хрупко, он для людей и правится свободно, поэтому дашборд получает
// строки доски отсюда, а не грепом по печати.

// jsonRow это строка доски в машинном виде: заголовок разобран на основу и
// суффиксы, ранг на сумму и слагаемые, пометки list («код слит», возраст)
// лежат отдельным списком.
type jsonRow struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	After  []string `json:"after,omitempty"`
	Fail   string   `json:"fail,omitempty"`
	Block  string   `json:"block,omitempty"`
	Type   string   `json:"type"`
	P      string   `json:"p"`
	R      int      `json:"r"`
	RParts [5]int   `json:"r_parts"`
	Cost   string   `json:"cost"`
	Link   string   `json:"link"`
	Notes  []string `json:"notes,omitempty"`
}

type jsonSection struct {
	Key   string    `json:"key"`
	Title string    `json:"title"`
	Rows  []jsonRow `json:"rows"`
}

type jsonBoard struct {
	Prefix   string        `json:"prefix,omitempty"`
	Sections []jsonSection `json:"sections"`
}

// sufText вынимает текст из хвоста «[метка: текст]», каким его отдал
// splitTitle; пустой хвост остаётся пустой строкой.
func sufText(suf, label string) string {
	s := strings.TrimSpace(suf)
	s = strings.TrimPrefix(s, "["+label+":")
	return strings.TrimSpace(strings.TrimSuffix(s, "]"))
}

func makeJSONRow(root string, r *Row, times map[int]int64, clean bool) jsonRow {
	base, deps, failSuf, blockSuf := splitTitle(r.Title)
	return jsonRow{
		ID:    r.ID,
		Title: strings.TrimSpace(base),
		After: deps,
		Fail:  sufText(failSuf, "провал"),
		Block: sufText(blockSuf, "блок"),
		Type:  r.Type, P: r.P, R: r.RTotal, RParts: r.RParts,
		Cost: r.Cost, Link: r.Link,
		Notes: rowNoteParts(root, r.Sect, r, times, clean),
	}
}

func marshal(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// cmdListJSON печатает доску одним объектом JSON. Backlog идёт целиком, без
// обрезки печатного list: та экономит контекст агента, а машинному читателю
// нужна вся доска.
func cmdListJSON(root, sect string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	clean := boardClean(root)
	var times map[int]int64
	if clean {
		times = boardTimes(root)
	}
	keys := []string{SectInProgress, SectCheck, SectBacklog, SectBlocked}
	if sect != "" {
		key := normalizeStatus(sect)
		if _, ok := b.Sects[key]; !ok {
			return "", fmt.Errorf("неизвестная секция %q, жду backlog / in-progress / check / blocked", sect)
		}
		keys = []string{key}
	}
	out := jsonBoard{Prefix: b.Prefix}
	for _, key := range keys {
		sec := jsonSection{Key: key, Title: sectTitles[key], Rows: []jsonRow{}}
		for _, r := range b.Sects[key].Rows {
			sec.Rows = append(sec.Rows, makeJSONRow(root, r, times, clean))
		}
		out.Sections = append(out.Sections, sec)
	}
	return marshal(out)
}

// jsonShow это ответ show --json: строка доски с секцией и обратными
// зависимостями; у закрытой задачи секция «archive» и дата закрытия, у
// черновика «draft» и путь файла.
type jsonShow struct {
	jsonRow
	Sect   string   `json:"sect"`
	Blocks []string `json:"blocks,omitempty"`
	File   string   `json:"file,omitempty"`
	Closed string   `json:"closed,omitempty"`
}

func cmdShowJSON(root, id string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	if row := b.find(id); row != nil {
		out := jsonShow{
			jsonRow: makeJSONRow(root, row, showTimes(root), true),
			Sect:    row.Sect,
		}
		if s := depSides(b)[id]; s != nil {
			out.Blocks = s.blocks
		}
		rel := filepath.Join("docs", "tasks", id+".md")
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			out.File = rel
		}
		return marshal(out)
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	for _, r := range arch.Rows {
		if r.ID == id {
			return marshal(jsonShow{
				jsonRow: jsonRow{ID: id, Title: r.Cells[1], Type: r.Cells[2], P: r.Cells[3], Link: r.Cells[5]},
				Sect:    "archive",
				Closed:  r.Cells[4],
			})
		}
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	if d := findDraft(drafts, id); d != nil {
		return marshal(jsonShow{
			jsonRow: jsonRow{ID: id, Title: d.Title},
			Sect:    "draft",
			File:    filepath.Join("docs", "tasks", "drafts", id+".md"),
		})
	}
	return "", fmt.Errorf("%s нет ни на доске, ни в архиве, ни в черновиках", id)
}

// jsonDep это зависимости одной задачи в обе стороны, как их печатает
// dep list: после кого она делается и кого держит.
type jsonDep struct {
	ID     string   `json:"id"`
	After  []string `json:"after,omitempty"`
	Blocks []string `json:"blocks,omitempty"`
}

func cmdDepListJSON(root, id string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	sides := depSides(b)
	if id != "" {
		if b.find(id) == nil {
			return "", fmt.Errorf("%s нет на доске", id)
		}
		d := jsonDep{ID: id}
		if s := sides[id]; s != nil {
			d.After, d.Blocks = s.after, s.blocks
		}
		return marshal(d)
	}
	deps := []jsonDep{}
	for _, r := range b.Rows {
		s := sides[r.ID]
		if s == nil || (len(s.after) == 0 && len(s.blocks) == 0) {
			continue
		}
		deps = append(deps, jsonDep{ID: r.ID, After: s.after, Blocks: s.blocks})
	}
	return marshal(struct {
		Deps []jsonDep `json:"deps"`
	}{deps})
}

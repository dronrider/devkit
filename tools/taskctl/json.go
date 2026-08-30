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
// суффиксы, ранг на сумму и слагаемые, дата последней правки строки лежит
// отдельным полем, пометки list («код слит», возраст) отдельным списком.
type jsonRow struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	After  []string `json:"after,omitempty"`
	Accept string   `json:"accept,omitempty"`
	Fail   string   `json:"fail,omitempty"`
	Block  string   `json:"block,omitempty"`
	Type   string   `json:"type"`
	P      string   `json:"p"`
	R      int      `json:"r"`
	ROwn   int      `json:"r_own"`
	RParts [5]int   `json:"r_parts"`
	// Поправки к рангу списком: у аддитивной имя и дельта, у подтягивающей
	// задача, от которой подтянут итог (DK-428). Дашборд хвост не разбирает,
	// правит ручкой пять слагаемых, а читает готовые r и r_own.
	Adjustments []jsonAdj `json:"adjustments,omitempty"`
	Cost        string    `json:"cost"`
	Link        string    `json:"link"`
	Moved       string    `json:"moved,omitempty"`
	Notes       []string  `json:"notes,omitempty"`
}

// jsonAdj это одна поправка машинным видом: имя с дельтой либо ссылка «от».
type jsonAdj struct {
	Name  string `json:"name,omitempty"`
	Delta int    `json:"delta,omitempty"`
	From  string `json:"from,omitempty"`
}

func jsonAdjs(adjs []Adjustment) []jsonAdj {
	var out []jsonAdj
	for _, a := range adjs {
		out = append(out, jsonAdj{Name: a.Name, Delta: a.Delta, From: a.From})
	}
	return out
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
	base, deps, acceptSuf, failSuf, blockSuf := splitTitle(r.Title)
	return jsonRow{
		ID:     r.ID,
		Title:  strings.TrimSpace(base),
		After:  deps,
		Accept: sufText(acceptSuf, "приёмка"),
		Fail:   sufText(failSuf, "провал"),
		Block:  sufText(blockSuf, "блок"),
		Type:   r.Type, P: r.P, R: r.RTotal, ROwn: r.ROwn, RParts: r.RParts,
		Adjustments: jsonAdjs(r.RAdj),
		Cost:        r.Cost, Link: r.Link,
		// Дата последней правки строки: перевод в статус двигает строку между
		// секциями доски, то есть правит её, и отдельной даты перевода в доске
		// нет. Возраст днями остаётся в notes, дашборд показывает дату.
		Moved: lineDate(times, r.LineIdx, clean),
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

// jsonDraft это черновик накопителя машинным видом. Возраст едет и днями, и
// теми же словами, что печатает draft list: читателю-человеку (дашборд рисует
// накопитель списком) слова нужны те же, а считать их второй раз по своей
// шкале значит разойтись с утилитой на первом же «вчера».
type jsonDraft struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	File     string `json:"file"`
	Written  string `json:"written,omitempty"`
	AgeDays  int    `json:"age_days"`
	AgeWords string `json:"age_words"`
	Deferred string `json:"deferred,omitempty"`
	Prio     string `json:"prio,omitempty"`
}

// cmdDraftListJSON печатает накопитель одним объектом JSON. Пустой накопитель
// это пустой список, а не отсутствие поля: «черновиков нет» читатель обязан
// отличать от «список не приехал».
func cmdDraftListJSON(root string) (string, error) {
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	out := struct {
		Drafts []jsonDraft `json:"drafts"`
	}{Drafts: []jsonDraft{}}
	for _, d := range drafts {
		item := jsonDraft{
			ID: d.ID, Title: d.Title, File: filepath.ToSlash(draftRel(d.ID)),
			AgeDays: int(d.Age.Hours() / 24), AgeWords: ageWords(d.Age), Deferred: d.Deferred,
			Prio: d.Prio,
		}
		if !d.Written.IsZero() {
			item.Written = d.Written.Format(draftDateLayout)
		}
		out.Drafts = append(out.Drafts, item)
	}
	return marshal(out)
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

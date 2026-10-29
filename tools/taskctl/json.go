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
	ID    string   `json:"id"`
	Title string   `json:"title"`
	After []string `json:"after,omitempty"`
	// HeldBy это неснятые рёбра строки (решение 2 LLD DK-933). Маркер «после»
	// снимает только close, а ребро снимается уже слиянием предпосылки, и
	// держит строку на экране это поле, а не after.
	HeldBy []string `json:"held_by,omitempty"`
	// Debt это метка технического долга (DK-624): строку берёт не очередь, а
	// скилл board-debt при сдаче соседней задачи с тем же диффом.
	Debt bool `json:"debt,omitempty"`
	// Armed это взвод строки (решение 1 LLD DK-933): человек разрешил ей
	// стартовать самой, как только рёбра сняты.
	Armed bool `json:"armed,omitempty"`
	// Arm это состояние взвода: ждёт рёбер, готова стартовать, получила отказ
	// ворот ёмкости либо поднимается рукой. Пусто там, где о взводе сказать
	// нечего: строка вне Backlog и строка без взвода с неснятым ребром.
	Arm    *jsonArm `json:"arm,omitempty"`
	Accept string   `json:"accept,omitempty"`
	// Due это срок строки доски датой 2006-01-02 из суффикса заголовка
	// «[срок: ...]» (DK-1464). Пусто у строки без срока.
	Due string `json:"due,omitempty"`
	// Control это контроль задачи из суффикса «[контроль: ...]» (DK-1470):
	// за сколько дней до срока контроль поднимает проверку работ. Nil у строки
	// без контроля, ноль значит «в сам день срока» и обязан выживать в json,
	// поэтому поле указателем.
	Control *int `json:"control,omitempty"`
	// Assign это назначенный участник команды из суффикса «[исп: ...]»
	// (DK-1468): handle из ~/.devkit/team.toml. Пусто у строки без
	// назначения.
	Assign string `json:"assign,omitempty"`
	Fail   string `json:"fail,omitempty"`
	Block  string `json:"block,omitempty"`
	Type   string `json:"type"`
	P      string `json:"p"`
	R      int    `json:"r"`
	ROwn   int    `json:"r_own"`
	RParts [5]int `json:"r_parts"`
	// Поправки к рангу списком: у аддитивной имя и дельта, у подтягивающей
	// задача, от которой подтянут итог (DK-428). Своего счёта поправок у
	// читателя нет: дашборд правит ручкой пять слагаемых, а итог, свою сумму и
	// хвост берёт отсюда готовыми (DK-650).
	Adjustments []jsonAdj `json:"adjustments,omitempty"`
	Cost        string    `json:"cost"`
	Link        string    `json:"link"`
	Moved       string    `json:"moved,omitempty"`
	Notes       []string  `json:"notes,omitempty"`
	// Строка этапа полями (DK-910): вид, начало, круг, возраст словами и
	// живость сессии словами. Пусто у строки без открытого этапа.
	*jsonStage
}

// jsonArm это состояние взвода строки для машинного читателя: разряд словом и
// та же пометка, какую печатают `list` и `show`. Своей копии правила у
// читателя нет, счёт остаётся за утилитой.
type jsonArm struct {
	// State это разряд: «ждёт», «готова», «отказ», «рукой».
	State string `json:"state"`
	Note  string `json:"note"`
	// At это назначенный час взвода форматом заголовка (решение 6 LLD
	// DK-933): пусто у взвода без часового условия.
	At string `json:"at,omitempty"`
}

func jsonArmOf(root string, ed *edges, r *Row) *jsonArm {
	v := armState(root, ed, r)
	if v == nil {
		return nil
	}
	return &jsonArm{State: v.State, Note: v.Note, At: v.At}
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

// jsonControlOf переводит хвост контроля в поле json: пустой хвост это nil,
// и ноль дней остаётся нулём, а не исчезает.
func jsonControlOf(suf string) *int {
	if days, ok := controlDays(suf); ok {
		return &days
	}
	return nil
}

func makeJSONRow(root string, r *Row, ed *edges, times map[int]int64, sv *stageView) jsonRow {
	base, deps, debtSuf, armSuf, _, assignSuf, dueSuf, controlSuf, acceptSuf, failSuf, blockSuf := splitTitle(r.Title)
	control := jsonControlOf(controlSuf)
	return jsonRow{
		ID:      r.ID,
		Title:   strings.TrimSpace(base),
		After:   deps,
		HeldBy:  ed.heldIDs(r),
		Debt:    debtSuf != "",
		Armed:   armSuf != "",
		Arm:     jsonArmOf(root, ed, r),
		Accept:  sufText(acceptSuf, "приёмка"),
		Due:     sufText(dueSuf, "срок"),
		Control: control,
		Assign:  sufText(assignSuf, "исп"),
		Fail:    sufText(failSuf, "провал"),
		Block:   sufText(blockSuf, "блок"),
		Type:    r.Type, P: r.P, R: r.RTotal, ROwn: r.ROwn, RParts: r.RParts,
		Adjustments: jsonAdjs(r.RAdj),
		Cost:        r.Cost, Link: r.Link,
		// Дата последней правки строки: перевод в статус двигает строку между
		// секциями доски, то есть правит её, и отдельной даты перевода в доске
		// нет. Возраст днями остаётся в notes, дашборд показывает дату.
		Moved:     lineDate(times, r.LineIdx),
		Notes:     rowNoteParts(root, ed, r.Sect, r, times),
		jsonStage: sv.mark(r.ID),
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
// debtOnly оставляет в выводе только строки с меткой технического долга: так
// скилл board-debt читает запас, не разбирая заголовки глазами.
func cmdListJSON(root, sect string, debtOnly bool) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	ed := newEdges(root, b, arch)
	times := boardTimes(root)
	sv := loadStageView(root)
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
			if debtOnly && !isDebt(r.Title) {
				continue
			}
			sec.Rows = append(sec.Rows, makeJSONRow(root, r, ed, times, sv))
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
	sides := depSides(b)
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	if row := b.find(id); row != nil {
		out := jsonShow{
			jsonRow: makeJSONRow(root, row, newEdges(root, b, arch), boardTimes(root), loadStageView(root)),
			Sect:    row.Sect,
		}
		if s := sides[id]; s != nil {
			out.Blocks = s.blocks
		}
		rel := filepath.Join("docs", "tasks", id+".md")
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			out.File = rel
		}
		return marshal(out)
	}
	if r := arch.find(id); r != nil {
		out := jsonShow{
			jsonRow: jsonRow{ID: id, Title: r.Cells[1], Type: r.Cells[2], P: r.Cells[3], Link: r.Cells[5]},
			Sect:    "archive",
			Closed:  r.Cells[4],
		}
		// «Держит» у архивной строки считается по доске, как у живой: с её
		// экрана берут в работу тех, кого она держала.
		if s := sides[id]; s != nil {
			out.Blocks = s.blocks
		}
		if rel, ok := archiveTaskFile(root, id, r.Cells[4]); ok {
			out.File = rel
		}
		return marshal(out)
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
	// DeferredUntil это дата возврата отложенного черновика (DK-1466): пусто
	// у неотложенного и у отложенного без срока.
	DeferredUntil string `json:"deferred_until,omitempty"`
	Prio          string `json:"prio,omitempty"`
	Type          string `json:"type"`
	Source        string `json:"source"`
	Lock          string `json:"lock"`
}

// cmdDraftListJSON печатает накопитель одним объектом JSON. Пустой накопитель
// это пустой список, а не отсутствие поля: «черновиков нет» читатель обязан
// отличать от «список не приехал».
func cmdDraftListJSON(root string) (string, error) { return cmdDraftListJSONHistory(root, false) }

func cmdDraftListJSONHistory(root string, history bool) (string, error) {
	return cmdDraftListJSONSafe(root, history, false)
}

func cmdDraftListJSONSafe(root string, history, safe bool) (string, error) {
	drafts, err := loadDraftsWithHistory(root, history)
	if err != nil {
		if safe {
			return "", fmt.Errorf("не удалось безопасно прочитать накопитель; проверьте метаданные локально")
		}
		return "", err
	}
	out := struct {
		Drafts []jsonDraft `json:"drafts"`
	}{Drafts: []jsonDraft{}}
	for _, d := range drafts {
		if history && !d.History {
			continue
		}
		item := jsonDraft{
			ID: d.ID, Title: d.Title, File: filepath.ToSlash(draftRel(d.ID)),
			AgeDays: int(d.Age.Hours() / 24), AgeWords: ageWords(d.Age), Deferred: d.Deferred,
			DeferredUntil: d.Until, Prio: d.Prio, Type: d.Type, Source: d.Source, Lock: d.Lock,
		}
		if safe && d.Lock == "locked" {
			item.Title = "ждёт локальную модель"
		}
		if !d.Written.IsZero() {
			item.Written = d.Written.Format(draftDateLayout)
		}
		out.Drafts = append(out.Drafts, item)
	}
	return marshal(out)
}

// jsonDep это зависимости одной задачи в обе стороны, как их печатает
// dep list: после кого она делается и кого держит. У закрытой задачи стоит
// признак archived, а вместо списка «после» пояснение after_note: направление
// в архиве не хранится, и пустой список читался бы как «ни после кого».
type jsonDep struct {
	ID        string   `json:"id"`
	After     []string `json:"after,omitempty"`
	Blocks    []string `json:"blocks,omitempty"`
	Archived  bool     `json:"archived,omitempty"`
	AfterNote string   `json:"after_note,omitempty"`
	// Edges это состояние каждого ребра из after словами «слита», «закрыта»
	// и «ждёт» с причиной (решение 2 LLD DK-933).
	Edges []jsonEdge `json:"edges,omitempty"`
	// Armed это взвод строки: со снятыми рёбрами она стартует сама, и экрану
	// зависимостей это говорит, кого ждать своим ходом, а кого рукой.
	Armed bool `json:"armed,omitempty"`
	// Arm это состояние взвода тем же составом, что у строки списка: карточка
	// зависимостей показывает его рядом с переключателем автозапуска.
	Arm *jsonArm `json:"arm,omitempty"`
}

type jsonEdge struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Why   string `json:"why"`
}

func jsonEdges(ed *edges, deps []string) []jsonEdge {
	var out []jsonEdge
	for _, d := range deps {
		e := ed.of(d)
		out = append(out, jsonEdge{ID: d, State: e.State, Why: e.Why})
	}
	return out
}

func cmdDepListJSON(root, id string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	ed := newEdges(root, b, arch)
	sides := depSides(b)
	if id != "" {
		d := jsonDep{ID: id}
		if row := b.find(id); row != nil {
			d.Armed = armed(row.Title)
			d.Arm = jsonArmOf(root, ed, row)
		}
		if s := sides[id]; s != nil {
			d.After, d.Blocks = s.after, s.blocks
			d.Edges = jsonEdges(ed, s.after)
		}
		if b.find(id) == nil {
			archived, err := inArchive(root, id)
			if err != nil {
				return "", err
			}
			if !archived {
				return "", fmt.Errorf("%s нет ни на доске, ни в архиве", id)
			}
			d.After, d.Archived, d.AfterNote, d.Edges = nil, true, archiveAfterNote, nil
		}
		return marshal(d)
	}
	deps := []jsonDep{}
	for _, r := range b.Rows {
		s := sides[r.ID]
		if s == nil || (len(s.after) == 0 && len(s.blocks) == 0) {
			continue
		}
		deps = append(deps, jsonDep{ID: r.ID, After: s.after, Blocks: s.blocks,
			Edges: jsonEdges(ed, s.after), Armed: armed(r.Title), Arm: jsonArmOf(root, ed, r)})
	}
	return marshal(struct {
		Deps []jsonDep `json:"deps"`
	}{deps})
}

package draftproposal

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/subtoml"
)

// Prepared-файл это носитель подготовленных задач записи (DK-1469, решение 1):
// docs/tasks/prepared/<ID черновика>.md. Файл открывается машинным блоком TOML,
// ниже лежит раздел на каждую задачу. Запись черновика машинных блоков разбора
// не содержит вовсе, старый блок «Предложения разбора» читается ридером
// совместимости из proposal.go, пока миграция не перевела запись.

const (
	preparedFence = "```toml"
	cardFields    = "revision outcome reason rationale board_type rank size accept barrier control assignee due target until after status"
	receiptFields = "source key proposal revision outcome ids result reason"
)

func PreparedPath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", "prepared", id+".md")
}

func PreparedRel(id string) string {
	return filepath.Join("docs", "tasks", "prepared", id+".md")
}

// Load читает состояние разбора записи: сначала prepared-файл, при его
// отсутствии старый блок самой записи. Запись читается вызывателем отдельно:
// шапка и материал нужны и рядом с prepared-файлом.
func Load(root, id string, record []byte) (State, error) {
	data, err := os.ReadFile(PreparedPath(root, id))
	if err == nil {
		return ParsePrepared(data)
	}
	if !os.IsNotExist(err) {
		return State{}, err
	}
	return Parse(record)
}

// SavePrepared пишет состояние в prepared-файл записи: состояние разбора
// живёт только новым видом (DK-1469, решение 2).
func SavePrepared(root, id string, s State) error {
	data, err := RenderPrepared(s)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(PreparedPath(root, id)), 0755); err != nil {
		return err
	}
	return os.WriteFile(PreparedPath(root, id), data, 0644)
}

func tomlArray(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(v))
	for _, s := range v {
		parts = append(parts, subtoml.Quote(s))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func writeKeyValue(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "%s = %s\n", key, value)
}

// RenderPrepared собирает канонический вид prepared-файла: по нему сверяются
// снимки показанного, поэтому вид обязан быть устойчив к круглым заходам.
func RenderPrepared(s State) ([]byte, error) {
	if err := s.validatePrepared(); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(preparedFence + "\n")
	writeKeyValue(&b, "version", strconv.Itoa(s.Version))
	writeKeyValue(&b, "state", tomlString(s.State))
	if s.Analysis != "" {
		writeKeyValue(&b, "analysis", tomlString(s.Analysis))
	}
	keys := make([]string, 0, len(s.Proposals))
	for _, p := range s.Proposals {
		keys = append(keys, p.ID)
	}
	writeKeyValue(&b, "keys", tomlArray(keys))
	for _, r := range s.Receipts {
		b.WriteString("\n[[receipts]]\n")
		if r.Source != "" {
			writeKeyValue(&b, "source", tomlString(r.Source))
		}
		writeKeyValue(&b, "key", tomlString(r.Key))
		writeKeyValue(&b, "proposal", tomlString(r.Proposal))
		writeKeyValue(&b, "revision", strconv.Itoa(r.Revision))
		if r.Outcome != "" {
			writeKeyValue(&b, "outcome", tomlString(r.Outcome))
		}
		if len(r.IDs) > 0 {
			writeKeyValue(&b, "ids", tomlArray(r.IDs))
		}
		writeKeyValue(&b, "result", tomlString(r.Result))
		if r.Reason != "" {
			writeKeyValue(&b, "reason", tomlString(r.Reason))
		}
	}
	b.WriteString("```\n")
	for _, p := range s.Proposals {
		title := p.Title
		if title != "" {
			title = " - " + title
		}
		fmt.Fprintf(&b, "\n## %s%s\n\n%s\n", p.ID, title, preparedFence)
		writeKeyValue(&b, "revision", strconv.Itoa(p.Revision))
		writeKeyValue(&b, "outcome", tomlString(p.Outcome))
		card := []struct {
			key   string
			value string
			set   bool
		}{
			{"reason", p.Reason, p.Reason != ""},
			{"rationale", p.Rationale, p.Rationale != ""},
			{"board_type", p.BoardType, p.BoardType != ""},
			{"rank", p.Rank, p.Rank != ""},
			{"size", p.Size, p.Size != ""},
			{"accept", p.Accept, p.Accept != ""},
			{"barrier", p.Barrier, p.Barrier != ""},
			{"control", p.Control, p.Control != ""},
			{"assignee", p.Assignee, p.Assignee != ""},
			{"due", p.Due, p.Due != ""},
			{"target", p.Target, p.Target != ""},
			{"until", p.Until, p.Until != ""},
			{"status", p.Status, p.Status != ""},
		}
		for _, f := range card {
			if f.set {
				writeKeyValue(&b, f.key, tomlString(f.value))
			}
		}
		if len(p.After) > 0 {
			writeKeyValue(&b, "after", tomlArray(p.After))
		}
		b.WriteString("```\n\n")
		b.WriteString(strings.TrimRight(p.Text, "\n"))
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

func tomlString(s string) string { return subtoml.Quote(s) }

// takeFence снимает с начала текста блок в заборе ```toml и отдаёт его тело и
// остаток текста после забора.
func takeFence(text string) (string, string, error) {
	s := strings.TrimLeft(text, "\n")
	if !strings.HasPrefix(s, preparedFence+"\n") {
		return "", "", fmt.Errorf("жду блок TOML в заборе ```toml")
	}
	rest := s[len(preparedFence)+1:]
	if body, tail, ok := strings.Cut(rest, "\n```\n"); ok {
		return body, tail, nil
	}
	if strings.HasSuffix(rest, "\n```") {
		return rest[1 : len(rest)-4], "", nil
	}
	return "", "", fmt.Errorf("машинный блок TOML не закрыт забором")
}

func checkFields(name string, t *subtoml.Table, allowed string) error {
	for _, k := range t.Keys {
		if !strings.Contains(" "+allowed+" ", " "+k+" ") {
			return fmt.Errorf("%s: неизвестное поле %s", name, subtoml.Quote(k))
		}
	}
	return nil
}

func wantStr(name, key string, t *subtoml.Table) (string, error) {
	v, ok := t.Get(key)
	if !ok {
		return "", nil
	}
	if v.Kind != subtoml.KindStr {
		return "", fmt.Errorf("%s: поле %s жду строкой", name, key)
	}
	return v.Str, nil
}

func wantInt(name, key string, t *subtoml.Table) (int, error) {
	v, ok := t.Get(key)
	if !ok {
		return 0, nil
	}
	if v.Kind != subtoml.KindInt {
		return 0, fmt.Errorf("%s: поле %s жду целым", name, key)
	}
	return v.Int, nil
}

func wantArr(name, key string, t *subtoml.Table) ([]string, error) {
	v, ok := t.Get(key)
	if !ok {
		return nil, nil
	}
	if v.Kind != subtoml.KindArr {
		return nil, fmt.Errorf("%s: поле %s жду массивом строк", name, key)
	}
	return v.Arr, nil
}

func parseReceipt(name, body string) (Receipt, error) {
	var r Receipt
	doc, err := subtoml.Parse(name, body)
	if err != nil {
		return r, err
	}
	t := doc.Table("")
	if err = checkFields(name, t, receiptFields); err != nil {
		return r, err
	}
	if r.Source, err = wantStr(name, "source", t); err != nil {
		return r, err
	}
	if r.Key, err = wantStr(name, "key", t); err != nil {
		return r, err
	}
	if r.Proposal, err = wantStr(name, "proposal", t); err != nil {
		return r, err
	}
	if r.Revision, err = wantInt(name, "revision", t); err != nil {
		return r, err
	}
	if r.Outcome, err = wantStr(name, "outcome", t); err != nil {
		return r, err
	}
	if r.IDs, err = wantArr(name, "ids", t); err != nil {
		return r, err
	}
	if r.Result, err = wantStr(name, "result", t); err != nil {
		return r, err
	}
	if r.Reason, err = wantStr(name, "reason", t); err != nil {
		return r, err
	}
	return r, nil
}

// sectionHeading узнаёт заголовок раздела задачи и разбирает его на ключ и
// заголовок: «## p1 - Название» либо «## p1» без названия.
func sectionHeading(line string) (key, title string, ok bool) {
	if !strings.HasPrefix(line, "## ") {
		return "", "", false
	}
	rest := line[3:]
	if k, t, has := strings.Cut(rest, " - "); has {
		return k, t, true
	}
	return rest, "", true
}

func findHeading(lines []string, key string, from int) int {
	for i := from; i < len(lines); i++ {
		if k, _, ok := sectionHeading(lines[i]); ok && k == key {
			return i
		}
	}
	return -1
}

// parseSection читает раздел одной задачи: заголовок «## <ключ> -
// <заголовок>», блок TOML с полями карточки и тело до раздела nextKey.
func parseSection(key, nextKey, text string) (Proposal, string, error) {
	lines := strings.Split(text, "\n")
	start := findHeading(lines, key, 0)
	if start < 0 {
		return Proposal{}, "", fmt.Errorf("%s: раздела задачи нет в prepared-файле", key)
	}
	end := len(lines)
	if nextKey != "" {
		if next := findHeading(lines, nextKey, start+1); next >= 0 {
			end = next
		}
	}
	_, title, _ := sectionHeading(lines[start])
	block, tail, err := takeFence(strings.Join(lines[start+1:end], "\n"))
	if err != nil {
		return Proposal{}, "", fmt.Errorf("%s: %w", key, err)
	}
	doc, err := subtoml.Parse(key, block)
	if err != nil {
		return Proposal{}, "", err
	}
	t := doc.Table("")
	if err = checkFields(key, t, cardFields); err != nil {
		return Proposal{}, "", err
	}
	p := Proposal{ID: key, Title: title}
	if p.Revision, err = wantInt(key, "revision", t); err != nil {
		return p, "", err
	}
	strs := []struct {
		key string
		dst *string
	}{
		{"outcome", &p.Outcome}, {"reason", &p.Reason}, {"rationale", &p.Rationale},
		{"board_type", &p.BoardType}, {"rank", &p.Rank}, {"size", &p.Size}, {"accept", &p.Accept},
		{"barrier", &p.Barrier}, {"control", &p.Control}, {"assignee", &p.Assignee}, {"due", &p.Due},
		{"target", &p.Target}, {"until", &p.Until}, {"status", &p.Status},
	}
	for _, f := range strs {
		if *f.dst, err = wantStr(key, f.key, t); err != nil {
			return p, "", err
		}
	}
	if p.After, err = wantArr(key, "after", t); err != nil {
		return p, "", err
	}
	p.Text = strings.TrimSpace(tail)
	return p, strings.Join(lines[end:], "\n"), nil
}

// ParsePrepared разбирает prepared-файл целиком: машинный блок, разделы задач в
// порядке ключей и восстановление результатов из журнала receipts.
func ParsePrepared(data []byte) (State, error) {
	header, rest, err := takeFence(string(data))
	if err != nil {
		return State{}, err
	}
	var top strings.Builder
	var receipts []Receipt
	var group strings.Builder
	inReceipt := false
	for _, raw := range strings.Split(header, "\n") {
		if strings.TrimSpace(raw) == "[[receipts]]" {
			if inReceipt {
				r, err := parseReceipt("receipts", group.String())
				if err != nil {
					return State{}, err
				}
				receipts = append(receipts, r)
			}
			group.Reset()
			inReceipt = true
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(raw), "[") {
			return State{}, fmt.Errorf("prepared: секции кроме [[receipts]] не поддержаны: %s", raw)
		}
		if inReceipt {
			group.WriteString(raw)
			group.WriteString("\n")
		} else {
			top.WriteString(raw)
			top.WriteString("\n")
		}
	}
	if inReceipt {
		r, err := parseReceipt("receipts", group.String())
		if err != nil {
			return State{}, err
		}
		receipts = append(receipts, r)
	}
	doc, err := subtoml.Parse("prepared", top.String())
	if err != nil {
		return State{}, err
	}
	t := doc.Table("")
	if err = checkFields("prepared", t, "version state analysis keys"); err != nil {
		return State{}, err
	}
	state := State{Receipts: receipts}
	if state.Version, err = wantInt("prepared", "version", t); err != nil {
		return State{}, err
	}
	if state.State, err = wantStr("prepared", "state", t); err != nil {
		return State{}, err
	}
	if state.Analysis, err = wantStr("prepared", "analysis", t); err != nil {
		return State{}, err
	}
	keys, err := wantArr("prepared", "keys", t)
	if err != nil {
		return State{}, err
	}
	for i, key := range keys {
		nextKey := ""
		if i+1 < len(keys) {
			nextKey = keys[i+1]
		}
		var p Proposal
		p, rest, err = parseSection(key, nextKey, rest)
		if err != nil {
			return State{}, err
		}
		state.Proposals = append(state.Proposals, p)
	}
	if strings.TrimSpace(rest) != "" {
		return State{}, fmt.Errorf("в prepared-файле лишний текст после разделов задач")
	}
	for i := range state.Proposals {
		p := &state.Proposals[i]
		for _, r := range receipts {
			if r.Proposal == p.ID && r.Revision == p.Revision {
				rp := r
				p.Result = &rp
			}
		}
	}
	if err = state.validatePrepared(); err != nil {
		return State{}, err
	}
	return state, nil
}

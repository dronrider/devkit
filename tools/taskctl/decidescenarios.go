package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

const scenarioRecheckNote = "сверить зависимые решения по содержанию; затронутые пометить «требует сверки ГГГГ-ММ-ДД: причина»"

func decideWrite(path, orig, doc string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, []byte(orig)) {
		return fmt.Errorf("запись изменилась перед сохранением: перечитать её и повторить ответ по свежему блоку")
	}
	return os.WriteFile(path, []byte(doc), 0o644)
}

func decidePack(doc string, p DecideParams) ([]taskform.Fork, error) {
	if name := decideName(p.Name); name != "" {
		f, ok := taskform.FindFork(doc, name)
		if !ok {
			return nil, fmt.Errorf("развилки «%s» нет", name)
		}
		return []taskform.Fork{f}, nil
	}
	return decideOpen(doc), nil
}

func prepareScenarioChat(doc string, p DecideParams) (string, error) {
	pack, err := decidePack(doc, p)
	if err != nil {
		return "", err
	}
	if len(pack) > 4 {
		pack = pack[:4]
	}
	for _, f := range pack {
		if f.Mode == "" {
			continue
		}
		snapshot, err := taskform.NewScenarioSnapshot(p.ID, f)
		if err != nil {
			return "", err
		}
		if err := taskform.ValidateScenarioCards(snapshot.Cards, snapshot.Scenarios); err != nil {
			return "", err
		}
		key, raw := snapshot.Encoded()
		found := false
		for _, sub := range f.Sub {
			if strings.HasPrefix(sub, "снимок: "+key+" ") {
				found = true
			}
		}
		if found {
			if _, err := taskform.ReadScenarioSnapshot(f, p.ID, key); err != nil {
				return "", err
			}
			continue
		}
		doc, err = taskform.AppendToFork(doc, f.Name, "  - снимок: "+key+" "+raw)
		if err != nil {
			return "", err
		}
	}
	return doc, nil
}

func scenarioPositions(s taskform.ScenarioSnapshot) string {
	var out []string
	for i, c := range s.Scenarios {
		out = append(out, fmt.Sprintf("%d=%s (%s)", i+1, c.ID, c.Title))
	}
	return strings.Join(out, "; ")
}

func scenarioStale(id, key string, old, current taskform.ScenarioSnapshot) error {
	var changed []string
	if !reflect.DeepEqual(old.Scenarios, current.Scenarios) {
		changed = append(changed, "список, порядок, название или происхождение")
	}
	if old.Cards != current.Cards {
		changed = append(changed, "карточки")
	}
	if !reflect.DeepEqual(old.Recommendation, current.Recommendation) {
		changed = append(changed, "рекомендация или довод")
	}
	if old.Question != current.Question {
		changed = append(changed, "вопрос")
	}
	if !reflect.DeepEqual(old.Current, current.Current) || old.ChoiceEvent != current.ChoiceEvent {
		changed = append(changed, "выбор или событие выбора")
	}
	return fmt.Errorf("снимок %s устарел: %s\nпрежние позиции: %s\nнынешние позиции: %s\nсверить изменения и напечатать блок: taskctl decide %s «%s» --chat", key, strings.Join(changed, ", "), scenarioPositions(old), scenarioPositions(current), id, current.Name)
}

func scenarioAnswerFork(doc string, p DecideParams) (taskform.Fork, bool, error) {
	if p.Name != "" {
		f, ok := taskform.FindFork(doc, decideName(p.Name))
		if !ok {
			return f, false, fmt.Errorf("развилки «%s» нет", decideName(p.Name))
		}
		return f, f.Mode != "", nil
	}
	for _, f := range taskform.ParseForks(doc) {
		text := strings.ToLower(strings.TrimSpace(p.Answer))
		named := strings.HasPrefix(text, strings.ToLower(f.Name)+" ") || strings.HasPrefix(text, "«"+strings.ToLower(f.Name)+"» ")
		special := text == "ничего" || text == "по рекомендации" || strings.HasPrefix(text, "плюс ") || strings.HasPrefix(text, "минус ")
		if f.Mode != "" && (p.Snapshot != "" || named || (f.HoldsStart() && special)) {
			return f, true, nil
		}
	}
	return taskform.Fork{}, false, nil
}

func decideScenarioAnswer(doc string, f taskform.Fork, p DecideParams) (string, string, error) {
	snapshot, err := taskform.ReadScenarioSnapshot(f, p.ID, p.Snapshot)
	if err != nil {
		return "", "", err
	}
	replies, err := f.ScenarioReplies()
	if err != nil {
		return "", "", err
	}
	for i := len(replies) - 1; i >= 0; i-- {
		r := replies[i]
		if r.Snapshot == p.Snapshot && r.Text == p.Answer {
			if r.Event == f.ChoiceEvent {
				return doc, r.Result, nil
			}
			current, err := taskform.NewScenarioSnapshot(p.ID, f)
			if err != nil {
				return "", "", err
			}
			return "", "", scenarioStale(p.ID, p.Snapshot, snapshot, current)
		}
	}
	current, err := taskform.NewScenarioSnapshot(p.ID, f)
	if err != nil {
		return "", "", err
	}
	key, _ := current.Encoded()
	if key != p.Snapshot {
		return "", "", scenarioStale(p.ID, p.Snapshot, snapshot, current)
	}
	selection, err := taskform.ParseScenarioAnswer(f, snapshot, p.Answer)
	if err != nil {
		return "", "", err
	}
	if len(selection.Unparsed) > 0 {
		return doc, "не разобрано словами: " + strings.Join(selection.Unparsed, "; ") + "; список не закрыт, нужен разбор агента", nil
	}
	for _, added := range selection.Added {
		doc, err = taskform.AddForkOption(doc, f.Name, added.String())
		if err != nil {
			return "", "", err
		}
	}
	if selection.NextID != f.NextID {
		doc, err = taskform.SetForkField(doc, f.Name, "следующий ID", fmt.Sprintf("СЦ-%d", selection.NextID))
		if err != nil {
			return "", "", err
		}
	}
	result := "выбрано: " + strings.Join(selection.IDs, ", ")
	line := taskform.ForkDecisionLine(taskform.ByHuman, p.Now.Format("2006-01-02"), strings.Join(selection.IDs, ", "))
	if len(selection.IDs) == 0 {
		result = "отказ от всех сохранён; прежний состав отменён; выбор открыт для пересмотра границ"
		line = "  - пересмотр человеком " + p.Now.Format("2006-01-02") + ": отказ от всех; требуется пересмотр границ"
	}
	if len(selection.Added) > 0 {
		ids := []string{}
		for _, s := range selection.Added {
			ids = append(ids, s.ID)
		}
		result += "\nдобавлено: " + strings.Join(ids, ", ") + "; дополнить карточки словами пользователя"
	}
	result += "\n" + scenarioRecheckNote
	doc, err = taskform.AppendToFork(doc, f.Name, line+"\n"+taskform.ScenarioReplyLine(taskform.ScenarioReply{Text: p.Answer, Snapshot: p.Snapshot, Event: f.ChoiceEvent + 1, Result: result}))
	if err != nil {
		return "", "", err
	}
	updated, _ := taskform.FindFork(doc, f.Name)
	if _, _, err := updated.Scenarios(); err != nil {
		return "", "", err
	}
	return doc, result, nil
}

func scenarioBlock(f taskform.Fork, id string) (string, error) {
	s, err := taskform.NewScenarioSnapshot(id, f)
	if err != nil {
		return "", err
	}
	key, _ := s.Encoded()
	out := []string{}
	if s.Cards != "" {
		out = append(out, "## Пользовательские сценарии\n\n"+s.Cards)
	}
	for _, c := range s.Scenarios {
		out = append(out, fmt.Sprintf("%s. %s (%s)", c.ID, c.Title, c.Origin))
	}
	out = append(out, "снимок сценариев: "+key)
	head := fmt.Sprintf("«%s»: %s", f.Name, f.Question)
	if len(s.Current) > 0 {
		head += "; выбрано: " + strings.Join(s.Current, ", ")
	}
	if f.Reconsidered {
		head += "; прежний состав отменён, требуется пересмотр границ"
	}
	var positions []string
	for _, id := range s.Recommendation.IDs {
		for i, c := range s.Scenarios {
			if c.ID == id {
				positions = append(positions, fmt.Sprint(i+1))
			}
		}
	}
	head += "; рекомендую " + strings.Join(positions, ", ") + ": " + s.Recommendation.Reason
	out = append(out, head)
	for i, c := range s.Scenarios {
		out = append(out, fmt.Sprintf("%d. %s. %s", i+1, c.ID, c.Title))
	}
	out = append(out, "\nответ строкой: «"+f.Name+" 1, 3», «минус 2», «плюс <свой сценарий>», своими словами или «по рекомендации»")
	out = append(out, "правки без полного набора: текущий выбор; до первого ответа основа рекомендованный набор; после отказа от всех основу нужно назвать")
	out = append(out, fmt.Sprintf("сохранить ответ: taskctl decide %s «%s» --snapshot '%s' --answer '<точные слова человека>'", id, f.Name, key))
	return strings.Join(out, "\n"), nil
}

func decideScenarioShow(doc string, p DecideParams) (string, error) {
	f, ok := taskform.FindFork(doc, decideName(p.Name))
	if !ok {
		return "", fmt.Errorf("развилки «%s» нет", decideName(p.Name))
	}
	snapshot, err := taskform.ReadScenarioSnapshot(f, p.ID, p.Snapshot)
	if err != nil {
		return "", err
	}
	_, raw := snapshot.Encoded()
	return "снимок сценариев: " + p.Snapshot + "\n" + scenarioPositions(snapshot) + "\n" + raw, nil
}

func decideScenarioGiven(doc string, p DecideParams) (string, error) {
	if strings.TrimSpace(p.Said) == "" {
		return "", fmt.Errorf("--given требует --said с дословным ответом человека или ссылкой на решение")
	}
	ids, err := taskform.ScenarioIDs(p.Given)
	if err != nil {
		return "", err
	}
	name := decideName(p.Ask)
	f, _ := taskform.FindFork(doc, name)
	s, err := taskform.NewScenarioSnapshot(p.ID, f)
	if err != nil {
		return "", err
	}
	if err := taskform.ValidateScenarioCards(s.Cards, s.Scenarios); err != nil {
		return "", err
	}
	for _, id := range ids {
		found := false
		for _, c := range s.Scenarios {
			if c.ID == id {
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("%s отсутствует в списке --given", id)
		}
	}
	key, raw := s.Encoded()
	doc, err = taskform.AppendToFork(doc, name, "  - снимок: "+key+" "+raw)
	if err != nil {
		return "", err
	}
	f, _ = taskform.FindFork(doc, name)
	p.Name, p.Snapshot, p.Answer = name, key, strings.Join(ids, ", ")
	updated, _, err := decideScenarioAnswer(doc, f, p)
	// Основание переноса хранится дословно в том же следе ответа.
	if err != nil {
		return "", err
	}
	after, _ := taskform.FindFork(updated, name)
	for _, sub := range after.Sub {
		if strings.HasPrefix(sub, "ответ: ") {
			var r taskform.ScenarioReply
			if err := json.Unmarshal([]byte(strings.TrimPrefix(sub, "ответ: ")), &r); err != nil {
				return "", err
			}
			r.Text = p.Said
			updated = strings.Replace(updated, "  - "+sub, taskform.ScenarioReplyLine(r), 1)
		}
	}
	return updated, nil
}

func decideScenarioOptions(doc string, f taskform.Fork, p DecideParams) (string, string, error) {
	if _, _, err := f.Scenarios(); err != nil {
		return "", "", err
	}
	next := f.NextID
	for _, o := range p.Opts {
		s, err := taskform.ParseScenario(o)
		if err != nil {
			return "", "", err
		}
		n, _ := taskform.ScenarioNumber(s.ID)
		for _, existing := range f.Options {
			old, _ := taskform.ParseScenario(existing)
			if old.ID == s.ID {
				return "", "", fmt.Errorf("ID %s уже есть; правка названия и перестановка делаются в Markdown с сохранением ID", s.ID)
			}
		}
		if n < next {
			return "", "", fmt.Errorf("ID %s уже выдан: следующий свободный СЦ-%d", s.ID, next)
		}
		next, err = taskform.NextScenarioNumber(n)
		if err != nil {
			return "", "", err
		}
		doc, err = taskform.AddForkOption(doc, f.Name, s.String())
		if err != nil {
			return "", "", err
		}
	}
	var err error
	doc, err = taskform.SetForkField(doc, f.Name, "следующий ID", fmt.Sprintf("СЦ-%d", next))
	if err != nil {
		return "", "", err
	}
	updated, _ := taskform.FindFork(doc, f.Name)
	if _, _, err := updated.Scenarios(); err != nil {
		return "", "", err
	}
	return doc, fmt.Sprintf("добавлено сценариев %d; следующий ID СЦ-%d", len(p.Opts), next), nil
}

func decideScenarioChat(root string, pack []taskform.Fork, p DecideParams, d askDeps, env func(string) string, more string) (string, error) {
	var blocks []string
	for _, f := range pack {
		if f.Mode != "" {
			block, err := scenarioBlock(f, p.ID)
			if err != nil {
				return "", err
			}
			blocks = append(blocks, block)
		} else {
			blocks = append(blocks, chatForkBlock(f)+"\n\n"+chatAnswerHint)
		}
	}
	if AskPanel(env) {
		if _, err := runAsk(root, AskParams{ID: p.ID, Pack: chatQuestions(pack), Quiet: true}, d, env); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s: в пачке развилок %d%s, блок ниже идёт в реплику как есть\n%s\n\n%s", p.ID, len(pack), more, chatFormatWarn, strings.Join(blocks, "\n\n")), nil
}

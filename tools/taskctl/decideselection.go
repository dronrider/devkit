package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskform"
)

type selectionReceipt struct {
	Reply     string         `json:"reply"`
	Selection chat.Selection `json:"selection"`
	Result    string         `json:"result"`
}

func decideSelection(doc string, p DecideParams) (string, string, error) {
	var input chat.Selection
	if err := json.Unmarshal([]byte(p.Selection), &input); err != nil {
		return "", "", fmt.Errorf("выбор не разобран: %w", err)
	}
	if p.SelectionReply == "" || input.Task != p.ID || len(input.Questions) == 0 || len(input.Questions) > chat.PackLimit {
		return "", "", fmt.Errorf("нужны ключ сообщения, задача и вопросы показанного выбора")
	}
	input.Tree = ""
	for i := range input.Questions {
		input.Questions[i].Labels = nil
	}
	key, _ := json.Marshal(input)
	for _, f := range taskform.ParseForks(doc) {
		for _, sub := range f.Sub {
			const prefix = "вложение ответа: "
			if !strings.HasPrefix(sub, prefix) {
				continue
			}
			var receipt selectionReceipt
			if json.Unmarshal([]byte(strings.TrimPrefix(sub, prefix)), &receipt) != nil {
				continue
			}
			if receipt.Reply != p.SelectionReply {
				continue
			}
			saved, _ := json.Marshal(receipt.Selection)
			if string(saved) != string(key) {
				return "", "", fmt.Errorf("ключ сообщения уже использован для другого выбора")
			}

			return doc, receipt.Result, nil
		}
	}
	if p.SelectionRepeatOnly {
		return "", "", fmt.Errorf("квитанция принятого выбора не найдена")
	}
	original := doc
	seen := map[string]bool{}
	var results []string
	for _, answer := range input.Questions {
		f, ok := taskform.FindFork(original, answer.Fork)
		if !ok || seen[answer.Fork] || answer.Snapshot == "" || answer.IDs == nil {
			return "", "", fmt.Errorf("нужны уникальные вопросы, снимки и явный список ID")
		}
		seen[answer.Fork] = true
		ids := map[string]bool{}
		for _, id := range answer.IDs {
			if ids[id] {
				return "", "", fmt.Errorf("повтор ID %s", id)
			}
			ids[id] = true
		}
		if f.Mode == taskform.ScenarioMode {
			snapshot, err := taskform.ReadScenarioSnapshot(f, p.ID, answer.Snapshot)
			if err != nil {
				return "", "", err
			}
			available := map[string]bool{}
			for _, item := range snapshot.Scenarios {
				available[item.ID] = true
			}
			for _, id := range answer.IDs {
				if !available[id] {
					return "", "", fmt.Errorf("ID %s не был показан", id)
				}
			}
			value := strings.Join(answer.IDs, ", ")
			if len(answer.IDs) == 0 {
				value = "ничего"
			}
			next := p
			next.Name, next.Snapshot, next.Answer = f.Name, answer.Snapshot, value
			var result string
			var err2 error
			doc, result, err2 = decideScenarioAnswer(doc, f, next)
			if err2 != nil {
				return "", "", err2
			}
			results = append(results, result)
		} else {
			q := chatQuestions([]taskform.Fork{f})[0]
			if chat.QuestionSnapshot(q) != answer.Snapshot || !f.HoldsStart() {
				return "", "", fmt.Errorf("снимок вопроса «%s» устарел", f.Name)
			}
			if len(answer.IDs) != 1 {
				return "", "", fmt.Errorf("вопросу «%s» нужен один вариант", f.Name)
			}
			n, err := strconv.Atoi(answer.IDs[0])
			if err != nil || n < 1 || n > len(f.Choices()) || strconv.Itoa(n) != answer.IDs[0] {
				return "", "", fmt.Errorf("ID варианта не был показан в «%s»", f.Name)
			}
			text := f.Choices()[n-1].Text
			doc, err = taskform.AppendToFork(doc, f.Name, taskform.ForkDecisionLine(taskform.ByHuman, p.Now.Format("2006-01-02"), text))
			if err != nil {
				return "", "", err
			}
			results = append(results, "решено: «"+f.Name+"» "+text)
		}
	}
	receipt := selectionReceipt{Reply: p.SelectionReply, Selection: input, Result: strings.Join(results, "\n")}
	data, _ := json.Marshal(receipt)
	doc, err := taskform.AppendToFork(doc, input.Questions[0].Fork, "  - вложение ответа: "+string(data))
	return doc, receipt.Result, err
}

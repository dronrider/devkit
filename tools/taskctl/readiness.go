package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

func prepareTaskBody(root, id string, p AddParams, rank string) (string, bool, error) {
	if _, err := os.Stat(taskFileAbs(root, id)); err == nil && p.BodyFile != taskFileAbs(root, id) {
		return "", false, fmt.Errorf("файл задачи %s уже существует", id)
	} else if err != nil && !os.IsNotExist(err) {
		return "", false, err
	}
	draft, err := os.ReadFile(draftPath(root, id))
	promoted := err == nil
	if err != nil && !os.IsNotExist(err) {
		return "", false, err
	}
	if promoted && p.BodyFile != "" {
		return "", false, fmt.Errorf("--body-file несовместим с оформлением черновика --id: заполните сам черновик")
	}
	body := fmt.Sprintf("# %s: %s\n", id, p.Title)
	switch {
	case promoted:
		body = renderTaskFromDraft(id, p.Title, rank, string(draft), time.Now().Format(draftDateLayout))
	case p.BodyFile != "":
		var data []byte
		if p.BodyFile == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(p.BodyFile)
		}
		if err != nil {
			return "", false, fmt.Errorf("--body-file: %w", err)
		}
		text := strings.TrimSpace(string(data))
		if strings.HasPrefix(text, "# ") {
			_, text, _ = strings.Cut(text, "\n")
		}
		body += "\n" + strings.TrimSpace(text) + "\n"
		if needsDoD(p.Type, p.Title) {
			body = taskform.WithRankFormula(body, rank)
		}
		if needsDoD(p.Type, p.Title) && !hasStageSection(body) {
			body = taskform.InsertSection(body, taskform.Stages, "")
		}
	}
	if needsDoD(p.Type, p.Title) {
		if err := taskform.ValidateTask(body); err != nil {
			return "", false, fmt.Errorf("%s: %w (прямое создание: --body-file <путь>)", id, err)
		}
	}
	return body, promoted, nil
}

func hasStageSection(doc string) bool {
	for _, section := range splitSections(doc, "## ") {
		if section.heading == taskform.Stages {
			return true
		}
	}
	return false
}

func taskReady(root string, row *Row) error {
	if !needsDoD(row.Type, row.Title) {
		return nil
	}
	data, err := os.ReadFile(taskFileAbs(root, row.ID))
	if err != nil {
		return fmt.Errorf("%s: постановка: %w", row.ID, err)
	}
	if err := taskform.ValidateTask(string(data)); err != nil {
		return fmt.Errorf("%s: %w", row.ID, err)
	}
	return nil
}

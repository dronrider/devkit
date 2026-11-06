package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

const draftIdeasHeading = "## Идеи"

// ideasRange находит границы раздела «Идеи»: от его заголовка до следующего
// заголовка того же уровня или до конца файла.
func (t *draftText) ideasRange() (int, int, bool) {
	for i, ln := range t.lines {
		if strings.TrimSpace(ln) != draftIdeasHeading {
			continue
		}
		end := len(t.lines)
		for j := i + 1; j < len(t.lines); j++ {
			if strings.HasPrefix(t.lines[j], "## ") {
				end = j
				break
			}
		}
		return i, end, true
	}
	return 0, 0, false
}

// addIdea дописывает датированный пункт в раздел «Идеи»: раздела не было,
// значит он встаёт перед разделом «Грумминг», если тот есть, либо в конец
// файла. Мысль человека видна в записи целиком, судьбу пункту приписывает
// грумер на разборе (DK-1469, решение 3).
func (t *draftText) addIdea(line string) {
	if s, e, ok := t.ideasRange(); ok {
		at := e
		for at > s+1 && strings.TrimSpace(t.lines[at-1]) == "" {
			at--
		}
		rest := append([]string{}, t.lines[e:]...)
		t.lines = append(t.lines[:at], append([]string{line, ""}, rest...)...)
		return
	}
	t.trimTail()
	block := []string{"", draftIdeasHeading, "", line, ""}
	at := len(t.lines)
	if s, _, ok := t.groomRange(); ok && s < at {
		at = s
	}
	if at == 0 {
		block = block[1:]
	}
	rest := append([]string{}, t.lines[at:]...)
	t.lines = append(t.lines[:at], append(block, rest...)...)
}

// cmdDraftIdea приписывает мысль человека в раздел «Идеи» записи черновика.
// Идея живёт рядом с исходным материалом: отдельный накопитель её прячет и
// заводит вторую сущность ради одной кнопки.
func cmdDraftIdea(root, id, text string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "draft idea"); err != nil {
		return "", err
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", fmt.Errorf("текст мысли пуст, дайте содержательный пункт")
	}
	all, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	if old := findDraft(all, id); old != nil && old.History {
		return "", fmt.Errorf("%s: разбор завершён и сохранён в истории; новую мысль запишите отдельным черновиком", id)
	}
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	t, err := loadDraftText(d.Path)
	if err != nil {
		return "", err
	}
	t.addIdea("- " + timeNow().Format(draftDateLayout) + ": " + text)
	if err := t.save(); err != nil {
		return "", err
	}
	rel := draftRel(id)
	tail, err := c.apply(root, []string{rel})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: мысль записана в раздел «Идеи» файла %s%s", id, filepath.ToSlash(rel), tail), nil
}

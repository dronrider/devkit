package main

import (
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/draftmeta"
)

func validateDraftMeta(root, kind, source, lock string) error {
	if kind != "" {
		set, err := draftTypeSet(root)
		if err != nil {
			return err
		}
		if _, ok := set.Types[kind]; !ok {
			return fmt.Errorf("типа %q нет в словаре", kind)
		}
	}
	if strings.ContainsAny(source+lock, "\r\n") {
		return fmt.Errorf("метаданные должны быть одной строкой")
	}
	text := "# запись\n"
	if source != "" {
		text += "источник: " + source + "\n"
	}
	if lock != "" {
		text += "замок: " + lock + "\n"
	}
	_, err := draftmeta.Parse([]byte(text))
	return err
}

func setDraftMeta(t *draftText, kind, source, lock string) {
	for _, field := range []struct{ key, value string }{{"тип", kind}, {"источник", source}, {"замок", lock}} {
		if field.value == "" {
			continue
		}
		found := false
		for i, line := range t.lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "## ") {
				break
			}
			if strings.HasPrefix(line, field.key+":") {
				t.lines[i] = field.key + ": " + field.value
				found = true
				break
			}
		}
		if !found {
			at := 1
			if len(t.lines) < at {
				at = len(t.lines)
			}
			t.lines = append(t.lines[:at], append([]string{field.key + ": " + field.value}, t.lines[at:]...)...)
		}
	}
}

func cmdDraftMeta(root, id, kind, source, lock string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if kind == "" && source == "" && lock == "" {
		return "", fmt.Errorf("укажите хотя бы одно поле: --type, --source или --lock")
	}
	if err := validateDraftMeta(root, kind, source, lock); err != nil {
		return "", err
	}
	if err := boardGuard(root, "draft meta"); err != nil {
		return "", err
	}
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	t, err := loadDraftText(d.Path)
	if err != nil {
		return "", err
	}
	setDraftMeta(t, kind, source, lock)
	if err = t.save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{draftRel(id)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: поля черновика изменены%s", id, tail), nil
}

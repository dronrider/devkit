package main

import (
	"fmt"
	"github.com/dronrider/devkit/internal/reviewaudit"
)

func reviewAuditPath(root, id string) (string, error) {
	return reviewaudit.Path(root, id, noteRoot(root) == "")
}
func checkReviewAudit(root, start, id string) error {
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return err
	}
	return reviewaudit.Validate(reviewCodeDir(root, start), path, id)
}
func cmdReviewAudit(root, start, id, base string, refresh, check bool) (string, error) {
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return "", err
	}
	code := reviewCodeDir(root, start)
	if check {
		if err := reviewaudit.Validate(code, path, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: полнота и актуальность отчёта подтверждены: %s; истинность выводов оценивает ревьювер", id, path), nil
	}
	out, err := reviewaudit.Create(code, path, id, base, refresh)
	if err != nil {
		return "", err
	}
	return "Отчёт ревью: " + out + "; заполните архитектурную оценку и каждое правило, затем review audit-check " + id, nil
}

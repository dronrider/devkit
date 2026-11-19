package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dronrider/devkit/internal/reviewaudit"
)

func cmdReviewAuditTemplate(dir, id, check, object, result, kind string) (string, error) {
	entries, err := reviewaudit.EvidenceTemplates(dir, id, check, object, result, kind)
	if err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(entries, "", "  ")
	return string(body), err
}

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

func cmdReviewAuditRepair(root, start, id, request, summary string, targets bool) (string, error) {
	if !targets {
		return "", fmt.Errorf("укажите --targets: команда восстанавливает только перечень targets")
	}
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return "", err
	}
	return reviewaudit.RepairTargets(reviewCodeDir(root, start), path, id, request, summary)
}

func cmdReviewAuditRecord(root, start, id, rule, object, input, request, summary string) (string, error) {
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return "", err
	}
	return reviewaudit.RecordNameEvidence(reviewCodeDir(root, start), path, id, rule, object, input, request, summary)
}

func cmdReviewAuditProgress(root, start, id, request, summary string, asJSON bool) (string, error) {
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return "", err
	}
	progress, err := reviewaudit.NameProgress(reviewCodeDir(root, start), path, id, request, summary)
	if err != nil {
		return "", err
	}
	if asJSON {
		body, err := json.MarshalIndent(progress, "", "  ")
		return string(body), err
	}
	return progress.String(), nil
}

func cmdReviewAudit(root, start, id, base, coverage, request string, refresh, check bool) (string, error) {
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return "", err
	}
	code := reviewCodeDir(root, start)
	if check {
		if coverage != "" {
			next, err := reviewaudit.CoverageWithNextSteps(code, path, id, coverage, request)
			if err != nil {
				if len(next) != 0 {
					err = errors.Join(err, errors.New(strings.Join(next, "\n")))
				}
				return "", err
			}
			message := fmt.Sprintf("%s: обязательные объекты, доказательства и ссылки на итог сверены", id)
			if len(next) != 0 {
				message += "\n" + strings.Join(next, "\n")
			}
			return message, nil
		}
		if err := reviewaudit.ValidateWithRequest(code, path, id, request); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: полнота и актуальность отчёта подтверждены: %s; истинность выводов оценивает ревьювер", id, path), nil
	}
	if coverage != "" {
		return "", fmt.Errorf("--coverage применяется с review audit-check")
	}
	out, err := reviewaudit.CreateWithRequest(code, path, id, base, request, refresh)
	if err != nil {
		return "", err
	}
	return "Отчёт ревью: " + out + "; заполните архитектурную оценку и результаты по правилам", nil
}

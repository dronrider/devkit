package reviewaudit

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/taskform"
)

// VerdictPacket is the model's view of a strictly validated audit snapshot.
type VerdictPacket struct {
	ID           string              `json:"id"`
	BaseSHA      string              `json:"base_sha"`
	HeadSHA      string              `json:"head_sha"`
	DiffBaseSHA  string              `json:"diff_base_sha"`
	CodeHash     string              `json:"code_hash"`
	TaskHash     string              `json:"task_hash"`
	Task         string              `json:"task"`
	PriorReview  string              `json:"prior_review"`
	Diff         string              `json:"diff"`
	Architecture Architecture        `json:"architecture"`
	Rules        []verdictPacketRule `json:"rules"`
	TestEvidence string              `json:"test_evidence"`
}

type verdictPacketRule struct {
	ID            string                  `json:"id"`
	Text          string                  `json:"text"`
	Applicability string                  `json:"applicability"`
	Reason        string                  `json:"reason"`
	Evidence      []verdictPacketEvidence `json:"evidence,omitempty"`
}

type verdictPacketEvidence struct {
	Object      string `json:"object"`
	Check       string `json:"check,omitempty"`
	Location    string `json:"location"`
	Result      string `json:"result,omitempty"`
	Observation string `json:"observation"`
}

func PrepareVerdictPacket(root, path, id, request, since, testEvidence string) (VerdictPacket, error) {
	root, report, now, err := validatedSnapshot(root, path, id, request)
	if err != nil {
		return VerdictPacket{}, err
	}
	header, tests, _ := strings.Cut(testEvidence, "\n")
	if strings.TrimSpace(header) != "head_sha: "+now.HeadSHA || strings.TrimSpace(tests) == "" {
		return VerdictPacket{}, fmt.Errorf("первая строка результатов тестов должна быть head_sha: %s, дальше нужен вывод проверки", now.HeadSHA)
	}
	diffBase := now.BaseSHA
	if since != "" {
		var err error
		diffBase, err = git(root, "rev-parse", "--verify", "--end-of-options", since+"^{commit}")
		if err != nil {
			return VerdictPacket{}, fmt.Errorf("предыдущая ревизия ревью: %w", err)
		}
		diffBase = strings.TrimSpace(diffBase)
		if _, err := git(root, "merge-base", "--is-ancestor", now.BaseSHA, diffBase); err != nil {
			return VerdictPacket{}, fmt.Errorf("предыдущая ревизия находится вне базы аудита")
		}
		if diffBase == now.HeadSHA {
			return VerdictPacket{}, fmt.Errorf("для ревью того же снимка передайте новые факты без --since")
		}
		if _, err := git(root, "merge-base", "--is-ancestor", diffBase, now.HeadSHA); err != nil {
			return VerdictPacket{}, fmt.Errorf("предыдущая ревизия ревью должна быть предком проверяемой")
		}
	}
	diff, err := verdictDiff(root, path, diffBase, now.HeadSHA)
	if err != nil {
		return VerdictPacket{}, err
	}
	var notes string
	if card := taskPath(path); card != "" {
		rel, err := filepath.Rel(root, card)
		if err != nil {
			return VerdictPacket{}, err
		}
		rel = filepath.ToSlash(rel)
		_, body, err := nameRuntimeBlob(root, now.HeadSHA, rel)
		if err != nil {
			return VerdictPacket{}, err
		}
		if digest(taskContent(body)) != now.TaskHash {
			return VerdictPacket{}, fmt.Errorf("постановка пакета должна быть закоммичена")
		}
		notes = strings.TrimSpace(strings.Join(taskform.SectionLines(body, "## Ревью"), "\n"))
		if since != "" {
			_, before, err := nameRuntimeBlob(root, diffBase, rel)
			if err != nil || digest(taskContent(before)) != now.TaskHash {
				return VerdictPacket{}, fmt.Errorf("постановка изменилась после предыдущего ревью; подготовьте полный пакет без --since")
			}
		}
	} else if since != "" {
		return VerdictPacket{}, fmt.Errorf("--since требует карточку с записью прежнего вердикта")
	}
	packet := VerdictPacket{
		ID: id, BaseSHA: now.BaseSHA, HeadSHA: now.HeadSHA, DiffBaseSHA: diffBase,
		CodeHash: now.CodeHash, TaskHash: now.TaskHash, Task: now.taskText, PriorReview: notes,
		Diff: diff, Architecture: report.Architecture, TestEvidence: strings.TrimSpace(tests),
	}
	for _, rule := range report.Rules {
		item := verdictPacketRule{ID: rule.ID, Text: rule.Text, Applicability: rule.Applicability, Reason: rule.Reason}
		for _, evidence := range rule.Evidence {
			item.Evidence = append(item.Evidence, verdictPacketEvidence{
				Object: evidence.Object, Check: evidence.Check, Location: evidence.Location,
				Result: evidence.Result, Observation: evidence.Observation,
			})
		}
		packet.Rules = append(packet.Rules, item)
	}
	return packet, nil
}

func verdictDiff(root, path, base, head string) (string, error) {
	args := []string{"--no-replace-objects", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=none"}
	names, err := gitRaw(root, append(args, "--name-only", "-z", base, head, "--")...)
	if err != nil {
		return "", err
	}
	var paths []string
	for _, name := range strings.Split(names, "\x00") {
		if name != "" && !excluded(root, path, name) {
			paths = append(paths, ":(literal)"+name)
		}
	}
	if len(paths) == 0 {
		return "", nil
	}
	return gitRaw(root, append(append(args, "--unified=5", base, head, "--"), paths...)...)
}

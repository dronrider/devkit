package reviewaudit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareVerdictPacketRequiresCurrentCompleteAudit(t *testing.T) {
	root, path := fixture(t)
	report := completed(t, root, path)
	write(t, taskPath(path), "## DoD\n\nПравильный результат.\n")
	if _, err := PrepareVerdictPacket(root, path, "XR-1", "", "", "PASS"); err == nil || !strings.Contains(err.Error(), "постановка изменились") {
		t.Fatalf("пакет со старой постановкой: %v", err)
	}
	gitT(t, root, "restore", "--", taskPath(path))
	body, err := gitRaw(root, "cat-file", "blob", "HEAD:docs/tasks/XR-1.md")
	if err != nil {
		t.Fatal(err)
	}
	write(t, taskPath(path), body+"\n## Ревью\n\n- Открыто: проверить путь отказа.\n")
	gitT(t, root, "add", "--", taskPath(path))
	gitT(t, root, "commit", "-qm", "review note")

	packet, err := PrepareVerdictPacket(root, path, "XR-1", "", report.HeadSHA, "go test: PASS")
	if err != nil {
		t.Fatal(err)
	}
	if packet.HeadSHA == report.HeadSHA || packet.DiffBaseSHA != report.HeadSHA ||
		!strings.Contains(packet.Task, "Правильный результат") ||
		!strings.Contains(packet.PriorReview, "проверить путь отказа") ||
		packet.Diff != "" || len(packet.Rules) != len(report.Rules) ||
		packet.TestEvidence != "go test: PASS" {
		t.Fatalf("пакет не соответствует проверенной версии: %+v", packet)
	}

	report.Rules[0].Result = "pending"
	report.Rules[1].Result = "pending"
	report.Architecture.Result = "pending"
	save(t, path, report)
	_, err = PrepareVerdictPacket(root, path, "XR-1", "", "", "PASS")
	if err == nil || !strings.Contains(err.Error(), "архитектурная оценка не завершена") ||
		!strings.Contains(err.Error(), "правило "+report.Rules[0].ID+" не завершено") ||
		!strings.Contains(err.Error(), "правило "+report.Rules[1].ID+" не завершено") {
		t.Fatalf("вывод не перечислил незакрытые проверки: %v", err)
	}
}

func TestVerdictDiffUsesOnlySelectedCommittedChanges(t *testing.T) {
	root, path := fixture(t)
	base := gitT(t, root, "rev-parse", "HEAD")
	code := filepath.Join(root, "code.go")
	write(t, code, "package sample\n\nvar earlier = 7\n")
	gitT(t, root, "add", "--", code)
	gitT(t, root, "commit", "-qm", "earlier review")
	prior := gitT(t, root, "rev-parse", "HEAD")
	write(t, code, "package sample\n\nvar earlier = 7\nvar later = 8\n")
	card := taskPath(path)
	write(t, card, "## Ревью\n\nЗакрыто.\n")
	gitT(t, root, "add", "--", code, card)
	gitT(t, root, "commit", "-qm", "later change")
	head := gitT(t, root, "rev-parse", "HEAD")

	complete, err := verdictDiff(root, path, base, head)
	if err != nil || !strings.Contains(complete, "+var earlier = 7") || strings.Contains(complete, "docs/tasks") {
		t.Fatalf("полный дифф: %v\n%s", err, complete)
	}
	delta, err := verdictDiff(root, path, prior, head)
	if err != nil || strings.Contains(delta, "+var earlier = 7") || !strings.Contains(delta, "+var later = 8") || strings.Contains(delta, "docs/tasks") {
		t.Fatalf("дифф повторного ревью: %v\n%s", err, delta)
	}
}

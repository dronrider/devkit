package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dronrider/devkit/internal/reviewaudit"
)

func cmdReviewVerdict(root, start, id, request, since, checks, model, effort string, packetOnly bool) (string, error) {
	if checks == "" {
		return "", fmt.Errorf("укажите --checks с результатами тестов или основанием их неприменимости")
	}
	if !packetOnly && (model == "" || effort == "") {
		return "", fmt.Errorf("укажите --model и --effort из agentctl pick <ID> --role review")
	}
	tests, err := os.ReadFile(checks)
	if err != nil {
		return "", fmt.Errorf("результаты тестов: %w", err)
	}
	path, err := reviewAuditPath(root, id)
	if err != nil {
		return "", err
	}
	codeDir := reviewCodeDir(root, start)
	packet, err := reviewaudit.PrepareVerdictPacket(codeDir, path, id, request, since, string(tests))
	if err != nil {
		return "", err
	}
	if since != "" && !hasVerdictBasis(codeDir, packet.PriorReview, packet.DiffBaseSHA) {
		return "", fmt.Errorf("в карточке нет чистого вердикта для %s; подготовьте полный пакет без --since", packet.DiffBaseSHA)
	}
	data, err := json.Marshal(packet)
	if err != nil {
		return "", err
	}
	if packetOnly {
		return string(data), nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := runReviewVerdict(ctx, data, model, effort)
	if err != nil {
		return "", err
	}
	reply := struct {
		ID          string          `json:"id"`
		HeadSHA     string          `json:"head_sha"`
		DiffBaseSHA string          `json:"diff_base_sha"`
		Result      json.RawMessage `json:"result"`
	}{id, packet.HeadSHA, packet.DiffBaseSHA, json.RawMessage(result)}
	out, err := json.Marshal(reply)
	return string(out), err
}

func hasVerdictBasis(root, notes, revision string) bool {
	for line := range strings.SplitSeq(notes, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		note := reviewNote{Text: line[2:]}
		if note.outcome() != "чисто" {
			continue
		}
		if sha := cleanVerdictSha(note.Text); sha != "" {
			resolved, err := gitRevParse(root, "--verify", sha+"^{commit}")
			if err == nil && resolved == revision {
				return true
			}
		}
	}
	return false
}

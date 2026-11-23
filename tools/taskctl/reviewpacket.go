package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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
	packet, err := reviewaudit.PrepareVerdictPacket(reviewCodeDir(root, start), path, id, request, since, string(tests))
	if err != nil {
		return "", err
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

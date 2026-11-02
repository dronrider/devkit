package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/stage"
)

type preparedChatAnswer struct {
	Tree      string                   `json:"tree"`
	Snapshots []draftproposal.Snapshot `json:"snapshots"`
	Decisions []draftproposal.Decision `json:"decisions"`
}

// preparedQuestionShape собирает короткий вид вопроса согласования (DK-1469,
// решение 4): реплика агента, строка-ссылка на каждую задачу и опция
// подтверждения. Тела задач остаются в prepared-файле, чат их не носит.
func preparedQuestionShape(tree string, shown chat.PreparedQuestion) (string, []chat.Option) {
	project := filepath.Base(tree)
	text := "Подготовил задачи, согласуй их описание."
	var options []chat.Option
	for _, s := range shown.Sources {
		for _, p := range s.Proposals {
			title := p.Title
			if title == "" {
				title = p.ID
			}
			text += fmt.Sprintf("\n\n%s: #%s/draft/%s/prepared/%s", title, project, s.Source, p.ID)
			options = append(options, chat.Option{Label: "Подтвердить " + title})
		}
	}
	return text, options
}

func preparedChatQuestion(root, anchor string, also []string) (chat.Question, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return chat.Question{}, err
	}
	root = filepath.Clean(root)
	ids := append([]string{anchor}, also...)
	shown := chat.PreparedQuestion{Version: 1, Tree: root, Anchor: anchor}
	seen := map[string]bool{}
	for _, id := range ids {
		if !idRe.MatchString(id) || seen[id] {
			return chat.Question{}, fmt.Errorf("неверный или повторный ID %s", id)
		}
		seen[id] = true
		raw, err := cmdDraftPrepared(root, id)
		if err != nil {
			return chat.Question{}, err
		}
		var view preparedView
		if err := json.Unmarshal([]byte(raw), &view); err != nil {
			return chat.Question{}, err
		}
		s := chat.PreparedSource{Source: id, Basis: view.Basis}
		for _, p := range view.State.Proposals {
			if p.Status == "" {
				p.Text = ""
				s.Proposals = append(s.Proposals, p)
			}
		}
		if len(s.Proposals) == 0 {
			return chat.Question{}, fmt.Errorf("%s: нет неподтверждённых задач", id)
		}
		shown.Sources = append(shown.Sources, s)
	}
	if err := shown.Validate(); err != nil {
		return chat.Question{}, err
	}
	text, options := preparedQuestionShape(root, shown)
	return chat.Question{Text: text, Multi: true, Options: options, Prepared: &shown}, nil
}

func cmdPreparedChatAsk(root, anchor string, also []string, d askDeps, env func(string) string) (string, error) {
	// Этот JSON уезжает спрашивающему агенту. Каждый показанный источник
	// держится под тем же резервированием, что и клиент облака, включая
	// основное дерево.
	var locks []*draftmeta.CloudLock
	defer func() {
		for _, lock := range locks {
			lock.Close()
		}
	}()
	roots := []string{root}
	if main := stage.MainRoot(root); main != root {
		roots = append(roots, main)
	}
	ids := append([]string{anchor}, also...)
	for _, tree := range roots {
		for _, id := range ids {
			lock, err := draftmeta.CloudShared(tree, id)
			if err != nil {
				return "", err
			}
			locks = append(locks, lock)
		}
	}
	for _, tree := range roots {
		for _, id := range ids {
			if err := draftmeta.GuardRecord(tree, id); err != nil {
				return "", err
			}
		}
	}
	q, err := preparedChatQuestion(root, anchor, also)
	if err != nil {
		return "", err
	}
	if _, err = runAsk(root, AskParams{ID: anchor, Draft: true, Pack: []chat.Question{q}, Quiet: true}, d, env); err != nil {
		return "", err
	}
	data, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parsePreparedChatAnswer(input []byte) (preparedChatAnswer, error) {
	var a preparedChatAnswer
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return a, fmt.Errorf("лишние данные ответа")
	}
	return a, nil
}

// shownPrepared достаёт из признака ожидания показанный вопрос согласования:
// до миграции ask-файлы несут прежнее поле proposal, миграция переписывает их
// на prepared.
func shownPrepared(ask chat.Ask) *chat.PreparedQuestion {
	var shown *chat.PreparedQuestion
	for _, q := range ask.Questions {
		if q.Prepared == nil && q.Proposal == nil {
			continue
		}
		if shown != nil || q.Scenario != nil {
			return nil
		}
		if q.Prepared != nil {
			shown = q.Prepared
		} else {
			shown = q.Proposal
		}
	}
	return shown
}

func cmdPreparedChatAnswer(root, anchor string, input []byte, opts CommitOpts) (string, error) {
	a, err := parsePreparedChatAnswer(input)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if !idRe.MatchString(anchor) || a.Tree != root {
		return "", fmt.Errorf("неверное дерево или адрес вопроса")
	}
	req := draftproposal.ConfirmRequest{Snapshots: a.Snapshots, Decisions: a.Decisions}
	ask, ok := chat.ReadAsk(chat.AskPath(stage.MainRoot(root), chat.TaskName(anchor)))
	if ok {
		shown := shownPrepared(ask)
		if ask.Task != anchor || shown == nil || shown.Anchor != anchor {
			return preparedChatRepeated(root, req)
		}
		if err := chat.CheckPreparedAnswer(*shown, root, req); err != nil {
			if repeated, retryErr := preparedChatRepeated(root, req); retryErr == nil {
				return repeated, nil
			}
			return "", err
		}
		// A generic Ask may contain a forged task. Verify the displayed fields
		// against the saved source without replacing the displayed snapshot.
		if repeated, err := preparedChatRepeated(root, req); err == nil {
			return repeated, nil
		}
		for _, source := range shown.Sources {
			record, state, err := readPrepared(root, source.Source)
			if err != nil {
				return "", err
			}
			basis, err := preparedBasis(root, record, state)
			if err != nil {
				return "", err
			}
			var cards []draftproposal.Proposal
			for _, p := range state.Proposals {
				if p.Status == "" {
					p.Text = ""
					cards = append(cards, p)
				}
			}
			if basis != source.Basis || !reflect.DeepEqual(cards, source.Proposals) {
				return "", fmt.Errorf("%s: показанные задачи изменились", source.Source)
			}
		}
		data, err := json.Marshal(req)
		if err != nil {
			return "", err
		}
		return cmdDraftPreparedConfirm(root, data, opts)
	}
	return preparedChatRepeated(root, req)
}

func cmdPreparedChatReplay(root, anchor string, input []byte) (string, error) {
	a, err := parsePreparedChatAnswer(input)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if !idRe.MatchString(anchor) || a.Tree != root {
		return "", fmt.Errorf("неверное дерево или адрес вопроса")
	}
	return preparedChatRepeated(root, draftproposal.ConfirmRequest{Snapshots: a.Snapshots, Decisions: a.Decisions})
}

// A missing Ask permits receipt recovery only; it never invokes the writer.
func preparedChatRepeated(root string, req draftproposal.ConfirmRequest) (string, error) {
	if len(req.Decisions) == 0 || len(req.Snapshots) == 0 {
		return "", fmt.Errorf("показанный вопрос не найден")
	}
	sort.Slice(req.Snapshots, func(i, j int) bool { return req.Snapshots[i].Source < req.Snapshots[j].Source })
	sort.Slice(req.Decisions, func(i, j int) bool {
		a, b := req.Decisions[i], req.Decisions[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Proposal < b.Proposal
	})
	snaps := map[string]bool{}
	used := map[string]bool{}
	seen := map[string]bool{}
	for _, s := range req.Snapshots {
		if !idRe.MatchString(s.Source) || s.Digest == "" || snaps[s.Source] {
			return "", fmt.Errorf("неверный снимок")
		}
		snaps[s.Source] = true
	}
	for _, d := range req.Decisions {
		key := d.Source + "\x00" + d.Proposal
		if !snaps[d.Source] || d.Proposal == "" || d.Revision < 1 || seen[key] || (d.Action != "accept" && d.Action != "reject") || (d.Action == "reject" && strings.TrimSpace(d.Reason) == "") {
			return "", fmt.Errorf("неверное решение")
		}
		seen[key] = true
		used[d.Source] = true
	}
	if len(used) != len(snaps) {
		return "", fmt.Errorf("лишний снимок")
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	key := draftproposal.Digest(b)
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	var results []draftproposal.Receipt
	for _, d := range req.Decisions {
		_, state, err := readPrepared(root, d.Source)
		if err != nil {
			return "", err
		}
		found := false
		for _, receipt := range state.Receipts {
			if receipt.Key == key && receipt.Source == d.Source && receipt.Proposal == d.Proposal && receipt.Revision == d.Revision {
				results = append(results, receipt)
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("показанный вопрос не найден, повтор не подтверждён")
		}
	}
	sortReceipts(results)
	return marshal(confirmationResult{Key: key, Results: results, Repeated: true})
}

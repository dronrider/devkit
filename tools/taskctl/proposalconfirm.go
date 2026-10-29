package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

type confirmationPlan struct {
	Key      string
	Results  []draftproposal.Receipt
	Changes  []drafttx.Change
	Repeated bool
}

// prepareConfirmation builds all images before a caller performs live writes.
// Caller must hold the shared lock throughout validation and installation.
func prepareConfirmation(root string, request draftproposal.ConfirmRequest) (confirmationPlan, error) {
	var out confirmationPlan
	if len(request.Decisions) == 0 {
		return out, fmt.Errorf("не выбрано ни одно предложение")
	}
	sort.Slice(request.Snapshots, func(i, j int) bool { return request.Snapshots[i].Source < request.Snapshots[j].Source })
	sort.Slice(request.Decisions, func(i, j int) bool {
		a, b := request.Decisions[i], request.Decisions[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Proposal < b.Proposal
	})
	keyBytes, err := json.Marshal(request)
	if err != nil {
		return out, err
	}
	out.Key = draftproposal.Digest(keyBytes)
	snapshots := map[string]string{}
	for _, s := range request.Snapshots {
		if !idRe.MatchString(s.Source) || s.Digest == "" || snapshots[s.Source] != "" {
			return out, fmt.Errorf("неверный снимок %s", s.Source)
		}
		snapshots[s.Source] = s.Digest
	}
	decisions := map[string][]draftproposal.Decision{}
	seen := map[string]bool{}
	for _, d := range request.Decisions {
		key := d.Source + "\x00" + d.Proposal
		if !idRe.MatchString(d.Source) || d.Proposal == "" || d.Revision < 1 || seen[key] || snapshots[d.Source] == "" {
			return out, fmt.Errorf("неверное решение %s/%s", d.Source, d.Proposal)
		}
		seen[key] = true
		if d.Action != "accept" && d.Action != "reject" {
			return out, fmt.Errorf("%s: неизвестное решение", d.Proposal)
		}
		if d.Action == "reject" && strings.TrimSpace(d.Reason) == "" {
			return out, fmt.Errorf("%s: нужна причина отказа", d.Proposal)
		}
		decisions[d.Source] = append(decisions[d.Source], d)
	}
	if len(decisions) != len(snapshots) {
		return out, fmt.Errorf("снимок не совпадает с выбранными записями")
	}
	type record struct {
		data   []byte
		state  draftproposal.State
		header draftmeta.Header
	}
	records := map[string]record{}
	allRepeated := true
	for _, snap := range request.Snapshots {
		data, err := os.ReadFile(draftPath(root, snap.Source))
		if err != nil {
			return out, err
		}
		state, err := draftproposal.Parse(data)
		if err != nil {
			return out, err
		}
		header, err := draftmeta.Parse(data)
		if err != nil {
			return out, err
		}
		records[snap.Source] = record{data, state, header}
		for _, d := range decisions[snap.Source] {
			matched := false
			for _, r := range state.Receipts {
				if r.Key == out.Key && r.Proposal == d.Proposal && r.Revision == d.Revision {
					out.Results = append(out.Results, r)
					matched = true
					break
				}
			}
			if !matched {
				allRepeated = false
			}
		}
	}
	if allRepeated {
		sortReceipts(out.Results)
		out.Repeated = true
		return out, nil
	}
	out.Results = nil
	for _, d := range request.Decisions {
		if d.Action == "accept" {
			if err := boardGuard(root, "draft proposals confirm"); err != nil {
				return out, err
			}
			break
		}
	}
	for _, snap := range request.Snapshots {
		rec := records[snap.Source]
		basis, err := proposalBasis(root, rec.data, rec.state)
		if err != nil {
			return out, err
		}
		if basis != snap.Digest {
			return out, fmt.Errorf("%s: показанный снимок устарел", snap.Source)
		}
		for _, d := range decisions[snap.Source] {
			found := false
			for _, p := range rec.state.Proposals {
				if p.ID == d.Proposal {
					found = true
					if p.Status != "" || p.Revision != d.Revision {
						return out, fmt.Errorf("%s: решение уже принято или редакция изменилась", p.ID)
					}
					break
				}
			}
			if !found {
				return out, fmt.Errorf("%s: предложение %s не найдено", snap.Source, d.Proposal)
			}
		}
	}
	staged, err := newStagingBoard(root)
	if err != nil {
		return out, err
	}
	defer staged.Close()
	set, err := draftTypeSet(root)
	if err != nil {
		return out, err
	}
	sourcePaths := map[string]bool{}
	for _, snap := range request.Snapshots {
		rec := records[snap.Source]
		stageSource := draftPath(staged.root, snap.Source)
		data, err := os.ReadFile(stageSource)
		if err != nil || !bytes.Equal(data, rec.data) {
			return out, fmt.Errorf("%s: исходник изменился при подготовке", snap.Source)
		}
		def, ok := set.Types[rec.header.Type]
		if !ok {
			return out, fmt.Errorf("тип %s не найден", rec.header.Type)
		}
		var selected []string
		for _, d := range decisions[snap.Source] {
			if d.Action == "accept" {
				selected = append(selected, d.Proposal)
			}
		}
		receipts, err := staged.planProposals(snap.Source, rec.header, def, rec.state, selected)
		if err != nil {
			return out, err
		}
		byID := map[string]draftproposal.Receipt{}
		for _, r := range receipts {
			r.Key = out.Key
			r.Source = snap.Source
			byID[r.Proposal] = r
		}
		for _, d := range decisions[snap.Source] {
			if d.Action == "reject" {
				byID[d.Proposal] = draftproposal.Receipt{Key: out.Key, Source: snap.Source, Proposal: d.Proposal, Revision: d.Revision, Result: "отклонено", Reason: strings.TrimSpace(d.Reason)}
			}
		}
		done := len(rec.state.Proposals) > 0
		for i := range rec.state.Proposals {
			p := &rec.state.Proposals[i]
			if r, ok := byID[p.ID]; ok {
				if r.Outcome == "" {
					r.Outcome = p.Outcome
				}
				p.Result = &r
				if r.Result == "отклонено" {
					p.Status = "rejected"
				} else {
					p.Status = "accepted"
				}
				rec.state.Receipts = append(rec.state.Receipts, r)
				out.Results = append(out.Results, r)
			}
			if p.Status == "" {
				done = false
			}
		}
		if done {
			rec.state.State = "history"
		}
		updated, err := draftproposal.Replace(rec.data, rec.state)
		if err != nil {
			return out, err
		}
		if err = os.WriteFile(stageSource, updated, 0644); err != nil {
			return out, err
		}
		sourcePaths[draftRel(snap.Source)] = true
	}
	out.Changes, err = staged.changes(sourcePaths)
	if err != nil {
		return out, err
	}
	for _, snap := range request.Snapshots {
		rec := records[snap.Source]
		current, err := os.ReadFile(draftPath(root, snap.Source))
		if err != nil {
			return out, err
		}
		basis, err := proposalBasis(root, current, rec.state)
		if err != nil {
			return out, err
		}
		if basis != snap.Digest {
			return out, fmt.Errorf("%s: снимок изменился при подготовке", snap.Source)
		}
	}
	sortReceipts(out.Results)
	return out, nil
}

func sortReceipts(receipts []draftproposal.Receipt) {
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].Source != receipts[j].Source {
			return receipts[i].Source < receipts[j].Source
		}
		return receipts[i].Proposal < receipts[j].Proposal
	})
}

type confirmationResult struct {
	Key         string                  `json:"key"`
	Results     []draftproposal.Receipt `json:"results"`
	Paths       []string                `json:"paths,omitempty"`
	Repeated    bool                    `json:"repeated"`
	Publication string                  `json:"publication,omitempty"`
}

func cmdDraftProposalsConfirm(root string, input []byte, opts CommitOpts) (resultText string, finalErr error) {
	if err := opts.validate(); err != nil {
		return "", err
	}
	var request draftproposal.ConfirmRequest
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		return "", fmt.Errorf("неверное подтверждение: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("лишние данные после подтверждения")
	}
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	plan, err := prepareConfirmation(root, request)
	var cloudLocks []*draftmeta.CloudLock
	var targets []string
	if err == nil && !plan.Repeated {
		cloudLocks, targets, err = confirmationCloudLocks(root, lock.ServiceDir(), request)
	}
	if err == nil && len(targets) > 0 {
		lock, err = guardOutsideLock(root, lock, targets)
	}
	if err == nil && !plan.Repeated {
		err = lock.Apply(root, plan.Changes)
	}
	publication := ""
	if err == nil && !plan.Repeated && len(plan.Changes) > 0 && opts.Msg != "" {
		publication, err = lock.BeginPublication()
		if err != nil {
			err = fmt.Errorf("применено, публикация не начата: %w", err)
		}
	}
	for i := len(cloudLocks) - 1; i >= 0; i-- {
		cloudLocks[i].Close()
	}
	var closeErr error
	if lock != nil {
		closeErr = lock.Close()
	}
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", fmt.Errorf("применено, замок не освобождён: %w", closeErr)
	}
	if publication != "" {
		defer func() {
			if err := drafttx.FinishPublication(root, publication); err != nil {
				if finalErr != nil {
					finalErr = fmt.Errorf("%v; журнал публикации не снят: %w", finalErr, err)
				} else {
					finalErr = fmt.Errorf("применено, журнал публикации не снят: %w", err)
				}
			}
		}()
	}
	result := confirmationResult{Key: plan.Key, Results: plan.Results, Repeated: plan.Repeated}
	for _, change := range plan.Changes {
		result.Paths = append(result.Paths, change.Path)
	}
	if !plan.Repeated && len(result.Paths) > 0 {
		result.Publication, err = opts.apply(root, result.Paths)
		if err != nil {
			return "", fmt.Errorf("применено, публикация не завершена: %w", err)
		}
	}
	return marshal(result)
}

func confirmationCloudLocks(root, serviceDir string, request draftproposal.ConfirmRequest) ([]*draftmeta.CloudLock, []string, error) {
	targets := map[string]bool{}
	for _, decision := range request.Decisions {
		if decision.Action != "accept" {
			continue
		}
		data, err := os.ReadFile(draftPath(root, decision.Source))
		if err != nil {
			return nil, nil, err
		}
		header, err := draftmeta.Parse(data)
		if err != nil {
			return nil, nil, err
		}
		if header.Lock != "locked" {
			continue
		}
		state, err := draftproposal.Parse(data)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range state.Proposals {
			if p.ID == decision.Proposal && p.Outcome == "attach" {
				targets[p.Target] = true
			}
		}
	}
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	var locks []*draftmeta.CloudLock
	for _, name := range names {
		l, err := draftmeta.CloudExclusiveIn(serviceDir, name)
		if err != nil {
			if l != nil {
				l.Close()
			}
			for _, held := range locks {
				held.Close()
			}
			return nil, nil, err
		}
		locks = append(locks, l)
	}
	return locks, names, nil
}

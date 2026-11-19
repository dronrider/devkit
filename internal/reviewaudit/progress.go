package reviewaudit

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// NameProgress checks the saved STYLE-001 evidence before the Markdown exists.
func NameProgress(root, path, id, request, summaryPath string) (NameProgressState, error) {
	root, report, snapshot, output, err := snapshotForCoverage(root, path, id, summaryPath, request)
	if err != nil {
		return NameProgressState{}, err
	}
	return nameProgressForSnapshot(root, path, id, output, report, snapshot)
}

func nameProgressForSnapshot(root, path, id, output string, report, snapshot Report) (NameProgressState, error) {
	report, inventoryIssues := canonicalTargetInventory(report, snapshot)
	if _, invalid := validateInventory(report, snapshot, id); invalid != nil {
		return NameProgressState{}, invalid
	}
	index := slices.IndexFunc(report.Rules, func(rule Rule) bool { return rule.ID == namingRule })
	if index == -1 {
		return NameProgressState{}, fmt.Errorf("в снимке нет STYLE-001")
	}
	rule := report.Rules[index]
	original := snapshot.Rules[slices.IndexFunc(snapshot.Rules, func(rule Rule) bool { return rule.ID == namingRule })]
	report.Rules = []Rule{rule}
	progress := describeNameProgress(rule, original.Files, errors.Join(
		errors.Join(checkSplitRule(rule, requiredChecks(namingRule))...),
		checkNames(root, path, output, report), checkMissingFiles(root, report, snapshot),
		validateTargetCoverage(rule, original.Targets)))
	progress.AuditPath, progress.BaseSHA, progress.HeadSHA = path, snapshot.BaseSHA, snapshot.HeadSHA
	progress.InventoryIssues = inventoryIssues
	if len(inventoryIssues) != 0 {
		progress.ReadyForSummary = false
	}
	return progress, nil
}

type NameProgressState struct {
	AuditPath       string                 `json:"audit_path"`
	BaseSHA         string                 `json:"base_sha"`
	HeadSHA         string                 `json:"head_sha"`
	TargetsTotal    int                    `json:"targets_total"`
	Closed          int                    `json:"closed"`
	Missing         int                    `json:"missing"`
	Rejected        int                    `json:"rejected"`
	Pending         int                    `json:"pending"`
	FilesTotal      int                    `json:"files_total"`
	FilesReady      int                    `json:"files_ready"`
	NextFile        *NameProgressFile      `json:"next_file"`
	OpenFiles       []NameProgressFile     `json:"open_files"`
	Unassigned      []string               `json:"unassigned,omitempty"`
	Issues          []string               `json:"issues,omitempty"`
	InventoryIssues []TargetInventoryIssue `json:"inventory_issues,omitempty"`
	ReadyForSummary bool                   `json:"ready_for_summary"`
}

type NameProgressFile struct {
	Path   string             `json:"path"`
	Issues []string           `json:"issues,omitempty"`
	Goals  []NameProgressGoal `json:"goals,omitempty"`
}

type NameProgressGoal struct {
	Object         string                `json:"object"`
	MissingChecks  []string              `json:"missing_checks,omitempty"`
	RejectedChecks []string              `json:"rejected_checks,omitempty"`
	Issues         []string              `json:"issues,omitempty"`
	Blockers       []NameProgressBlocker `json:"blockers,omitempty"`
}

type NameProgressBlocker struct {
	Check   string `json:"check"`
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Next    string `json:"next"`
}

func nameProgressBlocker(check string, err error) NameProgressBlocker {
	blocker := NameProgressBlocker{Check: check, Stage: "evidence", Code: "invalid_evidence",
		Message: oneLine(err.Error()), Next: "Исправьте доказательство по сообщению и повторите audit-progress."}
	var conclusion *nameConclusionIssue
	if errors.As(err, &conclusion) {
		blocker.Stage, blocker.Code = "conclusion", conclusion.code
		blocker.Next = "Сопоставьте полный expected с фактическим объектом целиком. Подтвердите их соответствие кодом либо запишите расхождение как violated и обновите meaning. Если факта не хватает, укажите его в pending. Ошибка проверки сама по себе не доказывает дефект."
	}
	var consumer *nameConsumerIssue
	if errors.As(err, &consumer) {
		blocker.Code = "missing_consumer"
		blocker.Next = fmt.Sprintf("Продолжите поиск чтений поля %q в доступном коде. В proof.assignment.use укажите объявление потребителя, строку чтения и выражение. Сохраните исправление и повторите audit-progress внутри текущего поручения.", consumer.field)
	}
	return blocker
}

func nameProgressBlockers(check string, err error) []NameProgressBlocker {
	switch issue := err.(type) {
	case *nameConclusionIssue, *nameConsumerIssue:
		return []NameProgressBlocker{nameProgressBlocker(check, err)}
	case interface{ Unwrap() []error }:
		var blockers []NameProgressBlocker
		for _, child := range issue.Unwrap() {
			blockers = append(blockers, nameProgressBlockers(check, child)...)
		}
		return blockers
	case interface{ Unwrap() error }:
		if issue.Unwrap() != nil {
			return nameProgressBlockers(check, issue.Unwrap())
		}
	}
	return []NameProgressBlocker{nameProgressBlocker(check, err)}
}

func (p NameProgressState) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "STYLE-001 | закрыто %d/%d | незаполнено %d | отклонено %d | обоснованно открыто %d\n",
		p.Closed, p.TargetsTotal, p.Missing, p.Rejected, p.Pending)
	fmt.Fprintf(&out, "Файлы готовы к итоговой сверке %d/%d\n", p.FilesReady, p.FilesTotal)
	if file := p.NextFile; file != nil {
		fmt.Fprintf(&out, "Следующий файл: %s\n", file.Path)
		for _, issue := range file.Issues {
			fmt.Fprintf(&out, "  %s\n", issue)
		}
		for _, goal := range file.Goals {
			steps := slices.Clone(goal.Issues)
			for _, blocker := range goal.Blockers {
				if blocker.Stage == "conclusion" || blocker.Code == "missing_consumer" {
					steps = append(steps, blocker.Next)
				}
			}
			if len(goal.MissingChecks) > 0 {
				steps = append(steps, "заполните "+strings.Join(goal.MissingChecks, ", "))
			}
			fmt.Fprintf(&out, "  %s -> %s\n", oneLine(goal.Object), strings.Join(steps, " | "))
		}
	} else if len(p.Unassigned) > 0 {
		fmt.Fprintf(&out, "Следующий шаг: укажите файл объявления для %s в files.objects\n", oneLine(p.Unassigned[0]))
	}
	for _, issue := range p.Issues {
		fmt.Fprintln(&out, issue)
	}
	for _, issue := range p.InventoryIssues {
		fmt.Fprintf(&out, "%s: %s; %s\n", issue.Rule, issue.Message, issue.Next)
	}
	if p.ReadyForSummary {
		fmt.Fprintln(&out, "Следующий шаг: соберите итог и запустите audit-check --coverage")
	}
	return strings.TrimSpace(out.String())
}

type nameGoal struct {
	missing        []string
	rejected       []string
	rejectedChecks []string
	pending        bool
	blockers       []NameProgressBlocker
}

func describeNameProgress(rule Rule, files []FileReview, checks error) NameProgressState {
	objects := make(map[string]bool, len(rule.Objects))
	goals := make(map[string]*nameGoal)
	var order []string
	for _, object := range rule.Objects {
		objects[object] = true
	}
	addGoal := func(object string) {
		if goals[object] == nil && strings.TrimSpace(object) != "" {
			goals[object] = &nameGoal{}
			order = append(order, object)
		}
	}
	for _, object := range rule.Objects {
		addGoal(object)
	}
	for _, target := range rule.Targets {
		addGoal(target.Object)
	}
	var common []string
	addIssue := func(err error) {
		issue, ok := err.(*objectIssue)
		if !ok || goals[issue.key.object] == nil {
			common = append(common, oneLine(err.Error()))
			return
		}
		goal := goals[issue.key.object]
		if issue.missing {
			goal.missing = append(goal.missing, issue.key.check)
		} else {
			detail := oneLine(issue.err.Error())
			check := issue.key.check
			if check != "" {
				detail = check + ": " + detail
			} else {
				check = "inventory"
			}
			if !slices.Contains(goal.rejectedChecks, check) {
				goal.rejectedChecks = append(goal.rejectedChecks, check)
			}
			goal.rejected = append(goal.rejected, detail)
			goal.blockers = append(goal.blockers, nameProgressBlockers(check, issue.err)...)
		}
	}
	eachAuditIssue(checks, addIssue)
	for _, e := range rule.Evidence {
		if e.Result == "pending" && goals[e.Object] != nil && len(e.Facts["question"]) == 1 {
			goals[e.Object].pending = true
		}
	}

	byPath := make(map[string]FileReview, len(rule.Files))
	for _, file := range rule.Files {
		if _, duplicate := byPath[file.Path]; duplicate {
			common = append(common, fmt.Sprintf("уберите повторный путь %q из files", file.Path))
		}
		byPath[file.Path] = file
	}
	type fileWork struct {
		path    string
		objects []string
		issues  []string
	}
	work := make([]fileWork, 0, len(files))
	mapped := make(map[string]bool, len(goals))
	for i, want := range files {
		file, found := byPath[want.Path]
		current := fileWork{path: want.Path, objects: slices.Clone(want.Objects)}
		if found {
			delete(byPath, want.Path)
			if i >= len(rule.Files) || rule.Files[i].Path != want.Path {
				current.issues = append(current.issues, "восстановите порядок files из снимка")
			}
			for _, err := range checkFileReview(namingRule, file, want, objects) {
				if _, ok := err.(*objectIssue); ok {
					addIssue(err)
				} else {
					current.issues = append(current.issues, oneLine(err.Error()))
				}
			}
			for _, object := range file.Objects {
				if objects[object] && !slices.Contains(current.objects, object) {
					current.objects = append(current.objects, object)
				}
			}
		} else {
			current.issues = append(current.issues, "восстановите запись в files")
		}
		for _, object := range current.objects {
			mapped[object] = true
		}
		work = append(work, current)
	}
	for _, file := range rule.Files {
		if _, unknown := byPath[file.Path]; unknown {
			common = append(common, fmt.Sprintf("уберите путь %q из files и сверьте порядок со снимком", file.Path))
			delete(byPath, file.Path)
		}
	}

	state := NameProgressState{
		TargetsTotal: len(goals), FilesTotal: len(files), Issues: common,
		OpenFiles: make([]NameProgressFile, 0),
	}
	for _, object := range order {
		goal := goals[object]
		if !mapped[object] {
			goal.rejected = append(goal.rejected, "укажите файл объявления в files.objects")
			state.Unassigned = append(state.Unassigned, object)
		}
		switch {
		case len(goal.rejected) > 0:
			state.Rejected++
		case len(goal.missing) > 0:
			state.Missing++
		case goal.pending:
			state.Pending++
		default:
			state.Closed++
		}
	}
	for _, file := range work {
		open := NameProgressFile{Path: file.path, Issues: file.issues}
		for _, object := range file.objects {
			if goal := goals[object]; goal != nil && (len(goal.rejected) != 0 || len(goal.missing) != 0) {
				open.Goals = append(open.Goals, NameProgressGoal{
					Object: object, MissingChecks: goal.missing, RejectedChecks: goal.rejectedChecks, Issues: goal.rejected,
					Blockers: goal.blockers,
				})
			}
		}
		if len(open.Issues) == 0 && len(open.Goals) == 0 {
			state.FilesReady++
		} else {
			state.OpenFiles = append(state.OpenFiles, open)
		}
	}
	if len(state.OpenFiles) > 0 {
		state.NextFile = &state.OpenFiles[0]
	}
	state.ReadyForSummary = state.FilesReady == state.FilesTotal && state.Missing == 0 &&
		state.Rejected == 0 && len(state.Issues) == 0
	return state
}

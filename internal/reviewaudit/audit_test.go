package reviewaudit

import (
	"encoding/json"
	"github.com/dronrider/devkit/internal/runsguard"
	"github.com/dronrider/devkit/internal/taskhead"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(runsguard.Guard(m)) }

func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(s), 0644); e != nil {
		t.Fatal(e)
	}
}
func gitT(t *testing.T, r string, a ...string) string {
	t.Helper()
	b, e := exec.Command("git", append([]string{"-C", r}, a...)...).CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", a, e, b)
	}
	return strings.TrimSpace(string(b))
}
func fixture(t *testing.T) (root, path string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root = t.TempDir()
	dk := t.TempDir()
	t.Setenv("DEVKIT_HOME", dk)
	write(t, filepath.Join(dk, taskhead.TaskRunRel), "marker")
	write(t, filepath.Join(dk, "RULES.architecture.md"), "# Архитектура\n\n- **ARCH-001.** Проверяется инвариант.\n")
	write(t, filepath.Join(dk, "RULES.style.md"), "# Стиль\n\n- **STYLE-001.** Имя соответствует смыслу.\n")
	write(t, filepath.Join(dk, "RULES.md"), "## Тесты обязательны\n\n- **TEST-001.** Тест различает поведения.\n\n## Другое\nНе тестовое правило\n")
	write(t, filepath.Join(root, "docs/ARCHITECTURE.md"), "# Архитектура\n\nОперации хранят историю изменений.\n\n- Один писатель состояния.\n  Блокировка у владельца.\n")
	write(t, filepath.Join(root, "docs/tasks/XR-1.md"), "# Задача\n\n## DoD\n\nПравильный результат.\n\n## Ход работы\n\nСлужебное.\n")
	write(t, filepath.Join(root, "code.go"), "package sample\n")
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.name", "test")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "base")
	path, _ = Path(root, "XR-1", true)
	return
}
func save(t *testing.T, p string, r Report) {
	t.Helper()
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	write(t, p, string(b))
}
func completed(t *testing.T, root, path string) Report {
	t.Helper()
	if _, e := Create(root, path, "XR-1", "HEAD", false); e != nil {
		t.Fatal(e)
	}
	r, e := readReport(path)
	if e != nil {
		t.Fatal(e)
	}
	r.Architecture = Architecture{Goal: "Правильный результат", Decision: "Один владелец", Boundaries: "Граница хранения", Alternative: "Копия отклонена", Justification: "Владелец сохраняет инвариант", Result: "checked", Evidence: []string{"code.go:1 путь операции"}}
	for i := range r.Rules {
		r.Rules[i].Applicability = "applicable"
		r.Rules[i].Result = "checked"
		r.Rules[i].Reason = "Инвариант относится к операции"
		r.Rules[i].Evidence = []Evidence{{Object: "операция", Location: "code.go:1", Observation: "ветка ошибки сохраняет состояние"}}
	}
	save(t, path, r)
	return r
}
func TestCompleteAndMetadataCommit(t *testing.T) {
	root, path := fixture(t)
	r := completed(t, root, path)
	if len(r.Rules) != 6 || r.Sources[4].Status != "absent" {
		t.Fatalf("inventory: %+v", r)
	}
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "docs/tasks/XR-1.md")
	b, _ := os.ReadFile(p)
	write(t, p, string(b)+"запись о ревью\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "report and log")
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
}
func TestRejectsIncompleteAndForged(t *testing.T) {
	cases := map[string]func(*Report){
		"missing rule": func(r *Report) { r.Rules = r.Rules[1:] }, "duplicate": func(r *Report) { r.Rules[1] = r.Rules[0] }, "invented": func(r *Report) { r.Rules[0].ID = "other" }, "changed text": func(r *Report) { r.Rules[0].Text = "Разрешено всё" }, "forged source": func(r *Report) { r.Sources[0].Path = "/other" }, "missing source": func(r *Report) { r.Sources = r.Sources[1:] }, "pending": func(r *Report) { r.Rules[0].Result = "pending" }, "violated": func(r *Report) { r.Rules[0].Result = "violated" }, "unknown applicability": func(r *Report) { r.Rules[0].Applicability = "pending" }, "NA without reason": func(r *Report) { r.Rules[0].Applicability = "not_applicable"; r.Rules[0].Reason = " " }, "missing objects": func(r *Report) { r.Rules[0].Evidence = nil }, "incomplete object": func(r *Report) { r.Rules[0].Evidence[0].Location = "" }, "architecture pending": func(r *Report) { r.Architecture.Result = "pending" }, "architecture violated": func(r *Report) { r.Architecture.Result = "violated" }, "missing architecture": func(r *Report) { r.Architecture = Architecture{} }, "missing alternative": func(r *Report) { r.Architecture.Alternative = "" }, "wrong ID": func(r *Report) { r.ID = "XR-2" }}
	for n, m := range cases {
		t.Run(n, func(t *testing.T) {
			root, path := fixture(t)
			r := completed(t, root, path)
			m(&r)
			save(t, path, r)
			if e := Validate(root, path, "XR-1"); e == nil {
				t.Fatal("отчёт принят")
			}
		})
	}
}
func TestStaleInputs(t *testing.T) {
	for _, what := range []string{"code", "new code", "rule", "new rule", "task", "new project"} {
		t.Run(what, func(t *testing.T) {
			root, path := fixture(t)
			completed(t, root, path)
			switch what {
			case "code":
				write(t, filepath.Join(root, "code.go"), "package changed\n")
			case "new code":
				write(t, filepath.Join(root, "new.go"), "package sample\n")
			case "rule":
				write(t, filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.style.md"), "- **STYLE-001.** Иное правило.\n")
			case "new rule":
				p := filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.style.md")
				b, _ := os.ReadFile(p)
				write(t, p, string(b)+"- **STYLE-002.** Новое правило.\n")
			case "task":
				write(t, filepath.Join(root, "docs/tasks/XR-1.md"), "## DoD\n\nИная задача.\n")
			case "new project":
				write(t, filepath.Join(root, "docs/STYLEGUIDE.md"), "# Стиль\n\n- Новый стиль.\n")
			}
			if e := Validate(root, path, "XR-1"); e == nil {
				t.Fatal("устаревший отчёт принят")
			}
		})
	}
}
func TestRefreshAndMissingInputs(t *testing.T) {
	root, path := fixture(t)
	if _, e := Create(root, path, "XR-1", "", false); e == nil {
		t.Fatal("угадана база")
	}
	r := completed(t, root, path)
	if _, e := Create(root, path, "XR-1", "HEAD", false); e == nil {
		t.Fatal("перезапись")
	}
	if _, e := Create(root, path, "XR-1", "", true); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(path + ".previous-*")
	if len(files) != 1 {
		t.Fatal(files)
	}
	old, e := readReport(files[0])
	if e != nil || old.Architecture.Goal != r.Architecture.Goal {
		t.Fatal("свидетельства потеряны")
	}
	fresh, e := readReport(path)
	if e != nil || fresh.BaseSHA != r.BaseSHA || fresh.Architecture.Result != "pending" {
		t.Fatal("refresh")
	}
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("сброшенный принят")
	}
	os.Remove(filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.style.md"))
	if _, e := Create(root, path, "XR-1", "HEAD", true); e == nil {
		t.Fatal("без источника")
	}
}
func TestDirtySnapshotSurvivesCommit(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "code.go"), "package improved\n")
	completed(t, root, path)
	if e := Validate(root, path, "XR-1"); e == nil || !strings.Contains(e.Error(), "не закоммичен") {
		t.Fatalf("dirty: %v", e)
	}
	gitT(t, root, "add", "code.go")
	gitT(t, root, "commit", "-qm", "code")
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
}
func TestNAAndPaths(t *testing.T) {
	root, path := fixture(t)
	r := completed(t, root, path)
	r.Rules[0].Applicability = "not_applicable"
	r.Rules[0].Reason = "Нет операции записи"
	r.Rules[0].Evidence = nil
	save(t, path, r)
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
	p, e := Path(root, "../unsafe/label", false)
	if e != nil || filepath.Dir(p) != filepath.Join(root, ".devkit/review-audits") {
		t.Fatal(p, e)
	}
	if _, e = Path(root, "../unsafe", true); e == nil {
		t.Fatal("выход из каталога")
	}
}
func TestParserKeepsUntaggedProjectRules(t *testing.T) {
	if _, e := parseRules("common", "- Правило без ID", true); e == nil {
		t.Fatal("пропущено")
	}
	rr, e := parseRules("project", "# Слой\n\nПравило прозой.\n\n- Правило списком.\n  Продолжение.\n", false)
	if e != nil || len(rr) != 3 {
		t.Fatal(rr, e)
	}
}

func TestTaskFenceDoesNotHideChangedRequirement(t *testing.T) {
	root, path := fixture(t)
	task := taskPath(path)
	write(t, task, "# Задача\n\n## DoD\n```markdown\n## Ревью\nrequired behavior\n```\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "requirements")
	completed(t, root, path)
	write(t, task, "# Задача\n\n## DoD\n```markdown\n## Ревью\nchanged behavior\n```\n")
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("fenced heading hid requirement change")
	}
}
func TestExtraSourcesAndInvalidManifest(t *testing.T) {
	for _, val := range []string{"../outside.md", "/tmp/rules.md", "docs/../other.md", "docs/ARCHITECTURE.md", "docs/extra.md, docs/extra.md", "docs/missing.md"} {
		t.Run(val, func(t *testing.T) {
			root, path := fixture(t)
			write(t, filepath.Join(root, "docs/extra.md"), "Keep operation atomic.\n")
			write(t, filepath.Join(root, ".devkit/review.conf"), "rule_sources = "+val+"\n")
			if _, e := Create(root, path, "XR-1", "HEAD", false); e == nil {
				t.Fatalf("manifest %q accepted", val)
			}
		})
	}
	root, path := fixture(t)
	write(t, filepath.Join(root, "docs/extra.md"), "Keep operation atomic.\n\n- No split write.\n")
	write(t, filepath.Join(root, ".devkit/review.conf"), "rule_sources = docs/extra.md\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "extra rules")
	r := completed(t, root, path)
	if len(r.Rules) != 8 {
		t.Fatalf("extra rules omitted: %d", len(r.Rules))
	}
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(root, "docs/extra.md"), "Changed invariant.\n")
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("extra source stale accepted")
	}
}
func TestSourcePathsSurviveCheckoutRelocation(t *testing.T) {
	root, path := fixture(t)
	completed(t, root, path)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "report")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, root, "clone", "-q", root, clone)
	newPath, _ := Path(clone, "XR-1", true)
	if e := Validate(clone, newPath, "XR-1"); e != nil {
		t.Fatal(e)
	}
}
func TestBaseMustRemainResolvedSHA(t *testing.T) {
	root, path := fixture(t)
	r := completed(t, root, path)
	r.BaseSHA = "HEAD"
	save(t, path, r)
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("mutable base accepted")
	}
}

func TestCommonRuleFenceCannotTruncateInventory(t *testing.T) {
	root, path := fixture(t)
	dk := os.Getenv("DEVKIT_HOME")
	write(t, filepath.Join(dk, "RULES.md"), "## Тесты обязательны\n\n- **TEST-001.** First.\n\n````markdown\n## Fake heading\n```\n~~~\n- **TEST-999.** Fake.\n````\n\n- **TEST-002.** Second.\n\n## Other\n")
	if _, e := Create(root, path, "XR-1", "HEAD", false); e != nil {
		t.Fatal(e)
	}
	r, e := readReport(path)
	if e != nil {
		t.Fatal(e)
	}
	var ids []string
	for _, rule := range r.Rules {
		if rule.Source == "common-tests" {
			ids = append(ids, rule.ID)
		}
	}
	if strings.Join(ids, ",") != "common-tests:TEST-001,common-tests:TEST-002" {
		t.Fatalf("inventory: %v", ids)
	}
	write(t, filepath.Join(dk, "RULES.style.md"), "- **STYLE-001.** First.\n\n```\n- **STYLE-002.** Hidden.\n")
	if _, e := Create(root, path, "XR-1", "", true); e == nil {
		t.Fatal("unclosed source fence accepted")
	}
}

func TestProjectHeadingsAndRepeatedBlocksAreInventoried(t *testing.T) {
	rules, e := parseRules("project-style", "# Rules\n\n## Never log tokens\n\n- Keep atomic.\n\n## Other scope\n\n- Keep atomic.\n", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(rules) != 5 {
		t.Fatalf("blocks skipped: %+v", rules)
	}
	if rules[2].ID == rules[4].ID {
		t.Fatal("repeated block lost identity")
	}
}
func TestOtherReviewArtifactsDoNotStaleReport(t *testing.T) {
	root, path := fixture(t)
	completed(t, root, path)
	write(t, filepath.Join(root, "docs/tasks/XR-2.review-audit.json"), "{}")
	write(t, filepath.Join(root, "docs/tasks/XR-2.review-audit.json.previous-123"), "{}")
	write(t, filepath.Join(root, ".devkit/review-audits/second.json"), "{}")
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
}
func TestUnchangedSubmoduleDoesNotBlockReview(t *testing.T) {
	root, path := fixture(t)
	sub := t.TempDir()
	write(t, filepath.Join(sub, "source.txt"), "dependency\n")
	gitT(t, sub, "init", "-q")
	gitT(t, sub, "config", "user.name", "test")
	gitT(t, sub, "config", "user.email", "test@test")
	gitT(t, sub, "add", ".")
	gitT(t, sub, "commit", "-qm", "dependency")
	gitT(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vendor")
	gitT(t, root, "commit", "-qam", "add dependency")
	completed(t, root, path)
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(root, "vendor/source.txt"), "dirty dependency\n")
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("dirty submodule accepted")
	}
}

func TestSparseTrackedChangeInvalidatesReport(t *testing.T) {
	root, path := fixture(t)
	write(t, filepath.Join(root, "hidden.txt"), "first\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "hidden file")
	gitT(t, root, "update-index", "--skip-worktree", "hidden.txt")
	os.Remove(filepath.Join(root, "hidden.txt"))
	completed(t, root, path)
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
	other := filepath.Join(t.TempDir(), "new")
	write(t, other, "changed\n")
	sha := gitT(t, root, "hash-object", "-w", other)
	gitT(t, root, "update-index", "--cacheinfo", "100644,"+sha+",hidden.txt")
	gitT(t, root, "commit", "-qm", "change hidden code")
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("sparse code change accepted")
	}
}

func TestValidateAtUsesCommittedContentAndRules(t *testing.T) {
	root, path := fixture(t)
	completed(t, root, path)
	sha := gitT(t, root, "rev-parse", "HEAD")
	write(t, filepath.Join(root, "code.go"), "local unfinished work\n")
	write(t, filepath.Join(root, "docs/ARCHITECTURE.md"), "local unfinished standard\n")
	write(t, filepath.Join(root, "untracked.txt"), "local scratch\n")
	if e := Validate(root, path, "XR-1"); e == nil {
		t.Fatal("live dirty snapshot accepted")
	}
	if e := ValidateAt(root, path, "XR-1", sha); e != nil {
		t.Fatalf("committed content rejected: %v", e)
	}
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "changed content")
	if e := ValidateAt(root, path, "XR-1", "HEAD"); e == nil {
		t.Fatal("different committed content accepted")
	}
	if e := ValidateAt(root, path, "XR-1", sha); e != nil {
		t.Fatalf("explicit old commit ignored: %v", e)
	}
	write(t, filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.style.md"), "- **STYLE-001.** New common standard.\n")
	if e := ValidateAt(root, path, "XR-1", sha); e == nil {
		t.Fatal("stale common standards accepted")
	}
}
func TestValidateAtUsesCommittedTask(t *testing.T) {
	root, path := fixture(t)
	completed(t, root, path)
	write(t, taskPath(path), "# Edited requirement\n")
	if e := ValidateAt(root, path, "XR-1", "HEAD"); e != nil {
		t.Fatal(e)
	}
	gitT(t, root, "add", taskPath(path))
	gitT(t, root, "commit", "-qm", "new requirement")
	if e := ValidateAt(root, path, "XR-1", "HEAD"); e == nil {
		t.Fatal("changed committed task accepted")
	}
}

func TestSiblingTaskMetadataDoesNotInvalidateReviewedCode(t *testing.T) {
	root, path := fixture(t)
	completed(t, root, path)
	write(t, filepath.Join(root, "docs/tasks/XR-2.md"), "# Other task\n\n## Ход работы\nIndependent update.\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "other task log")
	if e := Validate(root, path, "XR-1"); e != nil {
		t.Fatal(e)
	}
	if e := ValidateAt(root, path, "XR-1", "HEAD"); e != nil {
		t.Fatal(e)
	}
	write(t, taskPath(path), "# Задача\n\n## DoD\nChanged requirement.\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "change requirement")
	if e := ValidateAt(root, path, "XR-1", "HEAD"); e == nil {
		t.Fatal("own requirement change accepted")
	}
}

func TestValidateAtRejectsChangedCommittedCodeOnly(t *testing.T) {
	root, path := fixture(t)
	completed(t, root, path)
	write(t, filepath.Join(root, "code.go"), "package changed\n")
	gitT(t, root, "add", "code.go")
	gitT(t, root, "commit", "-qm", "change only code")
	if e := ValidateAt(root, path, "XR-1", "HEAD"); e == nil {
		t.Fatal("changed committed code accepted")
	}
}

func TestProjectListDoesNotSwallowIndependentParagraph(t *testing.T) {
	rules, e := parseRules("project-style", "- First rule.\n\n  Indented explanation.\n\nIndependent requirement.\n\n1. Numbered rule.\n2. Another rule.\n+ Another bullet.\n", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(rules) != 5 {
		t.Fatalf("independent requirements merged: %+v", rules)
	}
	if !strings.Contains(rules[0].Text, "Indented explanation") || strings.Contains(rules[0].Text, "Independent requirement") {
		t.Fatalf("wrong list boundary: %+v", rules)
	}
}

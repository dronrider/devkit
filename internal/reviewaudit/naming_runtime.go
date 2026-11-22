package reviewaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	nameRuntimeNamespace = "devkit-style-001"
	nameRuntimeSigners   = "DEVKIT_STYLE_RUN_ALLOWED_SIGNERS"
)

// NameRuntimeProof carries a signed record from an independent runner.
// The signature covers Payload as received, including whitespace.
type NameRuntimeProof struct {
	Runner    string `json:"runner"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type nameRuntimeRecord struct {
	Version     int                    `json:"version"`
	Task        string                 `json:"task"`
	Commit      string                 `json:"commit"`
	Object      string                 `json:"object"`
	Declaration string                 `json:"declaration"`
	Actual      string                 `json:"actual"`
	Command     nameRuntimeCommand     `json:"command"`
	Write       nameRuntimeWrite       `json:"write"`
	Read        nameRuntimeObservation `json:"read"`
	Negative    nameRuntimeControl     `json:"negative"`
}

type nameRuntimeSite struct {
	At         string `json:"at"`
	Quote      string `json:"quote"`
	Expression string `json:"expression"`
	Identity   string `json:"identity"`
}

type nameRuntimeObservation struct {
	nameRuntimeSite
	Value *string `json:"value"`
}

type nameRuntimeWrite struct {
	nameRuntimeObservation
	SourceValue string `json:"source_value"`
}

type nameRuntimeControl struct {
	Command      nameRuntimeCommand     `json:"command"`
	TargetWrites *int                   `json:"target_writes"`
	Read         nameRuntimeObservation `json:"read"`
}

func checkNameRuntimeWrite(e Evidence, declaration string, source *evidenceSource) error {
	if e.Check != "writes" || e.Result != "checked" || e.WriteEvidence == nil {
		return fmt.Errorf("proof.runtime: требуется writes с результатом checked и write_evidence")
	}
	pair, write := e.ObjectComparison, e.WriteEvidence
	proof := pair.Proof.Runtime
	if err := checkNameRuntimeSignature(proof, source.root); err != nil {
		return err
	}
	run, err := readNameRuntimeRecord(proof.Payload)
	if err != nil {
		return fmt.Errorf("proof.runtime.payload: %w", err)
	}
	if source.taskID == "" || run.Task != source.taskID || run.Object != e.Object || run.Declaration != declaration || run.Actual != pair.Actual {
		return fmt.Errorf("proof.runtime.payload: задача, цель, объявление и actual должны совпадать с проверкой")
	}
	if !validNameRuntimeCommand(run.Command) || !validNameRuntimeCommand(run.Negative.Command) ||
		slices.EqualFunc(run.Command, run.Negative.Command, func(a, b *string) bool { return *a == *b }) {
		return fmt.Errorf("proof.runtime.payload: укажите команды прогона и отдельного контроля")
	}
	if run.Write.At != write.WriteAt || run.Write.Expression != write.Target || run.Write.Expression != pair.Proof.Expression || run.Write.SourceValue != write.Value {
		return fmt.Errorf("proof.runtime.payload.write: точка записи, выражение и значение должны совпадать с write_evidence и proof.expression")
	}
	if err := checkFactQuote(run.Write.SourceValue, run.Write.Quote); err != nil {
		return fmt.Errorf("proof.runtime.payload.write.source_value: %w", err)
	}
	if strings.TrimSpace(run.Write.Identity) == "" || run.Read.Identity != run.Write.Identity || run.Write.Value == nil || run.Read.Value == nil || *run.Read.Value != *run.Write.Value {
		return fmt.Errorf("proof.runtime.payload: запись и последующее чтение должны наблюдать один объект и одно значение")
	}
	control := run.Negative
	if control.TargetWrites == nil || *control.TargetWrites != 0 || control.Read.nameRuntimeSite != run.Read.nameRuntimeSite || control.Read.Value == nil || *control.Read.Value == *run.Read.Value {
		return fmt.Errorf("proof.runtime.payload.negative: контроль должен прочитать другое значение того же объекта в той же точке при нуле записей")
	}
	if err := checkNameRuntimeTree(source.root, source.reportPath, run.Commit); err != nil {
		return fmt.Errorf("proof.runtime.payload.commit: %w", err)
	}
	lines := nameRuntimeSource{source: source, revision: run.Commit}
	if _, err := lines.line(declaration); err != nil {
		return fmt.Errorf("proof.runtime.payload.declaration: %w", err)
	}
	for _, site := range []struct {
		name string
		nameRuntimeSite
	}{{"write", run.Write.nameRuntimeSite}, {"read", run.Read.nameRuntimeSite}} {
		line, err := lines.line(site.At)
		if err != nil {
			return fmt.Errorf("proof.runtime.payload.%s.at: %w", site.name, err)
		}
		if err := checkFactQuote(site.Quote, line); err != nil {
			return fmt.Errorf("proof.runtime.payload.%s: %w", site.name, err)
		}
		if err := checkFactQuote(site.Expression, site.Quote); err != nil {
			return fmt.Errorf("proof.runtime.payload.%s.expression: %w", site.name, err)
		}
	}
	return checkNameRuntimeWords(e, run, source)
}

func checkNameRuntimeWords(e Evidence, run nameRuntimeRecord, source *evidenceSource) error {
	pair := e.ObjectComparison
	if pair.Actual == "" || !((pair.ActualAt == run.Write.At && containsIdentifier(run.Write.Expression, pair.Actual)) ||
		(pair.ActualAt == run.Read.At && containsIdentifier(run.Read.Expression, pair.Actual))) {
		return fmt.Errorf("proof.runtime: actual и actual_at должны ссылаться на идентификатор в подписанной записи или чтении")
	}
	if err := checkNameProofWords(pair, source); err != nil {
		return err
	}
	actual := identifierWords(pair.Actual)
	var objectWord bool
	for _, word := range pair.Proof.Words {
		fact := word.Fact
		if !(fact.Location == run.Write.At && strings.Contains(run.Write.Quote, fact.Quote) ||
			fact.Location == run.Read.At && strings.Contains(run.Read.Quote, fact.Quote)) {
			return fmt.Errorf("proof.words %s: факт должен цитировать подписанную запись или чтение", word.Word)
		}
		if word.Role != "object" {
			continue
		}
		matchesActual := slices.ContainsFunc(actual, func(token string) bool {
			return sameObjectName([]string{word.Word}, []string{token})
		})
		if !matchesActual || !containsIdentifier(fact.Value, pair.Actual) {
			return nameConclusionError("write_object", "proof.words %s: объект записи должен быть виден в подписанном идентификаторе %q", word.Word, pair.Actual)
		}
		objectWord = true
	}
	if !objectWord && namesResultOrWrite("writes", nameBeforeSource(identifierWords(nameObjectSymbol(e.Object)))) {
		return fmt.Errorf("proof.words: укажите слово с ролью object, подтверждённое подписанным идентификатором")
	}
	return nil
}

type nameRuntimeSource struct {
	source   *evidenceSource
	revision string
}

func (s *nameRuntimeSource) line(ref string) (string, error) {
	path, line, base, err := evidenceLocation(ref)
	if err != nil {
		return "", err
	}
	if base {
		return "", fmt.Errorf("укажите строку проверяемой ревизии без base@")
	}
	entry, err := gitRaw(s.source.root, "--no-replace-objects", "ls-tree", "-z", "--full-tree", s.revision, "--", ":(literal)"+path)
	if err != nil {
		return "", err
	}
	mode, name, _ := strings.Cut(entry, "\t")
	if name != path+"\x00" || !(strings.HasPrefix(mode, "100644 blob ") || strings.HasPrefix(mode, "100755 blob ")) {
		return "", fmt.Errorf("%s: ссылка должна вести в обычный файл коммита прогона", path)
	}
	body, err := gitRaw(s.source.root, "--no-replace-objects", "cat-file", "blob", s.revision+":"+path)
	if err != nil {
		return "", err
	}
	working, err := s.source.read(path, false)
	if err != nil {
		return "", err
	}
	if working != body {
		return "", fmt.Errorf("%s: содержимое рабочего файла отличается от коммита прогона", path)
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if body == "" || line > len(lines) {
		return "", fmt.Errorf("строка %d отсутствует в %s", line, path)
	}
	return lines[line-1], nil
}

func readNameRuntimeRecord(payload string) (nameRuntimeRecord, error) {
	var run nameRuntimeRecord
	if !utf8.ValidString(payload) {
		return run, fmt.Errorf("ожидается JSON в UTF-8")
	}
	d := json.NewDecoder(strings.NewReader(payload))
	d.DisallowUnknownFields()
	if err := d.Decode(&run); err != nil {
		return run, err
	}
	if _, err := d.Token(); err != io.EOF {
		return run, fmt.Errorf("ожидается один объект JSON")
	}
	if run.Version != 1 {
		return run, fmt.Errorf("ожидается version 1")
	}
	return run, nil
}

type nameRuntimeCommand []*string

func validNameRuntimeCommand(argv nameRuntimeCommand) bool {
	return len(argv) > 0 && argv[0] != nil && strings.TrimSpace(*argv[0]) != "" && !slices.ContainsFunc(argv, func(arg *string) bool {
		return arg == nil || strings.ContainsRune(*arg, '\x00')
	})
}

func nameRuntimeAllowedSigners(root string) (string, error) {
	allowed := os.Getenv(nameRuntimeSigners)
	if allowed == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("proof.runtime: задайте %s абсолютным путём к списку доверенных проверяющих", nameRuntimeSigners)
		}
		allowed = filepath.Join(home, ".devkit", "style-runtime.allowed_signers")
	}
	if !filepath.IsAbs(allowed) {
		return "", fmt.Errorf("proof.runtime: путь к списку доверенных проверяющих должен быть абсолютным (%s)", nameRuntimeSigners)
	}
	allowed = filepath.Clean(allowed)
	resolved, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		return "", fmt.Errorf("proof.runtime: нужен обычный файл со списком доверенных проверяющих %q (%s)", allowed, nameRuntimeSigners)
	}
	repo, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	for _, path := range []string{allowed, resolved} {
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			info, err := os.Stat(dir)
			if err != nil {
				return "", err
			}
			if os.SameFile(repo, info) {
				return "", fmt.Errorf("proof.runtime: список доверенных проверяющих должен находиться вне репозитория (%s)", nameRuntimeSigners)
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("proof.runtime: нужен обычный файл со списком доверенных проверяющих %q (%s)", allowed, nameRuntimeSigners)
	}
	return resolved, nil
}

func checkNameRuntimeSignature(proof *NameRuntimeProof, root string) error {
	if proof.Runner == "" || strings.ContainsAny(proof.Runner, " \t\r\n\x00") || proof.Payload == "" || proof.Signature == "" {
		return fmt.Errorf("proof.runtime: заполните runner, payload и signature")
	}
	allowed, err := nameRuntimeAllowedSigners(root)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "devkit-style-runtime-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	sig := filepath.Join(tmp, "payload.sig")
	if err := os.WriteFile(sig, []byte(proof.Signature), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-Y", "verify", "-f", allowed, "-I", proof.Runner, "-n", nameRuntimeNamespace, "-s", sig)
	cmd.Dir = tmp
	cmd.Stdin = strings.NewReader(proof.Payload)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("proof.runtime.signature: превышено время проверки подписи")
		}
		if cmd.Path == "ssh-keygen" {
			return fmt.Errorf("proof.runtime.signature: требуется ssh-keygen с поддержкой -Y verify: %w", err)
		}
		return fmt.Errorf("proof.runtime.signature: подпись не подтверждена для проверяющего %q", proof.Runner)
	}
	return nil
}

var fullNameRuntimeCommit = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func checkNameRuntimeTree(root, reportPath, revision string) error {
	if !fullNameRuntimeCommit.MatchString(revision) {
		return fmt.Errorf("укажите полный хеш коммита прогона")
	}
	resolved, err := gitRaw(root, "--no-replace-objects", "rev-parse", "--verify", revision+"^{commit}")
	if err != nil || strings.TrimSpace(resolved) != revision {
		return fmt.Errorf("коммит прогона %s отсутствует", revision)
	}
	head, err := gitRaw(root, "--no-replace-objects", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if _, err := gitRaw(root, "--no-replace-objects", "merge-base", "--is-ancestor", revision, head); err != nil {
		return fmt.Errorf("коммит прогона %s должен быть предком HEAD", revision)
	}
	// Use the literal audit pathname, without resolving a symlink to another file.
	report, err := filepath.Rel(root, reportPath)
	if err != nil || report == "." || !filepath.IsLocal(report) {
		report = ""
	}
	report = filepath.ToSlash(report)
	changed, err := gitRaw(root, "--no-replace-objects", "diff-tree", "--no-commit-id", "--no-renames", "--ignore-submodules=none", "--name-only", "-r", "-z", revision, head, "--")
	if err != nil {
		return err
	}
	for _, path := range strings.Split(changed, "\x00") {
		if path != "" && path != report {
			return fmt.Errorf("дерево коммита прогона отличается от HEAD по пути %q", path)
		}
	}
	status, err := gitRaw(root, "--no-replace-objects", "-c", "core.fsmonitor=false", "status", "--porcelain=v1", "-z", "--untracked-files=no", "--ignore-submodules=none", "--no-renames", "--")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(status, "\x00") {
		if len(entry) > 3 && entry[3:] != report {
			return fmt.Errorf("дерево проверки содержит изменение вне файла аудита по пути %q", entry[3:])
		}
	}
	return nil
}

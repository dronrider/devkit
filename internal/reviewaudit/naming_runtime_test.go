package reviewaudit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const nameRuntimeDeclaration = "code.go:3"

func TestReadNameRuntimeRecord(t *testing.T) {
	for _, tc := range []struct{ payload, want string }{
		{`{"version":1,"command":["program",""]}`, ""},
		{`{"version":2}`, "version 1"},
		{`{"version":1,"read":{"unexpected":"value"}}`, "unknown field"},
		{`{"version":1,"command":["program",1]}`, "cannot unmarshal number"},
		{`{"version":1} {}`, "один объект JSON"},
		{"\xff", "UTF-8"},
	} {
		t.Run(tc.payload, func(t *testing.T) {
			got, err := readNameRuntimeRecord(tc.payload)
			assertTargetCheck(t, err, tc.want)
			if tc.want == "" && (!validNameRuntimeCommand(got.Command) || len(got.Command) != 2 || got.Command[1] == nil || *got.Command[1] != "") {
				t.Fatalf("значимый пустой аргумент потерян: %#v", got.Command)
			}
		})
	}
	program, blank, nul := "program", " ", "\x00"
	for _, argv := range []nameRuntimeCommand{nil, {}, {nil}, {&blank}, {&program, nil}, {&program, &nul}} {
		if validNameRuntimeCommand(argv) {
			t.Errorf("принята некорректная команда %#v", argv)
		}
	}
}

// Changing either source expression, identity or outcome after the independent
// run must break the checked writes proof, even with a trusted signature.
func TestNameRuntimeWriteIntegration(t *testing.T) {
	source, key, original, evidence := nameRuntimeFixture(t)
	stamp := "2026-11-22T13:15:00Z"
	for _, tc := range []struct {
		name      string
		changeRun func(*nameRuntimeRecord)
		change    func(*Evidence)
		want      string
	}{
		{name: "валидная межпроцессная запись"},
		{name: "сокращённый хеш", changeRun: func(r *nameRuntimeRecord) { r.Commit = r.Commit[:12] }, want: "полный хеш"},
		{name: "другая задача", changeRun: func(r *nameRuntimeRecord) { r.Task = "XR-2" }, want: "задача, цель"},
		{name: "другая цель", change: func(e *Evidence) { e.Object = "code.go#SetDeadline" }, want: "задача, цель"},
		{name: "чужое объявление", changeRun: func(r *nameRuntimeRecord) { r.Declaration = "code.go:1" }, want: "задача, цель"},
		{name: "другое actual", change: func(e *Evidence) { e.ObjectComparison.Actual = "row" }, want: "задача, цель"},
		{name: "объект в неподписанной точке", change: func(e *Evidence) { e.ObjectComparison.ActualAt = "code.go:2" }, want: "actual и actual_at"},
		{name: "проверка результата", change: func(e *Evidence) { e.Check = "result" }, want: "требуется writes"},
		{name: "нарушение", change: func(e *Evidence) { e.Result = "violated" }, want: "результатом checked"},
		{name: "нет записи", changeRun: func(r *nameRuntimeRecord) { r.Write = nameRuntimeWrite{} }, want: "payload.write"},
		{name: "нет потребителя", changeRun: func(r *nameRuntimeRecord) { r.Read = nameRuntimeObservation{} }, want: "запись и последующее чтение"},
		{name: "другая команда контроля обязательна", changeRun: func(r *nameRuntimeRecord) {
			program, arg := *r.Command[0], *r.Command[1]
			r.Negative.Command = nameRuntimeCommand{&program, &arg}
		}, want: "отдельного контроля"},
		{name: "null среди аргументов", changeRun: func(r *nameRuntimeRecord) {
			r.Command = nameRuntimeCommand{r.Command[0], nil}
		}, want: "команды прогона и отдельного контроля"},
		{name: "другой наблюдаемый объект", changeRun: func(r *nameRuntimeRecord) { r.Read.Identity = "activity:other-row" }, want: "один объект и одно значение"},
		{name: "подменена правая часть", change: func(e *Evidence) { e.WriteEvidence.Value = "previous" }, want: "write_evidence и proof.expression"},
		{name: "подменено выражение", change: func(e *Evidence) { e.ObjectComparison.Proof.Expression = "row" }, want: "write_evidence и proof.expression"},
		{name: "подменено значение чтения", changeRun: func(r *nameRuntimeRecord) { r.Read.Value = &stamp }, want: "одно значение"},
		{name: "ссылка на базу", changeRun: func(r *nameRuntimeRecord) {
			r.Read.At = "base@" + r.Read.At
			r.Negative.Read.At = r.Read.At
		}, want: "без base@"},
		{name: "неверная цитата", changeRun: func(r *nameRuntimeRecord) {
			r.Read.Quote = "return unknown.activity"
			r.Negative.Read.Quote = r.Read.Quote
		}, want: "quote должна точно цитировать"},
		{name: "чтение без адреса", changeRun: func(r *nameRuntimeRecord) {
			r.Read.At = "activity.py"
			r.Negative.Read.At = r.Read.At
		}, want: "файл:строка"},
		{name: "выражение чтения отсутствует", changeRun: func(r *nameRuntimeRecord) {
			r.Read.Expression = "row.other"
			r.Negative.Read.Expression = r.Read.Expression
		}, want: "payload.read.expression"},
		{name: "контроль не отличается", changeRun: func(r *nameRuntimeRecord) { r.Negative.Read.Value = r.Read.Value }, want: "контроль должен прочитать другое"},
		{name: "контроль не считал записи", changeRun: func(r *nameRuntimeRecord) { r.Negative.TargetWrites = nil }, want: "нуле записей"},
		{name: "контроль писал", changeRun: func(r *nameRuntimeRecord) { n := 1; r.Negative.TargetWrites = &n }, want: "нуле записей"},
		{name: "контроль читал отдельно", changeRun: func(r *nameRuntimeRecord) { r.Negative.Read.At = "activity.py:1" }, want: "в той же точке"},
		{name: "доказательство слова потеряно", change: func(e *Evidence) { e.ObjectComparison.Proof.Words = nil }, want: "каждое слово expected"},
		{name: "факт взят из объявления", change: func(e *Evidence) {
			fact := &e.ObjectComparison.Proof.Words[0].Fact
			fact.Location, fact.Quote, fact.Value = nameRuntimeDeclaration, "func SetActivity", "SetActivity"
		}, want: "должен цитировать подписанную"},
		{name: "объект слова взят из значения", change: func(e *Evidence) { e.ObjectComparison.Proof.Words[0].Fact.Value = "stamp" }, want: "объект записи"},
		{name: "нет слова об объекте", change: func(e *Evidence) { e.ObjectComparison.Proof.Words[0].Role = "context" }, want: "с ролью object"},
		{name: "имя действия допускает контекст", changeRun: func(r *nameRuntimeRecord) {
			r.Object = "code.go#record"
		}, change: func(e *Evidence) {
			e.Object = "code.go#record"
			e.ObjectComparison.Expected = []string{"record"}
			word := &e.ObjectComparison.Proof.Words[0]
			word.Word, word.Role = "record", "context"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := original
			if tc.changeRun != nil {
				tc.changeRun(&run)
			}
			e := evidence()
			e.ObjectComparison.Proof.Runtime = signNameRuntimeRecord(t, key, run)
			if tc.change != nil {
				tc.change(&e)
			}
			assertTargetCheck(t, checkNameRuntimeWrite(e, nameRuntimeDeclaration, source), tc.want)
		})
	}
}

// A trusted key is the only authority for payload bytes. A report-only commit
// and committed review metadata may change after the run.
func TestNameRuntimeSignatureAndTreeIntegration(t *testing.T) {
	source, key, run, makeEvidence := nameRuntimeFixture(t)
	proof := signNameRuntimeRecord(t, key, run)
	e := makeEvidence()
	e.ObjectComparison.Proof.Runtime = proof
	check := func(want string) {
		t.Helper()
		assertTargetCheck(t, checkNameRuntimeWrite(e, nameRuntimeDeclaration, source), want)
	}
	check("")
	t.Run("изменён байт подписанного JSON", func(t *testing.T) {
		original := proof.Payload
		proof.Payload = strings.Replace(proof.Payload, `"2026-11-22T13:14:00Z"`, `"2026-11-22T13:15:00Z"`, 1)
		t.Cleanup(func() { proof.Payload = original })
		check("подпись не подтверждена")
	})
	t.Run("неизвестный проверяющий", func(t *testing.T) {
		original := proof.Runner
		proof.Runner = "unknown"
		t.Cleanup(func() { proof.Runner = original })
		check("подпись не подтверждена")
	})
	t.Run("чужой ключ под именем проверяющего", func(t *testing.T) {
		forged := signNameRuntimeRecord(t, nameRuntimeTestKey(t), run)
		e.ObjectComparison.Proof.Runtime = forged
		t.Cleanup(func() { e.ObjectComparison.Proof.Runtime = proof })
		check("подпись не подтверждена")
	})
	t.Run("относительный путь списка ключей", func(t *testing.T) {
		t.Setenv(nameRuntimeSigners, "style-runtime.allowed_signers")
		check("должен быть абсолютным")
	})
	t.Run("список ключей с произвольным абсолютным путём", func(t *testing.T) {
		allowed := filepath.Join(t.TempDir(), "allowed")
		writeNameRuntimeAllowed(t, allowed, key)
		t.Setenv(nameRuntimeSigners, allowed)
		check("")
	})
	t.Run("список ключей из репозитория", func(t *testing.T) {
		allowed := filepath.Join(source.root, "allowed")
		writeNameRuntimeAllowed(t, allowed, key)
		t.Cleanup(func() { os.Remove(allowed) })
		t.Setenv(nameRuntimeSigners, allowed)
		check("должен находиться вне репозитория")
		linked := filepath.Join(t.TempDir(), "allowed-link")
		if err := os.Symlink(allowed, linked); err != nil {
			t.Fatal(err)
		}
		t.Setenv(nameRuntimeSigners, linked)
		check("должен находиться вне репозитория")
	})
	t.Run("список ключей через путь со сменой регистра", func(t *testing.T) {
		alias := strings.ToUpper(source.root)
		original, err := os.Stat(source.root)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(alias)
		if alias == source.root || err != nil || !os.SameFile(original, info) {
			t.Skip("файловая система различает регистр пути")
		}
		allowed := filepath.Join(source.root, "allowed")
		writeNameRuntimeAllowed(t, allowed, key)
		t.Cleanup(func() { os.Remove(allowed) })
		t.Setenv(nameRuntimeSigners, filepath.Join(alias, "allowed"))
		check("должен находиться вне репозитория")
	})
	write(t, source.reportPath, "{}\n")
	gitT(t, source.root, "add", "--", source.reportPath)
	gitT(t, source.root, "commit", "-qm", "attach audit")
	check("")
	write(t, source.reportPath, "{\"updated\":true}\n")
	check("")
	gitT(t, source.root, "restore", "--", source.reportPath)
	t.Run("вердикт в карточке сохраняет подпись", func(t *testing.T) {
		head := gitT(t, source.root, "rev-parse", "HEAD")
		t.Cleanup(func() { gitT(t, source.root, "checkout", "--detach", head) })
		path := taskPath(source.reportPath)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		withVerdict := string(body) + "\n## Ревью\n\n- Вердикт: без замечаний.\n"
		write(t, path, withVerdict)
		assertTargetCheck(t, checkNameRuntimeWrite(e, nameRuntimeDeclaration, source), "изменение вне файла аудита")
		gitT(t, source.root, "add", "--", path)
		gitT(t, source.root, "commit", "-qm", "record verdict")
		assertTargetCheck(t, checkNameRuntimeWrite(e, nameRuntimeDeclaration, source), "")

		write(t, path, strings.Replace(withVerdict, "Правильный результат.", "Другой результат.", 1))
		gitT(t, source.root, "add", "--", path)
		gitT(t, source.root, "commit", "-qm", "change task contract")
		assertTargetCheck(t, checkNameRuntimeWrite(e, nameRuntimeDeclaration, source), "постановка")
	})
	t.Run("изменён файл в рабочем дереве", func(t *testing.T) {
		write(t, filepath.Join(source.root, "activity.py"), "return other\n")
		t.Cleanup(func() { gitT(t, source.root, "restore", "--staged", "--worktree", "--", "activity.py") })
		check("изменение вне файла аудита")
		gitT(t, source.root, "add", "activity.py")
		check("изменение вне файла аудита")
	})
	t.Run("изменено скрытое содержимое источника", func(t *testing.T) {
		gitT(t, source.root, "update-index", "--assume-unchanged", "activity.py")
		t.Cleanup(func() {
			gitT(t, source.root, "update-index", "--no-assume-unchanged", "activity.py")
			gitT(t, source.root, "restore", "--", "activity.py")
		})
		write(t, filepath.Join(source.root, "activity.py"), "def read_activity(row):\n    return row.LastActivityAt\n# local change\n")
		check("содержимое рабочего файла отличается от коммита прогона")
	})
	t.Run("наблюдение ссылается на незакоммиченный файл", func(t *testing.T) {
		path := filepath.Join(source.root, "untracked.py")
		write(t, path, "return row.LastActivityAt\n")
		t.Cleanup(func() { os.Remove(path) })
		copyRun := run
		copyRun.Read.At = "untracked.py:1"
		copyRun.Negative.Read.At = copyRun.Read.At
		e.ObjectComparison.Proof.Runtime = signNameRuntimeRecord(t, key, copyRun)
		t.Cleanup(func() { e.ObjectComparison.Proof.Runtime = proof })
		check("untracked.py: ссылка должна вести в обычный файл коммита прогона")
	})
	t.Run("наблюдение ссылается на символьную ссылку", func(t *testing.T) {
		head := gitT(t, source.root, "rev-parse", "HEAD")
		t.Cleanup(func() { gitT(t, source.root, "checkout", "--detach", head) })
		if err := os.Symlink("activity.py", filepath.Join(source.root, "link.py")); err != nil {
			t.Fatal(err)
		}
		gitT(t, source.root, "add", "link.py")
		gitT(t, source.root, "commit", "-qm", "symbolic link")
		copyRun := run
		copyRun.Commit = gitT(t, source.root, "rev-parse", "HEAD")
		copyRun.Read.At = "link.py:2"
		copyRun.Negative.Read.At = copyRun.Read.At
		e.ObjectComparison.Proof.Runtime = signNameRuntimeRecord(t, key, copyRun)
		t.Cleanup(func() { e.ObjectComparison.Proof.Runtime = proof })
		check("link.py: ссылка должна вести в обычный файл коммита прогона")
	})
	t.Run("одинаковое дерево в соседней ветке", func(t *testing.T) {
		head := gitT(t, source.root, "rev-parse", "HEAD")
		gitT(t, source.root, "checkout", "-qb", "unrelated", run.Commit)
		gitT(t, source.root, "commit", "--allow-empty", "-qm", "sibling")
		copyRun := run
		copyRun.Commit = gitT(t, source.root, "rev-parse", "HEAD")
		gitT(t, source.root, "checkout", "--detach", head)
		e.ObjectComparison.Proof.Runtime = signNameRuntimeRecord(t, key, copyRun)
		t.Cleanup(func() { e.ObjectComparison.Proof.Runtime = proof })
		check("должен быть предком HEAD")
	})
	t.Run("путь рядом с точным путём аудита", func(t *testing.T) {
		head := gitT(t, source.root, "rev-parse", "HEAD")
		t.Cleanup(func() { gitT(t, source.root, "checkout", "--detach", head) })
		path := source.reportPath + ".previous-2"
		write(t, path, "{}\n")
		gitT(t, source.root, "add", "--", path)
		gitT(t, source.root, "commit", "-qm", "neighbor of the report")
		check(".previous-2")
	})
	write(t, filepath.Join(source.root, "code.go"), "package changed\n")
	gitT(t, source.root, "add", "code.go")
	gitT(t, source.root, "commit", "-qm", "code changed")
	check(`отличается от HEAD по пути "code.go"`)
}

func nameRuntimeFixture(t *testing.T) (*evidenceSource, string, nameRuntimeRecord, func() Evidence) {
	t.Helper()
	root, report := fixture(t)
	t.Setenv(nameRuntimeSigners, "")
	write(t, filepath.Join(root, "code.go"), "package sample\nvar row struct{ LastActivityAt string }\nfunc SetActivity(stamp string) {\n\trow.LastActivityAt = stamp\n}\n")
	write(t, filepath.Join(root, "activity.py"), "def read_activity(row):\n    return row.LastActivityAt\n")
	gitT(t, root, "add", "code.go", "activity.py")
	gitT(t, root, "commit", "-qm", "activity writer and reader")
	files, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	key := nameRuntimeTestKey(t)
	writeNameRuntimeAllowed(t, filepath.Join(os.Getenv("HOME"), ".devkit", "style-runtime.allowed_signers"), key)
	source := &evidenceSource{root: root, reportPath: report, taskID: "XR-1", files: files, cache: make(map[string][]string)}
	stamp, before, zero := "2026-11-22T13:14:00Z", "2026-11-21T13:14:00Z", 0
	probe, writeArg, controlArg := "independent-activity-probe", "write", "control"
	writeSite := nameRuntimeSite{At: "code.go:4", Quote: "row.LastActivityAt = stamp", Expression: "row.LastActivityAt", Identity: "activity:row-1"}
	readSite := nameRuntimeSite{At: "activity.py:2", Quote: "return row.LastActivityAt", Expression: "row.LastActivityAt", Identity: "activity:row-1"}
	record := nameRuntimeRecord{
		Version: 1, Task: "XR-1", Commit: gitT(t, root, "rev-parse", "HEAD"), Object: "code.go#SetActivity", Declaration: nameRuntimeDeclaration, Actual: "LastActivityAt",
		Command: nameRuntimeCommand{&probe, &writeArg},
		Write:   nameRuntimeWrite{nameRuntimeObservation: nameRuntimeObservation{nameRuntimeSite: writeSite, Value: &stamp}, SourceValue: "stamp"},
		Read:    nameRuntimeObservation{nameRuntimeSite: readSite, Value: &stamp},
		Negative: nameRuntimeControl{
			Command: nameRuntimeCommand{&probe, &controlArg}, TargetWrites: &zero,
			Read: nameRuntimeObservation{nameRuntimeSite: readSite, Value: &before},
		},
	}
	return source, key, record, func() Evidence {
		word := "Activity"
		return Evidence{Object: record.Object, Check: "writes", Result: "checked",
			ObjectComparison: &NameObjectComparison{Expected: []string{word}, Actual: record.Actual, ActualAt: writeSite.At,
				Proof: &NameObjectProof{Expression: writeSite.Expression, Words: []NameProofWord{{Word: word, Role: "object",
					Fact: ReviewFact{Location: writeSite.At, Quote: writeSite.Quote, Value: writeSite.Expression, Text: "Записывается время активности."}}}}},
			WriteEvidence: &WriteEvidence{Word: &word, Target: writeSite.Expression, Value: record.Write.SourceValue, WriteAt: writeSite.At},
		}
	}
}

func nameRuntimeTestKey(t *testing.T) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "runner")
	if b, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, b)
	}
	return key
}

func writeNameRuntimeAllowed(t *testing.T, allowed, key string) {
	t.Helper()
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	write(t, allowed, "independent-reviewer namespaces=\"devkit-style-001\" "+string(pub))
}

func signNameRuntimeRecord(t *testing.T, key string, record any) *NameRuntimeProof {
	t.Helper()
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	write(t, payloadPath, string(payload)+"\n")
	if b, err := exec.Command("ssh-keygen", "-Y", "sign", "-f", key, "-n", nameRuntimeNamespace, payloadPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, b)
	}
	sig, err := os.ReadFile(payloadPath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	return &NameRuntimeProof{Runner: "independent-reviewer", Payload: string(payload) + "\n", Signature: string(sig)}
}

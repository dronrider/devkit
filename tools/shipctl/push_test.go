package main

import (
	"strings"
	"testing"
)

// boardCommit кладёт коммит, трогающий только файл задачи: то, что рубеж
// DK-602 обязан считать чистой доской вне зависимости от ID в subject.
func boardCommit(t *testing.T, root, id, note string) {
	t.Helper()
	write(t, root, "docs/tasks/"+id+".md", "# "+id+"\n"+note+"\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): "+id+" "+note)
}

// TestPushMixedRangePasses: диапазон с кодом, у которого легитимный ID (XR-001
// в In progress), и с чистой доской рядом проходит целиком. Это и есть исход
// DK-602: мелочь, слитая в main мимо ship/merge, больше не запирает пуш
// доски.
func TestPushMixedRangePasses(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	codeCommit(t, root, "XR-001", "feature.txt")
	boardCommit(t, root, "XR-001", "ход")

	msg, err := cmdPush(root, PushParams{})
	if err != nil {
		t.Fatalf("смешанный диапазон с легитимным ID должен пройти: %v", err)
	}
	if !strings.Contains(msg, "запушен") {
		t.Fatalf("сообщение не называет пуш: %q", msg)
	}
	local := gitT(t, root, "rev-parse", "main")
	if remote := gitT(t, bare, "rev-parse", "main"); remote != local {
		t.Fatalf("origin не сдвинулся: origin=%s local=%s", remote, local)
	}
}

// TestPushPureBoardRangePasses: чистая доска без единого кода проходит, как и
// раньше до DK-602.
func TestPushPureBoardRangePasses(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	boardCommit(t, root, "XR-001", "ход")

	if _, err := cmdPush(root, PushParams{}); err != nil {
		t.Fatalf("чистая доска должна пройти: %v", err)
	}
	local := gitT(t, root, "rev-parse", "main")
	if remote := gitT(t, bare, "rev-parse", "main"); remote != local {
		t.Fatalf("origin не сдвинулся: origin=%s local=%s", remote, local)
	}
}

// TestPushBareCodeWithoutIDRefused: голый код без ID задачи в subject рубеж
// отбивает по-старому, origin остаётся нетронутым.
func TestPushBareCodeWithoutIDRefused(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	before := gitT(t, bare, "rev-parse", "main")
	write(t, root, "tools/app.txt", "правка\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "правка без ID задачи")

	_, err := cmdPush(root, PushParams{})
	if err == nil {
		t.Fatal("голый код без ID должен отбить пуш")
	}
	if !strings.Contains(err.Error(), "без ID задачи") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
	if after := gitT(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("origin сдвинулся при отказе: было %s стало %s", before, after)
	}
}

// TestPushCodeWithBacklogIDRefused: код с ID задачи, которая ещё в Backlog
// (XR-002 в setup), рубеж тоже отбивает: работа над ней не начата.
func TestPushCodeWithBacklogIDRefused(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	before := gitT(t, bare, "rev-parse", "main")
	codeCommit(t, root, "XR-002", "backlog.txt")

	_, err := cmdPush(root, PushParams{})
	if err == nil {
		t.Fatal("код с ID из Backlog должен отбить пуш")
	}
	if !strings.Contains(err.Error(), "XR-002") || !strings.Contains(err.Error(), "Backlog") {
		t.Fatalf("отказ не называет задачу и Backlog: %v", err)
	}
	if after := gitT(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("origin сдвинулся при отказе: было %s стало %s", before, after)
	}
}

// archiveCommit заводит строку архива для ID: доска (docs/TASKS-archive.md)
// это тоже boardOnly, не код, а рубеж обязан считать такой ID легитимным для
// последующего код-коммита с ним же (DK-602, замечание ревью: фикс уже
// закрытой и заархивированной задачи это обычный код-коммит, а не дыра).
func archiveCommit(t *testing.T, root, id string) {
	t.Helper()
	row := "| " + id + " | Тест | task | P3 | 2026-01-01 | [tasks/archive/2026/" + id + ".md](tasks/archive/2026/" + id + ".md) |\n"
	write(t, root, "docs/TASKS-archive.md",
		"# devkit: сделано\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---|--------|\n"+row)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): "+id+" закрыта, строка в архив")
}

// TestPushCodeWithUnknownIDRefused: ID задачи, которой на доске нет вообще
// (не в живом docs/TASKS.md и не в docs/TASKS-archive.md, опечатка,
// выдуманный номер, незаведённая задача), рубеж отбивает явно, а не
// пропускает молча (DK-602, замечание ревью на rangeVerdict/sectOf).
func TestPushCodeWithUnknownIDRefused(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	before := gitT(t, bare, "rev-parse", "main")
	codeCommit(t, root, "XR-404", "unknown.txt")

	_, err := cmdPush(root, PushParams{})
	if err == nil {
		t.Fatal("код с ID, которого нет ни на доске, ни в архиве, должен отбить пуш")
	}
	if !strings.Contains(err.Error(), "XR-404") || !strings.Contains(err.Error(), "архив") {
		t.Fatalf("отказ не называет задачу и архив: %v", err)
	}
	if after := gitT(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("origin сдвинулся при отказе: было %s стало %s", before, after)
	}
}

// TestPushCodeWithArchivedIDPasses: ID задачи, закрытой и заархивированной
// (есть в docs/TASKS-archive.md, но не в живом docs/TASKS.md), рубеж
// пускает: это обычный фикс уже слитой задачи, а не дыра.
func TestPushCodeWithArchivedIDPasses(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	archiveCommit(t, root, "XR-050")
	bare := addRemote(t, root)
	codeCommit(t, root, "XR-050", "fix-after-close.txt")

	msg, err := cmdPush(root, PushParams{})
	if err != nil {
		t.Fatalf("код с ID закрытой и заархивированной задачи должен пройти: %v", err)
	}
	if !strings.Contains(msg, "запушен") {
		t.Fatalf("сообщение не называет пуш: %q", msg)
	}
	local := gitT(t, root, "rev-parse", "main")
	if remote := gitT(t, bare, "rev-parse", "main"); remote != local {
		t.Fatalf("origin не сдвинулся: origin=%s local=%s", remote, local)
	}
}

// TestPushCheckOnlyDoesNotPush: --check-only только отвечает вердиктом по
// названной паре sha и не трогает origin, тем самым флагом её зовёт
// hooks/pre-push.
func TestPushCheckOnlyDoesNotPush(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	before := gitT(t, bare, "rev-parse", "main")
	remoteSHA := gitT(t, root, "rev-parse", "origin/main")
	codeCommit(t, root, "XR-001", "feature.txt")
	localSHA := gitT(t, root, "rev-parse", "main")
	// След ревью на HEAD: без него код диапазона отбивает ворот следа, и
	// предмет этого теста (что --check-only не пушит) до проверки не доедет.
	reviewNote(t, root, "Уровень 1 до "+short(localSHA)+": рутина")

	if _, err := cmdPush(root, PushParams{CheckOnly: true, RemoteSHA: remoteSHA, LocalSHA: localSHA}); err != nil {
		t.Fatalf("check-only с легитимным ID должен пройти: %v", err)
	}
	if after := gitT(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("--check-only не должен пушить: было %s стало %s", before, after)
	}

	write(t, root, "tools/bare.txt", "код\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "код без ID")
	localSHA2 := gitT(t, root, "rev-parse", "main")
	if _, err := cmdPush(root, PushParams{CheckOnly: true, RemoteSHA: remoteSHA, LocalSHA: localSHA2}); err == nil {
		t.Fatal("check-only с голым кодом должен отказать")
	}
	if after := gitT(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("--check-only не должен пушить и при отказе: было %s стало %s", before, after)
	}
}

// TestPushRenameIntoBoardIsRefused: перенос кода в docs/tasks/ не должен
// сходить за доску, --no-renames в rangeVerdict держит ту же дыру закрытой,
// что и старый разбор в hooks/pre-push (DK-119).
func TestPushRenameIntoBoardIsRefused(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	bare := addRemote(t, root)
	before := gitT(t, bare, "rev-parse", "main")
	write(t, root, "tools/movable.txt", "код\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "код перед переносом")
	gitT(t, root, "mv", "tools/movable.txt", "docs/tasks/movable.md")
	gitT(t, root, "commit", "-qm", "перенос кода в доску без ID")

	if _, err := cmdPush(root, PushParams{}); err == nil {
		t.Fatal("перенос кода в docs/tasks/ не должен сходить за доску")
	}
	if after := gitT(t, bare, "rev-parse", "main"); after != before {
		t.Fatalf("origin сдвинулся при отказе: было %s стало %s", before, after)
	}
}

// TestPushNothingToPush: main и origin/main совпадают, команда молчит без
// ошибки и без пуша.
func TestPushNothingToPush(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)

	msg, err := cmdPush(root, PushParams{})
	if err != nil {
		t.Fatalf("нечего пушить не должно быть ошибкой: %v", err)
	}
	if !strings.Contains(msg, "пушить нечего") {
		t.Fatalf("сообщение не называет пустой диапазон: %q", msg)
	}
}

// reviewNote кладёт след ревью git-заметкой на HEAD, тем же способом, каким его
// пишет `taskctl review level` в репозитории без доски.
func reviewNote(t *testing.T, root, line string) {
	t.Helper()
	gitT(t, root, "notes", "--ref=review", "add", "-f", "-m", line, "HEAD")
}

// checkOnly зовёт проверку диапазона от origin/main до main тем же способом,
// каким её зовёт hooks/pre-push.
func checkOnly(t *testing.T, root string) error {
	t.Helper()
	remote := gitT(t, root, "rev-parse", "origin/main")
	local := gitT(t, root, "rev-parse", "HEAD")
	_, err := cmdPush(root, PushParams{CheckOnly: true, RemoteSHA: remote, LocalSHA: local})
	return err
}

// TestPushGateRefusesCodeWithoutReviewTrace: код с легитимным ID задачи, но без
// следа ревью, ворот отбивает. ID в subject говорит, чья это правка, а не
// то, что её кто-то читал.
func TestPushGateRefusesCodeWithoutReviewTrace(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	codeCommit(t, root, "XR-001", "feature.txt")

	err := checkOnly(t, root)
	if err == nil {
		t.Fatal("код без следа ревью должен отбиваться")
	}
	if !strings.Contains(err.Error(), "нет следа ревью") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
	if !strings.Contains(err.Error(), "taskctl review level") {
		t.Fatalf("отказ не говорит, чем поставить след: %v", err)
	}
}

// TestPushGatePassesWithReviewNote: заметка ревью ровно на HEAD пропускает код.
func TestPushGatePassesWithReviewNote(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	codeCommit(t, root, "XR-001", "feature.txt")
	reviewNote(t, root, "Уровень 2 до a1b2c3d: неопределённость 1")

	if err := checkOnly(t, root); err != nil {
		t.Fatalf("заметка ревью на HEAD должна пропускать код: %v", err)
	}
}

// TestPushGateRefusesNoteOnAncestor: заметка на прошлом коммите след с HEAD не
// снимает, иначе код, дописанный после ревью, уезжал бы под чужим следом.
func TestPushGateRefusesNoteOnAncestor(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	codeCommit(t, root, "XR-001", "feature.txt")
	reviewNote(t, root, "Уровень 2 до a1b2c3d: неопределённость 1")
	codeCommit(t, root, "XR-001", "later.txt")

	if err := checkOnly(t, root); err == nil {
		t.Fatal("код, дописанный после ревью, должен отбиваться")
	}
}

// TestPushGatePassesWithTaskFileLevel: у ветки задачи доски след живёт строкой
// уровня в файле задачи, и заметки такой ветке не нужно.
func TestPushGatePassesWithTaskFileLevel(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	gitT(t, root, "checkout", "-q", "-b", "xr-001-worktree")
	codeCommit(t, root, "XR-001", "feature.txt")

	if err := checkOnly(t, root); err != nil {
		t.Fatalf("строка уровня в docs/tasks/XR-001.md должна пропускать код ветки: %v", err)
	}
}

// TestPushGateRefusesTaskBranchWithoutLevel: та же ветка задачи, но со снятой
// строкой уровня, отбивается, и отказ называет файл, в котором её нет.
func TestPushGateRefusesTaskBranchWithoutLevel(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	gitT(t, root, "checkout", "-q", "-b", "xr-001")
	write(t, root, "docs/tasks/XR-001.md", "# XR-001\n\n## Ревью\n\n- гонка в close: исправлено\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 ревью без уровня")
	codeCommit(t, root, "XR-001", "feature.txt")

	err := checkOnly(t, root)
	if err == nil {
		t.Fatal("ветка задачи без строки уровня должна отбиваться")
	}
	if !strings.Contains(err.Error(), "docs/tasks/XR-001.md") {
		t.Fatalf("отказ не называет файл задачи: %v", err)
	}
}

// TestPushGatePassesPureBoard: чистая доска без кода проходит без следа ревью,
// как и раньше: коммит доски пушится сразу, а ревьюить там нечего.
func TestPushGatePassesPureBoard(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	boardCommit(t, root, "XR-001", "ход")

	if err := checkOnly(t, root); err != nil {
		t.Fatalf("чистая доска должна проходить без следа ревью: %v", err)
	}
}

// TestPushGateOutsideBoard: в репозитории без доски критерий DK-602 про ID
// неприменим, а ворот следа ревью работает: голый код отбит, код с заметкой
// проходит.
func TestPushGateOutsideBoard(t *testing.T) {
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	write(t, root, "code.txt", "первый\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: первая правка")
	base := gitT(t, root, "rev-parse", "HEAD")
	write(t, root, "code.txt", "второй\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: правка без ID задачи")
	head := gitT(t, root, "rev-parse", "HEAD")

	if _, err := cmdPush(root, PushParams{CheckOnly: true, RemoteSHA: base, LocalSHA: head}); err == nil {
		t.Fatal("код без следа ревью должен отбиваться и вне доски")
	}
	reviewNote(t, root, "Круг 1 до "+short(head)+": без замечаний")
	if _, err := cmdPush(root, PushParams{CheckOnly: true, RemoteSHA: base, LocalSHA: head}); err != nil {
		t.Fatalf("заметка ревью должна пропускать код вне доски: %v", err)
	}
}

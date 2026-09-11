package merged

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Стенд это временный репозиторий с доской. Глобальный конфиг git снят: хуки
// devkit с машины разработчика отбили бы коммит кода без теста, а подпись
// коммитов спросила бы ключ.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit кладёт файлы и коммитит их с subject, отдаёт полный sha.
func commit(t *testing.T, dir, subj string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		put(t, dir, rel, body)
		gitT(t, dir, "add", rel)
	}
	gitT(t, dir, "commit", "-q", "-m", subj)
	return gitT(t, dir, "rev-parse", "HEAD")
}

const archiveHead = "# сделано\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n"

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "docs(tasks): доска", map[string]string{
		"docs/TASKS.md":         "# доска\n",
		"docs/TASKS-archive.md": archiveHead,
	})
	return dir
}

func mustTask(t *testing.T, root, id string) Verdict {
	t.Helper()
	v, err := Task(root, "main", id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestBranchMergedAfterDelete: ветку слитой задачи shipctl удаляет, и признак
// обязан отвечать уже без неё. Опора тут subject коммита работы.
func TestBranchMergedAfterDelete(t *testing.T) {
	root := repo(t)
	gitT(t, root, "switch", "-q", "-c", "dk-2")
	work := commit(t, root, "feat(x): DK-2 код", map[string]string{"x/code.go": "package x\n"})
	gitT(t, root, "switch", "-q", "main")
	if v := mustTask(t, root, "DK-2"); v.Merged {
		t.Fatalf("работа лежит только в ветке, а признак ответил «слита»: %+v", v)
	}
	gitT(t, root, "merge", "-q", "--no-ff", "-m", "Merge branch dk-2", "dk-2")
	gitT(t, root, "branch", "-q", "-D", "dk-2")
	if _, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", "dk-2").Output(); err == nil {
		t.Fatal("ветка dk-2 осталась, тест проверял бы не то")
	}
	v := mustTask(t, root, "DK-2")
	if !v.Merged || v.Sha != work {
		t.Fatalf("после слияния и удаления ветки признак %+v, ждали слитую работу %s", v, work)
	}
	if !strings.Contains(v.Said("DK-2"), "слита") || !strings.Contains(v.Said("DK-2"), work[:8]) {
		t.Fatalf("слова признака не называют коммит: %q", v.Said("DK-2"))
	}
}

// TestRecordWithoutIDInSubject: коммит без номера в subject находится по
// записи «Выкат» в файле задачи, которую shipctl коммитит в main.
func TestRecordWithoutIDInSubject(t *testing.T) {
	root := repo(t)
	work := commit(t, root, "feat: правка без номера", map[string]string{"x/code.go": "package x\n"})
	if v := mustTask(t, root, "DK-3"); v.Merged {
		t.Fatalf("без записи и без номера признак ответил «слита»: %+v", v)
	}
	commit(t, root, "docs(tasks): DK-3 коммиты слияния", map[string]string{
		"docs/tasks/DK-3.md": "# DK-3\n\n## Выкат\n\n- слито 2026-09-11: " + work[:7] + "\n",
	})
	if v := mustTask(t, root, "DK-3"); !v.Merged || v.Sha != work {
		t.Fatalf("запись «Выкат» не прочитана: %+v", v)
	}
}

// TestBoardOnlyIsNotMerged: правка доски и файла задачи слиянием не считается,
// правка в docs/lld считается: зависимой от LLD нужен документ в main.
func TestBoardOnlyIsNotMerged(t *testing.T) {
	root := repo(t)
	commit(t, root, "docs(tasks): DK-4 в работу", map[string]string{
		"docs/TASKS.md":      "# доска\n| DK-4 |\n",
		"docs/tasks/DK-4.md": "# DK-4\n",
	})
	if v := mustTask(t, root, "DK-4"); v.Merged {
		t.Fatalf("правка доски сочтена слиянием: %+v", v)
	}
	lld := commit(t, root, "docs(lld): DK-4 документ", map[string]string{"docs/lld/DK-4.md": "# LLD\n"})
	if v := mustTask(t, root, "DK-4"); !v.Merged || v.Sha != lld {
		t.Fatalf("правка docs/lld не сочтена слиянием: %+v", v)
	}
}

// TestRevertHoldsUntilRemerge: откат новее работы делает признак ложным до
// следующего слияния.
func TestRevertHoldsUntilRemerge(t *testing.T) {
	root := repo(t)
	commit(t, root, "feat: DK-5 код", map[string]string{"x/code.go": "package x\n"})
	rev := commit(t, root, "revert: DK-5 откат", map[string]string{"x/code.go": ""})
	v := mustTask(t, root, "DK-5")
	if v.Merged || !v.Reverted || v.Sha != rev {
		t.Fatalf("откаченная работа сочтена слитой: %+v", v)
	}
	if !strings.Contains(v.Said("DK-5"), "откачена") {
		t.Fatalf("слова признака не называют отката: %q", v.Said("DK-5"))
	}
	again := commit(t, root, "fix: DK-5 снова", map[string]string{"x/code.go": "package x\n"})
	if v := mustTask(t, root, "DK-5"); !v.Merged || v.Sha != again {
		t.Fatalf("новое слияние после отката не увидено: %+v", v)
	}
}

// TestOwnerIsFirstID: соседка, упомянутая в subject второй, работы не
// получает.
func TestOwnerIsFirstID(t *testing.T) {
	root := repo(t)
	commit(t, root, "feat: DK-7 правка, соседка DK-6", map[string]string{"x/code.go": "package x\n"})
	if v := mustTask(t, root, "DK-6"); v.Merged {
		t.Fatalf("чужой коммит записан соседке: %+v", v)
	}
	if v := mustTask(t, root, "DK-7"); !v.Merged {
		t.Fatalf("владелец коммита не увиден: %+v", v)
	}
}

// TestSeenFromWorktree: голова спрашивает признак из дерева своей задачи, где
// файл соседки и архив стоят на старой базе ветки. Запись «Выкат» и строка
// архива лежат в основном чекауте, и признак обязан их увидеть.
func TestSeenFromWorktree(t *testing.T) {
	root := repo(t)
	commit(t, root, "docs(tasks): DK-8 файл", map[string]string{"docs/tasks/DK-8.md": "# DK-8\n"})
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, root, "worktree", "add", "-q", "-b", "dk-9", wt)
	work := commit(t, root, "feat: правка без номера", map[string]string{"x/code.go": "package x\n"})
	if v := mustTask(t, wt, "DK-8"); v.Merged {
		t.Fatalf("без записи признак ответил «слита»: %+v", v)
	}
	// Запись ещё не закоммичена: shipctl дописал её в основной чекаут.
	put(t, root, "docs/tasks/DK-8.md", "# DK-8\n\n## Выкат\n\n- слито: "+work[:7]+"\n")
	if v := mustTask(t, wt, "DK-8"); !v.Merged {
		t.Fatalf("запись основного чекаута не видна из дерева задачи: %+v", v)
	}
	if MainTree(wt) == wt {
		t.Fatalf("основной чекаут из дерева задачи не найден")
	}

	if ok, err := Closed(wt, "main", "DK-8"); err != nil || ok {
		t.Fatalf("строка в архиве до закрытия: %v %v", ok, err)
	}
	put(t, root, "docs/TASKS-archive.md", archiveHead+"| DK-8 | файл | task | P2 | 2026-09-11 | ссылка |\n")
	if ok, err := Closed(wt, "main", "DK-8"); err != nil || !ok {
		t.Fatalf("строка архива основного чекаута не видна из дерева задачи: %v %v", ok, err)
	}
}

// TestClosedReadsMainToo: основной чекаут может стоять на другой ветке, и
// закоммиченный архив main читается отдельно.
func TestClosedReadsMainToo(t *testing.T) {
	root := repo(t)
	commit(t, root, "docs(tasks): DK-10 закрыта", map[string]string{
		"docs/TASKS-archive.md": archiveHead + "| DK-10 | x | task | P2 | 2026-09-11 | - |\n",
	})
	gitT(t, root, "switch", "-q", "-c", "other", "HEAD~1")
	if ok, err := Closed(root, "main", "DK-10"); err != nil || !ok {
		t.Fatalf("архив в main не прочитан: %v %v", ok, err)
	}
	if ok, _ := Closed(root, "main", "DK-11"); ok {
		t.Fatal("чужая строка сочтена закрытой")
	}
}

// TestMainFallsBackToMaster: у старых репозиториев основная ветка master.
func TestMainFallsBackToMaster(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "master")
	commit(t, dir, "init", map[string]string{"a": "a\n"})
	if got, err := Main(dir); err != nil || got != "master" {
		t.Fatalf("основная ветка %q (%v), ждали master", got, err)
	}
	if _, err := Main(t.TempDir()); err == nil {
		t.Fatal("вне репозитория основная ветка нашлась")
	}
}

func TestBoardOnly(t *testing.T) {
	cases := map[string]bool{
		"docs/TASKS.md\ndocs/tasks/DK-1.md\n": true,
		"docs/TASKS-archive.md":               true,
		"docs/lld/DK-1.md":                    false,
		"docs/TASKS.md\ntools/x.go":           false,
		"":                                    true,
	}
	for files, want := range cases {
		if got := BoardOnly(files); got != want {
			t.Errorf("BoardOnly(%q) = %v, ждали %v", files, got, want)
		}
	}
}

func TestIsRevert(t *testing.T) {
	cases := map[string]bool{
		"revert: DK-1 x":             true,
		`Revert "feat: DK-1 x"`:      true,
		"fix: DK-1 откат настроек":   true,
		"fix: DK-1 откатить правило": false,
		"feat: DK-1 x":               false,
	}
	for subj, want := range cases {
		if got := IsRevert(subj); got != want {
			t.Errorf("IsRevert(%q) = %v, ждали %v", subj, got, want)
		}
	}
}

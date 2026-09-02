package freshtree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnv: окружение прогона собрано явно. Живой HOME подменён временным,
// каталоги из-под него ушли из PATH, переменные харнеса CLAUDE* унесены, а
// тулчейны вне дома и прочие переменные остались.
func TestEnv(t *testing.T) {
	home := "/fake/home"
	t.Setenv("CLAUDE_CODE_TEST_MARKER", "1")
	t.Setenv("FRESHTREE_KEEP_ME", "yes")
	t.Setenv("GOPATH", home+"/go")
	t.Setenv("VIRTUAL_ENV", home+"/.venv")
	// /fake/homework проверяет границу: общий префикс строки это не вложенность.
	t.Setenv("PATH", home+"/bin:/usr/bin:/fake/homework:"+home)
	env := Env(home, "/tmp/newhome", "")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if !strings.Contains(joined, "\nHOME=/tmp/newhome\n") {
		t.Fatalf("нет временного HOME:\n%s", joined)
	}
	if strings.Count(joined, "\nHOME=") != 1 {
		t.Fatalf("HOME должен быть один:\n%s", joined)
	}
	if strings.Contains(joined, "CLAUDE_CODE_TEST_MARKER") {
		t.Fatalf("переменная харнеса протекла:\n%s", joined)
	}
	if strings.Contains(joined, "\nGOPATH=") || strings.Contains(joined, "\nVIRTUAL_ENV=") {
		t.Fatalf("указатель внутрь живого дома протёк:\n%s", joined)
	}
	if !strings.Contains(joined, "\nFRESHTREE_KEEP_ME=yes\n") {
		t.Fatalf("обычная переменная потеряна:\n%s", joined)
	}
	if !strings.Contains(joined, "\nPATH=/usr/bin:/fake/homework\n") {
		t.Fatalf("PATH урезан неверно:\n%s", joined)
	}
}

func TestTrimHomePath(t *testing.T) {
	if got := TrimHomePath("/a:/b", ""); got != "/a:/b" {
		t.Fatalf("пустой home не должен резать PATH: %q", got)
	}
	if got := TrimHomePath("/h/bin:/usr/bin:/h", "/h"); got != "/usr/bin" {
		t.Fatalf("каталоги под home остались: %q", got)
	}
}

// TestMakeCheckoutsCommitWithoutWorkTree: дерево выложено на названный коммит,
// незакоммиченный артефакт исходного чекаута в него не поехал, рядом стоит
// пустой временный HOME, а cleanup уносит и дерево, и запись о нём в гите.
func TestMakeCheckoutsCommitWithoutWorkTree(t *testing.T) {
	root := gitRepo(t)
	sha := gitT(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "artifact.pyc"), []byte("след работы\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, home, cleanup, err := Make(root, sha, "freshtree-test-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree, "file.txt")); err != nil {
		t.Fatalf("коммит не выложен: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "artifact.pyc")); err == nil {
		t.Fatal("артефакт прогретого дерева поехал в свежее")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("временный HOME должен быть пустым каталогом: %v, %d записей", err, len(entries))
	}
	cleanup()
	if _, err := os.Stat(tree); err == nil {
		t.Fatal("дерево после уборки на месте")
	}
	if wl := gitT(t, root, "worktree", "list"); strings.Count(wl, "\n") != 0 {
		t.Fatalf("запись о дереве в гите осталась:\n%s", wl)
	}
}

// TestMakeNamesGitFailure: неизвестный коммит это отказ с выводом гита, а не
// голое «exit status 128»: без причины разбирать нечего.
func TestMakeNamesGitFailure(t *testing.T) {
	root := gitRepo(t)
	_, _, _, err := Make(root, "0000000000000000000000000000000000000000", "freshtree-test-")
	if err == nil {
		t.Fatal("выкладка несуществующего коммита должна падать")
	}
	if !strings.Contains(err.Error(), "invalid reference") && !strings.Contains(err.Error(), "fatal") {
		t.Fatalf("отказ не несёт вывода гита: %v", err)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("исходник\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-q", "-m", "init")
	return root
}

func gitT(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestEnvKeepsHomeToolchains: тулчейны из-под дома остаются прогону видны.
// Каталоги названных тулчейнов держатся в PATH на своих местах, указатели
// (CARGO_HOME, RUSTUP_HOME) едут от настоящего дома, а прочие каталоги дома
// уходят как раньше. Без этого подменённый HOME уводит rustup от ~/.rustup, и
// прогон отдаёт «could not choose a version of cargo to run» вместо тестов.
func TestEnvKeepsHomeToolchains(t *testing.T) {
	home := t.TempDir()
	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v20", "bin")
	for _, dir := range []string{filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".rustup"), nvmBin, filepath.Join(home, "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", strings.Join([]string{
		filepath.Join(home, ".cargo", "bin"), nvmBin, "/usr/bin", filepath.Join(home, "bin"),
	}, string(os.PathListSeparator)))
	joined := "\n" + strings.Join(Env(home, "/tmp/newhome", ""), "\n") + "\n"
	wantPath := "\nPATH=" + strings.Join([]string{
		filepath.Join(home, ".cargo", "bin"), nvmBin, "/usr/bin",
	}, string(os.PathListSeparator)) + "\n"
	if !strings.Contains(joined, wantPath) {
		t.Fatalf("каталоги тулчейнов не сохранены на своих местах:\n%s", joined)
	}
	for _, want := range []string{"CARGO_HOME=" + filepath.Join(home, ".cargo"),
		"RUSTUP_HOME=" + filepath.Join(home, ".rustup"),
		"NVM_DIR=" + filepath.Join(home, ".nvm")} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Fatalf("указателя %s в прогоне нет:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "PYENV_ROOT") {
		t.Fatalf("указатель тулчейна, которого на машине нет, протёк:\n%s", joined)
	}
}

// TestEnvLeavesSetToolchainVar: заданное пользователем значение сильнее
// умолчания, и второй копией переменная не задваивается.
func TestEnvLeavesSetToolchainVar(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARGO_HOME", "/opt/cargo")
	joined := "\n" + strings.Join(Env(home, "/tmp/newhome", ""), "\n") + "\n"
	if !strings.Contains(joined, "\nCARGO_HOME=/opt/cargo\n") || strings.Count(joined, "\nCARGO_HOME=") != 1 {
		t.Fatalf("своё значение указателя не пережило сборку окружения:\n%s", joined)
	}
}

// TestEnvPutsTreeBinFirst: утилиты проверяемого дерева стоят в PATH первыми и
// перекрывают одноимённые с машины.
func TestEnvPutsTreeBinFirst(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	joined := strings.Join(Env("/fake/home", "/tmp/newhome", "/tmp/run/bin"), "\n")
	if !strings.Contains(joined, "PATH=/tmp/run/bin"+string(os.PathListSeparator)+"/usr/bin") {
		t.Fatalf("каталог утилит дерева не первый в PATH:\n%s", joined)
	}
}

// TestToolsBuildsTreeUtilities: утилиты дерева собираются по тем же приметам,
// что у релиза. Go-каталог с go.mod становится бинарём, python-каталог с
// одноимённым файлом получает обёртку, и обе команды запускаются из bin.
func TestToolsBuildsTreeUtilities(t *testing.T) {
	tree := t.TempDir()
	writeTool(t, tree, "hello", "go.mod", "module hello\n\ngo 1.26\n")
	writeTool(t, tree, "hello", "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"привет из дерева\") }\n")
	writeTool(t, tree, "pyctl", "pyctl.py", "print(\"питон из дерева\")\n")
	writeTool(t, tree, "docs", "README.md", "не утилита\n")
	bin := filepath.Join(t.TempDir(), "bin")
	names, problems := Tools(tree, bin)
	if len(problems) > 0 {
		t.Fatalf("сборка дерева дала находки: %v", problems)
	}
	if strings.Join(names, ",") != "hello,pyctl" {
		t.Fatalf("собрано не то: %v", names)
	}
	for name, want := range map[string]string{"hello": "привет из дерева", "pyctl": "питон из дерева"} {
		out, err := exec.Command(filepath.Join(bin, name)).CombinedOutput()
		if err != nil || !strings.Contains(string(out), want) {
			t.Fatalf("утилита %s из дерева не запустилась: %v\n%s", name, err, out)
		}
	}
}

// TestToolsNamesBrokenBuild: несобравшаяся утилита это находка с причиной, а не
// провал всего прогона: сценарий, которому она не нужна, зеленеет и без неё.
func TestToolsNamesBrokenBuild(t *testing.T) {
	tree := t.TempDir()
	writeTool(t, tree, "broken", "go.mod", "module broken\n\ngo 1.26\n")
	writeTool(t, tree, "broken", "main.go", "package main\n\nfunc main() { это не go }\n")
	writeTool(t, tree, "hello", "go.mod", "module hello\n\ngo 1.26\n")
	writeTool(t, tree, "hello", "main.go", "package main\n\nfunc main() {}\n")
	names, problems := Tools(tree, filepath.Join(t.TempDir(), "bin"))
	if strings.Join(names, ",") != "hello" {
		t.Fatalf("здоровая утилита не собралась: %v", names)
	}
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "broken: сборка из дерева не прошла:") {
		t.Fatalf("находка не называет утилиту и причину: %v", problems)
	}
}

// TestDiagnoseNamesMissingCommand: отказ по нехватке команды называет саму
// команду и места, где прогон её искал. Разбираются обе формы отказа шелла
// (имя до слов и после), а несобравшаяся утилита объясняется своей находкой.
func TestDiagnoseNamesMissingCommand(t *testing.T) {
	run := &Run{Bin: "/tmp/run/bin", Tools: []string{"agentctl", "taskctl"},
		Env: []string{"PATH=/tmp/run/bin:/usr/bin"}}
	for _, out := range []string{"sh: taskctl: command not found\n", "zsh: command not found: taskctl\n"} {
		said := run.Diagnose(out)
		for _, want := range []string{"taskctl", "/tmp/run/bin", "/usr/bin"} {
			if !strings.Contains(said, want) {
				t.Fatalf("разбор отказа не называет %q: %q (вывод %q)", want, said, out)
			}
		}
	}
	if said := run.Diagnose("FAIL: ждали 2, получили 3\n"); said != "" {
		t.Fatalf("отказ не про команду разбирать нечего: %q", said)
	}
	broken := &Run{Bin: "/tmp/run/bin", Notes: []string{"taskctl: сборка из дерева не прошла: syntax error"}}
	if said := broken.Diagnose("sh: taskctl: command not found\n"); !strings.Contains(said, "syntax error") {
		t.Fatalf("разбор молчит про несобравшуюся утилиту: %q", said)
	}
}

// TestStartAssemblesRun: заход через Start отдаёт дерево, дом, каталог утилит в
// PATH и уборку, которая уносит всё разом.
func TestStartAssemblesRun(t *testing.T) {
	root := gitRepo(t)
	run, err := Start(root, gitT(t, root, "rev-parse", "HEAD"), "freshtree-test-")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+strings.Join(run.Env, "\n"), "\nPATH="+run.Bin+string(os.PathListSeparator)) {
		t.Fatalf("каталога утилит дерева нет в PATH прогона: %v", run.Env)
	}
	if !strings.HasPrefix(run.Bin, filepath.Dir(run.Tree)) || !strings.HasPrefix(run.Home, filepath.Dir(run.Tree)) {
		t.Fatalf("дом и утилиты лежат не во временном корне прогона: %s, %s", run.Home, run.Bin)
	}
	run.Cleanup()
	if _, err := os.Stat(run.Tree); err == nil {
		t.Fatal("дерево после уборки на месте")
	}
}

func writeTool(t *testing.T, tree, tool, name, body string) {
	t.Helper()
	dir := filepath.Join(tree, "tools", tool)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

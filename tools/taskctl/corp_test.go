package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// corpGitT гоняет git и валит тест на ошибке. Свой помощник, а не соседский:
// файл целиком одинаков в трёх утилитах, и таскать его между ними проще без
// связей с остальными тестами модуля.
func corpGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func corpWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// corpBoard кладёт скелет доски: findRoot ищет ровно docs/TASKS.md, содержимое
// ему безразлично.
func corpBoard(t *testing.T, dir string) {
	t.Helper()
	corpWrite(t, filepath.Join(dir, "docs", "TASKS.md"), "# Доска\n")
}

// corpClone заводит «корп-клон»: git-репозиторий с одним коммитом и без доски
// в дереве. Рядом с ним, сиблингом, лежит боковая директория со скелетом доски.
func corpClone(t *testing.T) (base, clone, local string) {
	t.Helper()
	base = t.TempDir()
	clone = filepath.Join(base, "proj")
	local = filepath.Join(base, "proj-local")
	corpWrite(t, filepath.Join(clone, "README.md"), "проект\n")
	corpBoard(t, local)
	corpGitT(t, clone, "init", "-q", "-b", "main")
	corpGitT(t, clone, "config", "user.email", "test@test")
	corpGitT(t, clone, "config", "user.name", "test")
	corpGitT(t, clone, "add", ".")
	corpGitT(t, clone, "commit", "-q", "-m", "init")
	return base, clone, local
}

func corpSame(t *testing.T, got, want string) {
	t.Helper()
	if !corpSamePath(got, want) {
		t.Fatalf("корень %s, ожидался %s", got, want)
	}
}

// TestFindRootRedirect: ключ задан, корнем становится боковая директория, хотя
// в дереве клона доски нет и подъём вверх её не нашёл бы.
func TestFindRootRedirect(t *testing.T) {
	_, clone, local := corpClone(t)
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")

	root, err := findRoot(clone)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	corpSame(t, root, local)
}

// TestFindRootRedirectFromSubdir ловит мутацию «путь разрешён от cwd»: из
// поддиректории клона относительный путь от cwd указал бы на proj/sub/../proj-local.
func TestFindRootRedirectFromSubdir(t *testing.T) {
	_, clone, local := corpClone(t)
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")
	sub := filepath.Join(clone, "sub", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := findRoot(sub)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	corpSame(t, root, local)
}

// TestFindRootRedirectFromDistantWorktree это база разрешения пути. Дерево
// ветки лежит не сиблингом проекта, и от его вершины (как и от cwd) путь
// «../proj-local» ушёл бы в чужое место, где доски нет вовсе. Тест ловит обе
// мутации базы разом, а заодно чтение ключа не только в основном чекауте:
// линкованное дерево делит конфиг с клоном и обязано видеть тот же редирект.
func TestFindRootRedirectFromDistantWorktree(t *testing.T) {
	_, clone, local := corpClone(t)
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")

	far := filepath.Join(t.TempDir(), "trees", "wt")
	corpGitT(t, clone, "worktree", "add", "-q", "-b", "dk-081", far)
	// Ловушка на разрешение от вершины дерева: сиблинг worktree с тем же
	// именем и своей доской. Правильная база его не видит.
	corpBoard(t, filepath.Join(filepath.Dir(far), "proj-local"))

	root, err := findRoot(far)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	corpSame(t, root, local)
}

// TestFindRootRedirectAbsolute: абсолютный путь в ключе берётся как есть.
func TestFindRootRedirectAbsolute(t *testing.T) {
	_, clone, local := corpClone(t)
	corpGitT(t, clone, "config", "devkit.local", local)

	root, err := findRoot(clone)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	corpSame(t, root, local)
}

// TestFindRootRedirectBeatsBoardAbove ловит мутацию «подъём раньше редиректа».
// Остальные сценарии её не различают: доски выше клона в них нет вовсе, подъём
// проваливается, и до corpLocal код доходит в любом порядке. Здесь доска лежит
// и в дереве клона, и над ним (так выходит, когда корп-клон завели внутри
// рабочей директории с домашним проектом), так что подъём нашёл бы её сразу.
// Побеждать обязан редирект: он свойство самого клона, а найденная подъёмом
// доска чужая.
func TestFindRootRedirectBeatsBoardAbove(t *testing.T) {
	for _, where := range []string{"над клоном", "в дереве клона"} {
		t.Run(where, func(t *testing.T) {
			base, clone, local := corpClone(t)
			if where == "над клоном" {
				corpBoard(t, base)
			} else {
				corpBoard(t, clone)
			}
			corpGitT(t, clone, "config", "devkit.local", "../proj-local")

			root, err := findRoot(clone)
			if err != nil {
				t.Fatalf("findRoot: %v", err)
			}
			corpSame(t, root, local)
		})
	}
}

// TestFindRootNoRedirect: ключа нет, значит прежний подъём по docs/TASKS.md.
// Домашние проекты редиректа не замечают.
func TestFindRootNoRedirect(t *testing.T) {
	_, clone, _ := corpClone(t)
	corpBoard(t, clone)
	sub := filepath.Join(clone, "docs")

	root, err := findRoot(sub)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	corpSame(t, root, clone)
}

// TestFindRootOutsideGit: вне git-репозитория поведение прежнее, подъём вверх.
func TestFindRootOutsideGit(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	corpBoard(t, proj)

	root, err := findRoot(filepath.Join(proj, "docs"))
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	corpSame(t, root, proj)
}

// TestFindRootFailureHint: обвязка клона потеряна (редиректа нет), но рядом
// лежит боковая директория с привязкой к этому клону, и отказ называет команду
// восстановления.
func TestFindRootFailureHint(t *testing.T) {
	_, clone, local := corpClone(t)
	corpWrite(t, filepath.Join(local, corpTrackerPath), "key = XX\nrepo = "+clone+"\n")

	_, err := findRoot(clone)
	if err == nil {
		t.Fatal("findRoot нашёл корень, а доски нет нигде")
	}
	if !strings.Contains(err.Error(), "devkitctl corp") {
		t.Fatalf("отказ без команды восстановления: %v", err)
	}
	if !strings.Contains(err.Error(), local) {
		t.Fatalf("отказ не назвал боковую директорию: %v", err)
	}
}

// TestFindRootFailureHintForeignLocal: соседняя «*-local» есть, но её привязка
// указывает на чужой клон, значит отказ прежний. Иначе подсказка вылезала бы в
// любом проекте, у которого сосед носит такое имя.
func TestFindRootFailureHintForeignLocal(t *testing.T) {
	base, clone, local := corpClone(t)
	corpWrite(t, filepath.Join(local, corpTrackerPath), "repo = "+filepath.Join(base, "other")+"\n")

	_, err := findRoot(clone)
	if err == nil {
		t.Fatal("findRoot нашёл корень, а доски нет нигде")
	}
	if strings.Contains(err.Error(), "devkitctl corp") {
		t.Fatalf("подсказка на чужой привязке: %v", err)
	}
}

// TestFindRootFailurePlain: привязки нет вовсе, отказ прежний.
func TestFindRootFailurePlain(t *testing.T) {
	_, clone, _ := corpClone(t)

	_, err := findRoot(clone)
	if err == nil {
		t.Fatal("findRoot нашёл корень, а доски нет нигде")
	}
	if !strings.Contains(err.Error(), "не нашёл docs/TASKS.md") {
		t.Fatalf("неожиданный отказ: %v", err)
	}
	if strings.Contains(err.Error(), "devkitctl corp") {
		t.Fatalf("подсказка без привязки: %v", err)
	}
}

// TestCorpLocalGlobalKeyIgnored: ключ из глобального конфига машины контур не
// включает. Читается он с --local нарочно, редирект это свойство клона, а
// глобальный ключ увёл бы корень разом во всех домашних проектах на машине,
// где корп-контур когда-то настраивали.
func TestCorpLocalGlobalKeyIgnored(t *testing.T) {
	_, clone, local := corpClone(t)
	gc := filepath.Join(t.TempDir(), "gitconfig")
	corpWrite(t, gc, "[devkit]\n\tlocal = "+local+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)

	// Ключ виден без --local, иначе тест зеленел бы по недосмотру, а не по делу.
	if got, err := corpGit(clone, "config", "--get", "devkit.local"); err != nil || got != local {
		t.Fatalf("глобальный ключ не подложился: %q, %v", got, err)
	}
	if got := corpLocal(clone); got != "" {
		t.Fatalf("контур включён глобальным ключом: %s", got)
	}
}

// TestCorpLocalSignal: редирект это признак корп-контура, и он одинаков для
// основного чекаута и для дерева ветки, а без ключа пуст.
func TestCorpLocalSignal(t *testing.T) {
	_, clone, local := corpClone(t)
	if got := corpLocal(clone); got != "" {
		t.Fatalf("контур без ключа: %s", got)
	}
	corpGitT(t, clone, "config", "devkit.local", "../proj-local")
	corpSame(t, corpLocal(clone), local)

	far := filepath.Join(t.TempDir(), "trees", "wt")
	corpGitT(t, clone, "worktree", "add", "-q", "-b", "dk-081-signal", far)
	corpSame(t, corpLocal(far), local)
}

// TestFindRootFailureHintContourLocal: боковая директория общая на контур, и
// привязка лежит подкаталогом ../<контур>-local/<проект> (DK-583). Обвязка
// клона потеряна, а отказ обязан назвать её так же, как называет соседнюю
// «<проект>-local» прежней раскладки.
func TestFindRootFailureHintContourLocal(t *testing.T) {
	base, clone, _ := corpClone(t)
	contour := filepath.Join(base, "acme-local", "proj")
	corpBoard(t, contour)
	corpWrite(t, filepath.Join(contour, corpTrackerPath), "key = XX\nrepo = "+clone+"\n")

	_, err := findRoot(clone)
	if err == nil {
		t.Fatal("findRoot нашёл корень, а доски в дереве клона нет")
	}
	if !strings.Contains(err.Error(), contour) {
		t.Fatalf("отказ не назвал боковую директорию контура: %v", err)
	}
}

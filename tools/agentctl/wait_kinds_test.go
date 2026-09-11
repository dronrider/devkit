package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Условия DK-930: слияние и закрытие соседней строки, конец процесса и
// назначенный час. Стенд это синтетическая доска во временном git, потолок
// берётся из профилей репозитория либо из подложенных копий.

const waitArchive = "# сделано\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n" +
	"| T-009 | закрытая без кода | task | P3 | 2026-09-01 | - |\n"

func waitGit(t *testing.T, dir string, args ...string) string {
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

func waitPut(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitRepo это доска sampleBoard с архивом в git на ветке main. Каталог
// профилей подставлен из репозитория: потолок не должен зависеть от того, что
// лежит на машине.
func waitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("DEVKIT_HOME", repoRoot(t))
	root := writeBoard(t)
	waitPut(t, root, "docs/TASKS-archive.md", waitArchive)
	waitGit(t, root, "init", "-q", "-b", "main")
	waitGit(t, root, "add", "-A")
	waitGit(t, root, "commit", "-q", "-m", "docs(tasks): доска")
	return root
}

func waitCode(t *testing.T, root, subj string) string {
	t.Helper()
	waitPut(t, root, "x/code.go", "package x // "+subj+"\n")
	waitGit(t, root, "add", "x/code.go")
	waitGit(t, root, "commit", "-q", "-m", subj)
	return waitGit(t, root, "rev-parse", "HEAD")
}

// deadPid отдаёт pid процесса, который уже кончился и прибран.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// TestWaitNewKindsLayMarks: каждое новое условие кладёт отметку с целью в той
// записи, которую оболочка проверит без догадок.
func TestWaitNewKindsLayMarks(t *testing.T) {
	root := waitRepo(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})
	pid := strconv.Itoa(os.Getpid())
	cases := []struct {
		kind, target, until, wantTarget, wantUntil string
	}{
		{waitMerged, "T-002", "30m", "T-002", "2026-09-10T10:12:00"},
		{waitClosed, "T-002", "30m", "T-002", "2026-09-10T10:12:00"},
		{waitProc, pid, "5m", pid, "2026-09-10T09:47:00"},
		// Короткая форма часа это ближайший такой момент, срок это сам час.
		{waitHour, "10:00", "", "2026-09-10T10:00:00", "2026-09-10T10:00:00"},
		{waitHour, "2026-09-10 10:30", "", "2026-09-10T10:30:00", "2026-09-10T10:30:00"},
	}
	for _, c := range cases {
		t.Run(c.kind+" "+c.target, func(t *testing.T) {
			said, err := cmdWait(root, "T-001", c.kind, c.target, c.until, "", env, waitNow())
			if err != nil {
				t.Fatal(err)
			}
			got := waitRead(t, dir, "T-001")
			if got["kind"] != c.kind || got["target"] != c.wantTarget || got["until"] != c.wantUntil {
				t.Fatalf("отметка %v, ждали цель %q и срок %q", got, c.wantTarget, c.wantUntil)
			}
			if !strings.Contains(said, c.kind+" "+c.wantTarget) {
				t.Fatalf("строка человеку не называет условия: %q", said)
			}
		})
	}
}

// TestWaitUnknownKindListsKinds: незнакомое условие даёт отказ с перечнем всех
// видов, иначе голове не из чего выбрать правильное.
func TestWaitUnknownKindListsKinds(t *testing.T) {
	root := waitRepo(t)
	env := waitEnv(map[string]string{waitDirEnv: t.TempDir()})
	_, err := cmdWait(root, "T-001", "слияние", "T-002", "10m", "", env, waitNow())
	if err == nil {
		t.Fatal("незнакомое условие легло отметкой")
	}
	for _, k := range waitKinds {
		if !strings.Contains(err.Error(), k) {
			t.Fatalf("отказ %q не называет вид %s", err, k)
		}
	}
}

// TestWaitMergedAfterBranchDeleted: голова ждёт слияния соседки, а ветку
// слитой задачи shipctl удаляет. Признак обязан увидеть слияние уже без ветки,
// и отметка на слитую задачу отбивается: ждать нечего.
func TestWaitMergedAfterBranchDeleted(t *testing.T) {
	root := waitRepo(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})

	waitGit(t, root, "switch", "-q", "-c", "t-002")
	work := waitCode(t, root, "feat: T-002 код")
	waitGit(t, root, "switch", "-q", "main")
	said, came, err := cmdWaitCheck(root, "T-001", waitMerged, "T-002", waitNow())
	if err != nil || came {
		t.Fatalf("до слияния событие пришло: %v %q %v", came, said, err)
	}
	if _, err := cmdWait(root, "T-001", waitMerged, "T-002", "30m", "", env, waitNow()); err != nil {
		t.Fatalf("отметка на неслитую соседку не легла: %v", err)
	}

	waitGit(t, root, "merge", "-q", "--no-ff", "-m", "Merge branch t-002", "t-002")
	waitGit(t, root, "branch", "-q", "-D", "t-002")
	said, came, err = cmdWaitCheck(root, "T-001", waitMerged, "T-002", waitNow())
	if err != nil || !came {
		t.Fatalf("после слияния и удаления ветки событие не пришло: %v %q %v", came, said, err)
	}
	if !strings.Contains(said, work[:8]) {
		t.Fatalf("ответ не называет коммит работы %s: %q", work[:8], said)
	}
	_, err = cmdWait(root, "T-001", waitMerged, "T-002", "30m", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "уже пришло") {
		t.Fatalf("ожидание слитой задачи не отбито: %v", err)
	}
}

// TestWaitClosedSeesTheArchive: закрытие это строка в архиве доски.
func TestWaitClosedSeesTheArchive(t *testing.T) {
	root := waitRepo(t)
	env := waitEnv(map[string]string{waitDirEnv: t.TempDir()})
	if _, came, err := cmdWaitCheck(root, "T-001", waitClosed, "T-002", waitNow()); err != nil || came {
		t.Fatalf("строка на доске сочтена закрытой: %v %v", came, err)
	}
	waitPut(t, root, "docs/TASKS-archive.md", waitArchive+"| T-002 | фича | task | P2 | 2026-09-10 | - |\n")
	if _, came, err := cmdWaitCheck(root, "T-001", waitClosed, "T-002", waitNow()); err != nil || !came {
		t.Fatalf("строка архива не увидена: %v %v", came, err)
	}
	_, err := cmdWait(root, "T-001", waitClosed, "T-002", "30m", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "в архиве") {
		t.Fatalf("ожидание закрытой строки не отбито: %v", err)
	}
}

// TestWaitRowMustBeWaitable: строке, которой событие не грозит, отметка не
// ставится. Ожидание встало бы до срока на выдуманном ID либо на закрытой
// строке без работы в main.
func TestWaitRowMustBeWaitable(t *testing.T) {
	root := waitRepo(t)
	env := waitEnv(map[string]string{waitDirEnv: t.TempDir()})
	cases := map[string]struct{ kind, target string }{
		"ни на доске, ни в архиве":   {waitMerged, "T-999"},
		"слияния закрытой строки":    {waitMerged, "T-009"},
		"закрытие строки без строки": {waitClosed, "T-999"},
	}
	for want, c := range cases {
		_, err := cmdWait(root, "T-001", c.kind, c.target, "10m", "", env, waitNow())
		if err == nil {
			t.Fatalf("%s %s: отметка легла", c.kind, c.target)
		}
		if c.target == "T-999" {
			want = "ни на доске, ни в архиве"
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%s %s: отказ %q не называет %q", c.kind, c.target, err, want)
		}
	}
}

// TestWaitProcess: конец процесса это событие, зомби тоже кончившийся процесс.
func TestWaitProcess(t *testing.T) {
	root := waitRepo(t)
	env := waitEnv(map[string]string{waitDirEnv: t.TempDir()})
	dead := strconv.Itoa(deadPid(t))
	_, err := cmdWait(root, "T-001", waitProc, dead, "5m", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "процесса "+dead+" нет") {
		t.Fatalf("ожидание кончившегося процесса не отбито: %v", err)
	}
	if _, came, err := cmdWaitCheck(root, "T-001", waitProc, strconv.Itoa(os.Getpid()), waitNow()); err != nil || came {
		t.Fatalf("живой процесс сочтён кончившимся: %v %v", came, err)
	}

	// Зомби: процесс вышел, а родитель его ещё не прибрал. Сигнал 0 такой
	// процесс принимает, и без спроса у ps ожидание стояло бы до срока.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	gone := false
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !procAlive(cmd.Process.Pid) {
			gone = true
			break
		}
	}
	if !gone {
		t.Fatal("вышедший и неприбранный процесс считается живым")
	}
}

// TestWaitHour: час в прошлом ждать нечего, срок у часа это сам час, и
// потолок меряется до него.
func TestWaitHour(t *testing.T) {
	root := waitRepo(t)
	env := waitEnv(map[string]string{waitDirEnv: t.TempDir()})
	cases := map[string]struct{ target, until string }{
		"наступил":          {"2026-09-10 09:00", ""},
		"--until не нужен":  {"10:00", "10m"},
		"дольше потолка 2h": {"09:00", ""}, // короткая форма уходит на завтра
		"не разобран":       {"в обед", ""},
	}
	for want, c := range cases {
		_, err := cmdWait(root, "T-001", waitHour, c.target, c.until, "", env, waitNow())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("час %q: отказ %v не называет %q", c.target, err, want)
		}
	}
	if _, came, err := cmdWaitCheck(root, "T-001", waitHour, "2026-09-10 09:42", waitNow()); err != nil || !came {
		t.Fatalf("наступивший час не событие: %v %v", came, err)
	}
}

// waitProfiles подкладывает каталог профилей с правкой потолка. Профили берутся
// из репозитория целиком: loadProfile проверяет все секции.
func waitProfiles(t *testing.T, edit map[string]string) {
	t.Helper()
	home := t.TempDir()
	for name, cap := range edit {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), "kit", "harness", name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		body := string(data)
		if !strings.Contains(body, `wait_cap = "2h"`) {
			t.Fatalf("в профиле %s нет потолка по умолчанию", name)
		}
		line := ""
		if cap != "" {
			line = "wait_cap = " + strconv.Quote(cap)
		}
		body = strings.Replace(body, `wait_cap = "2h"`, line, 1)
		waitPut(t, home, filepath.Join("kit", "harness", name+".toml"), body)
	}
	t.Setenv("DEVKIT_HOME", home)
}

// TestWaitCapFromProfile: потолок срока читается из [head] профиля харнеса, а
// отказ называет и потолок, и профиль.
func TestWaitCapFromProfile(t *testing.T) {
	root := waitRepo(t)
	waitProfiles(t, map[string]string{"claude-code": "30m", "glm-code": "3h"})
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})

	_, err := cmdWait(root, "T-001", waitTimer, "", "45m", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "потолка 30m") || !strings.Contains(err.Error(), "claude-code.toml") {
		t.Fatalf("отказ не называет потолок 30m и профиль claude-code: %v", err)
	}
	if _, err := cmdWait(root, "T-001", waitTimer, "", "20m", "", env, waitNow()); err != nil {
		t.Fatalf("срок под потолком профиля отбит: %v", err)
	}
	_, err = cmdWait(root, "T-001", waitHour, "10:30", "", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "потолка 30m") {
		t.Fatalf("час за потолком профиля не отбит: %v", err)
	}

	glm := waitEnv(map[string]string{waitDirEnv: dir, harnessEnv: "glm-code"})
	if _, err := cmdWait(root, "T-001", waitTimer, "", "150m", "", glm, waitNow()); err != nil {
		t.Fatalf("потолок glm-code не прочитан: %v", err)
	}
}

// TestWaitCapDefaultAndBroken: профиль без ключа получает два часа и говорит
// об умолчании, кривое значение даёт отказ, а не молча подменённый потолок.
func TestWaitCapDefaultAndBroken(t *testing.T) {
	root := waitRepo(t)
	waitProfiles(t, map[string]string{"claude-code": "", "glm-code": "два часа"})
	env := waitEnv(map[string]string{waitDirEnv: t.TempDir()})
	_, err := cmdWait(root, "T-001", waitTimer, "", "3h", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "умолчание") || !strings.Contains(err.Error(), "потолка 2h") {
		t.Fatalf("умолчание не названо: %v", err)
	}
	glm := waitEnv(map[string]string{waitDirEnv: t.TempDir(), harnessEnv: "glm-code"})
	_, err = cmdWait(root, "T-001", waitTimer, "", "10m", "", glm, waitNow())
	if err == nil || !strings.Contains(err.Error(), "не разобран") {
		t.Fatalf("кривой потолок не отбит: %v", err)
	}
}

// TestWaitCheckRefusesTimer: у голого срока события нет, и --check по нему
// отвечает ошибкой, а не «не пришло»: оболочка отличает одно от другого.
func TestWaitCheckRefusesTimer(t *testing.T) {
	root := waitRepo(t)
	if _, _, err := cmdWaitCheck(root, "T-001", waitTimer, "", waitNow()); err == nil {
		t.Fatal("проверка голого срока прошла")
	}
	if _, _, err := cmdWaitCheck(root, "T-001", "погода", "", waitNow()); err == nil {
		t.Fatal("проверка незнакомого условия прошла")
	}
	if _, _, err := cmdWaitCheck(root, "T-001", waitMerged, "мусор", waitNow()); err == nil {
		t.Fatal("проверка слияния без ID прошла")
	}
}

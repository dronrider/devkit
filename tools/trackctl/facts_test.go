package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func at(day, hour, min int) time.Time {
	return time.Date(2026, 8, day, hour, min, 0, 0, time.Local)
}

func dayOf(t *testing.T, days []workDay, date string) time.Duration {
	t.Helper()
	for _, d := range days {
		if d.Date == date {
			return d.Spent
		}
	}
	t.Fatalf("дня %s в разбивке нет: %v", date, days)
	return 0
}

// Разбивка факта по календарным дням идёт из журнала запусков и коммитов, и
// день, в который работа не шла, записи не получает: сессия без коммита тикета
// это работа над другой задачей, и её время в ворклог не едет.
func TestWorkDaysSplitsByCalendarDay(t *testing.T) {
	runs := []time.Time{
		at(3, 10, 0), at(3, 10, 20), at(3, 11, 0), at(3, 11, 20),
		at(4, 9, 0), at(4, 9, 40),
		at(5, 14, 0), at(5, 14, 30),
	}
	commits := []time.Time{at(3, 11, 30), at(4, 10, 0)}
	days := workDays(runs, commits)
	if len(days) != 2 {
		t.Fatalf("жду два дня работы, вижу %v", days)
	}
	if got := dayOf(t, days, "2026-08-03"); got != 90*time.Minute {
		t.Fatalf("день 3 августа: %v, жду 1h30m", got)
	}
	if got := dayOf(t, days, "2026-08-04"); got != time.Hour {
		t.Fatalf("день 4 августа: %v, жду 1h", got)
	}
	for _, d := range days {
		if d.Date == "2026-08-05" {
			t.Fatalf("день без коммитов тикета получил запись: %v", days)
		}
	}
}

// Интервал сессии это wall-clock от старта до конца: вместе с машинным временем
// агента в него входит менеджмент, постановка и разбор отчётов. Урезание до
// чистого времени модели ловится здесь же: у сессии четыре прогона и коммит, и
// час двадцать между первым и последним никуда не деваются.
func TestWorkDaysCountsWholeSession(t *testing.T) {
	runs := []time.Time{at(3, 10, 0), at(3, 10, 5), at(3, 11, 15), at(3, 11, 20)}
	commits := []time.Time{at(3, 11, 20)}
	days := workDays(runs, commits)
	if got := dayOf(t, days, "2026-08-03"); got != 80*time.Minute {
		t.Fatalf("сессия посчитана как %v, жду 1h20m целиком", got)
	}
	if spentHours(dayOf(t, days, "2026-08-03")) != "2h" {
		t.Fatalf("округление дня вверх до часов: %s", spentHours(dayOf(t, days, "2026-08-03")))
	}
}

// Пауза длиннее порога рвёт сессию, а одинокая отметка даёт минимум: команда
// гонялась, значит время на неё ушло, нулём оно не бывает.
func TestWorkDaysBreaksSessionsByGap(t *testing.T) {
	runs := []time.Time{at(3, 10, 0), at(3, 13, 0)}
	commits := []time.Time{at(3, 10, 5), at(3, 13, 5)}
	if got := dayOf(t, workDays(runs, commits), "2026-08-03"); got != time.Hour {
		t.Fatalf("две короткие сессии дают %v, жду 1h", got)
	}
}

// Ночной прогон делится по дням, а не приписывается одному: ворклог висит на
// календарной дате.
func TestWorkDaysSplitsAtMidnight(t *testing.T) {
	runs := []time.Time{at(3, 23, 30)}
	commits := []time.Time{at(4, 0, 30)}
	days := workDays(runs, commits)
	if len(days) != 2 {
		t.Fatalf("ночная сессия обязана лечь на два дня: %v", days)
	}
	if dayOf(t, days, "2026-08-03") != 30*time.Minute || dayOf(t, days, "2026-08-04") != 30*time.Minute {
		t.Fatalf("половинки ночной сессии: %v", days)
	}
}

// Коммитов с ключом тикета нет, значит и факта нет: журнал запусков сам по себе
// про задачу ничего не знает, привязку даёт коммит.
func TestWorkDaysNeedsCommits(t *testing.T) {
	if days := workDays([]time.Time{at(3, 10, 0), at(3, 11, 0)}, nil); days != nil {
		t.Fatalf("без коммитов фактов нет, вижу %v", days)
	}
}

func gitAt(t *testing.T, dir string, when time.Time, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	stamp := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitRepo заводит репозиторий с коммитами по списку «subject -> время».
func commitRepo(t *testing.T, dir string, commits []struct {
	subject string
	when    time.Time
}) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	corpGitT(t, dir, "init", "-q", "-b", "main")
	corpGitT(t, dir, "config", "user.email", "test@test")
	corpGitT(t, dir, "config", "user.name", "test")
	for i, c := range commits {
		corpWrite(t, filepath.Join(dir, "file.txt"), c.subject)
		corpGitT(t, dir, "add", ".")
		gitAt(t, dir, c.when, "commit", "-q", "-m", c.subject)
		_ = i
	}
}

// Коммиты тикета находятся по ключу в subject: ветка к моменту сдачи бывает уже
// слита, а ключ из subject никуда не девается. Соседний ключ с тем же префиксом
// в счёт не идёт.
func TestCommitTimesFindsByKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	commitRepo(t, dir, []struct {
		subject string
		when    time.Time
	}{
		{"feat: ABC-12 первый шаг", at(3, 11, 30)},
		{"feat: ABC-120 чужая задача", at(3, 12, 0)},
		{"fix: ABC-12 второй шаг", at(4, 10, 0)},
	})
	times, err := commitTimes(dir, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if len(times) != 2 {
		t.Fatalf("жду два коммита тикета, вижу %v", times)
	}
	for _, ts := range times {
		if ts.Equal(at(3, 12, 0)) {
			t.Fatalf("коммит соседнего ключа ABC-120 сошёл за ABC-12: %v", times)
		}
	}
}

// Записи ворклогов лежат в том же журнале, что запуски, и колонок у них больше:
// сводка запусков их не считает командой, а submit по ним видит уже уехавшие
// дни.
func TestWorklogMarksReadBack(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	if err := markWorklog(root, "ABC-12", "2026-08-03", "2h"); err != nil {
		t.Fatal(err)
	}
	if err := markWorklog(root, "ABC-13", "2026-08-04", "1h"); err != nil {
		t.Fatal(err)
	}
	days, err := writtenDays(root, "ABC-12")
	if err != nil {
		t.Fatal(err)
	}
	if !days["2026-08-03"] || days["2026-08-04"] {
		t.Fatalf("дни тикета ABC-12: %v", days)
	}
	runs, err := runTimes(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("запись ворклога сошла за прогон команды: %v", runs)
	}
}

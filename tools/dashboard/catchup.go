package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/gitrun"
)

// Доска проекта на этой машине отстаёт от origin, пока кто-нибудь не вспомнит
// про git pull: правила доски кладут правку в push сразу, и вторая машина над
// той же доской видит чужие строки только после ручного догона (DK-1414, класс
// DK-1069). Догоняет сам демон: фоновый круг по тику из машинного конфига
// делает git fetch и fast-forward основного чекаута каждого проекта. Сеть
// остаётся тут, в долгоживущем демоне дашборда: taskctl зовут не только из
// него, и записанная у утилиты позиция «сеть в чтении доски не ходит» держится
// (tools/taskctl/catchup.go).
//
// Гарды повторяют catchup утилиты: только fast-forward, только чистое дерево,
// только main или master. Грязное дерево, расхождение с origin и незапушенные
// коммиты не паркуются стэшем и не тянутся силой: догон отказывается, а
// витрина держит пометку, почему доска осталась старой. Молчаливая парковка
// чужих файлов уже показала цену (DK-1070).

// catchupDefault это интервал круга догона без ключа catchup_interval. Порядка
// пары минут: доска опаздывает на один круг, зато лёгкий fetch по тику
// незаметен рядом с чтением самой доски.
const catchupDefault = 2 * time.Minute

// catchupEvery отдаёт интервал круга: ключ конфига или умолчание. Кривое
// значение ключа старт уже отверг, поэтому ноль здесь значит только «ключа не
// было».
func (c *Config) catchupEvery() time.Duration {
	if c.CatchupInterval > 0 {
		return c.CatchupInterval
	}
	return catchupDefault
}

// boardCatchupKeeper это фоновый круг догона. Первый круг встаёт сразу после
// старта: демона перезапускают выкатом, и доски приезжают свежими к первому
// открытому экрану. Дальше круг ходит интервалом с джиттером в четверть
// интервала: машины с дашбордами не синхронизируются на одном мгновении, и
// fetch не собирается по всем проектам разом.
func (s *server) boardCatchupKeeper(stop <-chan struct{}) {
	for {
		s.catchupRound()
		every := s.cfg.catchupEvery()
		wait := every
		if jitter := every / 4; jitter > 0 {
			wait += time.Duration(rand.Int64N(int64(jitter)))
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// catchupRound обходит проекты догона разом, а не по очереди: fetch чужого
// origin это сеть, и складывать сроки проектов на одном круге незачем. Потолок
// стоит тот же, что у опроса проектов: круг догона не поднимает больше git,
// чем поднимает экран.
func (s *server) catchupRound() {
	projects, _ := s.projects()
	broken := inParallel(projectWorkers, len(projects), func(i int) { s.catchupOne(projects[i]) })
	for i, err := range broken {
		if err != nil {
			s.logf("догон доски %s: %s", projects[i].Name,
				s.notePanic("догон доски "+projects[i].Path, err))
		}
	}
}

// catchupOne догоняет одно дерево и держит пометку витрины. Журнал пишется
// сменой полосы, а не каждым кругом: отказ живёт минутами, и строка на полосу,
// как у noteLag. Удачный догон пишется всегда: событие редкое, и строки в
// журнале оно не повторяет.
func (s *server) catchupOne(p Project) {
	note, said := treeCatchup(p.Path)
	s.mu.Lock()
	s.catchup[p.Path] = note
	fresh := said != s.catchupSaid[p.Path]
	s.catchupSaid[p.Path] = said
	s.mu.Unlock()
	if said != "" && fresh {
		s.logf("догон доски %s: %s", p.Name, said)
	}
}

// boardLagNote отдаёт пометку догона для витрины: пустая строка значит, что
// доска свежая или догонять её нечего.
func (s *server) boardLagNote(dir string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catchup[dir]
}

// treeCatchup делает один догон дерева и отдаёт пометку витрины со строкой
// журнала. Пустая пометка значит «свежо», пустая строка журнала значит «сказать
// нечего». Дерево без удалённика, на чужой ветке или без ветки на origin
// догоном не занимается: не его дело, и пометка тут была бы шумом.
func treeCatchup(dir string) (note, said string) {
	if remotes, err := gitLine(dir, "remote"); err != nil || remotes == "" {
		return "", ""
	}
	branch := gitHeadBranch(dir)
	if branch != "main" && branch != "master" {
		return "", ""
	}
	if _, err := runProcEnv(remoteGitEnv(dir), "", "git", "-C", dir, "fetch"); err != nil {
		note = fmt.Sprintf("доска не догнана: git fetch не прошёл: %s", oneLine(procErr(err)))
		return note, note
	}
	ref := "origin/" + branch
	if _, err := gitLine(dir, "rev-parse", "--verify", "--quiet", ref); err != nil {
		return "", ""
	}
	behind, ahead := gitAheadBehind(dir, ref)
	if behind < 0 || ahead < 0 {
		return "", ""
	}
	if ahead > 0 {
		note = fmt.Sprintf("%s разошёлся с %s: локальных коммитов %d, чужих %d, догон отказан",
			branch, ref, ahead, behind)
		return note, note
	}
	if behind == 0 {
		return "", ""
	}
	if gitTreeBusy(dir) {
		note = fmt.Sprintf("доска позади %s на %d %s, догон стоит: в дереве идёт операция git",
			ref, behind, pluralCommits(behind))
		return note, note
	}
	if !gitTreeClean(dir) {
		note = fmt.Sprintf("доска позади %s на %d %s, догон стоит: дерево не чистое",
			ref, behind, pluralCommits(behind))
		return note, note
	}
	if _, err := runProc("git", "-C", dir, "merge", "--ff-only", ref); err != nil {
		note = fmt.Sprintf("доска позади %s на %d %s, догон не прошёл: %s",
			ref, behind, pluralCommits(behind), oneLine(procErr(err)))
		return note, note
	}
	sha, _ := gitLine(dir, "rev-parse", "--short", "HEAD")
	return "", fmt.Sprintf("догнано до %s, приехало %d %s", sha, behind, pluralCommits(behind))
}

// remoteGitEnv собирает окружение сетевого вызова git по дереву. Гигиену учётки
// даёт gitrun: пакетный ssh и снятые помощники ввода пароля, без них безлюдный
// демон вставал бы на вопросе, на который отвечать некому. А ssh берётся из
// конфига репозитория: GIT_SSH_COMMAND глушит core.sshCommand, и репозиторий с
// отдельным ключом и known_hosts молча подменялся голым ssh, который на чужом
// origin отвечает «Host key verification failed» вместо догона. Конфиг
// репозитория важнее случайной переменной окружения демона: ключ прописывают
// репозиторию. Сам вызов идёт через runProcEnv со сроком procTimeout: пакетный
// режим не спрашивает человека, а подвисший канал снимает срок.
func remoteGitEnv(dir string) []string {
	env := gitrun.Env([]string{"fetch"})
	ssh := gitConf(dir, "core.sshCommand")
	if ssh == "" {
		return env
	}
	line := "GIT_SSH_COMMAND=" + ssh + " -o BatchMode=yes"
	for i, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			env[i] = line
			return env
		}
	}
	return append(env, line)
}

// gitConf читает строку конфига git дерева, пустая строка значит «не задано».
func gitConf(dir, key string) string {
	out, err := runProc("git", "-C", dir, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitHeadBranch называет ветку дерева, пустая строка значит detached HEAD.
func gitHeadBranch(dir string) string {
	out, err := gitLine(dir, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// gitAheadBehind считает, на сколько HEAD позади ref и на сколько впереди него.
// Отрицательное число значит, что git не ответил: догон в этом состоянии
// молчит, а не гадает.
func gitAheadBehind(dir, ref string) (behind, ahead int) {
	count := func(rng string) int {
		out, err := gitLine(dir, "rev-list", "--count", rng)
		if err != nil {
			return -1
		}
		n, err := strconv.Atoi(out)
		if err != nil || n < 0 {
			return -1
		}
		return n
	}
	return count("HEAD.." + ref), count(ref + "..HEAD")
}

// gitTreeClean требует чистого дерева по закоммиченным файлам, как гарды
// catchup утилиты (tools/taskctl/catchup.go). Непрочитанные файлы грязью не
// считаются: fast-forward их не трогает, а коллизию с входящим файлом git
// отказывает сам.
func gitTreeClean(dir string) bool {
	out, err := gitLine(dir, "status", "--porcelain", "-uno")
	return err == nil && out == ""
}

// gitTreeBusy замечает идущую операцию git: чистое дерево посреди rebase,
// merge или bisect выглядит «позади origin» так же, как отставшее, и двигать
// файлы на середине операции нельзя.
func gitTreeBusy(dir string) bool {
	for _, n := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"} {
		p, err := gitLine(dir, "rev-parse", "--git-path", n)
		if err != nil || p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			return true
		}
	}
	return false
}

// oneLine сжимает вывод ошибки в одну строку: пометка витрины это строка, и
// многострочный stderr git расползался бы в ней на весь экран.
func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// pluralCommits склоняет «коммит» по числу.
func pluralCommits(n int) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return "коммитов"
	}
	switch n % 10 {
	case 1:
		return "коммит"
	case 2, 3, 4:
		return "коммита"
	default:
		return "коммитов"
	}
}

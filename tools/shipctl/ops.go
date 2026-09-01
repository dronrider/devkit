package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/dronrider/devkit/internal/frame"
	"github.com/dronrider/devkit/internal/gitrun"
	"github.com/dronrider/devkit/internal/freshtree"
)

// git это единственная дорога к git из shipctl: закрытый запрос учётки и
// предел времени на разговоре с remote живут в общем пакете (DK-697), и второй
// копии списка переменных тут заводить незачем.
func git(root string, args ...string) (string, error) {
	limit, err := gitrun.Timeout()
	if err != nil {
		return "", err
	}
	return gitrun.Run(root, args, limit)
}

// runShell выполняет команду теста или выката: они приходят строкой из флага
// и могут содержать пайпы, поэтому sh -c, а не argv. Без предела времени.
func runShell(root, cmdStr string) (string, error) {
	out, _, err := runShellLimit(root, cmdStr, 0, nil)
	return out, err
}

// runShellLimit это тот же запуск, но с пределом времени: за пределом команда
// убивается, второе значение говорит, что кончилось именно ожидание, а не
// команда. Убивается вся группа процессов, а не один sh: выкат обычно зовёт
// сборку или ssh, и смерть оболочки оставила бы потомков висеть дальше
// (инцидент DK-153: сборка ждала неподнятый демон Docker). Нулевой limit это
// прежнее поведение без предела, по нему гоняются тесты: их ограничивает
// собственный таймаут прогона, и предел выката им не указ. Нулевой env это
// наследование окружения сессии, им живёт выкат; тесты слияния приходят сюда
// с собранным окружением, где живые HOME и PATH внутрь не протекают.
func runShellLimit(root, cmdStr string, limit time.Duration, env []string) (string, bool, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = root
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Буфер читается только после Wait: до него вывод в него льют горутины
	// exec.Cmd, и чтение вперёд Wait это гонка, а не просто неполный вывод.
	if limit <= 0 {
		err := <-done
		return buf.String(), false, err
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		return buf.String(), false, err
	case <-timer.C:
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return buf.String(), true, fmt.Errorf("команда не кончилась за %s и убита", limit)
	}
}

// mergeTestEnv и freshTestTree это кирпичи чистого прогона (internal/freshtree):
// временное дерево на нужном коммите и окружение без следов дома сессии. За
// ними пришёл второй читатель (обкатка сценария в taskctl), поэтому разбор
// живёт в общем пакете, а здесь остались имена, под которыми его знает слияние.
func mergeTestEnv(home, tmpHome string) []string { return freshtree.Env(home, tmpHome) }

func trimHomePath(path, home string) string { return freshtree.TrimHomePath(path, home) }

func freshTestTree(root, sha string) (tree, home string, cleanup func(), err error) {
	return freshtree.Make(root, sha, "shipctl-merge-")
}

// deployProblem говорит, что стряслось с выкатом: короткое для заголовка
// уведомления, развёрнутое для текста ошибки. Вставшая команда называется
// целиком вместе со временем ожидания и ключом, которым предел двигают: без
// этого по одному хвосту вывода (а его у вставшей команды обычно нет) понять
// нечего.
func deployProblem(cmdStr string, timedOut bool, limit time.Duration) (short, full string) {
	if !timedOut {
		return "упал", "упал"
	}
	short = fmt.Sprintf("встал и убит по пределу %s", limit)
	return short, fmt.Sprintf("%s: команда `%s` не кончилась за %s; предел двигается ключом deploy_timeout в %s",
		short, cmdStr, limit, deployConfigPath)
}

// cmdoutFrame строит выжимку вывода провалившейся команды для ошибок shipctl.
// Это замена бывшей tail(out): на месте последних 30 строк без контекста
// агенту видна сводка по формату LLD (exit, lines_total, lines_hidden,
// significant, tail, path), и полный вывод лежит в файле по path. cmdName идёт
// в slug файла вывода, выше порога значимые строки с маркерами (FAIL, panic:,
// CONFLICT) видны агенту даже из середины вывода, а не только из хвоста. dir это
// каталог, из которого звалась команда: по нему ищется git-корень для файла
// полного вывода, и пустой dir не ломает построение сводки, просто оставляя
// path пустым. Exit код у провалившейся команды ненулевой, но он не всегда
// известен вызывающему, поэтому здесь он зафиксирован как 1: единственное, что
// читает агент в этом поле, «команда провалилась», и обманывать его кодом 0
// нельзя. По DK-266 выжимка въезжает на месте тринадцати tail(out) в этом файле
// и соседних вызовов в worktree.go и editor.go.
func cmdoutFrame(dir, cmdName, out string) string {
	s := frame.Summarize(dir, []string{cmdName}, []byte(out), 1)
	return s.Render()
}

func mainBranch(root string) (string, error) {
	for _, b := range []string{"main", "master"} {
		if _, err := git(root, "rev-parse", "--verify", b); err == nil {
			return b, nil
		}
	}
	return "", fmt.Errorf("не нашёл ветку main или master")
}

// preflight проверяет то, без чего нельзя начинать ни merge, ни revert:
// доску двигает taskctl, дерево должно быть чистым (untracked не мешают).
func preflight(root string) (string, error) {
	if _, err := exec.LookPath("taskctl"); err != nil {
		return "", fmt.Errorf("taskctl не найден в PATH, доску двигает он: поставить набор утилит devkit (python3 ~/projects/devkit/tools/devkitctl/devkitctl.py update)")
	}
	main, err := mainBranch(root)
	if err != nil {
		return "", err
	}
	st, err := git(root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	if st != "" {
		return "", fmt.Errorf("в рабочем дереве незакоммиченное, сначала закоммить:\n%s", cmdoutFrame(root, "git-status", st))
	}
	return main, nil
}

func taskMove(root, id, target string) (string, error) {
	out, err := exec.Command("taskctl", "-C", root, "move", id, target).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("taskctl move: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// taskFailClear гасит признак провала проверки. Доску правит taskctl, как и
// статус: shipctl только зовёт его и коммитит результат.
func taskFailClear(root, id string) (string, error) {
	out, err := exec.Command("taskctl", "-C", root, "fail", id, "--clear").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("taskctl fail --clear: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// drainOr решает исход одного из четырёх чистых отказов cmdShip под --drain:
// без флага err уходит как обычная ошибка, под флагом печатается «разлив не
// нужен: <причина>» и ship выходит нулём (LLD DK-306, решение 2). Чистые
// отказы это пустой поезд, занятая очередь, сломанный прод и занятый чужим
// заходом конвейер. Аномальные отказы замка (не открылся, не устоялся) в их
// числе не являются: из отказов замка под флагом глушится только занятость.
func drainOr(drain bool, err error) (string, error) {
	if drain {
		return "разлив не нужен: " + err.Error(), nil
	}
	return "", err
}

// taskFailSet ставит признак провала на строку id тем же путём, что taskctl
// fail: shipctl только зовёт команду и коммитит результат. Notify уходит
// изнутри cmdFail, отдельно его звать не нужно (LLD DK-306, решение 2).
func taskFailSet(root, id, reason string, push bool) (string, error) {
	args := []string{"-C", root, "fail", id, "--reason", reason, "-m",
		fmt.Sprintf("docs(tasks): %s провал проверки, разлив упал", id)}
	if push {
		args = append(args, "--push")
	}
	out, err := exec.Command("taskctl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("taskctl fail: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// failNote строит приписку к ошибке провала деплоя под --drain: провал
// постановки признака не должен прятать сам провал деплоя, поэтому идёт
// строкой-припиской, тем же приёмом, что notify() в notify.go.
func failNote(root, id, reason string, push bool) string {
	out, err := taskFailSet(root, id, reason, push)
	if err != nil {
		return "\nпризнак провала не поставлен: " + err.Error()
	}
	return "\n" + out
}

// commitBoard коммитит доску вместе с файлами задач, которых коснулся перевод
// статуса. Файл задачи тут не для красоты: на смене статуса taskctl уносит в
// раздел «Ход работы» пакет отмеченных этапов (DK-338), и оставленная
// незакоммиченной правка отбила бы следующий же merge своим же предполётом,
// ровно тем случаем, из-за которого этапы и уехали из рабочего дерева.
func commitBoard(root, msg string, ids ...string) (string, error) {
	paths := []string{"docs/TASKS.md"}
	for _, id := range ids {
		rel := "docs/tasks/" + id + ".md"
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			paths = append(paths, rel)
		}
	}
	if _, err := git(root, append([]string{"add", "--"}, paths...)...); err != nil {
		return "", err
	}
	if _, err := git(root, append([]string{"commit", "-m", msg, "--"}, paths...)...); err != nil {
		return "", err
	}
	return git(root, "rev-parse", "--short", "HEAD")
}

func cmdStatus(root string) (string, error) {
	// Доска и очередь считаются по основному дереву, из worktree тот же ответ.
	root, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	branch, err := corpWorkBranch(root)
	if err != nil {
		return "", err
	}
	var out []string
	out = append(out, "ветка: "+branch)
	_, linked, err := worktrees(root)
	if err != nil {
		linked = nil
	}
	for _, l := range linked {
		// Копия окна называется своим именем: она стоит в списке всегда, в том
		// числе отцепленной между задачами, и строка «worktree:  в ...» с
		// пустой веткой читалась бы поломкой.
		what, state := "worktree", l.Branch
		if samePath(l.Path, windowTree(root)) {
			what = "копия окна"
		}
		if state == "" {
			state = "detached, задачи в работе нет"
		}
		out = append(out, what+": "+state+" в "+l.Path)
	}
	for _, s := range []struct{ key, name string }{
		{"in-progress", "In progress"}, {"check", "Check"}, {"blocked", "Blocked"},
	} {
		if rows := b.sects[s.key]; len(rows) == 0 {
			out = append(out, s.name+": пусто")
		} else {
			var items []string
			for _, r := range rows {
				items = append(items, r.ID+" "+r.Title)
			}
			out = append(out, s.name+": "+strings.Join(items, "; "))
		}
	}
	out = append(out, fmt.Sprintf("Backlog: %d задач(и)", len(b.sects["backlog"])))
	// Оборванный файл задачи виден до слияния, а не только в отказе merge:
	// очередь и состав поезда считаются по записи «Выкат», и за обрывом она
	// не читается. У задачи в работе файл живёт на её ветке, в дереве задачи,
	// и читать его надо там же, где потом прочитает merge: основной чекаут
	// стоит на main и этих правок не видит.
	for _, key := range []string{"in-progress", "check", "blocked", "backlog"} {
		for _, r := range b.sects[key] {
			docRoot, where := root, ""
			for _, l := range linked {
				if branchOfTask(l.Branch, r.ID) {
					docRoot, where = l.Path, " (дерево задачи "+l.Path+")"
					break
				}
			}
			if at := cutTaskFile(docRoot, r.ID); at > 0 {
				out = append(out, "предупреждение: "+cutTaskFileNote(r.ID, at)+where+
					"; очередь и состав поезда считаются без записи этой задачи, merge по ней откажет")
			}
		}
	}
	// Корп-контур: слияние и выкат ведёт MR-флоу компании, у shipctl нет ни
	// main, который он двигает, ни прода, который он катит, поэтому очередь,
	// поезд и решение по деплою здесь не считаются вовсе, а не молчат.
	// Check в этом контуре значит «мяч на чужой стороне», и строку двигает
	// pull-синхронизация трекера (trackctl sync, DK-084), а не shipctl ship.
	if corpActive(root) {
		out = append(out, "корп-контур: слияние и выкат ведёт MR-флоу компании, shipctl очередь и поезд не считает; Check означает «мяч на чужой стороне» (MR открыт, тикет в ревью или тестировании), строку двигает trackctl sync")
		return strings.Join(out, "\n"), nil
	}
	var train []string
	var strays []stray
	var back []string
	var smoked []string
	// Без main очередь по коммитам не посчитать, тогда держит вся секция.
	busy := make([]string, 0, len(b.sects["check"]))
	for _, r := range b.sects["check"] {
		busy = append(busy, r.ID)
	}
	if main, err := mainBranch(root); err == nil {
		if train, strays, err = trainTasks(root, main, b); err != nil {
			return "", err
		}
		if busy, smoked, err = checkQueueParts(root, main, b); err != nil {
			return "", err
		}
		if back, err = returned(root, main, b); err != nil {
			return "", err
		}
	}
	if len(train) > 0 {
		out = append(out, "поезд: "+strings.Join(train, ", ")+" слиты и ждут выката (shipctl ship)")
	}
	if len(strays) > 0 {
		out = append(out, "аномалия: код в окне выката, а задача не в In progress ("+strayList(strays)+"); merge и ship будут отказывать, пока не разобрано")
	}
	// Задача ушла из Check, а её выкат остался на проде: строка про него
	// печатается всегда, и провал от приёмки с замечаниями отличается тут же.
	fails := failedChecks(b)
	failed := map[string]bool{}
	for _, f := range fails {
		failed[f.ID] = true
		out = append(out, brokenProd(f)+"; merge и ship отказывают, пока признак не погашен")
	}
	var soft []string
	for _, id := range back {
		if !failed[id] {
			soft = append(soft, id)
		}
	}
	if len(soft) > 0 {
		out = append(out, "выкат на проде за ушедшими из Check: "+strings.Join(soft, ", ")+
			" (проверка принята с замечаниями, доработка идёт кругом; очередь не держат)")
	}
	switch {
	case len(fails) > 0:
		// вердикта нет: прод сломан, и «очередь свободна» рядом с этим врало бы.
	case len(busy) > 0:
		out = append(out, fmt.Sprintf("очередь занята: %s в Check с выкатом без отметки smoke, сначала прогон агентской части сценария и shipctl smoke %s либо проверка и taskctl close", strings.Join(busy, ", "), busy[0]))
	case len(strays) > 0:
		// вердикт не печатается: строка аномалии уже сказала, что merge и
		// ship будут отказывать, «очередь свободна» рядом с ней врала бы.
	case len(b.sects["check"]) > 0:
		if len(smoked) > 0 {
			free := "очередь свободна: smoke прогнан за " + strings.Join(smoked, ", ") +
				", выкат очередь не держит"
			if len(b.sects["check"]) > len(smoked) {
				free += "; остальные в Check без выкаченного кода"
			}
			out = append(out, free)
		} else {
			out = append(out, "очередь свободна: в Check только задачи без выкаченного кода, подтверждение за пользователем, но выкат они не держат")
		}
	default:
		out = append(out, "очередь свободна, сливать и выкатывать можно")
	}
	// Кто из Check ждёт человека, называется поимённо и с видом приёмки: до
	// DK-516 status говорил про приёмку глазами одной фразой на всех, и по
	// ней нельзя было отличить строку, за которой стоит пользователь, от
	// строки, которую доведёт до Done тик сторожка.
	waiting, agentSmoked, agentStuck, agentRest := checkParts(root, b, smoked)
	if len(waiting) > 0 {
		line := "ждут человека в Check: " + strings.Join(waiting, ", ") + ", приёмка глазами и закрытие за пользователем"
		if len(agentSmoked) > 0 {
			line += "; агентские " + strings.Join(agentSmoked, ", ") + " закроет тик devkitctl watch"
		}
		out = append(out, line)
	} else if len(agentSmoked) > 0 {
		out = append(out, "человека в Check никто не ждёт: агентские "+strings.Join(agentSmoked, ", ")+" закроет тик devkitctl watch")
	}

	// Решение по выкату берётся из resolveDeploy, а не из сырого конфига:
	// иначе status и merge разъезжаются, как только логика меняется.
	plan, err := resolveDeploy(root, "")
	if err != nil {
		return "", err
	}
	if plan.warn != "" {
		out = append(out, "предупреждение: "+plan.warn)
	}
	switch {
	case plan.run != "":
		out = append(out, "выкат: автономный (autonomous=true), команда из "+deployConfigPath+", merge катит и пушит сам")
	case plan.manual != "":
		out = append(out, "выкат: за пользователем (autonomous=false), команда есть в "+deployConfigPath)
	case plan.autonomous:
		out = append(out, "выкат: по плейбуку проекта, команды нет в "+deployConfigPath+", но merge автономный и пушит сам")
	default:
		out = append(out, "выкат: команды нет в "+deployConfigPath+", остаётся за пользователем")
	}
	// Последней строкой идёт следующий шаг конвейера: status гоняют ровно
	// перед решением «сливать или ждать», и ответ на этот вопрос должен лежать
	// в выводе, а не собираться в голове из веток и очереди.
	st := pipelineState{train: train, autonomous: plan.autonomous}
	for _, r := range b.sects["in-progress"] {
		st.inProgress = append(st.inProgress, r.ID)
	}
	for _, r := range b.sects["check"] {
		st.check = append(st.check, r.ID)
	}
	st.waiting, st.agentSmoked, st.agentStuck, st.agentRest = waiting, agentSmoked, agentStuck, agentRest
	for _, f := range fails {
		st.failed = append(st.failed, f.ID)
	}
	out = append(out, nextStep(st))
	return strings.Join(out, "\n"), nil
}

// deployTag отмечает точку последнего выката на main. Состав поезда (слито,
// но не выкачено) вычисляется из git по окну deployTag..main, а не хранится
// на доске: git переживает клоны и вторую машину, и доске не нужна ещё одна
// пометка. Тег двигает cmdShip; пока тега нет, поезд не заводился.
const deployTag = "deployed"

func hasTag(root string) bool {
	_, err := git(root, "rev-parse", "--verify", deployTag)
	return err == nil
}

// pushTag отправляет тег точки выката вместе с обычным пушем: без него вторая
// машина считает уже выкаченное всё ещё стоящим в поезде. -f, потому что тег
// двигается с каждым выкатом. Без origin и без тега пушить нечего.
func pushTag(root string) error {
	if !hasTag(root) {
		return nil
	}
	if _, err := git(root, "config", "--get", "remote.origin.url"); err != nil {
		return nil
	}
	if out, err := git(root, "push", "-f", "origin", deployTag); err != nil {
		return fmt.Errorf("тег %s не запушен (%s), повторить: git push -f origin %s", deployTag, out, deployTag)
	}
	return nil
}

// isRevertSubject распознаёт коммит-откат: штатное «revert: ...» либо своё
// сообщение со словом «откат» (конвенция для проектов с белым списком
// префиксов, см. README). Слово ищется целиком, иначе фичекоммит про
// «откатить настройки» тихо выкидывал бы задачу из поезда.
func isRevertSubject(subj string) bool {
	low := strings.ToLower(subj)
	if strings.HasPrefix(low, "revert") {
		return true
	}
	for _, w := range strings.FieldsFunc(low, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if w == "откат" {
			return true
		}
	}
	return false
}

// stray это осиротевшая задача: код в окне выката есть, а строка ушла из
// In progress (руками в Blocked, обратно в Backlog, в Check мимо ship).
type stray struct{ ID, Sect string }

func strayList(ss []stray) string {
	var parts []string
	for _, s := range ss {
		parts = append(parts, s.ID+" в "+s.Sect)
	}
	return strings.Join(parts, ", ")
}

// codeCommits проверяет по строкам лога (формат %H\t%s, новые первыми), есть
// ли у задачи коммиты кода: записанные разделом «Выкат» либо с её ID первым в
// subject, трогающие что-то кроме docs/.
// Правки только под docs/ (доска, файлы задач, LLD) едут с выкатом, но прод
// не меняют, поэтому кодом не считаются. Прошлый откат это граница, как в
// taskCommits: всё, что старше него, уже откачено или относится к прошлым
// заходам на задачу.
func codeCommits(root string, lines []string, id string, rec []string) (bool, error) {
	for _, ln := range lines {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok || (!ownsSubject(subj, id) && !inRecord(rec, sha)) {
			continue
		}
		if isRevertSubject(subj) {
			return false, nil
		}
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return false, err
		}
		if docsOnly(files) {
			continue
		}
		return true, nil
	}
	return false, nil
}

// trainTasks собирает поезд: задачи из In progress, чьи коммиты кода слиты в
// main после точки последнего выката. Вторым списком приходят осиротевшие
// задачи, их вызывающие не везут на прод молча, а поднимают как ошибку.
func trainTasks(root, main string, b *board) (train []string, strays []stray, err error) {
	if !hasTag(root) {
		return nil, nil, nil
	}
	log, err := git(root, "log", deployTag+".."+main, "--format=%H%x09%s")
	if err != nil || log == "" {
		return nil, nil, err
	}
	lines := strings.Split(log, "\n") // новые первыми
	for _, sect := range []string{"in-progress", "check", "blocked", "backlog"} {
		for _, r := range b.sects[sect] {
			rec, err := mergedShas(root, r.ID)
			if err != nil {
				return nil, nil, err
			}
			ok, err := codeCommits(root, lines, r.ID, rec)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			if sect == "in-progress" {
				train = append(train, r.ID)
			} else {
				strays = append(strays, stray{r.ID, sect})
			}
		}
	}
	return train, strays, nil
}

// checkQueue возвращает задачи из Check, держащие очередь выката: те, у кого
// есть выкаченный код (коммиты под точкой последнего выката; пока тега нет,
// весь main) и при этом нет отметки прогона smoke. Двухступенчатый Check
// (LLD DK-400, решение 7): непроверенным считается выкат без прогнанного
// smoke, а не незакрытая задача, поэтому выкат с отметкой очередь не держит
// и приёмка глазами ждёт в Check сколько нужно. LLD, дока и прочие задачи
// без кода на проде ждут подтверждения пользователя, но следующему выкату
// не мешают: инвариант «непроверенный выкат один» про прод, а не про секцию
// доски. Задачу, слитую через shipctl, поиск находит по записи в файле
// задачи, а по ID в subject ищутся только слитые руками мимо него и до
// появления записи.
func checkQueue(root, main string, b *board) ([]string, error) {
	hold, _, err := checkQueueParts(root, main, b)
	return hold, err
}

// checkQueueParts делит выкаченные задачи из Check на держащих очередь (без
// отметки smoke) и освободивших её (smoke прогнан): merge и ship судят по
// первым, status называет вторых, чтобы висящая в Check строка с прогнанным
// smoke не выглядела забытой.
func checkQueueParts(root, main string, b *board) (hold, smoked []string, err error) {
	deployed, err := deployedIn(root, main, b, "check")
	if err != nil {
		return nil, nil, err
	}
	for _, id := range deployed {
		done, err := smokeDone(root, id)
		if err != nil {
			return nil, nil, err
		}
		if done {
			smoked = append(smoked, id)
		} else {
			hold = append(hold, id)
		}
	}
	return hold, smoked, nil
}

// returned возвращает задачи, ушедшие из Check с уже выкаченным кодом: строка
// вернулась в работу, а правка осталась на проде. Штатно это приёмка с
// замечаниями, очередь такая задача не держит, но молчать про висящий на
// проде выкат нельзя: по строке видно, чей код там стоит и с кого спрашивать,
// если после следующего выката что-то поедет.
func returned(root, main string, b *board) ([]string, error) {
	return deployedIn(root, main, b, "in-progress", "blocked")
}

// deployedIn отбирает из перечисленных секций задачи с выкаченным кодом.
func deployedIn(root, main string, b *board, sects ...string) ([]string, error) {
	var rows []row
	for _, s := range sects {
		rows = append(rows, b.sects[s]...)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ref := main
	if hasTag(root) {
		ref = deployTag
	}
	log, err := git(root, "log", ref, "--format=%H%x09%s")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(log, "\n")
	var busy []string
	for _, r := range rows {
		rec, err := mergedShas(root, r.ID)
		if err != nil {
			return nil, err
		}
		ok, err := codeCommits(root, lines, r.ID, rec)
		if err != nil {
			return nil, err
		}
		if ok {
			busy = append(busy, r.ID)
		}
	}
	return busy, nil
}

type MergeParams struct {
	ID     string
	Test   string // команда тестов, обязательна
	Deploy string // явная команда выката; пустую подхватывает .devkit/deploy.local
	Train  bool   // слить в поезд: без выката и без перевода в Check
	Push   bool
}

func cmdMerge(root string, p MergeParams) (string, error) {
	if p.Train && p.Deploy != "" {
		return "", fmt.Errorf("--train откладывает выкат до shipctl ship, вместе с --deploy он не имеет смысла")
	}
	primary, fromWT, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	root = primary
	if corpActive(root) {
		return "", corpRefused("merge")
	}
	unlock, err := acquireLock(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	// Перевод в поездной режим при занятой очереди помнит, откуда он: отчёт
	// обязан объяснить отказ от выката, иначе тихое отсутствие выката
	// неотличимо от забытья.
	queueFallback := ""
	test, testFromConfig, err := resolveTest(root, p.Test)
	if err != nil {
		return "", err
	}
	if test == "" {
		return "", fmt.Errorf("нужен --test с командой тестов проекта либо ключ test в %s: ветка сливается только зелёной", deployConfigPath)
	}
	main, err := preflight(root)
	if err != nil {
		return "", err
	}
	branch, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	// Ветка задачи бывает в трёх местах: чекаут основного дерева (запуск с
	// фичеветки, как раньше), worktree запуска и worktree, найденный по ID
	// (запуск с main при работе через shipctl start). Дальше wt != "" значит
	// «ветка в отдельном дереве»: ребейз и тесты идут там, слияние в основном.
	wt := ""
	switch {
	case fromWT != "":
		wt = fromWT
		if branch, err = git(wt, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
			return "", err
		}
		// Ветка дерева обязана быть веткой переданной задачи: запуск merge
		// одной задачи из worktree другой слил бы чужую ветку, а в Check
		// перевёл свою. Заодно отсекается detached HEAD (branch тогда «HEAD»).
		if !branchOfTask(branch, p.ID) {
			return "", fmt.Errorf("в worktree %s стоит ветка %s, а сливается %s: запускать merge из дерева задачи или из основного чекаута", wt, branch, p.ID)
		}
	case branch == main:
		l, err := taskWorktree(root, p.ID)
		if err != nil {
			return "", err
		}
		if l == nil {
			// Ветка задачи бывает и не выложена ни в одно дерево: копию окна
			// переключили на другую задачу, а эту оставили дозревать. Сказать
			// про это стоит прямо, иначе «сливать нечего» читается как
			// «работы не было».
			hint := ""
			if names, err := git(root, "branch", "--list", "--format=%(refname:short)"); err == nil {
				for _, n := range strings.Split(names, "\n") {
					if branchOfTask(n, p.ID) {
						hint = fmt.Sprintf("; ветка %s есть, но не выложена ни в одно дерево: вернуть её в копию окна (shipctl code %s) и повторить", n, p.ID)
						break
					}
				}
			}
			return "", fmt.Errorf("стоишь на %s, и worktree с веткой %s не нашёлся: сливать нечего%s", main, p.ID, hint)
		}
		wt, branch = l.Path, l.Branch
	}
	if wt != "" {
		if branch == main {
			return "", fmt.Errorf("в worktree %s стоит %s, сливать нечего: merge запускается для фичеветки", wt, main)
		}
		// Fast-forward делается в основном дереве, поэтому оно обязано стоять
		// на main; ветку в нём двигать нельзя, она занята в worktree.
		cur, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return "", err
		}
		if cur != main {
			return "", fmt.Errorf("ветка задачи в worktree %s, а основной чекаут стоит на %s: перевести его на %s и повторить", wt, cur, main)
		}
		st, err := git(wt, "status", "--porcelain", "--untracked-files=no")
		if err != nil {
			return "", err
		}
		if st != "" {
			return "", fmt.Errorf("в worktree %s незакоммиченное, сначала закоммить:\n%s", wt, cmdoutFrame(wt, "git-status", st))
		}
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	switch sect := b.sectOf(p.ID); sect {
	case "in-progress":
	case "":
		return "", fmt.Errorf("%s нет на доске", p.ID)
	default:
		return "", fmt.Errorf("%s в %s, сливается только задача из In progress", p.ID, sect)
	}
	if wt == "" && !branchOfTask(branch, p.ID) {
		// Та же защита, что у worktree: с чужой фичеветки merge слил бы её под
		// ID переданной задачи. Ветка задачи называется по ID (RULES.board.md).
		return "", fmt.Errorf("стоишь на %s, а сливается %s: перейти на ветку задачи и повторить", branch, p.ID)
	}
	// Провал проверки это сломанный прод, и до починки очередь стоит целиком.
	// Единственный заход, который она пропускает, это починка самой
	// проваленной задачи форвард-фиксом: он и снимет признак переводом в Check.
	// Поезд при сломанном проде не копится, чинят выкатом.
	failReason := failedOf(b, p.ID)
	for _, f := range failedChecks(b) {
		switch {
		case f.ID != p.ID:
			return "", fmt.Errorf("%s; своя задача сольётся, когда прод починен", brokenProd(f))
		case p.Train:
			return "", fmt.Errorf("%s; поезд при сломанном проде не копится, чинить одиночным merge или откатом", brokenProd(f))
		}
	}
	// Занятая очередь держит выкат, а не main: инвариант «непроверенный выкат
	// один» сказан про прод. Слияние на прод ничего не везёт, оно
	// возвращается до блока выката, и очередь его не касается. Одиночный merge
	// выкатывает сам, поэтому при занятой очереди он не отказывает, а
	// переводит себя в поездной режим: ветка льётся в main и ждёт свободной
	// очереди вместе с накопленным поездом (DK-306, решение 3), сессии к тому
	// моменту может уже не быть, и получателя у события освобождения нет.
	if !p.Train {
		if busy, err := checkQueue(root, main, b); err != nil {
			return "", err
		} else if len(busy) > 0 && failReason != "" {
			// Форвард-фикс проваленной задачи чинится выкатом, а выкат
			// очередь не проходит: копить поезд при сломанном проде значит
			// запереть починку навсегда (ship отбит признаком провала,
			// merge отбит занятой очередью), поэтому здесь честный отказ
			// с путём починки, а не молчаливое слияние в поезд.
			return "", fmt.Errorf("очередь занята: %s в Check с выкатом без отметки smoke, а %s провалена и чинится только выкатом форвард-фикса: сначала smoke (shipctl smoke %s) либо проверка занявшей очередь (taskctl close), либо откат своей задачи (shipctl revert %s), поезд при сломанном проде не копится", strings.Join(busy, ", "), p.ID, busy[0], p.ID)
		} else if len(busy) > 0 {
			p.Train = true
			queueFallback = fmt.Sprintf("очередь занята: %s в Check с выкатом без отметки smoke, поэтому слияние поездное: ветка ждёт свободной очереди в main, выкат потом одним деплоем (shipctl ship); отметка прогона сценария (shipctl smoke %s) очередь освобождает\n", strings.Join(busy, ", "), busy[0])
		}
	}
	// Одиночный merge при непустом поезде увёз бы на прод чужие непроверенные
	// правки, а в Check перевёл только свою задачу: инвариант ломается молча.
	train, strays, err := trainTasks(root, main, b)
	if err != nil {
		return "", err
	}
	if len(strays) > 0 {
		return "", fmt.Errorf("код в окне выката, а задача не в In progress: %s; вернуть задачу в In progress или откатить её коммиты, иначе они уедут на прод без Check", strayList(strays))
	}
	if !p.Train && len(train) > 0 {
		return "", fmt.Errorf("в поезде %s, одиночный выкат смешал бы их со своей задачей: либо merge --train, либо сначала shipctl ship", strings.Join(train, ", "))
	}
	// Файл задачи с замечаниями ревью живёт на фичеветке: при слиянии из
	// worktree читать его надо там, основной чекаут на main этих правок не видит.
	reviewRoot := root
	if wt != "" {
		reviewRoot = wt
	}
	// Оборванный файл задачи проверяется до ревью: за незакрытым ограждением
	// не видно ни замечаний, ни записи «Выкат», и слияние прошло бы «чисто»
	// ровно потому, что читать было нечего. Отказ тут ничего не успел
	// поменять, а чинится он одной строкой в файле задачи.
	if at := cutTaskFile(reviewRoot, p.ID); at > 0 {
		return "", fmt.Errorf("%s; закрыть ограждение и повторить, иначе запись слияния уйдёт туда же и очередь её не увидит", cutTaskFileNote(p.ID, at))
	}
	open, err := openReviewNotes(reviewRoot, p.ID)
	if err != nil {
		return "", err
	}
	if len(open) > 0 {
		return "", fmt.Errorf("в ревью %s замечания без исхода, сначала закрыть их в docs/tasks/%s.md:\n%s",
			p.ID, p.ID, strings.Join(open, "\n"))
	}
	// Конфиг выката и свежесть main проверяются до ребейза: ошибка после
	// прогона тестов и слияния стоила бы куда дороже, чем сейчас.
	deploy, err := resolveDeploy(root, p.Deploy)
	if err != nil {
		return "", err
	}
	if err := freshMain(root, main); err != nil {
		return "", err
	}
	// Ворота готовности правки. Это предусловия, а не подсказки: пропуск шага
	// ловится здесь, а не вниманием ревьювера. Каждое гасится либо правкой, либо
	// пометкой-исключением в файле задачи (читается в дереве ветки, как и ревью:
	// писать исключение туда же). Действуют в обоих режимах, одиночном и
	// поездном: они про готовность правки, а не про то, кто жмёт слияние.
	// Дифф ветки против main осмыслен до ребейза, поэтому ворота идут здесь.
	taskDoc := readTaskDoc(reviewRoot, p.ID)
	docsBranch, err := rangeDocsOnly(root, main, branch)
	if err != nil {
		return "", err
	}
	if err := regcheckGate(root, reviewRoot, main, branch, b.rowOf(p.ID).Type, taskDoc); err != nil {
		return "", err
	}
	if err := testsGate(root, main, branch, docsBranch, taskDoc); err != nil {
		return "", err
	}
	if err := scenarioGate(p.ID, docsBranch, taskDoc); err != nil {
		return "", err
	}
	// Предупреждения собираются до ребейза (diff ветки против main ещё
	// осмысленный) и не валят слияние: это подсказки по правилам, а не
	// предусловия.
	var warns []string
	if subjects, err := git(root, "log", main+".."+branch, "--format=%s"); err == nil && !strings.Contains(subjects, p.ID) {
		warns = append(warns, fmt.Sprintf("предупреждение: в коммитах ветки нет %s в subject; очередь, поезд и revert найдут их по записи в файле задачи, но по истории задачу так не собрать", p.ID))
	}
	if p.Train {
		warns = append(warns, trainWarnings(root, main, branch, b, p.ID, train)...)
	}
	warn := ""
	if len(warns) > 0 {
		warn = strings.Join(warns, "\n") + "\n"
	}
	// Поездное слияние возвращается до блока выката, поэтому его
	// предупреждение о конфиге едет здесь, вместе с преребейзными.
	if deploy.warn != "" && p.Train {
		warn += "предупреждение: " + deploy.warn + "\n"
	}
	// Ребейз и тесты идут там, где ветка стоит в чекауте: в worktree задачи
	// либо в основном дереве (старый путь без worktree).
	workDir := root
	if wt != "" {
		workDir = wt
	}
	// Ветка, уже вобравшая main слиянием, в ребейзе не нуждается: fast-forward
	// у неё есть, а ребейз тут не бездействует, он расплющивает сшивку и
	// возвращает разведённые конфликты по одному на коммит. На длинной ветке
	// это отказ навсегда: развести их заново нечем, и слияние упирается
	// намертво (DK-637).
	if _, err := git(workDir, "merge-base", "--is-ancestor", main, "HEAD"); err != nil {
		if out, err := git(workDir, "rebase", main); err != nil {
			git(workDir, "rebase", "--abort")
			return "", fmt.Errorf("ребейз на %s не прошёл, разбирать конфликт руками:\n%s", main, cmdoutFrame(workDir, "git-rebase", out))
		}
	}
	// Тесты идут не в workDir, а в свежем дереве на ребейзнутом коммите и в
	// собранном окружении: прогретый чекаут и живые HOME с PATH прячут
	// дефекты класса «зелено у исполнителя, красно на чужой машине».
	sha, err := git(workDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	testTree, testHome, cleanTree, err := freshTestTree(root, sha)
	if err != nil {
		return "", err
	}
	out, _, err := runShellLimit(testTree, test, 0, mergeTestEnv(os.Getenv("HOME"), testHome))
	cleanTree()
	if err != nil {
		return "", fmt.Errorf("тесты после ребейза красные в свежем дереве (чистый чекаут %s, без следов работы в worktree), ветка остаётся несшитой:\n%s", sha[:min(len(sha), 12)], cmdoutFrame(workDir, "test", out))
	}
	if wt == "" {
		if _, err := git(root, "checkout", main); err != nil {
			return "", err
		}
	}
	preSha, catchUps, err := ffCatchUp(root, workDir, wt, main, branch)
	if err != nil {
		return "", err
	}
	// Слитый диапазон известен ровно здесь, до записи в файл задачи: её коммит
	// лежит под docs/ и признак не смазывает, но считать признак по чистому
	// диапазону ветки честнее.
	docsRange := false
	if p.Train {
		if docsRange, err = rangeDocsOnly(root, preSha, "HEAD"); err != nil {
			return "", err
		}
	}
	var wtNote string
	if wt != "" {
		// Копия окна не сносится: в ней живёт окно редактора, и удалённая
		// директория убивает окно вместе с сессией (DK-192). Дерево на задачу
		// одноразовое, у него хозяин субагент, и уборка там нужное свойство.
		if samePath(wt, windowTree(root)) {
			wtNote = detachWorktree(root, wt, branch)
		} else {
			wtNote = removeWorktree(root, wt, branch)
		}
	} else {
		git(root, "branch", "-d", branch)
	}
	// Слитый диапазон известен ровно здесь и ровно этой задаче: дальше он
	// смешается с чужими коммитами, а связь по ID в subject держится на
	// договорённости и рвётся молча. Запись идёт до пуша, чтобы уехать вместе
	// с кодом, и своим коммитом: сам он под docs/, поэтому ни в поезд, ни в
	// откат не попадает.
	var recNote string
	if shas, err := git(root, "log", preSha+"..HEAD", "--format=%h"); err == nil && shas != "" {
		hash, err := recordMerge(root, p.ID, strings.Split(shas, "\n"))
		if err != nil {
			return "", fmt.Errorf("%s слита в %s, но коммиты не записаны в файл задачи: %v", p.ID, main, err)
		}
		recNote = fmt.Sprintf("коммиты задачи записаны в docs/tasks/%s.md, коммит %s", p.ID, hash)
	}
	// Первое поездное слияние заводит тег на main до себя: что было на main к
	// этому моменту, считается выкаченным, и окно поезда начинается с этой ветки.
	// Бескодовая ветка точкой выката не становится: везти на прод ей нечего, и
	// окно поезда она бы открыла на пустом месте.
	if p.Train && !docsRange {
		if _, err := git(root, "rev-parse", "--verify", deployTag); err != nil {
			if _, err := git(root, "tag", deployTag, preSha); err != nil {
				return "", err
			}
		}
	}
	msg := []string{warn + queueFallback + fmt.Sprintf("%s слита в %s fast-forward", p.ID, main)}
	if testFromConfig {
		msg = append(msg, "тесты гнались командой из "+deployConfigPath+": "+test)
	}
	msg = append(msg, "тесты гнались в свежем дереве на "+sha[:min(len(sha), 12)]+" с временным HOME")
	if catchUps > 0 {
		msg = append(msg, fmt.Sprintf("за время прогона %s уехал на коммиты доски, ff добран повторным ребейзом, заходов %d, тесты не перегонялись", main, catchUps))
	}
	if wtNote != "" {
		msg = append(msg, wtNote)
	} else {
		msg[0] += ", ветка " + branch + " удалена"
	}
	if recNote != "" {
		msg = append(msg, recNote)
	}
	// При autonomous=true пуш это часть автономного конвейера, иначе origin
	// отстал бы от задеплоенного прода, а revert по ID не нашёл бы коммитов.
	// Код пушится до выката: pull-выкат тянет код с origin, и без свежего
	// пуша туда уехала бы прошлая версия. Ошибка пуша называет, что уже
	// сделано: слияние к этому моменту необратимо.
	push := func(note, failed string) error {
		if !p.Push && !deploy.autonomous {
			return nil
		}
		if _, err := git(root, "push"); err != nil {
			return fmt.Errorf("%s: %v", failed, err)
		}
		if err := pushTag(root); err != nil {
			return err
		}
		msg = append(msg, note)
		return nil
	}
	if err := push("код запушен",
		fmt.Sprintf("%s слита в %s, ветка удалена, но пуш не прошёл, выкат не запускался", p.ID, main)); err != nil {
		return "", err
	}
	// Поездное слияние на этом заканчивается: задача остаётся в In progress,
	// доска не трогается, выкат и перевод в Check делает cmdShip на весь поезд.
	// Пересчёт состава здесь чисто информационный, слияние уже необратимо, и
	// его ошибка не должна хоронить отчёт о том, что реально сделано.
	if p.Train {
		// Ветка не тронула ничего вне docs/: везти на прод нечего, очередь такая
		// задача не держит, и ship её не подберёт. Молчащая задача застряла бы в
		// In progress до тех пор, пока кто-нибудь не заметит, поэтому в Check её
		// переводит сам merge, как переводит бескодовую задачу одиночный merge.
		if docsRange {
			if _, err := taskMove(root, p.ID, "check"); err != nil {
				return "", fmt.Errorf("слито, но доска не переведена: %v", err)
			}
			hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s в Check", p.ID), p.ID)
			if err != nil {
				return "", err
			}
			msg = append(msg, "поезд задачу не везёт: ветка не трогает ничего вне docs/, выката ей не нужно",
				fmt.Sprintf("доска: %s в Check, коммит %s", p.ID, hash))
			if err := push("доска запушена",
				"перевод в Check прошёл, но пуш доски не прошёл, повторить git push руками"); err != nil {
				return "", err
			}
			if note := syncWindowTree(root, main); note != "" {
				msg = append(msg, note)
			}
			msg = append(msg, nextAfterMerge(b, []string{p.ID}))
			return strings.Join(msg, "\n"), nil
		}
		if now, _, err := trainTasks(root, main, b); err == nil {
			msg = append(msg, fmt.Sprintf("в поезде: %s; выкат поезда: shipctl ship", strings.Join(now, ", ")))
			// Код в main уехал, а состав задачу не видит: её коммиты не нашлись
			// ни по ID в subject, ни по записи в файле задачи (так бывает, когда
			// верхний коммит ветки распознан как откат). Поезд её не повезёт и
			// ship в Check не переведёт, а правка на main уже лежит и уедет на
			// прод с чужим выкатом.
			if !slices.Contains(now, p.ID) {
				msg = append(msg, fmt.Sprintf("предупреждение: код слит, но %s не попала в поезд (её коммиты не нашлись ни по ID в subject, ни по записи в файле задачи): shipctl ship её не повезёт и в Check не переведёт, разобрать до выката", p.ID))
			}
		} else {
			msg = append(msg, fmt.Sprintf("в поезде (состав не пересчитался: %v); выкат поезда: shipctl ship", err))
		}
		msg = append(msg, "следующий шаг: добрать поезд следующей задачей либо выкатить его (shipctl ship): выкат и перевод строк доски идут на весь состав разом")
		return strings.Join(msg, "\n"), nil
	}
	if deploy.warn != "" && !p.Train {
		msg = append(msg, "предупреждение: "+deploy.warn)
	}
	switch {
	case deploy.run != "":
		if out, timedOut, err := runShellLimit(root, deploy.run, deploy.timeout, nil); err != nil {
			short, full := deployProblem(deploy.run, timedOut, deploy.timeout)
			outSummary := cmdoutFrame(root, "deploy", out)
			note := notify(root, p.ID, fmt.Sprintf("%s: выкат %s %s", filepath.Base(root), p.ID, short), full+"\n"+outSummary)
			return "", fmt.Errorf("слито, но выкат %s, задача остаётся в In progress:\n%s%s", full, outSummary, note)
		}
		msg = append(msg, "выкат прошёл")
	case deploy.manual != "":
		msg = append(msg, "выкат за пользователем ("+deploy.manual+")")
	default:
		msg = append(msg, "выкат за пользователем, по плейбуку проекта")
	}
	// Одиночный выкат это тоже точка последнего выката: если проект уже живёт
	// с поездами (тег есть), тег двигается и здесь, иначе окно поезда тащило
	// бы за собой давно выкаченные и закрытые задачи.
	if hasTag(root) {
		if _, err := git(root, "tag", "-f", deployTag, main); err != nil {
			return "", err
		}
	}
	if _, err := taskMove(root, p.ID, "check"); err != nil {
		return "", fmt.Errorf("слито, но доска не переведена: %v", err)
	}
	hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s в Check", p.ID), p.ID)
	if err != nil {
		return "", err
	}
	msg = append(msg, fmt.Sprintf("доска: %s в Check, коммит %s", p.ID, hash))
	if failReason != "" {
		msg = append(msg, fmt.Sprintf("признак провала (%s) снят переводом в Check, очередь выката свободна", failReason))
	}
	if err := push("доска запушена",
		"выкат и перевод в Check прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	if note := syncWindowTree(root, main); note != "" {
		msg = append(msg, note)
	}
	msg = append(msg, nextAfterMerge(b, []string{p.ID}))
	return strings.Join(msg, "\n"), nil
}

type ShipParams struct {
	Deploy string // явная команда выката; пустую подхватывает .devkit/deploy.local
	Push   bool
	Drain  bool // разлив: чистые отказы молчат нулём вместо ошибки (LLD DK-306)
}

// cmdShip выкатывает поезд: один деплой на все задачи, слитые после точки
// последнего выката, разом переводит их в Check и двигает тег. Проверка идёт
// на одном билде, каждая задача по своему сценарию; провал одной чинится
// точечным revert, остальной поезд остаётся на проде.
//
// Drain это разлив (LLD DK-306, решение 2): close и watch зовут задачу без
// сессии, получателя у ошибки «разливать нечего» нет, и четыре чистых отказа
// (пустой поезд, занятая очередь, сломанный прод, занятый чужим заходом
// конвейер) под --drain выходят нулём с сообщением вместо error. Провал
// деплоя в их число не входит: это не «нечего разливать», а поломка, и
// --drain сам ставит признак провала на первую задачу состава тем же
// суффиксом, что taskctl fail, чтобы следующий разлив
// упёрся в failedChecks и промолчал сам. Собственный notify() тут не зовётся:
// его шлёт taskFailSet через taskctl fail, и звать оба значило бы дублировать
// уведомление на одно и то же событие.
func cmdShip(root string, p ShipParams) (string, error) {
	// Запуск из worktree допустим, но выкат и доска живут в основном дереве.
	root, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	if corpActive(root) {
		return "", corpRefused("ship")
	}
	unlock, err := acquireLock(root)
	if err != nil {
		// Занятость конвейера под --drain это состыковка с чужим заходом
		// (merge, ship, разлив от close), а не поломка: сторожок, чей тик
		// попал в чужое окно, отступает тихо. Аномалии замка глушить нельзя.
		if errors.Is(err, errLockBusy) {
			return drainOr(p.Drain, err)
		}
		return "", err
	}
	defer unlock()
	main, err := preflight(root)
	if err != nil {
		return "", err
	}
	if _, err := git(root, "checkout", main); err != nil {
		return "", err
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	if fs := failedChecks(b); len(fs) > 0 {
		return drainOr(p.Drain, fmt.Errorf("%s; поезд не выкатывается, пока прод сломан", brokenProd(fs[0])))
	}
	busy, err := checkQueue(root, main, b)
	if err != nil {
		return "", err
	}
	if len(busy) > 0 {
		return drainOr(p.Drain, fmt.Errorf("очередь занята: %s в Check с выкатом без отметки smoke; по RULES.board.md непроверенный выкат один, сначала прогон агентской части сценария и shipctl smoke %s либо проверка и taskctl close", strings.Join(busy, ", "), busy[0]))
	}
	train, strays, err := trainTasks(root, main, b)
	if err != nil {
		return "", err
	}
	if len(strays) > 0 {
		return "", fmt.Errorf("код в окне выката, а задача не в In progress: %s; вернуть задачу в In progress или откатить её коммиты, иначе они уедут на прод без Check", strayList(strays))
	}
	if len(train) == 0 {
		return drainOr(p.Drain, fmt.Errorf("поезд пуст: после точки последнего выката нет слитых задач (копит их merge --train)"))
	}
	deploy, err := resolveDeploy(root, p.Deploy)
	if err != nil {
		return "", err
	}
	if err := freshMain(root, main); err != nil {
		return "", err
	}
	list := strings.Join(train, ", ")
	var msg []string
	if deploy.warn != "" {
		msg = append(msg, "предупреждение: "+deploy.warn)
	}
	if len(train) > 5 {
		msg = append(msg, fmt.Sprintf("предупреждение: в поезде %d задач(и), больше 3-5 не копят, регресс без сценария ищется перебором состава", len(train)))
	}
	doPush := p.Push || deploy.autonomous
	push := func(note, failed string) error {
		if !doPush {
			return nil
		}
		if _, err := git(root, "push"); err != nil {
			return fmt.Errorf("%s: %v", failed, err)
		}
		msg = append(msg, note)
		return nil
	}
	if err := push("код запушен", "пуш не прошёл, выкат не запускался"); err != nil {
		return "", err
	}
	switch {
	case deploy.run != "":
		if out, timedOut, err := runShellLimit(root, deploy.run, deploy.timeout, nil); err != nil {
			short, full := deployProblem(deploy.run, timedOut, deploy.timeout)
			outSummary := cmdoutFrame(root, "deploy", out)
			if p.Drain {
				note := failNote(root, train[0], short, doPush)
				return "", fmt.Errorf("выкат поезда %s, задачи остаются в In progress:\n%s%s", full, outSummary, note)
			}
			note := notify(root, train[0], fmt.Sprintf("%s: выкат поезда %s (%s)", filepath.Base(root), short, list), full+"\n"+outSummary)
			return "", fmt.Errorf("выкат поезда %s, задачи остаются в In progress:\n%s%s", full, outSummary, note)
		}
		msg = append(msg, fmt.Sprintf("поезд выкачен (%s)", list))
	case deploy.manual != "":
		msg = append(msg, fmt.Sprintf("поезд собран (%s), выкат за пользователем (%s)", list, deploy.manual))
	default:
		msg = append(msg, fmt.Sprintf("поезд собран (%s), выкат за пользователем, по плейбуку проекта", list))
	}
	// Тег двигается в момент, когда выкат запущен или передан пользователю
	// (поезд с этого места пуст, следующий копится заново), и пушится сразу
	// за сдвигом, до правок доски: упади дальше что угодно, вторая машина уже
	// не посчитает выкаченный поезд стоящим и не выкатит его повторно.
	if _, err := git(root, "tag", "-f", deployTag, main); err != nil {
		return "", err
	}
	if doPush {
		// Провал пуша тега не роняет ship: доску важнее довести до Check, она
		// и прикроет вторую машину (ship там упрётся в занятую очередь), а
		// напоминание про тег уходит в отчёт.
		if err := pushTag(root); err != nil {
			msg = append(msg, err.Error())
		}
	} else {
		msg = append(msg, "тег "+deployTag+" сдвинут локально, при пуше руками добавить: git push -f origin "+deployTag)
	}
	for _, id := range train {
		if _, err := taskMove(root, id, "check"); err != nil {
			return "", fmt.Errorf("выкат прошёл, но доска не переведена: %v", err)
		}
	}
	hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s в Check поездом", list), train...)
	if err != nil {
		return "", err
	}
	msg = append(msg, fmt.Sprintf("доска: %s в Check, коммит %s", list, hash))
	if err := push("доска запушена",
		"выкат и перевод в Check прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	if note := syncWindowTree(root, main); note != "" {
		msg = append(msg, note)
	}
	msg = append(msg, nextAfterMerge(b, train))
	return strings.Join(msg, "\n"), nil
}

// freshMain ловит отставший клон до необратимых шагов слияния: на origin
// доска могла уехать вперёд (например, другая машина поставила задачу в
// Check), а локальная копия об этом не знает. Без origin проверки нет,
// локальный проект сверять не с чем.
func freshMain(root, main string) error {
	if _, err := git(root, "config", "--get", "remote.origin.url"); err != nil {
		return nil
	}
	if out, err := git(root, "fetch", "origin", main); err != nil {
		return fmt.Errorf("git fetch origin не прошёл, состояние origin неизвестно:\n%s", cmdoutFrame(root, "git-fetch", out))
	}
	if _, err := git(root, "merge-base", "--is-ancestor", "origin/"+main, main); err != nil {
		return fmt.Errorf("локальный %s отстал от origin/%s, сначала подтянуть его (git pull --rebase)", main, main)
	}
	return nil
}

type RevertParams struct {
	ID   string
	Test string // команда тестов после отката, пустая пропускает прогон
	Msg  string // своё сообщение коммита-отката вместо «revert: ...»
	Push bool
}

// taskCommits собирает коммиты задачи, новые первыми: записанные разделом
// «Выкат» и найденные по её ID, стоящему в subject первым.
// Коммиты, трогающие только доску и файлы задач, не откатываются: состояние
// доски двигает taskctl, а не git revert.
func taskCommits(root, main, id string) ([]string, error) {
	log, err := git(root, "log", main, "--format=%H%x09%s")
	if err != nil {
		return nil, err
	}
	rec, err := mergedShas(root, id)
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, ln := range strings.Split(log, "\n") {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok || (!ownsSubject(subj, id) && !inRecord(rec, sha)) {
			continue
		}
		// Прошлый откат задачи это граница: всё старше него либо уже
		// откачено, либо относится к прошлым заходам на ту же задачу.
		// Распознавание общее с составом поезда, включая откаты с кастомным
		// -m: иначе второй заход откатил бы и прошлый откат, и старую правку.
		if isRevertSubject(subj) {
			break
		}
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return nil, err
		}
		if boardOnly(files) {
			continue
		}
		shas = append(shas, sha)
	}
	return shas, nil
}

// ownsSubject отвечает, принадлежит ли коммит задаче id: владельцем считается
// первый ID в subject, прочие упоминания дальше по тексту чужие. Упомянуть
// соседнюю задачу в сообщении законно («пробел DoD цели XR-002 снят»), и по
// поиску ID словом такой коммит записывался в чужой код, а задача-соседка
// приходила осиротевшей и отбивала merge.
// Ищется только ID с префиксом самой задачи: ключ внешнего трекера или «UTF-8»
// в тексте иначе занял бы место первого ID и владельца отобрал.
func ownsSubject(subj, id string) bool {
	pref, _, ok := strings.Cut(id, "-")
	if !ok || pref == "" {
		return false
	}
	return firstID(subj, pref) == id
}

// firstID возвращает первый ID вида «<pref>-<число>», стоящий в s отдельным
// словом. Пустая строка значит, что задач этого префикса в тексте нет.
func firstID(s, pref string) string {
	for i := 0; ; {
		j := strings.Index(s[i:], pref+"-")
		if j < 0 {
			return ""
		}
		j += i
		i = j + len(pref) + 1
		if j > 0 && isWordByte(s[j-1]) {
			continue
		}
		k := i
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k == i || (k < len(s) && isWordByte(s[k])) {
			continue
		}
		return s[j:k]
	}
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func boardOnly(files string) bool {
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if f != "docs/TASKS.md" && f != "docs/TASKS-archive.md" && !strings.HasPrefix(f, "docs/tasks/") {
			return false
		}
	}
	return true
}

// docsOnly шире boardOnly: коммит не трогает ничего за пределами docs/.
// Для отката это разные вещи: правку LLD или README revert возвращает вместе
// с кодом (boardOnly), но на прод она не влияет и грузом выката не считается.
func docsOnly(files string) bool {
	for _, f := range strings.Split(files, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.HasPrefix(f, "docs/") {
			return false
		}
	}
	return true
}

// rangeDocsOnly отвечает, лежит ли весь диапазон коммитов под docs/. От
// codeCommits он отличается множеством, а не признаком: там окно выката и
// коммиты, отобранные по ID задачи, здесь весь слитый диапазон ветки, где
// чужих коммитов быть не может. Пустой диапазон бескодовым не считается:
// сливать было нечего, и молча переводить такую задачу в Check нельзя.
func rangeDocsOnly(root, from, to string) (bool, error) {
	log, err := git(root, "log", from+".."+to, "--format=%H")
	if err != nil {
		return false, err
	}
	if log == "" {
		return false, nil
	}
	for _, sha := range strings.Split(log, "\n") {
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return false, err
		}
		if !docsOnly(files) {
			return false, nil
		}
	}
	return true, nil
}

// catchUpLimit ограничивает число доборов ff. Упёрлись в потолок значит main
// меняется быстрее, чем идёт слияние, и повторять его бесконечно бессмысленно.
const catchUpLimit = 3

// ffCatchUp уводит main на ветку fast-forward, переживая коммиты доски, легшие
// в main за время прогона тестов: такой коммит идёт мимо замка конвейера
// (его кладёт taskctl соседней сессии) и оставляет ветку позади. Пока уехавшее
// это только доска, ff добирается повторным ребейзом без повторного прогона:
// тесты такой дельты не касаются. Возвращает preSha того захода, на котором ff
// прошёл, и число доборов.
func ffCatchUp(root, workDir, wt, main, branch string) (string, int, error) {
	for catchUps := 0; ; catchUps++ {
		// preSha переснимается на каждом заходе: со старым recordMerge записал бы
		// в файл задачи чужие коммиты доски, а тег поезда втащил бы их в окно
		// выката.
		preSha, err := git(root, "rev-parse", "HEAD")
		if err != nil {
			return "", catchUps, err
		}
		out, ffErr := git(root, "merge", "--ff-only", branch)
		if ffErr == nil {
			return preSha, catchUps, nil
		}
		refused := fmt.Errorf("fast-forward не прошёл:\n%s", cmdoutFrame(root, "git-merge", out))
		if catchUps == catchUpLimit {
			return "", catchUps, fmt.Errorf("%v\nдоборов ребейза сделано %d, main меняется быстрее, чем идёт слияние: повторить merge", refused, catchUps)
		}
		// Дельту уехавшего main наивным preSha..main не увидеть: preSha снят уже
		// после уезда, и диапазон пуст. Считаем от merge-base ветки и main.
		base, err := git(root, "merge-base", "HEAD", branch)
		if err != nil {
			return "", catchUps, refused
		}
		board, err := rangeBoardOnly(root, base, "HEAD")
		if err != nil {
			return "", catchUps, err
		}
		if !board {
			return "", catchUps, refused
		}
		// Без worktree ветка к этому моменту не в чекауте, там стоит main.
		// Ребейз чекаутит её сам, а выход отсюда обязан вернуть дерево на main,
		// как оставляет его красный ff.
		if wt == "" {
			if _, err := git(root, "checkout", branch); err != nil {
				return "", catchUps, err
			}
		}
		rebaseOut, rebaseErr := git(workDir, "rebase", main)
		if rebaseErr != nil {
			git(workDir, "rebase", "--abort")
		}
		if wt == "" {
			if _, err := git(root, "checkout", main); err != nil {
				return "", catchUps, err
			}
		}
		if rebaseErr != nil {
			return "", catchUps, fmt.Errorf("повторный ребейз на %s не прошёл, разбирать конфликт руками:\n%s", main, cmdoutFrame(workDir, "git-rebase", rebaseOut))
		}
	}
}

// rangeBoardOnly отвечает, состоит ли диапазон целиком из коммитов доски.
// Признак тут уже, чем docsOnly: тестовый набор самого devkit читает docs/
// (doctor --layout ловит там код-файлы), и дельта «весь docs/» провезла бы
// красноту мимо прогона. Пустой диапазон доборным не считается: уезда не было,
// и красный ff говорит о другом.
func rangeBoardOnly(root, from, to string) (bool, error) {
	log, err := git(root, "log", from+".."+to, "--format=%H")
	if err != nil {
		return false, err
	}
	if log == "" {
		return false, nil
	}
	for _, sha := range strings.Split(log, "\n") {
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return false, err
		}
		if !boardOnly(files) {
			return false, nil
		}
	}
	return true, nil
}

func cmdRevert(root string, p RevertParams) (string, error) {
	// Как в ship: откат делается в основном дереве, откуда бы ни запустили.
	root, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	if corpActive(root) {
		return "", corpRefused("revert")
	}
	unlock, err := acquireLock(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	main, err := preflight(root)
	if err != nil {
		return "", err
	}
	if _, err := git(root, "checkout", main); err != nil {
		return "", err
	}
	shas, err := taskCommits(root, main, p.ID)
	if err != nil {
		return "", err
	}
	if len(shas) == 0 {
		return "", fmt.Errorf("на %s нет коммитов кода %s ни в записи файла задачи, ни по ID в subject, откатывать нечего (форвард-фикс?)", main, p.ID)
	}
	// Откат правок только под docs/ (LLD, дока) прода не касается: повторный
	// выкат не нужен, а при копящемся поезде он увёз бы туда чужой код.
	docsRevert := true
	for _, sha := range shas {
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return "", err
		}
		if !docsOnly(files) {
			docsRevert = false
			break
		}
	}
	// Задача из невыкаченного поезда до прода не доехала: откат нужен только в
	// git, а повторный выкат увёз бы на прод остальной поезд раньше времени.
	// Считается до коммита-отката (после него задача из поезда уже выбыла), и
	// ошибки здесь не глотаются: это защитный контур, fail-open на аварийном
	// пути кончился бы как раз преждевременным выкатом.
	// Осиротевшая задача (код в окне, строка не в In progress) считается так
	// же: её код тоже не доехал до прода, и повторный выкат при её откате
	// увёз бы туда остальной поезд.
	inTrain := false
	bTrain, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	tr, strayRows, err := trainTasks(root, main, bTrain)
	if err != nil {
		return "", err
	}
	for _, id := range tr {
		inTrain = inTrain || id == p.ID
	}
	for _, s := range strayRows {
		inTrain = inTrain || s.ID == p.ID
	}
	// Откат одним коммитом: последовательность revert-коммитов на середине
	// умеет падать в конфликт и оставлять прод полупочиненным.
	if out, err := git(root, append([]string{"revert", "--no-commit"}, shas...)...); err != nil {
		git(root, "revert", "--abort")
		return "", fmt.Errorf("revert в конфликте, чинить форвард-фиксом:\n%s", cmdoutFrame(root, "git-revert", out))
	}
	// Заглушка на случай, когда прошлый откат шёл с нераспознаваемым -m и
	// граница по subject его не увидела: пустой revert значит уже откачено.
	if st, _ := git(root, "status", "--porcelain", "--untracked-files=no"); st == "" {
		git(root, "revert", "--quit")
		return "", fmt.Errorf("изменения коммитов %s уже откачены, дерево не поменялось", p.ID)
	}
	msg := p.Msg
	var short []string
	for _, sha := range shas {
		short = append(short, sha[:7])
	}
	if msg == "" {
		msg = fmt.Sprintf("revert: %s откат %s", p.ID, strings.Join(short, ", "))
	}
	if _, err := git(root, "commit", "-m", msg); err != nil {
		return "", err
	}
	if p.Test != "" {
		if out, err := runShell(root, p.Test); err != nil {
			return "", fmt.Errorf("тесты после отката красные:\n%s", cmdoutFrame(root, "test", out))
		}
	}
	// Автономия действует и на аварийном пути, здесь она нужнее всего: без
	// пуша отката повторный pull-выкат утащил бы с origin как раз тот код,
	// который только что откатили.
	plan, err := resolveDeploy(root, "")
	if err != nil {
		return "", err
	}
	out := []string{fmt.Sprintf("откачено коммитов: %d (%s)", len(shas), strings.Join(short, ", "))}
	if plan.warn != "" {
		out = append(out, "предупреждение: "+plan.warn)
	}
	push := func(note, failed string) error {
		if !p.Push && !plan.autonomous {
			return nil
		}
		if _, err := git(root, "push"); err != nil {
			return fmt.Errorf("%s: %v", failed, err)
		}
		if err := pushTag(root); err != nil {
			return err
		}
		out = append(out, note)
		return nil
	}
	if err := push("откат запушен", "откат закоммичен, но пуш не прошёл, повторный выкат не запускался"); err != nil {
		return "", err
	}
	switch {
	case docsRevert:
		out = append(out, "откачены только правки под docs/, прод не менялся, повторный выкат не нужен")
	case inTrain:
		out = append(out, "задача была в поезде и до прода не доехала, повторный выкат не нужен")
	case plan.run != "":
		if o, timedOut, err := runShellLimit(root, plan.run, plan.timeout, nil); err != nil {
			short, full := deployProblem(plan.run, timedOut, plan.timeout)
			oSummary := cmdoutFrame(root, "deploy", o)
			note := notify(root, p.ID, fmt.Sprintf("%s: повторный выкат %s %s", filepath.Base(root), p.ID, short), full+"\n"+oSummary)
			return "", fmt.Errorf("откат закоммичен, но повторный выкат %s:\n%s%s", full, oSummary, note)
		}
		out = append(out, "повторный выкат прошёл")
	case plan.manual != "":
		out = append(out, "повторный выкат за пользователем ("+plan.manual+")")
	default:
		out = append(out, "прод чинится выкатом откатанного "+main+" по плейбуку проекта")
	}
	// Повторный выкат это тоже точка выката, тег двигается как в merge и ship.
	// У задачи из поезда и у документной правки прод не менялся, тег стоит
	// где стоял.
	if !inTrain && !docsRevert && hasTag(root) {
		if _, err := git(root, "tag", "-f", deployTag, main); err != nil {
			return "", err
		}
	}
	// Откат вернул прод к прежнему состоянию, значит провал проверки погашен.
	// Признак снимается отдельной командой и независимо от секции: перевод в
	// In progress его не трогает (гасит только перевод в Check), а между
	// провалом и откатом задачу могли поставить на блокер. Обе правки доски
	// идут одним коммитом.
	if b, err := loadBoard(root); err == nil && b.sectOf(p.ID) != "" {
		var done []string
		cleared := failedOf(b, p.ID) != ""
		if cleared {
			if _, err := taskFailClear(root, p.ID); err != nil {
				return "", err
			}
		}
		if b.sectOf(p.ID) != "in-progress" {
			if _, err := taskMove(root, p.ID, "in-progress"); err != nil {
				return "", err
			}
			done = append(done, "обратно в In progress")
		}
		if cleared {
			done = append(done, "признак провала снят откатом")
		}
		if len(done) > 0 {
			joined := strings.Join(done, ", ")
			hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s %s", p.ID, joined), p.ID)
			if err != nil {
				return "", err
			}
			note := ""
			if cleared {
				// Признак погашен, но занявшая очередь задача могла остаться
				// в Check: выкат за ней проверен, очередь держит она. «Свободна»
				// рядом с занятой очередью врала бы, поэтому свободна только
				// при честном пустом checkQueue.
				if busy, err := checkQueue(root, main, b); err == nil && len(busy) == 0 {
					note = ", очередь выката свободна"
				} else {
					note = fmt.Sprintf(", очередь держит %s (проверен выкат за откаченной задачей, признак провала больше её не держит)", strings.Join(busy, ", "))
				}
			}
			out = append(out, fmt.Sprintf("доска: %s %s%s, коммит %s", p.ID, joined, note, hash))
		}
	}
	if err := push("доска запушена", "откат и повторный выкат прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	if note := syncWindowTree(root, main); note != "" {
		out = append(out, note)
	}
	return strings.Join(out, "\n"), nil
}

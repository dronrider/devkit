package main

import (
	"fmt"
	"os/exec"
	"strings"
	"unicode"
)

func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// runShell выполняет команду теста или выката: они приходят строкой из флага
// и могут содержать пайпы, поэтому sh -c, а не argv.
func runShell(root, cmdStr string) (string, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 30 {
		lines = append([]string{"..."}, lines[len(lines)-30:]...)
	}
	return strings.Join(lines, "\n")
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
		return "", fmt.Errorf("taskctl не найден в PATH, собери его из devkit/taskctl")
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
		return "", fmt.Errorf("в рабочем дереве незакоммиченное, сначала закоммить:\n%s", tail(st))
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

func commitBoard(root, msg string) (string, error) {
	if _, err := git(root, "add", "--", "docs/TASKS.md"); err != nil {
		return "", err
	}
	if _, err := git(root, "commit", "-m", msg, "--", "docs/TASKS.md"); err != nil {
		return "", err
	}
	return git(root, "rev-parse", "--short", "HEAD")
}

func cmdStatus(root string) (string, error) {
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	branch, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	var out []string
	out = append(out, "ветка: "+branch)
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
	var train, strays []string
	if main, err := mainBranch(root); err == nil {
		if train, strays, err = trainTasks(root, main, b); err != nil {
			return "", err
		}
	}
	if len(train) > 0 {
		out = append(out, "поезд: "+strings.Join(train, ", ")+" слиты и ждут выката (shipctl ship)")
	}
	if len(strays) > 0 {
		out = append(out, "аномалия: код в окне выката, а задача не в In progress ("+strings.Join(strays, ", ")+"); merge и ship будут отказывать, пока не разобрано")
	}
	if rows := b.sects["check"]; len(rows) == 0 {
		out = append(out, "очередь свободна, сливать и выкатывать можно")
	} else {
		out = append(out, fmt.Sprintf("очередь занята: %s в Check, сначала проверка и taskctl close", rows[0].ID))
	}
	// Решение по выкату берётся из resolveDeploy, а не из сырого конфига:
	// иначе status и merge разъезжаются, как только логика меняется.
	plan, err := resolveDeploy(root, "")
	if err != nil {
		return "", err
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

// trainTasks собирает поезд: задачи из In progress, чьи коммиты кода слиты в
// main после точки последнего выката. Коммиты только по доске и файлам задач
// членства не дают (запись «в работу» это не код). Вторым списком приходят
// осиротевшие задачи: код в окне выката есть, а строка ушла из In progress
// (руками в Blocked, обратно в Backlog, в Check мимо ship). Такую аномалию
// вызывающие не везут на прод молча, а поднимают как ошибку.
func trainTasks(root, main string, b *board) (train, strays []string, err error) {
	if !hasTag(root) {
		return nil, nil, nil
	}
	log, err := git(root, "log", deployTag+".."+main, "--format=%H%x09%s")
	if err != nil || log == "" {
		return nil, nil, err
	}
	lines := strings.Split(log, "\n") // новые первыми
	inWindow := func(id string) (bool, error) {
		for _, ln := range lines {
			sha, subj, ok := strings.Cut(ln, "\t")
			if !ok || !containsWord(subj, id) {
				continue
			}
			if isRevertSubject(subj) {
				return false, nil
			}
			files, err := git(root, "show", "--name-only", "--pretty=", sha)
			if err != nil {
				return false, err
			}
			if boardOnly(files) {
				continue
			}
			return true, nil
		}
		return false, nil
	}
	for _, sect := range []string{"in-progress", "check", "blocked", "backlog"} {
		for _, r := range b.sects[sect] {
			ok, err := inWindow(r.ID)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			if sect == "in-progress" {
				train = append(train, r.ID)
			} else {
				strays = append(strays, r.ID+" в "+sect)
			}
		}
	}
	return train, strays, nil
}

type MergeParams struct {
	ID     string
	Test   string // команда тестов, обязательна
	Deploy string // явная команда выката; пустую подхватывает .devkit/deploy.local
	Train  bool   // слить в поезд: без выката и без перевода в Check
	Push   bool
}

func cmdMerge(root string, p MergeParams) (string, error) {
	if p.Test == "" {
		return "", fmt.Errorf("нужен --test с командой тестов проекта: ветка сливается только зелёной")
	}
	if p.Train && p.Deploy != "" {
		return "", fmt.Errorf("--train откладывает выкат до shipctl ship, вместе с --deploy он не имеет смысла")
	}
	main, err := preflight(root)
	if err != nil {
		return "", err
	}
	branch, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if branch == main {
		return "", fmt.Errorf("стоишь на %s, сливать нечего: merge запускается с фичеветки", main)
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
	if rows := b.sects["check"]; len(rows) > 0 {
		return "", fmt.Errorf("очередь занята: %s в Check; по RULES.board.md непроверенный выкат один, сначала проверка и taskctl close", rows[0].ID)
	}
	// Одиночный merge при непустом поезде увёз бы на прод чужие непроверенные
	// правки, а в Check перевёл только свою задачу: инвариант ломается молча.
	train, strays, err := trainTasks(root, main, b)
	if err != nil {
		return "", err
	}
	if len(strays) > 0 {
		return "", fmt.Errorf("код в окне выката, а задача не в In progress: %s; вернуть задачу в In progress или откатить её коммиты, иначе они уедут на прод без Check", strings.Join(strays, ", "))
	}
	if !p.Train && len(train) > 0 {
		return "", fmt.Errorf("в поезде %s, одиночный выкат смешал бы их со своей задачей: либо merge --train, либо сначала shipctl ship", strings.Join(train, ", "))
	}
	open, err := openReviewNotes(root, p.ID)
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
	var warn string
	if subjects, err := git(root, "log", main+"..HEAD", "--format=%s"); err == nil && !strings.Contains(subjects, p.ID) {
		warn = fmt.Sprintf("предупреждение: в коммитах ветки нет %s в subject, revert по ID их не найдёт\n", p.ID)
	}
	if out, err := git(root, "rebase", main); err != nil {
		git(root, "rebase", "--abort")
		return "", fmt.Errorf("ребейз на %s не прошёл, разбирать конфликт руками:\n%s", main, tail(out))
	}
	if out, err := runShell(root, p.Test); err != nil {
		return "", fmt.Errorf("тесты после ребейза красные, ветка остаётся несшитой:\n%s", tail(out))
	}
	if _, err := git(root, "checkout", main); err != nil {
		return "", err
	}
	preSha, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if out, err := git(root, "merge", "--ff-only", branch); err != nil {
		return "", fmt.Errorf("fast-forward не прошёл:\n%s", tail(out))
	}
	git(root, "branch", "-d", branch)
	// Первое поездное слияние заводит тег на main до себя: что было на main к
	// этому моменту, считается выкаченным, и окно поезда начинается с этой ветки.
	if p.Train {
		if _, err := git(root, "rev-parse", "--verify", deployTag); err != nil {
			if _, err := git(root, "tag", deployTag, preSha); err != nil {
				return "", err
			}
		}
	}
	msg := []string{warn + fmt.Sprintf("%s слита в %s fast-forward, ветка %s удалена", p.ID, main, branch)}
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
		if now, _, err := trainTasks(root, main, b); err == nil {
			msg = append(msg, fmt.Sprintf("в поезде: %s; выкат поезда: shipctl ship", strings.Join(now, ", ")))
		} else {
			msg = append(msg, fmt.Sprintf("в поезде (состав не пересчитался: %v); выкат поезда: shipctl ship", err))
		}
		return strings.Join(msg, "\n"), nil
	}
	switch {
	case deploy.run != "":
		if out, err := runShell(root, deploy.run); err != nil {
			return "", fmt.Errorf("слито, но выкат упал, задача остаётся в In progress:\n%s", tail(out))
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
	hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s в Check", p.ID))
	if err != nil {
		return "", err
	}
	msg = append(msg, fmt.Sprintf("доска: %s в Check, коммит %s", p.ID, hash))
	if err := push("доска запушена",
		"выкат и перевод в Check прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	msg = append(msg, fmt.Sprintf("после проверки пользователем: taskctl close %s", p.ID))
	return strings.Join(msg, "\n"), nil
}

type ShipParams struct {
	Deploy string // явная команда выката; пустую подхватывает .devkit/deploy.local
	Push   bool
}

// cmdShip выкатывает поезд: один деплой на все задачи, слитые после точки
// последнего выката, разом переводит их в Check и двигает тег. Проверка идёт
// на одном билде, каждая задача по своему сценарию; провал одной чинится
// точечным revert, остальной поезд остаётся на проде.
func cmdShip(root string, p ShipParams) (string, error) {
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
	if rows := b.sects["check"]; len(rows) > 0 {
		return "", fmt.Errorf("очередь занята: %s в Check; по RULES.board.md непроверенный выкат один, сначала проверка и taskctl close", rows[0].ID)
	}
	train, strays, err := trainTasks(root, main, b)
	if err != nil {
		return "", err
	}
	if len(strays) > 0 {
		return "", fmt.Errorf("код в окне выката, а задача не в In progress: %s; вернуть задачу в In progress или откатить её коммиты, иначе они уедут на прод без Check", strings.Join(strays, ", "))
	}
	if len(train) == 0 {
		return "", fmt.Errorf("поезд пуст: после точки последнего выката нет слитых задач (копит их merge --train)")
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
		if out, err := runShell(root, deploy.run); err != nil {
			return "", fmt.Errorf("выкат поезда упал, задачи остаются в In progress:\n%s", tail(out))
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
	hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s в Check поездом", list))
	if err != nil {
		return "", err
	}
	msg = append(msg, fmt.Sprintf("доска: %s в Check, коммит %s", list, hash))
	if err := push("доска запушена",
		"выкат и перевод в Check прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	msg = append(msg, "после проверки каждая задача закрывается своим taskctl close")
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
		return fmt.Errorf("git fetch origin не прошёл, состояние origin неизвестно:\n%s", tail(out))
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

// taskCommits собирает коммиты задачи по ID в subject, новые первыми.
// Коммиты, трогающие только доску и файлы задач, не откатываются: состояние
// доски двигает taskctl, а не git revert.
func taskCommits(root, main, id string) ([]string, error) {
	log, err := git(root, "log", main, "--format=%H%x09%s")
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, ln := range strings.Split(log, "\n") {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok || !containsWord(subj, id) {
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

func containsWord(s, w string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !isWordByte(s[j-1])
		after := j+len(w) == len(s) || !isWordByte(s[j+len(w)])
		if before && after {
			return true
		}
		i = j + len(w)
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

func cmdRevert(root string, p RevertParams) (string, error) {
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
		return "", fmt.Errorf("на %s нет коммитов кода с %s в subject, откатывать нечего (форвард-фикс?)", main, p.ID)
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
		inTrain = inTrain || strings.HasPrefix(s, p.ID+" ")
	}
	// Откат одним коммитом: последовательность revert-коммитов на середине
	// умеет падать в конфликт и оставлять прод полупочиненным.
	if out, err := git(root, append([]string{"revert", "--no-commit"}, shas...)...); err != nil {
		git(root, "revert", "--abort")
		return "", fmt.Errorf("revert в конфликте, чинить форвард-фиксом:\n%s", tail(out))
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
			return "", fmt.Errorf("тесты после отката красные:\n%s", tail(out))
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
	case inTrain:
		out = append(out, "задача была в поезде и до прода не доехала, повторный выкат не нужен")
	case plan.run != "":
		if o, err := runShell(root, plan.run); err != nil {
			return "", fmt.Errorf("откат закоммичен, но повторный выкат упал:\n%s", tail(o))
		}
		out = append(out, "повторный выкат прошёл")
	case plan.manual != "":
		out = append(out, "повторный выкат за пользователем ("+plan.manual+")")
	default:
		out = append(out, "прод чинится выкатом откатанного "+main+" по плейбуку проекта")
	}
	// Повторный выкат это тоже точка выката, тег двигается как в merge и ship.
	// У задачи из поезда прод не менялся, тег стоит где стоял.
	if !inTrain && hasTag(root) {
		if _, err := git(root, "tag", "-f", deployTag, main); err != nil {
			return "", err
		}
	}
	if b, err := loadBoard(root); err == nil && b.sectOf(p.ID) != "" && b.sectOf(p.ID) != "in-progress" {
		if _, err := taskMove(root, p.ID, "in-progress"); err != nil {
			return "", err
		}
		hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s обратно в In progress", p.ID))
		if err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("доска: %s обратно в In progress, коммит %s", p.ID, hash))
	}
	if err := push("доска запушена", "откат и повторный выкат прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	return strings.Join(out, "\n"), nil
}

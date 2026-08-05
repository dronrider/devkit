package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// pushEnv выдаёт разрешение на пуш хуку pre-push. Правила разрешают пуш доске
// и автономному режиму, оба пути идут через shipctl, а рубеж отличает их от
// самовольного пуша агента только по этой переменной. Нулевое окружение это
// наследование родительского, поэтому обычным командам git оно и остаётся.
func pushEnv(args []string) []string {
	if len(args) == 0 || args[0] != "push" {
		return nil
	}
	return append(os.Environ(), "DEVKIT_PUSH_OK=1")
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = pushEnv(args)
	out, err := cmd.CombinedOutput()
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

// taskFailClear гасит признак провала проверки. Доску правит taskctl, как и
// статус: shipctl только зовёт его и коммитит результат.
func taskFailClear(root, id string) (string, error) {
	out, err := exec.Command("taskctl", "-C", root, "fail", id, "--clear").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("taskctl fail --clear: %v (%s)", err, strings.TrimSpace(string(out)))
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
		out = append(out, "worktree: "+l.Branch+" в "+l.Path)
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
	// Без main очередь по коммитам не посчитать, тогда держит вся секция.
	busy := make([]string, 0, len(b.sects["check"]))
	for _, r := range b.sects["check"] {
		busy = append(busy, r.ID)
	}
	if main, err := mainBranch(root); err == nil {
		if train, strays, err = trainTasks(root, main, b); err != nil {
			return "", err
		}
		if busy, err = checkQueue(root, main, b); err != nil {
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
		out = append(out, fmt.Sprintf("очередь занята: %s в Check, сначала проверка и taskctl close", strings.Join(busy, ", ")))
	case len(strays) > 0:
		// вердикт не печатается: строка аномалии уже сказала, что merge и
		// ship будут отказывать, «очередь свободна» рядом с ней врала бы.
	case len(b.sects["check"]) > 0:
		out = append(out, "очередь свободна: в Check только задачи без выкаченного кода, подтверждение за пользователем, но выкат они не держат")
	default:
		out = append(out, "очередь свободна, сливать и выкатывать можно")
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
// ли у задачи коммиты кода: записанные разделом «Выкат» либо с ID в subject,
// трогающие что-то кроме docs/.
// Правки только под docs/ (доска, файлы задач, LLD) едут с выкатом, но прод
// не меняют, поэтому кодом не считаются. Прошлый откат это граница, как в
// taskCommits: всё, что старше него, уже откачено или относится к прошлым
// заходам на задачу.
func codeCommits(root string, lines []string, id string, rec []string) (bool, error) {
	for _, ln := range lines {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok || (!containsWord(subj, id) && !inRecord(rec, sha)) {
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
// весь main). LLD, дока и прочие задачи без кода на проде ждут в Check
// подтверждения пользователя, но следующему выкату не мешают: инвариант
// «непроверенный выкат один» про прод, а не про секцию доски. Задачу, слитую
// через shipctl, поиск находит по записи в файле задачи, а по ID в subject
// ищутся только слитые руками мимо него и до появления записи.
func checkQueue(root, main string, b *board) ([]string, error) {
	return deployedIn(root, main, b, "check")
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
			return "", fmt.Errorf("стоишь на %s, и worktree с веткой %s не нашёлся: сливать нечего", main, p.ID)
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
			return "", fmt.Errorf("в worktree %s незакоммиченное, сначала закоммить:\n%s", wt, tail(st))
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
	busy, err := checkQueue(root, main, b)
	if err != nil {
		return "", err
	}
	if len(busy) > 0 {
		return "", fmt.Errorf("очередь занята: %s в Check с выкаченным кодом; по RULES.board.md непроверенный выкат один, сначала проверка и taskctl close", strings.Join(busy, ", "))
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
	// Предупреждения собираются до ребейза (diff ветки против main ещё
	// осмысленный) и не валят слияние: это подсказки по правилам, а не
	// предусловия.
	var warns []string
	if subjects, err := git(root, "log", main+".."+branch, "--format=%s"); err == nil && !strings.Contains(subjects, p.ID) {
		warns = append(warns, fmt.Sprintf("предупреждение: в коммитах ветки нет %s в subject; очередь, поезд и revert найдут их по записи в файле задачи, но по истории задачу так не собрать", p.ID))
	}
	warns = append(warns, regcheckWarning(root, reviewRoot, main, branch, b.rowOf(p.ID).Type)...)
	if p.Train {
		warns = append(warns, trainWarnings(root, reviewRoot, main, branch, b, p.ID, train)...)
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
	if out, err := git(workDir, "rebase", main); err != nil {
		git(workDir, "rebase", "--abort")
		return "", fmt.Errorf("ребейз на %s не прошёл, разбирать конфликт руками:\n%s", main, tail(out))
	}
	if out, err := runShell(workDir, test); err != nil {
		return "", fmt.Errorf("тесты после ребейза красные, ветка остаётся несшитой:\n%s", tail(out))
	}
	if wt == "" {
		if _, err := git(root, "checkout", main); err != nil {
			return "", err
		}
	}
	preSha, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if out, err := git(root, "merge", "--ff-only", branch); err != nil {
		return "", fmt.Errorf("fast-forward не прошёл:\n%s", tail(out))
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
		wtNote = removeWorktree(root, wt, branch)
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
	msg := []string{warn + fmt.Sprintf("%s слита в %s fast-forward", p.ID, main)}
	if testFromConfig {
		msg = append(msg, "тесты гнались командой из "+deployConfigPath+": "+test)
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
			hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s в Check", p.ID))
			if err != nil {
				return "", err
			}
			msg = append(msg, "поезд задачу не везёт: ветка не трогает ничего вне docs/, выката ей не нужно",
				fmt.Sprintf("доска: %s в Check, коммит %s", p.ID, hash))
			if err := push("доска запушена",
				"перевод в Check прошёл, но пуш доски не прошёл, повторить git push руками"); err != nil {
				return "", err
			}
			msg = append(msg, fmt.Sprintf("после проверки пользователем: taskctl close %s", p.ID))
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
		return strings.Join(msg, "\n"), nil
	}
	if deploy.warn != "" && !p.Train {
		msg = append(msg, "предупреждение: "+deploy.warn)
	}
	switch {
	case deploy.run != "":
		if out, err := runShell(root, deploy.run); err != nil {
			note := notify(root, fmt.Sprintf("%s: выкат %s упал", filepath.Base(root), p.ID), out)
			return "", fmt.Errorf("слито, но выкат упал, задача остаётся в In progress:\n%s%s", tail(out), note)
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
	if failReason != "" {
		msg = append(msg, fmt.Sprintf("признак провала (%s) снят переводом в Check, очередь выката свободна", failReason))
	}
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
		return "", fmt.Errorf("%s; поезд не выкатывается, пока прод сломан", brokenProd(fs[0]))
	}
	busy, err := checkQueue(root, main, b)
	if err != nil {
		return "", err
	}
	if len(busy) > 0 {
		return "", fmt.Errorf("очередь занята: %s в Check с выкаченным кодом; по RULES.board.md непроверенный выкат один, сначала проверка и taskctl close", strings.Join(busy, ", "))
	}
	train, strays, err := trainTasks(root, main, b)
	if err != nil {
		return "", err
	}
	if len(strays) > 0 {
		return "", fmt.Errorf("код в окне выката, а задача не в In progress: %s; вернуть задачу в In progress или откатить её коммиты, иначе они уедут на прод без Check", strayList(strays))
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
		if out, err := runShell(root, deploy.run); err != nil {
			note := notify(root, fmt.Sprintf("%s: выкат поезда упал (%s)", filepath.Base(root), list), out)
			return "", fmt.Errorf("выкат поезда упал, задачи остаются в In progress:\n%s%s", tail(out), note)
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

// taskCommits собирает коммиты задачи, новые первыми: записанные разделом
// «Выкат» и найденные по ID в subject.
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
		if !ok || (!containsWord(subj, id) && !inRecord(rec, sha)) {
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
		if o, err := runShell(root, plan.run); err != nil {
			note := notify(root, fmt.Sprintf("%s: повторный выкат %s упал", filepath.Base(root), p.ID), o)
			return "", fmt.Errorf("откат закоммичен, но повторный выкат упал:\n%s%s", tail(o), note)
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
			hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s %s", p.ID, joined))
			if err != nil {
				return "", err
			}
			note := ""
			if cleared {
				note = ", очередь выката свободна"
			}
			out = append(out, fmt.Sprintf("доска: %s %s%s, коммит %s", p.ID, joined, note, hash))
		}
	}
	if err := push("доска запушена", "откат и повторный выкат прошли, но пуш доски не прошёл, повторить git push руками"); err != nil {
		return "", err
	}
	return strings.Join(out, "\n"), nil
}

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/gitrun"
)

// gitRun это единственная дорога к git из taskctl: закрытый запрос учётки и
// предел времени на разговоре с remote живут в общем пакете (DK-697), и второй
// копии списка переменных тут заводить незачем.
func gitRun(root string, args ...string) (string, error) {
	limit, err := gitrun.Timeout()
	if err != nil {
		return "", err
	}
	return gitrun.Run(root, args, limit)
}

func boardPath(root string) string   { return filepath.Join(root, "docs", "TASKS.md") }
func archivePath(root string) string { return filepath.Join(root, "docs", "TASKS-archive.md") }

// findRoot ищет корень репозитория (директорию с docs/TASKS.md) вверх от start.
// Перед подъёмом смотрит редирект корп-контура: в корп-клоне рабочие файлы
// лежат в боковой директории, и её называет ключ devkit.local (corp.go).
func findRoot(start string) (string, error) {
	if local := corpLocal(start); local != "" {
		return local, nil
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(boardPath(dir)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", corpRootErr(start)
		}
		dir = parent
	}
}

func checkCell(name, s string) error {
	if strings.ContainsAny(s, "|\n") {
		return fmt.Errorf("%s не может содержать «|» и переводы строк", name)
	}
	return nil
}

// checkReason это checkCell плюс запрет на квадратные скобки: причина
// блокировки приклеивается к заголовку суффиксом «[блок: ...]», и скобка в
// её тексте притворилась бы ещё одним суффиксом (например, границей
// «[после ...]»), которую разбор заголовка потом не отличит от настоящей.
func checkReason(reason string) error {
	if err := checkCell("причина", reason); err != nil {
		return err
	}
	if strings.ContainsAny(reason, "[]") {
		return fmt.Errorf("причина не может содержать «[» и «]»")
	}
	return nil
}

// parkPrefixes это первые слова машинных причин blocked (LLD DK-400, решение 2):
// «вопрос:» паркует задачу вопросом человека, и по ней строку будит сторожок
// devkitctl watch, «окружение:» ждёт неготовой среды задачи.
var parkPrefixes = []string{"вопрос", "окружение"}

// checkParkPrefix отбивает сломанный машинный префикс. «вопрос :», «вопрос -»,
// заглавная форма и ведущий пробел после неаккуратной вставки разбирались бы
// как проза, и припаркованная вопросом строка осталась бы в Blocked
// безнадзорной: сторожок ищет в причине ровно «вопрос:».
func checkParkPrefix(reason string) error {
	first := strings.TrimSpace(reason)
	if i := strings.IndexAny(first, " \t"); i >= 0 {
		first = first[:i]
	}
	first = strings.ToLower(strings.TrimSuffix(first, ":"))
	for _, p := range parkPrefixes {
		if first == p && !strings.HasPrefix(reason, p+":") {
			return fmt.Errorf("причина начинается со слова «%s», а машинный префикс пишется «%s: ...»: по нему сторожок devkitctl watch отличает строку, ждущую ответа", p, p)
		}
	}
	return nil
}

// taskGoalLink достаёт из файла задачи строку связи с целью: add кладёт её
// первой строкой под заголовком («Цель: [tasks/XR-900.md](tasks/XR-900.md)»).
// Потолок висящих вопросов считается на цель (LLD DK-400, решение 5), и цель
// строки узнаётся по этой строке. Пустая ссылка значит задачу вне цели,
// потолок её не держит.
func taskGoalLink(root, id string) string {
	body, err := os.ReadFile(taskFileAbs(root, id))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(body), "\n") {
		if s := strings.TrimSpace(ln); strings.HasPrefix(s, "Цель:") {
			return s
		}
	}
	return ""
}

// goalKey приводит запись связи с целью к одному виду: add принимает цель
// в разных формах (голый ID, путь с docs/tasks/, имя файла с .md), и без
// приведения одна цель, записанная двумя формами, потолком не узнаётся.
// Ключом служит имя файла ссылки без каталога и суффикса.
func goalKey(link string) string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(link), "Цель:"))
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.Index(s[i:], "]"); j > 0 {
			s = s[i+1 : i+j]
		}
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".md")
}

// parkedQuestions перечисляет висящие вопросы цели: строки в Blocked с
// причиной «вопрос:» и той же связью с целью, что у спрашивающей, в любой
// форме записи. Вышедшие из Blocked к ней не относятся, их будит сторожок.
func parkedQuestions(b *Board, root, goal string) []string {
	key := goalKey(goal)
	var ids []string
	for _, r := range b.Rows {
		if r.Sect != SectBlocked {
			continue
		}
		_, _, _, _, blockSuf := splitTitle(r.Title)
		if !strings.HasPrefix(blockReason(blockSuf), "вопрос:") {
			continue
		}
		if goalKey(taskGoalLink(root, r.ID)) == key {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// checkQuestionCeiling держит потолок висящих вопросов на цель (LLD DK-400,
// решение 5): при двух припаркованных вопросах новый не принимается, иначе
// человек вернётся к простыне вместо пачки. Отказавшему исполнителю остаётся
// правило доезжать до рубежа: вопрос пишется строкой в файл задачи, заход
// кончается, задача разбирается планировщиком на общих основаниях.
func checkQuestionCeiling(b *Board, root, id string) error {
	goal := taskGoalLink(root, id)
	if goal == "" {
		return nil
	}
	hanging := parkedQuestions(b, root, goal)
	if len(hanging) < questionCeiling() {
		return nil
	}
	return fmt.Errorf("вопросов висит %d из %d (%s): новый вопрос не принимается, доехать до ближайшего рубежа и записать вопрос строкой в файл задачи, ответ разберёт следующий заход",
		len(hanging), questionCeiling(), strings.Join(hanging, ", "))
}

// nextID берёт префикс из существующих строк, а на пустой доске из шапки
// «(префикс XX)», чтобы первая задача заводилась без --id. Черновики считаются
// наравне с доской и архивом: ID выдаётся им при заведении, чтобы на черновик
// можно было сослаться («оформи DK-073»), и занятый номер второй раз выдавать
// нельзя.
func nextID(b *Board, a *Archive, drafts []Draft) (string, error) {
	prefix := b.Prefix
	max := 0
	scan := func(id string, num int) error {
		m := idRe.FindStringSubmatch(id)
		if prefix == "" {
			prefix = m[1]
		} else if prefix != m[1] {
			return fmt.Errorf("на доске и в архиве разные префиксы ID: %s и %s", prefix, m[1])
		}
		if num > max {
			max = num
		}
		return nil
	}
	for _, r := range b.Rows {
		if err := scan(r.ID, r.Num); err != nil {
			return "", err
		}
	}
	for _, r := range a.Rows {
		if err := scan(r.ID, r.Num); err != nil {
			return "", err
		}
	}
	for _, d := range drafts {
		if err := scan(d.ID, d.Num); err != nil {
			return "", err
		}
	}
	if prefix == "" {
		return "", fmt.Errorf("ни одной задачи и нет «(префикс XX)» в шапке доски, укажи --id явно")
	}
	return fmt.Sprintf("%s-%03d", prefix, max+1), nil
}

// CommitOpts это флаги -m/--push изменяющих команд: закоммитить (и запушить)
// ровно те файлы, которые тронула операция, не задевая чужой индекс.
type CommitOpts struct {
	Msg  string
	Push bool
}

// validate зовётся до первой записи на диск, чтобы кривая пара флагов не
// оставляла доску изменённой, но не закоммиченной.
func (c CommitOpts) validate() error {
	if c.Push && c.Msg == "" {
		return fmt.Errorf("--push работает только вместе с -m")
	}
	// Значение -m, начинающееся с дефиса, это почти всегда проглоченный флаг:
	// flag.StringVar берёт значением -m следующий аргумент, даже если тот сам
	// флаг, и тогда -m --push коммитит с сообщением "--push", а сам --push
	// молча пропадает. Отбиваем до записи на диск.
	if c.Msg != "" && strings.HasPrefix(c.Msg, "-") {
		return fmt.Errorf("значение -m начинается с дефиса: после -m, видимо, идёт флаг, а не текст; передай -m \"сообщение\"")
	}
	return nil
}

// apply возвращает хвост для сообщения команды («, коммит abc1234») либо
// пустую строку, когда -m не передан.
func (c CommitOpts) apply(root string, paths []string) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if c.Msg == "" {
		return "", nil
	}
	git := func(args ...string) (string, error) {
		out, err := gitRun(root, args...)
		if err != nil {
			return "", err
		}
		return out, nil
	}
	// В add идут только существующие пути: файл, уехавший через git mv, уже
	// в индексе, а pathspec по нему упал бы. В pathspec коммита нужны все
	// пути, тогда staged-переименование попадает в коммит, а что агент
	// стейджил до этого, не попадает.
	var addPaths []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			addPaths = append(addPaths, p)
		}
	}
	if len(addPaths) > 0 {
		if _, err := git(append([]string{"add", "--"}, addPaths...)...); err != nil {
			return "", err
		}
	}
	if _, err := git(append([]string{"commit", "-m", c.Msg, "--"}, paths...)...); err != nil {
		return "", err
	}
	hash, err := git("rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	suffix := ", коммит " + hash
	if c.Push {
		if _, err := git("push"); err != nil {
			return "", err
		}
		suffix += ", запушено"
	}
	return suffix, nil
}

// normalizeStatus приводит статус к ключу секции: «In progress» = in-progress.
func normalizeStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	return strings.ReplaceAll(s, "_", "-")
}

var barePathRe = regexp.MustCompile(`^[0-9A-Za-z._/~-]+$`)

// wrapLink оборачивает голый путь вида tasks/XR-001.md в markdown-ссылку,
// иначе ячейка не кликается и выпадает из проверки ссылок в lint. Путь,
// написанный естественно от корня репозитория (docs/tasks/XR-001.md), режется
// до docs-относительного вида: checkLinks резолвит цель от docs/, и голый
// префикс docs/ разворачивался бы в несуществующий docs/docs/... (DK-176).
func wrapLink(link string) string {
	if barePathRe.MatchString(link) && (strings.Contains(link, "/") || strings.HasSuffix(link, ".md")) {
		link = strings.TrimPrefix(link, "docs/")
		return fmt.Sprintf("[%s](%s)", link, link)
	}
	return link
}

type AddParams struct {
	ID, Title, Type, Rank, Cost, Link, Status, Accept, Barrier string
	Commit                                                            CommitOpts
}

func cmdAdd(root string, p AddParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "add"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	id := p.ID
	if id == "" {
		if id, err = nextID(b, arch, drafts); err != nil {
			return "", err
		}
	} else if !idRe.MatchString(id) {
		return "", fmt.Errorf("ID %q не вида PREFIX-NNN", id)
	}
	if b.find(id) != nil || arch.has(id) {
		return "", fmt.Errorf("ID %s уже занят", id)
	}
	if strings.TrimSpace(p.Title) == "" {
		return "", fmt.Errorf("нужен --title")
	}
	if err := checkCell("заголовок", p.Title); err != nil {
		return "", err
	}
	// Вид приёмки при заведении обязателен (LLD DK-292, решение 3): без
	// пометки отсутствие суффикса значило бы «не думали вовсе», а не «решили,
	// что агентский». Переходный период, когда add без --accept проходил с
	// предупреждением, кончился: форму дашборда научил слать флаг DK-301, и
	// молчаливого умолчания больше нет.
	if p.Accept == "" {
		if p.Barrier != "" {
			return "", fmt.Errorf("--barrier без --accept не имеет смысла: барьер называют вместе с видом")
		}
		return "", fmt.Errorf("нужен --accept agent|mixed|user: вид приёмки обязателен, agent это агентский вид")
	}
	if !validAccept(p.Accept) {
		return "", fmt.Errorf("--accept %q не из {agent, mixed, user}", p.Accept)
	}
	if p.Accept == acceptAgent {
		if p.Barrier != "" {
			return "", fmt.Errorf("--barrier не имеет смысла у агентского вида: барьер называют там, где вида нет")
		}
	} else {
		if p.Barrier == "" {
			return "", fmt.Errorf("для --accept %s нужен --barrier <ключ> из шести барьеров (глаза, доступ, необратимость, секрет, согласие, событие)", p.Accept)
		}
		if _, ok := acceptBarriers[p.Barrier]; !ok {
			return "", fmt.Errorf("--barrier %q не из закрытого списка (глаза, доступ, необратимость, секрет, согласие, событие)", p.Barrier)
		}
	}
	if err := checkType(p.Type); err != nil {
		return "", err
	}
	total, parts, err := parseRank(p.Rank)
	if err != nil {
		return "", err
	}
	cost := p.Cost
	if cost == "" {
		cost = "-"
	}
	if err := checkCost(cost); err != nil {
		return "", err
	}
	status := normalizeStatus(p.Status)
	if status == "" {
		status = SectBacklog
	}
	sec, ok := b.Sects[status]
	if !ok {
		return "", fmt.Errorf("неизвестный статус %q, жду backlog / in-progress / check / blocked", status)
	}
	title := p.Title + acceptSuffix(p.Accept)
	if status == SectBlocked {
		// Новая строка в Blocked обходила бы тот же инвариант, что и move из
		// Backlog (RULES.board.md, «Трекинг задач» п. 4): заблокированной
		// бывает только начатая задача, а новую блокировать нечему. Причина
		// отсюда ушла вместе со статусом, скобки в ней проверяет move.
		return "", fmt.Errorf("новую задачу в blocked не заводят: блокировать нечего, пока её не взяли в работу; строка ждёт в Backlog, а зависимость от своей задачи ставится через taskctl dep add")
	}
	// Отказ по DoD стоит до переноса: черновик task/bug обязан нести «## DoD»
	// к моменту оформления (LLD DK-133, решение 4), и упавшая на кривом ранге
	// или без DoD команда не оставляет файл на новом месте без строки на доске.
	if p.ID != "" && needsDoD(p.Type, p.Title) {
		if _, found, ok := readSectionFromPath(draftPath(root, p.ID), dodHeading); ok && !found {
			return "", fmt.Errorf("у черновика %s нет заголовка «## DoD»: ворота заведения спрашивают, чем кончается работа, допишите раздел в черновик", p.ID)
		}
	}
	rankLine := fmt.Sprintf("`%d+%d+%d+%d+%d = %d`, %s.", parts[0], parts[1], parts[2], parts[3], parts[4], total, bucket(total))
	promoted, staged, err := promoteDraft(root, id, p.Title, rankLine)
	if err != nil {
		return "", err
	}
	// Файл задачи заводит сам add (LLD DK-133, решение 4): однострочного
	// бэклога не остаётся, ячейка строки ссылается на файл с минуты заведения.
	row := &Row{ID: id, Num: mustNum(id), Title: title, Type: p.Type, P: bucket(total), RTotal: total, RParts: parts, Cost: cost}
	if _, err := ensureTaskFile(root, id, row); err != nil {
		return "", err
	}
	rel := fmt.Sprintf("tasks/%s.md", id)
	taskFile := filepath.Join("docs", rel)
	link := fmt.Sprintf("[%s](%s)", rel, rel)
	// При --link связь с целью живёт в файле задачи, а не в ячейке: ссылка на
	// файл цели уходит в болванку под заголовок, ячейку занимает файл задачи.
	// Состав цели и сегодня читается из «Задачи цели» файла цели, так что
	// переезд ссылки ничего не ломает, а связь читается одним местом раньше.
	if gl := wrapLink(p.Link); gl != "" {
		if err := appendUnderHeading(taskFileAbs(root, id), "Цель: "+gl); err != nil {
			return "", err
		}
	}
	// Не агентский вид держит причину в файле задачи: add дописывает в него
	// раздел «Приёмка» (LLD DK-292, решение 3). Исполнитель дописывает per
	// строку обхода исход, имена обходов лежат в ACCEPTANCE.md (задача DK-299).
	// Раздел соседствует с заголовками болванки: разделы дополняют друг друга,
	// а не перезаписывают, а место ему выбирает форма (TASKFORM.md), иначе он
	// встал бы за «Ходом работы», который по форме идёт позже. Есть ли раздел, спрашивается у readSectionFromPath
	// тем же порядком, что у gate и lint: заголовок «## Приёмка» внутри блока
	// кода это цитата, а не раздел, и поиск подстрокой считал её разделом (DK-329).
	if p.Accept != "" && p.Accept != acceptAgent {
		abs := taskFileAbs(root, id)
		section := fmt.Sprintf("%s\n\n- вид: %s\n- барьер «%s»:\n", acceptanceHeading, p.Accept, p.Barrier)
		if _, found, _ := readSectionFromPath(abs, acceptanceHeading); !found {
			body, err := os.ReadFile(abs)
			if err != nil {
				return "", err
			}
			body = insertFormSection(body, acceptanceHeading, section)
			if err := os.WriteFile(abs, body, 0o644); err != nil {
				return "", err
			}
		}
	}
	if err := checkCell("ссылка", link); err != nil {
		return "", err
	}
	row.Link = link
	if err := insertRowLine(b, sec, row, formatRow(row)); err != nil {
		return "", err
	}
	// Свежая строка получает свой хвост поправок сразу: бонус за цену
	// производный, и строка без него легла бы на доску уже красной по lint.
	if _, b, err = rehydrate(b); err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	paths := []string{filepath.Join("docs", "TASKS.md"), taskFile}
	if promoted && staged {
		// Обе стороны переноса едят pathspec, только когда перенос шёл через
		// git mv: неотслеживаемый черновик git не знает, и pathspec по
		// исчезнувшему пути drafts/ ронял бы коммит. Сам файл уже на новом
		// месте через rename, и в коммит попадает как вновь добавленный.
		paths = append(paths, filepath.Join("docs", "tasks", "drafts", id+".md"))
	}
	tail, err := p.Commit.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s заведена в %s: %s, R=%d", id, status, row.P, total)
	if promoted {
		msg += ", черновик перенесён в docs/tasks/" + id + ".md"
	}
	return msg + tail, nil
}

func mustNum(id string) int {
	m := idRe.FindStringSubmatch(id)
	n := 0
	fmt.Sscanf(m[2], "%d", &n)
	return n
}

// Содержимое причины не пускает «[»: иначе при перепутанном порядке
// суффиксов («[блок: ...] [после ...]» вместо «[после ...] [блок: ...]»)
// жадный класс дотягивался до конца строки и съедал маркер зависимости
// целиком, делая его невидимым для lint, move и close.
var blockSufRe = regexp.MustCompile(`\s*\[блок: [^|\[]*\]\s*$`)

func cmdMove(root, id, target, reason string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "move"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	target = normalizeStatus(target)
	if _, ok := b.Sects[target]; !ok {
		return "", fmt.Errorf("неизвестный статус %q, жду backlog / in-progress / check / blocked", target)
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	if err := needTaskFile(root, id); err != nil {
		return "", err
	}
	if row.Sect == target {
		return "", fmt.Errorf("%s уже в %s", id, target)
	}
	if target == SectInProgress {
		arch, err := LoadArchive(archivePath(root))
		if err != nil {
			return "", err
		}
		_, deps, _, _, _ := splitTitle(row.Title)
		for _, d := range deps {
			if !arch.has(d) {
				return "", fmt.Errorf("%s зависит от незакрытой %s, нельзя перевести в in-progress", id, d)
			}
		}
	}
	if target == SectCheck {
		if err := checkGate(root, row); err != nil {
			return "", err
		}
	}
	// Заблокированной бывает только начатая задача (RULES.board.md, «Трекинг
	// задач» п. 4): нетронутую строку разблокировать некому, и Blocked у неё
	// значил бы просто «не начали», как весь Backlog. Ожидание своей же задачи
	// это маркер «после», для него статус не меняют.
	if target == SectBlocked && row.Sect == SectBacklog {
		return "", fmt.Errorf("%s ещё не в работе, блокировать нечего: задача ждёт в Backlog, а зависимость от своей задачи ставится через taskctl dep add %s <ID>", id, id)
	}
	line := b.Lines[row.LineIdx]
	moved := *row
	switch {
	case target == SectBlocked:
		if strings.TrimSpace(reason) == "" {
			return "", fmt.Errorf("для blocked обязателен --reason, одна строка почему")
		}
		if err := checkReason(reason); err != nil {
			return "", err
		}
		if err := checkParkPrefix(reason); err != nil {
			return "", err
		}
		if strings.HasPrefix(reason, "вопрос:") {
			if err := checkQuestionCeiling(b, root, id); err != nil {
				return "", err
			}
		}
		moved.Title = row.Title + " [блок: " + reason + "]"
	case row.Sect == SectBlocked:
		// На выходе из Blocked причина в заголовке больше не нужна.
		moved.Title = blockSufRe.ReplaceAllString(row.Title, "")
	}
	// Перевод в Check гасит признак провала проверки сам: задача снова ждёт
	// проверки на живом проде, значит прод починен. Именно этот move зовут
	// shipctl merge и ship после удачного выката, поэтому дочищать признак
	// руками после починки не приходится.
	quenched := ""
	if base, deps, acceptSuf, failSuf, blockSuf := splitTitle(moved.Title); target == SectCheck && failSuf != "" {
		moved.Title = joinTitle(base, deps, acceptSuf, "", blockSuf)
		quenched = ", признак провала снят"
	}
	if moved.Title != row.Title {
		line = formatRow(&moved)
	}
	b2, err := relocate(b, row, target, &moved, line)
	if err != nil {
		return "", err
	}
	if err := b2.Save(); err != nil {
		return "", err
	}
	// Задача доехала до Check или встала на блокере: повод громкий, и звучит
	// он тут же, где меняется статус, а не в shipctl или где-то ещё, кто бы
	// move ни позвал (RULES.board.md, «Ветки, ревью и деплой» п. 8).
	var note string
	base, _, _, _, _ := splitTitle(row.Title)
	switch target {
	case SectCheck:
		note = notify(root, reasonCheck, id, fmt.Sprintf("%s: %s в Check", filepath.Base(root), id), base)
	case SectBlocked:
		note = notify(root, reasonBlocked, id, fmt.Sprintf("%s: %s на блокере", filepath.Base(root), id), reason)
	}
	// Пакет этапов уезжает в файл задачи до открытия нового: смена статуса
	// закрывает всё, что накопил конвейер, и ожидание снаружи начинается уже
	// новым пакетом.
	now := time.Now()
	doc, stages := flushStages(root, id, now)
	openOutside(root, id, target, reason, now)
	paths := []string{filepath.Join("docs", "TASKS.md")}
	if doc != "" {
		paths = append(paths, doc)
	}
	tail, err := c.apply(root, paths)
	if err != nil {
		return "", err
	}
	// Правка промптов доезжает до пользователя иначе, чем правка кода: она
	// меняет поведение агентов, и проверяется прогоном стенда, а не тестами
	// ветки. Повод называется тут, на последнем рубеже, где задача ещё на
	// виду, и без отказа.
	hint := ""
	if target == SectCheck {
		if h := promptHint(root, id); h != "" {
			hint = h + "\n"
		}
	}
	return fmt.Sprintf("%s: %s -> %s%s%s%s%s\n%s%s", id, row.Sect, target, quenched, stages, tail, note, hint, nextAfterMove(root, id, target)), nil
}

// relocate вырезает строку из её секции и вставляет line в секцию target,
// отдавая перечитанную доску: после выреза индексы строк уезжают, поэтому
// разбор идёт заново.
func relocate(b *Board, row *Row, target string, moved *Row, line string) (*Board, error) {
	b.remove(row.LineIdx)
	b2, err := parseLines(b.Path, b.Lines)
	if err != nil {
		return nil, err
	}
	if err := insertRowLine(b2, b2.Sects[target], moved, line); err != nil {
		return nil, err
	}
	return b2, nil
}

type SetParams struct {
	ID, Title, Type, Rank, Cost, Link, Accept string
	Commit                                          CommitOpts
}

func cmdSet(root string, p SetParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if p.Title == "" && p.Type == "" && p.Rank == "" && p.Cost == "" && p.Link == "" && p.Accept == "" {
		return "", fmt.Errorf("нечего менять, жду --title, --type, --rank, --cost, --link и/или --accept")
	}
	if err := boardGuard(root, "set"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(p.ID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", p.ID)
	}
	if err := needTaskFile(root, p.ID); err != nil {
		return "", err
	}
	var changes []string
	if p.Title != "" {
		if err := checkCell("заголовок", p.Title); err != nil {
			return "", err
		}
		title := p.Title
		// У строки с зависимостью, приёмкой, провалом проверки и/или причиной
		// блокировки эти хвосты живут в заголовке, при замене текста они
		// переносятся в новый (в исходном порядке: «после», «приёмка», «провал»,
		// «блок»).
		_, deps, acceptSuf, failSuf, blockSuf := splitTitle(row.Title)
		if len(deps) > 0 && !strings.Contains(title, "[после") {
			title = joinTitle(title, deps, "", "", "")
		}
		if acceptSuf != "" && !strings.Contains(title, "[приёмка:") {
			title += acceptSuf
		}
		if failSuf != "" && !strings.Contains(title, "[провал:") {
			title += failSuf
		}
		if blockSuf != "" && !strings.Contains(title, "[блок:") {
			title += blockSuf
		}
		if title != row.Title {
			changes = append(changes, "заголовок")
			row.Title = title
		}
	}
	if p.Accept != "" {
		// Пересмотр вида по ходу работы (LLD DK-292, решение 4): значение в
		// строке доски правит только set --accept, причину (обход или барьер)
		// исполнитель дописывает в раздел «Приёмка» файла задачи сам. Барьер
		// «согласие» повышения не подлежит, и это единственный запрет.
		if !validAccept(p.Accept) {
			return "", fmt.Errorf("--accept %q не из {agent, mixed, user}", p.Accept)
		}
		old := acceptOf(row.Title)
		if p.Accept == acceptAgent && old != acceptAgent {
			// Повышение до агентского: непреходящий барьер «согласие» повышения
			// не подлежит (LLD DK-292, решение 1: обхода у него нет по
			// определению).
			if text, found, _ := acceptanceSection(root, p.ID); found {
				if bar, _ := parseAcceptance(text); bar == "согласие" {
					return "", fmt.Errorf("%s: барьер «согласие» не подлежит повышению (LLD DK-292, решение 1: у него нет обхода по определению)", p.ID)
				}
			}
		}
		if p.Accept != old {
			base, deps, _, failSuf, blockSuf := splitTitle(row.Title)
			row.Title = joinTitle(base, deps, acceptSuffix(p.Accept), failSuf, blockSuf)
			changes = append(changes, fmt.Sprintf("вид %s -> %s", old, p.Accept))
		}
	}
	if p.Type != "" {
		if err := checkType(p.Type); err != nil {
			return "", err
		}
		if p.Type != row.Type {
			changes = append(changes, fmt.Sprintf("тип %s -> %s", row.Type, p.Type))
			row.Type = p.Type
		}
	}
	if p.Cost != "" {
		if err := checkCost(p.Cost); err != nil {
			return "", err
		}
		if p.Cost != row.Cost {
			changes = append(changes, fmt.Sprintf("цена %s -> %s", row.Cost, p.Cost))
			row.Cost = p.Cost
		}
	}
	if p.Link != "" {
		link := wrapLink(p.Link)
		if err := checkCell("ссылка", link); err != nil {
			return "", err
		}
		if link != row.Link {
			changes = append(changes, "ссылка")
			row.Link = link
		}
	}
	if p.Rank != "" {
		total, parts, err := parseRank(p.Rank)
		if err != nil {
			return "", err
		}
		// Сравнивается собственная сумма: итог в ячейке несёт ещё и поправки,
		// и сверка с ним объявляла бы изменением каждый повтор той же разбивки.
		if total != row.ROwn || parts != row.RParts {
			changes = append(changes, fmt.Sprintf("разбивка %d -> %d", row.ROwn, total))
			row.RTotal, row.ROwn, row.RParts = total, total, parts
		}
	}
	if len(changes) == 0 {
		return "", fmt.Errorf("у %s уже такие значения, менять нечего", p.ID)
	}
	b.Lines[row.LineIdx] = formatRow(row)
	// В Backlog позиция строки зависит от ранга, и правка ранга или цены
	// двигает не только эту строку: поправки считаются по всей доске, поэтому
	// пересчёт и перестановку ведёт rehydrate, а не вставка одной строки.
	moves, b, err := rehydrate(b)
	if err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := p.Commit.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %s%s%s", p.ID, strings.Join(changes, ", "), movesTail(moves), tail), nil
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

type CloseParams struct {
	ID, Commits, Date, Link string
	Commit                  CommitOpts
}

func cmdClose(root string, p CloseParams) (string, error) {
	if err := p.Commit.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "close"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	row := b.find(p.ID)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", p.ID)
	}
	if arch.has(p.ID) {
		return "", fmt.Errorf("%s уже есть в архиве", p.ID)
	}
	// Закрыть задачу с непогашенным провалом значит увезти в архив сломанный
	// прод: строка с доски уйдёт, и очередь выката отпустит его молча.
	if _, _, _, failSuf, _ := splitTitle(row.Title); failSuf != "" {
		return "", fmt.Errorf("у %s непогашенный провал проверки%s: сначала починить прод (shipctl revert %s либо форвард-фикс и shipctl merge %s), а если он уже починен мимо shipctl, снять признак: taskctl fail %s --clear",
			p.ID, failSuf, p.ID, p.ID, p.ID)
	}
	// Агентский вид требует непустого раздела «Проверка» в файле задачи (LLD
	// DK-292, решение 4): пустое закрытие агентской задачи запрещено, и это
	// машинный рубеж против фиктивного сценария из повтора тестов ветки. Не
	// агентский вид тут не трогается, его держат ворота move check.
	if kind := acceptOf(row.Title); kind == acceptAgent {
		if err := closeAgentGate(root, p.ID); err != nil {
			return "", err
		}
	}
	// Сценарий прогоняет не автор правки (DK-642): прогон под именем
	// исполнителя разработки закрытия не даёт, отказ называет имя.
	if err := closeVerifyGate(root, p.ID); err != nil {
		return "", err
	}
	date := p.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !dateRe.MatchString(date) {
		return "", fmt.Errorf("дата %q не вида ГГГГ-ММ-ДД", date)
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("дата %q не разбирается: %v", date, err)
	}
	var commits []string
	if p.Commits != "" {
		for _, c := range strings.Split(p.Commits, ",") {
			c = strings.TrimSpace(c)
			if !commitRe.MatchString(c) {
				return "", fmt.Errorf("%q не похоже на хеш коммита", c)
			}
			commits = append(commits, c)
		}
	}
	// Пакет этапов уезжает в файл задачи до архивации: после git mv писать в
	// него уже некуда, а ожидание снаружи, открытое переводом в Check, иначе
	// пропало бы вместе с записью.
	_, stagesTail := flushStages(root, p.ID, time.Now())
	year := date[:4]
	moved := ""
	var changedFiles []string
	taskFile := filepath.Join(root, "docs", "tasks", p.ID+".md")
	if _, err := os.Stat(taskFile); err == nil {
		dst := filepath.Join(root, "docs", "tasks", "archive", year, p.ID+".md")
		if _, err := gitMv(root, taskFile, dst); err != nil {
			return "", err
		}

		oldBaseDir := filepath.Join(root, "docs", "tasks")
		newBaseDir := filepath.Join(root, "docs", "tasks", "archive", year)
		if err := rewriteLinksInFile(dst, oldBaseDir, newBaseDir); err != nil {
			return "", err
		}

		var err2 error
		changedFiles, err2 = findAndRewriteReferencesToFile(root, taskFile, dst)
		if err2 != nil {
			return "", err2
		}

		moved = fmt.Sprintf("tasks/archive/%s/%s.md", year, p.ID)
	}
	linkCell := p.Link
	if linkCell == "" {
		var parts []string
		if moved != "" {
			parts = append(parts, fmt.Sprintf("[%s](%s)", moved, moved))
		}
		for _, c := range commits {
			parts = append(parts, "`"+c+"`")
		}
		if len(parts) == 0 {
			parts = append(parts, row.Link)
		}
		linkCell = strings.Join(parts, ", ")
	}
	if err := checkCell("ссылка", linkCell); err != nil {
		return "", err
	}
	// В архивную строку маркер зависимости не попадает: закрытая задача
	// саму себя ждать больше не заставит. Суффикс приёмки переживает закрытие
	// наравне с «[блок: ...]» (LLD DK-292, решение 3): вид это свойство самой
	// задачи, а не её положения в очереди, и без него сводке нечего считать.
	archBase, _, archAcceptSuf, _, archBlockSuf := splitTitle(row.Title)
	cells := []string{p.ID, joinTitle(archBase, nil, archAcceptSuf, "", archBlockSuf), row.Type, row.P, date, linkCell}
	if err := appendArchiveRow(archivePath(root), cells); err != nil {
		return "", err
	}
	// Закрытая зависимость выполнена: снять «[после <ID>]» со всех
	// остальных строк доски, протухшие маркеры на ней не живут.
	var depTouched []string
	for _, r := range b.Rows {
		if r.ID == p.ID {
			continue
		}
		base, deps, acceptSuf, failSuf, blockSuf := splitTitle(r.Title)
		idx := -1
		for i, d := range deps {
			if d == p.ID {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue
		}
		deps = append(deps[:idx], deps[idx+1:]...)
		r.Title = joinTitle(base, deps, acceptSuf, failSuf, blockSuf)
		b.Lines[r.LineIdx] = formatRow(r)
		depTouched = append(depTouched, r.ID)
	}
	b.remove(row.LineIdx)
	// Закрытая задача больше никого не подтягивает: наследовавшие её ранг
	// строки переезжают вниз тем же заходом (DK-428).
	moves, b, err := rehydrate(b)
	if err != nil {
		return "", err
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	paths := []string{filepath.Join("docs", "TASKS.md"), filepath.Join("docs", "TASKS-archive.md")}
	if moved != "" {
		paths = append(paths, filepath.Join("docs", "tasks", p.ID+".md"), filepath.Join("docs", moved))
	}
	paths = append(paths, changedFiles...)
	tail, err := p.Commit.apply(root, paths)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s закрыта %s, строка в архиве%s", p.ID, date, stagesTail)
	if moved != "" {
		msg += ", файл задачи в " + moved
	}
	if len(depTouched) > 0 {
		msg += ", маркер «после» снят у: " + strings.Join(depTouched, ", ")
	}
	msg += movesTail(moves)
	return msg + tail + shipDrainNote(root) + "\n" + nextAfterClose(), nil
}

// shipDrainNote зовёт разлив поезда сразу после закрытия задачи (LLD DK-306,
// решение 4): очередь выката освободилась, и копящийся поезд надо увозить,
// пока есть кому увидеть его вывод. close не держит ship.lock (его держат
// только команды shipctl), поэтому дедлока нет. Вызов best-effort: доска уже
// записана и запушена, и держать закрытие ради чужого выката нельзя, провал
// дописывается в отчёт close предупреждением, а не ошибкой.
func shipDrainNote(root string) string {
	if _, err := exec.LookPath("shipctl"); err != nil {
		return "\nразлив не позван: shipctl не найден в PATH, поставить набор утилит devkit (python3 ~/projects/devkit/tools/devkitctl/devkitctl.py update)"
	}
	out, err := exec.Command("shipctl", "-C", root, "ship", "--drain").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("\nпредупреждение: разлив упал: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	if note := strings.TrimSpace(string(out)); note != "" {
		return "\n" + note
	}
	return ""
}

// gitMv переносит файл через git mv, а вне git-репозитория (или для
// неотслеживаемого файла) обычным rename. Возвращаемое staged=true значит, что
// перенос шёл через git mv и git знал исходный путь: только тогда исходный путь
// уместен в pathspec коммита. На неотслеживаемом файле git mv отбивается,
// срабатывает rename, и pathspec по исчезнувшему пути ронял бы коммит.
func gitMv(root, from, to string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return false, err
	}
	cmd := exec.Command("git", "-C", root, "mv", from, to)
	if out, err := cmd.CombinedOutput(); err != nil {
		if renameErr := os.Rename(from, to); renameErr != nil {
			return false, fmt.Errorf("git mv: %v (%s); rename: %v", err, strings.TrimSpace(string(out)), renameErr)
		}
		return false, nil
	}
	return true, nil
}

// fullLinkRe находит markdown-ссылки вида [текст](цель)
var fullLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)

// codeSpanRe находит инлайн-код в обратных кавычках: ссылка в примере команды
// это текст, а не ссылка.
var codeSpanRe = regexp.MustCompile("`+[^`]*`+")

// fenceRe находит забор блока кода: по CommonMark отступ не больше трёх
// пробелов, дальше три и больше символов ` или ~ подряд.
var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// rewriteLinksSkippingCodeBlocks переписывает ссылки в тексте функцией resolve,
// которая получает цель ссылки без якоря и возвращает новый путь (пустая строка
// значит «оставить как было»). Содержимое блоков кода не трогается.
func rewriteLinksSkippingCodeBlocks(text string, resolve func(path string) string) string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, line := range lines {
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			// Закрывает только забор из того же символа и не короче
			// открывающего, иначе вложенный ``` оборвал бы внешний ````.
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(line[len(m[0]):]) == "":
				fence = ""
			}
			continue
		}
		if fence == "" {
			lines[i] = rewriteLinksInLine(line, resolve)
		}
	}
	return strings.Join(lines, "\n")
}

// rewriteLinksInLine отделяет у каждой ссылки якорь, отбрасывает пустые,
// внешние и mailto-цели и собирает ссылку обратно с путём от resolve.
func rewriteLinksInLine(line string, resolve func(path string) string) string {
	var out strings.Builder
	pos := 0
	for _, span := range codeSpanRe.FindAllStringIndex(line, -1) {
		out.WriteString(rewriteLinksOutsideCode(line[pos:span[0]], resolve))
		out.WriteString(line[span[0]:span[1]])
		pos = span[1]
	}
	out.WriteString(rewriteLinksOutsideCode(line[pos:], resolve))
	return out.String()
}

func rewriteLinksOutsideCode(line string, resolve func(path string) string) string {
	return fullLinkRe.ReplaceAllStringFunc(line, func(match string) string {
		m := fullLinkRe.FindStringSubmatch(match)
		if m == nil {
			return match
		}
		path, anchor, hasAnchor := strings.Cut(m[2], "#")
		if path == "" || strings.Contains(path, "://") || strings.HasPrefix(path, "mailto:") {
			return match
		}
		newPath := resolve(path)
		if newPath == "" {
			return match
		}
		if hasAnchor {
			anchor = "#" + anchor
		}
		return fmt.Sprintf("[%s](%s%s)", m[1], newPath, anchor)
	})
}

// rewriteLinksInFile переписывает относительные ссылки в файле при его переносе.
// Ссылка, которая разрешалась от oldBaseDir, переписывается так, чтобы разрешаться
// от newBaseDir в один и тот же целевой файл. Ссылки внутри блоков кода пропускаются.
func rewriteLinksInFile(filePath, oldBaseDir, newBaseDir string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	result := rewriteLinksSkippingCodeBlocks(string(content), func(path string) string {
		newPath, err := filepath.Rel(newBaseDir, filepath.Join(oldBaseDir, path))
		if err != nil {
			return ""
		}
		return newPath
	})

	return os.WriteFile(filePath, []byte(result), 0o644)
}

// skipDirs это директории, которые пропускаются при поиске файлов
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true,
	"local-docs": true, ".venv": true, "venv": true, "__pycache__": true,
	".idea": true, ".vscode": true,
}

// findAndRewriteReferencesToFile находит все markdown-файлы со ссылками на
// oldPath и переписывает их на newPath. Возвращает список изменённых файлов.
// Ссылки внутри блоков кода пропускаются.
func findAndRewriteReferencesToFile(root, oldPath, newPath string) ([]string, error) {
	var changed []string
	oldPath = filepath.Clean(oldPath)
	newPath = filepath.Clean(newPath)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fileDir := filepath.Dir(path)
		var pathChanged bool

		newContent := rewriteLinksSkippingCodeBlocks(string(content), func(linkPath string) string {
			if filepath.Join(fileDir, linkPath) != oldPath {
				return ""
			}
			newLinkPath, err := filepath.Rel(fileDir, newPath)
			if err != nil {
				return ""
			}
			pathChanged = true
			return newLinkPath
		})

		if pathChanged {
			if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			changed = append(changed, rel)
		}

		return nil
	})

	return changed, err
}

func cmdSort(root string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "sort"); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	before := append([]string{}, b.Lines...)
	legacy := b.Legacy
	// Сортировка это же и пересчёт поправок: хвост скобки производный, и
	// команда, переставляющая Backlog по R_eff, обязана сперва этот R_eff
	// посчитать (DK-428).
	moves, b, err := rehydrate(b)
	if err != nil {
		return "", err
	}
	changed := 0
	for i, ln := range b.Lines {
		if i < len(before) && before[i] != ln {
			changed++
		}
	}
	// Разбор доски старого формата уже перевёл строки в памяти, sort тогда
	// сохраняет файл даже без перестановок: это штатный способ миграции.
	if changed == 0 && !legacy {
		return "Backlog уже отсортирован", nil
	}
	if err := b.Save(); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "TASKS.md")})
	if err != nil {
		return "", err
	}
	if changed == 0 {
		return "доска переведена в формат с колонкой «Цена»" + tail, nil
	}
	return fmt.Sprintf("Backlog пересортирован, строк переставлено: %d%s%s", changed, movesTail(moves), tail), nil
}

func cmdID(root string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	return nextID(b, arch, drafts)
}

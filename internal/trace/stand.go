package trace

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/taskform"
)

// Проверяющий стенда и его машинерия переехали из пятых ворот слияния shipctl
// (LLD DK-805, решения 1, 2 и 3; LLD DK-972, решение 6). Текст, который читают
// агенты, правится теми же ветками, что и код, а меряется он не тестом, а
// прогоном стенда: правило, которое сессия перестала держать, по диффу не
// видно. След требует на каждый тронутый раздел агентского файла сценарий
// стенда и свежий отпечаток прогона в файле задачи. Проверяется наличие
// следа, а не его честность: пару раскладок собирает автор, и подставить туда
// чужое дерево можно так же, как закоммитить пустой тест.
// ScenarioDir это каталог сценариев стенда в дереве devkit. Живёт константой
// здесь, потому что отказ Stand печатает путь для человека, а фикстуры прогонов
// стенда собирают сценарии по тому же пути.
const ScenarioDir = "tools/obeycheck/scenarios"

const (
	baseOld   = "старый"
	baseEmpty = "пусто"
	// headSection это имя, под которым след держит шапку файла: строки до
	// первого заголовка. Сценарием оно покрывается только предметом на весь
	// файл, потому что раздела с таким заголовком в файле нет.
	headSection = "шапка"
)

// standScenario это сценарий стенда, прочитанный из дерева ветки: имя файла
// без расширения и предметы из ключа шапки. Сценарий, заведённый той же
// веткой, считается, поэтому дерево берётся ветки, а не main.
type standScenario struct {
	id   string
	subs []obey.Subject
}

// touched это тронутый веткой раздел агентского файла вместе с назначенной ему
// базой прогона.
type touched struct {
	file    string
	section string
	base    string
}

// Stand отказывает ветке, которая правит текст для агентов без сценария
// стенда или без свежего следа прогона. Погашение живёт пометкой «- Исключение:
// стенд (причина)»: правка формулировки и вычитка поводом для прогона не
// считаются (порог повода в скилле prompt-test), а удаление текста
// доказывается прогоном ревизии с базой «пусто». Проект не devkit след не
// знает: сценарии живут в devkit, и спрашивать их с чужого дерева не с чего.
func Stand(root, main, branch, id, doc string) error {
	files, deleted, err := agentDiff(root, main, branch)
	if err != nil || (len(files) == 0 && !deleted) {
		return nil
	}
	if list, err := git(root, "ls-tree", "--name-only", branch, ScenarioDir+"/"); err != nil || strings.TrimSpace(list) == "" {
		return nil
	}
	if taskform.Exception(doc, taskform.GateStand) {
		return nil
	}
	read := TreeReader(root, branch)
	scens, err := branchScenarios(root, branch, read)
	if err != nil {
		return err
	}
	marks := taskform.StandMarks(doc)
	for _, f := range files {
		secs, err := touchedSections(root, main, branch, read, f)
		if err != nil {
			return err
		}
		for _, sec := range secs {
			if err := standCovered(read, scens, marks, sec, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// agentDiff отбирает из диффа ветки против main файлы, чей текст едет в
// контекст агента. Удалённые файлы едут отдельным флагом: разделов у них нет,
// сценария на них в дереве ветки уже не осталось, и спрашивать след не с чего,
// но осиротевший предмет соседнего сценария ловится именно на них.
func agentDiff(root, main, branch string) (files []string, deleted bool, err error) {
	out, err := git(root, "diff", "--name-status", "--no-renames", main+"..."+branch)
	if err != nil {
		// Ошибка git тут не валит проверку по тому же правилу, что у следа
		// тестов: проверяющему хватит общего случая.
		return nil, false, err
	}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(ln))
		if len(f) < 2 || !obey.AgentFile(f[len(f)-1]) {
			continue
		}
		if strings.HasPrefix(f[0], "D") {
			deleted = true
			continue
		}
		files = append(files, f[len(f)-1])
	}
	return files, deleted, nil
}

// TreeReader читает файлы из дерева ветки через git: слияние идёт из основного
// чекаута, стоящего на main, и файлов ветки на диске там нет. Прочитанное
// кешируется: предмет одного файла спрашивают все сценарии подряд.
func TreeReader(root, ref string) obey.Reader {
	type answer struct {
		text string
		err  error
	}
	cache := map[string]answer{}
	return func(p string) (string, error) {
		if a, ok := cache[p]; ok {
			return a.text, a.err
		}
		text, err := git(root, "show", ref+":"+p)
		cache[p] = answer{text, err}
		return text, err
	}
}

// branchScenarios читает сценарии стенда из дерева ветки и разбирает их ключ
// «предмет» тем же кодом, что и стенд. Предмет, указывающий в никуда, след
// отбивает тем же текстом, что и загрузка сценариев: сценарий, осиротевший от
// переименования или удаления, зеленел бы на тексте, которого больше нет.
func branchScenarios(root, branch string, read obey.Reader) ([]standScenario, error) {
	list, err := git(root, "ls-tree", "-r", "--name-only", branch, ScenarioDir+"/")
	if err != nil {
		return nil, nil
	}
	var out []standScenario
	for _, p := range strings.Split(list, "\n") {
		p = strings.TrimSpace(p)
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		text, err := git(root, "show", branch+":"+p)
		if err != nil {
			continue
		}
		value, ok := scenarioSubjectKey(text)
		if !ok {
			continue
		}
		subs, err := obey.ParseSubjects(value)
		if err != nil {
			return nil, fmt.Errorf("сценарий %s не разбирается: %v", p, err)
		}
		if err := obey.VerifyAllFrom(read, subs); err != nil {
			return nil, fmt.Errorf("сценарий %s остался без предмета: %v; перепривязать его той же веткой или убрать", p, err)
		}
		out = append(out, standScenario{id: strings.TrimSuffix(filepath.Base(p), ".md"), subs: subs})
	}
	return out, nil
}

// scenarioSubjectKey достаёт значение ключа «предмет» из шапки сценария: она
// кончается первым заголовком секции. Сценарий без ключа проверяющий
// пропускает, за форму шапки отвечает разбор самого стенда.
func scenarioSubjectKey(text string) (string, bool) {
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "## ") {
			return "", false
		}
		key, value, ok := strings.Cut(strings.TrimSpace(ln), ":")
		if ok && strings.TrimSpace(key) == obey.Key {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// touchedSections считает разделы файла, тронутые веткой, и назначает каждому
// базу прогона. Строка ханка относится к ближайшему заголовку «## » выше неё в
// новой редакции, строки до первого заголовка к шапке. Ханк чистого удаления
// разбирается по старой редакции: снятая строка правила меняет поведение не
// меньше дописанной, и уцелевший раздел считается тронутым. Исчезнувший раздел
// уходит из проверки вместе с удалёнными файлами. База назначается разделу, а
// не файлу: новый текст приходит в ядро правил новым разделом, и база «старый»
// пропускала бы его зачётом «не хуже».
func touchedSections(root, main, branch string, read obey.Reader, file string) ([]touched, error) {
	diff, err := git(root, "diff", "-U0", main+"..."+branch, "--", file)
	if err != nil {
		return nil, nil
	}
	newText, err := read(file)
	if err != nil {
		return nil, nil
	}
	newHeads := headings(newText)
	oldText, oldErr := git(root, "show", main+":"+file)
	oldHeads := headings(oldText)
	var out []touched
	seen := map[string]bool{}
	add := func(section string) {
		if seen[section] {
			return
		}
		seen[section] = true
		base := baseOld
		if oldErr != nil || (section != headSection && !hasHead(headNames(oldHeads), section)) {
			base = baseEmpty
		}
		out = append(out, touched{file: file, section: section, base: base})
	}
	for _, h := range diffHunks(diff) {
		if h.newCount > 0 {
			// Ханк длиннее одной строки задевает столько разделов, сколько
			// заголовков в него попало: правка на стыке разделов и новый
			// раздел в хвосте файла приходят одним ханком.
			for _, s := range spanned(newHeads, h.newStart, h.newStart+h.newCount-1) {
				add(s)
			}
			continue
		}
		// Чистое удаление: разделы берутся из старой редакции и считаются
		// тронутыми, только если уцелели в новой.
		for _, s := range spanned(oldHeads, h.oldStart, h.oldStart+h.oldCount-1) {
			if s == headSection || hasHead(headNames(newHeads), s) {
				add(s)
			}
		}
	}
	return out, nil
}

// heading это заголовок второго уровня вместе с номером строки, на которой он
// стоит.
type heading struct {
	line int
	name string
}

// headings собирает заголовки «## » файла вне ограждённых блоков: заголовок
// внутри ограды это чужой пример, а не раздел.
func headings(text string) []heading {
	var out []heading
	var f obey.Fence
	for i, ln := range strings.Split(text, "\n") {
		if f.Step(ln) {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			out = append(out, heading{line: i + 1, name: strings.TrimSpace(ln[3:])})
		}
	}
	return out
}

// sectionAt называет раздел, которому принадлежит строка: ближайший заголовок
// выше неё, а до первого заголовка это шапка.
func sectionAt(heads []heading, line int) string {
	name := headSection
	for _, h := range heads {
		if h.line > line {
			break
		}
		name = h.name
	}
	return name
}

// spanned называет разделы, которых касается диапазон строк: раздел его начала
// и каждый заголовок внутри диапазона.
func spanned(heads []heading, from, to int) []string {
	out := []string{sectionAt(heads, from)}
	for _, h := range heads {
		if h.line > from && h.line <= to {
			out = append(out, h.name)
		}
	}
	return out
}

// headNames отдаёт имена заголовков списком.
func headNames(heads []heading) []string {
	var out []string
	for _, h := range heads {
		out = append(out, h.name)
	}
	return out
}

// hasHead отвечает, стоит ли раздел с таким заголовком в списке.
func hasHead(names []string, name string) bool {
	return slices.Contains(names, name)
}

// hunk это диапазоны строк одного ханка: старой редакции и новой.
type hunk struct {
	oldStart, oldCount int
	newStart, newCount int
}

// diffHunks разбирает заголовки ханков «@@ -a,b +c,d @@» из вывода git diff
// -U0. Контекста при -U0 нет, поэтому диапазон ханка это ровно тронутые строки.
func diffHunks(diff string) []hunk {
	var out []hunk
	for _, ln := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(ln, "@@ ") {
			continue
		}
		body, _, ok := strings.Cut(strings.TrimPrefix(ln, "@@ "), " @@")
		if !ok {
			continue
		}
		parts := strings.Fields(body)
		if len(parts) < 2 {
			continue
		}
		oldStart, oldCount := rangeOf(strings.TrimPrefix(parts[0], "-"))
		newStart, newCount := rangeOf(strings.TrimPrefix(parts[1], "+"))
		out = append(out, hunk{oldStart, oldCount, newStart, newCount})
	}
	return out
}

// rangeOf разбирает «12,3» и «12»: без запятой длина диапазона это одна строка.
func rangeOf(s string) (start, count int) {
	head, tail, ok := strings.Cut(s, ",")
	start, _ = strconv.Atoi(head)
	count = 1
	if ok {
		count, _ = strconv.Atoi(tail)
	}
	return start, count
}

// standCovered держит сам критерий: на тронутый раздел есть сценарий, а в файле
// задачи стоит зачтённая отметка стенда, где этот сценарий назван, база та, что
// назначена разделу, и отпечаток сошёлся с текстом дерева ветки. Отказ называет
// первую причину из ряда и печатает готовую команду прогона.
func standCovered(read obey.Reader, scens []standScenario, marks []taskform.StandMark, sec touched, id string) error {
	var covering []standScenario
	for _, s := range scens {
		if obey.CoversAny(s.subs, sec.file, sec.section) {
			covering = append(covering, s)
		}
	}
	if len(covering) == 0 {
		return fmt.Errorf("на %s нет сценария стенда: правка текста для агентов меряется прогоном, а не диффом (LLD DK-805); завести сценарий в %s с ключом «%s: %s» или загасить ворот пометкой «- Исключение: стенд (причина)» в docs/tasks/%s.md",
			where(sec), ScenarioDir, obey.Key, subjectOf(sec), id)
	}
	named, based := false, false
	for _, m := range marks {
		if !namesAny(m, covering) {
			continue
		}
		named = true
		if m.Base != sec.base {
			continue
		}
		based = true
		subs, ok := markSubjects(scens, m.Scenarios)
		if !ok {
			continue
		}
		// Отпечаток отметки снят с объединения предметов всех сценариев
		// прогона, а не одного покрывающего: `--for` берёт несколько путей, и
		// один прогон закрывает несколько сценариев разом.
		print, err := obey.PrintFrom(read, subs)
		if err != nil {
			continue
		}
		if m.Print == print && !m.Failed {
			return nil
		}
	}
	why := "сценарий не гонялся"
	switch {
	case based:
		why = "отпечаток отметки не совпал с текстом дерева ветки: текст правился после прогона либо прогон не зачтён"
	case named:
		why = "прогон шёл с другой базой, а разделу назначена база «" + sec.base + "»"
	}
	return fmt.Errorf("на %s нет зачтённого следа стенда (%s): прогнать `obeycheck --task %s --for %s -k 5 --base %s <раскладка-кандидат> <раскладка-база>` (сценарии: %s) или загасить ворот пометкой «- Исключение: стенд (причина)» в docs/tasks/%s.md",
		where(sec), why, id, sec.file, sec.base, strings.Join(scenarioIDs(covering), ", "), id)
}

// namesAny отвечает, назван ли в отметке хоть один из сценариев, покрывающих
// тронутый раздел.
func namesAny(m taskform.StandMark, covering []standScenario) bool {
	for _, s := range covering {
		if slices.Contains(m.Scenarios, s.id) {
			return true
		}
	}
	return false
}

// markSubjects собирает предметы прогона по именам сценариев из самой отметки
// тем же объединением, каким их считает стенд. Имя, которого в дереве ветки
// больше нет, оставляет отметку без отпечатка: доказывать ей нечем.
func markSubjects(scens []standScenario, names []string) ([]obey.Subject, bool) {
	var groups [][]obey.Subject
	for _, name := range names {
		i := slices.IndexFunc(scens, func(s standScenario) bool { return s.id == name })
		if i < 0 {
			return nil, false
		}
		groups = append(groups, scens[i].subs)
	}
	return obey.Union(groups...), true
}

// where называет тронутый раздел так, как он читается в отказе.
func where(sec touched) string {
	if sec.section == headSection {
		return sec.file + " (шапка файла)"
	}
	return sec.file + " «" + sec.section + "»"
}

// subjectOf собирает пример значения ключа «предмет» для тронутого раздела.
func subjectOf(sec touched) string {
	if sec.section == headSection {
		return sec.file
	}
	return sec.file + " «" + sec.section + "»"
}

// scenarioIDs отдаёт имена сценариев списком, как они пишутся в отметке.
func scenarioIDs(scens []standScenario) []string {
	var out []string
	for _, s := range scens {
		out = append(out, s.id)
	}
	return out
}

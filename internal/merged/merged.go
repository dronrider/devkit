// Package merged держит два слоя решения 2 LLD DK-933. Нижний это признак
// «слита»: работа задачи лежит в main и не откачена, его читает условие «слита»
// у agentctl wait. Верхний это снятость ребра «после» (edge.go), она добавляет к
// признаку архив, пометку провала и вид приёмки, её читают taskctl и shipctl.
// У трёх утилит ответ обязан совпадать, и отдельная копия в каждой разошлась бы
// на первой правке.
//
// Ветку слитой задачи shipctl удаляет, и `git merge-base --is-ancestor` по ней
// после слияния отвечать нечем. Опорой служат запись «Выкат» в файле задачи и
// ID задачи первым в subject коммита, как у отката и состава поезда в shipctl.
package merged

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/dronrider/devkit/internal/taskform"
)

// Verdict это ответ признака. Sha и Subject называют коммит, на котором признак
// сошёлся: работу задачи, её откат либо ничего, когда коммитов задачи в main
// нет вовсе. Слова нужны вызывающему, чтобы ответ был виден человеку, а не
// одним да или нет.
type Verdict struct {
	Merged   bool
	Reverted bool
	Sha      string
	Subject  string
}

// Said это ответ словами для строки человеку и журнала.
func (v Verdict) Said(id string) string {
	switch {
	case v.Merged:
		return fmt.Sprintf("%s слита: %s %s", id, short(v.Sha), v.Subject)
	case v.Reverted:
		return fmt.Sprintf("%s откачена: %s %s", id, short(v.Sha), v.Subject)
	}
	return fmt.Sprintf("%s не слита: в main нет её коммитов кроме доски и файлов задач", id)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Task отвечает, слита ли работа задачи id в ветку main репозитория root.
// Лог идёт от новых к старым. Коммит задачи это коммит из записи «Выкат» либо
// коммит с её ID первым в subject. Первый такой коммит, трогающий что-то кроме
// доски и файлов задач, делает признак истинным. Правка docs/lld и прочей доки
// работой считается: зависимой от LLD нужен документ в main, и этим признак
// отличается от codeCommits в shipctl. Откат задачи новее её работы делает
// признак ложным до следующего слияния.
func Task(root, main, id string) (Verdict, error) {
	return (&Book{root: root, main: main}).Task(id)
}

// Book это лог main, прочитанный один раз на команду. Признак у доски
// спрашивают десятки рёбер разом (`list --json`, lint, slot), а лог, файлы
// коммитов и список деревьев у них общие. Книга живёт одну команду и
// перечитывать себя не умеет: доска и main за это время не двигаются.
type Book struct {
	root, main string
	mainErr    error
	read       bool
	log        []logLine
	logErr     error
	trees      []string
	work       map[string]bool
	edges      map[Prereq]Edge
	want       []string
	warmed     bool
	docs       map[string]string
	recs       map[string][]string
}

type logLine struct{ sha, subj string }

// Open заводит книгу репозитория root, основную ветку она называет сама. Без
// git и без main книга отвечает ошибкой на каждый вопрос признака, а снятость
// ребра в таком случае сводится к архиву.
func Open(root string) *Book {
	b := &Book{root: root}
	b.main, b.mainErr = Main(root)
	return b
}

// Task это признак «слита» из книги, правило то же, что у Task пакета.
func (b *Book) Task(id string) (Verdict, error) {
	v, _, err := b.scan(id, false)
	return v, err
}

// Ever отвечает, ложилась ли работа задачи в main хоть раз, откачена она
// потом или нет. По нему lint отличает начатую строку, которую ворота старта
// когда-то пустили, от строки, пущенной мимо них.
func (b *Book) Ever(id string) (bool, error) {
	_, ever, err := b.scan(id, true)
	return ever, err
}

// scan идёт по логу от новых к старым. Первый коммит задачи даёт признак.
// С past обход после отката продолжается до первой работы: так считается Ever.
func (b *Book) scan(id string, past bool) (Verdict, bool, error) {
	lines, err := b.lines()
	if err != nil {
		return Verdict{}, false, err
	}
	rec := b.record(id)
	var v Verdict
	found := false
	for _, ln := range lines {
		if !OwnsSubject(ln.subj, id) && !inRecord(rec, ln.sha) {
			continue
		}
		if IsRevert(ln.subj) {
			if !found {
				v, found = Verdict{Reverted: true, Sha: ln.sha, Subject: ln.subj}, true
			}
			if !past {
				return v, false, nil
			}
			continue
		}
		work, err := b.isWork(ln.sha)
		if err != nil {
			return Verdict{}, false, err
		}
		if !work {
			continue
		}
		if !found {
			v = Verdict{Merged: true, Sha: ln.sha, Subject: ln.subj}
		}
		return v, true, nil
	}
	return v, false, nil
}

func (b *Book) lines() ([]logLine, error) {
	if b.mainErr != nil {
		return nil, b.mainErr
	}
	if b.read {
		return b.log, b.logErr
	}
	b.read = true
	out, err := git(b.root, "log", b.main, "--format=%H%x09%s")
	if err != nil {
		b.logErr = fmt.Errorf("лог %s не прочитан: %v", b.main, err)
		return nil, b.logErr
	}
	for _, ln := range strings.Split(out, "\n") {
		if sha, subj, ok := strings.Cut(ln, "\t"); ok {
			b.log = append(b.log, logLine{sha, subj})
		}
	}
	return b.log, nil
}

// isWork отвечает, трогает ли коммит что-то кроме доски и файлов задач.
func (b *Book) isWork(sha string) (bool, error) {
	b.warm()
	if w, ok := b.work[sha]; ok {
		return w, nil
	}
	files, err := git(b.root, "show", "--name-only", "--pretty=", sha)
	if err != nil {
		return false, err
	}
	if b.work == nil {
		b.work = map[string]bool{}
	}
	b.work[sha] = !BoardOnly(files)
	return b.work[sha], nil
}

func (b *Book) record(id string) []string {
	if b.trees == nil {
		b.trees = trees(b.root)
	}
	b.warm()
	if rec, ok := b.recs[id]; ok {
		return rec
	}
	var docs []string
	// Файл задачи из main уже прочитан пакетом, когда книгу о задаче
	// предупредили. Пустая строка там значит, что в main такого файла нет: это
	// тот же ответ, что давал отказ `git show` по одному пути.
	if doc, ok := b.docs[id]; ok {
		if doc != "" {
			docs = append(docs, doc)
		}
	} else if out, err := git(b.root, "show", b.main+":docs/tasks/"+id+".md"); err == nil {
		docs = append(docs, out)
	}
	rec := recordShas(append(docs, treeDocs(b.trees, id)...))
	if b.recs == nil {
		b.recs = map[string][]string{}
	}
	b.recs[id] = rec
	return rec
}

// Expect называет ID, про которые книгу спросят в этой команде. По ним книга
// читает файлы задач из main и состав коммитов пакетными вызовами git: один
// `cat-file --batch` на все файлы и один `show --name-only` на все коммиты.
//
// Экономия тут не в работе git, а в числе запусков процесса. Обход доски
// поднимал под сотню подпроцессов git на команду, а под соседним полным
// прогоном один запуск стоит в три-пять раз дороже обычного. `nice` не
// снимает этой цены. Понижение приоритета берёт счёт, а не создание процесса
// (замер DK-1168). Книга без Expect работает как раньше, по вызову на файл и
// на коммит.
func (b *Book) Expect(ids []string) {
	b.want = append(b.want, ids...)
}

// warmBatch это потолок ревизий в одном `git show`. Список едет аргументами, и
// на доске из тысячи строк он упёрся бы в предел длины командной строки.
const warmBatch = 500

// warm читает пакетом всё, что книга знает наперёд: файлы задач названных
// Expect строк и состав коммитов, про которые спросят признак. Зовётся из
// первого же запроса, а не из Expect. Команда вроде `show --json` про одну
// строку не должна платить за пакет, которого не спросит.
func (b *Book) warm() {
	if b.warmed {
		return
	}
	b.warmed = true
	if len(b.want) == 0 || b.mainErr != nil {
		return
	}
	if b.trees == nil {
		b.trees = trees(b.root)
	}
	b.docs = batchDocs(b.root, b.main, b.want)
	lines, err := b.lines()
	if err != nil {
		return
	}
	want := map[string]bool{}
	prefs := map[string]bool{}
	for _, id := range b.want {
		if p, _, ok := strings.Cut(id, "-"); ok && p != "" {
			want[id] = true
			prefs[p] = true
		}
	}
	var rec []string
	for id := range want {
		rec = append(rec, b.record(id)...)
	}
	// Кандидаты это коммиты, про которые scan спросит состав: владелец по
	// subject либо коммит из записи «Выкат». Первый ID в subject считается один
	// раз на строку лога, а не на каждую пару строки и задачи: лог у доски
	// живёт десятками тысяч строк.
	var shas []string
	seen := map[string]bool{}
	for _, ln := range lines {
		own := false
		for p := range prefs {
			if want[FirstID(ln.subj, p)] {
				own = true
				break
			}
		}
		if !own && !inRecord(rec, ln.sha) {
			continue
		}
		if !seen[ln.sha] {
			seen[ln.sha] = true
			shas = append(shas, ln.sha)
		}
	}
	b.fillWork(shas)
}

// fillWork читает состав названных коммитов пакетами `git show`. Команда
// берёт список ревизий, а вызов на коммит стоит запуска процесса.
func (b *Book) fillWork(shas []string) {
	if b.work == nil {
		b.work = map[string]bool{}
	}
	for len(shas) > 0 {
		n := len(shas)
		if n > warmBatch {
			n = warmBatch
		}
		part := shas[:n]
		shas = shas[n:]
		out, err := git(b.root, append([]string{"show", "--name-only", "--pretty=%H"}, part...)...)
		if err != nil {
			return
		}
		known := map[string]bool{}
		for _, sha := range part {
			known[sha] = true
		}
		// Ответ идёт кусками: строка sha, пустая строка, имена файлов. Начало
		// куска узнаётся по тому, что строка это один из спрошенных sha. Без
		// этой проверки имя файла из сорока шестнадцатеричных знаков сбило бы
		// разбор. Коммит слияния имён не печатает вовсе, и состав у него
		// пустой, как и был при вызове на один коммит.
		cur := ""
		files := map[string][]string{}
		said := map[string]bool{}
		for _, ln := range strings.Split(out, "\n") {
			if known[ln] {
				cur, said[ln] = ln, true
				continue
			}
			if cur != "" && strings.TrimSpace(ln) != "" {
				files[cur] = append(files[cur], ln)
			}
		}
		// Коммит, которого в ответе не оказалось, в память не кладётся.
		// Про него спросят отдельным вызовом, и отказ git останется
		// отказом, а не превратится в «коммит без файлов».
		for sha := range said {
			b.work[sha] = !BoardOnly(strings.Join(files[sha], "\n"))
		}
	}
}

// batchDocs читает файлы задач из main одним `cat-file --batch`. Ответ на
// каждый путь это строка заголовка и ровно столько байтов содержимого, сколько
// в ней названо; ненайденный путь отвечает строкой без размера, и такой задачи
// в main просто нет. Порядок ответов тот же, что порядок запросов, по нему
// содержимое и раздаётся задачам.
func batchDocs(root, main string, ids []string) map[string]string {
	if main == "" || len(ids) == 0 {
		return nil
	}
	var in strings.Builder
	for _, id := range ids {
		in.WriteString(main + ":docs/tasks/" + id + ".md\n")
	}
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	docs := map[string]string{}
	rest := out
	for _, id := range ids {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		head := string(rest[:nl])
		rest = rest[nl+1:]
		f := strings.Fields(head)
		if len(f) < 3 {
			docs[id] = ""
			continue
		}
		size, err := strconv.Atoi(f[2])
		if err != nil || size > len(rest) {
			break
		}
		docs[id] = strings.TrimRight(string(rest[:size]), "\n")
		rest = rest[size:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
	}
	return docs
}

// Record собирает коммиты записи «Выкат» задачи id из всех мест, где свежая
// запись может лежать. Запись пишет shipctl в основном чекауте и коммитит в
// main, а спрашивают признак и из дерева задачи, где файл задачи стоит на
// старой базе ветки. Поэтому читаются main, основной чекаут и сам root вместе
// с архивом файлов задач. Лишний коммит в объединении вреда не делает: в
// расчёт идут только коммиты, лежащие в логе main.
func Record(root, main, id string) []string {
	return record(root, main, id, trees(root))
}

func record(root, main, id string, dirs []string) []string {
	var docs []string
	if out, err := git(root, "show", main+":docs/tasks/"+id+".md"); err == nil {
		docs = append(docs, out)
	}
	return recordShas(append(docs, treeDocs(dirs, id)...))
}

// treeDocs читает файл задачи из рабочих деревьев и из архива файлов задач.
func treeDocs(dirs []string, id string) []string {
	var docs []string
	for _, dir := range dirs {
		files := []string{filepath.Join(dir, "docs", "tasks", id+".md")}
		if more, err := filepath.Glob(filepath.Join(dir, "docs", "tasks", "archive", "*", id+".md")); err == nil {
			files = append(files, more...)
		}
		for _, f := range files {
			if data, err := os.ReadFile(f); err == nil {
				docs = append(docs, string(data))
			}
		}
	}
	return docs
}

// recordShas собирает коммиты записи «Выкат» из прочитанных файлов задачи.
func recordShas(docs []string) []string {
	var out []string
	for _, doc := range docs {
		for _, sha := range taskform.MergedShas(doc) {
			if !inRecord(out, sha) {
				out = append(out, sha)
			}
		}
	}
	return out
}

// Closed отвечает, стоит ли строка id в архиве доски. Архив правит taskctl
// close в основном чекауте, коммит доски идёт следом, а из дерева задачи архив
// виден только на старой базе ветки. Поэтому читаются файл основного чекаута,
// файл самого root и архив в main, и строки хватает в любом из них.
func Closed(root, main, id string) (bool, error) {
	for _, dir := range trees(root) {
		data, err := os.ReadFile(filepath.Join(dir, "docs", "TASKS-archive.md"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		if archiveHas(string(data), id) {
			return true, nil
		}
	}
	if main == "" {
		return false, nil
	}
	if out, err := git(root, "show", main+":docs/TASKS-archive.md"); err == nil && archiveHas(out, id) {
		return true, nil
	}
	return false, nil
}

// archiveHas ищет строку таблицы архива, первая ячейка которой это id.
func archiveHas(doc, id string) bool {
	for _, ln := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		if len(cells) > 0 && strings.TrimSpace(cells[0]) == id {
			return true
		}
	}
	return false
}

// Main называет основную ветку репозитория: main, а за ней master, как у
// shipctl.
func Main(root string) (string, error) {
	for _, b := range []string{"main", "master"} {
		if _, err := git(root, "rev-parse", "--verify", "--quiet", b); err == nil {
			return b, nil
		}
	}
	return "", fmt.Errorf("в %s не нашёл ветку main или master", root)
}

// MainTree называет основной чекаут репозитория, в котором лежит root. Из
// линкованного дерева это родитель общего каталога git, из основного чекаута
// сам root. Вне git и у голого репозитория ответ тоже root: читать больше
// негде.
func MainTree(root string) string {
	out, err := git(root, "rev-parse", "--git-common-dir")
	if err != nil {
		return root
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	out = filepath.Clean(out)
	if filepath.Base(out) != ".git" {
		return root
	}
	return filepath.Dir(out)
}

// trees это основной чекаут и root без повтора, когда они совпадают.
func trees(root string) []string {
	main := MainTree(root)
	if same(main, root) {
		return []string{root}
	}
	return []string{main, root}
}

func same(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// OwnsSubject отвечает, принадлежит ли коммит задаче id: владельцем считается
// первый ID в subject, прочие упоминания дальше по тексту чужие. Упомянуть
// соседнюю задачу в сообщении законно, и по поиску ID словом такой коммит
// записывался бы в чужую работу. Ищется только ID с префиксом самой задачи:
// ключ внешнего трекера или «UTF-8» в тексте иначе заняли бы место первого ID.
func OwnsSubject(subj, id string) bool {
	pref, _, ok := strings.Cut(id, "-")
	if !ok || pref == "" {
		return false
	}
	return FirstID(subj, pref) == id
}

// FirstID возвращает первый ID вида «<pref>-<число>», стоящий в s отдельным
// словом. Пустая строка значит, что задач этого префикса в тексте нет.
func FirstID(s, pref string) string {
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

// IsRevert распознаёт коммит-откат: штатное «revert: ...» либо своё сообщение
// со словом «откат» (конвенция для проектов с белым списком префиксов). Слово
// ищется целиком, иначе коммит про «откатить настройки» тихо снимал бы признак.
func IsRevert(subj string) bool {
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

// BoardOnly отвечает, трогает ли коммит только доску и файлы задач. files это
// вывод `git show --name-only`, путь на строку. Состояние доски двигает
// taskctl, и такая правка работой задачи не считается.
func BoardOnly(files string) bool {
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

// inRecord сверяет полный sha из лога с записанным сокращённым.
func inRecord(rec []string, sha string) bool {
	for _, r := range rec {
		if strings.HasPrefix(sha, r) || strings.HasPrefix(r, sha) {
			return true
		}
	}
	return false
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

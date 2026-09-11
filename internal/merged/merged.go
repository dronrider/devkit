// Package merged держит признак «слита» из решения 2 LLD DK-933: работа
// задачи лежит в main и не откачена. Признак читают agentctl (условие «слита» у
// wait), а за ней taskctl и shipctl (снятость ребра «после»), и у трёх утилит
// он обязан совпадать. Отдельная копия в каждой разошлась бы на первой правке.
//
// Ветку слитой задачи shipctl удаляет, и `git merge-base --is-ancestor` по ней
// после слияния отвечать нечем. Опорой служат запись «Выкат» в файле задачи и
// ID задачи первым в subject коммита, как у отката и состава поезда в shipctl.
package merged

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	rec := Record(root, main, id)
	log, err := git(root, "log", main, "--format=%H%x09%s")
	if err != nil {
		return Verdict{}, fmt.Errorf("лог %s не прочитан: %v", main, err)
	}
	for _, ln := range strings.Split(log, "\n") {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok || (!OwnsSubject(subj, id) && !inRecord(rec, sha)) {
			continue
		}
		if IsRevert(subj) {
			return Verdict{Reverted: true, Sha: sha, Subject: subj}, nil
		}
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return Verdict{}, err
		}
		if BoardOnly(files) {
			continue
		}
		return Verdict{Merged: true, Sha: sha, Subject: subj}, nil
	}
	return Verdict{}, nil
}

// Record собирает коммиты записи «Выкат» задачи id из всех мест, где свежая
// запись может лежать. Запись пишет shipctl в основном чекауте и коммитит в
// main, а спрашивают признак и из дерева задачи, где файл задачи стоит на
// старой базе ветки. Поэтому читаются main, основной чекаут и сам root вместе
// с архивом файлов задач. Лишний коммит в объединении вреда не делает: в
// расчёт идут только коммиты, лежащие в логе main.
func Record(root, main, id string) []string {
	var docs []string
	if out, err := git(root, "show", main+":docs/tasks/"+id+".md"); err == nil {
		docs = append(docs, out)
	}
	for _, dir := range trees(root) {
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

// Package freshtree выкладывает коммит в одноразовое дерево и собирает
// окружение прогона, отвязанное от дома сессии. Прогретый чекаут и живые HOME
// с PATH прячут дефекты класса «зелено у исполнителя, красно на чужой машине»,
// и одинаково прячут их у слияния (shipctl merge) и у обкатки сценария
// (taskctl rehearse). Кирпичи писались под слияние (DK-641) и переехали сюда
// целиком, когда за ними пришёл второй читатель: две копии разошлись бы на
// первой правке списка переменных.
//
// Чистота прогона стоит ровно на двух изъятиях (DK-684). Утилиты проверяемого
// дерева собираются в свой каталог и уходят в начало PATH: шаг зовёт `taskctl`
// той ветки, которая проверяется, а не собирает его в теле сценария. Тулчейны,
// поставленные под домом (rustup, cargo и родня), остаются на месте
// каталогами и указателями: подменённый HOME иначе уводит rustup от ~/.rustup,
// и прогон падает не на правке, а на выборе версии cargo.
package freshtree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// homeToolchains это тулчейны, живущие под домом пользователя. Их каталоги
// остаются в PATH прогона, а указатель на данные едет переменной от настоящего
// дома: подменённый HOME для rustup значит «~/.rustup нет», и вместо тестов
// проекта прогон отдаёт «could not choose a version of cargo to run». Список
// назван поимённо и пополняется по надобности: чистота прогона держится на
// том, что назад возвращается перечисленное, а не всё подряд.
var homeToolchains = []struct {
	dir  string
	vars []string
}{
	{".cargo", []string{"CARGO_HOME"}},
	{".rustup", []string{"RUSTUP_HOME"}},
	{".nvm", []string{"NVM_DIR"}},
	{".pyenv", []string{"PYENV_ROOT"}},
	{".rbenv", []string{"RBENV_ROOT"}},
	{".bun", []string{"BUN_INSTALL"}},
	{".deno", []string{"DENO_INSTALL"}},
}

// Run это готовый чистый прогон: дерево на нужном коммите, пустой дом рядом,
// каталог утилит дерева и окружение, которое читатели отдают команде.
type Run struct {
	Tree  string   // свежее дерево на коммите
	Home  string   // пустой временный дом
	Bin   string   // каталог утилит проверяемого дерева
	Env   []string // окружение команды прогона
	Tools []string // утилиты дерева, которые собрались
	Notes []string // находки сборки: чья не прошла и почему
	clean func()
}

// Start готовит прогон целиком: выкладывает дерево, собирает в него утилиты и
// складывает окружение. Обкатке сценария и слиянию достаётся одна дорога, и
// разойтись им негде: список изъятий у обоих читателей общий.
func Start(root, sha, prefix string) (*Run, error) {
	tree, home, cleanup, err := Make(root, sha, prefix)
	if err != nil {
		return nil, err
	}
	// Каталог утилит лежит рядом с деревом и домом, в том же временном
	// корне, который завёл Make, и уезжает вместе с ним.
	bin := filepath.Join(filepath.Dir(tree), "bin")
	tools, notes := Tools(tree, bin)
	r := &Run{Tree: tree, Home: home, Bin: bin, Tools: tools, Notes: notes, clean: cleanup}
	r.Env = Env(os.Getenv("HOME"), home, bin)
	return r, nil
}

// Cleanup уносит дерево и всё временное. Зовётся и при провале прогона: отказ
// одноразовых деревьев не копит.
func (r *Run) Cleanup() {
	if r.clean != nil {
		r.clean()
	}
}

// Env собирает окружение прогона. Живой HOME сессии подменяется временным,
// каталоги из-под него уходят из PATH, переменные харнеса CLAUDE* уносятся:
// прогон обязан зеленеть на чужой машине, а не на прогретой раскладке дома
// исполнителя. Уносятся и указатели внутрь дома (GOPATH, GOMODCACHE,
// PYTHONPATH, VIRTUAL_ENV): формально это не HOME и не PATH, но живую
// раскладку они возвращают в прогон тем же путём. Тулчейны вне дома
// (/usr/bin, /opt/homebrew) остаются, иначе команде нечем работать, а
// тулчейны под домом возвращаются названным списком. Каталог bin с утилитами
// проверяемого дерева встаёт в начало PATH и перекрывает одноимённые
// установленные: под проверку идёт код ветки.
func Env(home, tmpHome, bin string) []string {
	homePointers := map[string]bool{
		"GOPATH": true, "GOMODCACHE": true, "PYTHONPATH": true, "VIRTUAL_ENV": true,
	}
	seen := map[string]bool{}
	var out []string
	for _, kv := range os.Environ() {
		name, val, _ := strings.Cut(kv, "=")
		switch {
		case name == "HOME" || strings.HasPrefix(name, "CLAUDE") || homePointers[name]:
			continue
		case name == "PATH":
			kv = "PATH=" + prependBin(TrimHomePath(val, home), bin)
		}
		seen[name] = true
		out = append(out, kv)
	}
	out = append(out, "HOME="+tmpHome)
	return append(out, toolchainVars(home, seen)...)
}

// prependBin ставит каталог утилит дерева первым: одноимённая утилита с машины
// иначе перекрыла бы ту, которую проверяют.
func prependBin(path, bin string) string {
	if bin == "" {
		return path
	}
	if path == "" {
		return bin
	}
	return bin + string(os.PathListSeparator) + path
}

// TrimHomePath убирает из PATH каталоги под home: там живут обвязки сессии
// вроде ~/bin и ~/.local/bin, которых на чужой машине нет. Изъятие одно:
// каталоги названных тулчейнов (~/.cargo/bin и родня) остаются на своих
// местах в строке, потому что порядок в PATH решает, какой cargo возьмёт шаг.
func TrimHomePath(path, home string) string {
	if home == "" {
		return path
	}
	var kept []string
	for _, p := range filepath.SplitList(path) {
		if underHome(p, home) && !toolchainDir(p, home) {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// underHome отвечает, лежит ли путь в доме. Общий префикс строки вложенностью
// не считается: /fake/homework это не /fake/home.
func underHome(p, home string) bool {
	return p == home || strings.HasPrefix(p, home+string(os.PathSeparator))
}

// toolchainDir отвечает, принадлежит ли каталог названному тулчейну под домом.
// Смотрится корень: у nvm команды лежат в ~/.nvm/versions/node/<версия>/bin, и
// перечислить такие пути списком нечем.
func toolchainDir(p, home string) bool {
	rel := strings.TrimPrefix(strings.TrimPrefix(p, home), string(os.PathSeparator))
	for _, t := range homeToolchains {
		if rel == t.dir || strings.HasPrefix(rel, t.dir+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// toolchainVars отдаёт указатели тулчейнов на настоящий дом. Ставятся только
// те, которых в окружении сессии нет и чей каталог на машине есть: заданное
// пользователем значение сильнее умолчания, а указатель в пустоту хуже
// отсутствующего.
func toolchainVars(home string, seen map[string]bool) []string {
	if home == "" {
		return nil
	}
	var out []string
	for _, t := range homeToolchains {
		dir := filepath.Join(home, t.dir)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		for _, name := range t.vars {
			if !seen[name] {
				out = append(out, name+"="+dir)
			}
		}
	}
	return out
}

// Tools собирает утилиты проверяемого дерева в каталог bin. Что собирать,
// выводится из дерева, а не из списка в коде, теми же приметами, что у сборки
// релиза: каталог tools/<имя> с go.mod идёт в go build, каталог с одноимённым
// python-файлом получает обёртку рядом с бинарями. Сборка идёт в окружении
// сессии, с прогретым кешем и своим модульным каталогом: это подготовка
// прогона, а не его шаг. Провал сборки одной утилиты прогон не роняет, он
// возвращается находкой: сценарий, которому эта утилита не нужна, зеленеет и
// без неё, а тому, кому нужна, находку назовёт Diagnose.
func Tools(tree, bin string) (names, problems []string) {
	dirs, err := filepath.Glob(filepath.Join(tree, "tools", "*"))
	if err != nil || len(dirs) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, []string{"каталог утилит дерева не завёлся: " + err.Error()}
	}
	for _, dir := range dirs {
		name := filepath.Base(dir)
		switch {
		case exists(filepath.Join(dir, "go.mod")):
			if out, err := goBuild(dir, filepath.Join(bin, name)); err != nil {
				problems = append(problems, fmt.Sprintf("%s: сборка из дерева не прошла: %s", name, tail(out)))
				continue
			}
		case exists(filepath.Join(dir, name+".py")):
			if err := wrapper(filepath.Join(bin, name), filepath.Join(dir, name+".py")); err != nil {
				problems = append(problems, fmt.Sprintf("%s: обёртка не легла: %v", name, err))
				continue
			}
		default:
			continue
		}
		names = append(names, name)
	}
	return names, problems
}

// goBuild собирает одну go-утилиту. GOWORK=off нужен там, где на машине лежит
// чужой go.work: утилита живёт своим модулем и в чужой список не входит.
// CGO_ENABLED=0 держит сборку одинаковой на любой машине.
func goBuild(dir, target string) (string, error) {
	cmd := exec.Command("go", "build", "-o", target, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// wrapper кладёт рядом с бинарями вход в python-часть дерева. Тот же приём,
// что у установки devkit: python-утилита бинарём не собирается, а команда с её
// именем в PATH нужна.
func wrapper(target, script string) error {
	text := "#!/bin/sh\n# Утилита проверяемого дерева, вход чистого прогона.\nexec python3 " +
		shellQuote(script) + " \"$@\"\n"
	return os.WriteFile(target, []byte(text), 0o755)
}

// shellQuote заворачивает путь в одинарные кавычки: временный каталог прогона
// заводит система, и пробел в его имени не запрещён.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// missingCommand это две формы отказа шелла по нехватке команды: sh и bash
// пишут имя перед словами, zsh после них.
var missingCommand = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^.*?([^\s:]+): (?:command )?not found\s*$`),
	regexp.MustCompile(`(?m)^.*?command not found: (\S+)\s*$`),
}

// Diagnose разбирает вывод упавшего прогона и объясняет отказ по нехватке
// команды: называет саму команду и места, где прогон её искал. Без этого
// «command not found» в чистом окружении неразличим с любым другим красным
// шагом, а искать надо в трёх разных местах: утилиты дерева, урезанный PATH,
// тулчейны под домом. Пустая строка значит, что отказ не про команду.
func (r *Run) Diagnose(out string) string {
	name := ""
	for _, re := range missingCommand {
		if m := re.FindStringSubmatch(out); m != nil {
			name = m[1]
			break
		}
	}
	if name == "" {
		return ""
	}
	said := fmt.Sprintf("команды `%s` в прогоне нет", name)
	for _, p := range r.Notes {
		if strings.HasPrefix(p, name+":") {
			return said + ": " + p
		}
	}
	where := "утилит дерево не несёт"
	if len(r.Tools) > 0 {
		where = fmt.Sprintf("утилиты дерева лежат в %s (%s)", r.Bin, strings.Join(r.Tools, ", "))
	}
	return fmt.Sprintf("%s: искали в PATH прогона (%s), %s; каталоги под домом сессии из PATH убраны, кроме тулчейнов",
		said, envValue(r.Env, "PATH"), where)
}

// envValue достаёт значение переменной из собранного окружения.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v
		}
	}
	return ""
}

// tail режет вывод сборки до последних строк: в находку едет причина отказа, а
// не весь лог компилятора.
func tail(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, "; ")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Make выкладывает коммит во временное detached-дерево и заводит рядом пустой
// временный HOME. Прогретый worktree ветки несёт следы работы исполнителя
// (артефакты сборки, __pycache__), и зелень в нём не доказывает зелень свежего
// чекаута: на этой разнице дефект 98b43e7 проехал слияние и всплыл после
// выката. Уборка на вызывающем: cleanup зовётся и при провале прогона, отказ
// временных деревьев не копит. Префикс идёт в имя временного каталога, по нему
// в `git worktree list` видно, чей это прогон.
func Make(root, sha, prefix string) (tree, home string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", "", nil, err
	}
	tree = filepath.Join(tmp, "tree")
	home = filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		os.RemoveAll(tmp)
		return "", "", nil, err
	}
	if out, err := git(root, "worktree", "add", "--detach", tree, sha); err != nil {
		os.RemoveAll(tmp)
		return "", "", nil, wrap(err, out)
	}
	cleanup = func() {
		git(root, "worktree", "remove", "--force", tree)
		os.RemoveAll(tmp)
	}
	return tree, home, cleanup, nil
}

// git зовёт гит в каталоге root и отдаёт вывод вместе с ошибкой: у
// `worktree add` причина отказа живёт в выводе, а не в коде возврата.
func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// wrap приклеивает к ошибке гита его же вывод, когда он есть.
func wrap(err error, out string) error {
	if out == "" {
		return err
	}
	return &gitError{err: err, out: out}
}

type gitError struct {
	err error
	out string
}

func (e *gitError) Error() string { return e.err.Error() + ": " + e.out }
func (e *gitError) Unwrap() error { return e.err }

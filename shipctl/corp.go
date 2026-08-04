package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Привязка проекта к трекеру лежит в боковой директории и несёт ключ repo с
// путём корп-клона. Здесь она нужна одним этим ключом: по нему потерянная
// обвязка клона отличается от обычного проекта без доски.
const corpTrackerPath = ".devkit/tracker.local"

// corpLocal отдаёт боковую директорию корп-контура для стартовой директории.
// В корп-клоне рабочие файлы devkit (доска, файлы задач, .devkit) лежат не в
// дереве проекта, а рядом с ним, и путь к ним записан в конфиге клона ключом
// devkit.local. Пустой ответ значит домашний проект, где корень ищется прежним
// подъёмом. Наличие редиректа и есть машинный признак корп-контура: остальные
// команды спрашивают контур этой же функцией, а не разбирают конфиг заново.
func corpLocal(start string) string {
	val, err := corpGit(start, "config", "--local", "--get", "devkit.local")
	if err != nil || val == "" {
		return ""
	}
	if filepath.IsAbs(val) {
		return filepath.Clean(val)
	}
	base, err := corpCheckout(start)
	if err != nil {
		return ""
	}
	return filepath.Join(base, val)
}

// corpCheckout отдаёт директорию основного чекаута репозитория, которому
// принадлежит start, то есть родителя git-common-dir. Относительный путь
// редиректа разрешается от неё, а не от cwd и не от вершины текущего дерева: у
// дерева ветки вершина своя, и от неё путь сходился бы, только пока дерево
// лежит сиблингом проекта, как его кладёт shipctl. Дерево в стороннем месте
// молча увело бы в чужую директорию.
func corpCheckout(start string) (string, error) {
	out, err := corpGit(start, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := out
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(start)
		if err != nil {
			return "", err
		}
		dir = filepath.Join(abs, dir)
	}
	return filepath.Dir(filepath.Clean(dir)), nil
}

// corpRootErr собирает отказ поиска корня. Обвязка корп-клона живёт в .git и в
// негитигнорнутом дереве, поэтому переклонирование и «git clean -xdf» теряют её
// молча, и клон выглядит обычным проектом без доски. Когда рядом лежит боковая
// директория с привязкой к этому клону, отказ называет команду восстановления
// вместо голого «не нашёл docs/TASKS.md».
func corpRootErr(start string) error {
	msg := fmt.Sprintf("не нашёл docs/TASKS.md вверх от %s", start)
	if local := corpLostLocal(start); local != "" {
		return fmt.Errorf("%s, а рядом лежит %s с привязкой к этому клону: обвязка корп-контура потеряна, восстанавливает её «devkitctl corp»", msg, local)
	}
	return fmt.Errorf("%s", msg)
}

// corpLostLocal ищет рядом с клоном боковую директорию «*-local», чья привязка
// указывает ключом repo на этот самый клон. Чужие соседние директории с таким
// именем не в счёт, привязка называет свой клон явно.
func corpLostLocal(start string) string {
	clone, err := corpCheckout(start)
	if err != nil {
		return ""
	}
	parent := filepath.Dir(clone)
	ents, err := os.ReadDir(parent)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), "-local") {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		repo := corpTrackerRepo(dir)
		if repo == "" {
			continue
		}
		if !filepath.IsAbs(repo) {
			repo = filepath.Join(dir, repo)
		}
		if corpSamePath(repo, clone) {
			return dir
		}
	}
	return ""
}

// trackerBinding это поля привязки .devkit/tracker.local, нужные shipctl:
// ключ тикета и шаблон ветки для start, путь клона для status и для
// восстановления обвязки. Остальное (контур компании, статусы, оценки) знает
// только trackctl, у него свой конфиг и свой разбор того же плоского формата.
type trackerBinding struct {
	Key    string
	Branch string
	Repo   string
}

// loadTrackerBinding читает привязку в dir. Формат плоский, «key = value» с
// решёткой на комментарий, как у deploy.local, парсера TOML нет и не будет:
// привязку читает и trackctl (там же формат описан подробно), и shipctl, и
// каждый своим разбором. Второе значение говорит, найден ли сам файл: его
// наличие и есть местный признак корп-контура (root уже указывает туда же,
// куда его кладёт devkitctl corp), а поля внутри бывают неполными по
// отдельности, это не повод считать проект домашним.
func loadTrackerBinding(dir string) (trackerBinding, bool) {
	var b trackerBinding
	f, err := os.Open(filepath.Join(dir, corpTrackerPath))
	if err != nil {
		return b, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		switch key {
		case "key":
			b.Key = val
		case "branch":
			b.Branch = val
		case "repo":
			b.Repo = val
		}
	}
	return b, true
}

// corpTrackerRepo достаёт ключ repo из привязки боковой директории.
func corpTrackerRepo(dir string) string {
	b, ok := loadTrackerBinding(dir)
	if !ok {
		return ""
	}
	return b.Repo
}

// corpActive это местный признак корп-контура для команд конвейера: файл
// привязки лежит ровно там, куда findRoot уже привёл (боковая директория), и
// его наличие эквивалентно «мы внутри неё». Валидность полей внутри corpActive
// не проверяет: неполная привязка это всё ещё корп-контур, просто с честной
// находкой вместо тихого домашнего поведения.
func corpActive(root string) bool {
	_, err := os.Stat(filepath.Join(root, corpTrackerPath))
	return err == nil
}

// corpBranchName разворачивает шаблон ветки привязки: {key} это ключ тикета,
// {slug} хвост, как у --slug дома. Формат общий с trackctl (README рядом),
// пустой шаблон берёт вид «{key}-{slug}» по умолчанию, а без хвоста висящий
// дефис срезается.
func corpBranchName(tpl, key, slug string) string {
	if tpl == "" {
		tpl = "{key}-{slug}"
	}
	out := strings.ReplaceAll(tpl, "{key}", key)
	return strings.TrimRight(strings.ReplaceAll(out, "{slug}", slug), "-")
}

// corpWorkBranch отдаёт рабочую ветку. В корп-контуре git-корень это боковая
// директория со своей историей (доска, файлы задач), а ветка задачи стоит в
// клоне, на который привязка указывает ключом repo: спрашивать её у root
// значит называть веткой ветку доски, а без единого коммита в боковой
// директории (обычное дело: она только для docs/) там и вовсе падать на
// rev-parse. Без привязки или без ключа repo (домашний проект) ветку
// спрашивают у самого root, как и раньше: смена поведения не должна задевать
// дом.
func corpWorkBranch(root string) (string, error) {
	b, ok := loadTrackerBinding(root)
	if !ok || b.Repo == "" {
		return git(root, "rev-parse", "--abbrev-ref", "HEAD")
	}
	repo := b.Repo
	if !filepath.IsAbs(repo) {
		repo = filepath.Join(root, repo)
	}
	return git(repo, "rev-parse", "--abbrev-ref", "HEAD")
}

// corpRefused формулирует единый отказ merge, ship и revert в корп-контуре:
// слияние и выкат там ведёт MR-флоу компании, эта граница по RULES.md
// («Git», п. 3) не агентская, и подсказка называет ручные шаги вместо того,
// чтобы изображать слияние, которого нет.
func corpRefused(cmd string) error {
	return fmt.Errorf("корп-контур: %s здесь не работает, слияние и выкат ведёт MR-флоу компании; дальше руками: trackctl submit, пуш ветки, MR, дальше по процессу контура", cmd)
}

// corpSamePath сравнивает два пути с оглядкой на симлинки: временные
// директории на macOS лежат под /var, который сам симлинк на /private/var, и
// голое сравнение строк там расходится на ровном месте.
func corpSamePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// corpGit гоняет git в стартовой директории. Ошибка возвращается как есть:
// вне git-репозитория она значит домашнее поведение, а не поломку.
func corpGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

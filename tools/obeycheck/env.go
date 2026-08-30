package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Окружение прогона: временный HOME и синтетический проект. Живая машина и
// живая доска в стенде не участвуют вовсе, иначе сценарий про правку доски
// правил бы настоящую.
type runEnv struct {
	Root       string // директория прогона
	Home       string // временный HOME
	Project    string // синтетический проект, он же рабочая директория агента
	Origin     string // фиктивный origin: голый репозиторий рядом с проектом
	Transcript string // сюда пишется вывод прогона, по нему работают проверки
	Bin        string // команды, которые стенд даёт проверке сценария
	Seed       string // коммит, с которого агент начал: по нему видно, что он сделал
}

const devkitMarker = "RULES.md"

// findDevkit ищет чекаут devkit тем же порядком, что agentctl: переменная,
// корень репозитория, соседняя директория, домашняя раскладка. Стенду он нужен
// живьём: оттуда едут хуки в синтетический проект и определения субагентов во
// временный HOME.
func findDevkit(start string) (string, error) {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, v)
	}
	if root, err := gitOut(start, "rev-parse", "--show-toplevel"); err == nil {
		cands = append(cands, root, filepath.Join(filepath.Dir(root), "devkit"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "projects", "devkit"))
	}
	for _, c := range cands {
		if _, err := os.Stat(filepath.Join(c, devkitMarker)); err == nil {
			if _, err := os.Stat(filepath.Join(c, "hooks")); err == nil {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("не нашёл чекаут devkit, искал: %s; указать: DEVKIT_HOME=<путь к devkit>",
		strings.Join(cands, ", "))
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func copyFile(from, to string, mode fs.FileMode) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, data, mode.Perm())
}

// copyTree раскладывает дерево поверх целевого, не убирая того, что там уже
// лежит: так раскладка правил ложится на синтетический проект.
func copyTree(from, to string, skip func(rel string) bool) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		// Ссылку переносим ссылкой, а не содержимым: затравка может принести
		// свою связку ключей, и копировать связку целиком было бы и дорого, и
		// не тем, чего от затравки ждут.
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			return os.Symlink(target, dst)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, dst, info.Mode())
	})
}

// Связка ключей дома пользователя. Харнес авторизуется через неё, и временный
// дом получает на связку ссылку, а не копию ключей: дамп связки пришлось бы
// снимать командой security, а она из неинтерактивной сессии поднимает
// системный диалог доступа и вешает прогон.
const keychainRel = "Library/Keychains"

// userHomeDir отдаёт дом пользователя, откуда берётся связка. Непустое
// значение приходит из теста: живая связка машины тесту не нужна, и зависеть
// от того, залогинен ли пользователь, он не должен.
func userHomeDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return os.UserHomeDir()
}

// checkAuth смотрит, поднимется ли сессия во временном доме, до того как
// собрано хоть одно окружение. Без этой проверки негодная авторизация видна
// только пробным прогоном, и выглядит она как поведение агента: сессия не
// стартует, проверки на отрицание зеленеют, а клетки краснеют не по делу.
func checkAuth(userHome, homeSeed string) error {
	kc := filepath.Join(userHome, keychainRel)
	if homeSeed != "" {
		if !dirExists(homeSeed) {
			return fmt.Errorf("затравки HOME %s нет или это не каталог, а временный дом собирается из неё; "+
				"починить: дать существующий каталог флагом --home-seed или убрать флаг, "+
				"тогда временный дом сошлётся на связку ключей %s", homeSeed, kc)
		}
		return nil
	}
	fi, err := os.Lstat(kc)
	if err != nil {
		return fmt.Errorf("связки ключей %s нет, а временный дом ссылается на неё, иначе харнес в нём не авторизуется; "+
			"починить: залогиниться харнесом на этой машине (claude, дальше /login) "+
			"или дать готовый дом флагом --home-seed <каталог>", kc)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		if _, err := os.Stat(kc); err != nil {
			target, _ := os.Readlink(kc)
			return fmt.Errorf("связка ключей %s это ссылка в никуда, ведёт на %s, и авторизации во временном доме не будет; "+
				"починить: поправить ссылку или дать готовый дом флагом --home-seed <каталог>", kc, target)
		}
	}
	if !dirExists(kc) {
		return fmt.Errorf("связка ключей %s это не каталог, и сослаться временному дому не на что; "+
			"починить: поправить раскладку дома или дать готовый дом флагом --home-seed <каталог>", kc)
	}
	return nil
}

const hookSettings = `{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [
          { "type": "command", "command": "python3 %s/hooks/check-symbols.py --hook" },
          { "type": "command", "command": "python3 %s/hooks/check-sensitive.py --hook" }
        ]
      }
    ]
  }
}
`

// makeEnv собирает окружение одного прогона: синтетический проект под гитом с
// доской, парой файлов кода и хуками devkit, поверх него раскладка правил, и
// временный HOME с настройками харнеса и определениями субагентов.
func makeEnv(root, devkit, layout, homeSeed, userHome string) (*runEnv, error) {
	e := &runEnv{
		Root:       root,
		Home:       filepath.Join(root, "home"),
		Project:    filepath.Join(root, "project"),
		Origin:     filepath.Join(root, "origin.git"),
		Transcript: filepath.Join(root, "transcript"),
		Bin:        filepath.Join(root, "bin"),
	}
	if err := writePhrase(e.Bin); err != nil {
		return nil, err
	}
	for _, d := range []string{e.Home, e.Project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	fixture := filepath.Join(devkit, "tools", "obeycheck", "testdata", "project")
	if _, err := os.Stat(fixture); err != nil {
		return nil, fmt.Errorf("нет фикстуры синтетического проекта %s: %v", fixture, err)
	}
	if err := copyTree(fixture, e.Project, nil); err != nil {
		return nil, err
	}
	// раскладка правил: всё в проект, кроме home/, который едет во временный
	// HOME (там живут глобальная точка правил и определения субагентов).
	if err := copyTree(layout, e.Project, func(rel string) bool {
		return rel == "home" || strings.HasPrefix(rel, "home/")
	}); err != nil {
		return nil, fmt.Errorf("раскладка %s: %v", layout, err)
	}
	// затравка HOME кладётся до раскладки: это машинная обвязка (авторизация
	// харнеса и подобное), и раскладка правил вправе её перекрыть.
	if homeSeed != "" {
		if !dirExists(homeSeed) {
			return nil, fmt.Errorf("затравки HOME %s нет или это не директория", homeSeed)
		}
		if err := copyTree(homeSeed, e.Home, nil); err != nil {
			return nil, fmt.Errorf("затравка HOME %s: %v", homeSeed, err)
		}
	}
	// Ссылка на связку ключей кладётся после затравки: у затравки свой готовый
	// дом, и перекрывать его нечем. Уборка отдельного шага не просит, каталог
	// прогона сносится целиком, а os.RemoveAll идёт по ссылке не внутрь, а
	// мимо, и связка пользователя остаётся цела.
	if link := filepath.Join(e.Home, keychainRel); !pathExists(link) {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return nil, err
		}
		if err := os.Symlink(filepath.Join(userHome, keychainRel), link); err != nil {
			return nil, fmt.Errorf("ссылка на связку ключей: %v", err)
		}
	}
	if lh := filepath.Join(layout, "home"); dirExists(lh) {
		if err := copyTree(lh, e.Home, nil); err != nil {
			return nil, fmt.Errorf("раскладка %s: %v", layout, err)
		}
	}

	if err := seedRepo(e.Project); err != nil {
		return nil, err
	}
	head, err := gitOut(e.Project, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	e.Seed = head
	if _, err := gitOut(e.Project, "config", "core.hooksPath", filepath.Join(devkit, "hooks")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(e.Origin, 0o755); err != nil {
		return nil, err
	}
	if _, err := gitOut(e.Origin, "init", "-q", "--bare", "-b", "main"); err != nil {
		return nil, err
	}
	if _, err := gitOut(e.Project, "remote", "add", "origin", e.Origin); err != nil {
		return nil, err
	}
	// журнал запусков утилит: без директории .devkit они молчат, а сценарии
	// как раз по журналу отличают вызов taskctl от правки доски редактором.
	if err := os.MkdirAll(filepath.Join(e.Project, ".devkit"), 0o755); err != nil {
		return nil, err
	}

	claude := filepath.Join(e.Home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		return nil, err
	}
	settings := filepath.Join(claude, "settings.json")
	if _, err := os.Stat(settings); err != nil { // раскладка могла принести свои
		if err := os.WriteFile(settings, []byte(fmt.Sprintf(hookSettings, devkit, devkit)), 0o644); err != nil {
			return nil, err
		}
	}
	agents := filepath.Join(claude, "agents")
	if !dirExists(agents) {
		if err := copyTree(filepath.Join(devkit, "kit", "agents"), agents, nil); err != nil {
			return nil, err
		}
	}
	// Скиллы едут тем же порядком, что и определения субагентов: готовому дому
	// из --home-seed есть чем их принести самому, и раскладка тогда уступает.
	skills := filepath.Join(claude, "skills")
	if !dirExists(skills) {
		if err := copyTree(filepath.Join(devkit, "kit", "skills"), skills, skipSkillNoise); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// skipSkillNoise отсеивает при раскладке в дом прогона то же самое, что
// skill_files() в tools/devkitctl/devkitctl.py отсеивает на боевой машине:
// служебное (__pycache__, точечные файлы вроде .DS_Store) заводит прогон
// тестов и файловый менеджер, а не автор скилла. Заодно снимает самопроверку
// kit/skills/check-skills.py с её тестом: она лежит прямо в kit/skills, не в
// подкаталоге со своим SKILL.md, скиллом не является, и реальная раскладка её
// тоже не копирует.
func skipSkillNoise(rel string) bool {
	if rel == "check-skills.py" || rel == "check_skills_test.py" {
		return true
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "__pycache__" || strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// pathExists отвечает и про битую ссылку: она в раскладке дома значит «занято»
// ровно так же, как живой каталог.
func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// seedRepo заводит историю синтетического проекта: три коммита с разными
// префиксами, чтобы у сценария про стиль коммита было откуда брать «префикс из
// истории». Хуки на этом шаге ещё не подключены: они проверяют работу агента, а
// не заготовку стенда.
func seedRepo(project string) error {
	steps := []struct {
		paths []string
		msg   string
	}{
		{[]string{"tool.py", "test_tool.py"}, "feat(tool): OB-000 clamp и тест"},
		{[]string{"README.md"}, "docs: OB-000 README утилиты"},
		{[]string{"-A"}, "docs(tasks): OB-001 и OB-002 на доске"},
	}
	if _, err := gitOut(project, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	for _, kv := range [][2]string{{"user.email", "stand@obeycheck"}, {"user.name", "obeycheck"}, {"commit.gpgsign", "false"}} {
		if _, err := gitOut(project, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	for _, st := range steps {
		if _, err := gitOut(project, append([]string{"add"}, st.paths...)...); err != nil {
			return err
		}
		if _, err := gitOut(project, "commit", "-q", "--no-verify", "-m", st.msg); err != nil {
			return err
		}
	}
	return nil
}

// environ собирает окружение прогона: временный HOME, указатели стенда и
// вычищенные переменные харнеса. Переменные вида CLAUDE* уносятся, чтобы
// вложенный прогон не наследовал сессию, из которой стенд запущен.
func (e *runEnv) environ(devkit, layout, scenario string, repeat int) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "HOME" || strings.HasPrefix(name, "CLAUDE") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"HOME="+e.Home,
		"OBEY_HOME="+e.Home,
		"OBEY_PROJECT="+e.Project,
		"OBEY_ORIGIN="+e.Origin,
		"OBEY_TRANSCRIPT="+e.Transcript,
		"OBEY_SEED="+e.Seed,
		"OBEY_DEVKIT="+devkit,
		"OBEY_LAYOUT="+layout,
		"OBEY_SCENARIO="+scenario,
		fmt.Sprintf("OBEY_REPEAT=%d", repeat),
		// Стенд пишет секреты файлами-маркерами во временный HOME, и file-бэкенд
		// их читает. На macOS default Keychain эти маркеры не видит, и secretctl
		// exec возвращает «секрета нет», из-за чего сценарий про секрет либо ложно
		// зеленеет, либо ловит обход. Та же фиксация бэкенда, что в тестах самого
		// secretctl: см. tools/secretctl/backend.go, defaultBackend.
		"SECRETCTL_BACKEND=file",
	)
}

// checkEnviron это окружение проверки сценария: то же, что у прогона, плюс
// каталог с командами стенда в начале PATH. Агенту эти команды не достаются:
// проверка судит его работу, и знать про судью он не должен.
func (e *runEnv) checkEnviron(env []string) []string {
	out := make([]string, 0, len(env)+1)
	seen := false
	for _, kv := range env {
		if name, val, _ := strings.Cut(kv, "="); name == "PATH" {
			kv = "PATH=" + e.Bin + string(os.PathListSeparator) + val
			seen = true
		}
		out = append(out, kv)
	}
	if !seen {
		out = append(out, "PATH="+e.Bin)
	}
	return out
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Окно редактора на второй подписке. Клиент claude держит конфиг в каталоге, на
// который смотрит CLAUDE_CONFIG_DIR, а endpoint и токен берёт из секции env его
// settings.json. Отсюда весь рычаг: окно, запущенное с этой переменной, ходит
// во вторую подписку, соседнее окно без неё в первую, и сессии друг о друге не
// знают. Руками рычаг дёргается плохо: переменную надо экспортировать перед
// каждым запуском, а забытый экспорт молча тратит дорогую подписку.

// altHarness это имя харнеса второй подписки, altMachineConfig машинный конфиг
// харнесов, altHomeKey ключ его секции с каталогом конфига. Каталог не прибит
// константой и не лежит в ключе выката: у него один хозяин, машинный слой, и
// оттуда же его читают раскладка машинного хозяйства (devkitctl) и окружение
// подпроцесса делегирования (agentctl). Два места под один каталог разъезжаются,
// а разъехавшись, дают самую дорогую поломку из возможных, окно на дорогой
// подписке, считающее себя дешёвым. Болванку конфига кладёт devkitctl doctor
// --fix, endpoint и токен вписывает пользователь.
const (
	altHarness       = "glm-code"
	altMachineConfig = ".devkit/harness.local"
	altHomeKey       = "home"
	// Переменная, которой devkit называет активный харнес; тем же именем её
	// читает agentctl (harnessEnv в tools/agentctl/harness.go).
	harnessEnv = "DEVKIT_HARNESS"
)

const altSettingsName = "settings.json"

// altConfigDir достаёт каталог конфига второй подписки из машинного конфига
// харнесов. Читается ровно один ключ, а не формат целиком: полный разбор
// подмножества TOML живёт в agentctl и devkitctl, общего пакета у отдельных
// go-модулей devkit нет (DK-063), и тащить сюда третий парсер ради одной строки
// незачем. Ключа нет, значит второй подписки на машине не заведено, и это отказ
// с готовой строкой, а не догадка про каталог.
func altConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, altMachineConfig)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	inSection := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i == 0 {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inSection = line == "["+altHarness+"]"
			continue
		}
		if !inSection {
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != altHomeKey {
			continue
		}
		val, err := altHomeValue(rest)
		if err != nil {
			return "", fmt.Errorf("%s: ключ %s секции [%s] не разобран (%v): каталог второй подписки берётся отсюда, и читать его наугад нельзя; значение это путь в двойных кавычках, после него допустим только комментарий",
				path, altHomeKey, altHarness, err)
		}
		if val == "" {
			break
		}
		// Ведущий ~/ разворачивается тут же: буквальным он завёл бы клиенту
		// каталог с именем ~ в рабочем дереве, и промах этот виден не сразу.
		if strings.HasPrefix(val, "~/") {
			val = filepath.Join(home, val[2:])
		}
		return val, nil
	}
	return "", fmt.Errorf("второй подписки на этой машине не объявлено: вписать %s в секцию [%s] файла %s (это каталог конфигурации, которым поднимается её клиент), дальше разложить болванку конфига: devkitctl doctor --fix",
		altHomeKey, altHarness, path)
}

// altHomeValue разбирает значение ключа тем же подмножеством TOML, каким его
// читают остальные: строка в двойных кавычках с экранированием \\, \", \n и \t,
// после закрывающей кавычки допустим только хвостовой комментарий, а решётка
// внутри кавычек это часть значения. Своей мерки тут быть не может: файл один на
// три инструмента, и разойдись они на решётке, один и тот же машинный конфиг
// читался бы по-разному, а расплатой было бы окно на дорогой подписке с мусором
// вместо каталога. Близнецы: parseTOMLValue в tools/agentctl/toml.go и
// parse_value в tools/devkitctl/harness.py, общего пакета у отдельных
// go-модулей devkit нет (DK-063).
func altHomeValue(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s[0] != '"' {
		return "", fmt.Errorf("значение %q не строка в двойных кавычках", s)
	}
	var b strings.Builder
	for i := 1; i < len(s); {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return "", fmt.Errorf("строка не закрыта")
			}
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("неизвестная escape-последовательность \\%c", s[i+1])
			}
			i += 2
			continue
		}
		if c == '"' {
			if tail := strings.TrimSpace(s[i+1:]); tail != "" && !strings.HasPrefix(tail, "#") {
				return "", fmt.Errorf("после значения лишнее: %q", tail)
			}
			return b.String(), nil
		}
		b.WriteByte(c)
		i++
	}
	return "", fmt.Errorf("строка не закрыта")
}

// editorBin это команда окна редактора. Имя фиксировано: сменный редактор
// потребовал бы своего ключа в машинном слое, а второго редактора у конвейера
// нет. Нового окна команда не просит: окно второй подписки одно, и повторный
// вызов на той же директории его фокусирует, а не плодит дубли.
const editorBin = "code"

// Копия окна: рабочее дерево, которое живёт дольше задачи. Дерево на задачу
// (shipctl start) проектировалось под субагентов, оно одноразовое, и merge его
// сносит вместе с веткой. Окно редактора живёт иначе: оно переживает задачу, и
// снос директории из-под живого окна убивает окно вместе с сессией и её
// контекстом (DK-192, живой случай IRC-097). Поэтому у окна своя копия
// репозитория, одна на все задачи: ветка переключается внутри неё, а merge
// вместо сноса переводит копию на слитый коммит.
const (
	// Окружение копии: клиент claude читает его из настроек директории, и
	// потому окно, открытое хоть из shipctl, хоть из дока, хоть из «Open
	// Recent», ходит во вторую подписку одинаково. Экспорт переменной перед
	// запуском этого не давал: окно, переоткрытое мимо shipctl, молча уходило
	// на дорогую подписку.
	windowEnvFile = ".claude/settings.local.json"
	// Заголовок окна: имя директории говорит про поставщика само, но в
	// заголовке видно ярус без разглядывания пути.
	windowTitleFile = ".vscode/settings.json"
)

// windowEnvKeys это ключи второй подписки, которые едут из машинного слоя в
// настройки копии. Ключей два комплекта (токен клиент берёт из
// ANTHROPIC_AUTH_TOKEN либо из ANTHROPIC_API_KEY), переносится заполненный.
var windowEnvKeys = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"}

// windowSuffix это хвост имени копии окна: имя харнеса без «-code»
// («glm-code» -> «glm»). Имя берётся от харнеса, а не от каталога конфига
// подписки: каталог пользовательский, лежит он где угодно и зовётся как
// угодно, а директория рядом с проектом нужна предсказуемая, её же ищет merge.
func windowSuffix() string {
	if s := strings.TrimSuffix(altHarness, "-code"); s != "" {
		return s
	}
	return altHarness
}

// windowTree: копия окна лежит рядом с проектом и называется
// ../<проект>-<поставщик>. Правило одно на code и на merge, иначе окно
// открывалось бы в одной директории, а слияние берегло другую.
func windowTree(primary string) string {
	return filepath.Join(filepath.Dir(primary), filepath.Base(primary)+"-"+windowSuffix())
}

// windowTitle это заголовок окна: ярус подписки и имя проекта.
func windowTitle(primary string) string {
	return "[" + strings.ToUpper(windowSuffix()) + "] " + filepath.Base(primary)
}

// ensureWindowTree заводит копию окна, если её ещё нет, и отдаёт её путь.
// Копия стоит detached: ветку в неё кладёт переключение задачи, и заведённая
// на ветке копия занимала бы её ещё до того, как задача выбрана.
func ensureWindowTree(primary string) (string, string, error) {
	tree := windowTree(primary)
	_, linked, err := worktrees(primary)
	if err != nil {
		return "", "", err
	}
	for _, l := range linked {
		if samePath(l.Path, tree) {
			return l.Path, "", nil
		}
	}
	if _, err := os.Stat(tree); err == nil {
		return "", "", fmt.Errorf("директория %s есть, а git не считает её рабочим деревом: разобрать её и убрать, копия окна заводится на это место", tree)
	}
	main, err := mainBranch(primary)
	if err != nil {
		return "", "", err
	}
	if out, err := git(primary, "worktree", "add", "--detach", tree, main); err != nil {
		return "", "", fmt.Errorf("копия окна не завелась:\n%s", tail(out))
	}
	// Путь отдаётся развёрнутым, как его отдаёт git списком деревьев: иначе
	// первое открытие окна называло бы копию одним путём, а следующее другим,
	// и в джамплисте редактора копилось бы по записи на каждое имя.
	if real, err := filepath.EvalSymlinks(tree); err == nil {
		tree = real
	}
	return tree, "копия окна заведена: " + tree, nil
}

// syncWindowFiles кладёт в копию окружение второй подписки и заголовок окна и
// правит их, когда машинный слой уехал вперёд. Файлы эти живут только на
// машине: в первом лежит токен, второй это настройка вида, и обоим место в
// исключениях гита, а не в коммите.
func syncWindowFiles(primary, tree string, a altSub) ([]string, error) {
	var notes []string
	env := map[string]any{harnessEnv: altHarness}
	for _, k := range windowEnvKeys {
		if v := a.Env[k]; v != "" {
			env[k] = v
		}
	}
	envPath := filepath.Join(tree, windowEnvFile)
	changed, err := patchJSON(envPath, "env", env, 0o600)
	if err != nil {
		return nil, err
	}
	if changed {
		notes = append(notes, "окружение копии переписано из машинного слоя: "+envPath)
	}
	titlePath := filepath.Join(tree, windowTitleFile)
	changed, err = patchJSON(titlePath, "", map[string]any{"window.title": windowTitle(primary)}, 0o644)
	if err != nil {
		return nil, err
	}
	if changed {
		notes = append(notes, "заголовок окна прописан: "+titlePath)
	}
	added, err := ensureExcluded(tree, windowEnvFile, windowTitleFile)
	if err != nil {
		return nil, err
	}
	if added != "" {
		notes = append(notes, added)
	}
	return notes, nil
}

// patchJSON дописывает в json-файл нужные ключи, не трогая соседних: рядом
// лежит написанное человеком (разрешения сессии в настройках claude, свои
// настройки редактора), и переписывать файл целиком значило бы это стирать.
// Секция это вложенный объект («env»), пустая значит верхний уровень.
func patchJSON(path, section string, keys map[string]any, mode os.FileMode) (bool, error) {
	doc := map[string]any{}
	raw, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if exists && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return false, fmt.Errorf("%s не читается как json (%v): поправить руками либо убрать файл, он заведётся заново", path, err)
		}
	}
	target := doc
	if section != "" {
		sub, _ := doc[section].(map[string]any)
		if sub == nil {
			sub = map[string]any{}
		}
		target = sub
	}
	changed := !exists
	for k, v := range keys {
		if cur, ok := target[k]; !ok || cur != v {
			target[k] = v
			changed = true
		}
	}
	if section != "" {
		doc[section] = target
	}
	if !changed {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, append(out, '\n'), mode); err != nil {
		return false, err
	}
	// Права сужаются и на уже лежавшем файле: в окружении копии лежит токен.
	return true, os.Chmod(path, mode)
}

// ensureExcluded прячет файлы копии от гита. Пишется это в исключения самого
// репозитория (info/exclude общего каталога .git), а не в .gitignore: файлы
// машинные, и строка про них в коммитимом .gitignore ехала бы всем, кто взял
// проект. Незаигноренные, они лежали бы в копии untracked-черновиками и
// отбивали бы переключение задачи чистотой дерева.
func ensureExcluded(tree string, names ...string) (string, error) {
	dir, err := git(tree, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(tree, dir)
	}
	path := filepath.Join(dir, "info", "exclude")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	have := map[string]bool{}
	for _, ln := range strings.Split(string(raw), "\n") {
		have[strings.TrimSpace(ln)] = true
	}
	add := []string{}
	for _, n := range names {
		if !have[n] {
			add = append(add, n)
		}
	}
	if len(add) == 0 {
		return "", nil
	}
	text := string(raw)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += "# Настройки копии окна второй подписки (shipctl code): токен и вид окна.\n" +
		strings.Join(add, "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return "в исключения репозитория (" + path + ") добавлено: " + strings.Join(add, ", "), nil
}

// editorLimit это предел ожидания редактора. Команда code отдаёт работу
// приложению и выходит сразу, так что предел тут только против зависшего
// запуска: без него shipctl молча стоял бы вместо отчёта.
const editorLimit = 2 * time.Minute

// probeLimit это предел ожидания пробника: он ходит в чужой endpoint, и
// висящий запрос превратил бы проверку окружения в зависшую команду.
const probeLimit = 20 * time.Second

// altSub это машинный слой второй подписки, прочитанный из её settings.json.
// Токен лежит нераскрытым полем и наружу не выходит ни строкой отчёта, ни
// текстом ошибки: у него есть только признак «есть» или «нет».
type altSub struct {
	Dir      string // каталог конфига второй подписки в машинном слое
	Settings string // файл, откуда прочитано
	Env      map[string]string
	BaseURL  string
	Model    string
	token    string
	bearer   bool // токен уходит заголовком Authorization, а не x-api-key
}

func (a altSub) tokenState() string {
	if a.token == "" {
		return "нет"
	}
	return "есть"
}

// redact вычищает токен из чужого текста. Тело ответа endpoint печатается в
// ошибке пробника, и запрет на печать токена держится тут, а не надеждой на
// то, что чужая сторона его не вернёт.
func (a altSub) redact(s string) string {
	if a.token == "" {
		return s
	}
	return strings.ReplaceAll(s, a.token, "<токен>")
}

// loadAltSub читает конфиг второй подписки из машинного слоя.
func loadAltSub() (altSub, error) {
	dir, err := altConfigDir()
	if err != nil {
		return altSub{}, err
	}
	a := altSub{Dir: dir}
	a.Settings = filepath.Join(a.Dir, altSettingsName)
	raw, err := os.ReadFile(a.Settings)
	if err != nil {
		if os.IsNotExist(err) {
			return a, fmt.Errorf("нет конфига второй подписки %s: разложить болванку и узнать, каких ключей в ней не хватает: devkitctl doctor --fix", a.Settings)
		}
		return a, err
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return a, fmt.Errorf("%s не читается как json: %v; переразложить болванку: devkitctl doctor --fix", a.Settings, err)
	}
	a.Env = doc.Env
	a.BaseURL = strings.TrimRight(doc.Env["ANTHROPIC_BASE_URL"], "/")
	a.Model = doc.Env["ANTHROPIC_MODEL"]
	// Клиент берёт токен из двух ключей и по-разному кладёт их в запрос,
	// поэтому пробник запоминает, какой именно ключ заполнен.
	if t := doc.Env["ANTHROPIC_AUTH_TOKEN"]; t != "" {
		a.token, a.bearer = t, true
	} else if t := doc.Env["ANTHROPIC_API_KEY"]; t != "" {
		a.token = t
	}
	// Пустой endpoint это не полбеды, а тихий уход окна на первую подписку:
	// claude без ANTHROPIC_BASE_URL ходит туда же, куда обычная сессия.
	if a.BaseURL == "" {
		return a, fmt.Errorf("в %s пустой env.ANTHROPIC_BASE_URL: окно ушло бы на первую подписку молча; вписать endpoint второй подписки, незаполненные ключи называет devkitctl doctor", a.Settings)
	}
	return a, nil
}

type CodeParams struct {
	ID     string
	DryRun bool
	Probe  bool
}

// cmdCode открывает окно редактора на копии окна и переключает её на ветку
// задачи, а с --dry-run печатает то же окружение, ничего не запуская. Ветка
// заводится тем же путём, что и shipctl start: отдельной команды для этого не
// заводится, иначе у задачи появилось бы два способа получить ветку.
func cmdCode(root string, p CodeParams) (string, error) {
	a, err := loadAltSub()
	if err != nil {
		return "", err
	}
	primary, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	// Корп-контур отбивается сразу: там дерево задачи заводится в клоне кода и
	// называется ключом тикета, а вторая подписка это личный доступ, которому в
	// репозитории компании делать нечего.
	if _, bound := loadTrackerBinding(primary); bound {
		return "", fmt.Errorf("%s это корп-контур: вторая подписка личная, и окно на код компании она не открывает; работать обычной сессией", primary)
	}
	// Пробник идёт до заведения дерева, а не после: подтверждать подписку он
	// обязан раньше побочных действий, иначе мёртвый endpoint оставляет после
	// себя заведённую ветку и сдвинутую в In progress доску, а окна всё равно
	// не даёт (ревью DK-175).
	probe := ""
	if p.Probe {
		line, err := probeAlt(a)
		if err != nil {
			return "", err
		}
		probe = line
	}
	// Dry-run ничего не заводит и не двигает: он показывает окружение, а не
	// готовит работу, и заведённая им копия с переведённой доской осталась бы
	// на уборку руками.
	msg := []string{}
	tree := windowTree(primary)
	if p.DryRun {
		state := "уже заведена"
		if _, err := os.Stat(tree); err != nil {
			state = "ещё не заведена, заведёт запуск без --dry-run"
		}
		msg = append(msg,
			"копия окна: "+tree+" ("+state+")",
			"ветка задачи "+strings.ToLower(p.ID)+" переключается внутри копии (тем же путём, что shipctl start)")
	} else {
		t, note, err := ensureWindowTree(primary)
		if err != nil {
			return "", err
		}
		tree = t
		if note == "" {
			note = "копия окна: " + tree
		}
		msg = append(msg, note)
		notes, err := syncWindowFiles(primary, tree, a)
		if err != nil {
			return "", err
		}
		msg = append(msg, notes...)
		out, err := cmdStart(primary, StartParams{ID: p.ID, Tree: tree})
		if err != nil {
			return "", err
		}
		msg = append(msg, out)
	}
	msg = append(msg,
		"машинный слой: "+a.Settings,
		"окружение копии: "+filepath.Join(tree, windowEnvFile)+" ("+strings.Join(windowEnvNames(a), ", ")+")",
		"base URL: "+a.BaseURL,
		"модель: "+altModel(a),
		"токен: "+a.tokenState(),
		"заголовок окна: "+windowTitle(primary),
		"редактор: "+strings.Join(editorArgv(tree), " "))
	if probe != "" {
		msg = append(msg, probe)
	}
	if p.DryRun {
		msg = append(msg, "dry-run: редактор не запускался")
		return strings.Join(msg, "\n"), nil
	}
	if err := openEditor(tree); err != nil {
		return "", err
	}
	msg = append(msg, "окно открыто, claude в нём ходит во вторую подписку")
	return strings.Join(msg, "\n"), nil
}

// editorArgv это команда окна. Флага -n тут нет: окно второй подписки одно, и
// повторный вызов на той же директории его фокусирует. Раньше флаг был нужен
// затем, чтобы дерево задачи не открылось вкладкой в окне первой подписки; с
// окружением, привязанным к директории, этой опасности нет вовсе.
func editorArgv(tree string) []string {
	return []string{editorBin, tree}
}

// windowEnvNames это имена ключей окружения копии для отчёта. Значения не
// печатаются: среди них токен, а у него есть только признак «есть» или «нет».
// Имя харнеса печатается со значением: оно не секрет, а без него сессия во
// втором окне считала бы себя домашней (детект по переменным тут неоднозначен
// по построению, CLAUDECODE=1 виден и там), то есть разворачивала бы ярусы по
// чужому маппингу и корректировала вердикты по чужому остатку.
func windowEnvNames(a altSub) []string {
	names := []string{}
	for _, k := range windowEnvKeys {
		if a.Env[k] != "" {
			names = append(names, k)
		}
	}
	return append(names, harnessEnv+"="+altHarness)
}

// altModel это модель второй подписки для отчёта: ключа в конфиге может и не
// быть, и молчаливый пропуск строки читался бы как «модель по умолчанию».
func altModel(a altSub) string {
	if a.Model == "" {
		return "не задана (env.ANTHROPIC_MODEL пуст)"
	}
	return a.Model
}

// openEditor запускает окно на копии. Окружение подписки процессу редактора не
// ставится: оно лежит в настройках самой директории, и окно, открытое из дока
// или «Open Recent», получает его наравне с открытым отсюда. Экспорт перед
// запуском давал обратное, окно мимо shipctl молча уходило на первую подписку.
func openEditor(tree string) error {
	bin, err := exec.LookPath(editorBin)
	if err != nil {
		return fmt.Errorf("команда %s не найдена в PATH: окно открывать нечем; поставить её из vscode (палитра команд, Shell Command: Install 'code' command in PATH)", editorBin)
	}
	cmd := exec.Command(bin, tree)
	cmd.Dir = tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(editorLimit)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s не открыл окно: %v (%s)", editorBin, err, strings.TrimSpace(buf.String()))
		}
		return nil
	case <-timer.C:
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return fmt.Errorf("%s не кончился за %s и убит: окно не открылось", editorBin, editorLimit)
	}
}

// probeAlt стучится в endpoint второй подписки Anthropic-совместимым запросом.
// Печать окружения показывает, что окно уйдёт по нужному адресу, но не что там
// живая подписка: протухший токен и опечатка в адресе выглядят на печати
// одинаково исправно, а различает их только ответ.
func probeAlt(a altSub) (string, error) {
	if a.token == "" {
		return "", fmt.Errorf("пробнику нечем представиться: в %s нет токена второй подписки (env.ANTHROPIC_AUTH_TOKEN пуст)", a.Settings)
	}
	if a.Model == "" {
		return "", fmt.Errorf("пробнику нечего спросить: в %s пустой env.ANTHROPIC_MODEL", a.Settings)
	}
	url := a.BaseURL + "/v1/messages"
	body, err := json.Marshal(map[string]any{
		"model":      a.Model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("пробник не собрал запрос к %s: %v", url, err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.bearer {
		req.Header.Set("authorization", "Bearer "+a.token)
	} else {
		req.Header.Set("x-api-key", a.token)
	}
	resp, err := (&http.Client{Timeout: probeLimit}).Do(req)
	if err != nil {
		return "", fmt.Errorf("пробник не достучался до %s: %v", url, a.redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	answer := strings.TrimSpace(a.redact(string(raw)))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("пробник %s: ответ %s, окно уйдёт в нерабочую подписку; проверить endpoint и токен в %s (%s)", url, resp.Status, a.Settings, tail(answer))
	}
	var ans struct {
		Model string `json:"model"`
	}
	json.Unmarshal(raw, &ans)
	if ans.Model == "" {
		return "", fmt.Errorf("пробник %s: ответ 200, а модели в теле нет: endpoint отвечает не по-Anthropic-совместимому (%s)", url, tail(answer))
	}
	return fmt.Sprintf("пробник %s: 200, модель в ответе %s", url, ans.Model), nil
}

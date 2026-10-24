package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/gitrun"
	"github.com/dronrider/devkit/internal/taskform"
)

// Проверяющие этого файла переехали из ворот слияния shipctl без изменения
// поведения (LLD DK-972, решение 6): ворота зовут их отсюда, а этап плана
// спрашивает тот же след словом из шаблона.

// RunLogPath это журнал запусков, по нему regcheck ищет свой прогон.
const RunLogPath = ".devkit/log"

// git это единственная дорога к git из пакета: предел времени на разговоре с
// remote живёт в общем пакете (DK-697).
func git(root string, args ...string) (string, error) {
	limit, err := gitrun.Timeout()
	if err != nil {
		return "", err
	}
	return gitrun.Run(root, args, limit)
}

// Tests отказывает ветке без тестовых файлов в диффе против main. Правка едет
// вместе с тестами (RULES.md, «Тесты обязательны»), а пропуск этого шага по
// статистике ловится только вниманием ревьювера. Бескодовая ветка (только
// docs/) проходит: кода нет, тест не нужен. Тестовый файл узнаётся по
// соглашениям проекта (IsTestFile), предикат без привязки к одному языку.
// Оговарка: проверяющий ловит наличие файла, а не его содержание, пустая
// заглушка пройдёт, поэтому обещать полную проверку таким следом нельзя.
func Tests(root, main, branch string, docsBranch bool, doc string) error {
	if docsBranch {
		return nil
	}
	files, err := git(root, "diff", "--name-only", main+"..."+branch)
	if err != nil {
		// Ошибка git здесь не валит проверку по тому же правилу, что и у
		// пересечения поезда: следу хватит общего случая, а падать из-за неё
		// дороже, чем пропустить.
		return nil
	}
	// Пустой дифф значит ветку без правки кода (забытая ветка без коммитов):
	// мерджить нечего, и след тестов тут не к чему, как и у бескодовой ветки,
	// только с другой стороны.
	if strings.TrimSpace(files) == "" {
		return nil
	}
	for _, f := range strings.Split(files, "\n") {
		if IsTestFile(strings.TrimSpace(f)) {
			return nil
		}
	}
	if taskform.Exception(doc, taskform.GateTests) {
		return nil
	}
	return fmt.Errorf("в диффе ветки против main нет тестовых файлов: правка едет вместе с тестами (RULES.md, «Тесты обязательны»); добавить тест или загасить ворот пометкой «- Исключение: тесты (причина)» в docs/tasks/<ID>.md, если правка бескодовая или тест к ней неприменим")
}

// IsTestFile узнаёт тест по соглашениям об именах, общим для языков проекта:
// Go (суффикс _test.go), Python (test_*.py и *_test.py), shell (test_*.sh и
// *_test.sh). Предикат консервативный: лучше признать файл тестом, чем
// пропустить. Путь берётся с каталогом, как его отдаёт git diff.
func IsTestFile(path string) bool {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if base == "" {
		return false
	}
	if strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, "_test.py") ||
		strings.HasSuffix(base, "_test.sh") ||
		strings.HasSuffix(base, "_test.rs") {
		return true
	}
	if strings.HasPrefix(base, "test_") && (strings.HasSuffix(base, ".py") || strings.HasSuffix(base, ".sh")) {
		return true
	}
	// Rust и JVM кладут тест не по имени файла, а по месту: у Cargo это
	// tests/ рядом с src, у Gradle и Maven src/test. Без этого след врал бы
	// на любом Rust-проекте с честными интеграционными тестами.
	return inTestDir(path, base)
}

// inTestDir опознаёт тест по каталогу, в котором тот лежит. Каталог берётся
// вместе с расширением файла: голое имя каталога считало бы тестом и
// заглушку стенда, и данные примера, лежащие там же.
// У Cargo отдельным тестовым крейтом собирается только .rs на верхнем
// уровне tests/, всё, что глубже, это модули-хелперы для тестов. У Gradle
// и Maven расходная ветка по пакетам позволяет оставить проверку по пути:
// компиляция src/test/kotlin идёт отдельно, реорганизация пакетов меняет
// путь ввода.
func inTestDir(path, base string) bool {
	dirs := strings.Split(path, "/")
	if len(dirs) < 2 {
		return false
	}
	dirs = dirs[:len(dirs)-1]
	rust := strings.HasSuffix(base, ".rs")
	jvm := strings.HasSuffix(base, ".kt") || strings.HasSuffix(base, ".java")
	for i, d := range dirs {
		if rust && d == "tests" && i == len(dirs)-1 {
			// Для Cargo: только верхний уровень tests/, не подпапки
			return true
		}
		if jvm && d == "test" && i > 0 && dirs[i-1] == "src" {
			return true
		}
	}
	return false
}

// Regcheck отказывает bug-задаче, если в журнале запусков нет зелёного
// regcheck за время жизни ветки: регрессионный тест обязан краснеть на
// старом коде (RULES.md, «Тесты обязательны»), а этот шаг по статистике
// пропускается чаще других. Где regcheck неприменим (правка и тест в одном
// файле, правка бескодовая), след гасится пометкой-исключением в файле
// задачи, а не снимается молча: молчание тут неотличимо от зелёного
// прогона. Без самого журнала след не работает: каталог .devkit ложится
// обвязкой выката и командой start задолго до того, как хоть один прогон
// оставит в нём строку, поэтому страж привязан к файлу журнала, а не к
// каталогу. Журнал берётся из logRoot: при работе через worktree прогоны
// regcheck оседают в дереве задачи, а не в основном чекауте.
// Оговарка: след ловит наличие прогона, а не его качество, прогон мог пройти
// мимо правки или мимо тестов задачи, поэтому обещать «100% проверено» таким
// следом нельзя.
func Regcheck(root, logRoot, main, branch, taskType, doc string) error {
	if !strings.Contains(taskType, "bug") {
		return nil
	}
	if _, err := os.Stat(filepath.Join(logRoot, RunLogPath)); err != nil {
		return nil
	}
	if taskform.Exception(doc, taskform.GateRegcheck) {
		return nil
	}
	// Начало жизни ветки это коммит, где она отошла от main. regcheck прошлой
	// задачи остаётся за границей: после её слияния main уехал вперёд и
	// merge-base новой ветки свежее того прогона.
	base, err := git(root, "merge-base", main, branch)
	if err != nil {
		return nil
	}
	ctStr, err := git(root, "log", "-1", "--format=%ct", base)
	if err != nil {
		return nil
	}
	ct, err := strconv.ParseInt(ctStr, 10, 64)
	if err != nil {
		return nil
	}
	if regcheckLogged(filepath.Join(logRoot, RunLogPath), time.Unix(ct, 0)) {
		return nil
	}
	return fmt.Errorf("задача типа bug, а в %s нет зелёного regcheck за время жизни ветки: регрессионный тест обязан краснеть на старом коде (RULES.md, «Тесты обязательны»); прогнать regcheck и повторить, а где он неприменим (правка и тест в одном файле, бескодовая правка) загасить ворот пометкой «- Исключение: regcheck (причина)» в docs/tasks/<ID>.md",
		RunLogPath)
}

// regcheckLogged ищет в журнале успешный запуск regcheck не старше since.
func regcheckLogged(path string, since time.Time) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, ln := range strings.Split(string(data), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) < 4 || f[1] != "regcheck" || f[3] != "0" {
			continue
		}
		ts, err := time.ParseInLocation("2006-01-02T15:04:05", f[0], time.Local)
		if err != nil {
			continue
		}
		if !ts.Before(since) {
			return true
		}
	}
	return false
}

// ReviewLevel требует строку уровня в разделе «Ревью» («Уровень 2 до
// a1b2c3d: причина», её пишет taskctl review level). Уровень тщательности
// выбирает скилл review до чтения диффа, и эта строка единственный машинный
// след того, что ревью шло по скиллу: без неё пропуск ревью неотличим от
// ревью, прошедшего мимо. Нулевой уровень проходит, это осознанный пропуск с
// причиной, а не молчание. Бескодовая ветка тут не исключение: у LLD и доки
// ревью тоже есть, и читается оно тем же скиллом. Судится первая непустая
// строка раздела вне ограждённых блоков: замечания и вердикт идут ниже неё.
// Где ревью неприменимо, след гасится пометкой-исключением, как и соседние.
func ReviewLevel(id, doc string) error {
	if HasReviewLevel(doc) {
		return nil
	}
	if taskform.Exception(doc, taskform.GateReview) {
		return nil
	}
	return fmt.Errorf("нет строки уровня ревью в разделе «Ревью» docs/tasks/%s.md, ревью шло мимо скилла review или не шло вовсе: taskctl review level %s <0-3> \"причина\"; если ревью тут неприменимо, загасить ворот пометкой «- Исключение: ревью (причина)»",
		id, id)
}

// HasReviewLevel говорит, стоит ли строка уровня первой непустой строкой
// раздела «Ревью». Читателей у критерия двое, ворот слияния и след у пуша, и
// разойдись они, пуш отбивал бы ветку, которую merge пропускает.
func HasReviewLevel(doc string) bool {
	for _, ln := range taskform.SectionLines(doc, taskform.Review) {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		return taskform.IsReviewLevel(ln)
	}
	return false
}

// OpenReviewNotes возвращает замечания раздела «Ревью» файла задачи без
// зафиксированного исхода: по RULES.board.md у каждого элемента списка должен
// стоять итог «исправлено» либо «отклонено» с причиной. Замечанием считается
// элемент списка целиком (маркерная строка вместе со строками продолжения),
// а вердикт без замечаний замечанием не считается.
func OpenReviewNotes(root, id string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var open []string
	for _, item := range reviewItems(string(data), taskform.Review) {
		if reviewOutcome(item) == "" {
			open = append(open, strings.TrimSpace(item))
		}
	}
	return open, nil
}

// reviewItems собирает элементы списка раздела: маркерная строка вместе со
// строками продолжения (до пустой или следующей маркерной) составляет один
// элемент целиком. Абзац без маркера элементом не считается, поэтому чистый
// вердикт абзацем парсер не видит вовсе, а замечание, перенесённое на
// несколько строк, судится по всему тексту, и исход на строке переноса
// закрывает его.
func reviewItems(doc, heading string) []string {
	var items []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			items = append(items, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, ln := range taskform.SectionLines(doc, heading) {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			flush()
			cur = append(cur, t)
		case t == "":
			flush()
		case len(cur) > 0:
			cur = append(cur, t)
		}
	}
	flush()
	return items
}

// resolvedTailRe узнаёт исход по месту, где его пишет taskctl review resolve
// ("<текст>: исправлено" либо "<текст>: отклонено, причина"), а не по факту
// появления слова где-то в тексте: цитата замечания, пересказывающая формат
// команды, этому хвосту не соответствует и замечание не закрывает (DK-514).
var resolvedTailRe = regexp.MustCompile(`: (исправлено|отклонено)(,.*)?$`)

// cleanVerdictRe узнаёт чистый вердикт по голове элемента, куда его пишет
// ревьювер («Вердикт: без замечаний. пояснение» либо «замечаний нет» без
// пояснения). За фразой идёт точка, двоеточие, необязательный хвост
// «до <sha>» второго и следующих кругов либо конец текста, поэтому «замечаний
// неточностей» и продолжение фразы перечислением вердиктом не считаются.
// Маркер списка терпится потому, что reviewItems отдаёт элементы вместе с
// маркером.
var cleanVerdictRe = regexp.MustCompile(`^(?:[-*] )?(?:(?:вердикт|ревью):\s*)?(?:без замечаний|замечаний нет)(?:\s+до\s+[0-9a-f]{4,40})?(?:[.:]|$)`)

// reviewOutcome возвращает исход элемента раздела «Ревью»: «исправлено»,
// «отклонено», «чисто» (вердикт без замечаний) или пусто у открытого
// замечания. Порядок проверок исход -> чистый итог -> открыто держит
// «гибрид: исправлено, теперь без замечаний» закрытым.
func reviewOutcome(item string) string {
	low := strings.ToLower(item)
	if m := resolvedTailRe.FindStringSubmatch(low); m != nil {
		return m[1]
	}
	if cleanVerdictRe.MatchString(low) {
		return "чисто"
	}
	return ""
}

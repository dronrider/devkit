package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/accept"
	"github.com/dronrider/devkit/internal/obey"
	"github.com/dronrider/devkit/internal/taskform"
)

const runLogPath = ".devkit/log"

// pipelineState это всё, что подсказке нужно знать о конвейере: где стоят
// задачи и кому доверена кнопка слияния.
type pipelineState struct {
	inProgress []string
	check      []string
	// Строки Check, разобранные по тому, кто их сдаёт: waiting это виды user
	// и mixed вместе с видом в скобках, agentSmoked это агентские строки с
	// прогнанным smoke и вложенным выводом (тик сторожка доводит их до Done),
	// agentStuck это агентские с прогнанным smoke, но пустым разделом
	// «Проверка» (тик такую не закроет), agentRest это агентские, сценарий
	// которых после выката ещё не прогнан.
	waiting     []string
	agentSmoked []string
	agentStuck  []string
	agentRest   []string
	train       []string
	failed      []string
	autonomous  bool
}

// checkParts делит строки Check по виду приёмки и отметке smoke. Строка с
// непогашенным провалом не попадает никуда: её называет отдельная строка
// status, а закрыть её не даст ни человек, ни тик, пока прод не починен.
// Вид читается из заголовка строки (LLD DK-292, решение 3), а прогнанный smoke
// значит, что агентскую строку закроет тик сторожка (DK-516), и звать по ней
// человека незачем.
func checkParts(root string, b *board, smoked []string) (waiting, agentSmoked, agentStuck, agentRest []string) {
	for _, r := range b.sects["check"] {
		switch kind := accept.KindOf(r.Title); {
		case failSufRe.MatchString(r.Title):
			// Непогашенный провал держит очередь целиком, и про такую строку
			// status кричит своей строкой. Ни человеку в приёмку, ни тику она
			// сейчас не принадлежит: сперва чинится прод.
		case kind != accept.Agent:
			waiting = append(waiting, r.ID+" ("+kind+")")
		case !slices.Contains(smoked, r.ID):
			agentRest = append(agentRest, r.ID)
		case strings.TrimSpace(strings.Join(sectionLines(readTaskDoc(root, r.ID), taskform.Verification), "\n")) == "":
			// Тот же рубеж, что у ворот close: без вложенного вывода прогона
			// агентская задача не закрывается ни руками, ни тиком.
			agentStuck = append(agentStuck, r.ID)
		default:
			agentSmoked = append(agentSmoked, r.ID)
		}
	}
	return waiting, agentSmoked, agentStuck, agentRest
}

// nextStep называет следующий шаг конвейера одной строкой. Знание «что
// дальше» живёт в выводе утилиты, а не в прозе скилла: подсказку читает любая
// модель и ровно в тот момент, когда решение принимается, а абзац скилла к
// этому моменту прочитан час назад или не прочитан вовсе (DK-180, DK-205).
// Порядок веток это порядок срочности: сломанный прод держит очередь целиком,
// дальше проверка выкаченного, потом стоящий поезд, и только потом работа над
// кодом.
func nextStep(st pipelineState) string {
	switch {
	case len(st.failed) > 0:
		return "следующий шаг: чинить прод по " + strings.Join(st.failed, ", ") +
			" (shipctl revert <ID> либо форвард-фикс), очередь стоит целиком, пока висит признак провала"
	case len(st.check) > 0:
		return checkStep(st)
	case len(st.train) > 0:
		return "следующий шаг: выкатить поезд (shipctl ship), в нём " + strings.Join(st.train, ", ")
	case len(st.inProgress) > 0:
		return mergeFork(st.inProgress, st.autonomous)
	}
	return "следующий шаг: взять задачу с доски (taskctl list, дальше taskctl move <ID> in-progress)"
}

// checkStep называет, что делать с Check, помечая каждую строку по её виду
// приёмки. Прежняя общая фраза «прогнать сценарий и закрыть» не различала
// виды приёмки и не упоминала тик сторожка: строка вида agent с прогнанным
// smoke стояла в Check до утра с тем же советом «закрыть задачу», хотя
// закрывать было некому (DK-516).
func checkStep(st pipelineState) string {
	var parts []string
	if len(st.agentRest) > 0 {
		parts = append(parts, "прогнать сценарий проверки "+strings.Join(st.agentRest, ", ")+
			" и закрыть задачу (taskctl close <ID>)")
	}
	if len(st.agentSmoked) > 0 {
		parts = append(parts, "агентские "+strings.Join(st.agentSmoked, ", ")+
			" доведёт до Done тик devkitctl watch, руками их закрывать не нужно")
	}
	if len(st.agentStuck) > 0 {
		parts = append(parts, "вложить вывод прогона в раздел «Проверка» файла задачи "+
			strings.Join(st.agentStuck, ", ")+": без вывода такую строку не закроет ни тик, ни taskctl close")
	}
	if len(st.waiting) > 0 {
		parts = append(parts, "приёмка за человеком по "+strings.Join(st.waiting, ", ")+
			", закрытия они ждут от пользователя")
	}
	if len(parts) == 0 {
		parts = append(parts, "прогнать сценарий проверки "+strings.Join(st.check, ", ")+
			" и закрыть задачу (taskctl close <ID>)")
	}
	return "следующий шаг: " + strings.Join(parts, "; ")
}

// mergeFork это развилка «кто нажимает кнопку» перед слиянием. Ветка
// autonomous называется явно и вместе со значением флага: в ядре правил
// автономный режим сказан исключением в хвосте, и на нём агент сходит с
// конвейера ровно на готовой задаче (DK-205, диспетчер остановился со словами
// «пуш за тобой» при autonomous = true).
func mergeFork(ids []string, autonomous bool) string {
	head := "следующий шаг по " + strings.Join(ids, ", ") + ": довести код с тестами, позвать ревью"
	if autonomous {
		return head + " и слить самому (shipctl merge <ID>): в " + deployConfigPath +
			" стоит autonomous = true, merge пушит и катит выкат сам, отдельного слова пользователя тут не ждут"
	}
	return head + ", а слияние за пользователем: в " + deployConfigPath +
		" стоит autonomous = false, агент останавливается на локальном коммите и ждёт команды"
}

// nextAfterMerge говорит, чем задача сдаётся после слияния и выката. Совет
// один на вид, а не обе ветки сразу: вид приёмки читается из строки доски
// (LLD DK-292, решение 3), и у каждого вида свой следующий шаг (LLD DK-292,
// решение 2). Агентский вид прогоняет сценарий и закрывает задачу, смешанный
// прогоняет агентскую часть, вкладывает вывод и ждёт пользователя,
// пользовательский ждёт слова человека. У поезда задачи группируются по виду,
// и каждый вид звучит своей строкой.
func nextAfterMerge(b *board, ids []string) string {
	byKind := map[string][]string{}
	for _, id := range ids {
		kind := accept.Agent
		if r := b.rowOf(id); r != nil {
			kind = accept.KindOf(r.Title)
		}
		byKind[kind] = append(byKind[kind], id)
	}
	var lines []string
	for _, kind := range []string{accept.Agent, accept.Mixed, accept.User} {
		if grp := byKind[kind]; len(grp) > 0 {
			lines = append(lines, nextStepByKind(kind, strings.Join(grp, ", ")))
		}
	}
	return strings.Join(lines, "\n")
}

// nextStepByKind это один следующий шаг по виду приёмки. Тексты зафиксированы
// решением 2 LLD DK-292: у каждого вида свой путь сдачи.
func nextStepByKind(kind, ids string) string {
	switch kind {
	case accept.Mixed:
		return "следующий шаг: прогнать агентскую часть сценария " + ids +
			", вложить вывод в файл задачи, дальше задачу ждёт пользователь"
	case accept.User:
		return "следующий шаг: " + ids + " ждёт пользователя (пользовательский сценарий проверки, прогон за человеком)"
	default:
		return "следующий шаг: прогнать сценарий проверки " + ids +
			" и закрыть задачу (taskctl close <ID>)"
	}
}

// readTaskDoc читает файл задачи как строку. Нет файла значит пустая строка:
// ворота, которые его разбирают (сценарий, пометки-исключения), на отсутствующем
// файле работают как на пустом, а отсутствует он только в тестах и у задач,
// которые ещё не доросли до файла вовсе. Читается в дереве ветки (reviewRoot):
// файл пишется туда же, где ветка.
func readTaskDoc(reviewRoot, id string) string {
	data, err := os.ReadFile(taskFilePath(reviewRoot, id))
	if err != nil {
		return ""
	}
	return string(data)
}

// regcheckGate отказывает слиянию bug-задачи, если в журнале запусков нет
// зелёного regcheck за время жизни ветки: регрессионный тест обязан краснеть на
// старом коде (RULES.md, «Тесты обязательны»), а этот шаг по статистике
// пропускается чаще других. Где regcheck неприменим (правка и тест в одном
// файле, правка бескодовая), ворот гасится пометкой-исключением в файле задачи,
// а не снимается молча: молчание тут неотличимо от зелёного прогона. Без самого
// журнала (.devkit/log) ворот не работает: каталог .devkit ложится обвязкой
// выката и командой start задолго до того, как хоть один прогон оставит в нём
// строку, поэтому страж привязан к файлу журнала, а не к каталогу. Журнал
// берётся из logRoot: при работе через worktree прогоны regcheck оседают в
// дереве задачи, а не в основном чекауте.
// Оговарка: ворот ловит наличие прогона, а не его качество, прогон мог пройти
// мимо правки или мимо тестов задачи, поэтому обещать «100% проверено» таким
// воротом нельзя.
func regcheckGate(root, logRoot, main, branch, taskType, doc string) error {
	if !strings.Contains(taskType, "bug") {
		return nil
	}
	if _, err := os.Stat(filepath.Join(logRoot, runLogPath)); err != nil {
		return nil
	}
	if hasException(doc, gateRegcheck) {
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
	if regcheckLogged(filepath.Join(logRoot, runLogPath), time.Unix(ct, 0)) {
		return nil
	}
	return fmt.Errorf("задача типа bug, а в %s нет зелёного regcheck за время жизни ветки: регрессионный тест обязан краснеть на старом коде (RULES.md, «Тесты обязательны»); прогнать regcheck и повторить, а где он неприменим (правка и тест в одном файле, бескодовая правка) загасить ворот пометкой «- Исключение: regcheck (причина)» в docs/tasks/<ID>.md",
		runLogPath)
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

// trainWarnings проговаривает мягкие критерии поезда из RULES.board.md
// («Ветки, ревью и деплой» п. 9): цена S или M, не больше 3-5 задач, задачи
// не трогают одни файлы. Нарушение не валит merge, критерии на суждении
// (связность «по смыслу» git не видит), но в отчёте оно обязано прозвучать.
func trainWarnings(root, main, branch string, b *board, id string, train []string) []string {
	var warns []string
	if r := b.rowOf(id); r != nil {
		switch r.Cost {
		case "", "S", "M":
		case "-":
			warns = append(warns, "предупреждение: цена "+id+" не оценена, в поезд берут S или M со снятой неопределённостью (RULES.board.md п. 9)")
		default:
			warns = append(warns, "предупреждение: цена "+id+" это "+r.Cost+", в поезд берут S или M, крупное едет одиночным выкатом (RULES.board.md п. 9)")
		}
	}
	if len(train) >= 5 {
		warns = append(warns, fmt.Sprintf("предупреждение: в поезде уже %d задач(и), больше 3-5 не копят, регресс без сценария ищется перебором состава: пора shipctl ship", len(train)))
	}
	if overlaps, err := trainOverlap(root, main, branch, train); err == nil && len(overlaps) > 0 {
		warns = append(warns, "предупреждение: ветка трогает файлы задач поезда ("+strings.Join(overlaps, "; ")+"), в поезд берут независимые задачи")
	}
	return warns
}

// scenarioGate отказывает слиянию, если в файле задачи нет раздела «Сценарий
// проверки». Перевод в Check это не конец работы, а передача на проверку, и без
// сценария задача уедет туда без способа себя проверить: одиночный выкат везёт
// одну задачу и её проверяют по горячим следам, а поезд переводит в Check всю
// пачку разом и уже после деплоя. Бескодовая правка тут не исключение: выката
// нет, и подтвердить её по сценарию это единственный способ, а не повтор
// проверки прода. Признак раздела это заголовок «Сценарий проверки» вне
// ограждённых блоков (см. hasHeading), файл читается в дереве ветки: писать
// раздел туда же. Где сценарий неприменим (задача проверяется вместе с другой,
// разбор без выката), ворот гасится пометкой-исключением. Сам раздел помечает
// сценарий агентский или пользовательский, но ворот проверяет наличие раздела,
// а не его содержание: пустая заглушка пройдёт, содержательность держит ревью.
func scenarioGate(id string, docsBranch bool, doc string) error {
	if hasHeading(doc, "Сценарий проверки") {
		return nil
	}
	if hasException(doc, gateScenario) {
		return nil
	}
	who := "ship переведёт задачу в Check разом со всем поездом, проверять выкат будет нечем"
	if docsBranch {
		who = "бескодовую задачу merge переведёт в Check без выката, подтверждать её будет нечем"
	}
	return fmt.Errorf("в docs/tasks/%s.md нет раздела «Сценарий проверки», а %s: дописать раздел и повторить (RULES.board.md, «Трекинг задач» п. 6); если сценарий неприменим (бескодовая правка, проверяется вместе с другой задачей), загасить ворот пометкой «- Исключение: сценарий (причина)»",
		id, who)
}

// reviewLevelGate отказывает слиянию, если в разделе «Ревью» нет строки уровня
// («Уровень 2 до a1b2c3d: причина», её пишет taskctl review level). Уровень
// тщательности выбирает скилл review до чтения диффа, и эта строка
// единственный машинный след того, что ревью шло по скиллу: без неё пропуск
// ревью неотличим от ревью, прошедшего мимо. Нулевой уровень ворот проходит,
// это осознанный пропуск с причиной, а не молчание. Бескодовая ветка тут не
// исключение: у LLD и доки ревью тоже есть, и читается оно тем же скиллом.
// Судится первая непустая строка раздела вне ограждённых блоков: замечания и
// вердикт идут ниже неё. Где ревью неприменимо, ворот гасится
// пометкой-исключением, как и соседние.
func reviewLevelGate(id, doc string) error {
	if hasReviewLevel(doc) {
		return nil
	}
	if hasException(doc, gateReview) {
		return nil
	}
	return fmt.Errorf("нет строки уровня ревью в разделе «Ревью» docs/tasks/%s.md, ревью шло мимо скилла review или не шло вовсе: taskctl review level %s <0-3> \"причина\"; если ревью тут неприменимо, загасить ворот пометкой «- Исключение: ревью (причина)»",
		id, id)
}

// hasReviewLevel говорит, стоит ли строка уровня первой непустой строкой
// раздела «Ревью». Читателей у критерия двое: ворот слияния и ворот пуша, и
// разойдись они, пуш отбивал бы ветку, которую merge пропускает.
func hasReviewLevel(doc string) bool {
	for _, ln := range sectionLines(doc, taskform.Review) {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		return taskform.IsReviewLevel(ln)
	}
	return false
}

// testsGate отказывает слиянию ветки без тестовых файлов в диффе против main.
// Правка едет вместе с тестами (RULES.md, «Тесты обязательны»), а пропуск этого
// шага по статистике ловится только вниманием ревьювера. Бескодовая ветка (только
// docs/) ворот снимается: кода нет, тест не нужен. Тестовый файл узнаётся по
// соглашениям проекта (isTestFile), предикат без привязки к одному языку.
// Оговарка та же, что у regcheck: ворот ловит наличие файла, а не его
// содержание, пустая заглушка пройдёт, поэтому обещать полную проверку таким
// воротом нельзя.
func testsGate(root, main, branch string, docsBranch bool, doc string) error {
	if docsBranch {
		return nil
	}
	files, err := git(root, "diff", "--name-only", main+"..."+branch)
	if err != nil {
		// Ошибка git здесь не валит слияние по тому же правилу, что и в
		// trainOverlap: вороту хватит общего случая, а падать из-за неё дороже,
		// чем пропустить.
		return nil
	}
	// Пустой дифф значит ветку без правки кода (забытая ветка без коммитов):
	// мерджить нечего, и ворот тестов тут не к чему, как и у бескодовой ветки,
	// только с другой стороны.
	if strings.TrimSpace(files) == "" {
		return nil
	}
	for _, f := range strings.Split(files, "\n") {
		if isTestFile(strings.TrimSpace(f)) {
			return nil
		}
	}
	if hasException(doc, gateTests) {
		return nil
	}
	return fmt.Errorf("в диффе ветки против main нет тестовых файлов: правка едет вместе с тестами (RULES.md, «Тесты обязательны»); добавить тест или загасить ворот пометкой «- Исключение: тесты (причина)» в docs/tasks/<ID>.md, если правка бескодовая или тест к ней неприменим")
}

// isTestFile узнаёт тест по соглашениям об именах, общим для языков проекта:
// Go (суффикс _test.go), Python (test_*.py и *_test.py), shell (test_*.sh и
// *_test.sh). Предикат консервативный: лучше признать файл тестом, чем пропустить.
// Путь берётся с каталогом, как его отдаёт git diff.
func isTestFile(path string) bool {
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
	// tests/ рядом с src, у Gradle и Maven src/test. Без этого ворот врал бы
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

// trainOverlap находит пересечение файлов ветки с файлами коммитов задач
// поезда. Коммиты задачи берутся так же, как в составе поезда: из записи
// «Выкат» и по ID в subject, иначе подсказка молчала бы ровно на той ветке,
// где ID в сообщения не попал. Правки под docs/ (доска, файлы задач, LLD)
// пересечением не считаются: файл задачи и доску трогает почти каждая ветка.
func trainOverlap(root, main, branch string, train []string) ([]string, error) {
	if len(train) == 0 {
		return nil, nil
	}
	branchFiles, err := git(root, "diff", "--name-only", main+"..."+branch)
	if err != nil {
		return nil, err
	}
	mine := map[string]bool{}
	for _, f := range strings.Split(branchFiles, "\n") {
		if f = strings.TrimSpace(f); f != "" && !strings.HasPrefix(f, "docs/") {
			mine[f] = true
		}
	}
	if len(mine) == 0 {
		return nil, nil
	}
	log, err := git(root, "log", deployTag+".."+main, "--format=%H%x09%s")
	if err != nil || log == "" {
		return nil, err
	}
	var out []string
	for _, id := range train {
		rec, err := mergedShas(root, id)
		if err != nil {
			return nil, err
		}
		var hit []string
		for _, ln := range strings.Split(log, "\n") {
			sha, subj, ok := strings.Cut(ln, "\t")
			if !ok || isRevertSubject(subj) || (!ownsSubject(subj, id) && !inRecord(rec, sha)) {
				continue
			}
			files, err := git(root, "show", "--name-only", "--pretty=", sha)
			if err != nil {
				return nil, err
			}
			for _, f := range strings.Split(files, "\n") {
				if f = strings.TrimSpace(f); f != "" && mine[f] && !slices.Contains(hit, f) {
					hit = append(hit, f)
				}
			}
		}
		if len(hit) > 0 {
			out = append(out, id+": "+strings.Join(hit, ", "))
		}
	}
	return out, nil
}

// Ворота стенда (LLD DK-805, решения 1, 2 и 3). Текст, который читают агенты,
// правится теми же ветками, что и код, а меряется он не тестом, а прогоном
// стенда: правило, которое сессия перестала держать, по диффу не видно.
// Пятые ворота требуют на каждый тронутый раздел агентского файла сценарий
// стенда и свежий след прогона в файле задачи. Проверяется наличие следа, а не
// его честность: пару раскладок собирает автор, и подставить туда чужое дерево
// можно так же, как закоммитить пустой тест.
const (
	scenarioDir = "tools/obeycheck/scenarios"
	baseOld     = "старый"
	baseEmpty   = "пусто"
	// headSection это имя, под которым ворота держат шапку файла: строки до
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

// standGate отказывает слиянию ветки, которая правит текст для агентов без
// сценария стенда или без свежего следа прогона. Ворот гасится пометкой
// «- Исключение: стенд (причина)»: правка формулировки и вычитка поводом для
// прогона не считаются (порог повода в скилле prompt-test), а удаление текста
// доказывается прогоном ревизии с базой «пусто». Проект не devkit ворот не
// знает: сценарии живут в devkit, и спрашивать их с чужого дерева не с чего.
func standGate(root, main, branch, id, doc string) error {
	files, deleted, err := agentDiff(root, main, branch)
	if err != nil || (len(files) == 0 && !deleted) {
		return nil
	}
	if list, err := git(root, "ls-tree", "--name-only", branch, scenarioDir+"/"); err != nil || strings.TrimSpace(list) == "" {
		return nil
	}
	if hasException(doc, gateStand) {
		return nil
	}
	read := treeReader(root, branch)
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
		// Ошибка git тут не валит слияние по тому же правилу, что у ворот
		// теста: вороту хватит общего случая.
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

// treeReader читает файлы из дерева ветки через git: слияние идёт из основного
// чекаута, стоящего на main, и файлов ветки на диске там нет. Прочитанное
// кешируется: предмет одного файла спрашивают все сценарии подряд.
func treeReader(root, ref string) obey.Reader {
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
// «предмет» тем же кодом, что и стенд. Предмет, указывающий в никуда, ворота
// отбивают тем же текстом, что и загрузка сценариев: сценарий, осиротевший от
// переименования или удаления, зеленел бы на тексте, которого больше нет.
func branchScenarios(root, branch string, read obey.Reader) ([]standScenario, error) {
	list, err := git(root, "ls-tree", "-r", "--name-only", branch, scenarioDir+"/")
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
// кончается первым заголовком секции. Сценарий без ключа ворота пропускают, за
// форму шапки отвечает разбор самого стенда.
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
			where(sec), scenarioDir, obey.Key, subjectOf(sec), id)
	}
	named, based := false, false
	for _, s := range covering {
		print, err := obey.PrintFrom(read, s.subs)
		if err != nil {
			continue
		}
		for _, m := range marks {
			if !slices.Contains(m.Scenarios, s.id) {
				continue
			}
			named = true
			if m.Base != sec.base {
				continue
			}
			based = true
			if m.Print == print && !m.Failed {
				return nil
			}
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

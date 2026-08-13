package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const runLogPath = ".devkit/log"

// pipelineState это всё, что подсказке нужно знать о конвейере: где стоят
// задачи и кому доверена кнопка слияния.
type pipelineState struct {
	inProgress []string
	check      []string
	train      []string
	failed     []string
	autonomous bool
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
		return "следующий шаг: прогнать сценарий проверки " + strings.Join(st.check, ", ") +
			" и закрыть задачу (taskctl close <ID>); агентский сценарий агент прогоняет сам, пользовательский ждёт слова пользователя"
	case len(st.train) > 0:
		return "следующий шаг: выкатить поезд (shipctl ship), в нём " + strings.Join(st.train, ", ")
	case len(st.inProgress) > 0:
		return mergeFork(st.inProgress, st.autonomous)
	}
	return "следующий шаг: взять задачу с доски (taskctl list, дальше taskctl move <ID> in-progress)"
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

// nextAfterMerge говорит, чем задача сдаётся после слияния и выката. Ветку
// сценария (агентский или пользовательский) выбирает не утилита: пометка
// стоит прозой в файле задачи, поэтому названы обе.
func nextAfterMerge(ids string) string {
	return "следующий шаг: прогнать сценарий проверки " + ids +
		" и закрыть задачу (taskctl close <ID>); агентский сценарий агент прогоняет сам, пользовательский ждёт слова пользователя"
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
		// Ошибка git здесь не валяет слияние по тому же правилу, что и в
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
		strings.HasSuffix(base, "_test.sh") {
		return true
	}
	if strings.HasPrefix(base, "test_") && (strings.HasSuffix(base, ".py") || strings.HasSuffix(base, ".sh")) {
		return true
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
			if !ok || isRevertSubject(subj) || (!containsWord(subj, id) && !inRecord(rec, sha)) {
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

package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/dronrider/devkit/internal/accept"
	"github.com/dronrider/devkit/internal/rehearsal"
	"github.com/dronrider/devkit/internal/taskform"
	"github.com/dronrider/devkit/internal/trace"
)

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
		case strings.TrimSpace(strings.Join(taskform.SectionLines(readTaskDoc(root, r.ID), taskform.Verification), "\n")) == "":
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

// Проверяющие следов regcheck и ревью переехали в internal/trace вместе с
// разбором журнала запусков и строки уровня (LLD DK-972, решение 6).

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
// ограждённых блоков (см. trace.HasHeading), файл читается в дереве ветки: писать
// раздел туда же. Где сценарий неприменим (задача проверяется вместе с другой,
// разбор без выката), ворот гасится пометкой-исключением. Сам раздел помечает
// сценарий агентский или пользовательский, но ворот проверяет наличие раздела,
// а не его содержание: пустая заглушка пройдёт, содержательность держит ревью.
func scenarioGate(id string, docsBranch bool, doc string) error {
	if trace.HasHeading(doc, "Сценарий проверки") {
		return nil
	}
	if taskform.Exception(doc, taskform.GateScenario) {
		return nil
	}
	who := "ship переведёт задачу в Check разом со всем поездом, проверять выкат будет нечем"
	if docsBranch {
		who = "бескодовую задачу merge переведёт в Check без выката, подтверждать её будет нечем"
	}
	return fmt.Errorf("в docs/tasks/%s.md нет раздела «Сценарий проверки», а %s: дописать раздел и повторить (RULES.board.md, «Трекинг задач» п. 6); если сценарий неприменим (бескодовая правка, проверяется вместе с другой задачей), загасить ворот пометкой «- Исключение: сценарий (причина)»",
		id, who)
}

// rehearsalGate отказывает слиянию агентской задачи, чей сценарий не обкатан.
// Стоит он до слияния намеренно: перевод в Check идёт следом за merge и
// спрашивает ту же отметку, и без ворот здесь ветка уезжала в main, а строка
// оставалась в In progress, потому что move отбивал ворот обкатки уже после
// слияния (DK-685). Мерка та же, что у taskctl, из internal/rehearsal, включая
// пометку-исключение «- Исключение: обкатка (причина)»: своей копии ворот у
// shipctl нет. У mixed и user часть шагов держит человек, машинной отметки с
// них не спрашивают, а без файла задачи отметке негде стоять.
func rehearsalGate(root, id, title, doc string) error {
	if accept.KindOf(title) != accept.Agent || doc == "" {
		return nil
	}
	return rehearsal.Fresh(root, id, doc, "слияние")
}


// Проверяющие следов regcheck, тестов и ревью переехали в internal/trace
// (LLD DK-972, решение 6): ворота слияния и этап плана спрашивают одну и ту же
// нехватку.

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

// Проверяющие следов стенда и его машинерия переехали в internal/trace
// (LLD DK-972, решение 6): ворота слияния и этап плана спрашивают одну и ту же
// нехватку.

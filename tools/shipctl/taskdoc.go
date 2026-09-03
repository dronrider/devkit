package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// В файл задачи по RULES.board.md вкладывается реальный вывод команд, а вывод
// сплошь и рядом содержит куски чужих файлов задач: и заголовок «## Выкат», и
// строку записи, и замечание ревью. Читатели идут по строкам, поэтому такую
// цитату они приняли бы за собственную разметку файла: чужой sha попал бы в
// запись выката (и очередь посчитала бы выкаченной не ту задачу), а
// процитированное замечание без исхода отбило бы слияние.
//
// fenceMask помечает строки, лежащие внутри ограждённого блока, вместе с
// самими строками ограждения; вторым значением идёт номер строки незакрытого
// ограждения (ноль значит «файл цел»), и дальше по этому признаку читатели
// говорят вслух, что разделы после обрыва не прочитаны. Разбор общий с записью
// этапов в «Ход работы» (internal/stage): та же цитата ломала там поиск
// заголовка (DK-338), и держать два разбора одного и того же значит разойтись
// на первой правке.
func fenceMask(lines []string) ([]bool, int) { return stage.FenceMask(lines) }

// openFenceLine возвращает строку незакрытого ограждения, ноль у целого файла.
func openFenceLine(doc string) int {
	_, at := fenceMask(strings.Split(doc, "\n"))
	return at
}

// cutTaskFile проверяет файл задачи на оборванное ограждение. Разбор угадывать
// намерение автора не берётся (сочти незакрытый блок закрытым, и цитата с
// одним ограждением опять станет записью), но и молчать тут нельзя: за
// обрывом пропадают разом «Выкат», «Ревью» и «Сценарий проверки», а RULES.md
// («Фича доезжает до пользователя») требует, чтобы бездействие из-за кривых
// данных было видно снаружи. Файла нет значит и обрыва нет.
func cutTaskFile(root, id string) int {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return 0
	}
	return openFenceLine(string(data))
}

// cutTaskFileNote это та же находка словами, одинаково в status и в отказе
// merge: где оборвано и что из-за этого не прочитано.
func cutTaskFileNote(id string, at int) string {
	return fmt.Sprintf("в docs/tasks/%s.md не закрыт ограждённый блок (строка %d), всё после него читается как цитата: разделы «Выкат», «Ревью» и «Сценарий проверки» оттуда не видны",
		id, at)
}

// hasHeading говорит, есть ли в файле заголовок любого уровня с заданным
// текстом. Заголовок внутри ограждённого блока не считается: это чужой вывод,
// а не раздел нашего файла.
func hasHeading(doc, name string) bool {
	lines := strings.Split(doc, "\n")
	mask, _ := fenceMask(lines)
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, "#") && strings.Contains(ln, name) {
			return true
		}
	}
	return false
}

// sectionLines возвращает строки раздела с заданным заголовком, пропуская
// ограждённые блоки. Заголовок сравнивается по префиксу: у разделов в файлах
// задач встречается хвост вроде «## Ревью (второй круг)».
func sectionLines(doc, heading string) []string {
	lines := strings.Split(doc, "\n")
	mask, _ := fenceMask(lines)
	var out []string
	in := false
	for i, ln := range lines {
		if mask[i] {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			in = strings.HasPrefix(ln, heading)
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return out
}

// reviewItems собирает элементы списка раздела: маркерная строка вместе со
// строками продолжения (до пустой или следующей маркерной) составляет один
// элемент целиком. Абзац без маркера элементом не считается, поэтому чистый
// вердикт абзацем парсер не видит вовсе, а замечание, перенесённое на несколько
// строк, судится по всему тексту, и исход на строке переноса закрывает его.
func reviewItems(doc, heading string) []string {
	var items []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			items = append(items, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, ln := range sectionLines(doc, heading) {
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
// Копия того же критерия, что в taskctl outcome, иначе merge и review show
// разойдутся на одной строке.
var resolvedTailRe = regexp.MustCompile(`: (исправлено|отклонено)(,.*)?$`)

// cleanVerdictRe узнаёт чистый вердикт по голове элемента, куда его пишет
// ревьювер («Вердикт: без замечаний. пояснение» либо «замечаний нет» без
// пояснения), а не по факту оборота где-то в тексте: суть замечания,
// кончающаяся словами «про вердикт без замечаний нет ничего», голове не
// соответствует и замечание не закрывает (DK-469). За фразой идёт точка,
// двоеточие или конец текста, поэтому «замечаний неточностей» и продолжение
// фразы перечислением вердиктом не считаются. Маркер списка терпится потому,
// что reviewItems отдаёт элементы вместе с маркером. Копия того же критерия,
// что в taskctl outcome, иначе merge и review show разойдутся на одной строке.
var cleanVerdictRe = regexp.MustCompile(`^(?:[-*] )?(?:(?:вердикт|ревью):\s*)?(?:без замечаний|замечаний нет)(?:[.:]|$)`)

// reviewOutcome возвращает исход элемента раздела «Ревью»: «исправлено»,
// «отклонено», «чисто» (вердикт без замечаний) или пусто у открытого заме-
// чания. Порядок проверок исход -> чистый итог -> открыто тот же, что в taskctl
// outcome: «гибрид: исправлено, теперь без замечаний» остаётся закрытым.
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

// Имена ворот готовности и разбор пометки-исключения общие с воротами перевода
// в Check (internal/taskform): пометку пишет один человек в один файл.
const (
	gateRegcheck = taskform.GateRegcheck
	gateTests    = taskform.GateTests
	gateScenario = taskform.GateScenario
	gateReview   = taskform.GateReview
)

func hasException(doc, gate string) bool { return taskform.Exception(doc, gate) }

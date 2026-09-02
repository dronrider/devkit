// Package taskform держит машинную часть формы файла задачи из TASKFORM.md:
// имена и порядок разделов, а с ними вставку раздела на его место. Форму
// читают три писателя файла задачи (taskctl кладёт болванку и «Приёмку»,
// пакет этапов уходит в «Ход работы» через stage, shipctl ведёт «Выкат»), и
// у каждого своя копия порядка разошлась бы с остальными на первой правке.
// Текст формы и разбор решений в самом TASKFORM.md.
package taskform

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Doc это имя страницы формы: его называют отказы и находки, чтобы читатель
// шёл за разбором в одно место, а не собирал форму по подсказкам команд.
const Doc = "TASKFORM.md"

// Заголовки разделов файла задачи. Совпадение везде по префиксу строки:
// «## Проверка после выката» это тот же раздел, что «## Проверка».
const (
	Situation    = "## Что происходит"
	Want         = "## Чего хотим"
	Bounds       = "## Границы"
	Forks        = "## Развилки"
	DoD          = "## DoD"
	Rank         = "## Ранг"
	Acceptance   = "## Приёмка"
	Stages       = "## Ход работы"
	Review       = "## Ревью"
	Scenario     = "## Сценарий проверки"
	Merged       = "## Выкат"
	Verification = "## Проверка"
)

// Sections это порядок разделов файла задачи: сначала то, что пишет человек,
// дальше контрактные разделы в порядке жизни задачи. «Выкат» стоит перед
// «Проверкой» потому, что раздел выката пишет слияние, а вывод прогона
// вкладывается уже после выката.
var Sections = []string{
	Situation, Want, Bounds, Forks, DoD, Rank,
	Acceptance, Stages, Review, Scenario, Merged, Verification,
}

// Order говорит, каким по счёту идёт раздел формы в строке заголовка, и
// отвечает -1 на заголовке не из формы.
func Order(line string) int {
	for i, h := range Sections {
		if strings.HasPrefix(line, h) {
			return i
		}
	}
	return -1
}

// fenceRe это открывающее и закрывающее ограждение блока кода.
var fenceRe = regexp.MustCompile("^(`{3,}|~{3,})")

// FenceMask помечает строки, лежащие внутри ограждённого блока, вместе с самими
// строками ограждения. Блок закрывается ограждением того же знака не короче
// открывающего и без хвоста, поэтому ``` внутри блока на ~~~ его не закроет.
// Незакрытое ограждение уводит в блок весь остаток файла, как это делает и
// разметка при отрисовке, и вторым значением возвращается номер его строки
// (нумерация с единицы, ноль значит «файл цел»).
//
// Маска тут одна на весь devkit не для красоты: в файл задачи по RULES.board.md
// вкладывается реальный вывод команд, а вывод сплошь и рядом содержит куски
// чужих файлов задач, вместе с заголовками разделов и строками записей. Читатель
// по строкам принял бы такую цитату за собственную разметку файла, и вторая
// копия разбора разошлась бы с первой на первой же правке.
func FenceMask(lines []string) ([]bool, int) {
	mask := make([]bool, len(lines))
	open, at := "", 0
	for i, ln := range lines {
		t := strings.TrimLeft(ln, " \t")
		m := fenceRe.FindString(t)
		if open == "" {
			if m != "" {
				open, at, mask[i] = m, i+1, true
			}
			continue
		}
		mask[i] = true
		if m != "" && m[0] == open[0] && len(m) >= len(open) && strings.TrimSpace(t[len(m):]) == "" {
			open, at = "", 0
		}
	}
	return mask, at
}

// SectionAt отвечает, перед какой строкой встаёт раздел по форме: это первый
// заголовок вне ограждений, который по порядку идёт позже. Ответ -1 значит,
// что позже ничего нет (или заголовок не из формы) и место разделу в конце
// файла.
func SectionAt(lines []string, heading string) int {
	rank := Order(heading)
	if rank < 0 {
		return -1
	}
	mask, _ := FenceMask(lines)
	for i, ln := range lines {
		if mask[i] || !strings.HasPrefix(ln, "## ") {
			continue
		}
		if r := Order(ln); r > rank {
			return i
		}
	}
	return -1
}

// InsertSection вписывает готовый раздел (заголовок и тело) на его место по
// форме. Так «Приёмка» от add встаёт выше «Хода работы» из болванки, а «Ход
// работы» на файле, заведённом из черновика, выше «Сценария проверки», а не
// приклеивается за ним последней строкой.
func InsertSection(content, heading, body string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	block := []string{heading}
	if body = strings.TrimSpace(body); body != "" {
		block = append(block, "", body)
	}
	at := SectionAt(lines, heading)
	if at < 0 {
		out := append(lines, "")
		out = append(out, block...)
		return strings.Join(out, "\n") + "\n"
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, block...)
	out = append(out, "")
	out = append(out, lines[at:]...)
	return strings.Join(out, "\n") + "\n"
}

// InsertIntoSection дописывает строки в конец названного раздела: перед
// хвостовыми пустыми строками, чтобы записи не оторвались от остальных, и не
// задевая следующий раздел. Раздела нет, значит он заводится на своём месте по
// форме, а раздел не из формы (например, «Журнал» файла цели) в конце файла.
// Общая для пакета этапов в «Ход работы» файла задачи, снимка витка в «Журнал»
// файла цели и строк «Выката» от shipctl: разделы разные, а место записи
// ищется одинаково, и копии разошлись бы на первой правке.
//
// Заголовок и граница следующего раздела ищутся вне ограждённых блоков. Иначе
// процитированный в сценарии проверки вывод с «## Ход работы» перехватывал бы
// поиск, и пакет этапов ложился бы в раздел «Проверка» посреди чужого
// транскрипта: на самом docs/tasks/DK-338.md это уже случилось.
func InsertIntoSection(content, heading string, lines ...string) string {
	if len(lines) == 0 {
		return content
	}
	content = strings.TrimRight(content, "\n") + "\n"
	body := strings.Join(lines, "\n")
	rows := strings.Split(content, "\n")
	mask, _ := FenceMask(rows)
	head := -1
	for i, ln := range rows {
		if !mask[i] && strings.HasPrefix(ln, heading) {
			head = i
			break
		}
	}
	if head < 0 {
		return InsertSection(content, heading, body)
	}
	end := len(rows)
	for i := head + 1; i < len(rows); i++ {
		if !mask[i] && strings.HasPrefix(rows[i], "## ") {
			end = i
			break
		}
	}
	for end > head+1 && strings.TrimSpace(rows[end-1]) == "" {
		end--
	}
	ins := []string{body}
	if end == head+1 { // раздел был пуст, отбить запись от заголовка
		ins = []string{"", body}
	}
	out := make([]string, 0, len(rows)+len(ins))
	out = append(out, rows[:end]...)
	out = append(out, ins...)
	out = append(out, rows[end:]...)
	return strings.Join(out, "\n")
}

// SmokeNote это начало строки отметки прогона smoke в разделе «Выкат»:
// «smoke прогнан, <дата>». Пишет её `shipctl smoke`, читают двое, и префикса
// довольно, чтобы отличить отметку от строк записи слияния и прозы.
const SmokeNote = "smoke прогнан"

// SmokeCovers возвращает true, если отметка прогона smoke действует на
// последний выкат задачи (на входе текст её файла). Круг доработки после
// возврата из Check дописывает в раздел «Выкат» новую строку слияния, и
// отметка прошлого круга новый выкат не прикрывает: считается отметка,
// стоящая после последней строки с коммитами. Раздела нет значит выкат
// непроверенный, как и раздел без отметки.
//
// Разбор лежит тут, а не у shipctl, потому что читателей у отметки двое:
// shipctl считает по ней очередь выката, а taskctl отбирает по ней строки
// Check, которые вправе закрыть автоматика (LLD DK-400, решение 7). Вторая
// копия разбора разошлась бы с первой на первой же правке формы.
//
// Читается мимо ограждённых блоков, как и вся форма: в раздел «Проверка»
// вкладывается реальный вывод команд, и процитированная там отметка чужой
// задачи освобождала бы очередь без прогона.
func SmokeCovers(doc string) bool {
	lastMerge, smoke := -1, -1
	for i, ln := range sectionLines(doc, Merged) {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "- ") {
			continue
		}
		if strings.HasPrefix(strings.TrimPrefix(t, "- "), SmokeNote) {
			smoke = i
			continue
		}
		if _, list, ok := strings.Cut(t, ":"); ok && hasSha(list) {
			lastMerge = i
		}
	}
	return smoke > lastMerge
}

// SectionLines отдаёт строки названного раздела вне ограждённых блоков. Тем же
// разбором читают форму сама taskform и её вызывающие: раздел «Ход работы»
// спрашивают ворота закрытия и подъём прогона после выката, и вторая копия
// разбора разошлась бы с первой на первой же правке формы.
func SectionLines(doc, heading string) []string { return sectionLines(doc, heading) }

// sectionLines это строки названного раздела вне ограждённых блоков.
func sectionLines(doc, heading string) []string {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
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

// hasSha говорит, есть ли в перечне через запятую хотя бы один коммит: строкой
// записи слияния считается строка с коммитами, а не любая проза с двоеточием.
func hasSha(list string) bool {
	for _, part := range strings.Split(list, ",") {
		if IsSha(strings.TrimSpace(part)) {
			return true
		}
	}
	return false
}

// IsSha отсеивает прозу в строке записи: коммит это семь и больше знаков
// шестнадцатеричного числа.
func IsSha(s string) bool {
	if len(s) < 7 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Имена ворот готовности, как они пишутся в пометке-исключении. regcheck это
// имя инструмента, остаётся латиницей; остальные по-русски, как и сам текст
// файла задачи. Совпадение с именем нестрогим регистром (см. Exception).
const (
	GateRegcheck  = "regcheck"
	GateTests     = "тесты"
	GateScenario  = "сценарий"
	GateRehearsal = "обкатка"
)

// Exception говорит, гасит ли файл задачи ворот именем gate пометкой-
// исключением. Формат тот же, что у override в pick («Модель:»/«Эффорт:»):
// маркерная строка «- Исключение: <ворота>» с необязательным поясняющим хвостом
// в скобках, который отбрасывается. Ворота независимы, строк в файле может быть
// несколько, по каждой берётся своя. Строка внутри ограждённого блока не
// считается: в файл задачи вкладывается реальный вывод команд, и процитированная
// пометка не должна гасить ворот. Регистр нестрогий: «тесты» и «Тесты» одно и то
// же, писать надо имя ворот из списка выше, а не синоним.
//
// Разбор общий у ворот слияния (shipctl) и ворот перевода в Check (taskctl):
// пометку пишет один человек на один файл, и разойтись двум копиям на форме
// строки значило бы гасить ворота через раз.
func Exception(doc, gate string) bool {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	gate = strings.ToLower(gate)
	for i, ln := range lines {
		if mask[i] {
			continue
		}
		t := strings.TrimLeft(ln, " \t")
		rest, ok := strings.CutPrefix(strings.ToLower(t), "- исключение:")
		if !ok {
			continue
		}
		v := strings.TrimSpace(rest)
		if i := strings.Index(v, "("); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if v == gate {
			return true
		}
	}
	return false
}

// Начала строк, которыми `taskctl rehearse` отмечает в разделе «Проверка» свой
// прогон: зачтённый («- Обкатка: <дата>, свежее дерево <коммит>, ...») и
// красный, который ворота не открывает. Читают отметку ворота перевода в
// Check, и префикса довольно, чтобы отличить её от вложенного вывода и прозы.
const (
	RehearsalNote     = "- Обкатка:"
	RehearsalFailNote = "- Обкатка не зачтена:"
)

// Слова перед машинными полями строки отметки: по ним из неё достаются дерево,
// на котором шёл прогон, и отпечаток обкатанного сценария.
const (
	treeWord     = "свежее дерево "
	scenarioWord = "сценарий "
)

// ScenarioPrint это отпечаток текста раздела «Сценарий проверки»: восемь знаков
// sha256 от нормализованного тела раздела. Отпечаток едет в отметку обкатки, и
// ворота по нему видят, что после прогона сценарий переписали. Одних путей
// коммитов для этого мало: правка сценария лежит в том же файле задачи, что и
// запись прогона, и по именам файлов эти два коммита неразличимы. Нормализация
// снимает хвостовые пробелы и пустые строки: перевёрстка абзаца сценария не
// меняет ни одного шага и отметку отменять не должна. Ограждённые блоки в тело
// входят, в них и живут команды.
func ScenarioPrint(doc string) string {
	var kept []string
	for _, ln := range scenarioBody(doc) {
		if t := strings.TrimRight(ln, " \t"); t != "" {
			kept = append(kept, t)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(kept, "\n")))
	return hex.EncodeToString(sum[:])[:8]
}

// scenarioBody отдаёт строки раздела «Сценарий проверки» вместе с ограждёнными
// блоками. Заголовок внутри блока разделом не считается: там лежит чужой вывод.
func scenarioBody(doc string) []string {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	var out []string
	in := false
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, "## ") {
			in = strings.HasPrefix(ln, Scenario)
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return out
}

// RehearsalSha достаёт коммит из отметки обкатки. Одного факта отметки воротам
// мало: ветка уезжает вперёд после прогона, и вчерашняя обкатка открывала бы
// Check сегодняшнему коду. Отметок в разделе может лежать несколько (круг
// доработки после возврата из Check), берётся последняя. Читается мимо
// ограждённых блоков по той же причине, что и отметка smoke: в «Проверку»
// вкладывается реальный вывод команд, и процитированная там отметка чужой
// задачи открывала бы ворота без прогона.
func RehearsalSha(doc string) (string, bool) {
	sha, _, ok := RehearsalStamp(doc)
	return sha, ok
}

// RehearsalStamp достаёт из отметки обкатки коммит прогона и отпечаток
// сценария, на котором он шёл. Отметка старого образца, без отпечатка, за
// прогон не считается: сценарий под ней мог смениться, и доказать обратное
// нечем.
func RehearsalStamp(doc string) (sha, print string, ok bool) {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	found := false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if mask[i] || !strings.HasPrefix(t, RehearsalNote) {
			continue
		}
		tree, okTree := markField(t, treeWord)
		mark, okPrint := markField(t, scenarioWord)
		if !okTree || !okPrint {
			continue
		}
		sha, print, found = tree, mark, true
	}
	return sha, print, found
}

// markField достаёт из строки отметки значение поля, названного словом word:
// первое слово после него, очищенное от запятой и точки. Значение обязано быть
// шестнадцатеричным, иначе это проза, а не машинное поле.
func markField(line, word string) (string, bool) {
	rest, ok := cutAfter(line, word)
	if !ok {
		return "", false
	}
	v := strings.TrimRight(strings.Fields(rest)[0], ",.")
	if !IsSha(v) {
		return "", false
	}
	return v, true
}

// Rehearsed отвечает, стоит ли в файле задачи зачтённая отметка обкатки.
func Rehearsed(doc string) bool {
	_, ok := RehearsalSha(doc)
	return ok
}

// cutAfter отдаёт хвост строки после первого вхождения sep вместе с непустым
// остатком: пустой хвост это отметка без коммита, и разбирать в ней нечего.
func cutAfter(s, sep string) (string, bool) {
	_, rest, ok := strings.Cut(s, sep)
	if !ok || len(strings.Fields(rest)) == 0 {
		return "", false
	}
	return rest, true
}

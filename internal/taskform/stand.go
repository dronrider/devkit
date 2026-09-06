package taskform

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Начала строк, которыми стенд `obeycheck --task` отмечает в разделе
// «Проверка» свой прогон: зачтённый и красный, который ворота не открывает.
// Форма повторяет отметку обкатки, машинные поля стоят после фиксированных
// слов (LLD DK-805, решение 2).
const (
	StandNote     = "- Стенд:"
	StandFailNote = "- Стенд не зачтён:"
)

// GateStand это имя пятых ворот слияния в пометке-исключении: «- Исключение:
// стенд (правка формулировки, шаги не менялись)».
const GateStand = "стенд"

// Слова перед машинными полями отметки стенда.
const (
	standTreeWord  = "дерево "
	standPrintWord = "предмет "
	standBaseWord  = "база "
	standTierWord  = "ярус "
	standKWord     = "k "
	standScenWord  = "сценарии "
)

// standTailWords это слова, которыми кончается отметка после списка
// сценариев. По ним список отделяется от хвоста: имя сценария начинается с
// цифры, а хвост со слова.
var standTailWords = []string{"зачтён", "не зачтено", "просадка", "ворота"}

// StandMark это разобранная отметка стенда. Ворота слияния (DK-806) читают по
// ней, каким прогоном доказана правка: тем ли сценарием, с той ли базой и на
// том ли тексте, что лежит в дереве ветки.
type StandMark struct {
	Failed    bool     // отметка «- Стенд не зачтён:»
	Tree      string   // HEAD дерева прогона, для глаз
	Print     string   // отпечаток текста предметов прогона
	Base      string   // база прогона: старый или пусто
	Tier      string   // ярус модели
	Repeats   int      // повторов на раскладку
	Scenarios []string // сценарии прогона, в порядке таблицы
}

// SameKey отвечает, тот ли у отметки ключ записи: список сценариев вместе с
// базой. Повторный прогон с тем же ключом заменяет свою прошлую запись, а
// запись с другим ключом дописывается, и «Проверка» не растёт дублями.
func (m StandMark) SameKey(scenarios []string, base string) bool {
	if m.Base != base || len(m.Scenarios) != len(scenarios) {
		return false
	}
	for i := range scenarios {
		if m.Scenarios[i] != scenarios[i] {
			return false
		}
	}
	return true
}

// StandLine складывает строку отметки стенда. Хвост это исход прогона:
// «зачтён» у зелёной отметки, причина и «ворота закрыты» у красной.
func StandLine(m StandMark, when time.Time, tail string) string {
	head := StandNote
	if m.Failed {
		head = StandFailNote
	}
	return fmt.Sprintf("%s %s, %s%s, %s%s, %s%s, %s%s, %s%d, %s%s, %s.",
		head, when.Format("2006-01-02 15:04"),
		standTreeWord, m.Tree, standPrintWord, m.Print, standBaseWord, m.Base,
		standTierWord, m.Tier, standKWord, m.Repeats,
		standScenWord, strings.Join(m.Scenarios, ", "), tail)
}

// StandMarks достаёт из файла задачи все отметки стенда по порядку. Читается
// мимо ограждённых блоков: в «Проверку» вкладывается вывод команд, и
// процитированная там отметка чужой задачи открывала бы ворота без прогона.
// Отметка без машинных полей за прогон не считается, это проза.
func StandMarks(doc string) []StandMark {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	var out []StandMark
	for i, ln := range lines {
		if mask[i] {
			continue
		}
		t := strings.TrimSpace(ln)
		m := StandMark{}
		switch {
		case strings.HasPrefix(t, StandNote):
		case strings.HasPrefix(t, StandFailNote):
			m.Failed = true
		default:
			continue
		}
		tree, okTree := markField(t, standTreeWord)
		print, okPrint := markField(t, standPrintWord)
		base, okBase := standWord(t, standBaseWord)
		tier, okTier := standWord(t, standTierWord)
		k, okK := standWord(t, standKWord)
		scen, okScen := standScenarios(t)
		if !okTree || !okPrint || !okBase || !okTier || !okK || !okScen {
			continue
		}
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		m.Tree, m.Print, m.Base, m.Tier, m.Repeats, m.Scenarios = tree, print, base, tier, n, scen
		out = append(out, m)
	}
	return out
}

// standWord достаёт значение поля одним словом, очищенным от запятой и точки.
func standWord(line, word string) (string, bool) {
	rest, ok := cutAfter(line, ", "+word)
	if !ok {
		return "", false
	}
	v := strings.TrimRight(strings.Fields(rest)[0], ",.")
	if v == "" {
		return "", false
	}
	return v, true
}

// standScenarios достаёт список сценариев: всё после слова «сценарии» до
// хвоста отметки.
func standScenarios(line string) ([]string, bool) {
	rest, ok := cutAfter(line, ", "+standScenWord)
	if !ok {
		return nil, false
	}
	for _, w := range standTailWords {
		if i := strings.Index(rest, ", "+w); i >= 0 {
			rest = rest[:i]
		}
	}
	var out []string
	for _, p := range strings.Split(strings.TrimRight(rest, " .,"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// WarnLine это начало строки предупреждения, которое стенд кладёт под отметкой:
// текст предмета не нашёлся в раскладке-кандидате. Строка идёт вне ограждения,
// и уборка прошлой записи обязана знать её в лицо.
const WarnLine = "Предупреждение: "

// DropStandRecord уносит из файла задачи прошлую запись стенда с тем же ключом:
// строку отметки (зачтённую или красную) и всё, что писал тот же прогон, то
// есть предупреждения и ограждённый блок с командой и таблицей. Первая
// посторонняя строка запись кончает, так что вложенный руками вывод и проза
// раздела остаются на месте.
func DropStandRecord(doc string, scenarios []string, base string) string {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	var out []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		isMark := !mask[i] && (strings.HasPrefix(t, StandNote) || strings.HasPrefix(t, StandFailNote))
		if !isMark {
			out = append(out, lines[i])
			continue
		}
		marks := StandMarks(lines[i])
		if len(marks) == 0 || !marks[0].SameKey(scenarios, base) {
			out = append(out, lines[i])
			continue
		}
		for i++; i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" || mask[i] || strings.HasPrefix(t, WarnLine) {
				continue
			}
			break
		}
		i--
		// хвостовая пустая строка перед следующей записью не нужна: её
		// поставит вставка новой записи.
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
	}
	return strings.Join(out, "\n")
}

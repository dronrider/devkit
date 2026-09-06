package taskform

import (
	"strings"
	"testing"
	"time"
)

func standDoc(body string) string {
	return "# DK-836\n\n## Проверка\n\nПроза раздела.\n" + body
}

func standMark() StandMark {
	return StandMark{
		Tree:      "a1b2c3d",
		Print:     "9f8e7d6c",
		Base:      "старый",
		Tier:      "mini",
		Repeats:   5,
		Scenarios: []string{"14-prose-sample", "15-mimicry-readme"},
	}
}

func when() time.Time { return time.Date(2026, 9, 5, 14, 10, 0, 0, time.UTC) }

// Форма отметки: фиксированные слова, за ними машинные поля, и разбор достаёт
// их обратно. Ворота слияния читают отметку тем же кодом, что её пишет стенд.
func TestStandLineRoundTrip(t *testing.T) {
	line := StandLine(standMark(), when(), "зачтён")
	want := "- Стенд: 2026-09-05 14:10, дерево a1b2c3d, предмет 9f8e7d6c, база старый, ярус mini, k 5, " +
		"сценарии 14-prose-sample, 15-mimicry-readme, зачтён."
	if line != want {
		t.Fatalf("строка отметки:\n%s\nждал:\n%s", line, want)
	}
	marks := StandMarks(standDoc("\n" + line + "\n"))
	if len(marks) != 1 {
		t.Fatalf("отметок разобрано %d", len(marks))
	}
	m := marks[0]
	if m.Failed || m.Tree != "a1b2c3d" || m.Print != "9f8e7d6c" || m.Base != "старый" ||
		m.Tier != "mini" || m.Repeats != 5 {
		t.Fatalf("поля разобраны как %+v", m)
	}
	if strings.Join(m.Scenarios, ",") != "14-prose-sample,15-mimicry-readme" {
		t.Fatalf("сценарии разобраны как %v", m.Scenarios)
	}
}

// Красная отметка ворот не открывает, но читается тем же разбором: разбирать
// провал ревьюверу всё равно нужно.
func TestStandFailLine(t *testing.T) {
	m := standMark()
	m.Failed = true
	line := StandLine(m, when(), "просадка 15-mimicry-readme, ворота закрыты")
	if !strings.HasPrefix(line, StandFailNote) {
		t.Fatalf("красная отметка начата не тем: %s", line)
	}
	marks := StandMarks(standDoc("\n" + line + "\n"))
	if len(marks) != 1 || !marks[0].Failed {
		t.Fatalf("красная отметка разобрана как %+v", marks)
	}
	if strings.Join(marks[0].Scenarios, ",") != "14-prose-sample,15-mimicry-readme" {
		t.Fatalf("хвост уехал в список сценариев: %v", marks[0].Scenarios)
	}
}

// Процитированная в ограждённом блоке отметка чужой задачи за прогон не
// считается: в «Проверку» вкладывается реальный вывод команд.
func TestStandMarkInsideFenceIgnored(t *testing.T) {
	doc := standDoc("\n```console\n$ obeycheck --task DK-836\n" + StandLine(standMark(), when(), "зачтён") + "\n```\n")
	if marks := StandMarks(doc); len(marks) != 0 {
		t.Fatalf("отметка из блока зачтена: %+v", marks)
	}
}

// Отметка без машинных полей это проза, а не след прогона.
func TestStandMarkWithoutFields(t *testing.T) {
	doc := standDoc("\n- Стенд: гонял, всё зелено.\n")
	if marks := StandMarks(doc); len(marks) != 0 {
		t.Fatalf("проза принята за отметку: %+v", marks)
	}
}

// Ключ записи это список сценариев вместе с базой.
func TestStandSameKey(t *testing.T) {
	m := standMark()
	if !m.SameKey([]string{"14-prose-sample", "15-mimicry-readme"}, "старый") {
		t.Fatal("свой же ключ не сошёлся")
	}
	if m.SameKey([]string{"14-prose-sample", "15-mimicry-readme"}, "пусто") {
		t.Fatal("другая база сошлась ключом")
	}
	if m.SameKey([]string{"14-prose-sample"}, "старый") {
		t.Fatal("другой список сценариев сошёлся ключом")
	}
}

// Повторный прогон заменяет свою прошлую запись целиком, вместе с вложенной
// таблицей, а запись с другим ключом остаётся на месте. Иначе «Проверка» растёт
// дублями, и ревьювер читает вперемешку прогоны разных редакций.
func TestStandDropRecord(t *testing.T) {
	old := StandLine(standMark(), when(), "зачтён")
	other := standMark()
	other.Base = "пусто"
	otherLine := StandLine(other, when(), "зачтён")
	doc := standDoc("\n" + old + "\n\n```console\n$ obeycheck --task DK-836\nтаблица\n```\n\n" +
		otherLine + "\n\n```console\n$ obeycheck --base пусто\nтаблица\n```\n\nПроза после записей.\n")
	got := DropStandRecord(doc, []string{"14-prose-sample", "15-mimicry-readme"}, "старый")
	if strings.Contains(got, old) {
		t.Fatalf("прошлая запись с тем же ключом осталась:\n%s", got)
	}
	if strings.Contains(got, "$ obeycheck --task DK-836") {
		t.Fatalf("таблица прошлой записи осталась:\n%s", got)
	}
	if !strings.Contains(got, otherLine) || !strings.Contains(got, "$ obeycheck --base пусто") {
		t.Fatalf("запись с другим ключом снесена:\n%s", got)
	}
	if !strings.Contains(got, "Проза раздела.") || !strings.Contains(got, "Проза после записей.") {
		t.Fatalf("проза раздела снесена:\n%s", got)
	}
}

// Пометка «- Исключение: стенд» гасит пятые ворота тем же разбором, что и
// четверо соседних.
func TestStandGateException(t *testing.T) {
	doc := "## Границы\n\n- Исключение: стенд (правка формулировки, шаги не менялись)\n"
	if !Exception(doc, GateStand) {
		t.Fatal("пометка ворота стенда не гасит")
	}
	if Exception(doc, GateRehearsal) {
		t.Fatal("пометка гасит чужие ворота")
	}
}

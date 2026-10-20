# Общие материалы обычной фичи

Источник: `internal/taskform/stand.go:74-88`, ревизия `07cd996d51f7baefc788d196a22d5d2558ca1b81`.

```text
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

```

Источник: `internal/taskform/stand.go:204-255`, ревизия `07cd996d51f7baefc788d196a22d5d2558ca1b81`.

```text
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
```

Источник: `internal/taskform/stand_test.go:100-143`, ревизия `07cd996d51f7baefc788d196a22d5d2558ca1b81`.

```text
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

// Запись с предупреждением уносится целиком. Предупреждение стоит между
// отметкой и таблицей вне ограждения, и уборка, которая о нём не знает,
// оставляет в «Проверке» и старый текст, и старую таблицу.
func TestStandDropRecordWithWarning(t *testing.T) {
	old := StandLine(standMark(), when(), "зачтён")
	doc := standDoc("\n" + old + "\n\n" + WarnLine +
		"текст предмета RULES.core.md в раскладке-кандидате не найден, раскладка собрана до правки?\n" +
		"\n```console\n$ obeycheck --task DK-836\nтаблица\n```\n\nПроза после записей.\n")
	got := DropStandRecord(doc, []string{"14-prose-sample", "15-mimicry-readme"}, "старый")
	if strings.Contains(got, WarnLine) {
		t.Fatalf("предупреждение прошлой записи осталось:\n%s", got)
	}
	if strings.Contains(got, "таблица") || strings.Contains(got, old) {
		t.Fatalf("таблица прошлой записи осталась:\n%s", got)
	}
	if !strings.Contains(got, "Проза после записей.") {
		t.Fatalf("проза раздела снесена:\n%s", got)
	}
}

// Пометка «- Исключение: стенд» гасит пятые ворота тем же разбором, что и
```

Источник: `docs/tasks/archive/2026/DK-662.md:764-764`, ревизия `c007fccc2`.

```text
- Стенд: 2026-10-13 05:37, дерево c43a61ebc, предмет e3b0c442, база старый, ярус mini, k 3, сценарии , зачтён.
```

Источник: `docs/tasks/archive/2026/DK-662.md:768-771`, ревизия `c007fccc2`.

```text
сценарий                                         rules-sk-new  rules-sk-old  вердикт
разбор этапа на диск, диспетчеру строка статуса             -             -  пропущен, конец субагент

итог: сценариев 1, все зачтены, база старый
```

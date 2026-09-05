package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReturnClassRejectsUnknown: значение вне закрытого списка отбивается, и
// отказ называет допустимые слова. Без перечня в отказе следующая попытка
// ставится тем же наугад, а запись возврата не появляется вовсе.
func TestReturnClassRejectsUnknown(t *testing.T) {
	err := checkReturnClass("лень")
	if err == nil {
		t.Fatal("класс вне списка прошёл")
	}
	for _, want := range []string{"постановка", "правила", "реализация"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет допустимого значения %q: %v", want, err)
		}
	}
	if err := checkReturnClass("постановка"); err != nil {
		t.Fatalf("класс из списка отбит: %v", err)
	}
	if err := checkReturnClass(""); err != nil {
		t.Fatalf("пустой класс это не ошибка, ключ необязателен: %v", err)
	}
}

// TestFailWritesReturnClass: провал с классом кладёт машинную запись в «Ход
// работы» файла задачи. Признак в строке доски снимается починкой, поэтому
// мерять по нему нечего, а запись остаётся и после того, как прод поднят.
func TestFailWritesReturnClass(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	msg, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "прод падает на старте", Class: retSpec})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "класс возврата: постановка") {
		t.Errorf("класс не назван в ответе: %q", msg)
	}
	doc := readTaskFile(t, root, "XR-005")
	want := "- Возврат: постановка, проверка, " + time.Now().Format(retDateLayout) + ", прод падает на старте."
	if !strings.Contains(doc, want) {
		t.Fatalf("записи возврата нет в файле задачи:\n%s\nждал %q", doc, want)
	}
	if i, j := strings.Index(doc, "## Ход работы"), strings.Index(doc, want); i < 0 || j < i {
		t.Fatalf("запись легла мимо «Хода работы»:\n%s", doc)
	}
	// Признак провала снят, а запись на месте: сводке считать по чему-то надо
	// и после починки прода.
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Clear: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTaskFile(t, root, "XR-005"), want) {
		t.Fatal("запись возврата пропала вместе с признаком провала")
	}
}

// TestFailWithoutClassIsVisible: провал без ключа тоже оставляет запись, но с
// меткой пропуска, и команда говорит, чего не хватило. Молчаливый пропуск
// неотличим от возврата, которого не было.
func TestFailWithoutClassIsVisible(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	msg, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "прод падает на старте"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "класс возврата не назван") {
		t.Errorf("пропуск класса не назван в ответе: %q", msg)
	}
	if !strings.Contains(readTaskFile(t, root, "XR-005"), "- Возврат: неназван, проверка, ") {
		t.Fatalf("возврат без класса не записан:\n%s", readTaskFile(t, root, "XR-005"))
	}
}

// TestFailRejectsUnknownClass: класс вне списка это отказ до правки доски.
// Иначе строка уехала бы в In progress с признаком провала, а класс не
// записался, и чинить пришлось бы руками.
func TestFailRejectsUnknownClass(t *testing.T) {
	root := setup(t)
	toCheck(t, root, "XR-005")
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "прод лёг", Class: "лень"}); err == nil {
		t.Fatal("класс вне списка прошёл")
	}
	if sect := sectOf(t, root, "XR-005"); sect != SectCheck {
		t.Fatalf("после отказа задача в %s, ждал Check: доска правиться не должна", sect)
	}
}

// TestReviewAddMarksClass: класс замечания встаёт маркером в голову строки,
// суть остаётся читаемой, а исход дописывается в хвост той же строки и
// закрывает замечание. Маркер в голове выбран как раз ради этого: хвост занят
// исходом, и пометка там разошлась бы с разбором закрытости.
func TestReviewAddMarksClass(t *testing.T) {
	root := setup(t)
	if _, err := cmdReviewAdd(root, "XR-005", "тесты не едут с правкой", retRule, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	doc := readTaskFile(t, root, "XR-005")
	want := "- [возврат: правила, " + time.Now().Format(retDateLayout) + "] тесты не едут с правкой"
	if !strings.Contains(doc, want) {
		t.Fatalf("маркер класса не встал в голову замечания:\n%s\nждал %q", doc, want)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTaskFile(t, root, "XR-005"), want+": исправлено") {
		t.Fatalf("исход не дописался к помеченному замечанию:\n%s", readTaskFile(t, root, "XR-005"))
	}
	if _, err := cmdReviewAdd(root, "XR-005", "ещё одно", "лень", CommitOpts{}); err == nil {
		t.Fatal("класс вне списка прошёл в замечание")
	}
}

// TestReviewStatsReturns: сводка считает возвраты обоих происхождений, делит
// их по классам и печатает долю по постановке. Возврат без класса стоит
// отдельной строкой и в долю не идёт: он не говорит ни за постановку, ни
// против неё, а растворённый в знаменателе занижал бы цифру.
func TestReviewStatsReturns(t *testing.T) {
	root := setup(t)
	if _, err := cmdReviewAdd(root, "XR-005", "предмет понят иначе", retSpec, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewAdd(root, "XR-005", "дока не едет", retRule, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewAdd(root, "XR-002", "замечание без класса", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	toCheck(t, root, "XR-001")
	if _, err := cmdFail(root, FailParams{ID: "XR-001", Reason: "делали не то", Class: retSpec}); err != nil {
		t.Fatal(err)
	}
	toCheck(t, root, "XR-003")
	if _, err := cmdFail(root, FailParams{ID: "XR-003", Reason: "прод лёг"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewStats(root, StatsCut{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"возвраты с классом: 3 (постановка 2, правила 1, реализация 0)",
		"возвраты по постановке: 2 из 3, 66%",
		"возвратов без класса: 1",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в сводке нет %q:\n%s", want, msg)
		}
	}
	// Возврат без класса тут один, провальный: непомеченное замечание событием
	// не считается вовсе. Класс ставит тот, кто возвращает, и приписывать
	// цифру за него сводка не берётся, а строка «возвратов без класса» осталась
	// бы двойкой, попади туда замечание.
	if strings.Contains(msg, "возвратов без класса: 2") {
		t.Errorf("непомеченное замечание попало в возвраты:\n%s", msg)
	}
}

// TestReviewStatsCuts: окно режет события по дате записи, а цель по составу.
// Без среза доля считается по всей доске, а меряет себя ею цель, и цифра «за
// всё время по всем задачам» вектору цели не отвечает.
func TestReviewStatsCuts(t *testing.T) {
	root := setup(t)
	tasks := filepath.Join(root, "docs", "tasks")
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(tasks, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// XR-005 под целью XR-900, возврат старый; XR-002 под той же целью, возврат
	// свежий; XR-001 вне цели, возврат свежий.
	write("XR-005.md", "# XR-005\n\nЦель: [tasks/XR-900.md](tasks/XR-900.md)\n\n"+
		"## Ход работы\n\n- Возврат: постановка, проверка, 2026-01-10, старое.\n")
	write("XR-002.md", "# XR-002\n\nЦель: [tasks/XR-900.md](tasks/XR-900.md)\n\n"+
		"## Ход работы\n\n- Возврат: правила, ревью, 2026-08-20, свежее.\n")
	write("XR-001.md", "# XR-001\n\n## Ход работы\n\n- Возврат: постановка, ревью, 2026-08-21, чужое.\n")
	all, err := cmdReviewStats(root, StatsCut{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "возвраты по постановке: 2 из 3") {
		t.Fatalf("без среза считаются все три возврата:\n%s", all)
	}
	win, err := cmdReviewStats(root, StatsCut{Since: "2026-08-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(win, "срез: окно с 2026-08-01") || !strings.Contains(win, "возвраты по постановке: 1 из 2") {
		t.Fatalf("окно не отрезало старый возврат:\n%s", win)
	}
	goal, err := cmdReviewStats(root, StatsCut{Goal: "XR-900"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goal, "срез: цель XR-900") || !strings.Contains(goal, "возвраты по постановке: 1 из 2") {
		t.Fatalf("срез по цели взял чужую задачу:\n%s", goal)
	}
	both, err := cmdReviewStats(root, StatsCut{Since: "2026-08-01", Goal: "XR-900"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(both, "возвраты по постановке: 0 из 1") {
		t.Fatalf("окно и цель вместе:\n%s", both)
	}
	if _, err := cmdReviewStats(root, StatsCut{Since: "вчера"}); err == nil {
		t.Fatal("окно не датой прошло")
	}
}

// TestReviewStatsGoalByList: задача попадает в срез цели и по составу в
// разделе «Задачи цели», а не только по строке связи в своём файле: нарезка
// цели приписывает задачи в файл цели, и заведённая раньше цели строка ссылки
// на неё не несёт.
func TestReviewStatsGoalByList(t *testing.T) {
	root := setup(t)
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.WriteFile(filepath.Join(tasks, "XR-900.md"),
		[]byte("# XR-900: Цель\n\n## Задачи цели\n\n- XR-005 первая\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasks, "XR-005.md"),
		[]byte("# XR-005\n\n## Ход работы\n\n- Возврат: постановка, ревью, 2026-08-20, своё.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasks, "XR-001.md"),
		[]byte("# XR-001\n\n## Ход работы\n\n- Возврат: правила, ревью, 2026-08-20, чужое.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewStats(root, StatsCut{Goal: "XR-900"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "возвраты по постановке: 1 из 1") {
		t.Fatalf("состав цели прочитан мимо раздела «Задачи цели»:\n%s", msg)
	}
}

// TestReturnsInReadsBothHomes: разбор берёт события из обеих записей и не
// принимает за возврат ни прозу про возврат, ни строку этапа без даты.
func TestReturnsInReadsBothHomes(t *testing.T) {
	lines := strings.Split(strings.Join([]string{
		"Возврат тут поминается прозой, датой 2026-08-20 и словом постановка.",
		"- Возврат: постановка, проверка, 2026-08-20, прод лёг.",
		"- Возврат: правила, ревью, без даты.",
		"- [возврат: реализация, 2026-08-21] баг в разборе",
		"- обычное замечание: исправлено",
	}, "\n"), "\n")
	got := returnsIn("XR-005", lines)
	if len(got) != 2 {
		t.Fatalf("событий %d, ждал 2: %+v", len(got), got)
	}
	if got[0].Class != retSpec || got[0].Source != retFromCheck || got[0].Date != "2026-08-20" {
		t.Errorf("событие провала разобрано неверно: %+v", got[0])
	}
	if got[1].Class != retImpl || got[1].Source != retFromReview || got[1].Date != "2026-08-21" {
		t.Errorf("событие замечания разобрано неверно: %+v", got[1])
	}
}

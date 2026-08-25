package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestAcceptOfSuffix разбирает вид из заголовка: без суффикса это агентский
// (умолчание), а три значения вида читаются с суффикса ровно. На чужой текст в
// квадратных скобках вид не меняется (LLD DK-292, решение 3).
func TestAcceptOfSuffix(t *testing.T) {
	cases := []struct {
		title string
		kind  string
	}{
		{"Простой заголовок", acceptAgent},
		{"С зависимостью [после XR-001]", acceptAgent},
		{"С видом [приёмка: agent]", acceptAgent},
		{"Пользовательская [приёмка: user]", acceptUser},
		{"Смешанная [приёмка: mixed]", acceptMixed},
		{"С другими скобками [блок: ждём]", acceptAgent},
		{"Вид и зависимость [после XR-001] [приёмка: user]", acceptUser},
	}
	for _, c := range cases {
		if got := acceptOf(c.title); got != c.kind {
			t.Errorf("acceptOf(%q) = %q, ожидал %q", c.title, got, c.kind)
		}
	}
}

// TestAcceptOfWithTrailingSuffixes: фиксированный порядок ставит [приёмка:]
// раньше [провал:] и [блок:] (dep.go:13-16), поэтому end-anchored регулярка на
// сыром заголовке прокусывает user/mixed-задачу с суффиксом провала или
// блокировки и читает её как агентскую. acceptOf разбирает вид через
// splitTitle, снимая хвосты fail/block до сопоставления (LLD DK-292, решение 3).
func TestAcceptOfWithTrailingSuffixes(t *testing.T) {
	cases := []struct {
		title string
		kind  string
	}{
		{"С провалом [приёмка: user] [провал: 500]", acceptUser},
		{"С блоком [приёмка: user] [блок: ждём]", acceptUser},
		{"Mixed с провалом [приёмка: mixed] [провал: 500]", acceptMixed},
		{"Mixed с блоком [приёмка: mixed] [блок: ждём]", acceptMixed},
		{"Все хвосты [после XR-001] [приёмка: user] [провал: 500] [блок: ждём]", acceptUser},
		// Агентский суффикса не несёт, но провал и блок бывают.
		{"Агентский с провалом [провал: 500]", acceptAgent},
		{"Агентский с блоком [блок: ждём]", acceptAgent},
	}
	for _, c := range cases {
		if got := acceptOf(c.title); got != c.kind {
			t.Errorf("acceptOf(%q) = %q, ожидал %q", c.title, got, c.kind)
		}
	}
}

// TestAcceptSuffix собирается только для неагентских видов: агентский суффикса
// не несёт, и строка остаётся как есть (LLD DK-292, решение 2).
func TestAcceptSuffix(t *testing.T) {
	cases := map[string]string{
		acceptAgent: "",
		acceptUser:  " [приёмка: user]",
		acceptMixed: " [приёмка: mixed]",
		"":          "",
	}
	for kind, want := range cases {
		if got := acceptSuffix(kind); got != want {
			t.Errorf("acceptSuffix(%q) = %q, ожидал %q", kind, got, want)
		}
	}
}

// TestAddAcceptRequired: add без --accept отказывает (DK-301 включил
// обязательность: все три входа заведения слают флаг, LLD DK-292, «Входы
// заведения строки»). Агентский вид заводится без суффикса и без --barrier,
// --barrier у агентского и --barrier без --accept лишние.
func TestAddAcceptRequired(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{Title: "Без вида", Type: "task", Rank: "0+1+1+0+1", Link: "x"}); err == nil {
		t.Fatal("add без --accept должен отбиваться")
	}
	if _, err := cmdAdd(root, AddParams{Title: "Барьер без вида", Type: "task", Rank: "0+1+1+0+1", Barrier: "глаза"}); err == nil {
		t.Fatal("--barrier без --accept должен отбиваться")
	}
	// Агентский без барьера проходит, суффикса в заголовке нет.
	if _, err := cmdAdd(root, AddParams{Title: "Агентская", Type: "task", Rank: "0+1+1+0+1", Link: "x", Accept: "agent"}); err != nil {
		t.Fatalf("agent add: %v", err)
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-008").Title; got != "Агентская" {
		t.Fatalf("агентский вид не вешает суффикс: %q", got)
	}
	// --barrier у агентского лишний.
	if _, err := cmdAdd(root, AddParams{Title: "С барьером", Type: "task", Rank: "0+1+1+0+1", Accept: "agent", Barrier: "глаза"}); err == nil {
		t.Fatal("agent с --barrier должен отбиваться")
	}
}

// TestAddNonAgentRequiresBarrier: user и mixed требуют --barrier из закрытого
// списка, иначе вид повисает без причины. Команда заводит файл задачи с разделом
// «Приёмка» (скелетом), который исполнитель дописывает (LLD DK-292, решение 3).
func TestAddNonAgentRequiresBarrier(t *testing.T) {
	root := setup(t)
	// Без барьера user и mixed отбиваются.
	if _, err := cmdAdd(root, AddParams{Title: "Юзер", Type: "task", Rank: "0+1+1+0+1", Accept: "user"}); err == nil {
		t.Fatal("user без --barrier должен отбиваться")
	}
	if _, err := cmdAdd(root, AddParams{Title: "Микс", Type: "task", Rank: "0+1+1+0+1", Accept: "mixed"}); err == nil {
		t.Fatal("mixed без --barrier должен отбиваться")
	}
	// Барьер не из шести.
	if _, err := cmdAdd(root, AddParams{Title: "Чужой", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "чего"}); err == nil {
		t.Fatal("чужой барьер должен отбиваться")
	}
	// user с барьером «глаза» проходит: суффикс в заголовке, файл задачи создан.
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "С видом", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза"}); err != nil {
		t.Fatalf("user с барьером глаза: %v", err)
	}
	b, _ := LoadBoard(boardPath(root))
	row := b.find("XR-100")
	if row == nil {
		t.Fatal("XR-100 нет на доске")
	}
	if got := row.Title; got != "С видом [приёмка: user]" {
		t.Fatalf("заголовок user-задачи: %q", got)
	}
	if row.Link != "[tasks/XR-100.md](tasks/XR-100.md)" {
		t.Fatalf("ссылка user-задачи: %q", row.Link)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-100.md"))
	if err != nil {
		t.Fatalf("файл задачи не создан: %v", err)
	}
	body := string(data)
	// DoD болванки и «Приёмка» неагентского вида соседствуют в одном файле:
	// разделы дополняют друг друга, а не перезаписывают (LLD DK-133).
	for _, want := range []string{"## DoD", "## Приёмка", "- вид: user", "- барьер «глаза»:"} {
		if !strings.Contains(body, want) {
			t.Errorf("в скелете нет %q:\n%s", want, body)
		}
	}
}

// TestAddNonAgentAppendsAcceptance: раздел «Приёмка» не агентского вида
// дописывается к уже существующему файлу задачи, а не переписывает его (DK-329).
// Черновик переносится раньше записи, и решение о скелете принималось по
// taskFile, который заполняет только ветка без --link: add со ссылкой молча
// клал скелет поверх перенесённого текста. Срабатывание от --link не зависит.
func TestAddNonAgentAppendsAcceptance(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "текст черновика под нарезку цели\n\nситуация из черновика", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	// Оформление со ссылкой на файл цели: штатный виток нарезки, у LLD-строк
	// вид user.
	p := AddParams{
		ID: "XR-008", Title: "Из черновика со ссылкой", Type: "task",
		Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза",
		Link: "tasks/XR-005.md",
	}
	msg, err := cmdAdd(root, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "черновик перенесён") {
		t.Fatalf("add молчит про перенос черновика: %q", msg)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatalf("файл задачи не появился: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "текст черновика под нарезку цели") {
		t.Fatalf("текст черновика потерян:\n%s", body)
	}
	// Ситуация из черновика доказывает, что стоит перенесённый текст, а не скелет.
	for _, want := range []string{"## Что происходит\n\nситуация из черновика", "## Приёмка", "- вид: user", "- барьер «глаза»:"} {
		if !strings.Contains(body, want) {
			t.Errorf("в файле задачи нет %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "Цель: [tasks/XR-005.md](tasks/XR-005.md)") {
		t.Errorf("ссылка на файл цели не уехала в болванку:\n%s", body)
	}
	b, _ := LoadBoard(boardPath(root))
	if row := b.find("XR-008"); row == nil {
		t.Fatal("строки XR-008 нет на доске")
	} else if row.Link != "[tasks/XR-008.md](tasks/XR-008.md)" {
		t.Fatalf("ссылка строки %q, жду на файл задачи: при --link ячейку занимает он", row.Link)
	}
	// Без --link итог тот же: текст цел, скелет не пишется, раздел дописан.
	if _, err := cmdDraft(root, "второй черновик без ссылки\n\nситуация второго черновика", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-009")
	if _, err := cmdAdd(root, AddParams{
		ID: "XR-009", Title: "Из черновика без ссылки", Type: "task",
		Rank: "0+1+1+0+1", Accept: "mixed", Barrier: "глаза",
	}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-009.md"))
	if err != nil {
		t.Fatalf("файл задачи не появился: %v", err)
	}
	body = string(data)
	if !strings.Contains(body, "второй черновик без ссылки") {
		t.Fatalf("текст черновика потерян:\n%s", body)
	}
	for _, want := range []string{"## Что происходит\n\nситуация второго черновика", "## Приёмка", "- вид: mixed", "- барьер «глаза»:"} {
		if !strings.Contains(body, want) {
			t.Errorf("в файле задачи нет %q:\n%s", want, body)
		}
	}
	// Файл без раздела и без замыкающего перевода строки: раздел дописывается
	// с отделяющей пустой строкой, ожидание выведено руками.
	manual := "# XR-010: Ручной файл без раздела\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-010.md"), []byte(manual), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{
		ID: "XR-010", Title: "Ручной файл без раздела", Type: "task",
		Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза",
	}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-010.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# XR-010: Ручной файл без раздела\n\n## Приёмка\n\n- вид: user\n- барьер «глаза»:\n"
	if string(data) != want {
		t.Fatalf("файл после дописывания:\n%s\nожидал:\n%s", data, want)
	}
	// Файл с уже стоящим разделом не получает второй: такой файл остаётся от
	// add, упавшего после записи до правки доски.
	withSect := "# XR-011: Ручной файл с разделом\n\n## Приёмка\n\n- вид: user\n- барьер «глаза»:\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-011.md"), []byte(withSect), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{
		ID: "XR-011", Title: "Ручной файл с разделом", Type: "task",
		Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза",
	}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-011.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != withSect {
		t.Fatalf("файл с разделом изменён:\n%s", data)
	}
}

// TestAddAppendsAcceptancePastFencedQuote: наличие раздела решает тот же
// читатель, что gate и lint (readSectionFromPath), а не подстрока. Черновик
// про поведение taskctl цитирует «## Приёмка» внутри блока кода: подстрока
// считала цитату разделом, настоящий не дописывался, и move check отказывал
// громко с «раздела нет». На коде до правки тест падал на первом же шаге:
// дописанного суффикса в файле нет.
func TestAddAppendsAcceptancePastFencedQuote(t *testing.T) {
	root := setup(t)
	quote := "```md\n## Приёмка\n\n- вид: user\n```\n"
	if _, err := cmdDraft(root, "черновик про taskctl цитирует раздел:\n\n"+quote, "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveDraftDoD(t, root, "XR-008")
	if _, err := cmdAdd(root, AddParams{
		ID: "XR-008", Title: "Цитата в блоке кода", Type: "task",
		Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-008.md"))
	if err != nil {
		t.Fatalf("файл задачи не появился: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, quote) {
		t.Fatalf("цитата внутри блока кода изменена:\n%s", body)
	}
	if want := "## Приёмка\n\n- вид: user\n- барьер «глаза»:\n\n## Ход работы\n"; !strings.Contains(body, want) {
		t.Fatalf("настоящий раздел не дописан:\n%s", body)
	}
	// Дописанный раздел обязан быть виден читателю move check целиком.
	text, found, ok := acceptanceSection(root, "XR-008")
	if !ok || !found {
		t.Fatalf("раздел не виден читателю gate: ok=%v found=%v", ok, found)
	}
	if bar, _ := parseAcceptance(text); bar != "глаза" {
		t.Errorf("барьер раздела %q, жду «глаза»", bar)
	}
}

// TestMoveCheckGateRejectsNonAgentWithoutBypasses: не агентский вид требует
// раздел «Приёмка» с перебором обходов по числу из закрытого списка. Меньше
// строк это отказ move check, ровно столько проходит (LLD DK-292, решение 4).
func TestMoveCheckGateRejectsNonAgentWithoutBypasses(t *testing.T) {
	root := setup(t)
	// user-задача с барьером «глаза» (3 обхода): раздел стоит, но обходов
	// меньше, чем нужно, move в Check обязан отбиться.
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "С видом", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-100", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveScenario(t, root, "XR-100")
	if _, err := cmdMove(root, "XR-100", SectCheck, "", CommitOpts{}); err == nil {
		t.Fatal("move check без перебора обходов должен отбиваться")
	}
	// Допишем три строки обхода: барьер «глаза» требует ровно столько.
	acceptanceBody := "# XR-100: С видом\n" + fixtureScenario +
		"\n## Приёмка\n\n- вид: user\n- барьер «глаза»: причина\n" +
		"  - headless-замер: годится, ширина уходит агенту\n" +
		"  - регcheck по хуку: годится, гоняется на comмите\n" +
		"  - глазом по дашборду: не годится, нужна доставка ревьюверу\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-100.md"), []byte(acceptanceBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-100", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("move check с полным перебором должен пройти: %v", err)
	}
}

// TestMoveCheckGateAgentSkipsAcceptance: агентский вид без суффикса ворота
// «Приёмка» не требует, для него довольно сценария проверки и слитого кода.
func TestMoveCheckGateAgentSkipsAcceptance(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "Агентская", Type: "task", Rank: "0+1+1+0+1", Accept: "agent", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-100", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	giveScenario(t, root, "XR-100")
	if _, err := cmdMove(root, "XR-100", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("агентский move check без «Приёмка» должен пройти: %v", err)
	}
}

// TestClosePreservesAcceptSuffix: вид переживает close и попадает в архивную
// строку (LLD DK-292, решение 3). Виды это свойство задачи, а не её очереди, и
// без него сводке нечего считать. На старом коде splitTitle возвращал 4 значения
// и суффикс просто терялся.
func TestClosePreservesAcceptSuffix(t *testing.T) {
	root := setup(t)
	// user-задача с барьером «событие» (3 обхода), перебор заполнен.
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "С видом", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "событие"}); err != nil {
		t.Fatal(err)
	}
	acceptanceBody := "# XR-100: С видом\n" +
		"\n## Приёмка\n\n- вид: user\n- барьер «событие»: причина\n" +
		"  - событие в логе: годится\n" +
		"  - пустой лог: годится\n" +
		"  - сторонний источник: годится\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-100.md"), []byte(acceptanceBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-100", Date: "2026-07-08"}); err != nil {
		t.Fatalf("close user-задачи: %v", err)
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	var archTitle string
	for _, r := range arch.Rows {
		if r.ID == "XR-100" {
			archTitle = r.Cells[1]
		}
	}
	if !strings.Contains(archTitle, "[приёмка: user]") {
		t.Fatalf("суффикс вида потерян при close: %q", archTitle)
	}
}

// TestSetAcceptRevision: set --accept меняет вид в заголовке, согласие на
// повышение не переводится (у него нет обхода по определению). На старом коде
// поля Accept в SetParams не было, и менять вид было некомандой (LLD DK-292,
// решение 4).
func TestSetAcceptRevision(t *testing.T) {
	root := setup(t)
	// Агентскую повышать до согласия нельзя: у согласия нет обхода.
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "Согласие", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "согласие"}); err != nil {
		t.Fatal(err)
	}
	acceptanceBody := "# XR-100: Согласие\n" +
		"\n## Приёмка\n\n- вид: user\n- барьер «согласие»: причина\n" +
		"  - подтверждение заказчика: годится\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-100.md"), []byte(acceptanceBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-100", Accept: "agent"}); err == nil {
		t.Fatal("повышение через «согласие» должно отбиваться")
	}
	// Понижение agent -> user возможно.
	if _, err := cmdAdd(root, AddParams{ID: "XR-101", Title: "Вид", Type: "task", Rank: "0+1+1+0+1", Accept: "agent", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSet(root, SetParams{ID: "XR-101", Accept: "user"})
	if err != nil {
		t.Fatalf("set --accept user: %v", err)
	}
	if !strings.Contains(msg, "вид agent -> user") {
		t.Fatalf("сообщение set --accept: %q", msg)
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-101").Title; got != "Вид [приёмка: user]" {
		t.Fatalf("после set заголовок: %q", got)
	}
	// Повышение user -> agent на барьере не «согласие» проходит.
	giveScenario(t, root, "XR-100")
	acceptanceBody2 := "# XR-100: Согласие\n" +
		"\n## Приёмка\n\n- вид: user\n- барьер «глаза»: причина\n" +
		"  - одно: годится\n  - два: годится\n  - три: годится\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-100.md"), []byte(acceptanceBody2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-100", Accept: "agent"}); err != nil {
		t.Fatalf("повышение через «глаза» должно проходить: %v", err)
	}
}

// TestJSONAcceptField: list --json кладёт вид в отдельное поле, а не оставляет
// его в title (LLD DK-292, решение 3: вид живёт суффиксом для строки доски, а
// машинному читателю нужно значение). У агентской поле пустое.
func TestJSONAcceptField(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [приёмка: mixed]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "backlog")
	if err != nil {
		t.Fatal(err)
	}
	rows := decodeBoard(t, out).Sections[0].Rows
	var got *jsonRow
	for i := range rows {
		if rows[i].ID == "XR-004" {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatal("XR-004 не нашлась")
	}
	if got.Title != "Хвост" || got.Accept != "mixed" {
		t.Fatalf("accept не разобран: title=%q accept=%q", got.Title, got.Accept)
	}
}

// TestParseAcceptanceCountsByBarrier: счёт обходов идёт по подсписку второго
// уровня, ровно по строкам после барьера. Меньше и больше положенного это
// нарушение, ключ из шести отдельно (LLD DK-292, решение 1).
func TestParseAcceptanceCountsByBarrier(t *testing.T) {
	cases := []struct {
		text     string
		barrier  string
		bypasses int
	}{
		{"- вид: user\n- барьер «глаза»: delegate\n  - один\n  - два\n  - три\n", "глаза", 3},
		{"- барьер «доступ»: delegate\n  - один\n  - два\n  - три\n  - четыре\n", "доступ", 4},
		{"- барьер «глаза»: delegate\n  - один\n", "глаза", 1},
		{"- барьер «глаза»: delegate\n", "глаза", 0},
		{"- вид: user\n", "", 0},
	}
	for _, c := range cases {
		bar, n := parseAcceptance(c.text)
		if bar != c.barrier || n != c.bypasses {
			t.Errorf("parseAcceptance(%q) = %q,%d; ожидал %q,%d", c.text, bar, n, c.barrier, c.bypasses)
		}
	}
}

// TestAcceptanceSectionHonoursFences: заголовок «## Приёмка» внутри кодового
// блока это чужой вывод, не раздел. Текст чужого блока в раздел не попадает.
func TestAcceptanceSectionHonoursFences(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# XR-001: задача\n\n## Сценарий проверки\n\nшаги\n\n" +
		"```\n## Приёмка\n\n- барьер «глаза»: не считается\n  - один\n  - два\n  - три\n```\n\n" +
		"## Приёмка\n\n- вид: user\n- барьер «согласие»: delegate\n  - один\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-001.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	text, found, ok := acceptanceSection(root, "XR-001")
	if !ok {
		t.Fatal("файл есть, раздел обязан читаться")
	}
	if !found {
		t.Fatal("свой раздел «Приёмка» не найден за чужим забором")
	}
	bar, n := parseAcceptance(text)
	if bar != "согласие" {
		t.Errorf("барьер из чужого блока принят за свой: %q", bar)
	}
	if n != 1 {
		t.Errorf("обходов из чужого блока насчитано %d, ждал 1", n)
	}
}

// TestLintAcceptanceIgnoresBypassCount: lint ловит отсутствие раздела «Приёмка»
// и чужой ключ барьера, но не считает строки обхода. Счёт оставлен воротам move
// check (LLD DK-292, решение 4), а у свежего скелета, который заводит add,
// обходов ноль, и пересчёт шумел бы на каждой новой user/mixed-задаче (решение
// 6). На старом коде, где lintAcceptance проверял bypasses != want, тест падал
// на первом шаге: находка про «обходов 3, а строк 0».
func TestLintAcceptanceIgnoresBypassCount(t *testing.T) {
	root := setup(t)
	// Свежий скелет: барьер есть, обходов ноль. lint молчит.
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "С видом", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "глаза"}); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "XR-100") {
			t.Fatalf("на свежем скелете lint нашёл: %s", f)
		}
	}
	// Чужой ключ барьера ловится по-прежнему: заменим «глаза» на «чего».
	p := filepath.Join(root, "docs", "tasks", "XR-100.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(data), "глаза", "чего")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(finds, func(f string) bool {
		return strings.Contains(f, "XR-100") && strings.Contains(f, "не из шести")
	}) {
		t.Fatalf("чужой ключ барьера не пойман: %v", finds)
	}
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Блок «Связи» экрана задачи (замечания пользователя по DK-470): закрытая
// задача не остаётся голым ID, тип артефакта и род связи видны, порядок
// осмысленный. Стенд собирает проект во временном каталоге: доска мокается
// картой строк, архив и файлы задач лежат настоящими файлами.

func putDoc(t *testing.T, root, rel, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func linkTasks(t *testing.T, links map[string]any) []map[string]any {
	t.Helper()
	if links == nil {
		t.Fatal("блок связей пуст")
	}
	tasks, ok := links["tasks"].([]map[string]any)
	if !ok {
		t.Fatalf("в блоке связей нет задач: %+v", links)
	}
	return tasks
}

// Название закрытой задачи находится в архиве доски вместе с датой закрытия, у
// упоминания без строки и архива читается из файла задачи, а совсем неизвестный
// ID едет с причиной полем note: молча голым ID связь не остаётся.
func TestTaskLinksTitlesForClosed(t *testing.T) {
	root := t.TempDir()
	putDoc(t, root, "docs/TASKS-archive.md", `# сделано

| ID | Задача | Тип | P | Закрыто | Ссылка |
|----|--------|-----|---|---------|--------|
| XR-136 | панель чата в шапке | task | P2 | 2026-07-01 | [tasks/archive/2026/XR-136.md](tasks/archive/2026/XR-136.md) |
`)
	putDoc(t, root, "docs/tasks/XR-9.md", "# XR-9: название из файла задачи\n\nтело\n")
	putDoc(t, root, "docs/lld/XR-1-own.md", "# XR-1: свой дизайн\n")
	putDoc(t, root, "docs/lld/XR-77-alien.md", "# XR-77: чужой дизайн\n")
	rows := map[string]boardRow{}
	text := "дизайн в lld/XR-77-alien.md, постановка поминает XR-136, XR-9 и XR-404"
	links := taskLinks(root, "XR-1", "", text, rows, nil, nil)
	// Артефакты самой задачи стоят выше упомянутых чужих: свой LLD первым.
	lld := links["lld"].([]map[string]any)
	if len(lld) != 2 || lld[0]["own"] != true || lld[1]["file"] != "lld/XR-77-alien.md" {
		t.Errorf("свой LLD не встал раньше чужого: %+v", lld)
	}
	tasks := linkTasks(t, links)
	byID := map[string]map[string]any{}
	for _, row := range tasks {
		byID[row["id"].(string)] = row
	}
	if got := byID["XR-136"]; got["title"] != "панель чата в шапке" || got["closed"] != "2026-07-01" {
		t.Errorf("закрытая задача не собралась из архива: %+v", got)
	}
	if got := byID["XR-9"]; got["title"] != "название из файла задачи" {
		t.Errorf("название не прочиталось из файла задачи: %+v", got)
	}
	if got := byID["XR-404"]; got["title"] != nil || got["note"] == nil {
		t.Errorf("неизвестный ID остался без причины: %+v", got)
	}
}

// Тип артефакта и род связи: цель узнаётся по заголовку, задачи из
// зависимостей несут направление (после, держит), упоминание без зависимости
// идёт без рода, источник его не различает. Порядок пересмотрен
// пользователем: сначала открытые со связью держит/после, затем остальные
// открытые по убыванию ранга (высокий ранг обычной задачи не обгоняет
// блокирующую), закрытые в самом низу; без ранга по номеру.
func TestTaskLinksKindsRelOrder(t *testing.T) {
	root := t.TempDir()
	putDoc(t, root, "docs/TASKS-archive.md", `# сделано

| ID | Задача | Тип | P | Закрыто | Ссылка |
|----|--------|-----|---|---------|--------|
| XR-136 | давно закрытая | task | P2 | 2026-07-01 | [tasks/archive/2026/XR-136.md](tasks/archive/2026/XR-136.md) |
`)
	rows := map[string]boardRow{
		"XR-100": {ID: "XR-100", Title: "Цель: большой раздел", R: 40},
		"XR-2":   {ID: "XR-2", Title: "блокирует после", R: 8},
		"XR-30":  {ID: "XR-30", Title: "блокирует держит", R: 30},
		"XR-7":   {ID: "XR-7", Title: "важная открытая", R: 50},
		"XR-5":   {ID: "XR-5", Title: "открытая поменьше", R: 10},
	}
	text := "сначала XR-5, потом XR-136, XR-7, XR-30, XR-100, XR-404 и XR-2"
	links := taskLinks(root, "XR-1", "", text, rows, []string{"XR-2"}, []string{"XR-30"})
	tasks := linkTasks(t, links)
	var order []string
	for _, row := range tasks {
		order = append(order, row["id"].(string))
	}
	want := []string{"XR-30", "XR-2", "XR-7", "XR-100", "XR-5", "XR-136", "XR-404"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("порядок связей %v, жду %v (блокирующие, открытые по рангу, закрытые ниже, мёртвые в самом низу)", order, want)
		}
	}
	byID := map[string]map[string]any{}
	for _, row := range tasks {
		byID[row["id"].(string)] = row
	}
	if byID["XR-100"]["kind"] != "цель" || byID["XR-2"]["kind"] != "задача" {
		t.Errorf("тип артефакта не различился: %+v", tasks)
	}
	if byID["XR-2"]["rel"] != "после" || byID["XR-30"]["rel"] != "держит" {
		t.Errorf("род связи из зависимостей не доехал: %+v", tasks)
	}
	if byID["XR-7"]["rel"] != nil {
		t.Errorf("у упоминания без зависимости выдуман род: %+v", byID["XR-7"])
	}
}

// Разметка блока «Связи»: предмет проверки собранный экран, а не исходник,
// поэтому статика поднимается в node с заглушкой DOM (стенд
// testdata/poc_links.mjs). Без node шаг пропускается: узел стенда, а не
// рабочей части.
func TestStaticTaskLinksCard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд блока связей пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_links.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("блок связей: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Лестница названий кончается накопителем: за ID без строки и архива может
// стоять запись черновика, и связь тогда зовётся черновиком, несёт название из
// первой строки файла и признак draft (по нему показ уводит на экран записи, а
// не на форму несуществующей задачи). За ID, которого нет нигде, не стоит
// ничего: связь помечается мёртвой, потому что хранилища у связей нет и снятый
// ID из чужой постановки сам собой не пропадает.
func TestTaskLinksDraftAndGone(t *testing.T) {
	root := t.TempDir()
	putDoc(t, root, "docs/tasks/drafts/XR-50.md",
		"# XR-50: чат копит замечания, пока агент занят\n\nзаписан 2026-08-20\n")
	putDoc(t, root, "docs/tasks/drafts/XR-51.md", "тело записи без заголовка\n")
	rows := map[string]boardRow{"XR-8": {ID: "XR-8", Title: "живая соседка", R: 20}}
	text := "поминаем XR-8, XR-50, XR-51 и снятый XR-483"
	tasks := linkTasks(t, taskLinks(root, "XR-1", "", text, rows, nil, nil))
	byID := map[string]map[string]any{}
	for _, row := range tasks {
		byID[row["id"].(string)] = row
	}
	draft := byID["XR-50"]
	if draft["kind"] != "черновик" {
		t.Errorf("запись накопителя не названа черновиком: %+v", draft)
	}
	if draft["title"] != "чат копит замечания, пока агент занят" {
		t.Errorf("название черновика не прочиталось из его файла: %+v", draft)
	}
	if draft["draft"] != true {
		t.Errorf("у черновика нет признака дороги в накопитель: %+v", draft)
	}
	if draft["gone"] != nil || draft["note"] != nil {
		t.Errorf("лежащий на месте черновик посчитан пропавшим: %+v", draft)
	}
	// Запись без заголовка на месте, и мёртвой её звать не за что.
	if bare := byID["XR-51"]; bare["gone"] != nil || bare["kind"] != "черновик" || bare["note"] == nil {
		t.Errorf("черновик без заголовка разобран неверно: %+v", bare)
	}
	dead := byID["XR-483"]
	if dead["gone"] != true {
		t.Errorf("исчезнувший ID не помечен мёртвым: %+v", dead)
	}
	if dead["kind"] == "задача" || dead["title"] != nil {
		t.Errorf("исчезнувший ID выдаёт себя за живую задачу: %+v", dead)
	}
	if dead["note"] == nil {
		t.Errorf("мёртвая связь не сказала, где её искали: %+v", dead)
	}
	// Живая связь мёртвой пометки не получает, иначе признак ничего не значит.
	if live := byID["XR-8"]; live["gone"] != nil || live["kind"] != "задача" {
		t.Errorf("живая связь пострадала от разбора мёртвых: %+v", live)
	}
	// Мёртвая строка уходит в самый низ: делать с ней нечего, а место наверху
	// принадлежит живым.
	if last := tasks[len(tasks)-1]["id"]; last != "XR-483" {
		t.Errorf("мёртвая связь не легла в самый низ, внизу %v", last)
	}
}

// Черновик, разобранный груммингом до строки доски, остаётся задачей: файл
// записи после грумминга может ещё лежать на месте, и лестница обязана
// остановиться на строке, а не переназвать живую задачу черновиком.
func TestTaskLinksBoardRowBeatsDraft(t *testing.T) {
	root := t.TempDir()
	putDoc(t, root, "docs/tasks/drafts/XR-50.md", "# XR-50: старая запись накопителя\n")
	rows := map[string]boardRow{"XR-50": {ID: "XR-50", Title: "разобранная задача", R: 12}}
	tasks := linkTasks(t, taskLinks(root, "XR-1", "", "поминаем XR-50", rows, nil, nil))
	if got := tasks[0]; got["kind"] != "задача" || got["title"] != "разобранная задача" || got["draft"] != nil {
		t.Errorf("строка доски проиграла файлу накопителя: %+v", got)
	}
}

// Разметка мёртвой и черновой связи: предмет проверки собранный экран, стенд
// поднимает статику в node с заглушкой DOM. Без node шаг пропускается.
func TestStaticTaskLinksGoneRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд мёртвых связей пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_linkgone.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("мёртвые и черновые связи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

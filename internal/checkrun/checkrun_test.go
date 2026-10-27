package checkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Модель, которой нет в первой лестнице, берётся из следующей, и ниже
// исходного яруса отбор не опускается. Совпадение модели с моделью разработки
// выбор не режет: независимость несёт отдельная сессия (DK-1360).
func TestChooseAvailableTakesNextLadderWithoutLoweringTier(t *testing.T) {
	ladders := []Ladder{
		{Name: "glm", Steps: []Step{{"max", "glm-5.3"}}},
		{Name: "router", Steps: []Step{{"base", "z-ai/glm-5.3"}, {"pro", "anthropic/sonnet"}, {"max", "anthropic/opus"}}},
	}
	// Точного яруса у первой лестницы нет, и отбор берёт его у соседней, а не
	// ступенью вверх по первой: ярус вердикта это требование достаточности.
	ch := ChooseAvailable("base", ladders)
	if ch.Refusal != "" || ch.Harness != "router" || ch.Tier != "base" || ch.Model != "z-ai/glm-5.3" || ch.Stepped {
		t.Fatalf("base: %+v", ch)
	}
	ch = ChooseAvailable("max", ladders)
	if ch.Harness != "glm" || ch.Tier != "max" || ch.Model != "glm-5.3" || ch.Stepped {
		t.Fatalf("max: %+v", ch)
	}
	if ch := ChooseAvailable("ultra", ladders); ch.Refusal == "" {
		t.Fatalf("незнакомый ярус: %+v", ch)
	}
}

func TestChooseAvailableKeepsPrimaryWhenItCanChoose(t *testing.T) {
	ladders := []Ladder{{Name: "first", Steps: ladder}, {Name: "second", Steps: []Step{{"base", "other"}}}}
	ch := ChooseAvailable("base", ladders)
	if ch.Harness != "first" || ch.Tier != "base" || ch.Model != "sonnet" {
		t.Fatalf("своя ступень пропущена: %+v", ch)
	}
}

func TestParseLaddersUsesEnabledCurrentHarnessBeforeDefault(t *testing.T) {
	raw := []byte(`{"harnesses":[{"name":"off","enabled":false,"default":true},
	{"name":"default","enabled":true,"default":true},
	{"name":"current","enabled":true,"auto":true}]}`)
	for _, preferred := range []string{"current", "off", ""} {
		got, note := ParseLadders(raw, preferred)
		want := "default"
		if preferred == "current" {
			want = "current"
		}
		if note != "" || len(got) != 2 || got[0].Name != want {
			t.Fatalf("%s: %+v, %s", preferred, got, note)
		}
	}
}

// Лестница подписки как на машине: снизу вверх, у каждой ступени своя модель.
var ladder = []Step{{"mini", "haiku"}, {"base", "sonnet"}, {"pro", "opus"}, {"max", "fable"}}

// Кейс 2 DK-947 в редакции DK-1360: модель яруса ревью совпала с моделью
// разработки. Ступень не нужна: независимость несёт отдельная сессия прогона,
// и сценарий идёт той же моделью на своём ярусе.
func TestChooseKeepsSameModelAsDeveloper(t *testing.T) {
	ch := Choose("base", ladder)
	if ch.Refusal != "" || ch.Stepped || ch.Tier != "base" || ch.Model != "sonnet" {
		t.Fatalf("та же модель на своём ярусе: %+v", ch)
	}
}

// Яруса в лестнице нет: модель пуста, решение за носителем.
func TestChooseUnknownTier(t *testing.T) {
	if ch := Choose("ultra", ladder); ch.Model != "" || ch.Refusal != "" {
		t.Fatalf("незнакомый ярус: %+v", ch)
	}
	if ch := Choose("base", nil); ch.Model != "" || ch.Refusal != "" {
		t.Fatalf("пустая лестница: %+v", ch)
	}
}

func TestParseTier(t *testing.T) {
	if got := ParseTier("model: sonnet\neffort: high\ntier: base\nvia: x\n"); got != "base" {
		t.Fatalf("ярус: %q", got)
	}
	if got := ParseTier("model: sonnet\n"); got != "" {
		t.Fatalf("без строки tier ярус пуст: %q", got)
	}
}

// Модель читается из тех же машинных строк, что и ярус: их печатают вердикт
// `agentctl pick` и расчёт проверяющего `agentctl check` (DK-1116). Прочерк
// это «разворачивать нечем», и модели в нём нет.
func TestParseModel(t *testing.T) {
	if got := ParseModel("model: sonnet\neffort: high\ntier: base\n"); got != "sonnet" {
		t.Fatalf("модель: %q", got)
	}
	if got := ParseModel("model: -\ntier: base\n"); got != "" {
		t.Fatalf("прочерк за модель не сходит: %q", got)
	}
	if got := ParseModel("tier: base\n"); got != "" {
		t.Fatalf("без строки model модель пуста: %q", got)
	}
}

func TestParseLadder(t *testing.T) {
	raw := `{"harnesses": [
	  {"name": "выключенная", "enabled": false, "default": false, "models": []},
	  {"name": "вторая", "enabled": true, "default": false, "models": [{"tier": "base", "model": "b2"}]},
	  {"name": "первая", "enabled": true, "default": true, "models": [{"tier": "base", "model": "b1"}, {"tier": "pro", "model": "p1"}]}
	]}`
	name, steps, note := ParseLadder([]byte(raw))
	if note != "" || name != "первая" || len(steps) != 2 || steps[1] != (Step{"pro", "p1"}) {
		t.Fatalf("подписка по умолчанию: %q %+v %q", name, steps, note)
	}
	name, _, _ = ParseLadder([]byte(strings.Replace(raw, `"default": true`, `"default": false`, 1)))
	if name != "вторая" {
		t.Fatalf("без признака берётся первая включённая: %q", name)
	}
	if _, _, note := ParseLadder([]byte("не json")); note == "" {
		t.Fatal("неразобранный ответ молчит")
	}
}

// Заказ смешанного вида оставляет закрытие человеку, агентского закрывает.
func TestOrderByAccept(t *testing.T) {
	mixed := Order("XR-1", "sonnet", AcceptMixed)
	if !strings.HasPrefix(mixed, OrderHead+"XR-1") || !strings.Contains(mixed, "строку из Check не закрывай") ||
		strings.Contains(mixed, "taskctl close") || !strings.Contains(mixed, "shipctl smoke XR-1") ||
		!strings.Contains(mixed, "Разработку вёл sonnet") {
		t.Fatalf("заказ mixed: %s", mixed)
	}
	if agent := Order("XR-1", "", ""); !strings.Contains(agent, "taskctl close XR-1") {
		t.Fatalf("заказ agent: %s", agent)
	}
}

// regcheck:test-begin
//
// TestOrderCarriesDebtStep: DK-1356. Конвейерная сдача бежит мимо исполнителя,
// и хвост долга у неё разбирает проверяющий. Шаг лежит в заказе у любого вида
// приёмки, до которого конвейер вообще доносит проверяющего, и называет команду
// закрытия отметки.
func TestOrderCarriesDebtStep(t *testing.T) {
	for _, accept := range []string{"", AcceptMixed} {
		order := Order("XR-1", "sonnet", accept)
		if !strings.Contains(order, "board-debt") || !strings.Contains(order, `taskctl debt-done XR-1 "пересечений нет"`) {
			t.Fatalf("заказ (%s) не несёт шаг долга с командой закрытия: %s", accept, order)
		}
	}
}

// regcheck:test-end

// project собирает синтетический проект: обвязка выката и файлы задач.
func project(t *testing.T, cfg string, docs map[string]string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if cfg != "" {
		if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".devkit", "deploy.local"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for id, doc := range docs {
		p := DocPath(root, id)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const autonomous = "deploy = true\nautonomous = true\n"

const doc = "# XR-3: задача\n\n## Выкат\n\n- Коммиты: abc1234\n"

const devSonnet = "\n## Ход работы\n\n- Разработка: субагент sonnet/high по вердикту pick, 2026-09-11.\n"

func TestNeed(t *testing.T) {
	check := Row{ID: "XR-3", Sect: "check", Section: "Check"}
	for _, tc := range []struct {
		name, cfg, doc, want string
		row                  Row
	}{
		{"выкат за пользователем", "deploy = true\nautonomous = false\n", doc, "autonomous = false", check},
		{"не в Check", autonomous, doc, "не в Check", Row{ID: "XR-3", Sect: "in-progress", Section: "In progress"}},
		{"вид user", autonomous, doc, "вид user", Row{ID: "XR-3", Sect: "check", Accept: AcceptUser}},
		{"провал", autonomous, doc, "провал", Row{ID: "XR-3", Sect: "check", Fail: "лежит"}},
		{"файла нет", autonomous, "", "файла задачи нет", check},
		{"отметка стоит", autonomous, doc + "- smoke прогнан, 2026-09-11\n", "smoke прогнан", check},
		{"нужен", autonomous, doc, "", check},
		{"mixed нужен", autonomous, doc, "", Row{ID: "XR-3", Sect: "check", Accept: AcceptMixed}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]string{}
			if tc.doc != "" {
				docs["XR-3"] = tc.doc
			}
			root := project(t, tc.cfg, docs)
			got := Need(root, tc.row)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("ждал %q, пришло %q", tc.want, got)
			}
		})
	}
}

// fake это носитель стенда: помнит, что ему отдали, и отвечает как сказано.
type fake struct {
	ready   string
	failed  bool
	tier    string
	ladder  []Step
	planned []Plan
}

func (f *fake) Ready(string) (string, bool) { return f.ready, f.failed }
func (f *fake) Tier(string) (string, string) {
	return f.tier, "ярус " + f.tier + " по вердикту стенда"
}
func (f *fake) Ladder() []Step { return f.ladder }
func (f *fake) Raise(p Plan) Report {
	f.planned = append(f.planned, p)
	return Report{Line: p.Raised("стендом"), Raised: true}
}

// Кейс 2 целиком в редакции DK-1360: строка mixed выкачена, разработку вёл
// sonnet, вердикт ревью дал base той же модели. Прогон уходит носителю той же
// моделью на своём ярусе, отчёт называет модель и автора, а заказ оставляет
// закрытие человеку.
func TestRunRaisesSteppedChecker(t *testing.T) {
	root := project(t, autonomous, map[string]string{"XR-3": doc + devSonnet})
	f := &fake{tier: "base", ladder: ladder}
	rows := map[string]Row{
		"XR-3": {ID: "XR-3", Sect: "check", Accept: AcceptMixed},
		"XR-4": {ID: "XR-4", Sect: "in-progress", Section: "In progress"},
	}
	reps := Run(f, root, rows, nil)
	if len(reps) != 1 || !reps[0].Raised || reps[0].Failed || reps[0].ID != "XR-3" {
		t.Fatalf("ждал один поднятый прогон по секции Check: %+v", reps)
	}
	if len(f.planned) != 1 || f.planned[0].Choice.Model != "sonnet" {
		t.Fatalf("носителю ушла не та модель: %+v", f.planned)
	}
	for _, want := range []string{"модель sonnet", "разработку вёл sonnet", "независимость несёт отдельная сессия"} {
		if !strings.Contains(reps[0].Line, want) {
			t.Errorf("отчёт не называет %q: %s", want, reps[0].Line)
		}
	}
	if !strings.Contains(f.planned[0].Order, "строку из Check не закрывай") {
		t.Errorf("заказ mixed: %s", f.planned[0].Order)
	}
}

// Отказ носителя и отказ предполёта это поломки со словами, «не нужно» это
// норма, а пустая секция отвечает одной строкой. Модель яруса, совпавшая с
// моделью разработки, подъёмом больше не мешает (DK-1360).
func TestRunOutcomes(t *testing.T) {
	root := project(t, autonomous, map[string]string{"XR-3": doc + "\n## Ход работы\n\n- Разработка: субагент fable/high по вердикту pick, 2026-09-11.\n"})
	rows := map[string]Row{"XR-3": {ID: "XR-3", Sect: "check"}}

	reps := Run(&fake{tier: "max", ladder: ladder}, root, rows, nil)
	if Failed(reps) || !strings.Contains(reps[0].Line, "модель fable") ||
		!strings.Contains(reps[0].Line, "разработку вёл fable") {
		t.Fatalf("та же модель на верхнем ярусе: %+v", reps)
	}
	reps = Run(&fake{ready: "tmux нет", failed: true, tier: "base", ladder: ladder}, root, rows, nil)
	if !Failed(reps) || !strings.Contains(reps[0].Line, "tmux нет") {
		t.Fatalf("отказ предполёта: %+v", reps)
	}
	reps = Run(&fake{tier: "base", ladder: ladder}, root, rows, []string{"XR-9"})
	if !Failed(reps) || !strings.Contains(reps[0].Line, "строки нет") {
		t.Fatalf("строки нет: %+v", reps)
	}
	reps = Run(&fake{}, root, map[string]Row{}, nil)
	if Failed(reps) || len(reps) != 1 || !strings.Contains(reps[0].Line, "в Check пусто") {
		t.Fatalf("пустая секция: %+v", reps)
	}
	if err := os.WriteFile(DocPath(root, "XR-3"), []byte(doc+"- smoke прогнан, 2026-09-11\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reps = Run(&fake{}, root, rows, nil)
	if Failed(reps) || !strings.Contains(reps[0].Line, NoNeed) {
		t.Fatalf("прогнанная строка: %+v", reps)
	}
}

func TestChooseAvailableUsesActualEnabledOwnerAndPartialLadder(t *testing.T) {
	raw := []byte(`{"harnesses":[
	{"name":"glm","enabled":true,"default":true,"models":[{"tier":"pro","model":"glm"},{"tier":"max","model":"glm","via":"owner"}]},
	{"name":"route","enabled":true,"auto":true,"models":[{"tier":"base","model":"too-small"},{"tier":"max","model":"sonnet","via":"owner"}]},
	{"name":"owner","enabled":true,"auto":true,"models":[]}]}`)
	ladders, note := ParseLadders(raw, "glm")
	if note != "" {
		t.Fatal(note)
	}
	ch := ChooseAvailable("pro", ladders)
	if ch.Harness != "glm" || ch.Tier != "pro" || ch.Model != "glm" || ch.Refusal != "" {
		t.Fatalf("обслуженный ярус первой лестницы пропущен: %+v", ch)
	}
	ch = ChooseAvailable("max", ladders)
	if ch.Harness != "owner" || ch.Tier != "max" || ch.Model != "glm" || ch.Refusal != "" {
		t.Fatalf("фактический владелец ступени не назван: %+v", ch)
	}
	ladders = ladders[:2]
	if ch := ChooseAvailable("max", ladders); ch.Refusal == "" {
		t.Fatalf("выключенный владелец ступени принят: %+v", ch)
	}
}

// TestTaskHarnessPolicy это допуск подписок к автоматическим назначениям
// (DK-1453): включённость и разрешённость расходятся, и вторичной подписке
// мало стоять в enabled. Без явного auto независимый выбор её не видит,
// предпочтение текущей подписки запрет не снимает, уехавшая ступень не
// поднимается с запрещённого владельца, а ответ старого agentctl без флага
// держит подписку по умолчанию.
func TestTaskHarnessPolicy(t *testing.T) {
	primary := `{"name":"glm","enabled":true,"default":true,` +
		`"models":[{"tier":"base","model":"glm-5.3"},{"tier":"pro","model":"glm-5.3"},{"tier":"max","model":"glm-5.3"}]}`
	secondary := func(auto string) string {
		flag := ""
		if auto != "" {
			flag = `,"auto":` + auto
		}
		return `{"name":"router","enabled":true` + flag +
			`,"models":[{"tier":"base","model":"route-base"},{"tier":"pro","model":"route-pro"},{"tier":"max","model":"route-max"}]}`
	}
	t.Run("включённая без разрешения не участвует", func(t *testing.T) {
		got, note := ParseLadders([]byte(`{"harnesses":[`+primary+`,`+secondary("")+`]}`), "")
		if note != "" || len(got) != 1 || got[0].Name != "glm" {
			t.Fatalf("вторичная подписка без auto прошла в отбор: %+v, %s", got, note)
		}
	})
	t.Run("после явного разрешения участвует", func(t *testing.T) {
		got, note := ParseLadders([]byte(`{"harnesses":[`+primary+`,`+secondary("true")+`]}`), "")
		if note != "" || len(got) != 2 || got[1].Name != "router" {
			t.Fatalf("разрешённая подписка не участвует: %+v, %s", got, note)
		}
	})
	t.Run("предпочтение текущей не снимает запрет", func(t *testing.T) {
		got, note := ParseLadders([]byte(`{"harnesses":[`+primary+`,`+secondary("")+`]}`), "router")
		if note != "" || len(got) != 1 || got[0].Name != "glm" {
			t.Fatalf("запрещённая подписка прошла предпочтением: %+v, %s", got, note)
		}
	})
	t.Run("запрет всех отвечает словами", func(t *testing.T) {
		got, note := ParseLadders([]byte(`{"harnesses":[`+secondary("false")+`]}`), "")
		if got != nil || !strings.Contains(note, "автоматическим назначениям не разрешены") {
			t.Fatalf("запрет всех не назван словами: %+v, %s", got, note)
		}
	})
	t.Run("старый ответ без флага держит подписку по умолчанию", func(t *testing.T) {
		got, note := ParseLadders([]byte(`{"harnesses":[`+primary+`]}`), "")
		if note != "" || len(got) != 1 || got[0].Name != "glm" {
			t.Fatalf("без флага auto подписка по умолчанию потеряна: %+v, %s", got, note)
		}
	})
	t.Run("ступень запрещённого владельца не поднимается", func(t *testing.T) {
		base := `{"name":"glm","enabled":true,"default":true,` +
			`"models":[{"tier":"max","model":"sonnet","via":"router"}]}`
		for _, tc := range []struct {
			auto, wantModel, wantHarness string
			refuse                       bool
		}{
			{auto: "", refuse: true},
			// Разрешение владельцу включает его лестницу в отбор, и точный
			// ярус владельца дешевле ступени вверх по via (DK-1360).
			{auto: "true", wantModel: "route-base", wantHarness: "router"},
		} {
			ladders, note := ParseLadders([]byte(`{"harnesses":[`+base+`,`+secondary(tc.auto)+`]}`), "")
			if note != "" {
				t.Fatal(note)
			}
			ch := ChooseAvailable("base", ladders)
			if tc.refuse {
				if ch.Refusal == "" {
					t.Fatalf("ступень запрещённого владельца поднята: %+v", ch)
				}
				continue
			}
			if ch.Refusal != "" || ch.Model != tc.wantModel || ch.Harness != tc.wantHarness {
				t.Fatalf("разрешённый владелец не поднят: %+v", ch)
			}
		}
	})
	t.Run("пустая модель после фильтрации via отдаёт отказ", func(t *testing.T) {
		base := `{"name":"glm","enabled":true,"default":true,` +
			`"models":[{"tier":"max","model":"route-max","via":"router"}]}`
		ladders, note := ParseLadders([]byte(`{"harnesses":[`+base+`,`+secondary("false")+`]}`), "")
		if note != "" {
			t.Fatal(note)
		}
		// Допуск держится и без имени исполнителя: модель яруса фильтр via
		// снял, и отказ держит только допуск.
		ch := ChooseAvailable("max", ladders)
		if ch.Refusal == "" || ch.Model != "" || ch.Harness != "" {
			t.Fatalf("пустая модель без отказа уехала бы запрещённому владельцу: %+v", ch)
		}
	})
	t.Run("пустые лестницы и ступени отдают отказ", func(t *testing.T) {
		if ch := ChooseAvailable("max", nil); ch.Refusal == "" {
			t.Fatalf("без допущенных подписок отказа нет: %+v", ch)
		}
		empty := `{"name":"glm","enabled":true,"default":true,"models":[]}`
		ladders, note := ParseLadders([]byte(`{"harnesses":[`+empty+`]}`), "")
		if note != "" {
			t.Fatal(note)
		}
		if ch := ChooseAvailable("max", ladders); ch.Refusal == "" {
			t.Fatalf("допущенная подписка без ступеней не отказала: %+v", ch)
		}
	})
}

func TestChooseAvailableSearchesIncompletePrimary(t *testing.T) {
	ladders := []Ladder{
		{Name: "primary", Steps: []Step{{"pro", "sonnet"}}},
		{Name: "secondary", Steps: []Step{{"base", "opus"}}},
	}
	// Точного яруса у первой лестницы нет, и отбор берёт его у соседней, а не
	// ступенью вверх по первой (DK-1360).
	ch := ChooseAvailable("base", ladders)
	if ch.Model != "opus" || ch.Tier != "base" || ch.Harness != "secondary" {
		t.Fatalf("точный ярус соседней лестницы скрыт ступенью первой: %+v", ch)
	}
	// Когда точного яруса нет ни у кого, отбор поднимается ступенью, и первая
	// лестница дешевле соседней.
	ladders[1].Steps = []Step{{"pro", "opus"}}
	ch = ChooseAvailable("base", ladders)
	if ch.Model != "sonnet" || ch.Tier != "pro" || ch.Harness != "primary" || !ch.Stepped {
		t.Fatalf("пропущен доступный старший ярус: %+v", ch)
	}
}

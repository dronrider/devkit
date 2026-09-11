package checkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Лестница подписки как на машине: снизу вверх, у каждой ступени своя модель.
var ladder = []Step{{"mini", "haiku"}, {"base", "sonnet"}, {"pro", "opus"}, {"max", "fable"}}

// Кейс 2 DK-947: модель яруса ревью совпала с моделью разработки. Раньше тут
// был отказ «поднять другой моделью руками», и тик повторял его каждые пять
// минут; теперь ярус берётся ступенью выше, и прогон идёт независимым
// проверяющим.
func TestChooseStepsUpOnCollision(t *testing.T) {
	ch := Choose("base", ladder, "sonnet", true)
	if ch.Refusal != "" || !ch.Stepped || ch.Tier != "pro" || ch.Model != "opus" || ch.From != "base" {
		t.Fatalf("ждал ступень base -> pro с моделью opus: %+v", ch)
	}
}

// Совпадения нет, значит и ступени нет: ярус вердикта остаётся как есть.
func TestChooseKeepsTierWithoutCollision(t *testing.T) {
	for _, tc := range []struct {
		name, dev string
		known     bool
	}{{"автор другой модели", "opus", true}, {"автор неизвестен", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ch := Choose("base", ladder, tc.dev, tc.known)
			if ch.Stepped || ch.Tier != "base" || ch.Model != "sonnet" || ch.Refusal != "" {
				t.Fatalf("ярус без столкновения не держится: %+v", ch)
			}
		})
	}
}

// Регистр в имени модели разницы не делает: ворота закрытия сравнивают так же.
func TestChooseIgnoresCase(t *testing.T) {
	if ch := Choose("base", ladder, "Sonnet", true); !ch.Stepped {
		t.Fatalf("Sonnet и sonnet это одна модель: %+v", ch)
	}
}

// Ступень выше с той же моделью пропускается: у второй подписки ярусы бывают
// разведены по одной модели, и ступень ради ступени независимости не даёт.
func TestChooseSkipsSameModelSteps(t *testing.T) {
	second := []Step{{"mini", "flash"}, {"base", "glm"}, {"pro", "glm"}, {"max", "opus"}}
	ch := Choose("base", second, "glm", true)
	if !ch.Stepped || ch.Tier != "max" || ch.Model != "opus" {
		t.Fatalf("ждал ступень до max мимо pro той же модели: %+v", ch)
	}
}

// Выше модели, отличной от автора, нет: отказ словами, в них ярус и модель.
func TestChooseRefusesAtTop(t *testing.T) {
	ch := Choose("max", ladder, "fable", true)
	if ch.Refusal == "" {
		t.Fatalf("на верхней ступени с моделью автора ждал отказ: %+v", ch)
	}
	for _, want := range []string{"max", "fable", "вела разработку"} {
		if !strings.Contains(ch.Refusal, want) {
			t.Errorf("отказ не называет %q: %s", want, ch.Refusal)
		}
	}
}

// Яруса в лестнице нет: модель пуста, решение за носителем.
func TestChooseUnknownTier(t *testing.T) {
	if ch := Choose("ultra", ladder, "sonnet", true); ch.Model != "" || ch.Refusal != "" {
		t.Fatalf("незнакомый ярус: %+v", ch)
	}
	if ch := Choose("base", nil, "sonnet", true); ch.Model != "" || ch.Refusal != "" {
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

func (f *fake) Ready(string) (string, bool)   { return f.ready, f.failed }
func (f *fake) Tier(string) (string, string)  { return f.tier, "ярус " + f.tier + " по вердикту стенда" }
func (f *fake) Ladder() []Step                { return f.ladder }
func (f *fake) Raise(p Plan) Report {
	f.planned = append(f.planned, p)
	return Report{Line: p.Raised("стендом"), Raised: true}
}

// Кейс 2 целиком: строка mixed выкачена, разработку вёл sonnet, вердикт ревью
// дал base той же модели. Прогон уходит носителю моделью opus, отчёт называет
// ступень, а заказ оставляет закрытие человеку.
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
	if len(f.planned) != 1 || f.planned[0].Choice.Model != "opus" {
		t.Fatalf("носителю ушла не та модель: %+v", f.planned)
	}
	for _, want := range []string{"поднят ступенью до pro", "модель opus", "разработку вёл sonnet"} {
		if !strings.Contains(reps[0].Line, want) {
			t.Errorf("отчёт не называет %q: %s", want, reps[0].Line)
		}
	}
	if !strings.Contains(f.planned[0].Order, "строку из Check не закрывай") {
		t.Errorf("заказ mixed: %s", f.planned[0].Order)
	}
}

// Отказ носителя и отказ ступени это поломки со словами, «не нужно» это норма,
// а пустая секция отвечает одной строкой.
func TestRunOutcomes(t *testing.T) {
	root := project(t, autonomous, map[string]string{"XR-3": doc + "\n## Ход работы\n\n- Разработка: субагент fable/high по вердикту pick, 2026-09-11.\n"})
	rows := map[string]Row{"XR-3": {ID: "XR-3", Sect: "check"}}

	reps := Run(&fake{tier: "max", ladder: ladder}, root, rows, nil)
	if !Failed(reps) || !strings.Contains(reps[0].Line, NotRaised) {
		t.Fatalf("верхняя ступень с моделью автора: %+v", reps)
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

package obey

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const doc = `# Правила

## Мимикрия

Текст должен быть неотличим от того, что пользователь пишет сам.

` + "```text" + `
## Символы
строка примера
` + "```" + `

Хвост раздела.

## Символы

Писать только теми символами, которые есть на клавиатуре.
`

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "RULES.core.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(root, "kit", "skills", "prose")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# prose\n\nвыборка эталонов\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestParseSubjects(t *testing.T) {
	subs, err := ParseSubjects("RULES.core.md «Мимикрия»; kit/skills/prose/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 {
		t.Fatalf("предметов %d, ждал 2: %+v", len(subs), subs)
	}
	if subs[0].Path != "RULES.core.md" || subs[0].Section != "Мимикрия" {
		t.Fatalf("предмет с разделом разобран как %+v", subs[0])
	}
	if subs[1].Path != "kit/skills/prose/SKILL.md" || subs[1].Section != "" {
		t.Fatalf("предмет без раздела разобран как %+v", subs[1])
	}
	if got := Join(subs); got != "RULES.core.md «Мимикрия»; kit/skills/prose/SKILL.md" {
		t.Fatalf("сборка обратно дала %q", got)
	}
}

func TestParseSubjectsErrors(t *testing.T) {
	cases := map[string]string{
		"пусто":              "",
		"раздел не закрыт":   "RULES.core.md «Мимикрия",
		"пустой раздел":      "RULES.core.md «»",
		"без пути":           "«Мимикрия»",
		"путь наружу дерева": "../secrets.md",
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSubjects(val); err == nil {
				t.Fatalf("ждал отказ на %q", val)
			}
		})
	}
}

// Привязка проверяется по дереву: путь обязан существовать, а раздел стоять
// заголовком. Заголовок внутри ограждённого блока за раздел не считается, там
// лежит текст примера.
func TestVerify(t *testing.T) {
	root := tree(t)
	ok := []Subject{
		{Path: "RULES.core.md"},
		{Path: "RULES.core.md", Section: "Мимикрия"},
		{Path: "kit/skills/prose/SKILL.md"},
	}
	for _, s := range ok {
		if err := Verify(root, s); err != nil {
			t.Fatalf("предмет %s забракован: %v", s, err)
		}
	}
	bad := map[string]Subject{
		"файла нет":               {Path: "kit/skills/нет/SKILL.md"},
		"раздела нет":             {Path: "RULES.core.md", Section: "Такого нет"},
		"раздел только в блоке":   {Path: "kit/skills/prose/SKILL.md", Section: "Символы"},
		"директория вместо файла": {Path: "kit/skills/prose"},
	}
	for name, s := range bad {
		t.Run(name, func(t *testing.T) {
			if err := Verify(root, s); err == nil {
				t.Fatalf("предмет %s принят", s)
			}
		})
	}
}

func TestCovers(t *testing.T) {
	whole := Subject{Path: "RULES.core.md"}
	part := Subject{Path: "RULES.core.md", Section: "Мимикрия"}
	cases := []struct {
		name          string
		s             Subject
		file, section string
		want          bool
	}{
		{"файл целиком покрывает любой раздел", whole, "RULES.core.md", "Символы", true},
		{"раздел покрывает свой", part, "RULES.core.md", "Мимикрия", true},
		{"раздел не покрывает соседний", part, "RULES.core.md", "Символы", false},
		{"отбор по файлу берёт предмет с разделом", part, "RULES.core.md", "", true},
		{"чужой файл", part, "RULES.md", "Мимикрия", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Covers(c.s, c.file, c.section); got != c.want {
				t.Fatalf("покрытие %v, ждал %v", got, c.want)
			}
		})
	}
	if !CoversAny([]Subject{whole, part}, "RULES.core.md", "Символы") {
		t.Fatal("список предметов не покрыл файл целиком")
	}
}

func TestAgentFile(t *testing.T) {
	yes := []string{"RULES.core.md", "RULES.board.md", "TASKFORM.md", "RANKING.md", "ACCEPTANCE.md",
		"kit/skills/prose/SKILL.md", "kit/agents/exec-medium.md"}
	for _, p := range yes {
		if !AgentFile(p) {
			t.Errorf("%s должен считаться агентским", p)
		}
	}
	no := []string{"docs/lld/DK-805-prompt-stand.md", "tools/obeycheck/main.go", "README.md",
		"internal/obey/obey.go", "docs/tasks/DK-836.md"}
	for _, p := range no {
		if AgentFile(p) {
			t.Errorf("%s агентским не считается", p)
		}
	}
}

// Отпечаток снят с тела раздела у предмета с разделом и с файла целиком у
// предмета без него: правка соседнего раздела след прогона не портит, а правка
// самого предмета делает отпечаток устаревшим.
func TestPrint(t *testing.T) {
	root := tree(t)
	part := []Subject{{Path: "RULES.core.md", Section: "Мимикрия"}}
	whole := []Subject{{Path: "RULES.core.md"}}
	before, err := Print(root, part)
	if err != nil {
		t.Fatal(err)
	}
	beforeWhole, err := Print(root, whole)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(doc, "Писать только теми символами", "Писать теми символами", 1)
	if err := os.WriteFile(filepath.Join(root, "RULES.core.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Print(root, part)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("правка соседнего раздела сменила отпечаток: %s -> %s", before, after)
	}
	afterWhole, err := Print(root, whole)
	if err != nil {
		t.Fatal(err)
	}
	if afterWhole == beforeWhole {
		t.Fatal("правка файла отпечаток файла целиком не сменила")
	}
	// Перевёрстка без правки текста отпечаток не трогает: хвостовые пробелы и
	// пустые строки нормализация снимает.
	reflowed := strings.Replace(edited, "## Мимикрия\n", "## Мимикрия   \n\n", 1)
	if err := os.WriteFile(filepath.Join(root, "RULES.core.md"), []byte(reflowed), 0o644); err != nil {
		t.Fatal(err)
	}
	same, err := Print(root, part)
	if err != nil {
		t.Fatal(err)
	}
	if same != after {
		t.Fatalf("перевёрстка сменила отпечаток: %s -> %s", after, same)
	}
}

func TestSectionBody(t *testing.T) {
	body, ok := SectionBody(doc, "Мимикрия")
	if !ok {
		t.Fatal("раздел не найден")
	}
	joined := strings.Join(body, "\n")
	if !strings.Contains(joined, "неотличим") || !strings.Contains(joined, "Хвост раздела") {
		t.Fatalf("тело раздела: %q", joined)
	}
	if strings.Contains(joined, "Писать только") {
		t.Fatalf("в тело раздела уехал соседний: %q", joined)
	}
	if !strings.Contains(joined, "строка примера") {
		t.Fatalf("ограждённый блок раздела потерялся: %q", joined)
	}
}

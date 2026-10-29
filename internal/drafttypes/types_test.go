package drafttypes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLayersAndValidation(t *testing.T) {
	root := t.TempDir()
	kit, project, skills := filepath.Join(root, "kit"), filepath.Join(root, "project"), filepath.Join(root, "skills")
	write(t, filepath.Join(skills, "board-groom", "SKILL.md"), "skill")
	def := func(name, title, required string) string {
		return "name = \"" + name + "\"\ntitle = \"" + title + "\"\nversion = 1\nskill = \"board-groom\"\ndefault_type = \"task\"\nrequired = [" + required + "]\n"
	}
	write(t, filepath.Join(kit, "task.toml"), def("task", "Встроенный", `"title", "body"`))
	write(t, filepath.Join(project, "task.toml"), def("task", "Проектный", `"due"`))
	write(t, filepath.Join(project, "extra.toml"), def("extra", "Другой", `"title"`))
	set, err := Load(kit, project, skills)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Names) != 2 || set.Types["task"].Title != "Проектный" || len(set.Types["task"].Required) != 1 || set.Types["task"].Required[0] != "due" {
		t.Fatalf("set: %+v", set)
	}
	write(t, filepath.Join(project, "task.toml"), "")
	set, err = Load(kit, project, skills)
	if err != nil || set.Types["task"].Title != "Встроенный" || len(set.Warnings) != 1 {
		t.Fatalf("empty override: %+v %v", set, err)
	}
	for _, invalid := range []string{def("task", "Ошибка", `"alien"`), def("task", "Ошибка", `"title"`) + "unknown = 1\n", strings.Replace(def("task", "Ошибка", `"title"`), "board-groom", "missing", 1)} {
		path := filepath.Join(project, "task.toml")
		write(t, path, invalid)
		if _, err = Load(kit, project, skills); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("invalid config: %v", err)
		}
	}
}

func TestBuiltins(t *testing.T) {
	set, err := Load("../../kit/draft-types", "", "../../kit/skills")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(set.Names, ",") != "agreement,control,milestone,task" {
		t.Fatalf("names %v", set.Names)
	}
}

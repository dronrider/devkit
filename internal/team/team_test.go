package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAbsentIsEmpty(t *testing.T) {
	list, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("без файла команда не пустая: %v", list)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	home := t.TempDir()
	in := []Participant{
		{Handle: "rider", Kind: Human, Name: "Райдер", Roles: []string{RoleExec, RoleInspect}, Channel: "telegram:@rider"},
		{Handle: "glm53", Kind: Agent, Model: "glm-5.3", Tier: "pro", Roles: []string{RoleExec, RoleReview, RoleCheck}},
	}
	if err := Save(home, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("участников %d, жду 2", len(out))
	}
	if out[0].Handle != "rider" || out[0].Kind != Human || out[0].Channel != "telegram:@rider" {
		t.Fatalf("человек не пережил круг: %+v", out[0])
	}
	if !out[0].HasRole(RoleInspect) {
		t.Fatal("роль инспектора не читается")
	}
	if out[1].Model != "glm-5.3" || out[1].Tier != "pro" {
		t.Fatalf("агент не пережил круг: %+v", out[1])
	}
}

func TestLoadRejectsBadParticipant(t *testing.T) {
	home := t.TempDir()
	cases := map[string]string{
		"вид мимо словаря":     "[x]\nkind = \"robot\"\nroles = [\"exec\"]\n",
		"агент без яруса":      "[x]\nkind = \"agent\"\nmodel = \"m\"\nroles = [\"exec\"]\n",
		"агент с чужим ярусом": "[x]\nkind = \"agent\"\nmodel = \"m\"\ntier = \"mega\"\nroles = [\"exec\"]\n",
		"человек с ярусом":     "[x]\nkind = \"human\"\ntier = \"pro\"\nroles = [\"exec\"]\n",
		"ролей нет":            "[x]\nkind = \"human\"\n",
		"роль мимо словаря":    "[x]\nkind = \"human\"\nroles = [\"dev\"]\n",
	}
	for name, text := range cases {
		if err := os.MkdirAll(filepath.Dir(Path(home)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(Path(home), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(home); err == nil {
			t.Fatalf("%s должен был отбиться", name)
		}
	}
}

func TestRenderNamesWriterAndSensitivity(t *testing.T) {
	text := Render([]Participant{{Handle: "a", Kind: Human, Roles: []string{RoleExec}}})
	if !strings.Contains(text, "Пишет taskctl team") || !strings.Contains(text, "в гит не идёт") {
		t.Fatalf("шапка не называет писателя и границу гита:\n%s", text)
	}
}

func TestTitleHandle(t *testing.T) {
	cases := map[string]string{
		"Хвост [срок: 2026-11-05]":               "",
		"Хвост [исп: glm-53] [срок: 2026-11-05]": "glm-53",
		"Хвост [исп: rider]":                     "rider",
		"[исп: a_b-2]":                           "a_b-2",
		"Хвост [исп: Райдер]":                    "",
	}
	for title, want := range cases {
		if got := TitleHandle(title); got != want {
			t.Fatalf("TitleHandle(%q) = %q, жду %q", title, got, want)
		}
	}
}

func TestPathLivesNearUserPage(t *testing.T) {
	if got := Path("/Users/x"); got != filepath.Join("/Users/x", ".devkit", "team.toml") {
		t.Fatalf("путь команды: %q", got)
	}
}

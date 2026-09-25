package clientdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceMakesDirUnderHome(t *testing.T) {
	home := t.TempDir()
	dir, err := Service(home)
	if err != nil {
		t.Fatalf("служебный каталог не заведён: %v", err)
	}
	if want := filepath.Join(home, Base, Name); dir != want {
		t.Fatalf("каталог %s, ждали %s", dir, want)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("каталога на диске нет: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("каталог не прочитан: %v", err)
	}
	if len(ents) != 0 {
		t.Fatalf("служебный каталог не пуст (%d записей): обходить в нём нечего быть не должно", len(ents))
	}
}

func TestServiceIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := Service(home); err != nil {
		t.Fatalf("первый зов отказал: %v", err)
	}
	if _, err := Service(home); err != nil {
		t.Fatalf("повторный зов отказал: %v", err)
	}
}

func TestCheckRefusesEmptyDir(t *testing.T) {
	err := Check("", "/Users/кто-то")
	if err == nil {
		t.Fatal("пустой каталог подъёма прошёл проверку")
	}
	if !strings.Contains(err.Error(), "не назван") {
		t.Fatalf("причина отказа не названа: %v", err)
	}
}

func TestCheckRefusesHome(t *testing.T) {
	home := t.TempDir()
	if err := Check(home, home); err == nil {
		t.Fatal("дом прошёл проверку каталогом подъёма")
	}
	// Хвостовой разделитель и точка в пути это тот же каталог, и проверка
	// обязана узнать его, иначе обход дома вернулся бы через написание пути.
	if err := Check(home+string(filepath.Separator)+".", home); err == nil {
		t.Fatal("дом, записанный через точку, прошёл проверку")
	}
}

func TestCheckAcceptsProjectTree(t *testing.T) {
	home := t.TempDir()
	if err := Check(filepath.Join(home, "projects", "devkit"), home); err != nil {
		t.Fatalf("дерево проекта под домом не прошло: %v", err)
	}
	if err := Check(Path(home), home); err != nil {
		t.Fatalf("служебный каталог не прошёл: %v", err)
	}
}

func TestPickSwapsHomeAndEmpty(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, Base, Name)
	for _, dir := range []string{"", home} {
		got, err := Pick(dir, home)
		if err != nil {
			t.Fatalf("выбор для %q отказал: %v", dir, err)
		}
		if got != want {
			t.Fatalf("для %q выбран %s, ждали служебный %s", dir, got, want)
		}
	}
}

func TestPickKeepsNamedTree(t *testing.T) {
	home := t.TempDir()
	tree := filepath.Join(home, "projects", "devkit")
	got, err := Pick(tree, home)
	if err != nil {
		t.Fatalf("выбор отказал: %v", err)
	}
	if got != tree {
		t.Fatalf("выбран %s, ждали названное дерево %s", got, tree)
	}
}

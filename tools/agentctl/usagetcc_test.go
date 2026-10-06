package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapUsagePanelNamesDir: съёмщик поднимает клиента с явным «-c», и каталог
// этот не дом. Без «-c» клиент брал рабочий каталог agentctl, под launchd это
// корень файловой системы, и обход дерева упирался в защищённые папки macOS
// диалогами доступа с именем службы (DK-1163). tmux тут подменён скриптом: ему
// довольно записать доводы подъёма и отказать, дальше съёмщик уходит сам.
func TestSnapUsagePanelNamesDir(t *testing.T) {
	bin := t.TempDir()
	raise := filepath.Join(t.TempDir(), "raise")
	script := "#!/bin/sh\nif [ \"$1\" = new-session ]; then printf '%s\\n' \"$*\" >" + raise + "; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := t.TempDir()
	q := specAt(t, filepath.Join(home, ".devkit", "quota", "claude-code.local"))
	q.Home = home

	if _, err := snapUsagePanel(q, testNow); err == nil {
		t.Fatal("стенд оборвал подъём, а съёмщик отказа не вернул")
	}
	raw, err := os.ReadFile(raise)
	if err != nil {
		t.Fatalf("доводы подъёма не записались: %v", err)
	}
	args := strings.Fields(strings.TrimSpace(string(raw)))
	dir := ""
	for i, a := range args {
		if a == "-c" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	if dir == "" {
		t.Fatalf("клиент поднят без каталога: %v", args)
	}
	if err := clientdirCheck(dir, home); err != nil {
		t.Fatalf("каталог подъёма %q не годится: %v", dir, err)
	}
}

// TestSnapUsagePanelRaisesClientPath: клиент поднимается абсолютным путём,
// разрешённым в процессе съёмщика. Сессию tmux стартует логин-оболочка, её
// профиль дописывает свои префиксы PATH вперёд всего остального, и клиент по
// имени доставался профилю: на машине с двумя claude съём шёл не тем,
// которого выбрал съёмщик, а стенд не мог удержать подложного клиента
// (DK-1307). tmux тут подменён скриптом, как и в соседнем тесте: ему довольно
// записать доводы подъёма и отказать, дальше съёмщик уходит сам.
func TestSnapUsagePanelRaisesClientPath(t *testing.T) {
	bin := t.TempDir()
	raise := filepath.Join(t.TempDir(), "raise")
	script := "#!/bin/sh\nif [ \"$1\" = new-session ]; then printf '%s\\n' \"$*\" >" + raise + "; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := t.TempDir()
	q := specAt(t, filepath.Join(home, ".devkit", "quota", "claude-code.local"))
	q.Home = home

	if _, err := snapUsagePanel(q, testNow); err == nil {
		t.Fatal("стенд оборвал подъём, а съёмщик отказа не вернул")
	}
	raw, err := os.ReadFile(raise)
	if err != nil {
		t.Fatalf("доводы подъёма не записались: %v", err)
	}
	args := strings.Fields(strings.TrimSpace(string(raw)))
	if len(args) == 0 {
		t.Fatal("доводы подъёма пусты")
	}
	if got, want := args[len(args)-1], filepath.Join(bin, "claude"); got != want {
		t.Fatalf("клиент поднят как %q вместо абсолютного пути %q: путь выбирает профиль логин-оболочки сессии, а не съёмщик", got, want)
	}
}

// TestSnapUsagePanelRaisesClientPathRelative: LookPath с относительной
// компонентой PATH возвращает относительный путь, а сессия стартует с каталогом
// «-c» отличным от cwd съёмщика, и относительный клиент там не находился бы.
// Подложный claude лежит в подкаталоге временного каталога, PATH начинается
// относительной компонентой, cwd теста стоит на родителе, а подложный tmux
// прописан абсолютной компонентой следом: довод подъёма обязан выйти абсолютом.
func TestSnapUsagePanelRaisesClientPathRelative(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	raise := filepath.Join(t.TempDir(), "raise")
	script := "#!/bin/sh\nif [ \"$1\" = new-session ]; then printf '%s\\n' \"$*\" >" + raise + "; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	absBin := filepath.Join(root, "absbin")
	if err := os.Mkdir(absBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(absBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "bin"+string(os.PathListSeparator)+absBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := t.TempDir()
	q := specAt(t, filepath.Join(home, ".devkit", "quota", "claude-code.local"))
	q.Home = home
	// Каталог теста меняется уже после specAt: профили ищутся от repoRoot, а тот
	// стоит на cwd пакета, и временный каталог сбил бы поиск до старта.
	t.Chdir(root)

	_, snapErr := snapUsagePanel(q, testNow)
	raw, err := os.ReadFile(raise)
	if err != nil {
		t.Fatalf("доводы подъёма не записались, отказ съёмщика %q: %v", snapErr, err)
	}
	if snapErr == nil {
		t.Fatal("стенд оборвал подъём, а съёмщик отказа не вернул")
	}
	args := strings.Fields(strings.TrimSpace(string(raw)))
	if len(args) == 0 {
		t.Fatal("доводы подъёма пусты")
	}
	got := args[len(args)-1]
	if !filepath.IsAbs(got) {
		t.Fatalf("клиент поднят как %q: путь относителен, а сессия стартует с каталогом «-c» отличным от cwd съёмщика, клиента там нет", got)
	}
	if want := filepath.Join(root, "bin", "claude"); got != want {
		t.Fatalf("клиент поднят как %q вместо %q: относительная компонента PATH увела выбор мимо подложного bin", got, want)
	}
}

// clientdirCheck это та же проверка каталога, что в internal/clientdir, своими
// словами: стенд обязан судить о каталоге сам, иначе правка и её мерка
// съезжали бы вместе.
func clientdirCheck(dir, home string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("каталог подъёма клиента не назван")
	}
	if filepath.Clean(dir) == filepath.Clean(home) {
		return errors.New("каталог подъёма клиента это дом " + home)
	}
	return nil
}

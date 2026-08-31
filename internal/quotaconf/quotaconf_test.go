package quotaconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, home, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStaleAge(t *testing.T) {
	t.Run("без файла умолчание", func(t *testing.T) {
		d, err := StaleAge(t.TempDir())
		if err != nil || d != Default {
			t.Fatalf("жду %v без ошибки, получил %v, %v", Default, d, err)
		}
	})

	t.Run("без строки stale умолчание", func(t *testing.T) {
		home := t.TempDir()
		write(t, home, "# комментарий\nдругое = 7\n")
		d, err := StaleAge(home)
		if err != nil || d != Default {
			t.Fatalf("жду %v без ошибки, получил %v, %v", Default, d, err)
		}
	})

	t.Run("порог из файла", func(t *testing.T) {
		home := t.TempDir()
		write(t, home, "stale = 90\n")
		d, err := StaleAge(home)
		if err != nil || d != 90*time.Minute {
			t.Fatalf("жду 90м без ошибки, получил %v, %v", d, err)
		}
	})

	// Кривое значение это отказ, а не молчаливое умолчание: порог, тихо
	// съехавший на 45 минут из-за опечатки, не виден ничем.
	for _, val := range []string{"сорок", "0", "-5", "1.5"} {
		t.Run("кривое значение "+val, func(t *testing.T) {
			home := t.TempDir()
			write(t, home, "stale = "+val+"\n")
			_, err := StaleAge(home)
			if err == nil {
				t.Fatalf("stale = %q прошло молча", val)
			}
			for _, part := range []string{"quota.local", "минут"} {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("в причине нет %q: %v", part, err)
				}
			}
		})
	}
}

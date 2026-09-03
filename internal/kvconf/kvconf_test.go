package kvconf

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConf(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conf")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadPairs: комментарии и пустые строки уходят, пробелы вокруг ключа и
// значения обрезаются, порядок пар сохраняется.
func TestReadPairs(t *testing.T) {
	path := writeConf(t, "# заметка\n\n  publish = auto \npause = 20-60\nпроза без знака равенства\npublish = confirm\n")
	pairs, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Pair{{"publish", "auto"}, {"pause", "20-60"}, {"publish", "confirm"}}
	if len(pairs) != len(want) {
		t.Fatalf("пар %d, ждём %d: %v", len(pairs), len(want), pairs)
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Fatalf("пара %d = %v, ждём %v", i, pairs[i], want[i])
		}
	}
}

// TestReadMissing: конфига может не быть вовсе, и это не отказ.
func TestReadMissing(t *testing.T) {
	pairs, err := Read(filepath.Join(t.TempDir(), "нет.conf"))
	if err != nil || pairs != nil {
		t.Fatalf("отсутствие файла: pairs=%v err=%v", pairs, err)
	}
}

// TestUnquote: снимается ровно внешняя пара кавычек.
func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"ssh host 'restart'"`: "ssh host 'restart'",
		`'20-60'`:              "20-60",
		`ssh host 'restart'`:   `ssh host 'restart'`,
		`"`:                    `"`,
	}
	for in, want := range cases {
		if got := Unquote(in); got != want {
			t.Fatalf("Unquote(%q) = %q, ждём %q", in, got, want)
		}
	}
}

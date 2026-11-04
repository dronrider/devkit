package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTmuxResidentEqualTimeFollowsJournalOrder(t *testing.T) {
	stamp := time.Date(2026, 11, 4, 23, 14, 14, 0, time.UTC)
	birth := func(sid, transcript string, at time.Time) string {
		return Line(at, sid, Bind{Task: "XR-903", Source: ByOrder,
			Tmux: "task-XR-903", Transcript: transcript}, "startup")
	}
	for _, tc := range []struct {
		name, log, want string
	}{
		{"A затем B", birth("zz-old", "/p/a.jsonl", stamp) + birth("aa-new", "/p/b.jsonl", stamp), "aa-new"},
		{"обратный порядок SID", birth("aa-new", "/p/b.jsonl", stamp) + birth("zz-old", "/p/a.jsonl", stamp), "zz-old"},
		{"незачатый заказ", birth("zz-old", "/p/a.jsonl", stamp) + birth("aa-new", "", stamp), "zz-old"},
		{"клиент после заказа", birth("zz-old", "/p/a.jsonl", stamp) + birth("aa-new", "", stamp) + birth("aa-new", "/p/b.jsonl", stamp), "aa-new"},
		{"разное время", birth("zz-old", "/p/a.jsonl", stamp.Add(time.Second)) + birth("aa-new", "/p/b.jsonl", stamp), "zz-old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(Path(home)), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(Path(home), []byte(tc.log), 0644); err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 256; n++ {
				if got := TmuxResident(LoadAll(home), "task-XR-903"); got != tc.want {
					t.Fatalf("чтение %d: жилец %q, ждал %q", n, got, tc.want)
				}
			}
		})
	}
}

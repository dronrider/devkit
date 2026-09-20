package spend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stampedLine это запись хода ассистента с моментом, как её пишет харнес.
func stampedLine(req, stamp string, out int) string {
	return `{"type":"assistant","requestId":"` + req + `","timestamp":"` + stamp +
		`","message":{"usage":{"output_tokens":` + itoa(out) + `,"input_tokens":10,` +
		`"cache_read_input_tokens":100,"cache_creation_input_tokens":0}}}` + "\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestReadTurnsStamps: ходы приезжают с моментами и в местной зоне, а повтор
// записи с тем же requestId считается один раз: без склейки сессия выросла бы в
// разы (DK-913, делёж хода головной сессии по времени).
func TestReadTurnsStamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "head.jsonl")
	body := stampedLine("req-1", "2026-09-18T07:00:00Z", 100) +
		stampedLine("req-1", "2026-09-18T07:00:00Z", 100) +
		`{"type":"user","message":{"content":"реплика"}}` + "\n" +
		stampedLine("req-2", "2026-09-18T08:30:00Z", 250)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	turns, err := ReadTurns(path)
	if err != nil {
		t.Fatalf("ReadTurns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("ходов %d, ждал 2: %+v", len(turns), turns)
	}
	want := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	if !turns[0].At.Equal(want) {
		t.Fatalf("момент первого хода %s, ждал %s", turns[0].At, want)
	}
	if turns[0].At.Location() != time.Local {
		t.Fatalf("момент отдан не в местной зоне: %s", turns[0].At.Location())
	}
	if turns[1].Usage.Output != 250 || turns[1].Usage.Turns != 1 {
		t.Fatalf("расход второго хода %+v", turns[1].Usage)
	}
}

// TestStreamsSplitsHeadsAndWorks: перечень потоков разделяет головные сессии и
// работы субагентов, а файл, не тронутый с начала среза, не читается вовсе:
// журналов на машине полтора гигабайта.
func TestStreamsSplitsHeadsAndWorks(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(Dir(home), "slug")
	kids := filepath.Join(proj, "sess-1", "subagents")
	if err := os.MkdirAll(kids, 0o755); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(proj, "sess-1.jsonl")
	work := filepath.Join(kids, "agent-a1b2c3.jsonl")
	old := filepath.Join(proj, "sess-old.jsonl")
	for _, p := range []string{head, work, old} {
		if err := os.WriteFile(p, []byte(stampedLine("req-"+filepath.Base(p), "2026-09-18T07:00:00Z", 10)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Date(2026, 8, 1, 10, 0, 0, 0, time.Local)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	got := Streams(home, time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local))
	if len(got) != 2 {
		t.Fatalf("потоков %d, ждал 2: %+v", len(got), got)
	}
	for _, s := range got {
		switch {
		case strings.HasSuffix(s.Path, "sess-1.jsonl"):
			if s.Session != "sess-1" || s.Work != "" {
				t.Fatalf("головной поток разобран как %+v", s)
			}
		case strings.HasSuffix(s.Path, "agent-a1b2c3.jsonl"):
			if s.Work != "a1b2c3" || s.Session != "" {
				t.Fatalf("поток работы разобран как %+v", s)
			}
		default:
			t.Fatalf("в перечень попал лишний поток %s", s.Path)
		}
	}
}

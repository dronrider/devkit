package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/spend"
	"github.com/dronrider/devkit/internal/taskform"
)

// standTurn это ход ассистента во временном доме прогона.
func standTurn(req string, out int) string {
	return `{"type":"assistant","requestId":"` + req + `","timestamp":"2026-09-18T10:00:00Z",` +
		`"message":{"usage":{"output_tokens":` + itoa(out) + `,"input_tokens":5,` +
		`"cache_read_input_tokens":50,"cache_creation_input_tokens":0}}}` + "\n"
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

// TestHomeUsageSumsStandSessions: расход временного дома складывается по всем
// его сессиям, головным и субагентским, пока дом цел: по концу прогона его
// сносят вместе с журналами, и своду читать после этого нечего (DK-913).
func TestHomeUsageSumsStandSessions(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(spend.Dir(home), "slug")
	kids := filepath.Join(proj, "sess-1", "subagents")
	if err := os.MkdirAll(kids, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "sess-1.jsonl"), []byte(standTurn("req-1", 300)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kids, "agent-a1b2.jsonl"), []byte(standTurn("req-2", 700)), 0o644); err != nil {
		t.Fatal(err)
	}
	u := homeUsage(home)
	if u.Turns != 2 || u.Output != 1000 || u.Input != 10 || u.CacheRead != 100 {
		t.Fatalf("расход дома прогона %+v", u)
	}
	if homeUsage(t.TempDir()).Turns != 0 {
		t.Fatal("пустой дом дал ходы")
	}
}

// TestTaskNoteCarriesTokens: числа прогона уезжают в отметку раздела
// «Проверка», и свод читает их оттуда статьёй «стенд».
func TestTaskNoteCarriesTokens(t *testing.T) {
	p := taskDoc(t)
	n := standNote(t, scenarios(t, "press"), baseOld)
	n.Usage = spend.Usage{Turns: 120, Output: 45210, Input: 3100, CacheRead: 2100000}
	if err := n.write(p); err != nil {
		t.Fatal(err)
	}
	marks := taskform.StandMarks(read(t, p))
	if len(marks) != 1 {
		t.Fatalf("отметок %d", len(marks))
	}
	m := marks[0]
	if m.Turns != 120 || m.Output != 45210 || m.Input != 3100 || m.CacheRead != 2100000 {
		t.Fatalf("числа отметки %+v", m)
	}
}

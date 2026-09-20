package peers

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// deadPID это pid только что кончившегося процесса: запись реестра с ним
// подделывает клиента, упавшего посреди хода.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func write(t *testing.T, home, name string, p Peer) {
	t.Helper()
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(home), name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDropsDeadAndDedupes(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 10, 15, 30, 0, 0, time.Local)
	write(t, home, "1.json", Peer{PID: os.Getpid(), SessionID: "live", Status: "busy", Updated: now.UnixMilli()})
	write(t, home, "2.json", Peer{PID: deadPID(t), SessionID: "dead", Status: "busy", Updated: now.UnixMilli()})
	// Та же сессия дважды: перезапуск клиента с тем же ID, свежая запись
	// выигрывает.
	write(t, home, "3.json", Peer{PID: os.Getpid(), SessionID: "live", Status: "idle", Updated: now.Add(-time.Hour).UnixMilli()})
	write(t, home, "junk.txt", Peer{PID: os.Getpid(), SessionID: "junk"})

	alive := Load(home, true)
	if len(alive) != 1 || alive["live"].Status != "busy" {
		t.Fatalf("живые записи: %+v", alive)
	}
	if want := filepath.Join(SockDir, strconv.Itoa(os.Getpid())+".sock"); alive["live"].Sock != want {
		t.Fatalf("путь сокета %q, жду %q", alive["live"].Sock, want)
	}
	all := Load(home, false)
	if len(all) != 2 || all["dead"].Alive() {
		t.Fatalf("все записи: %+v", all)
	}
	if Load(filepath.Join(home, "нет"), false) == nil {
		t.Fatal("без каталога жду пустую карту, а не nil")
	}
}

func TestFreshAndTurnAge(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 30, 0, 0, time.Local)
	p := Peer{Status: "busy", Updated: now.Add(-IdleAfter).UnixMilli(), StatusAt: now.Add(-5 * time.Minute).UnixMilli()}
	if !p.Fresh(now) {
		t.Fatal("касание ровно на рубеже это ещё свежая запись")
	}
	p.Updated = now.Add(-IdleAfter - time.Second).UnixMilli()
	if p.Fresh(now) {
		t.Fatal("касание старше рубежа выдано за свежее")
	}
	if age, ok := p.TurnAge(now); !ok || age != 5*time.Minute {
		t.Fatalf("возраст хода %v, %v", age, ok)
	}
	p.Status = "idle"
	if _, ok := p.TurnAge(now); ok {
		t.Fatal("у простаивающей сессии хода нет")
	}
	if (Peer{}).Fresh(now) {
		t.Fatal("запись без времени свежей не считается")
	}
}

func TestJudge(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 30, 0, 0, time.Local)
	me := os.Getpid()
	all := map[string]Peer{
		"fresh": {PID: me, SessionID: "fresh", Updated: now.Add(-time.Minute).UnixMilli()},
		"quiet": {PID: me, SessionID: "quiet", Updated: now.Add(-25 * time.Minute).UnixMilli()},
		"dead":  {PID: deadPID(t), SessionID: "dead", Updated: now.UnixMilli()},
		"blank": {PID: me, SessionID: "blank"},
	}
	cases := []struct {
		name    string
		sids    []string
		want    State
		silence time.Duration
		session string
	}{
		{"живая сессия", []string{"fresh"}, Alive, 0, "fresh"},
		{"молчит дольше рубежа", []string{"quiet"}, Silent, 25 * time.Minute, "quiet"},
		{"процесс мёртв", []string{"dead"}, Gone, 0, ""},
		{"записи нет", []string{"unknown"}, Gone, 0, ""},
		{"сессий не названо", nil, Gone, 0, ""},
		{"судит самая свежая из живых", []string{"quiet", "fresh", "dead"}, Alive, 0, "fresh"},
		{"мёртвая свежее живой молчащей", []string{"dead", "quiet"}, Silent, 25 * time.Minute, "quiet"},
		{"без времени касания живая", []string{"blank"}, Alive, 0, "blank"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Judge(all, c.sids, now)
			if got.State != c.want || got.Silence != c.silence || got.Session != c.session {
				t.Fatalf("Judge = %+v, жду %s/%v/%s", got, c.want, c.silence, c.session)
			}
		})
	}
}

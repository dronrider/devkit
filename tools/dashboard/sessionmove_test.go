package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Правила переезда перевода разговора (DK-1455) без сервера: свой корень не
// трогается, коллизия с копией ручного обхода решается свежестью, и сайдкаров
// у разговора может не быть.
func TestTranslateSessionRules(t *testing.T) {
	base := t.TempDir()
	srcRoot := filepath.Join(base, "writer", "projects")
	dstRoot := filepath.Join(base, "target", "projects")
	proj := "/tmp/demo"
	sid := "a1455-1"
	now := time.Now()

	t.Run("свой корень", func(t *testing.T) {
		path := writeSessionAt(t, srcRoot, proj, "", sid, plainTalk, now)
		note, err := translateSession(sessionInfo{path: path, root: srcRoot, ID: sid}, srcRoot)
		if err != nil || note != "" {
			t.Fatalf("свой корень дал перевод: %q %v", note, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("свой корень тронут: %v", err)
		}
	})

	t.Run("пустой разговор", func(t *testing.T) {
		note, err := translateSession(sessionInfo{root: srcRoot, ID: sid}, dstRoot)
		if err != nil || note != "" {
			t.Fatalf("пустой разговор дал перевод: %q %v", note, err)
		}
		if _, err := os.Stat(dstRoot); !os.IsNotExist(err) {
			t.Fatalf("пустой разговор наследил в цели: %v", err)
		}
	})

	t.Run("переезд без сайдкаров", func(t *testing.T) {
		path := writeSessionAt(t, srcRoot, proj, "", sid, plainTalk, now)
		note, err := translateSession(sessionInfo{path: path, root: srcRoot, ID: sid}, dstRoot)
		if err != nil || note == "" {
			t.Fatalf("переезд не отчитался: %q %v", note, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("оригинал остался: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(dstRoot, claudeDirName(proj), sid+".jsonl")); err != nil ||
			!strings.Contains(string(data), "работа идёт") {
			t.Fatalf("цель не получила разговор: %v %s", err, data)
		}
	})

	t.Run("дубль в цели устарел", func(t *testing.T) {
		path := writeSessionAt(t, srcRoot, proj, "", sid, plainTalk, now)
		writeSessionAt(t, dstRoot, proj, "", sid, plainTalk, now.Add(-time.Hour))
		if _, err := translateSession(sessionInfo{path: path, root: srcRoot, ID: sid}, dstRoot); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(filepath.Join(dstRoot, claudeDirName(proj), sid+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.ModTime().Equal(now) {
			t.Fatalf("устаревшая копия пережила свежий разговор: %v", fi.ModTime())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("оригинал свежей стороны остался: %v", err)
		}
	})

	t.Run("цель свежее источника", func(t *testing.T) {
		path := writeSessionAt(t, srcRoot, proj, "", sid, plainTalk, now.Add(-time.Hour))
		fresh := writeSessionAt(t, dstRoot, proj, "", sid, plainTalk, now)
		note, err := translateSession(sessionInfo{path: path, root: srcRoot, ID: sid}, dstRoot)
		if err != nil || note == "" {
			t.Fatalf("снятие дубля не отчиталось: %q %v", note, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("дубль-источник остался: %v", err)
		}
		if fi, err := os.Stat(fresh); err != nil || !fi.ModTime().Equal(now) {
			t.Fatalf("свежая цель тронута: %v", err)
		}
	})
}

// Правила смены умершему переведённому резюму (DK-1455): порог мгновенной
// смерти, снятие признака самой смертью и чтение реплики человека из журнала
// только свежей.
func TestMovedDeathRules(t *testing.T) {
	t.Run("медленная смерть смену не поднимает", func(t *testing.T) {
		e, _, tmuxLog, _ := moveEnv(t)
		sid := "slow1455-1111-4111-8111-111111111111"
		st := e.s.chatDeathSay("chat-77", chatStore{From: sid, Project: "demo",
			Raised: time.Now().Add(-2 * time.Minute).Unix(), Moved: movedRoadSay})
		if st.Dead == 0 {
			t.Fatal("медленная смерть не записалась")
		}
		if got := newSessionLines(t, tmuxLog); len(got) != 0 {
			t.Fatalf("проживший резюм поднял смену: %q", got)
		}
	})

	t.Run("признак снят смертью", func(t *testing.T) {
		e, _, _, _ := moveEnv(t)
		sid := "gone1455-1111-4111-8111-111111111111"
		e.s.chatDeathSay("chat-78", chatStore{From: sid, Project: "demo",
			Raised: time.Now().Unix(), Moved: movedRoadSay})
		if st := e.s.chatStoreRead("tmux-chat-78"); st.Moved != "" {
			t.Fatalf("смерть оставила признак перевода: %q", st.Moved)
		}
	})

	t.Run("вооружение без подъёма не пишет", func(t *testing.T) {
		e, _, _, _ := moveEnv(t)
		e.s.chatMovedArm("chat-79", movedRoadSay)
		if st := e.s.chatStoreRead("tmux-chat-79"); st.Moved != "" || st.Raised != 0 {
			t.Fatalf("вооружение завело запись из ничего: %+v", st)
		}
	})

	t.Run("новый жилец имени снимает признак", func(t *testing.T) {
		e, _, _, _ := moveEnv(t)
		sid := "next1455-1111-4111-8111-111111111111"
		if err := e.s.chatStoreWrite("tmux-chat-80", chatStore{From: sid,
			Raised: time.Now().Unix(), Moved: movedRoadSay}); err != nil {
			t.Fatal(err)
		}
		e.s.chatRaised("chat-80", sid, "", "demo")
		if st := e.s.chatStoreRead("tmux-chat-80"); st.Moved != "" {
			t.Fatalf("новый жилец имени унаследовал признак перевода: %q", st.Moved)
		}
	})

	t.Run("свежая реплика читается, старая нет", func(t *testing.T) {
		e, _, _, _ := moveEnv(t)
		sid := "said1455-1111-4111-8111-111111111111"
		now := time.Now().UTC()
		put := func(text string, at time.Time) {
			t.Helper()
			if err := e.s.saidPut(saidSessionKey(sid), saidRec{Text: text, Time: at.Format(time.RFC3339)}); err != nil {
				t.Fatal(err)
			}
		}
		put("старые слова", now.Add(-time.Hour))
		put("продолжай с места обрыва", now)
		if err := e.s.saidPut(saidSessionKey(sid), saidRec{Kind: saidKindMark, Text: "разделитель"}); err != nil {
			t.Fatal(err)
		}
		text, ok := lastUserSay(e.home, sid, now)
		if !ok || text != "продолжай с места обрыва" {
			t.Fatalf("свежая реплика не прочитана: %q %v", text, ok)
		}
		if _, ok := lastUserSay(e.home, sid, now.Add(time.Minute)); ok {
			t.Fatal("старшая реплика воскресла в смену")
		}
		if _, ok := lastUserSay(e.home, "nosuch-1111-2222-3333", now); ok {
			t.Fatal("пустой журнал отдал реплику")
		}
	})
}

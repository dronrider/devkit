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

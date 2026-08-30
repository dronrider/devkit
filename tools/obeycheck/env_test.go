package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Раскладка скиллов отсеивает служебное так же, как боевая раскладка машины
// (skill_files() в tools/devkitctl/devkitctl.py): __pycache__ и точечные файлы
// заводит прогон тестов и файловый менеджер, а не автор скилла, и в дом
// прогона они уезжать не должны (DK-546).
func TestSkillLayoutSkipsNoise(t *testing.T) {
	src := t.TempDir()
	for _, d := range []string{
		filepath.Join(src, "board-task"),
		filepath.Join(src, "board-task", "__pycache__"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join("board-task", "SKILL.md"):             "тело скилла",
		filepath.Join("board-task", "__pycache__", "x.pyc"): "байткод прогона теста",
		filepath.Join("board-task", ".DS_Store"):            "файловый менеджер",
		"check-skills.py":                                   "самопроверка скиллов",
		"check_skills_test.py":                              "тест самопроверки",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(t.TempDir(), "skills")
	if err := copyTree(src, dst, skipSkillNoise); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "board-task", "SKILL.md")); err != nil {
		t.Fatalf("скилл не доехал: %v", err)
	}
	for _, bad := range []string{
		filepath.Join(dst, "board-task", "__pycache__"),
		filepath.Join(dst, "board-task", ".DS_Store"),
		filepath.Join(dst, "check-skills.py"),
		filepath.Join(dst, "check_skills_test.py"),
	} {
		if _, err := os.Stat(bad); err == nil {
			t.Fatalf("мусор доехал в дом прогона: %s", bad)
		}
	}
}

// fakeHome собирает дом пользователя со связкой ключей. Живой дом машины тестам
// не годится: связку они бы читали настоящую, а зелень зависела бы от того,
// залогинен ли пользователь.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	kc := filepath.Join(home, keychainRel)
	if err := os.MkdirAll(kc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kc, "login.keychain-db"), []byte("не ключи, метка теста"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func wantErr(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("ждал находку, а ошибки нет")
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("в находке нет %q:\n%s", p, err)
		}
	}
}

// Связки в доме пользователя нет: временному дому не на что ссылаться, и стенд
// говорит это прямо, вместе с командой починки.
func TestAuthMissingKeychain(t *testing.T) {
	wantErr(t, checkAuth(t.TempDir(), ""), "связки ключей", "нет", "/login", "--home-seed")
}

// Связка это ссылка в никуда: снаружи путь есть, а каталога за ним нет.
func TestAuthBrokenKeychainLink(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(home, "снесённая-связка")
	if err := os.Symlink(dead, filepath.Join(home, keychainRel)); err != nil {
		t.Fatal(err)
	}
	wantErr(t, checkAuth(home, ""), "ссылка в никуда", dead, "--home-seed")
}

// На месте связки файл: сослаться тоже не на что, и молчать об этом нельзя.
func TestAuthKeychainIsNotDir(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, keychainRel), []byte("файл вместо каталога"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr(t, checkAuth(home, ""), "не каталог", "--home-seed")
}

// Готовый дом задан руками, а каталога нет: находка называет флаг, которым это
// пришло, и говорит, что будет без него.
func TestAuthMissingHomeSeed(t *testing.T) {
	home := fakeHome(t)
	seed := filepath.Join(t.TempDir(), "нет-такого-дома")
	wantErr(t, checkAuth(home, seed), "затравки HOME", seed, "--home-seed", filepath.Join(home, keychainRel))
}

// Годность смотрится до раскладки: в рабочей директории после отказа не должно
// остаться ни одного прогона, а сессий не должно уйти ни одной.
func TestAuthCheckedBeforeLayout(t *testing.T) {
	p := params(t, scenarios(t, "press"), "full", "core")
	p.UserHome = t.TempDir()
	p.Work = t.TempDir()
	_, _, err := Run(p)
	wantErr(t, err, "связки ключей")
	left, err := os.ReadDir(p.Work)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("до раскладки дело дойти не должно, а в рабочей директории %d записей", len(left))
	}
}

// Временный дом получает ссылку на связку пользователя, а не копию ключей.
func TestKeychainLinkedIntoTempHome(t *testing.T) {
	home := fakeHome(t)
	e, err := makeEnv(filepath.Join(t.TempDir(), "run"), devkitRoot(t), layout("full"), "", home)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.Home, keychainRel)
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("связка во временном доме это %s, а ждали ссылку", fi.Mode())
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(home, keychainRel) {
		t.Fatalf("ссылка ведёт на %s", target)
	}
	if _, err := os.Stat(filepath.Join(link, "login.keychain-db")); err != nil {
		t.Fatalf("через ссылку связка не читается: %v", err)
	}
}

// Готовый дом из затравки перекрывает умолчание: свою ссылку он приносит сам, и
// стенд её не трогает.
func TestSeedKeychainWins(t *testing.T) {
	home := fakeHome(t)
	seed := t.TempDir()
	own := fakeHome(t)
	if err := os.MkdirAll(filepath.Join(seed, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(own, keychainRel), filepath.Join(seed, keychainRel)); err != nil {
		t.Fatal(err)
	}
	e, err := makeEnv(filepath.Join(t.TempDir(), "run"), devkitRoot(t), layout("full"), seed, home)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(e.Home, keychainRel))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(own, keychainRel) {
		t.Fatalf("ссылка ведёт на %s, а затравка принесла свою", target)
	}
}

// Прогон уносит временный дом вместе со ссылкой, а связка пользователя цела:
// os.RemoveAll идёт по ссылке мимо, а не внутрь.
func TestTempHomeGoesWithTheRun(t *testing.T) {
	home := fakeHome(t)
	p := params(t, scenarios(t, "press"), "full", "core")
	p.UserHome = home
	p.Work = t.TempDir()
	if _, _, err := Run(p); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(p.Work)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("после прогона осталось %d записей, а временные дома должны уйти", len(left))
	}
	if _, err := os.Stat(filepath.Join(home, keychainRel, "login.keychain-db")); err != nil {
		t.Fatalf("связка пользователя не пережила прогон: %v", err)
	}
}

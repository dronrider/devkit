package main

import (
	"strings"
	"testing"
	"time"
)

func TestCookieRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	val := cookieValue("secret", now.Add(time.Hour))
	if !cookieValid("secret", val, now) {
		t.Fatal("свежая кука со своей подписью должна приниматься")
	}
}

func TestCookieForgedSignature(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	val := cookieValue("secret", now.Add(time.Hour))
	exp, _, _ := strings.Cut(val, ".")
	for _, bad := range []string{
		exp,
		exp + ".",
		exp + ".deadbeef",
		"",
		"мусор",
	} {
		if cookieValid("secret", bad, now) {
			t.Errorf("кука %q без верной подписи принята", bad)
		}
	}
}

func TestCookieExpiry(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	val := cookieValue("secret", now.Add(-time.Second))
	if cookieValid("secret", val, now) {
		t.Fatal("просроченная кука принята: срок не проверяется")
	}
}

// Ротация секрета гасит все выданные куки: подпись перестаёт сходиться.
func TestCookieDiesOnRotate(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	val := cookieValue("old-secret", now.Add(time.Hour))
	if cookieValid("new-secret", val, now) {
		t.Fatal("кука пережила ротацию секрета")
	}
	if cookieValid("", val, now) {
		t.Fatal("пустой секрет не должен принимать ничего")
	}
}

func TestTokenMatch(t *testing.T) {
	if !tokenMatch("abc", "abc") {
		t.Fatal("одинаковые токены должны сходиться")
	}
	if tokenMatch("abc", "abd") || tokenMatch("abc", "ab") || tokenMatch("abc", "") {
		t.Fatal("чужой токен принят")
	}
	if tokenMatch("", "") {
		t.Fatal("пустой настроенный токен не должен пускать никого")
	}
}

func TestNewSecret(t *testing.T) {
	a, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 64 || a == b {
		t.Fatalf("секреты должны быть 256 бит и разными: %q %q", a, b)
	}
}

package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Вход по решению LLD DK-112: единый секрет-токен в конфиге и подписанная
// кука. Значение куки это срок действия с подписью HMAC-SHA256 от секрета,
// состояния сессий на сервере нет, и рестарт демона никого не разлогинивает.
// dashboard secret --rotate меняет секрет, и все куки разом мертвы, потому
// что подпись перестаёт сходиться.

const (
	cookieName = "dashboard_session"
	cookieAge  = 30 * 24 * time.Hour
)

// loginPause гасит перебор токена после провала входа; тесты её обнуляют.
var loginPause = time.Second

// newSecret рождает 256 бит случайности; он же токен входа.
func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func sign(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// cookieValue собирает значение куки: unix-срок и подпись через точку.
func cookieValue(secret string, expiry time.Time) string {
	exp := strconv.FormatInt(expiry.Unix(), 10)
	return exp + "." + sign(secret, exp)
}

// cookieValid сверяет подпись константным временем и срок по часам сервера.
func cookieValid(secret, value string, now time.Time) bool {
	exp, mac, ok := strings.Cut(value, ".")
	if !ok || secret == "" {
		return false
	}
	sec, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() > sec {
		return false
	}
	want := sign(secret, exp)
	return subtle.ConstantTimeCompare([]byte(mac), []byte(want)) == 1
}

// tokenMatch сверяет токен входа константным временем; хеши выравнивают
// длину, чтобы сравнение не выдавало и её.
func tokenMatch(want, got string) bool {
	if want == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

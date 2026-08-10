package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// KeychainBackend хранит значения секретов в macOS Keychain через бинарь
// security: service один на все секреты devkit (devkit.secretctl), account
// равен имени секрета, а пароль равен значению. Индекс имён лежит пустыми
// файлами-маркерами в Dir, как у FileBackend: names работает единообразно и не
// зовёт dump-keychain (шумный, требует прав и роняет диалог разблокировки
// Keychain на неподготовленной машине).
//
// Security это путь к бинарю security вместе с обёрткой его вызова, вынесенный
// в интерфейс ради тестов: подстановка фейка проверяет разбор аргументов и
// реакции на отказ Keychain, не трогая настоящее хранилище.
type KeychainBackend struct {
	Dir      string
	Service  string
	Security securityRunner
}

// securityRunner абстрагивает вызов security find-generic-password: продакшен
// держит realSecurity, тесты подменяют его фейком, чтобы не связываться с
// настоящим Keychain и его диалогами.
type securityRunner interface {
	// Find достаёт пароль (значение секрета) по сервису и account. Ошибка
	// Keychain «запись не найдена» это тоже error: различать её с вызывающим
	// кодом не нужно, у secretctl своя missingSecret по маркеру в Dir.
	Find(service, account string) (string, error)
}

// realSecurity зовёт системный бинарь security. Флаг -w печатает только пароль
// на stdout; без него security выводит метаданные записи, а пароль глушит.
func (r *realSecurity) Find(service, account string) (string, error) {
	cmd := exec.Command(r.Path, "find-generic-password", "-s", service, "-a", account, "-w")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// stderr security при ошибке пишет «The specified item could not be found
	// in the keychain.» и подобное: это текст причины, не значение. Однако
	// токен в stderr не пишется никогда (только в stdout с -w), поэтому
	// копируем stderr в свой, чтобы продиагностировать отказ без потери
	// значения: stderr secretctl не считается утечкой, его видит оператор, а
	// не модель.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		hint := strings.TrimSpace(stderr.String())
		if hint != "" {
			return "", fmt.Errorf("%s: %w", hint, err)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

type realSecurity struct {
	Path string
}

func (b *KeychainBackend) Names() ([]string, error) {
	return readNames(b.Dir)
}

func (b *KeychainBackend) Get(name string) (string, error) {
	if !validName(name) {
		return "", badName(name)
	}
	// Маркер в Dir это источник правды для names: если маркера нет, секрета
	// нет, даже если в Keychain что-то и лежит под этим account. Так names и
	// Get сходятся в одном наборе имён, и Keychain без маркера не светится.
	if _, err := os.Stat(filepath.Join(b.Dir, name)); err != nil {
		if os.IsNotExist(err) {
			return "", missingSecret(name)
		}
		return "", fmt.Errorf("не проверил секрет %q: %w", name, err)
	}
	value, err := b.Security.Find(b.Service, name)
	if err != nil {
		// Имя называем, причину отказа Keychain не тащим: она может содержать
		// путь или дамп, а от модели нужно только имя отсутствующего секрета.
		return "", fmt.Errorf("не достал секрет %q из Keychain: %w", name, err)
	}
	return value, nil
}

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
	// Add кладёт или заменяет запись (-U): значение уходит в security по
	// stdin, а не аргументом, потому что argv виден в ps.
	Add(service, account, value string) error
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

// Add зовёт add-generic-password с флагом обновления: -U заменяет существующую
// запись и создаёт отсутствующую. Значение едет командой из stdin в режиме
// бинаря -i, и у этого входа две предпосылки. Аргументом его не пишем: argv
// виден в ps, и туда секрет не уходит ни в каком виде. Обычный же stdin при
// хвостовом -w бинарь читает с подтверждением и держит в буфере не больше
// 128 байт, так что длинная запись проходила с кодом 0, а в Keychain ложился
// короткий кусок. Команда из stdin в -i разбирается целиком, проверено до
// четырёх килобайт. Перевод строки команду разбивает, поэтому значение с ним
// сюда не кладётся, а спецсимволы закрываются обратным слэшем.
func (r *realSecurity) Add(service, account, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("в значении перевод строки: команда security -i построчная, в Keychain его не положить")
	}
	cmd := exec.Command(r.Path, "-i")
	cmd.Stdin = strings.NewReader("add-generic-password -U -s " +
		secEscape(service) + " -a " + secEscape(account) + " -w " +
		secEscape(value) + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		hint := strings.TrimSpace(stderr.String())
		if hint != "" {
			return fmt.Errorf("%s: %w", hint, err)
		}
		return err
	}
	return nil
}

// secEscape закрывает спецсимволы разборщика команд security -i обратным
// слэшем: без него пробел обрывал значение, а кавычка меняла кавычки внутри.
// Перед обычным символом разборщик слэш снимает, поэтому закрывается только
// то, что ему мешает, и обычные значения уходят как есть.
func secEscape(v string) string {
	const specials = " \t\"'\\$`;|&<>#*?!~^()[]"
	var b strings.Builder
	for _, ch := range v {
		if strings.ContainsRune(specials, ch) {
			b.WriteByte('\\')
		}
		b.WriteRune(ch)
	}
	return b.String()
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

// Set кладёт значение в Keychain, потом ставит маркер в Dir. Маркер это
// источник правды для names, и без него новая запись в перечне не появилась бы.
// Порядок обратный чтению: сначала значение, потом маркер, чтобы обрыв на
// Keychain не оставлял маркер без секрета под ним.
func (b *KeychainBackend) Set(name, value string) error {
	if !validName(name) {
		return badName(name)
	}
	if err := b.Security.Add(b.Service, name, value); err != nil {
		return fmt.Errorf("не записал секрет %q в Keychain: %w", name, err)
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return fmt.Errorf("не создал директорию маркеров: %w", err)
	}
	marker := filepath.Join(b.Dir, name)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		return fmt.Errorf("не поставил маркер секрета %q: %w", name, err)
	}
	return nil
}

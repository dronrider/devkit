package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	stdout, stderr, err := r.runSecurity("", "find-generic-password", "-s", service, "-a", account, "-w")
	if err != nil {
		return "", securityErr(stderr, err)
	}
	return strings.TrimSpace(stdout), nil
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
	_, stderr, err := r.runSecurity("add-generic-password -U -s "+
		secEscape(service)+" -a "+secEscape(account)+" -w "+
		secEscape(value)+"\n", "-i")
	if err != nil {
		return securityErr(stderr, err)
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
	// Timeout это предел ожидания одного вызова security. Ноль берёт
	// значение из SECRETCTL_SECURITY_TIMEOUT, а переменная не задана -
	// defaultSecurityTimeout. Поле впрыскиваемое: живой тест ставит короткий
	// предел, чтобы headless не стоял в ожидании авторизации UI.
	Timeout time.Duration
}

// defaultSecurityTimeout это предел ожидания security в проде. Keychain в
// headless вечно держит makeLoginAuthUI -> AuthorizationCopyRights, и без
// предела вызывающий ждёт вместе с подпроцессом до потолка go test.
const defaultSecurityTimeout = 30 * time.Second

// errSecurityTimeout называет снятый по пределу подпроцесс: ответа не было,
// ждать дальше нельзя. От прочих ошибок security его отличает вызывающий
// (errors.Is) и принимает решение пропустить шаг либо отказать.
var errSecurityTimeout = errors.New("security снят по пределу ожидания")

// errBadSecurityTimeout называет отвергнутое значение предела из переменной
// окружения. Падение выбрано вместо отката к умолчанию: короткий предел
// обычно ставят под headless, и молчаливый откат возвращает то самое долгое
// ожидание, от которого убегают.
var errBadSecurityTimeout = errors.New("SECRETCTL_SECURITY_TIMEOUT отвергнута")

func (r *realSecurity) waitLimit() (time.Duration, error) {
	if r.Timeout > 0 {
		return r.Timeout, nil
	}
	v, ok := os.LookupEnv("SECRETCTL_SECURITY_TIMEOUT")
	if !ok {
		return defaultSecurityTimeout, nil
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("%w: пустое значение", errBadSecurityTimeout)
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: %q", errBadSecurityTimeout, v)
	}
	return d, nil
}

// runSecurity зовёт бинарь security с пределом своего подпроцесса: по
// истечении подпроцесс убивается, трубы закрываются по WaitDelay, и вызывающий
// получает errSecurityTimeout. Предел свой у каждого вызова: Find и Add зовут
// помощника и не дублируют ожидание.
func (r *realSecurity) runSecurity(stdin string, args ...string) (stdout, stderr string, err error) {
	limit, err := r.waitLimit()
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Path, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// Без WaitDelay Run ждёт трубу, а её после убийства процесса может
	// держать открытой его выживший потомок.
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", "", fmt.Errorf("%w: %s %v", errSecurityTimeout, r.Path, args)
	}
	return outBuf.String(), errBuf.String(), err
}

// securityErr поднимает отказ вызова: stderr security при ошибке пишет
// «The specified item could not be found in the keychain.» и подобное, это
// текст причины, не значение. Токен в stderr не пишется никогда (только в
// stdout с -w), поэтому hint уезжает в ошибку без потери значения: stderr
// secretctl не считается утечкой, его видит оператор, а не модель.
func securityErr(stderr string, err error) error {
	hint := strings.TrimSpace(stderr)
	if hint != "" {
		return fmt.Errorf("%s: %w", hint, err)
	}
	return err
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

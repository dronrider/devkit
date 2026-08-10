package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// runSecretctlAux запускает собранный бинарь с extras поверх окружения
// процесса теста: HOME и SECRETCTL_BACKEND выставляет setupSecretsEnv, поэтому
// бинарь читает то же временное хранилище, что и assertions.
func runSecretctlAux(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("запуск %v: %v", args, err)
		}
	}
	return out.String(), errb.String(), code
}

// setupSecretsEnv раскладывает временное хранилище и фиксирует file-бэкенд: так
// интеграционный прогон не зависит от того, есть ли security на машине теста и
// разблокирован ли Keychain.
func setupSecretsEnv(t *testing.T, secrets map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SECRETCTL_BACKEND", "file")
	dir := filepath.Join(home, ".devkit", "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range secrets {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// names перечисляет имена в алфавите и не тащит значений: это и есть контракт
// команды, на котором стоит принцип цели «модель видит имена, не значения».
func TestNamesListsNamesWithoutValues(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{
		"GITHUB_TOKEN": "ghs_secretvalue",
		"GITLAB_TOKEN": "glpat_othervalue",
		"OS_TOKEN":     "os-thirdvalue",
	})
	stdout, _, code := runSecretctlAux(t, bin, "names")
	if code != 0 {
		t.Fatalf("names упал с %d: %s", code, stdout)
	}
	got := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if !reflect.DeepEqual(got, []string{"GITHUB_TOKEN", "GITLAB_TOKEN", "OS_TOKEN"}) {
		t.Fatalf("names: %v", got)
	}
	for _, value := range []string{"ghs_secretvalue", "glpat_othervalue", "os-thirdvalue"} {
		if strings.Contains(stdout, value) {
			t.Fatalf("names напечатал значение %q: %q", value, stdout)
		}
	}
}

// names на пустом хранилище это пустой stdout и код 0, а не ошибка: секретов
// нет, и утилита честно об этом говорит.
func TestNamesEmptyStorage(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{})
	stdout, _, code := runSecretctlAux(t, bin, "names")
	if code != 0 || stdout != "" {
		t.Fatalf("names на пустом: code=%d stdout=%q", code, stdout)
	}
}

// exec подставляет значение секрета в переменную окружения подпроцесса, имя
// переменной равно имени секрета. Подтверждается подпроцессом, который печатает
// свою переменную окружения: так проверяется вся цепочка get + setEnv + exec.
func TestExecInjectsSecretIntoSubprocessEnv(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"API_TOKEN": "injected-value-42"})
	stdout, stderr, code := runSecretctlAux(t, bin, "exec", "API_TOKEN", "--", "sh", "-c", "printf %s \"$API_TOKEN\"")
	if code != 0 {
		t.Fatalf("exec упал (%d): stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "injected-value-42" {
		t.Fatalf("подпроцесс увидел не то значение: %q", stdout)
	}
}

// Переменная окружения подпроцесса перекрывается секретом: так случайно
// выставленная наружу переменная не подменяет значение, а утилита остаётся
// единственным источником правды.
func TestExecOverridesExistingEnv(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"API_TOKEN": "from-secret"})
	t.Setenv("API_TOKEN", "from-outer-env")
	stdout, _, code := runSecretctlAux(t, bin, "exec", "API_TOKEN", "--", "sh", "-c", "printf %s \"$API_TOKEN\"")
	if code != 0 {
		t.Fatalf("exec упал: %s", stdout)
	}
	if stdout != "from-secret" {
		t.Fatalf("значение из секрета не перекрыло внешнюю env: %q", stdout)
	}
}

// Код выхода подпроцесса пробрасывается как есть: обвязка видит реальный исход
// команды, а не «exec прошёл».
func TestExecPropagatesExitCode(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"TOKEN": "v"})
	for _, want := range []int{0, 1, 7} {
		t.Run("", func(t *testing.T) {
			_, _, code := runSecretctlAux(t, bin, "exec", "TOKEN", "--", "sh", "-c", fmtExit(want))
			if code != want {
				t.Fatalf("код: %d, жду %d", code, want)
			}
		})
	}
}

func fmtExit(code int) string {
	return "exit " + itoa(code)
}

// itoa без strconv: строка короткая, и зависеть от импорта тут незачем.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// exec без команды после «--» это отказ: имя секрета без подпроцесса не имеет
// смысла, и молчаливый ноль значил бы соврать обвязке, что секрет подставился.
func TestExecRefusesEmptyCommand(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"TOKEN": "v"})
	_, stderr, code := runSecretctlAux(t, bin, "exec", "TOKEN", "--")
	if code == 0 {
		t.Fatalf("exec без команды прошёл: code=0 stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "команду") {
		t.Fatalf("отказ не назвал причину: %q", stderr)
	}
	if strings.Contains(stderr, "v") {
		t.Fatalf("отказ светит значение секрета: %q", stderr)
	}
}

// exec без «--» это отказ: имя секрета обязано отделяться от команды подпроцесса
// однозначно, иначе флаги команды путались бы с флагами secretctl.
func TestExecRefusesWithoutSeparator(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"TOKEN": "v"})
	_, stderr, code := runSecretctlAux(t, bin, "exec", "TOKEN", "env")
	if code == 0 {
		t.Fatalf("exec без «--» прошёл: stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "команду") && !strings.Contains(stderr, "--") {
		t.Fatalf("отказ не назвал причину: %q", stderr)
	}
}

// exec для отсутствующего секрета это отказ с именем, без значения: значение
// другого секрета в ошибку не светится.
func TestExecMissingSecretRefusesWithName(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"PRESENT": "real-value"})
	_, stderr, code := runSecretctlAux(t, bin, "exec", "NOPE", "--", "true")
	if code == 0 {
		t.Fatalf("exec отсутствующего секрета прошёл: stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "NOPE") {
		t.Fatalf("отказ обязан назвать имя отсутствующего секрета: %q", stderr)
	}
	if strings.Contains(stderr, "real-value") {
		t.Fatalf("отказ светит значение другого секрета: %q", stderr)
	}
}

// Обход всех подкоманд с запросом значения не выводит его в stdout самой
// secretctl. Это ключевое закрепление принципа цели DK-207: у утилиты нет
// команды «напечатать значение», и пробовать все известные и неизвестные имена
// тоже нельзя. exec подставляет значение в env подпроцесса, но сама secretctl
// его не печатает; если подпроцесс не печатает env, значение не появляется.
//
// Регрессия тут механически не ловится (модуль новый, на main его нет), и
// регрессионный прогон regcheck против main не находит старого кода, чтобы
// краснеть на нём. Поэтому закрепление держится обычным тестом на собранном
// бинаре: он зелёный сейчас и покраснеет, если в будущем добавится команда,
// печатающая значение в stdout.
func TestNoSubcommandPrintsValue(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{"API_TOKEN": "SUPERSECRET-VALUE-42"})
	// Имена, которые новоявленная команда печати могла бы получить: перебор
	// покрывает и те, что в usage, и те, что напрашиваются по аналогии.
	probes := [][]string{
		{"names"},
		{"names", "API_TOKEN"},
		{"names", "--", "API_TOKEN"},
		{"exec", "API_TOKEN", "--", "true"},
		{"exec", "API_TOKEN", "--", "echo", "hello"},
		{"exec", "API_TOKEN", "--", "sh", "-c", "echo hello"},
		{"help"},
		{"--help"},
		{"-h"},
		{"get", "API_TOKEN"},
		{"show", "API_TOKEN"},
		{"print", "API_TOKEN"},
		{"value", "API_TOKEN"},
		{"cat", "API_TOKEN"},
		{"read", "API_TOKEN"},
		{"reveal", "API_TOKEN"},
		{"dump", "API_TOKEN"},
		{"view", "API_TOKEN"},
		{"API_TOKEN"},
	}
	for _, args := range probes {
		stdout, _, _ := runSecretctlAux(t, bin, args...)
		if strings.Contains(stdout, "SUPERSECRET-VALUE-42") {
			t.Fatalf("команда %v вывела значение секрета в stdout: %q", args, stdout)
		}
	}
}

// Сортировка по алфавиту детерминирована: на ней строятся тесты и список,
// который видит модель, не должен дрожать от запуска к запуску.
func TestNamesSortedAcrossMany(t *testing.T) {
	bin := buildSecretctl(t)
	in := map[string]string{
		"zebra":  "1",
		"alpha":  "2",
		"mango":  "3",
		"beta":   "4",
		"gamma":  "5",
		"delta":  "6",
		"omega":  "7",
		"kappa":  "8",
		"sigma":  "9",
		"theta":  "10",
	}
	setupSecretsEnv(t, in)
	stdout, _, code := runSecretctlAux(t, bin, "names")
	if code != 0 {
		t.Fatalf("names: %s", stdout)
	}
	got := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	want := make([]string, 0, len(in))
	for k := range in {
		want = append(want, k)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names: %v, жду %v", got, want)
	}
}

// Неизвестная команда это код 2 и stderr с usage, а не молчаливый ноль: иначе
// опечатка в имени команды выглядела бы успешным прогоном.
func TestUnknownCommandExitsNonZero(t *testing.T) {
	bin := buildSecretctl(t)
	setupSecretsEnv(t, map[string]string{})
	_, stderr, code := runSecretctlAux(t, bin, "frobnicate")
	if code != 2 {
		t.Fatalf("неизвестная команда: code=%d, жду 2", code)
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Fatalf("отказ не назвал неизвестную команду: %q", stderr)
	}
}

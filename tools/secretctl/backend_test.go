package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// makeSecretsDir раскладывает временное хранилище: отдельная HOME с .devkit/
// secrets/, куда лягут файлы-значения (file) или пустые маркеры (keychain).
// t.Setenv держит HOME на время теста и возвращает её обратно на cleanup, и
// фабрика бэкендов получает ту же HOME через os.UserHomeDir.
func makeSecretsDir(t *testing.T, secrets map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".devkit", "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, value := range secrets {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFileBackendNamesSorted(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{
		"ZEBRA_TOKEN": "z",
		"ALPHA_TOKEN": "a",
		"MID_TOKEN":   "m",
	})
	b := &FileBackend{Dir: dir}
	got, err := b.Names()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ALPHA_TOKEN", "MID_TOKEN", "ZEBRA_TOKEN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names: %v, жду %v", got, want)
	}
}

// names на пустом хранилище это пустой список, а не ошибка: секретов нет, и
// утилита честно об этом говорит. Так же ведёт себя отсутствующая директория.
func TestFileBackendNamesEmpty(t *testing.T) {
	for _, name := range []string{"директория пустая", "директории нет"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if name == "директория пустая" {
				dir := filepath.Join(home, ".devkit", "secrets")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			b := &FileBackend{Dir: filepath.Join(home, ".devkit", "secrets")}
			got, err := b.Names()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("на пустом хранилище жду nil, вижу %v", got)
			}
		})
	}
}

// Значение TrimSpace'ится, как token_file у trackctl: случайный перевод строки
// в конце файла не должен ломать подстановку.
func TestFileBackendGetTrimsValue(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{"TOKEN": "  trimmed-value  \n"})
	b := &FileBackend{Dir: dir}
	v, err := b.Get("TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if v != "trimmed-value" {
		t.Fatalf("Get: %q (жду trimmed-value)", v)
	}
}

// Отказ для отсутствующего секрета называет имя, но не светит чужое значение.
// Это то же правило, что у token_env в trackctl (contour.go:176).
func TestFileBackendGetMissingNamesSecretNotValue(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{"PRESENT": "real-value"})
	b := &FileBackend{Dir: dir}
	_, err := b.Get("NOPE")
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("отказ обязан назвать имя отсутствующего секрета: %v", err)
	}
	if strings.Contains(err.Error(), "real-value") {
		t.Fatalf("отказ светит значение другого секрета: %v", err)
	}
}

func TestFileBackendRejectsBadName(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{})
	b := &FileBackend{Dir: dir}
	for _, bad := range []string{"", "../etc", "a/b", "a.b", "a b", ".hidden", "a\nb"} {
		if _, err := b.Get(bad); err == nil {
			t.Fatalf("имя %q прошло валидацию, жду отказ", bad)
		}
	}
}

// readNames отсекает директории и невалидные имена, а не падает на них: в
// хранилище мог залететь .DS_Store или чужой файл, и список имён от этого
// ломаться не должен.
func TestReadNamesSkipsDirectoriesAndBadNames(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{"GOOD_ONE": "v"})
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"GOOD_ONE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("readNames: %v, жду %v", got, want)
	}
}

// fakeSecurity подменяет бинарь security в тестах Keychain, не трогая
// настоящее хранилище и его диалоги. missing эмулирует отказ Keychain «запись
// не найдена»: различать её с кодом не нужно, у secretctl своя missingSecret по
// маркеру в Dir.
type fakeSecurity struct {
	values  map[string]string
	missing map[string]bool
	err     error
}

func (f *fakeSecurity) Find(service, account string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.missing[account] {
		return "", fmt.Errorf("The specified item could not be found in the keychain.")
	}
	if v, ok := f.values[account]; ok {
		return v, nil
	}
	return "", fmt.Errorf("The specified item could not be found in the keychain.")
}

func TestKeychainBackendGetReturnsValue(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{"TOKEN": ""}) // пустой маркер
	b := &KeychainBackend{
		Dir:      dir,
		Service:  "devkit.secretctl",
		Security: &fakeSecurity{values: map[string]string{"TOKEN": "kc-value"}},
	}
	v, err := b.Get("TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if v != "kc-value" {
		t.Fatalf("Get: %q (жду kc-value)", v)
	}
}

// Маркер в Dir это источник правды для names: нет маркера -> нет секрета, даже
// если в Keychain что-то лежит под тем же account. Так names и Get сходятся в
// одном наборе имён, и Keychain без маркера не светится.
func TestKeychainBackendGetMissingMarkerRefusesEvenWithKeychainValue(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{}) // маркера TOKEN нет
	b := &KeychainBackend{
		Dir:      dir,
		Service:  "devkit.secretctl",
		Security: &fakeSecurity{values: map[string]string{"TOKEN": "kc-value"}},
	}
	_, err := b.Get("TOKEN")
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("отказ обязан назвать имя секрета без маркера: %v", err)
	}
	if strings.Contains(err.Error(), "kc-value") {
		t.Fatalf("отказ светит значение из Keychain: %v", err)
	}
}

// names берётся из индекса-директории, а не из Keychain: список имён доступен
// без открытия Keychain и его диалогов, и одинаков для обоих бэкендов.
func TestKeychainBackendNamesFromIndex(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{
		"BETA":  "",
		"ALPHA": "",
	})
	b := &KeychainBackend{Dir: dir, Service: "devkit.secretctl", Security: &fakeSecurity{}}
	got, err := b.Names()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ALPHA", "BETA"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names: %v, жду %v", got, want)
	}
}

func TestKeychainBackendGetErrorNamesSecret(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{"TOKEN": ""})
	b := &KeychainBackend{
		Dir:      dir,
		Service:  "devkit.secretctl",
		Security: &fakeSecurity{err: fmt.Errorf("keychain locked")},
	}
	_, err := b.Get("TOKEN")
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("отказ обязан назвать имя секрета: %v", err)
	}
}

func TestKeychainBackendRejectsBadName(t *testing.T) {
	dir := makeSecretsDir(t, map[string]string{})
	b := &KeychainBackend{Dir: dir, Service: "devkit.secretctl", Security: &fakeSecurity{}}
	for _, bad := range []string{"", "../etc", "a/b"} {
		if _, err := b.Get(bad); err == nil {
			t.Fatalf("имя %q прошло валидацию, жду отказ", bad)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"A", "a", "TOKEN", "a-b", "a_b", "ABC123", "0"} {
		if !validName(ok) {
			t.Fatalf("validName(%q) = false, жду true", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a/b", "a.b", ".a", "a..b", "a\nb", "привет"} {
		if validName(bad) {
			t.Fatalf("validName(%q) = true, жду false", bad)
		}
	}
}

func TestSetEnvOverridesExisting(t *testing.T) {
	env := []string{"PATH=/usr/bin", "TOKEN=old", "HOME=/u"}
	got := setEnv(env, "TOKEN", "new")
	want := []string{"PATH=/usr/bin", "HOME=/u", "TOKEN=new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("setEnv: %v, жду %v", got, want)
	}
}

func TestSetEnvAddsWhenMissing(t *testing.T) {
	got := setEnv([]string{"PATH=/usr/bin"}, "TOKEN", "new")
	if !reflect.DeepEqual(got, []string{"PATH=/usr/bin", "TOKEN=new"}) {
		t.Fatalf("setEnv: %v", got)
	}
}

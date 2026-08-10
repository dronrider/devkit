package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
)

// Backend это хранилище секретов: модель оперирует именами, значение в момент
// исполнения подставляет утилита. Реализации две, обе читают индекс имён из
// одной директории ~/.devkit/secrets: FileBackend держит там же и значения
// (файлы 0600), KeychainBackend держит значения в macOS Keychain, а в
// директории лежат пустые файлы-маркеры. Так names работает единообразно и не
// зовёт потенциально блокирующий диалогом dump-keychain.
type Backend interface {
	// Names перечисляет имена секретов в алфавите, без значений.
	Names() ([]string, error)
	// Get достаёт значение секрета по имени. Ошибка называет имя, но не
	// значение: тем же порядком, что token у trackctl (contour.go:176).
	Get(name string) (string, error)
}

// secretsDir это директория хранилища ~/.devkit/secrets.
func secretsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".devkit", "secrets"), nil
}

// defaultBackend выбирает хранилище по окружению: на macOS с доступным в PATH
// бинарем security это Keychain, в ином случае файлы 0600. Выбор перекрывается
// переменной SECRETCTL_BACKEND (file | keychain): ей тесты фиксируют бэкенд,
// не связываясь с настоящим Keychain, а машина без Keychain получает file
// явно, без автодетекта.
func defaultBackend() (Backend, error) {
	dir, err := secretsDir()
	if err != nil {
		return nil, err
	}
	switch os.Getenv("SECRETCTL_BACKEND") {
	case "file":
		return &FileBackend{Dir: dir}, nil
	case "keychain":
		return &KeychainBackend{Dir: dir, Service: keychainService, Security: &realSecurity{Path: "security"}}, nil
	}
	if runtime.GOOS == "darwin" && securityAvailable("security") {
		return &KeychainBackend{Dir: dir, Service: keychainService, Security: &realSecurity{Path: "security"}}, nil
	}
	return &FileBackend{Dir: dir}, nil
}

// keychainService это имя сервиса в Keychain: одно на все секреты devkit,
// account равен имени секрета. Свое пространство имён в Keychain, чтобы не
// пересекаться с чужими generic-password.
const keychainService = "devkit.secretctl"

func securityAvailable(path string) bool {
	_, err := exec.LookPath(path)
	return err == nil
}

// readNames перечисляет имена секретов в директории: для файлового бэкенда это
// файлы-значения, для Keychain файлы-маркеры. Сортировка алфавитная, чтобы
// вывод был детерминированным: на нём строятся тесты, а модель видит стабильный
// список. Директории и имена вне разрешённого алфавита (validName) молча
// пропускаются: хранилище не ломается от .DS_Store и чужих файлов.
func readNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !validName(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// validName отсекает имена, опасные для файлового бэкенда: имя секрета
// становится именем файла, поэтому в нём не бывает слешей, точек и чего-то вне
// букв, цифр, подчёркивания и дефиса. Тем же порядком имя служит account в
// Keychain, и те же правила держат его предсказуемым. Пустое имя тоже
// отсекается: get("") не должен создавать файл с пустым именем.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// missingSecret это отказ «секрета нет», он не светит значение ни в сообщении,
// ни в причине. Таким же образом описывается отказ token_file у trackctl.
func missingSecret(name string) error {
	return fmt.Errorf("нет секрета %q", name)
}

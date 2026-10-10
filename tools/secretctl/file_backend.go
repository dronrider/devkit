package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileBackend хранит секреты файлами 0600 в одной директории: имя файла равно
// имени секрета, содержимое файла равно значению. Так хранилище переносимо и
// работает на пустой машине без сторонних зависимостей, что держит принцип
// devkit «ставится на голую ОС». Формат значения повторяет token_file у
// trackctl (contour.go:179): TrimSpace срезает крайние пробелы и переводы
// строки, иначе случайный хвост в файле ломал бы подстановку.
type FileBackend struct {
	Dir string
}

func (b *FileBackend) Names() ([]string, error) {
	return readNames(b.Dir)
}

func (b *FileBackend) Get(name string) (string, error) {
	if !validName(name) {
		return "", badName(name)
	}
	data, err := os.ReadFile(filepath.Join(b.Dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", missingSecret(name)
		}
		// Сама ошибка чтения имя называет, а значение не называет: его тут
		// ещё нет в руках. Тем не менее формулировка держит только имя.
		return "", fmt.Errorf("не прочитал секрет %q: %w", name, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Set кладёт значение файлом через переименование: tmp пишется тем же
// именем рядом, os.Replace подменяет файл целиком, так недописанное значение
// не становится видимым читателю. Права 0600 ставятся при записи.
func (b *FileBackend) Set(name, value string) error {
	if !validName(name) {
		return badName(name)
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return fmt.Errorf("не создал директорию хранилища: %w", err)
	}
	path := filepath.Join(b.Dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return fmt.Errorf("не записал секрет %q: %w", name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("не заменил секрет %q: %w", name, err)
	}
	return nil
}

// badName это отказ для имени, не прошедшего validName: формулировка не даёт
// подставить ../ или .git/config ни в файловый путь, ни в account Keychain.
func badName(name string) error {
	return fmt.Errorf("имя секрета %q: жду буквы, цифры, знак подчёркивания и дефис", name)
}

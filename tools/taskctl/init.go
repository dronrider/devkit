package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var prefixArgRe = regexp.MustCompile(`^[A-ZА-Я]+$`)

type InitParams struct {
	Prefix, Name string
	// Here велит завести доску в названной директории, не поднимаясь к вершине
	// репозитория. Так подключается проект корп-контура: боковая директория
	// контура лежит подкаталогом его репозитория, и досок в нём столько,
	// сколько проектов (DK-583).
	Here bool
}

// initRoot определяет, где создавать доску: вершина git-репозитория, чтобы
// запуск из поддиректории не плодил вложенных досок; вне git сама стартовая
// директория.
func initRoot(dir string, here bool) (string, error) {
	if here {
		return filepath.Abs(dir)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return filepath.Abs(dir)
}

const boardSkeleton = `# %s: задачи (префикс %s)

Канбан-доска открытой работы и единственный источник правды по тому, что в
работе. Правила ведения в devkit: RULES.md, раздел «Трекинг задач». Строка
держит метаданные и короткий заголовок, разбор задачи живёт в файле
tasks/%s-NNN.md (имя по чистому ID, файл заводится, когда появляется
содержимое). Закрытое переезжает в [TASKS-archive.md](TASKS-archive.md).
Механику доски гоняет taskctl (смотреть list/show, менять add/move/set/close),
таблицы руками не править.

Приоритет P выводится из ранга R по RANKING.md (devkit). Формула
R = Серьёзность(0-75) + Ценность(0-10) + Неопределённость(0-5) + Баг(+5) + Рычаг(0-5),
колонка R держит сумму и разбивку в этом порядке. Бакеты: P0 при R >= 75, P1
при 50-74, P2 при 25-49, P3 при 0-24. Backlog отсортирован по R убыванию, при
равенстве по возрастанию ID. Типы: bug / task / LLD. Цена это грубая оценка
затрат агента на исполнение (S / M / L / XL, шкала в RANKING.md), в ранг не
входит; «-» значит «не оценено».

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

Нет.
`

const archiveSkeleton = `# %s: сделано

Append-only журнал закрытых задач, растёт свободно. Файлы закрытых задач лежат
в tasks/archive/<год закрытия>/, ссылка в строке ведёт туда.

| ID | Задача | Тип | P | Закрыто | Ссылка |
|--------|--------|-----|---|---------|--------|
`

func cmdInit(dir string, p InitParams) (string, error) {
	if !prefixArgRe.MatchString(p.Prefix) {
		return "", fmt.Errorf("нужен --prefix заглавными буквами (например XR), получил %q", p.Prefix)
	}
	root, err := initRoot(dir, p.Here)
	if err != nil {
		return "", err
	}
	if err := boardGuard(root, "init"); err != nil {
		return "", err
	}
	name := p.Name
	if name == "" {
		name = filepath.Base(root)
	}
	for _, f := range []string{boardPath(root), archivePath(root)} {
		if _, err := os.Stat(f); err == nil {
			return "", fmt.Errorf("%s уже существует, доска не пересоздаётся", f)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		return "", err
	}
	board := fmt.Sprintf(boardSkeleton, name, p.Prefix, p.Prefix)
	if err := os.WriteFile(boardPath(root), []byte(board), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(archivePath(root), []byte(fmt.Sprintf(archiveSkeleton, name)), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("доска создана в %s: docs/TASKS.md, docs/TASKS-archive.md, docs/tasks/", root), nil
}

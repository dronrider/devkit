package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

// Раздел «Выкат» в файле задачи держит коммиты, которые shipctl слил под этим
// ID. До него связь задачи с её коммитами выводилась поиском ID в subject
// (codeCommits, taskCommits), и ветка, где ID в сообщения не попал, пропадала
// разом из очереди, из состава поезда и из отката. Пропадала молча: пустой
// ответ там неотличим от честного «кода нет». Запись эту связь фиксирует,
// поиск по subject остаётся для задач, слитых до неё или руками мимо shipctl.
const mergedSection = taskform.Merged

func taskFilePath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// mergedShas возвращает коммиты из раздела «Выкат» файла задачи. Нет файла
// или раздела значит «записи нет», и это не ошибка: так живут все задачи,
// слитые до появления раздела.
func mergedShas(root, id string) ([]string, error) {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var shas []string
	for _, ln := range sectionLines(string(data), mergedSection) {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "- ") {
			continue
		}
		_, list, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		for _, part := range strings.Split(list, ",") {
			if s := strings.TrimSpace(part); isSha(s) {
				shas = append(shas, s)
			}
		}
	}
	return shas, nil
}

// smokeNote это начало строки отметки прогона smoke в разделе «Выкат». Разбор
// самой отметки живёт в форме файла задачи: по ней считает очередь выката
// shipctl и отбирает строки на закрытие taskctl.
const smokeNote = taskform.SmokeNote

// smokeDone говорит, действует ли на последний выкат отметка прогона smoke.
// Файла нет значит выкат непроверенный.
func smokeDone(root, id string) (bool, error) {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return taskform.SmokeCovers(string(data)), nil
}

// isSha отсеивает прозу в строке записи: коммит это семь и больше знаков
// шестнадцатеричного числа.
func isSha(s string) bool { return taskform.IsSha(s) }

// inRecord сверяет полный sha из лога с записанным сокращённым.
func inRecord(rec []string, sha string) bool {
	for _, r := range rec {
		if strings.HasPrefix(sha, r) || strings.HasPrefix(r, sha) {
			return true
		}
	}
	return false
}

// recordMerge дописывает в файл задачи коммиты слитого диапазона и коммитит
// правку. Файла может не быть (однострочная задача из бэклога), тогда его
// заводит taskctl вместе со ссылкой в строке; отказ taskctl не роняет
// слияние, файл тогда пишется голым заголовком.
func recordMerge(root, id string, shas []string) (string, error) {
	if len(shas) == 0 {
		return "", nil
	}
	path := taskFilePath(root, id)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		exec.Command("taskctl", "-C", root, "file", id).Run()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		data = []byte("# " + id + "\n")
	}
	line := "- " + time.Now().Format("2006-01-02") + " слито: " + strings.Join(shas, ", ")
	if err := os.WriteFile(path, []byte(appendToSection(string(data), line)), 0o644); err != nil {
		return "", err
	}
	if _, err := git(root, "add", "--", "docs/TASKS.md", "docs/tasks/"+id+".md"); err != nil {
		return "", err
	}
	if _, err := git(root, "commit", "-m", fmt.Sprintf("docs(tasks): %s коммиты слияния", id),
		"--", "docs/TASKS.md", "docs/tasks/"+id+".md"); err != nil {
		return "", err
	}
	return git(root, "rev-parse", "--short", "HEAD")
}

// appendToSection дописывает строку в конец раздела «Выкат», заводя сам
// раздел, если его нет: место ему выбирает форма TASKFORM.md, а не хвост
// файла, иначе «Выкат» вставал бы после «Проверки», написанной до слияния.
// Круг доработки после возврата из Check это второе слияние той же задачи, и
// его коммиты встают следующей строкой, а не затирают прежние.
func appendToSection(doc, line string) string {
	return taskform.InsertIntoSection(doc, mergedSection, line)
}

// SmokeParams это параметры отметки прогона smoke.
type SmokeParams struct {
	ID   string
	Push bool
}

// cmdSmoke отмечает прогон агентской части сценария после выката: строка
// «smoke прогнан, <дата>» в разделе «Выкат» файла задачи. Двухступенчатый
// Check (LLD DK-400, решение 7): с отметкой очередь выката свободна, а
// приёмка глазами остаётся в Check и закрытия не ждёт. Отметка ставится
// только задаче в Check с непроверенным выкатом: поставленная до выката или
// бескодовой задаче, она освобождала бы очередь без причины.
func cmdSmoke(root string, p SmokeParams) (string, error) {
	primary, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	root = primary
	if corpActive(root) {
		return "", corpRefused("smoke")
	}
	unlock, err := acquireLock(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	main, err := preflight(root)
	if err != nil {
		return "", err
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	if b.sectOf(p.ID) != "check" {
		return "", fmt.Errorf("%s не в Check: smoke отмечается после выката, пока задача ждёт приёмки", p.ID)
	}
	done, err := smokeDone(root, p.ID)
	if err != nil {
		return "", err
	}
	if done {
		return "", fmt.Errorf("smoke за %s уже отмечен, действующей на последний выкат отметки достаточно одной", p.ID)
	}
	busy, err := checkQueue(root, main, b)
	if err != nil {
		return "", err
	}
	holds := false
	for _, id := range busy {
		if id == p.ID {
			holds = true
		}
	}
	if !holds {
		return "", fmt.Errorf("за %s нет непроверенного выката: очередь она не держит, и отметка ей не нужна", p.ID)
	}
	path := taskFilePath(root, p.ID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		exec.Command("taskctl", "-C", root, "file", p.ID).Run()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		// taskctl file мог не завести файл (одна из его правок доски), и
		// голый заголовок достаточен: раздел «Выкат» заведёт appendToSection.
		data = []byte("# " + p.ID + "\n")
	}
	line := "- " + smokeNote + ", " + time.Now().Format("2006-01-02")
	if err := os.WriteFile(path, []byte(appendToSection(string(data), line)), 0o644); err != nil {
		return "", err
	}
	hash, err := commitBoard(root, fmt.Sprintf("docs(tasks): %s smoke прогнан", p.ID), p.ID)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("smoke за %s отмечен в разделе «Выкат», очередь выката задача больше не держит; приёмка глазами остаётся в Check, коммит %s", p.ID, hash)
	plan, err := resolveDeploy(root, "")
	if err != nil {
		return "", err
	}
	if p.Push || plan.autonomous {
		if _, err := git(root, "push"); err != nil {
			return "", fmt.Errorf("отметка поставлена (коммит %s), но пуш доски не прошёл, повторить git push руками: %v", hash, err)
		}
		msg += ", доска запушена"
	}
	return msg, nil
}

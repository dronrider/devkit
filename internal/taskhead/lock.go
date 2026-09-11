// Package taskhead поднимает голову задачи доски (DK-931): замок на ID
// задачи, лестница носителей и ключи профиля харнеса под команду клиента.
// Зовёт его `taskctl run`, а дашборд переходит на тот же код строкой DK-935.
// Порядок носителей и судьба замка разобраны в файле задачи DK-931, раздел
// «Развилки».
package taskhead

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// LockFromEnv называет переменную, которой команда подъёма передаёт замок
// оболочке task-run.py: в ней pid команды. Оболочка, увидев в замке ровно этот
// pid, вписывает свой и держит замок дальше сама.
const LockFromEnv = "DEVKIT_TASK_LOCK_FROM"

const lockPidFile = "pid"

// lockYoung это возраст замка без pid, который ещё считается занятым. Каталог
// и файл pid пишутся двумя шагами, и между ними соседний подъём увидел бы
// пустой замок. Снять его как брошенный значило бы поднять вторую голову.
const lockYoung = 5 * time.Second

// LockPath называет замок задачи id в доме home. Замок машинный, как и
// реестр чатов: задачи с разных досок различаются префиксом ID.
func LockPath(home, id string) string {
	return filepath.Join(home, ".devkit", "task-"+strings.ToUpper(strings.TrimSpace(id))+".lock")
}

// BusyError это отказ занятого замка: голова задачи уже поднята.
type BusyError struct {
	Path  string
	Owner int
}

func (e *BusyError) Error() string {
	if e.Owner == 0 {
		return fmt.Sprintf("голова уже поднимается: замок %s взят только что", e.Path)
	}
	return fmt.Sprintf("голова уже поднята: замок %s держит pid %d", e.Path, e.Owner)
}

// Owner читает pid владельца замка. Ноль значит, что замка нет или pid в нём
// не записан.
func Owner(path string) int {
	data, err := os.ReadFile(filepath.Join(path, lockPidFile))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// Alive отвечает, жив ли процесс pid. Чужой процесс без права на сигнал тоже
// жив: EPERM говорит, что процесс есть.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func young(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && time.Since(fi.ModTime()) < lockYoung
}

func writePid(path string, pid int) error {
	tmp := filepath.Join(path, lockPidFile+".tmp")
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(path, lockPidFile))
}

// Take берёт замок на pid. Занятый живым владельцем замок это *BusyError.
// Замок мёртвого владельца снимается и берётся заново: после ребута и kill -9
// в нём остаётся pid, которого уже нет.
func Take(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	for try := 0; try < 2; try++ {
		err := os.Mkdir(path, 0o755)
		if err == nil {
			return writePid(path, pid)
		}
		if !os.IsExist(err) {
			return err
		}
		owner := Owner(path)
		if Alive(owner) || (owner == 0 && young(path)) {
			return &BusyError{Path: path, Owner: owner}
		}
		os.RemoveAll(path)
	}
	return &BusyError{Path: path, Owner: Owner(path)}
}

// Release снимает замок, если держит его pid. Замок, переданный оболочке,
// команде уже не принадлежит, и снимать его она не вправе.
func Release(path string, pid int) {
	if Owner(path) == pid {
		os.RemoveAll(path)
	}
}

// WaitHandoff ждёт, пока замок перейдёт от mine к живому процессу. Возврат это
// pid нового владельца. Ноль значит, что за срок wait оболочка замок не
// приняла либо носитель умер раньше (gone отвечает true).
func WaitHandoff(path string, mine int, wait time.Duration, gone func() bool) int {
	until := time.Now().Add(wait)
	for {
		if o := Owner(path); o != 0 && o != mine && Alive(o) {
			return o
		}
		if gone != nil && gone() {
			// Последний взгляд: оболочка могла успеть принять замок и
			// выйти между двумя опросами, тогда подъёма не было.
			if o := Owner(path); o != 0 && o != mine && Alive(o) {
				return o
			}
			return 0
		}
		if !time.Now().Before(until) {
			return 0
		}
		time.Sleep(50 * time.Millisecond)
	}
}

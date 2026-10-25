// Package taskhead поднимает голову задачи доски (DK-931): замок на ID
// задачи, лестница носителей и ключи профиля харнеса под команду клиента.
// Зовут его `taskctl run` и дашборд (кнопка экрана, wake, check и round,
// DK-935), так что замок у всех дорог подъёма один.
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

// lockBeat это файл в замке, которого касается оболочка конвейера на каждом
// признаке жизни: отметке хода, вопросе человеку и подтверждённом ожидании.
const lockBeat = "beat"

// lockStale это возраст без признаков жизни, после которого живой владелец
// замка считается замерзшим и замок снимается как брошенный. Живой pid сам по
// себе работой не считается (DK-1454): замерзшее окно и работа снаружи
// неотличимы по pid. Значение стоит с запасом к сроку оболочки: молчащее окно
// она снимает сама двумя окнами молчания, а этот рубеж подхватывает замерзшую
// саму оболочку. Одно число с оболочкой держит сторож пары
// TestStaleBeatIsOneForLockAndShell.
const lockStale = 2 * time.Hour

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

// beatAge это возраст последнего признака жизни в замке. Отметка это файл
// beat, а замкам, поднятым до его появления, признак жизни последний раз
// заявлял сам pid: время его записи и меряется. Замка без обоих файлов не
// бывает, нулевой возврат значит свежий подъём.
func beatAge(path string) time.Duration {
	for _, name := range []string{lockBeat, lockPidFile} {
		fi, err := os.Stat(filepath.Join(path, name))
		if err == nil {
			return time.Since(fi.ModTime())
		}
	}
	return 0
}

// touchBeat заявляет признак жизни замка: пишет отметку beat либо освежает
// её время.
func touchBeat(path string) {
	name := filepath.Join(path, lockBeat)
	if f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	}
	now := time.Now()
	os.Chtimes(name, now, now)
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
// в нём остаётся pid, которого уже нет. Живой, но замерзший владелец
// снимается тем же порядком: pid жив, а признаков жизни в замке нет, и
// работать за такого владельца некому.
func Take(path string, pid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	for try := 0; try < 2; try++ {
		err := os.Mkdir(path, 0o755)
		if err == nil {
			touchBeat(path)
			return writePid(path, pid)
		}
		if !os.IsExist(err) {
			return err
		}
		owner := Owner(path)
		if Alive(owner) && beatAge(path) < lockStale || (owner == 0 && young(path)) {
			return &BusyError{Path: path, Owner: owner}
		}
		os.RemoveAll(path)
	}
	return &BusyError{Path: path, Owner: Owner(path)}
}

// Working отвечает, держит ли замок живой владелец с признаками работы:
// pid жив, а признак жизни в замке свежий. Читателям снаружи, дашборду и
// сторожу, этого довольно, чтобы отличить идущую работу от замерзшего окна.
func Working(path string) bool {
	owner := Owner(path)
	return owner > 0 && Alive(owner) && beatAge(path) < lockStale
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

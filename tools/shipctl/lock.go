package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const lockPath = ".devkit/ship.lock"

// lockOwnerPath это файл рядом с замком, куда держатель пишет, кто он.
// Отдельный файл, а не сам замок: замок в старых проектах попадал под git
// (DK-271), и запись прямо в него делала бы отслеживаемый файл изменённым на
// всё время работы команды. Тогда git отбивал бы checkout своего же слияния, а
// проверка чистоты дерева считала бы замок незакоммиченной правкой. Файл
// держателя заводится захватом и стирается снятием, поэтому вне работы
// команды его на диске нет.
const lockOwnerPath = lockPath + ".owner"

// errLockBusy отличает отказ занятости от аномальных отказов замка (не
// открылся, не устоялся за N попыток). Занятость под ship --drain это
// штатная состыковка с чужим заходом (LLD DK-306, решение 2) и уходит в
// тихий no-op, а аномалия остаётся ошибкой и в разливе, поэтому вызывающий
// код проверяет её errors.Is, а не разбирает текст.
var errLockBusy = errors.New("конвейер занят")

// Замок конвейера. Предусловия merge, ship и revert проверяются одно за
// другим, а между проверкой и checkout с fast-forward помещается целый чужой
// заход: пользователь руками, второй агент, исполнитель не по инструкции.
// Замок берут все команды, двигающие main или доску, включая start:
// он единственный из них пишет и коммитит доску в основном дереве, и его
// taskctl move с пушем посреди чужого слияния бьёт ровно в тот зазор, ради
// которого замок и заводится.
//
// Держится замок дескриптором через flock, поэтому снимает его ядро при
// завершении процесса, в том числе аварийном: убитое посреди слияния shipctl
// конвейер не запирает. Само наличие файла занятости не значит: файл лежит на
// месте и когда замок свободен, ждать его исчезновения бессмысленно, о
// занятости говорит только отказ повторного запуска.
//
// Взявший замок пишет, кто он, в соседний файл .devkit/ship.lock.owner: pid,
// команда с ID задачи и время захвата (DK-122). Отказ читает эту строку и
// называет держателя, чтобы следующий видел, ждать ему (чужое слияние
// началось минуту назад) или разбираться (процесс висит с прошлой ночи).
// Время правки самого замка тут ни при чём: пустой файл трёхдневной давности
// лежит и под свежим замком. Отпуская замок, держатель свой файл удаляет.
//
// flock не знает про переименования и unlink: если файл под живым замком
// снят чужим rm, дескриптор держит замок на старом, отвязанном от пути inode,
// а следующий open создаёт по тому же пути новый файл и берёт flock на нём
// без всякого конфликта. acquireLock после каждого захвата сверяет inode
// своего дескриптора с тем, что сейчас лежит на диске: разошлись, значит
// открытый файл больше не тот, что виден по пути (кто-то снял и пересоздал
// его прямо в эту секунду), и попытка повторяется на актуальном файле.

// maxLockAttempts ограничивает перебор замка сверху: путь, который меняют
// быстрее, чем идёт проверка, это уже не гонка на снятие файла, а сломанное
// окружение, и дальше пытаться незачем.
const maxLockAttempts = 20

// lockOwner собирает строку владельца замка: pid, команда (с ID задачи, если
// он у команды есть) и время захвата. Разделитель табуляция: команда бывает
// из двух слов, а значения по ключам разбираются без догадок.
func lockOwner(who string, now time.Time) string {
	return fmt.Sprintf("pid=%d\tкоманда=%s\tвзят=%s\n", os.Getpid(), who, now.Format(time.RFC3339))
}

// lockHolder превращает строку файла держателя в кусок отказа. Пустой файл
// или строка не того вида это не поломка: замок мог взять shipctl сборки до
// DK-122, а могла не успеть лечь запись держателя, взявшего замок в эту
// секунду.
func lockHolder(data []byte, now time.Time) string {
	fields := map[string]string{}
	for _, part := range strings.Split(strings.TrimSpace(string(data)), "\t") {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			fields[k] = v
		}
	}
	who, pid := fields["команда"], fields["pid"]
	if who == "" || pid == "" {
		return "неназвавшийся запуск shipctl (замок взят сборкой до DK-122 либо запись держателя ещё не легла)"
	}
	holder := fmt.Sprintf("%s, pid %s", who, pid)
	started, err := time.Parse(time.RFC3339, fields["взят"])
	if err != nil {
		return holder
	}
	return fmt.Sprintf("%s, взят %s (%s)", holder, started.Format("2006-01-02 15:04:05"), lockAge(now.Sub(started)))
}

// lockAge пишет возраст замка словами: по нему и видно, ждать или
// разбираться. Точность до минуты, потому что решение принимается по порядку
// величины, а не по секундам.
func lockAge(d time.Duration) string {
	switch {
	case d < 0:
		return "время захвата в будущем, часы разъехались"
	case d < time.Minute:
		return "меньше минуты назад"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d ч %d мин назад", int(d.Hours()), int(d.Minutes())%60)
	}
}

// lockBusy собирает отказ занятости, прочитав держателя из файла рядом с
// замком.
func lockBusy(root string) error {
	data, err := os.ReadFile(filepath.Join(root, lockOwnerPath))
	if err != nil {
		data = nil
	}
	return fmt.Errorf("%w: замок %s держит %s; замок снимется сам, когда держатель закончит, снимать файл руками нечего и не надо; занятость проверяется повторным запуском, а не ожиданием, пока файл исчезнет: под свободным замком он лежит на том же месте пустым, и время его правки ни о чём не говорит; ничего не сделано, повторить позже", errLockBusy, lockPath, lockHolder(data, time.Now()))
}

// acquireLock берёт замок конвейера в основном дереве и возвращает функцию,
// снимающую его. Аргумент who это имя команды с ID задачи, если он у неё
// есть: его читает отказ второго запуска. Без директории .devkit замок не
// берётся, как не пишется и журнал запусков: обвязку заводит devkitctl, и в
// проекте без неё запирать нечего.
func acquireLock(root, who string) (func(), error) {
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return func() {}, nil
	}
	path := filepath.Join(root, lockPath)
	for attempt := 0; ; attempt++ {
		if attempt >= maxLockAttempts {
			return nil, fmt.Errorf("замок %s не устоялся за %d попыток: файл на диске меняется быстрее, чем идёт проверка", lockPath, maxLockAttempts)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("замок %s не открылся: %v", lockPath, err)
		}
		if lockRaceHook != nil {
			lockRaceHook()
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			return nil, lockBusy(root)
		}
		stale, err := lockStale(f, path)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("замок %s не проверился: %v", lockPath, err)
		}
		if stale {
			f.Close()
			continue
		}
		// Запись держателя ложится под уже взятым flock, а снимается перед
		// освобождением замка: вне работы команды файла держателя нет.
		owner := filepath.Join(root, lockOwnerPath)
		if err := os.WriteFile(owner, []byte(lockOwner(who, time.Now())), 0o644); err != nil {
			f.Close()
			return nil, fmt.Errorf("замок %s не записал держателя в %s: %v", lockPath, lockOwnerPath, err)
		}
		return func() {
			os.Remove(owner)
			f.Close()
		}, nil
	}
}

// lockStale сравнивает inode взятого дескриптора с тем, что сейчас лежит по
// пути замка: разошлись, значит файл, на который держится flock, уже не тот,
// что виден снаружи (снят и пересоздан между open и flock), и захват не
// считается действительным.
func lockStale(f *os.File, path string) (bool, error) {
	fdInfo, err := f.Stat()
	if err != nil {
		return false, err
	}
	diskInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return !os.SameFile(fdInfo, diskInfo), nil
}

// lockRaceHook раздвигает окно между open и flock для детерминированного
// теста гонки (замок снят и пересоздан чужим процессом между ними); в
// проде остаётся nil и ничего не делает.
var lockRaceHook func()

// lockWho называет держателя замка так, как его увидит отказ чужого запуска:
// команда и ID задачи, если команда работает с задачей. У ship и push ID нет,
// они идут по поезду и по диапазону коммитов.
func lockWho(cmd, id string) string {
	if id == "" {
		return cmd
	}
	return cmd + " " + id
}

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockPath = ".devkit/ship.lock"

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
// конвейер не запирает. Сам файл остаётся лежать пустым всегда, и когда замок
// свободен, и когда занят: по содержимому и по самому наличию файла занятость
// не читается, ждать его исчезновения бессмысленно, о ней говорит только
// отказ повторного запуска.
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

// acquireLock берёт замок в основном дереве и возвращает функцию, снимающую
// его. Без директории .devkit замок не берётся, как не пишется и журнал
// запусков: обвязку заводит devkitctl, и в проекте без неё запирать нечего.
func acquireLock(root string) (func(), error) {
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
			return nil, fmt.Errorf("%w: замок %s держит другой запуск shipctl (merge, ship, revert или start); файл лежит пустым и когда свободен, и когда занят, само его наличие ни о чём не говорит, занятость проверяется повторным запуском, а не ожиданием, пока файл исчезнет; ничего не сделано, повторить позже", errLockBusy, lockPath)
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
		return func() { f.Close() }, nil
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

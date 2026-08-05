package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockPath = ".devkit/ship.lock"

// Замок конвейера. Предусловия merge, ship и revert проверяются одно за
// другим, а между проверкой и checkout с fast-forward помещается целый чужой
// заход: пользователь руками, второй агент, исполнитель не по инструкции.
// Замок берут все четыре команды, двигающие main или доску, включая start:
// он единственный из них пишет и коммитит доску в основном дереве, и его
// taskctl move с пушем посреди чужого слияния бьёт ровно в тот зазор, ради
// которого замок и заводится.
//
// Держится замок дескриптором через flock, поэтому снимает его ядро при
// завершении процесса, в том числе аварийном: убитое посреди слияния shipctl
// конвейер не запирает. Сам файл остаётся лежать пустым, убирать его руками
// не нужно и нечего.

// acquireLock берёт замок в основном дереве и возвращает функцию, снимающую
// его. Без директории .devkit замок не берётся, как не пишется и журнал
// запусков: обвязку заводит devkitctl, и в проекте без неё запирать нечего.
func acquireLock(root string) (func(), error) {
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return func() {}, nil
	}
	f, err := os.OpenFile(filepath.Join(root, lockPath), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("замок %s не открылся: %v", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("конвейер занят: замок %s держит другой запуск shipctl (merge, ship, revert или start), ничего не сделано; дождаться его и повторить", lockPath)
	}
	return func() { f.Close() }, nil
}

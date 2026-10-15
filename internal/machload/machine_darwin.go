//go:build darwin

package machload

import "syscall"

// sysctlRaw читает значение системного вызова macOS. Библиотечный Sysctl
// рассчитан на текстовые значения и режет у ответа один замыкающий ноль;
// разборы ниже знают об этом и терпят обрезанный хвост.
func sysctlRaw(name string) ([]byte, error) {
	s, err := syscall.Sysctl(name)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

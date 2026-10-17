//go:build !darwin

package machload

import "errors"

// sysctlRaw отказывает на чужой системе: объём памяти машины читается
// системным вызовом macOS, и на другой системе съёма нет. Отказ едет словами
// в заметку сводки, а не молчанием нулей.
func sysctlRaw(string) ([]byte, error) {
	return nil, errors.New("память читается системным вызовом macOS")
}

// Package quotaconf читает настройку порога свежести снимка квоты: строку
// `stale = <минуты>` в машинном файле ~/.devkit/quota.local. Порог решает,
// когда снимок считается протухшим, и держат его трое: agentctl (дорога
// кеш/панель, режим --if-stale и подпись возраста), демон дашборда (пометка
// свежести на экране) и через agentctl тик сторожка. Источник у всех один,
// этот пакет, копий константы нет (DK-633).
//
// Файла или строки нет, значит действует умолчание в 45 минут. Кривое
// значение это отказ с причиной, а не молчаливое умолчание: порог, тихо
// съехавший на 45 минут из-за опечатки, разъехался бы с ожиданием человека и
// не был бы виден ничем.
package quotaconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Default это порог свежести без настройки: за ним снимок протух, корректор
// вверх не двигает, а периодический съём переснимает.
const Default = 45 * time.Minute

// file это имя машинного файла настройки под ~/.devkit. Рядом с ним лежит
// каталог снимков quota/, и имя выбрано той же основой.
const file = "quota.local"

// Path это путь файла настройки от дома: его называют отказы и дока.
func Path(home string) string {
	return filepath.Join(home, ".devkit", file)
}

// StaleAge отдаёт порог свежести. Дом передаётся снаружи: демон живёт с домом
// из конфига, а тесты со своим временным.
func StaleAge(home string) (time.Duration, error) {
	path := Path(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default, nil
		}
		return 0, fmt.Errorf("файл порога свежести %s не читается: %v", path, err)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		key, val, ok := strings.Cut(ln, "=")
		if !ok || strings.TrimSpace(key) != "stale" {
			continue
		}
		val = strings.TrimSpace(val)
		minutes, err := strconv.Atoi(val)
		if err != nil || minutes <= 0 {
			return 0, fmt.Errorf("в %s строка stale = %q не разобрана: жду целое число минут больше нуля", path, val)
		}
		return time.Duration(minutes) * time.Minute, nil
	}
	return Default, nil
}

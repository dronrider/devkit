package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Скорость пакета держится уговором о фикстурах, и сторожит его стенд, а не
// замер времени. На каждый свежий исполняемый файл macOS тратит проверку в
// первом запуске: она стоит от десятых долей секунды до нескольких секунд,
// повторный запуск того же файла идёт за миллисекунды. Пока у каждого стенда
// были свои исполняемые фикстуры, эта проверка складывалась сотнями и растила
// пакет с полутора минут до шести, а под нагрузкой соседних прогонов срывала
// срок слияния (DK-649). Уговор такой: исполняемый файл у фикстур один на всех
// и лежит вне каталога стенда, а тело каждой фикстуры ложится рядом обычным
// файлом.
func TestScriptFixturesShareOneExecutable(t *testing.T) {
	one, two := realDir(t, t.TempDir()), realDir(t, t.TempDir())
	writeScript(t, one, "taskctl", "echo первое")
	writeScript(t, two, "taskctl", "echo второе")
	// Перезапись тела тоже не должна заводить нового исполняемого файла: ею
	// пользуется половина стендов, подменяя ответ утилиты своим.
	writeScript(t, one, "taskctl", "echo третье")

	first, second := realDir(t, filepath.Join(one, "taskctl")), realDir(t, filepath.Join(two, "taskctl"))
	if first != second {
		t.Errorf("у фикстур двух стендов разные исполняемые файлы (%s и %s): проверка macOS достаётся каждому заново",
			first, second)
	}
	for _, dir := range []string{one, two} {
		if strings.HasPrefix(first, dir+string(filepath.Separator)) {
			t.Errorf("исполняемый файл фикстуры лежит в каталоге стенда (%s): свой файл заведёт каждый тест", first)
		}
	}
	// Уговор не должен стоить фикстуре ни голоса, ни аргументов: подмена
	// работает, и последнее записанное тело отвечает своё.
	for dir, want := range map[string]string{one: "третье", two: "второе"} {
		out, err := exec.Command(filepath.Join(dir, "taskctl"), "аргумент").Output()
		if err != nil {
			t.Fatalf("фикстура в %s не позвалась: %v", dir, err)
		}
		if strings.TrimSpace(string(out)) != want {
			t.Errorf("фикстура в %s ответила %q, ожидал %q", dir, strings.TrimSpace(string(out)), want)
		}
	}
}

// realDir разворачивает ссылки пути: временный каталог на macOS лежит под /var,
// а сам /var это ссылка на /private/var, и сравнивать неразвёрнутое с
// развёрнутым нельзя.
func realDir(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("путь %s не развернулся: %v", path, err)
	}
	return real
}

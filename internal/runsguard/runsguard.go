// Package runsguard сторожит настоящий ~/.devkit/runs от записей тестовых
// прогонов. DK-818 разобрал случай, где общий setup(t) сюиты taskctl подменял
// корень проекта, а HOME оставлял настоящим: писатель этапов stage.Open берёт
// дом через stage.Home(), и каждый прогон нёс фикстурные записи прямо в
// боевой каталог машины. Подмена HOME в самих сюитах закрывает конкретный
// случай, а Guard ловит любой другой: он не знает имён сюит и не разбирает,
// какой тест виноват, он просто считает файлы каталога до и после m.Run().
package runsguard

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/stage"
)

// Guard оборачивает m.Run(): сюита зовёт его одной строкой из своего TestMain.
//
//	func TestMain(m *testing.M) { os.Exit(runsguard.Guard(m)) }
//
// Каталог берётся через stage.Dir(stage.Home()) в момент вызова Guard, то есть
// до всяких t.Setenv("HOME", ...) внутри отдельных тестов: те подменяют
// переменную окружения только на время своего теста, а на входе в TestMain
// она ещё настоящая. Рост числа файлов после прогона печатает их имена в w и
// заваливает прогон: код возврата m.Run() не трогается, если он уже ненулевой,
// а с нулевым подменяется на единицу. На машине без ~/.devkit/runs (каталога
// нет вовсе) Guard молчит и в снимке, и в сверке.
//
// Guard смотрит на общий каталог, а не на записи своей сюиты: боевая запись
// от другой живой сессии машины (обычный taskctl move по несвязанной задаче),
// подоспевшая ровно за время m.Run(), заваливает прогон так же, как утечка
// самой сюиты. shipctl merge от этого не страдает (тесты гоняются на
// временном HOME через internal/freshtree), а ad hoc `go test` на реальном
// доме такому совпадению открыт.
func Guard(m *testing.M) int {
	return guard(stage.Dir(stage.Home()), m.Run, os.Stderr)
}

func guard(dir string, run func() int, w io.Writer) int {
	before := entryNames(dir)
	code := run()
	var added []string
	for name := range entryNames(dir) {
		if !before[name] {
			added = append(added, name)
		}
	}
	if len(added) == 0 {
		return code
	}
	sort.Strings(added)
	fmt.Fprintf(w, "runsguard: прогон оставил след в настоящем %s: %s\n", dir, strings.Join(added, ", "))
	if code == 0 {
		code = 1
	}
	return code
}

// entryNames читает имена файлов каталога. Отсутствующий каталог не отличается
// от пустого: свежая машина без единого прогона .run-записей его не заводила.
func entryNames(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]bool{}
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	return names
}

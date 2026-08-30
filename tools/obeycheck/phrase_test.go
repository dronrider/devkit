package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// phraseBin кладёт команду в свежий каталог и отдаёт путь до неё.
func phraseBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := writePhrase(bin); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(bin, phraseName)
}

func phraseCode(t *testing.T, bin, needle, file string) int {
	t.Helper()
	cmd := exec.Command(bin, needle, file)
	out, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("phrase не запустился: %v (%s)", err, out)
	}
	return cmd.ProcessState.ExitCode()
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Тот же абзац, разбитый по-разному, даёт один ответ: вычитка переформатирует
// текст по ширине почти всегда, и клетка от этого краснеть не должна (DK-547).
func TestPhraseIgnoresLineBreaks(t *testing.T) {
	bin := phraseBin(t)
	const needle = "(виток стоять не будет)"
	files := map[string]string{
		"одной строкой":     "Живой чат нужен там, где ответ требуется прямо в работе (виток стоять не будет). Дальше абзац.\n",
		"перенос в скобках": "Живой чат нужен там, где ответ требуется прямо в работе (виток стоять не\nбудет). Дальше абзац.\n",
		"перенос и отступ":  "- Живой чат нужен там, где ответ требуется прямо\n  в работе (виток стоять не\n  будет).\n",
	}
	for name, body := range files {
		if code := phraseCode(t, bin, needle, write(t, "text.md", body)); code != 0 {
			t.Errorf("%s: фраза не найдена, код %d", name, code)
		}
	}
}

// Найтись должно только то, что в файле есть: нормализация пробелов не должна
// превращать проверку в такую, которая зеленеет всегда.
func TestPhraseMissIsRed(t *testing.T) {
	bin := phraseBin(t)
	file := write(t, "text.md", "Ширина панели берётся из памяти браузера.\n")
	for _, needle := range []string{
		"(виток стоять не будет)",
		"из памяти  браузера и ещё",
		"панели берётся памяти",
	} {
		if code := phraseCode(t, bin, needle, file); code != 1 {
			t.Errorf("фраза %q: жду код 1, получил %d", needle, code)
		}
	}
}

// Фраза ищется как текст, а не как шаблон: точка и скобки в прозе стоят часто,
// и грепом-по-шаблону проверка находила бы лишнее.
func TestPhraseIsPlainText(t *testing.T) {
	bin := phraseBin(t)
	file := write(t, "text.md", "Ширина панели переживает перезапуск.\n")
	if code := phraseCode(t, bin, "Ширина.панели", file); code != 1 {
		t.Errorf("точка сработала как шаблон, код %d", code)
	}
}

// Ошибка вызова отвечает своим кодом: молча зеленеть проверке нельзя, а
// путать «фразы нет» с «файла нет» дороже всего в живом прогоне.
func TestPhraseUsage(t *testing.T) {
	bin := phraseBin(t)
	file := write(t, "text.md", "текст\n")
	cases := [][]string{
		{"одна фраза"},
		{"фраза", file, "лишнее"},
		{"фраза", filepath.Join(filepath.Dir(file), "нет-такого.md")},
		{"   ", file},
	}
	for _, args := range cases {
		cmd := exec.Command(bin, args...)
		out, _ := cmd.CombinedOutput()
		if cmd.ProcessState.ExitCode() != 2 {
			t.Errorf("вызов %v: жду код 2, получил %d (%s)", args, cmd.ProcessState.ExitCode(), out)
		}
		if !strings.Contains(string(out), "phrase:") {
			t.Errorf("вызов %v: ошибка не названа (%s)", args, out)
		}
	}
}

// Команда достаётся проверке и не достаётся агенту: судить работу и делать её
// это разные концы, и подсовывать агенту инструменты стенда незачем.
func TestPhraseOnlyInCheckEnv(t *testing.T) {
	e := &runEnv{Bin: filepath.Join(t.TempDir(), "bin")}
	agent := e.environ("/devkit", "full", "phrase", 1)
	for _, kv := range agent {
		if name, val, _ := strings.Cut(kv, "="); name == "PATH" && strings.Contains(val, e.Bin) {
			t.Fatalf("каталог команд стенда уехал в PATH агента: %s", kv)
		}
	}
	check := e.checkEnviron(agent)
	got := ""
	for _, kv := range check {
		if name, val, _ := strings.Cut(kv, "="); name == "PATH" {
			got = val
		}
	}
	if !strings.HasPrefix(got, e.Bin+string(os.PathListSeparator)) {
		t.Fatalf("каталог команд стенда не в начале PATH проверки: %q", got)
	}
}

// Сквозной прогон: проверка сценария зовёт phrase и зеленеет на тексте, где
// фраза разъехалась на две строки, а голый греп её не находит.
func TestPhraseReachesScenarioCheck(t *testing.T) {
	p := params(t, scenarios(t, "phrase"), "full", "core")
	p.Repeats = 1
	report, regression := runOK(t, p)
	if regression {
		t.Fatalf("сценарий с phrase не прошёл:\n%s", report)
	}
	if !strings.Contains(flat(report), "1 1") {
		t.Fatalf("проверка не зелёная на обеих раскладках:\n%s", report)
	}
}

// Склейка идёт внутри блока и останавливается на его границе. Фраза со стыка
// двух абзацев в тексте не написана, и находиться она не должна: иначе
// нормализация приносит обратно ту же ложную клетку, только с другой стороны
// (замечание ревью DK-547).
func TestPhraseKeepsBlockBorders(t *testing.T) {
	bin := phraseBin(t)
	cases := []struct {
		name   string
		body   string
		needle string
	}{
		{
			"пустая строка между абзацами",
			"конец первого абзаца слово\n\nначало следующего абзаца\n",
			"слово начало следующего",
		},
		{
			"заголовок и текст под ним",
			"## Что происходит\n\nПанель дашборда это список задач.\n",
			"Что происходит Панель дашборда",
		},
		{
			"два пункта списка подряд",
			"- первый пункт про ширину\n- второй пункт про сторожок\n",
			"про ширину второй пункт",
		},
		{
			"нумерованный список",
			"1. поднять стенд\n2. прогнать сценарий\n",
			"поднять стенд 2. прогнать",
		},
	}
	for _, c := range cases {
		if code := phraseCode(t, bin, c.needle, write(t, "text.md", c.body)); code != 1 {
			t.Errorf("%s: фраза со стыка блоков нашлась, код %d", c.name, code)
		}
	}
}

// Внутри одного блока перенос по-прежнему не мешает: пункт списка и абзац
// читаются целиком, на сколько бы строк их ни разложила вычитка.
func TestPhraseJoinsInsideBlock(t *testing.T) {
	bin := phraseBin(t)
	cases := []struct {
		name   string
		body   string
		needle string
	}{
		{
			"пункт списка в три строки",
			"- Живой чат нужен там, где ответ\n  требуется прямо в работе (виток\n  стоять не будет).\n",
			"(виток стоять не будет)",
		},
		{
			"абзац после заголовка",
			"## Что происходит\n\nПанель дашборда это\nсписок задач.\n",
			"это список задач",
		},
	}
	for _, c := range cases {
		if code := phraseCode(t, bin, c.needle, write(t, "text.md", c.body)); code != 0 {
			t.Errorf("%s: фраза внутри блока не нашлась, код %d", c.name, code)
		}
	}
}

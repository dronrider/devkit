package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCmdRotate: команда печатает порог двумя строками по образцу budget
// (DK-615). Первая строка машинная, её читает диспетчер и дашборд, вторая
// называет источник числа. Ключа нет, значит порог это умолчание agentctl, и
// молчать об этом нельзя: число из конфига и число из кода читаются одинаково,
// а чинятся по-разному.
func TestCmdRotate(t *testing.T) {
	cases := []struct {
		name, line string
		want       int
		why        string
		warn       bool
	}{
		{"ключ задан", "exec_rotate_tokens = 640000\n", 640000, "ключ exec_rotate_tokens машинного конфига", false},
		{"ключа нет", "", execRotateDefault, "умолчание agentctl", false},
		{"мусор в ключе", "exec_rotate_tokens = \"много\"\n", execRotateDefault, "умолчание agentctl", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kit := fakeKit(t)
			writeProfile(t, kit, "homecli", echoProfile)
			writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n"+c.line)
			text, err := cmdRotate(kit, rotateOpts{})
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("жду две строки, вижу %d:\n%s", len(lines), text)
			}
			if lines[0] != "rotate: "+strconv.Itoa(c.want) {
				t.Fatalf("машинная строка %q, жду порог %d", lines[0], c.want)
			}
			if !strings.Contains(lines[1], c.why) {
				t.Fatalf("вторая строка %q не называет источник %q", lines[1], c.why)
			}
			if !strings.Contains(lines[1], machineConfigPath()) {
				t.Fatalf("вторая строка %q не называет файл конфига", lines[1])
			}
			warned := strings.Contains(lines[1], "значение пропущено")
			if warned != c.warn {
				t.Fatalf("предупреждение про битый ключ: жду %v, вижу %v:\n%s", c.warn, warned, text)
			}
		})
	}
}

// TestCmdRotateDispatcher: --dispatcher берёт порог диспетчера (DK-662).
// Число живёт отдельным ключом dispatcher_rotate_tokens рядом с
// exec_rotate_tokens, и один ключ на обоих разъехал бы боль исполнителя и
// боль диспетчера молча.
func TestCmdRotateDispatcher(t *testing.T) {
	cases := []struct {
		name, line string
		want       int
		why        string
		warn       bool
	}{
		{"ключ задан", "dispatcher_rotate_tokens = 180000\n", 180000, "ключ dispatcher_rotate_tokens машинного конфига", false},
		{"ключа нет", "", dispatcherRotateDefault, "умолчание agentctl", false},
		{"мусор в ключе", "dispatcher_rotate_tokens = \"много\"\n", dispatcherRotateDefault, "умолчание agentctl", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kit := fakeKit(t)
			writeProfile(t, kit, "homecli", echoProfile)
			// Исполнительский ключ в конфиге лежит рядом: мерка диспетчера не
			// должна съехать на его число.
			writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\nexec_rotate_tokens = 900000\n"+c.line)
			text, err := cmdRotate(kit, rotateOpts{dispatch: true})
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
			if lines[0] != "rotate: "+strconv.Itoa(c.want) {
				t.Fatalf("машинная строка %q, жду порог %d", lines[0], c.want)
			}
			if !strings.Contains(lines[1], c.why) {
				t.Fatalf("вторая строка %q не называет источник %q", lines[1], c.why)
			}
			warned := strings.Contains(lines[1], "значение пропущено")
			if warned != c.warn {
				t.Fatalf("предупреждение про битый ключ: жду %v, вижу %v:\n%s", c.warn, warned, text)
			}
		})
	}
}

// TestCmdRotateMeasure: мерка контекста печатает вердикт, который читает
// оболочка (DK-662). Счётчик стенда DEVKIT_CONTEXT_TOKENS перебивает чтение
// транскрипта: стенд и самопроверка оболочек гоняют механику на подставном
// числе, не трогая журналы харнеса.
func TestCmdRotateMeasure(t *testing.T) {
	cases := []struct {
		name     string
		counter  string
		dispatch bool
		want     string
	}{
		{"под порогом", "120000", false, "verdict: under"},
		{"над порогом", "600000", false, "verdict: over"},
		{"диспетчер под порогом", "140000", true, "verdict: under"},
		{"диспетчер над порогом", "160000", true, "verdict: over"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kit := fakeKit(t)
			writeProfile(t, kit, "homecli", echoProfile)
			writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
			t.Setenv(contextEnv, c.counter)
			text, err := cmdRotate(kit, rotateOpts{dispatch: c.dispatch, measure: "self"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "context: "+c.counter) {
				t.Fatalf("в ответе нет контекста %s:\n%s", c.counter, text)
			}
			if !strings.Contains(text, c.want) {
				t.Fatalf("жду %q, вижу:\n%s", c.want, text)
			}
			if !strings.Contains(text, contextEnv) {
				t.Fatalf("ответ не называет источник счётчика:\n%s", text)
			}
		})
	}
}

// TestCmdRotateMeasureBadCounter: битый счётчик стенда это ошибка, а не
// молчание: оболочка решает о ротации по вердикту, и невердикт на битом
// числе оставил бы диспетчера без решения.
func TestCmdRotateMeasureBadCounter(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
	t.Setenv(contextEnv, "много")
	_, err := cmdRotate(kit, rotateOpts{measure: "self"})
	if err == nil {
		t.Fatal("битый счётчик стенда молчит, а должен давать ошибку")
	}
	if !strings.Contains(err.Error(), contextEnv) {
		t.Fatalf("ошибка не называет счётчик: %v", err)
	}
}

// TestCmdRotateMeasureSelfNoSession: --self без адреса сессии это ошибка со
// словами про CLAUDE_CODE_SESSION_ID: клиент кладёт адрес сам, и гадать его
// оболочке нечем.
func TestCmdRotateMeasureSelfNoSession(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
	t.Setenv(contextEnv, "")
	t.Setenv(parentSessionEnv, "")
	_, err := cmdRotate(kit, rotateOpts{measure: "self"})
	if err == nil {
		t.Fatal("мерка без адреса сессии молчит, а должна давать ошибку")
	}
	if !strings.Contains(err.Error(), parentSessionEnv) {
		t.Fatalf("ошибка не называет переменную адреса: %v", err)
	}
}

// TestCmdRotateMatchesHarnessJSON: команда и машинная раскладка отдают одно и
// то же число. Дашборд читает его из harness --json, а диспетчер в чате видит
// строкой rotate, и разъехаться этим двум дорогам нельзя. Порог диспетчера
// тот же порядок держит своим полем (DK-662).
func TestCmdRotateMatchesHarnessJSON(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
	text, err := cmdRotate(kit, rotateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cmdHarnessJSON(kit)
	if err != nil {
		t.Fatal(err)
	}
	want := "\"exec_rotate_tokens\": " + strconv.Itoa(execRotateDefault)
	if !strings.Contains(raw, want) {
		t.Fatalf("в раскладке нет %q:\n%s", want, raw)
	}
	want = "\"dispatcher_rotate_tokens\": " + strconv.Itoa(dispatcherRotateDefault)
	if !strings.Contains(raw, want) {
		t.Fatalf("в раскладке нет %q:\n%s", want, raw)
	}
	if !strings.HasPrefix(text, "rotate: "+strconv.Itoa(execRotateDefault)+"\n") {
		t.Fatalf("команда и раскладка разъехались:\n%s", text)
	}
	dtext, err := cmdRotate(kit, rotateOpts{dispatch: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dtext, "rotate: "+strconv.Itoa(dispatcherRotateDefault)+"\n") {
		t.Fatalf("диспетчерский порог и раскладка разъехались:\n%s", dtext)
	}
}

// TestLastRequestTokens: вес сессии это последний запрос транскрипта, и
// весят в нём input плюс запись и чтение кеша (DK-662). Записи без usage и
// повторы не в счёт.
func TestLastRequestTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	body := strings.Join([]string{
		`{"type":"user","message":{"content":"вопрос"}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":2000,"cache_read_input_tokens":100}}}`,
		`{"type":"assistant","message":{"usage":{"input_tokens":20,"cache_creation_input_tokens":3000,"cache_read_input_tokens":200}}}`,
		`{"type":"assistant","message":{}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := lastRequestTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 20+3000+200 {
		t.Fatalf("вес сессии %d, жду %d", got, 20+3000+200)
	}
	// Запись ближе к концу без usage не затирает последний вес.
	if err := os.WriteFile(path, []byte(body+`{"type":"assistant","message":{"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = lastRequestTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 20+3000+200 {
		t.Fatalf("ненулевой вес затёрт нулевым: %d", got)
	}
}

// TestRotateMark: строка журнала о ротации несёт прежний и новый адрес сессии
// и порог. Молчание за работу не считается (DoD DK-1312), строка пишется
// в turns.log тем же форматом, что turn-mark.py.
func TestRotateMark(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
	logFile := filepath.Join(t.TempDir(), "turns.log")
	t.Setenv("DEVKIT_TURN_MARK_LOG", logFile)
	text, err := cmdRotate(kit, rotateOpts{oldSess: "old-sess-1", newSess: "new-sess-2", mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ход ротация") {
		t.Fatalf("в ответе нет строки ротации:\n%s", text)
	}
	if !strings.Contains(text, "сессия old-sess-1") {
		t.Fatalf("строка не называет прежнюю сессию:\n%s", text)
	}
	if !strings.Contains(text, "новая new-sess-2") {
		t.Fatalf("строка не называет новую сессию:\n%s", text)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	logText := string(data)
	if !strings.Contains(logText, "ход ротация") {
		t.Fatalf("в журнале нет строки ротации:\n%s", logText)
	}
	if !strings.Contains(logText, "новая new-sess-2") {
		t.Fatalf("в журнале нет новой сессии:\n%s", logText)
	}
	if !strings.Contains(logText, "порог-") {
		t.Fatalf("в журнале нет порога:\n%s", logText)
	}
}

// TestRotateMarkError: отказ записи журнала возвращается вызывающему, а не
// глотается (замечание 7 ревью DK-1312).
func TestRotateMarkError(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
	blocker := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_TURN_MARK_LOG", filepath.Join(blocker, "turns.log"))
	_, err := cmdRotate(kit, rotateOpts{oldSess: "old-sess-1", newSess: "new-sess-2", mark: true})
	if err == nil {
		t.Fatal("отказ записи журнала молчит, а должен возвращать ошибку")
	}
	if !strings.Contains(err.Error(), "журнал") && !strings.Contains(err.Error(), "строка") {
		t.Fatalf("ошибка не называет журнал: %v", err)
	}
}

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
			text, err := cmdRotate(kit, "", "", false)
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

// TestCmdRotateMatchesHarnessJSON: команда и машинная раскладка отдают одно и
// то же число. Дашборд читает его из harness --json, а диспетчер в чате видит
// строкой rotate, и разъехаться этим двум дорогам нельзя.
func TestCmdRotateMatchesHarnessJSON(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, "enabled = [\"homecli\"]\ndefault = \"homecli\"\n")
	text, err := cmdRotate(kit, "", "", false)
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
	if !strings.HasPrefix(text, "rotate: "+strconv.Itoa(execRotateDefault)+"\n") {
		t.Fatalf("команда и раскладка разъехались:\n%s", text)
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
	text, err := cmdRotate(kit, "old-sess-1", "new-sess-2", true)
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
	_, err := cmdRotate(kit, "old-sess-1", "new-sess-2", true)
	if err == nil {
		t.Fatal("отказ записи журнала молчит, а должен возвращать ошибку")
	}
	if !strings.Contains(err.Error(), "журнал") && !strings.Contains(err.Error(), "строка") {
		t.Fatalf("ошибка не называет журнал: %v", err)
	}
}

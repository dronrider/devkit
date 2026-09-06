package main

import (
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
			text, err := cmdRotate(kit)
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
	text, err := cmdRotate(kit)
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

package main

import (
	"strings"
	"testing"
)

// Образец снят живьём с agentctl harness: маппинг ярусов стенд читает оттуда,
// своего второго читателя профилей харнеса заводить незачем.
const harnessOut = `харнес: claude-code (детект по переменной CLAUDECODE)
включены: claude-code (машинного конфига /Users/rider/.devkit/harness.local нет, умолчание)
маппинг ярусов: mini = haiku, base = sonnet, pro = opus, max = fable (предложение профиля, машина не настроена)
делегирование: native (профиль /Users/rider/projects/devkit/kit/harness/claude-code.toml)
снимок квоты: /Users/rider/.devkit/quota/claude-code.local, возраст 20м
`

func TestParseTierMap(t *testing.T) {
	m, err := parseTierMap(harnessOut)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"mini": "haiku", "base": "sonnet", "pro": "opus", "max": "fable"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("ярус %s развернулся в %q, ждал %q", k, m[k], v)
		}
	}
	if len(m) != len(want) {
		t.Errorf("ярусов %d, ждал %d: %v", len(m), len(want), m)
	}
}

func TestParseTierMapErrors(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{"строки нет", "харнес: не определён\n", "нет строки"},
		{"маппинг не задан", "маппинг ярусов: не задан, харнес ненастроен; вписать секцию\n", "не разобран"},
		{"половина пары", "маппинг ярусов: mini = , base = sonnet (машинный конфиг)\n", "не разобран"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseTierMap(c.out); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ждал %q, получил: %v", c.want, err)
			}
		})
	}
}

package main

import (
	"testing"
)

// Ключи напоминания известны конфигу: до правки незнакомый ключ ронял старт, и
// круг напоминаний не поднимался вовсе (тот же отказ, что у catchup_interval,
// DK-1414).
func TestLoadConfigRemindKeysKnown(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "root = /x\nremind_hour = 12\nremind_days = 3\n")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("ключи напоминания не приняты конфигом: %v", err)
	}
	if cfg.RemindHour != 12 || cfg.RemindDays != 3 {
		t.Fatalf("час и окно напоминания: %d %d", cfg.RemindHour, cfg.RemindDays)
	}
}

// Умолчания ложатся при сборке конфига, как у порта: поле всегда держит
// рабочее значение, и читателю напоминания не нужно знать про отсутствие
// ключа. Час нуля при этом осмыслен и задаётся ключом, а не умолчанием.
func TestLoadConfigRemindDefaults(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RemindHour != defaultRemindHour || cfg.RemindDays != defaultRemindDays {
		t.Fatalf("умолчания напоминания: час %d, окно %d", cfg.RemindHour, cfg.RemindDays)
	}
}

func TestLoadConfigRemindBadValues(t *testing.T) {
	for _, bad := range []string{
		"remind_hour = 24\n",
		"remind_hour = полдень\n",
		"remind_days = -1\n",
		"remind_days = пара\n",
	} {
		home := t.TempDir()
		writeConf(t, home, "root = /x\n"+bad)
		if _, err := LoadConfig(home); err == nil {
			t.Errorf("конфиг принял неверное значение %q", bad)
		}
	}
}

package main

import (
	"testing"
)

// Ключ догона известен конфигу: до правки незнакомый ключ ронял старт, и круг
// догона не поднимался вовсе. Тест написан без новых символов пакета, чтобы
// краснота на старом коде была поведенческой, а не ошибкой сборки (DK-1414).
func TestLoadConfigCatchupKeyKnown(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "root = /x\ncatchup_interval = 300ms\n")
	if _, err := LoadConfig(home); err != nil {
		t.Fatalf("ключ catchup_interval не принят конфигом: %v", err)
	}
}

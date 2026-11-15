package main

import (
	"fmt"
	"os"
	"strings"
)

// hooksMarker это хук старта сессии, по которому в настройках клиента узнаётся
// раскладка devkit: он пишет журнал сессий, и без него дашборд не связывает
// разговор с tmux-сессией. Тот же скрипт зовётся и хуком PostToolUse с ключом
// --touch, поэтому ищется вызов с --hook. Шесть групп хуков кладёт доктор одним
// заходом, поэтому отсутствие этого хука значит отсутствие всей обвязки.
const hooksMarker = "session-task.py --hook"

// protocolClaude это протокол семейства claude-code, единственного, чьи
// настройки клиента держат хуки devkit. Пустое значение ключа protocol тому же
// семейству принадлежит по умолчанию: профили, написанные до ключа, читаются
// без переучивания.
const protocolClaude = "claude-code"

// foreignProtocol отвечает, чужой ли протокол у профиля: ключ protocol секции
// [hooks] непуст и не из семейства claude-code. Такому клиенту обвязка devkit
// не раскладывается, поднимается он штатным входом из своего каталога, и пустые
// пары окружения в машинном слое для него штатны.
func foreignProtocol(p *profile) bool {
	if p == nil {
		return false
	}
	proto := p.section("hooks").Str("protocol")
	return proto != "" && proto != protocolClaude
}

// hooksGap отвечает, подключена ли обвязка devkit в настройках харнеса. Пустой
// ответ значит «судить нечего или всё на месте»; непустой это находка словами
// для человека.
//
// Судится только включённый харнес: невключённому devkit хуков не раскладывает,
// и их отсутствие там штатно. Харнес чужого протокола не судится вовсе: у него
// нет настроек хуков формата claude-code, обвязка к нему не раскладывается, и
// разрыв ищется там, где его быть не может. Профиль без ключа config в [hooks],
// харнес без каталога под {home} и настройки, которые не прочитались по иной
// причине, чем отсутствие файла, остаются без суждения: молчаливый отказ на
// неясном месте стоил бы разового захода законной сессии.
func hooksGap(l *layers, name string) string {
	if l == nil || !inList(l.Enabled, name) {
		return ""
	}
	p := l.Profiles[name]
	if p == nil {
		return ""
	}
	if foreignProtocol(p) {
		return ""
	}
	path := p.section("hooks").Str("config")
	if path == "" {
		return ""
	}
	if strings.Contains(path, homeMark) {
		home := l.Setup[name].homeOf()
		if home == "" {
			return ""
		}
		path = strings.ReplaceAll(path, homeMark, home)
	}
	path = expandTilde(path)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return ""
	}
	if err == nil && strings.Contains(string(data), hooksMarker) {
		return ""
	}
	return fmt.Sprintf("обвязки нет: devkitctl doctor --fix (харнес %s, в %s нет хуков devkit: "+
		"сессия пошла бы мимо журнала, сторожа плана и рубежей)", name, path)
}

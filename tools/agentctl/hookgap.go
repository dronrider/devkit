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

// hooksGap отвечает, подключена ли обвязка devkit в настройках харнеса. Пустой
// ответ значит «судить нечего или всё на месте»; непустой это находка словами
// для человека.
//
// Судится только включённый харнес: невключённому devkit хуков не раскладывает,
// и их отсутствие там штатно. Профиль без ключа config в [hooks], харнес без
// каталога под {home} и настройки, которые не прочитались по иной причине, чем
// отсутствие файла, остаются без суждения: молчаливый отказ на неясном месте
// стоил бы разового захода законной сессии.
func hooksGap(l *layers, name string) string {
	if l == nil || !inList(l.Enabled, name) {
		return ""
	}
	p := l.Profiles[name]
	if p == nil {
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

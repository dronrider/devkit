package main

import (
	"fmt"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// assignmentOf читает назначение строки и разворачивает его в участника
// команды (DK-1468). Суффикс «[исп: имя]» несёт handle, состав живёт в
// ~/.devkit/team.toml. Возвраты: участник и пустая пометка, когда назначение
// разобралось; nil и пометка-предупреждение, когда суффикс есть, а состава
// под ним не нашлось. У строки без назначения оба значения пустые, и вердикт
// идёт по метаданным, как шёл до команды.
func assignmentOf(r row) (*team.Participant, string) {
	handle := team.TitleHandle(r.Title)
	if handle == "" {
		return nil, ""
	}
	list, err := team.Load(stage.Home())
	if err != nil {
		return nil, fmt.Sprintf("команда машины не прочитана (%v), назначение строки %s не сузило вердикт", err, handle)
	}
	p, ok := team.Find(list, handle)
	if !ok {
		return nil, fmt.Sprintf("строка назначена участнику %s, которого нет в команде: назначение не сузило вердикт, состав показывает taskctl team list", handle)
	}
	return &p, ""
}

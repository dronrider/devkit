package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// Напоминание участнику команды (DK-1468): у человека в команде есть канал
// связи, и напоминание по строке уходит текстом для этого канала. Доставку
// держит сам человек (цель DK-1198 проговаривает это прямо: модель пишет
// текст, человек копирует или просит переписать), команда собирает адресата
// и текст, чтобы копировать было нечего, кроме самого текста. Агентским
// участникам напоминания не ходят: их не зовут, а поднимают.

// RemindParams это вход напоминания.
type RemindParams struct {
	To   string // участник напрямую, назначение строки otherwise
	Note string // свой текст; без него текст собирается из состояния строки
}

// cmdRemind собирает напоминание по строке. Адресат это участник с ролью
// разработки: либо назначенный суффиксом [исп: ...], либо названный ключом
// --to. Ключ сильнее назначения, им диспетчер зовёт того, от кого ждут, даже
// если на строке стоит другой исполнитель.
func cmdRemind(root string, id string, p RemindParams) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	handle := p.To
	if handle == "" {
		handle = assignOf(row.Title)
	}
	if handle == "" {
		return "", fmt.Errorf("%s: назначенного участника нет, зовите ключом --to или назначьте taskctl set %s --assign <имя>", id, id)
	}
	list, err := team.Load(stage.Home())
	if err != nil {
		return "", err
	}
	part, ok := team.Find(list, handle)
	if !ok {
		return "", fmt.Errorf("участника %s нет в команде, состав показывает taskctl team list", handle)
	}
	if part.Kind == team.Agent {
		return "", fmt.Errorf("участник %s агент, напоминания ходят людям: агентскую строку поднимают, а не ждут", handle)
	}
	if part.Channel == "" {
		return "", fmt.Errorf("у участника %s нет канала, задайте его taskctl team add %s --channel ...", handle, handle)
	}
	note := strings.TrimSpace(p.Note)
	if note == "" {
		note, err = rowStateNote(row.Title)
		if err != nil {
			return "", fmt.Errorf("%s: %v (свой текст задаётся ключом --note)", id, err)
		}
	}
	base, _, _, _, _, _, _, _, _, _, _ := splitTitle(row.Title)
	title := strings.TrimSpace(base)
	if title == "" {
		title = row.ID
	}
	return fmt.Sprintf("участник %s (%s), канал %s\n%s %s: %s", handle, teamLine(part), part.Channel, row.ID, title, note), nil
}

// rowStateNote собирает суть напоминания из состояния строки: блокировка
// говорит, кого ждут, срок говорит, когда. Свободной строки без того и
// другого напоминание не строится: выдумывать повод модель бы не могла, а
// утилите тем более нечем.
func rowStateNote(title string) (string, error) {
	// Срок и контроль читаются разбором заголовка: за сроком могут стоять
	// контроль и приёмка, и якорные регулярки на такой строке молчат.
	_, _, _, _, _, _, dueSuf, controlSuf, _, _, blockSuf := splitTitle(title)
	if blockSuf != "" {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(blockSuf), "[блок:"), "]")), nil
	}
	if d := sufText(dueSuf, "срок"); d != "" {
		note := "срок " + d
		today := time.Now().Format(time.DateOnly)
		if d < today {
			note += ", просрочен"
		}
		if controlSuf != "" && d < today {
			note = fmt.Sprintf("контроль просрочен: %s, работы не сданы, дожми (проверка работ и инспектор по кнопке «дожать» на главной)", note)
		}
		return note, nil
	}
	return "", fmt.Errorf("строка без блокировки и срока, повода для напоминания нет")
}

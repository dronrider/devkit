package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// stageSection это раздел файла задачи, куда уезжает пакет этапов.
const stageSection = "## Ход работы"

// flushStages уносит накопленные этапы задачи в раздел «Ход работы» её файла
// одним пакетом. Момент один на все переходы: этап начинают инструменты, которые
// поднимают деятельность (хук спавна субагента, shipctl, taskctl), а смена статуса
// это единственная точка, где пакет заведомо закончен и рабочее дерево задачи
// уже слито. Отсюда и одна правка файла задачи вместо правки на каждый вердикт,
// из-за которой ревьювер оставлял за собой незакоммиченную строку (DK-120).
// Возврат: относительный путь файла задачи, если он изменился, и хвост для
// сообщения команды.
func flushStages(root, id string, now time.Time) (string, string) {
	stages, err := stage.Flush(stage.Home(), stage.MainRoot(root), id)
	if err != nil || len(stages) == 0 {
		return "", ""
	}
	path := taskFilePath(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		// Файла задачи нет (или он уже уехал в архив): этапы пропали, и сказать
		// об этом надо вслух. Ронять из-за этого перевод статуса нельзя, доска
		// уже переписана.
		return "", fmt.Sprintf(", этапы (%d) записывать некуда: %v", len(stages), err)
	}
	lines := stage.Lines(stages, now)
	if err := os.WriteFile(path, []byte(stage.InsertIntoSection(string(data), stageSection, lines...)), 0o644); err != nil {
		return "", fmt.Sprintf(", этапы (%d) не записаны: %v", len(stages), err)
	}
	return filepath.Join("docs", "tasks", id+".md"), fmt.Sprintf(", этапов в ход работы: %d", len(lines))
}

// openWait отмечает ожидание. Статусы Check и Blocked значат ровно это: работа
// ушла к человеку либо стоит на чужом блокере, живой сессии агента за ней нет и
// быть не должно. Check это приёмка глазами, ожидание человека. У Blocked
// ожидание выбирается по разряду причины парковки: слияние соседа и закрытие
// предпосылки (разряды taskctl wake) это машинное событие, остальные разряды
// зовут человека. Остальные переходы этапа не открывают: работу начинает
// инструмент, который её поднимает, и отмечать её здесь значило бы объявлять
// разработку раньше, чем за неё кто-то взялся.
func openWait(root, id, target, reason string, now time.Time) {
	kind, note := stage.WaitHuman, ""
	switch target {
	case SectCheck:
		note = "проверка после выката"
	case SectBlocked:
		note = "блок: " + reason
		if _, ok, _ := parseWaitCond(reason); ok {
			kind = stage.WaitEvent
		}
	default:
		return
	}
	stage.Open(stage.Home(), stage.MainRoot(root), id, kind, note, now)
}

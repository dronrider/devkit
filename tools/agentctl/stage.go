package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// cmdStage отмечает этап работы над задачей руками и показывает отмеченное.
// Разработку и ревью отмечает pick --record сам, ожидание снаружи ставит
// taskctl на смене статуса, а уточнение отмечать некому: вопрос пользователю
// задаёт сессия, и своей команды у неё нет. Ею же смотрят состояние: без
// команды, печатающей запись, бездействующая отметка неотличима от штатной
// работы, и разбираться пришлось бы чтением файла в домашней директории.
// Прогон сценария (вид «проверка») требует --by с именем прогнавшей модели:
// по этой записи ворота taskctl close сверяют прогонявшего с исполнителем
// разработки (DK-642), и запись без имени оставила бы их слепыми.
func cmdStage(root, id, kind, note, by string, now time.Time) (string, error) {
	main := stage.MainRoot(root)
	home := stage.Home()
	if kind == "" {
		return stageShow(home, main, id)
	}
	if kind == stage.Verify && by == "" {
		return "", fmt.Errorf("вид %s требует --by <модель>: прогонявший сценарий записывается машинно", stage.Verify)
	}
	if by != "" && kind != stage.Verify {
		return "", fmt.Errorf("--by ставится только у вида %s: остальным этапам имя кладёт вердикт pick", stage.Verify)
	}
	if by != "" {
		v := stage.VerifyNote(by)
		if note != "" {
			v += ", " + note
		}
		note = v
	}
	if err := stage.Open(home, main, id, kind, note, now); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: этап %s с %s, запись %s", id, kind, now.Format("15:04"), stage.Path(home, main, id)), nil
}

// stageShow печатает запись задачи: живой этап первой строкой, накопленные до
// него следом. Пустая запись отвечает словами, а не пустотой: «этапов нет» это
// ответ, а молчание неотличимо от сломанной команды.
func stageShow(home, root, id string) (string, error) {
	rec, err := stage.Load(stage.Path(home, root, id))
	if err != nil {
		return "", err
	}
	live, ok := rec.Live()
	if !ok {
		return fmt.Sprintf("%s: этапов не отмечено, запись %s не заведена", id, stage.Path(home, root, id)), nil
	}
	out := []string{fmt.Sprintf("stage: %s", live.Kind), fmt.Sprintf("since: %s", live.Start.Format(stage.Stamp))}
	if live.Note != "" {
		out = append(out, "note: "+live.Note)
	}
	if len(rec.Stages) > 1 {
		out = append(out, "до него в пакете:")
		for _, s := range rec.Stages[:len(rec.Stages)-1] {
			out = append(out, fmt.Sprintf("  %s с %s", s.Kind, s.Start.Format(stage.Stamp)))
		}
	}
	return strings.Join(out, "\n"), nil
}

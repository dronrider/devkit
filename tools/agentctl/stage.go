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
//
// Ревью несёт ещё и активную работу: --turns и --minutes (DK-731) считают
// ходы и минуты без ожидания, а --by называет, чья это работа. Пара ставится
// вместе, поодиночке считать нечего, и оба требуют модель тем же смыслом, что
// и проверка: без имени запись работы обезличена.
func cmdStage(root, id, kind, note, by string, turns, minutes int, now time.Time) (string, error) {
	main := stage.MainRoot(root)
	home := stage.Home()
	if kind == "" {
		return stageShow(home, main, id)
	}
	if kind == stage.Verify && by == "" {
		return "", fmt.Errorf("вид %s требует --by <модель>: прогонявший сценарий записывается машинно", stage.Verify)
	}
	if by != "" && kind != stage.Verify && kind != stage.Review {
		return "", fmt.Errorf("--by ставится только у видов %s и %s: остальным этапам имя кладёт вердикт pick", stage.Verify, stage.Review)
	}
	if (turns != 0 || minutes != 0) && kind != stage.Review {
		return "", fmt.Errorf("--turns и --minutes ставятся только у вида %s", stage.Review)
	}
	if turns != 0 || minutes != 0 {
		if turns <= 0 || minutes <= 0 {
			return "", fmt.Errorf("--turns и --minutes ставятся вместе, оба больше нуля")
		}
		if by == "" {
			return "", fmt.Errorf("--turns и --minutes требуют --by <модель>: чья работа посчитана")
		}
	}
	if by != "" {
		var v string
		switch kind {
		case stage.Verify:
			v = stage.VerifyNote(by)
		case stage.Review:
			v = "ревью провёл " + by
		}
		if note != "" {
			v += ", " + note
		}
		note = v
	}
	if turns > 0 && minutes > 0 {
		work := stage.WorkNote(turns, minutes)
		if note != "" {
			note += ", " + work
		} else {
			note = work
		}
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

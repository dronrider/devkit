package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// cmdStage отмечает этап работы над задачей руками и показывает отмеченное.
// Руками ставится немногое. Этапы работы кладут инструменты, которые её
// начинают (DK-911): постановку, разработку, вычитку, ревью и доработку хук
// спавна субагента по определению агента, слияние и выкат shipctl, ожидания
// taskctl на парковке и agentctl wait. Диспетчеру о них помнить не надо, и
// команда такие виды отбивает, называя писателя: ручная отметка поверх
// автоматической дала бы две строки об одном этапе.
//
// Исключение одно, проверка. Прогон сценария (вид «проверка») требует --by с
// именем прогнавшей модели: по этой записи ворота taskctl close сверяют
// прогонявшего с исполнителем разработки (DK-642), и запись без имени
// оставила бы их слепыми. Хук спавна этого имени не знает, а ворота уже
// требуют его. Три ожидания тоже ставятся руками: сессия, вставшая на чужой
// блокер вне парковки, иначе осталась бы без слова.
//
// Ею же смотрят состояние: без команды, печатающей запись, бездействующая
// отметка неотличима от штатной работы, и разбираться пришлось бы чтением
// файла в домашней директории.
func cmdStage(root, id, kind, note, by string, now time.Time) (string, error) {
	main := stage.MainRoot(root)
	home := stage.Home()
	if kind == "" {
		return stageShow(home, main, id)
	}
	if err := stageByHand(kind); err != nil {
		return "", err
	}
	if kind == stage.Verify && by == "" {
		return "", fmt.Errorf("вид %s требует --by <модель>: прогонявший сценарий записывается машинно", stage.Verify)
	}
	if by != "" && kind != stage.Verify {
		return "", fmt.Errorf("--by ставится только у вида %s: остальным этапам имя кладёт их писатель", stage.Verify)
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

// handKinds это виды, которые ставятся руками: проверка и три ожидания.
var handKinds = []string{stage.Verify, stage.WaitHuman, stage.WaitEvent, stage.WaitQueue}

// stageWriters называет, кто ставит каждый этап работы: отказ ручной отметки
// говорит, откуда этап возьмётся сам.
var stageWriters = map[string]string{
	stage.Setup:  "хук старта сессии по записи черновика или цели",
	stage.Dev:    "хук спавна субагента по определению exec-*",
	stage.Proof:  "хук спавна субагента по определению proofread",
	stage.Review: "хук спавна субагента по определению review-*",
	stage.Rework: "хук спавна субагента по определению exec-* после ревью",
	stage.Merge:  "shipctl merge",
	stage.Deploy: "shipctl ship",
}

// stageByHand отбивает вид, который руками не ставится. Незнакомое слово и
// слово прежнего словаря отбивает сам stage.Open, тут только этапы, у которых
// есть свой писатель.
func stageByHand(kind string) error {
	for _, k := range handKinds {
		if k == kind {
			return nil
		}
	}
	if who, ok := stageWriters[kind]; ok {
		return fmt.Errorf("этап %s ставит %s, руками он не отмечается; руками ставятся: %s", kind, who, strings.Join(handKinds, ", "))
	}
	if stage.Legacy(kind) {
		return fmt.Errorf("вид %q остался в прежнем словаре, ожидание пишется одним из: %s", kind, strings.Join(handKinds[1:], ", "))
	}
	return fmt.Errorf("неизвестный вид деятельности %q, жду один из: %s", kind, strings.Join(stage.Kinds, ", "))
}

// stageShow печатает запись задачи: живой этап первой строкой, накопленные до
// него следом. Закрытый писателем последний этап живым не считается и
// печатается в пакете с концом. Пустая запись отвечает словами, а не
// пустотой: «этапов нет» это ответ, а молчание неотличимо от сломанной
// команды.
func stageShow(home, root, id string) (string, error) {
	rec, err := stage.Load(stage.Path(home, root, id))
	if err != nil {
		return "", err
	}
	if len(rec.Stages) == 0 {
		return fmt.Sprintf("%s: этапов не отмечено, запись %s не заведена", id, stage.Path(home, root, id)), nil
	}
	var out []string
	pack := rec.Stages
	if live, ok := rec.Live(); ok {
		out = append(out, fmt.Sprintf("stage: %s", live.Kind), fmt.Sprintf("since: %s", live.Start.Format(stage.Stamp)))
		if live.Note != "" {
			out = append(out, "note: "+live.Note)
		}
		pack = rec.Stages[:len(rec.Stages)-1]
	} else {
		out = append(out, "stage: нет, последний этап закрыт писателем")
	}
	if len(pack) > 0 {
		out = append(out, "до него в пакете:")
		for _, s := range pack {
			ln := fmt.Sprintf("  %s с %s", s.Kind, s.Start.Format(stage.Stamp))
			if s.Ended() {
				ln += " до " + s.End.Format(stage.Stamp)
			}
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n"), nil
}

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Второй круг чужого ревью (LLD DK-756, решение 5, задача DK-759). Тик
// сторожка увидел ответ автора в тредах MR, снял парковку и зовёт эту команду.
// Дорога тут та же, что у работы с экрана: живой сессии задачи реплика
// подаётся в её окно (DK-724), а без живой сессии поднимается новая, той же
// tmux-сессией task-<ID>, что кнопка запуска и подъём прогона сценария.

// roundOrder это заказ второго круга. Слова дословные, как у заказов экрана:
// по ним скилл review разводит второй круг, а не первый.
func roundOrder(id string) string {
	return "Второй круг ревью " + id + ": автор ответил в тредах MR." +
		" Читай журнал docs/tasks/" + id + ".review.md и дифф от sha строки уровня до головы MR," +
		" скилл review, раздел «Ревью чужой задачи»." +
		" Каждое исправление сверяй с диффом, каждый отказ с причиной автора." +
		" Отказ на блокирующее это одно возражение фактом, дальше парковка" +
		" `taskctl move " + id + " blocked --reason \"спор: ...\"` и зов человека."
}

// reviewRound подаёт второй круг по одной строке. Возврат тот же, что у
// подъёма прогона сценария: строка отчёта и два признака, чтобы зовущий знал,
// поднялось ли что-нибудь и была ли поломка.
func (s *server) reviewRound(proj *Project, id string, rows map[string]boardRow) checkRunReport {
	if _, ok := rows[id]; !ok {
		return checkRunReport{Line: id + ": строки нет на доске проекта " + proj.Name, Failed: true}
	}
	if m := tmuxMissingCheck(); m != "" {
		return checkRunReport{Line: id + ": второй круг не начат, " + m, Failed: true}
	}
	sess := "task-" + id
	order := roundOrder(id)
	// Живая сессия задачи получает второй круг репликой в своё окно: у неё в
	// памяти первый круг, и поднимать поверх неё вторую сессию значило бы
	// читать тот же дифф дважды. Разговор человека под тем же именем сюда не
	// годится, реплика встала бы поверх его хода.
	talk := s.tmuxTalk(proj.Path)
	for _, name := range tmuxSessions() {
		if name != sess {
			continue
		}
		if talk[name] {
			return checkRunReport{Line: id + ": в сессии " + sess + " идёт разговор, второй круг не подаю"}
		}
		if err := chatSend(sess, order); err != nil {
			return checkRunReport{Line: id + ": реплика в живую сессию " + sess + " не ушла, " + procErr(err), Failed: true}
		}
		return checkRunReport{Line: id + ": второй круг подан репликой в живую сессию " + sess, Raised: true}
	}
	own := s.harnesses().byDefault()
	if m := claudeMissing(); m != "" {
		return checkRunReport{Line: id + ": второй круг не начат, " + m, Failed: true}
	}
	if why := s.permsRefusal(); why != "" {
		return checkRunReport{Failed: true, Line: id +
			": второй круг не начат, права машинного контура на машине не разложены: " + why}
	}
	tier, tierWhy := s.pickTier(proj.Path, id, roleReview)
	model := ""
	if own != nil && own.Default {
		model = own.tierModel(tier)
	}
	if err := s.startTaskSession(proj, id, sess, nil, model, order, runPrompt("in-progress", id)); err != nil {
		return checkRunReport{Line: id + ": второй круг не начат, " + err.Error(), Failed: true}
	}
	return checkRunReport{Raised: true,
		Line: fmt.Sprintf("%s: второй круг поднят в tmux-сессии %s, %s", id, sess, tierWhy)}
}

// cmdRound это вход команды `dashboard round <ID>`: второй круг чужого ревью
// по названной строке. Зовёт её тик сторожка без экрана и без демона, поэтому
// сервер собирается на месте, как у команды подъёма прогона.
func cmdRound(home, root string, ids []string, out io.Writer) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if !hasBoard(abs) {
		return fmt.Errorf("доски %s в %s нет: второй круг заказывается по строке доски", boardRel, abs)
	}
	if len(ids) == 0 {
		return fmt.Errorf("жду ID строки ревью: dashboard round -C <корень> <ID>")
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	s := newServer(cfg, nil, nil)
	proj := &Project{Name: filepath.Base(abs), Path: abs}
	raw, err := s.projectBoard(proj.Path)
	if err != nil {
		return fmt.Errorf("доска проекта %s не прочиталась: %v", proj.Name, err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return fmt.Errorf("доска проекта %s не разобралась: %v", proj.Name, err)
	}
	failed := false
	for _, id := range ids {
		rep := s.reviewRound(proj, strings.TrimSpace(id), rows)
		fmt.Fprintln(out, rep.Line)
		if rep.Failed {
			failed = true
		}
	}
	if failed {
		return errCheckRunFailed
	}
	return nil
}

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// Задача, оставшаяся без ведущей сессии (DK-660). Исполнитель погибал на
// середине хода, строка оставалась стоять в разработке, и снаружи это ничем не
// отличалось от штатной очереди: кнопка предлагала продолжить, кружка на
// строке не было вовсе, реплика человека лежала во входе недоставленной. В
// живом случае простой заметил сам человек, через полтора часа, написав задаче
// в чат.
//
// Замечает пропажу тот же сторож, что смотрит за подъёмом сессий (chatwatch.go):
// смерть сессии он и так ловит, а тут у неё появляется второй адресат. Строка
// разговора остаётся на месте, ей человек и пользуется, вернувшись к экрану, а
// уведомитель зовёт его туда, где он есть.

// noLeadReason это повод строки журнала уведомителя. Лента дашборда разводит
// события по поводам (notifications.go), и осиротевшая задача едет тем же
// типом, что стоп и провал: событие про строку доски.
const noLeadReason = "task_nolead"

// projectNamed ищет проект по имени среди корней конфига.
func (s *server) projectNamed(name string) *Project {
	if name == "" {
		return nil
	}
	projects, _ := s.projects()
	for i := range projects {
		if projects[i].Name == name {
			return &projects[i]
		}
	}
	return nil
}

// noLead отвечает, осталась ли задача без ведущей сессии: строка стоит в
// разработке, а живой работы за ней нет. Мера живости тут та же, что у
// признака строки на экране (rowRun): разговор о задаче работы ей не даёт, и
// открытая карточка чата осиротелости не отменяет.
func (s *server) noLead(proj *Project, task string) bool {
	raw, err := s.projectBoard(proj.Path)
	if err != nil {
		s.logf("задача %s: доска не прочиталась, осиротелость не проверить: %v", task, err)
		return false
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		s.logf("задача %s: ответ taskctl не разобрался, осиротелость не проверить: %v", task, err)
		return false
	}
	if rows[task].Sect != sectRun {
		return false
	}
	prefix := ""
	if view, err := parseBoardView(raw); err == nil {
		prefix = view.Prefix
	}
	return runMarks(s.liveWorks(proj.Path, prefix, raw))[task] == ""
}

// noLeadSay зовёт человека к осиротевшей задаче. Канал тот же, каким о себе
// говорит стоп из дашборда: свой уведомитель дашборд не заводит, отправку
// держит hooks/notify.py, и лента показывает его журнал. Отказ отправки
// остаётся в журнале демона, отменять от этого нечего: сессии всё равно нет.
func (s *server) noLeadSay(task, project, why string) {
	if task == "" || project == "" {
		return
	}
	proj := s.projectNamed(project)
	if proj == nil {
		s.logf("задача %s осталась без ведущей сессии, а проекта %s нет в корнях конфига", task, project)
		return
	}
	if !s.noLead(proj, task) {
		return
	}
	np := notifierPath(s.cfg.Roots)
	if np == "" {
		s.logf("задача %s осталась без ведущей сессии: %s", task, notifierMissing)
		return
	}
	title := fmt.Sprintf("%s: %s осталась без ведущей сессии", project, task)
	// Слова смерти приходят без точки на конце, и без неё две фразы в теле
	// баннера склеиваются в одну.
	body := fmt.Sprintf("%s. Строка стоит в разработке, работа по ней не идёт, "+
		"и продолжение это новый запуск.", strings.TrimRight(why, ". "))
	// Задача и проект едут своими ключами (DK-323): по ним лента ведёт от
	// события к строке доски, а кнопка подъёма поднимает работу того проекта,
	// где работа и оборвалась.
	cmd := exec.Command("python3", np, "--reason", noLeadReason,
		"--task", task, "--project", project, title, body)
	cmd.Dir = proj.Path
	if out, err := cmd.CombinedOutput(); err != nil {
		s.logf("задача %s осталась без ведущей сессии, а уведомление не ушло: %v (%s)",
			task, err, strings.TrimSpace(string(out)))
		return
	}
	s.logf("задача %s осталась без ведущей сессии, человек позван", task)
}

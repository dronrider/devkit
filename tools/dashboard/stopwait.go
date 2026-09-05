package main

import (
	"fmt"
	"os"
	"time"
)

// Дожим стопа (доработка DK-716 после провала приёмки). Стоп у разговора это
// два Escape в его окно, и прерывают они ровно то, что идёт прямо сейчас: сам
// ход. Работа сессии ходом не кончается. Субагента она запускает боковым
// журналом, тот живёт своей жизнью, и вернувшись, поднимает агента новым ходом.
//
// Живой прогон 2026-09-05: ход кончился в 14:54:26, стоп со строки пришёл в
// 14:54:33, Escape ушли в пустоту, боковой журнал субагента писался ещё три
// минуты, а привязка сессии к задаче снялась сразу, и строка доски объявила
// работу оконченной, пока та шла.
//
// Отсюда и правило. Остановка это не одно нажатие, а состояние: заказ стопа
// живёт, пока жива работа. Сторож разговоров держит его на глазах, всякий
// поднявшийся ход дожимает теми же Escape, а привязку снимает только тогда,
// когда останавливать больше нечего. До тех пор строка доски честно стоит под
// «Стопом»: работа по ней идёт.

const (
	// stopWaitTTL это срок заказа. Работа, не вставшая за него, снимается со
	// строки словами: держать «Стоп» вечно нельзя, а врать про идущую работу
	// тем более. Четверть часа это дольше любого субагента, которого стоит
	// дожидаться, и короче рабочего перерыва человека.
	stopWaitTTL = 15 * time.Minute
	// subFreshStop это рубеж живости бокового журнала для стопа. Он тот же,
	// каким работу субагента меряет кольцо пульса: разойдись они, экран считал
	// бы работу идущей, а стоп кончал бы её как вставшую.
	subFreshStop = subFresh
)

// subBusyOf отвечает, жива ли фоновая работа сессии. Знание машинное: журнал
// на каждый вызов субагента заводит сам харнес, и пока журнал пишется, работа
// идёт, даже когда транскрипт самого разговора молчит минутами. Метка возврата
// в мете старше свежести: её пишет тот, кто работу ждал.
func subBusyOf(path string, now time.Time) bool {
	for _, log := range subLogs(path) {
		if log.Ended != "" {
			continue
		}
		fi, err := os.Stat(log.File)
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()) <= subFreshStop {
			return true
		}
	}
	return false
}

// chatSubBusy это тот же вопрос, спрошенный про разговор: транскрипт ищется по
// проекту и сессии, как его ищет состояние работы.
func (s *server) chatSubBusy(projPath, sid string) bool {
	if sid == "" {
		return false
	}
	info, ok := findSession(s.transcriptRoots(), projPath, sid)
	if !ok {
		return false
	}
	return subBusyOf(info.path, s.now())
}

// stopWaitSet ставит заказ дожима на окно разговора. Задача с проектом нужны
// концу: по ним снимается привязка и зовётся уведомитель. У стопа из самой
// панели чата задачи нет вовсе, и заказ там держит только дожим хода.
func (s *server) stopWaitSet(tmux, sid, task, project string) {
	if tmux == "" || !chatKeyRe.MatchString(tmux) {
		return
	}
	path := ""
	if info, ok := findSession(s.transcriptRoots(), s.projectPathOf(project), sid); ok {
		path = info.path
	}
	key := "tmux-" + tmux
	st := s.chatStoreRead(key)
	st.StopAt = s.now().Unix()
	st.StopSid, st.StopTask, st.StopProject, st.StopPath = sid, task, project, path
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("заказ дожима стопа для %s не запомнился: %v", tmux, err)
	}
	s.watchAdd(tmux)
}

// stopWaitOff снимает заказ, ничего не заканчивая. Зовёт её реплика человека в
// тот же разговор: написал сам, значит стоп передумал, и дожимать его ход
// сторожу нечего.
func (s *server) stopWaitOff(tmux string) {
	if tmux == "" || !chatKeyRe.MatchString(tmux) {
		return
	}
	key := "tmux-" + tmux
	st := s.chatStoreRead(key)
	if st.StopAt == 0 {
		return
	}
	s.logf("дожим стопа для %s снят: в разговор пришла реплика человека", tmux)
	st.StopAt, st.StopSid, st.StopTask, st.StopProject, st.StopPath = 0, "", "", "", ""
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("снятие дожима стопа для %s не запомнилось: %v", tmux, err)
	}
}

// stopWaitOn отвечает, стоит ли на окне заказ дожима. Спрашивает её сборка
// строки доски: пока стоп дожимается, строка говорит об этом подсказкой, иначе
// человек жмёт кнопку второй раз и третий.
func (s *server) stopWaitOn(tmux string) bool {
	if tmux == "" || !chatKeyRe.MatchString(tmux) {
		return false
	}
	return s.chatStoreRead("tmux-"+tmux).StopAt > 0
}

// projectPathOf находит корень проекта по имени. Пусто там, где имени не
// знают: заказ дожима от этого не пропадает, у него остаётся имя окна.
func (s *server) projectPathOf(name string) string {
	if name == "" {
		return ""
	}
	if p := s.projectNamed(name); p != nil {
		return p.Path
	}
	return ""
}

// stopWaitOne это один заход сторожа по заказу. Дорог четыре: окна не стало,
// поднялся новый ход, фоновая работа ещё идёт, работа встала.
func (s *server) stopWaitOne(name string, alive func(string) bool) {
	st := s.chatStoreRead("tmux-" + name)
	if st.StopAt == 0 {
		return
	}
	now, since := s.now(), time.Unix(st.StopAt, 0)
	if !alive(name) {
		s.stopWaitDone(name, st, "окно разговора закрылось")
		return
	}
	turn, work := false, false
	if st.StopPath != "" {
		last, _ := sessionBusyTail(st.StopPath)
		turn = last.After(since)
		work = subBusyOf(st.StopPath, now)
	}
	// Ход поднялся снова: субагент вернул работу и разбудил агента. Это и есть
	// тот случай, ради которого заказ живёт: второй стоп человека, нажатый
	// руками в этот момент, срабатывал, а первый уходил в пустоту.
	if turn {
		if err := chatStop(name); err != nil {
			s.logf("дожим стопа в %s не подался: %v", name, err)
			return
		}
		st.StopAt = now.Unix()
		if err := s.chatStoreWrite("tmux-"+name, st); err != nil {
			s.logf("дожим стопа для %s не запомнился: %v", name, err)
		}
		s.logf("стоп %s: ход разговора %s поднялся снова и прерван дожимом", stopWaitWhat(st), name)
		return
	}
	if work {
		if now.Sub(since) < stopWaitTTL {
			return
		}
		s.stopWaitDone(name, st, fmt.Sprintf("фоновая работа не встала за %s", stopWaitTTL))
		return
	}
	s.stopWaitDone(name, st, "фоновая работа встала")
}

// stopWaitWhat подписывает заказ в журнале: задачей, а при стопе из панели
// чата, где задачи нет, именем самого разговора.
func stopWaitWhat(st chatStore) string {
	if st.StopTask != "" {
		return st.StopTask
	}
	return "разговора " + st.StopSid
}

// stopWaitDone кончает заказ: привязка сессии к задаче снимается, лента
// разговора получает слова, уведомитель повод. Всё это откладывалось ровно до
// этой минуты, потому что раньше неё работа шла.
func (s *server) stopWaitDone(name string, st chatStore, why string) {
	if st.StopTask != "" && st.StopSid != "" {
		if err := s.stopChatWorkRelease(st.StopSid, st.StopTask, st.StopProject, name); err != nil {
			s.logf("стоп %s: привязка сессии к задаче не снялась: реестр %s не записался: %v",
				st.StopTask, s.bindsPath(), err)
		}
		s.saidMark(saidSessionKey(st.StopSid), stopChatWord(st.StopTask))
		if p := s.projectNamed(st.StopProject); p != nil {
			if note := s.sayStop(p.Path, p.Name, st.StopTask, "chat"); note != "" {
				s.logf("стоп %s: %s", st.StopTask, note)
			}
		}
	}
	s.logf("стоп %s: %s, дожим кончен (окно %s)", stopWaitWhat(st), why, name)
	st.StopAt, st.StopSid, st.StopTask, st.StopProject, st.StopPath = 0, "", "", "", ""
	if err := s.chatStoreWrite("tmux-"+name, st); err != nil {
		s.logf("конец дожима стопа для %s не запомнился: %v", name, err)
	}
}

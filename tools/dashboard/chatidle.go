package main

import (
	"fmt"
	"time"
)

// Срок жизни живого чата (DK-828): разговор, в котором ход не шёл дольше
// порога, дашборд снимает сам той же точкой chatKill, что и уборку в архив.
// Без этого процессы клиента по 200-300 МБ копятся неделями, пока машина не
// уходит в своп, а снять их нечем, кроме kill по pid. Разговор при снятии не
// гибнет: транскрипт остаётся, следующая реплика поднимает продолжение
// резюмом.

const (
	// chatIdleStep это обычный шаг обхода. Порог меряется сутками, и чаще
	// десяти минут обходить нечего.
	chatIdleStep = 10 * time.Minute
	// chatIdleMinStep это нижняя граница шага. Порог из машинного конфига
	// бывает и минутным, и обход обязан успеть за него, иначе живая проверка
	// снятия ждала бы тик дольше самого порога.
	chatIdleMinStep = 20 * time.Second
)

// chatIdleAfter отдаёт порог простоя из машинного конфига. Ноль значит «ключа
// chat_idle не было», и работает умолчание, названное в README.
func (s *server) chatIdleAfter() time.Duration {
	if s.cfg.ChatIdle > 0 {
		return s.cfg.ChatIdle
	}
	return chatIdleDefault
}

// chatIdleKeeper это круг снятия за простой, по устройству тот же сторож, что
// и у окон (chatwatch.go). Первый обход идёт сразу: разговор мог простоять
// дольше порога, пока демона не было, и ждать первого тика значит копить хвост
// дальше.
func (s *server) chatIdleKeeper(stop <-chan struct{}) {
	step := chatIdleStep
	if after := s.chatIdleAfter(); after/3 < step {
		step = after / 3
		if step < chatIdleMinStep {
			step = chatIdleMinStep
		}
	}
	t := time.NewTicker(step)
	defer t.Stop()
	s.chatIdleTick()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.chatIdleTick()
		}
	}
}

// chatIdleTick это один обход. Список разговоров машины собирается той же
// свёрткой, что и панель: у разговоров конвейера и цели (признак hidden) своего
// окна в нём нет, и их сессии снимает собственный ход, а не порог простоя.
// Сорванный опрос tmux пропускает обход целиком, как у сторожа окон (DK-904):
// ответ «спросить не удалось» это не «окна нет».
func (s *server) chatIdleTick() {
	if tmuxMissingCheck() != "" {
		return
	}
	roll, err := tmuxRollAsk()
	if err != nil {
		s.logf("обход простоя пропущен, опрос tmux сорван: %v", err)
		return
	}
	list, _ := s.chatEntriesAll(0, chatWindow{})
	projs := map[string]string{}
	if ps, _ := s.projects(); len(ps) > 0 {
		for _, p := range ps {
			projs[p.Name] = p.Path
		}
	}
	live := s.peers()
	binds := s.binds()
	now, after := s.now(), s.chatIdleAfter()
	for _, e := range list {
		// Архивный разговор убрал сам человек, и второй раз его снимать нечем:
		// окна за ним либо нет, либо оно поднято заново чужим разговором.
		if e.Archived || e.Tmux == "" || !roll.alive(e.Tmux) {
			continue
		}
		path := projs[e.Project]
		if path == "" {
			continue
		}
		// Занятость и время последнего хода считаются той же свёрткой, что и
		// у работ на экране «Агенты», и меряются разговором самой строки, по
		// его сессии. Имя окна к замеру не подключается: под ним к этому часу
		// может жить уже чужой разговор, и свёртка отмерила бы чужой ход.
		state, moved, _ := s.workState(path, "", e.ID, "", live, nil)
		if state == workBusy || state == workWait {
			continue
		}
		// Вопрос, адресованный разговору, держит окно: ответ человека доезжает
		// в разговор, чья сессия поднята, и убивать его под припаркованным
		// вопросом значит терять ответ.
		if len(handedAsks(path, e.ID, binds, now)) > 0 {
			continue
		}
		if moved == 0 {
			// Времени последнего хода нет вовсе: простой не доказан, и снятие
			// по нему было бы догадкой.
			continue
		}
		idle := now.Sub(time.Unix(moved, 0))
		if idle < after {
			continue
		}
		// Окно до сюда доходит только свободным либо своим. Чужого жильца в
		// имени разбирает сама склейка списка раньше обхода: слово живого
		// клиента она выставляет из имени с пометкой Gone (DK-673), а
		// переподнятое имя срезает вовсе (DK-859), и обход застаёт разговор
		// без окна.
		s.chatWatchOff(e.Tmux)
		if err := chatKill(e.Tmux); err != nil {
			s.logf("чат %s простоял %s, но окно %s не снялось, процесс остался: %v",
				e.ID, chatIdleAge(idle), e.Tmux, err)
			continue
		}
		s.saidMark(saidSessionKey(e.ID), fmt.Sprintf(
			"разговор простоял %s без хода: дашборд снял окно %s сам, разговор продолжится следующей репликой",
			chatIdleAge(idle), e.Tmux))
		s.logf("чат %s снят за простой: ход не шёл %s, окно %s", e.ID, chatIdleAge(idle), e.Tmux)
	}
}

// chatIdleAge говорит простой человеческими словами. Часы в строке при пороге
// в сутках читаются хуже суток, поэтому дни названы сутками.
func chatIdleAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч %d мин", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d сут %d ч", int(d.Hours())/24, int(d.Hours())%24)
	}
}

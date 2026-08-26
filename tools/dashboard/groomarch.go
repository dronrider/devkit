package main

import (
	"regexp"
	"strings"
	"time"
)

// Уборка чатов груминга. Разбор десяти черновиков оставляет десять мёртвых
// разговоров, и человек убирает их руками по одному (замечание пользователя).
// Дашборд убирает их сам, но только там, где ошибиться нельзя: разбор оставил
// след на диске, человека никто не ждёт, и непрочитанного в чате нет.
//
// Механика уборки общая с рукой (chatArchive): признак ложится в память
// диалога, живая сессия снимается. Второй дороги в архив у дашборда нет.
//
// Конвейер задачи сюда не входит нарочно: обкатывается это на груминге.

// groomChatID отдаёт ID черновика, который разбирал этот разговор, и пустую
// строку у всех прочих. Узнаётся разбор по первой реплике: заказ пишет сам
// дашборд (groomPrompt), и слова его те же, по которым такие транскрипты уже
// отсеиваются из живых работ (groomOrderPrefix). Второго признака у разбора
// нет: имя tmux-сессии у него общее с конвейером задачи.
func groomChatID(e chatEntry) string {
	said := strings.TrimSpace(e.First)
	if !strings.HasPrefix(said, groomOrderPrefix) {
		return ""
	}
	// ID стоит первым словом после заказа, дальше идут слова про вопросы, и
	// общее сито ID тут не годится: оно меряет строку целиком.
	return groomIDRe.FindString(strings.TrimPrefix(said, groomOrderPrefix))
}

var groomIDRe = regexp.MustCompile(`^[A-Za-z]+-[0-9]+`)

// groomFresh это срок, в который свежий ответ агента считается непрочитанным,
// когда отметки показа у разговора нет вовсе. Отметку ставит чтение ленты
// (chatSeenMark), и до первого показа её нет ни у одного разговора.
const groomFresh = 15 * time.Minute

// groomDone отвечает, оставил ли разбор твёрдый след на диске. Твёрдых исходов
// три: строка заведена, приписка сделана, черновик удалён. Все три уносят файл
// записи из накопителя, а первый ставит строку на доску. Отложенный с вопросом
// исходом не считается: файл его остаётся лежать.
//
// Смотрим по факту, а не по словам агента: сказать «завёл строку» он может и
// не заведя её.
func (s *server) groomDone(projPath, id string) bool {
	if !draftHere(projPath, id) {
		return true
	}
	raw, err := s.projectBoard(projPath)
	if err != nil {
		return false
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return false
	}
	_, hit := rows[id]
	return hit
}

// groomSweepable отвечает, можно ли убрать этот разговор груминга, и называет
// причину отказа для журнала.
func (s *server) groomSweepable(projPath string, e chatEntry, id string) (bool, string) {
	if e.Archived {
		return false, "уже в архиве"
	}
	// Идущий ход не трогаем: уборка снимает сессию, а снимать её посреди
	// работы значит обрывать разбор.
	if e.State == "live" && !e.Idle {
		return false, "агент работает"
	}
	if _, waits := askWaiting(projPath, id, s.now()); waits {
		return false, "агент ждёт человека"
	}
	if !s.groomDone(projPath, id) {
		return false, "твёрдого исхода нет"
	}
	if s.groomUnread(e) {
		return false, "в чате непрочитанное"
	}
	return true, ""
}

// groomUnread отвечает, есть ли в разговоре непрочитанное. Своей отметки
// прочтения у нас нет, и мера тут простая: последняя запись разговора свежее
// последнего показа. Разговор, который ни разу не открывали, судится сроком:
// свежий хвост считается непрочитанным, старый прочитанным.
func (s *server) groomUnread(e chatEntry) bool {
	said, err := time.Parse(time.RFC3339, e.Mtime)
	if err != nil {
		return false
	}
	seen := s.chatStoreRead(e.ID).Seen
	if seen > 0 {
		return said.After(time.Unix(seen, 0))
	}
	return s.now().Sub(said) < groomFresh
}

// groomSweep убирает разговоры груминга, у которых разбор кончился. Зовётся с
// экрана накопителя: разбирают черновики оттуда, и там же человек видит исход.
func (s *server) groomSweep(projPath string) {
	for _, e := range s.chatEntries(projPath, chatListLimit) {
		id := groomChatID(e)
		if id == "" {
			continue
		}
		ok, why := s.groomSweepable(projPath, e, id)
		if !ok {
			if why != "уже в архиве" {
				s.logf("чат груминга %s (%s) остаётся в списке: %s", id, e.ID, why)
			}
			continue
		}
		if _, err := s.chatArchive(e.ID, true); err != nil {
			s.logf("чат груминга %s (%s) не убрался в архив: %v", id, e.ID, err)
			continue
		}
		s.logf("чат груминга %s (%s) убран в архив сам: разбор кончился", id, e.ID)
	}
}

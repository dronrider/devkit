package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
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
// живёт, пока жива работа. Сторож разговоров держит его на глазах, поднявшийся
// ход дожимает теми же Escape, а привязку снимает только тогда, когда
// останавливать больше нечего. До тех пор строка доски честно стоит под
// «Стопом»: работа по ней идёт.

const (
	// stopWaitTTL это срок заказа, и считается он от первого нажатия. Работа,
	// не вставшая за него, снимается со строки словами: держать «Стоп» вечно
	// нельзя, а врать про идущую работу тем более. Четверть часа это дольше
	// любого субагента, которого стоит дожидаться, и короче рабочего перерыва
	// человека.
	stopWaitTTL = 15 * time.Minute
	// stopWaitSettle это тишина после нажатия, за которую в транскрипт успевает
	// лечь запись самого прерывания. Считается она из рубежа занятости и шага
	// сторожа: без паузы сторож принимал бы за поднявшийся ход собственный след
	// Escape и дожимал бы в пустоту на первом же заходе.
	stopWaitSettle = busyFresh + chatWatchStep
)

// subBusyOf отвечает, жива ли фоновая работа сессии. Знание машинное: журнал на
// каждый вызов субагента заводит сам харнес.
//
// Мерок три, и хватает любой. Первые две те же, что у кольца пульса (subWorks в
// sessions.go): журнал писался не дольше subFresh назад, либо ответа на вызов в
// транскрипте нет вовсе и журнал моложе subStale. Третья своя, и она про долгий
// инструмент. Субагент, ушедший в сборку или в прогон тестов, журнала не
// трогает минутами, а работать не перестал; в хвосте его журнала при этом висит
// вызов без ответа, и спрашивается он тем же способом, каким меряется ход
// самого разговора (busyEntryOf). Без третьей мерки стоп считал бы такую работу
// вставшей и снимал привязку посреди неё, то есть возвращал бы ту самую беду,
// которую нашла приёмка.
//
// Метка возврата в мете старше всех трёх: её пишет тот, кто работу ждал.
func (s *server) subBusyOf(path string, now time.Time) bool {
	ents, err := os.ReadDir(subDir(path))
	if err != nil {
		return false
	}
	var closed map[string]bool
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		// Журнал, молчащий дольше получаса, работой не считается ни по одной
		// мерке, и меты у него не спрашивают: у долгой сессии таких журналов
		// десятки, а сборка доски ходит сюда по кругу.
		quiet := now.Sub(fi.ModTime())
		if quiet > subStale {
			continue
		}
		file := filepath.Join(subDir(path), e.Name())
		meta := subMetaOf(strings.TrimSuffix(file, ".jsonl") + ".meta.json")
		if strings.TrimSpace(meta.Ended) != "" {
			continue
		}
		if quiet <= subFresh {
			return true
		}
		if tail := s.busyEntryOf(file); tail.open > 0 {
			return true
		}
		if closed == nil {
			closed = transcriptDigest(path).closed
		}
		if meta.ToolID != "" && !closed[meta.ToolID] {
			return true
		}
	}
	return false
}

// subMetaOf читает мету бокового журнала. Пустая мета это обычный случай: файла
// может не быть вовсе, и работа тогда судится по одному журналу.
func subMetaOf(path string) subMeta {
	var m subMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	json.Unmarshal(data, &m)
	return m
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
	return s.subBusyOf(info.path, s.now())
}

// stopWaitSet ставит заказ дожима на окно разговора. Задача с проектом нужны
// концу: по ним снимается привязка и зовётся уведомитель. У стопа из самой
// панели чата задачи нет вовсе, и заказ там держит только дожим хода. Корень
// проекта приходит параметром от зовущего: он его уже нашёл, а второй поиск по
// имени отдавал бы заказ без транскрипта там, где имя не сошлось.
func (s *server) stopWaitSet(tmux, sid, task, project, projPath string) {
	if tmux == "" || !chatKeyRe.MatchString(tmux) {
		return
	}
	path := ""
	if info, ok := findSession(s.transcriptRoots(), projPath, sid); ok {
		path = info.path
	}
	if path == "" {
		s.logf("заказ дожима стопа для %s не поставлен: транскрипта сессии %s не нашлось", tmux, sid)
		return
	}
	key := "tmux-" + tmux
	st := s.chatStoreRead(key)
	now := s.now().Unix()
	st.StopAt, st.StopFrom = now, now
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
	s.logf("дожим стопа для %s снят: в разговор пришли слова человека", tmux)
	stopWaitClear(&st)
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("снятие дожима стопа для %s не запомнилось: %v", tmux, err)
	}
}

// stopWaitOffSaid снимает заказ по ключу журнала разговора. Стоит она в общей
// точке доставки человеческих слов (saidSay): дорог у реплики полдесятка, от
// клавиш в своё окно до сокета клиента, и снимать заказ в одной ручке значило
// бы гасить чужой ход молча.
func (s *server) stopWaitOffSaid(key string) {
	sid := strings.TrimPrefix(key, "sess-")
	if sid == key || sid == "" {
		return
	}
	if tmux := sessions.Last(s.bindsAll()[sid]).Tmux; tmux != "" {
		s.stopWaitOff(tmux)
	}
}

// stopWaitClear стирает заказ из записи разговора.
func stopWaitClear(st *chatStore) {
	st.StopAt, st.StopFrom = 0, 0
	st.StopSid, st.StopTask, st.StopProject, st.StopPath = "", "", "", ""
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
	// Ход поднялся снова: субагент вернул работу и разбудил агента. Это и есть
	// тот случай, ради которого заказ живёт: второй стоп человека, нажатый
	// руками в эту минуту, срабатывал, а первый уходил в пустоту. Идущим ход
	// считается по свежей записи транскрипта, и своя запись о прерывании под
	// эту мерку не попадает, её отделяет тишина stopWaitSettle.
	tail := s.busyEntryOf(st.StopPath)
	if now.Sub(since) >= stopWaitSettle && !tail.last.IsZero() && now.Sub(tail.last) < busyFresh {
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
	if s.subBusyOf(st.StopPath, now) {
		// Срок считается от первого нажатия, а не от последнего дожима: иначе
		// заказ, у которого ходы поднимаются один за другим, жил бы сколько
		// угодно, а строка стояла бы под «Стопом» вечно.
		if now.Sub(time.Unix(st.StopFrom, 0)) < stopWaitTTL {
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
	stopWaitClear(&st)
	if err := s.chatStoreWrite("tmux-"+name, st); err != nil {
		s.logf("конец дожима стопа для %s не запомнился: %v", name, err)
	}
}

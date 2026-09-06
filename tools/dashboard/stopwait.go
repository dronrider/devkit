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

// stopHold это живой заказ дожима в памяти процесса. Держится он ради второй
// беды четвёртой приёмки DK-716: агент, чей ход прерывают, успевает открыть
// этап командой доски, и строка становится рабочей уже после стопа. Остановка
// отменялась тем, кого останавливают. Пока заказ жив, такие взятия строке
// признака работы не дают.
type stopHold struct {
	tmux string
	task string
	from time.Time
}

// stopHoldSet запоминает заказ, stopHoldDrop забывает. Диск и память тут ходят
// парой: на диске заказ переживает перезапуск демона, а из памяти его читает
// сборка доски, и лезть за ним в файл по десятку раз на заход незачем.
func (s *server) stopHoldSet(sid, tmux, task string, from time.Time) {
	if sid == "" || task == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stops == nil {
		s.stops = map[string]stopHold{}
	}
	s.stops[sid] = stopHold{tmux: tmux, task: task, from: from}
}

func (s *server) stopHoldDrop(sid string) {
	if sid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.stops, sid)
}

// stopHolds отдаёт снимок живых заказов.
func (s *server) stopHolds() map[string]stopHold {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stops) == 0 {
		return nil
	}
	out := make(map[string]stopHold, len(s.stops))
	for sid, h := range s.stops {
		out[sid] = h
	}
	return out
}

// bindsWork читает реестр для счёта работы. Записи те же, что у bindsAll, минус
// взятия, легшие под живым заказом дожима: строку остановили, и вернуть её себе
// командой доски та же сессия не может, пока остановка идёт. Остальные читатели
// реестра берут его целиком: имя окна и адрес транскрипта нужны и у
// остановленной сессии.
func (s *server) bindsWork() map[string][]sessionBind {
	all := s.bindsAll()
	holds := s.stopHolds()
	for sid, h := range holds {
		recs, ok := all[sid]
		if !ok {
			continue
		}
		kept := make([]sessionBind, 0, len(recs))
		for _, r := range recs {
			if stopHushes(h, r, s.now().Location()) {
				continue
			}
			kept = append(kept, r)
		}
		all[sid] = kept
	}
	return all
}

// stopHushes отвечает, глушит ли заказ эту запись реестра. Глушится одно:
// взятие остановленной строки той же сессией, случившееся после нажатия.
// Привязка дерева и записи по соседним задачам остаются на месте: стоп снимает
// одну строку, а не всю работу сессии.
// Время записи стоит в ней без пояса, и читается оно в поясе тех же часов,
// какими живёт сервер: писатель и читатель тут одна машина.
func stopHushes(h stopHold, r sessionBind, loc *time.Location) bool {
	if r.Source != sessions.BySrc || r.Task != h.task {
		return false
	}
	at, err := time.ParseInLocation(bindStamp, r.Time, loc)
	return err == nil && !at.Before(h.from)
}

// stopRetaken отвечает, взяла ли сессия остановленную строку снова, пока шла
// остановка. Спрашивается это у нетронутого реестра: в bindsWork такие записи
// как раз и не видны.
func (s *server) stopRetaken(st chatStore) bool {
	if st.StopSid == "" || st.StopTask == "" {
		return false
	}
	h := stopHold{task: st.StopTask, from: time.Unix(st.StopFrom, 0)}
	for _, r := range s.bindsAll()[st.StopSid] {
		if stopHushes(h, r, s.now().Location()) {
			return true
		}
	}
	return false
}

// stopWaitSet ставит заказ дожима на окно разговора. Задача с проектом нужны
// концу: по ним снимается привязка и зовётся уведомитель. У стопа из самой
// панели чата задачи нет вовсе, и заказ там держит только дожим хода. Корень
// проекта приходит параметром от зовущего: он его уже нашёл, а второй поиск по
// имени отдавал бы заказ без транскрипта там, где имя не сошлось.
func (s *server) stopWaitSet(tmux, sid, task, project, projPath string, freed bool) {
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
	st.StopAt, st.StopFrom, st.StopFreed = now, now, freed
	st.StopSid, st.StopTask, st.StopProject, st.StopPath = sid, task, project, path
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("заказ дожима стопа для %s не запомнился: %v", tmux, err)
	}
	s.stopHoldSet(sid, tmux, task, time.Unix(now, 0))
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
	s.stopHoldDrop(st.StopSid)
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
	st.StopAt, st.StopFrom, st.StopFreed = 0, 0, false
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
	// Состояние окна спрашивается у самого клиента, снимком экрана. Прежде тут
	// стояла свежесть транскрипта, и сторож бил Escape по всему, что писалось
	// последние двадцать секунд. При остановленном ходе пара нажатий открывает
	// меню отката, и клиент оставался заперт в нём, а сторож долбил дальше
	// шагом в пять секунд (четвёртая приёмка DK-716).
	//
	// Тишина после нажатия остаётся: окно перерисовывается не мгновенно, и
	// первые секунды снимок показывает прежнее состояние.
	pane := chatPaneState(name)
	if pane == paneRewind {
		// Клиента застали в модальном окне. Закрывается оно тем же Escape, и
		// сделать это надо: человек в него не заходил, а выйти оттуда некому.
		if err := escape(name); err != nil {
			s.logf("меню клиента в %s не закрылось: %v", name, err)
			return
		}
		s.logf("стоп %s: окно %s стояло в меню клиента, оно закрыто", stopWaitWhat(st), name)
		return
	}
	// Окно, которое держит человек (вопрос агента, экран входа), ниже проходит
	// теми же дорогами, что и простой: клавиш туда не идёт вовсе, а заказ
	// кончается, когда встанет фоновая работа. Прежде оба этих окна читались
	// как меню отката, и сторож отменял вопрос агента каждые пять секунд, пока
	// жил заказ (замечание ревью 13).
	// Ход поднялся снова: субагент вернул работу и разбудил агента. Это и есть
	// тот случай, ради которого заказ живёт: второй стоп человека, нажатый
	// руками в эту минуту, срабатывал, а первый уходил в пустоту.
	if pane == paneTurn && now.Sub(since) >= stopWaitSettle {
		way, err := chatStop(name)
		if err != nil {
			s.logf("дожим стопа в %s не подался: %v", name, err)
			return
		}
		st.StopAt = now.Unix()
		if err := s.chatStoreWrite("tmux-"+name, st); err != nil {
			s.logf("дожим стопа для %s не запомнился: %v", name, err)
		}
		s.logf("стоп %s: ход разговора %s поднялся снова и %s дожимом", stopWaitWhat(st), name, way)
		return
	}
	// Ход идёт, а тишина после нажатия ещё не вышла: ждём её и не трогаем окно.
	if pane == paneTurn {
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
	// Снимок окна не читается: заказ не кончается по нему. Мёртвое окно ловит
	// проверка живости выше, а нечитаемый снимок живого окна это не довод
	// объявлять работу вставшей.
	if pane == paneBlind {
		if now.Sub(time.Unix(st.StopFrom, 0)) < stopWaitTTL {
			return
		}
		s.stopWaitDone(name, st, fmt.Sprintf("снимок окна не прочитался за %s", stopWaitTTL))
		return
	}
	if pane == paneAsk || pane == paneLogin {
		s.stopWaitDone(name, st, "хода нет, окно держит человек: "+pane)
		return
	}
	s.stopWaitDone(name, st, "хода нет, фоновая работа встала")
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
		// Привязку сняли ещё при нажатии, и слова об этом уже сказаны. Снимать
		// её второй раз нужно только тогда, когда сессия успела взять строку
		// снова: пока заказ жил, такое взятие не считалось, а последним словом
		// реестра должна остаться «снята» (четвёртая приёмка DK-716).
		retaken := st.StopFreed && s.stopRetaken(st)
		if !st.StopFreed || retaken {
			if err := s.stopChatWorkRelease(st.StopSid, st.StopTask, st.StopProject, name); err != nil {
				s.logf("стоп %s: привязка сессии к задаче не снялась: реестр %s не записался: %v",
					st.StopTask, s.bindsPath(), err)
			}
		}
		if retaken {
			s.logf("стоп %s: сессия %s взяла строку снова после нажатия, привязка снята повторно",
				st.StopTask, st.StopSid)
		}
		if !st.StopFreed {
			s.saidMark(saidSessionKey(st.StopSid), stopChatWord(st.StopTask))
			if p := s.projectNamed(st.StopProject); p != nil {
				if note := s.sayStop(p.Path, p.Name, st.StopTask, "chat"); note != "" {
					s.logf("стоп %s: %s", st.StopTask, note)
				}
			}
		}
	}
	s.logf("стоп %s: %s, дожим кончен (окно %s)", stopWaitWhat(st), why, name)
	s.stopHoldDrop(st.StopSid)
	stopWaitClear(&st)
	if err := s.chatStoreWrite("tmux-"+name, st); err != nil {
		s.logf("конец дожима стопа для %s не запомнился: %v", name, err)
	}
}

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/sessions"
)

// Заходы одного окна это один разговор (DK-723). Конвейер задачи идёт живой
// сессией, но остановы у неё прежние: потолок проходов, воронка молчания,
// вышедший клиент, перезагрузка машины. После каждого кнопка поднимает новую
// сессию со своим ID под тем же именем окна, и список, ключующий строку по ID
// сессии, показывал такие заходы отдельными строками с одинаковыми
// заголовками. Тут они сводятся в одну строку списка и в одну ленту.

// chatPass это прошлый заход разговора: сессия, кончившаяся под тем же именем
// окна, и время её последней реплики. Больше про неё списку знать нечего:
// заголовок у заходов один, а лента достаётся по ID.
type chatPass struct {
	ID    string `json:"id"`
	Mtime string `json:"mtime,omitempty"`
}

// chatWin это ключ склейки заходов: проект и имя окна. Имя тут берётся целиком
// и не разбирается: конвейер поднимается под task-<ID>, цикл цели под
// goal-<ID>, разговор под chat-<ID>-<n>, и склеивать по разобранной задаче
// нельзя вовсе. Задача склеила бы в одну строку конвейер, доводящий чат и окно
// человека, а это три разных разговора с тремя разными лентами. Проект стоит
// в ключе, потому что имя окна на машине переиспользуется: живой случай
// chat-DK-397-2, где имя носили сессии двух проектов сразу.
func chatWin(projPath, tmux string) string {
	name := strings.SplitN(tmux, ":", 2)[0]
	if name == "" {
		return ""
	}
	return projPath + "\x00" + name
}

// chatGlue склеивает заходы одного окна в одну строку списка (DK-723).
// Остановы у живой головы конвейера прежние (потолок проходов, воронка
// молчания, вышедший клиент, перезагрузка машины), и после каждого кнопка
// поднимает новую сессию со своим ID под тем же именем окна. Список ключевал
// строку по ID сессии, и такие заходы стояли отдельными строками с одинаковыми
// заголовками: человек не знал, в какую заходить, а история задачи собиралась
// по нескольким записям.
//
// Голова группы это тот заход, за которым имя закреплено: у прочих сборка выше
// уже сняла имя и назвала снятие словами (Gone). Не нашлось такого, значит
// голова первая по порядку, то есть самая свежая: список к этому месту уже
// отсортирован. Прошлые заходы уходят из списка в поле Past головы старшими
// первыми, тем же порядком, каким их читает лента, а задачи их доезжают до
// строки: заход, двигавший строку доски, назвал её своей записью реестра, и
// терять эту привязку вместе со строкой нельзя.
//
// Склейка держит потолок chatPassMax, тот же, каким мерит себя лента. Заход
// старше потолка остаётся своей строкой списка со словами про снятый разговор.
// Иначе история за потолком пропадала бы молча: чип строки считал бы девять
// заходов, лента показывала бы шесть, а дороги к остальным трём не было бы
// вовсе (замечание ревью 2).
func chatGlue(list []chatEntry) []chatEntry {
	keep := make([]chatEntry, 0, len(list))
	byWin := map[string][]chatEntry{}
	wins := []string{}
	for _, e := range list {
		if e.win == "" {
			keep = append(keep, e)
			continue
		}
		if _, seen := byWin[e.win]; !seen {
			wins = append(wins, e.win)
		}
		byWin[e.win] = append(byWin[e.win], e)
	}
	for _, win := range wins {
		grp := byWin[win]
		if len(grp) == 1 {
			keep = append(keep, grp[0])
			continue
		}
		at := 0
		for i, e := range grp {
			if e.Gone == "" {
				at = i
				break
			}
		}
		head := grp[at]
		rest := make([]chatEntry, 0, len(grp)-1)
		rest = append(rest, grp[:at]...)
		rest = append(rest, grp[at+1:]...)
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].Mtime < rest[j].Mtime })
		// Заход за потолком остаётся своей строкой списка. Лента головы тянет
		// столько же заходов, сколько считает строка, и молчаливой пропажи
		// истории тут нет: строка стоит на месте со словами про снятый
		// разговор, и лента у неё своя.
		if len(rest) > chatPassMax {
			keep = append(keep, rest[:len(rest)-chatPassMax]...)
			rest = rest[len(rest)-chatPassMax:]
		}
		for _, e := range rest {
			head.Past = append(head.Past, chatPass{ID: e.ID, Mtime: e.Mtime})
			for _, id := range e.Tasks {
				if !hasTask(head.Tasks, id) {
					head.Tasks = append(head.Tasks, id)
				}
			}
		}
		keep = append(keep, head)
	}
	sortEntries(keep)
	return keep
}

// chatPassMax это потолок прошлых заходов, чью ленту разговор тянет за собой.
// Заходов копится по числу остановов, а не по числу дней, и шесть это тот же
// потолок, каким мерит себя сам конвейер: дальше история читается открытием
// прошлого захода по его ID. Читать без потолка нельзя, лента разговора
// собирается на каждый заход панели.
const chatPassMax = 6

// chatPasses называет прошлые заходы сессии старшими первыми: сессии, чья
// последняя запись реестра носит то же имя окна и легла раньше. Своё имя
// сессия могла и потерять (его занял следующий заход), поэтому берётся имя из
// её собственной последней записи, а не владение именем.
func (s *server) chatPasses(sid string) []chatPass {
	recs := s.bindsAll()
	last := sessions.Last(recs[sid])
	name := strings.SplitN(last.Tmux, ":", 2)[0]
	if name == "" {
		return nil
	}
	out := []chatPass{}
	for id, rs := range recs {
		if id == sid {
			continue
		}
		rec := sessions.Last(rs)
		if strings.SplitN(rec.Tmux, ":", 2)[0] != name || rec.Time > last.Time {
			continue
		}
		out = append(out, chatPass{ID: id, Mtime: rec.Time})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Mtime != out[j].Mtime {
			return out[i].Mtime < out[j].Mtime
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > chatPassMax {
		out = out[len(out)-chatPassMax:]
	}
	return out
}

// passMarkWord подписывает границу захода в ленте. Заход назван своим ID: по
// нему разговор открывается отдельно, и человеку видно, что лента склеена, а
// не перепутана.
func passMarkWord(n, of int, id string) string {
	return fmt.Sprintf("заход %d из %d, сессия %s", n, of, id)
}

// passMark это разделитель ленты между заходами. Роль тут та же, что у пометки
// смены модели: рубеж разговора рисуется чертой, а не пузырём. Ключ устойчивый,
// как у всякой записи ленты: пагинация назад режет ленту по ключу.
func passMark(n, of int, id, at string) reply {
	return reply{
		Role: roleMark, Time: at, Text: passMarkWord(n, of, id),
		Key: "pass-" + id + ":0",
	}
}

// passKey уводит ключи записей прошлого захода из-под ключей нынешнего. Ключ
// записи транскрипта это смещение в своём файле («m:0.0» у первой записи), и у
// каждого захода файл свой. В склеенной ленте такие ключи совпадали, панель
// считала запись повтором и рисовала из трёх заходов один, а страница истории
// просилась по чужому ключу (замечание ревью 1). Заход тут и есть то, чем
// запись отличается от соседней, поэтому его ID стоит в приставке.
func passKey(id, key string) string {
	if key == "" {
		return ""
	}
	return "pass-" + id + "/" + key
}

// passPart это лента одного прошлого захода: его ID и записи с уже своими
// ключами.
type passPart struct {
	id    string
	items []reply
}

// passFeed приклеивает к ленте разговора ленты прошлых заходов, старшими
// сверху, с разделителем перед каждым. История задачи читается тогда сверху
// вниз одной лентой, без перехода в другой чат.
//
// Клеится только у ленты, доехавшей до начала своего транскрипта: пока окно
// стоит на хвосте, прошлому заходу в нём места нет, а страница истории просит
// окно шире, пока не упрётся в начало. Второй возврат это «дальше просить
// нечего»: он верен, только когда все заходы прочитаны целиком.
func (s *server) passFeed(projPath, sid string, items []reply, want int, whole bool) ([]reply, bool) {
	if !whole {
		return items, false
	}
	passes := s.chatPasses(sid)
	if len(passes) == 0 {
		return items, true
	}
	roots := s.transcriptRoots()
	parts := make([]passPart, 0, len(passes))
	all := true
	for _, p := range passes {
		info, ok := findSession(roots, projPath, p.ID)
		if !ok {
			// Транскрипта нет: заход шёл в чужом проекте под тем же именем
			// окна либо его файл убрали. Ленты у такого захода нет, и строкой
			// в разговоре он не встаёт.
			continue
		}
		feed := sessionFeedOf(info.path, want)
		part := saidMerge(feed.items,
			saidCut(saidLoad(s.cfg.Home, saidSessionKey(p.ID)), feedFrom(feed.items, feed.whole)))
		if len(part) == 0 {
			continue
		}
		if !feed.whole {
			all = false
		}
		for i := range part {
			part[i].Key = passKey(p.ID, part[i].Key)
		}
		parts = append(parts, passPart{id: p.ID, items: part})
	}
	if len(parts) == 0 {
		return items, true
	}
	// Номер разделителя считается по вставленным заходам, а не по перечню
	// реестра: пропущенный заход иначе оставлял в ленте дыру в нумерации,
	// «заход 1 из 4» рядом с «заход 3 из 4» (замечание ревью 4).
	of := len(parts) + 1
	out := make([]reply, 0, len(items)+want)
	for i, part := range parts {
		out = append(out, passMark(i+1, of, part.id, part.items[0].Time))
		out = append(out, part.items...)
	}
	at := ""
	if len(items) > 0 {
		at = items[0].Time
	}
	out = append(out, passMark(of, of, sid, at))
	return append(out, items...), all
}

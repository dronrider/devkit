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
func chatGlue(list []chatEntry) []chatEntry {
	head := map[string]int{}
	out := make([]chatEntry, 0, len(list))
	for _, e := range list {
		if e.win == "" {
			out = append(out, e)
			continue
		}
		at, seen := head[e.win]
		if !seen {
			head[e.win] = len(out)
			out = append(out, e)
			continue
		}
		// Имя закреплено за этим заходом, а строка группы уже стоит: голова
		// меняется местами с прошлым заходом. Свежесть тут не мера, живой
		// заход бывает и тише мёртвого соседа.
		if e.Gone == "" && out[at].Gone != "" {
			e.Past, out[at].Past = out[at].Past, nil
			e, out[at] = out[at], e
		}
		out[at].Past = append(out[at].Past, chatPass{ID: e.ID, Mtime: e.Mtime})
		for _, id := range e.Tasks {
			if !hasTask(out[at].Tasks, id) {
				out[at].Tasks = append(out[at].Tasks, id)
			}
		}
	}
	for i := range out {
		sort.SliceStable(out[i].Past, func(a, b int) bool {
			return out[i].Past[a].Mtime < out[i].Past[b].Mtime
		})
	}
	return out
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
	out := make([]reply, 0, len(items)+want)
	all := true
	for i, p := range passes {
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
		out = append(out, passMark(i+1, len(passes)+1, p.ID, part[0].Time))
		out = append(out, part...)
	}
	if len(out) == 0 {
		return items, true
	}
	at := ""
	if len(items) > 0 {
		at = items[0].Time
	}
	out = append(out, passMark(len(passes)+1, len(passes)+1, sid, at))
	return append(out, items...), all
}

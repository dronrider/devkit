package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Полка ждущих: все задачи и разговоры машины, которые стоят на вопросе
// человеку, одним списком (DK-696). Состояние ожидания у строки доски считалось
// и раньше (waiting.go), но собранного места у него не было: человек шёл в
// Blocked и сверял строки глазами, а свёрнутая секция добавляла к этому ещё
// один щелчок. Полка это тот же расчёт, поднятый до машины: строки всех досок и
// признаки ожидания, за которыми строки нет вовсе.

// WaitItem это одна ждущая запись полки. Waiting тут ровно тот же, что у строки
// доски, и вторым разбором ожидания полка не заводится. Addr это адрес
// разговора для панели: ID ждущей сессии, если он известен, иначе ID задачи,
// который панель читает как «последний разговор этой задачи» (LLD DK-430,
// решение 5).
type WaitItem struct {
	Project string  `json:"project"`
	ID      string  `json:"id,omitempty"`
	Title   string  `json:"title,omitempty"`
	Addr    string  `json:"addr"`
	Waiting Waiting `json:"waiting"`
}

// waitShelfItem собирает запись полки из состояния ожидания: адрес выбирается
// один раз и на сервере, потому что правило выбора («сессия точнее задачи»)
// одно на все источники, а собранное клиентом оно разъезжалось бы по местам.
func waitShelfItem(project, id, title string, w Waiting) (WaitItem, bool) {
	addr := w.Session
	if addr == "" {
		addr = id
	}
	if addr == "" {
		// Открывать нечего: ни сессии, ни задачи. Такая запись увела бы в
		// пустую панель, и молчание там человек читал бы как сломанную дорогу.
		return WaitItem{}, false
	}
	return WaitItem{Project: project, ID: id, Title: title, Addr: addr, Waiting: w}, true
}

// projectWaits собирает ждущих одного проекта. Сначала строки доски, всеми
// тремя источниками сразу (waitLookup), потом живые признаки ожидания, чьей
// строки на доске не нашлось: вопрос грумера черновика и вопрос сессии,
// раздавшей работу, ждут человека наравне со строкой, а в Blocked их нет.
func (s *server) projectWaits(p Project) ([]WaitItem, error) {
	var out []WaitItem
	seen := map[string]bool{}
	raw, err := s.projectBoard(p.Path)
	if err == nil {
		var secs []boardSect
		if secs, err = parseBoardSects(raw); err == nil {
			look := s.waitLookup(p.Path)
			for _, sec := range secs {
				for _, row := range sec.Rows {
					w, ok := look(row.ID, sec.Key, row.Block)
					if !ok {
						continue
					}
					seen[row.ID] = true
					if it, ok := waitShelfItem(p.Name, row.ID, row.Title, w); ok {
						out = append(out, it)
					}
				}
			}
		}
	}
	for _, h := range askScan(p.Path, s.now(), nil) {
		if h.Ask.Task != "" && seen[h.Ask.Task] {
			continue
		}
		if it, ok := waitShelfItem(p.Name, h.Ask.Task, "", handedWaiting(h)); ok {
			out = append(out, it)
		}
	}
	return out, err
}

// waitShelf это полка целиком, по всем доскам машины. Отказ одного проекта
// остальных не отменяет: причина едет строкой ошибки рядом со списком, потому
// что пустая полка при нечитаемой доске это не «никто не ждёт».
func (s *server) waitShelf() ([]WaitItem, []string) {
	projects, errs := s.projects()
	out := []WaitItem{}
	for _, p := range projects {
		items, err := s.projectWaits(p)
		out = append(out, items...)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Name, err))
		}
	}
	// Дольше всех ждущий стоит первым: полка отвечает на вопрос «кому отвечать
	// сейчас», и порядок тут это и есть ответ. Запись без момента начала
	// (парковка, повод из журнала) уходит в конец: сравнивать её со знающими
	// своё время не по чему, а нулём она встала бы во главе списка.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Waiting.Since, out[j].Waiting.Since
		if (a == 0) != (b == 0) {
			return b == 0
		}
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, errs
}

// askScan обходит признаки ожидания проекта: живые, с разобранным моментом
// начала. keep отбирает нужные, пустой отбор берёт все. Обход тут один на два
// места: панель разговора спрашивает про свою сессию (handedAsks), а полка про
// всю машину.
func askScan(projPath string, now time.Time, keep func(chat.Ask) bool) []handedAsk {
	entries, err := os.ReadDir(chat.Root(projPath))
	if err != nil {
		return nil
	}
	var out []handedAsk
	for _, e := range entries {
		name, found := strings.CutSuffix(e.Name(), chat.AskSuffix)
		if !found || e.IsDir() {
			continue
		}
		path := chat.AskPath(projPath, name)
		a, ok := chat.ReadAsk(path)
		if !ok || !a.Live(now) {
			continue
		}
		if keep != nil && !keep(a) {
			continue
		}
		h := handedAsk{Name: name, Ask: a}
		if fi, err := os.Stat(path); err == nil {
			h.Since = fi.ModTime().Unix()
		}
		out = append(out, h)
	}
	// Порядок по времени файла, а не по сроку: признак без срока (DK-715) не
	// назовёт, кто спросил раньше, а mtime это тот же момент, когда встал сам
	// вопрос.
	sort.Slice(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	return out
}

// handleWaitShelf отдаёт полку целиком. Ручка машинная, а не проектная: место
// ждущих одно на дашборд, и открывается оно с любого экрана, в том числе с
// главной, где своего проекта нет вовсе.
func (s *server) handleWaitShelf(w http.ResponseWriter, r *http.Request) {
	items, errs := s.waitShelf()
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "errors": errs})
}

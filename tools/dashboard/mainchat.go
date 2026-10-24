package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/mainchat"
)

// Ручка адреса основного чата записи (LLD DK-1422, решения 1 и 2). Адрес это
// ID записи, а журнал назначений ~/.devkit/main-chat.log говорит, какой разговор
// ему принадлежит. Кнопки списка и формы зовут эту ручку одну, и разговор
// находится после закрытия строки, простоя за порогом живости и перезапуска
// дашборда: носитель меняется строкой журнала, адрес остаётся. Просмотр
// разговора его не создаёт и разработку не запускает.

// handleTaskChat разрешает адрес записи в действующее назначение. Пустой chat
// в ответе значит, что разговора у записи нет: панель показывает состояние и
// отдельное действие запуска. Отрезки основной ленты едут порядком строк
// журнала, подписи на их стыках ложатся отдельно (DK-871).
func (s *server) handleTaskChat(w http.ResponseWriter, r *http.Request) {
	found, id, _, _, ok := s.taskRowOf(w, r, true)
	if !ok {
		return
	}
	id = strings.ToUpper(id)
	data := s.mainChatData()
	recs := mainchat.All(data)[id]
	resp := map[string]any{"id": id, "project": found.Name, "chat": "",
		"segments": mainchat.Segments(recs)}
	if n := len(recs); n > 0 {
		resp["chat"] = recs[n-1].Chat
		resp["source"] = recs[n-1].Source
		resp["since"] = recs[n-1].Time
	}
	writeJSON(w, http.StatusOK, resp)
}

// mainChatData склеивает журналы назначений обоих домов тем же порядком, что
// реестр сессий: назначение, поставленное прежней сборкой либо мимо дашборда,
// лежит в доме машины, и без второго дома адрес смотрел бы в пустоту.
func (s *server) mainChatData() []byte {
	var out []byte
	for _, home := range s.bindHomes() {
		data, err := os.ReadFile(mainchat.Path(home))
		if err != nil {
			continue
		}
		out = append(out, data...)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	return out
}

// mainChatAssign ставит назначение в журнал дома дашборда. Отказ журнала не
// роняет запуск: работа уже поднята и видна живыми признаками, а адрес без
// строки журнала деградирует до прежнего разрешения, и строчка уходит в журнал
// сервера, где её найдёт диагностика.
func (s *server) mainChatAssign(id, chat, project, source string) {
	if err := mainchat.Put(s.cfg.Home, mainchat.Bind{
		Record: id, Chat: chat, Project: project, Source: source}); err != nil {
		s.logf("назначение основного чата %s не записалось: %v", id, err)
	}
}

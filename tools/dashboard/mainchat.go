package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/sessions"
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

// mainChatBind сворачивает журнал в действующее назначение записи: та же
// свёртка, которой читает адрес ручка записи, а пустой Chat значит, что
// назначения нет либо оно снято.
func (s *server) mainChatBind(id string) mainchat.Bind {
	return mainchat.Parse(s.mainChatData())[strings.ToUpper(strings.TrimSpace(id))]
}

// mainChatMove смещает назначение строки «передача» (LLD DK-1422, решение 3):
// прежний носитель уезжает полем «прежде», и реплика, вошедшая следом, ложится
// безадресно по решению 4. Отказ журнала не роняет передачу: работа видна
// живыми признаками, а адрес без строки деградирует до прежнего разрешения.
func (s *server) mainChatMove(id, chat, prior, project string) {
	if err := mainchat.Put(s.cfg.Home, mainchat.Bind{
		Record: id, Chat: chat, Prior: prior, Project: project, Source: mainchat.ByMove}); err != nil {
		s.logf("передача основного чата %s не записалась: %v", id, err)
	}
}

// mainChatFollow ведёт назначение за поднятым заходом: «Продолжить работу»
// поднимает новый заход того же разговора, и носителем становится он. Без
// назначения строки нет: запись, которую журнал не знает, подъём не
// назначает, разбор таких записей это миграция DK-1432.
func (s *server) mainChatFollow(id, chat, project string) {
	bind := s.mainChatBind(id)
	if bind.Chat == "" || bind.Chat == chat {
		return
	}
	s.mainChatMove(id, chat, bind.Chat, project)
}

// chatHandedOver отвечает, снята ли голова разговора строкой передачи (LLD
// DK-1422, решение 4): такая голова адресатом не считается, хотя её ход
// доходит до конца. Ключи головы это ID сессии и имя её окна: у конвейера
// носителем служит само имя, и снятие головы окна снимает разговоры в нём.
// Назначение могло вернуться к той же сессии позже, поэтому строки записи
// сканируются порядком, а решает последняя строка о разговоре: возвращение
// узнаётся по назначению сессии, окно же вернувшимся носителем не считается,
// в новом заходе окна работает другой разговор.
func (s *server) chatHandedOver(info sessionInfo, rec sessions.Bind) bool {
	if info.Task == "" {
		return false
	}
	window := ""
	if rec.Tmux != "" {
		window = "tmux-" + rec.Tmux
	}
	handed := false
	for _, b := range mainchat.All(s.mainChatData())[info.Task] {
		if b.Chat == info.ID {
			handed = false
		}
		if b.Source != mainchat.ByMove || b.Time < rec.Time {
			continue
		}
		if b.Prior == info.ID || (window != "" && b.Prior == window) {
			handed = true
		}
	}
	return handed
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

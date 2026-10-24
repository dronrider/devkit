package main

import (
	"net/http"
	"os"
	"sort"
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
// журнала с подписями ролей и этапов на стыках (DK-871).
func (s *server) handleTaskChat(w http.ResponseWriter, r *http.Request) {
	found, id, _, _, ok := s.taskRowOf(w, r, true)
	if !ok {
		return
	}
	id = strings.ToUpper(id)
	data := s.mainChatData()
	recs := mainchat.All(data)[id]
	resp := map[string]any{"id": id, "project": found.Name, "chat": "",
		"segments": s.mainChatSegments(recs)}
	if n := len(recs); n > 0 {
		resp["chat"] = recs[n-1].Chat
		resp["source"] = recs[n-1].Source
		resp["since"] = recs[n-1].Time
	}
	writeJSON(w, http.StatusOK, resp)
}

// mainChatSegment это отрезок основной ленты с подписью роли и этапа. Отрезок
// даёт журнал назначений, а подпись выводится из записей реестра сессий
// носителя за время отрезка (DK-871): одной историей задача проходит груминг,
// разработку, ревью и проверку, и отрезки говорят, где какая шла.
type mainChatSegment struct {
	mainchat.Segment
	Role  string `json:"role"`
	Stage string `json:"stage,omitempty"`
}

// tmuxChatKey это приставка ключа носителя-конвейера в журнале назначений.
const tmuxChatKey = "tmux-"

// mainChatSegments подписывает отрезки записи ролями и этапами. Носитель
// сессия зовётся своим ID, конвейер ключом tmux-<имя окна>, и записи реестра
// собираются по этой паре за время отрезка: свежая известная роль выигрывает,
// роли нет и по источнику не выводится, подписью становится неизвестная. У
// оформления черновика роль своя: отрезок это бывший свободный разговор,
// ставший основным (DK-1253).
func (s *server) mainChatSegments(recs []mainchat.Bind) []mainChatSegment {
	segs := mainchat.Segments(recs)
	if len(segs) == 0 {
		return nil
	}
	all := s.bindsAll()
	out := make([]mainChatSegment, 0, len(segs))
	for _, seg := range segs {
		var pool []sessionBind
		if name, ok := strings.CutPrefix(seg.Chat, tmuxChatKey); ok {
			for _, rs := range all {
				last := sessions.Last(rs)
				if strings.SplitN(last.Tmux, ":", 2)[0] == name {
					pool = append(pool, rs...)
				}
			}
		} else {
			pool = all[seg.Chat]
		}
		var timed []sessionBind
		for _, r := range pool {
			if r.Time < seg.Since || (seg.Until != "" && r.Time > seg.Until) {
				continue
			}
			timed = append(timed, r)
		}
		sort.SliceStable(timed, func(i, j int) bool { return timed[i].Time < timed[j].Time })
		label := mainChatSegment{Segment: seg}
		if role, stage, known := sessions.RoleStage(timed); known {
			label.Role, label.Stage = role, stage
		} else if seg.Source == mainchat.ByDecor {
			label.Role = sessions.RoleFree
		} else {
			label.Role = sessions.RoleUnknown
		}
		out = append(out, label)
	}
	return out
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

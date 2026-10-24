package main

import (
	"errors"
	"fmt"
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
	found, id, ok := s.taskChatRow(w, r)
	if !ok {
		return
	}
	data := s.mainChatData()
	recs := mainchat.All(data)[id]
	resp := map[string]any{"id": id, "project": found.Name, "chat": "",
		"segments": s.mainChatSegments(recs)}
	if n := len(recs); n > 0 {
		resp["chat"] = s.mainChatCarrier(recs[n-1].Chat)
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

// taskChatRow находит запись адресом основного чата: строку доски, закрытую
// строку архива либо запись накопителя. Адрес принадлежит записи, а не строке
// доски: черновик до грумминга строки не имеет, а основной чат у него уже
// есть, и кнопка записи открывает разговор, а не отказ (LLD DK-1422,
// решение 2, источник «оформление» у DK-1429).
func (s *server) taskChatRow(w http.ResponseWriter, r *http.Request) (*Project, string, bool) {
	found := s.findProject(w, r, "чат записи")
	if found == nil {
		return nil, "", false
	}
	id := r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return nil, "", false
	}
	id = strings.ToUpper(id)
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return nil, "", false
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return nil, "", false
	}
	if _, hit := rows[id]; hit {
		return found, id, true
	}
	if _, closed := s.archiveRows(found.Path)[id]; closed {
		return found, id, true
	}
	if draftHere(found.Path, id) {
		return found, id, true
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": s.rowGone(found, id)})
	return nil, "", false
}

// mainChatCarrier называет действующего носителя записи. Незачатый разговор,
// посаженный оформлением второй записи из одного обсуждения, вырастает
// в сессию первой репликой, а строка журнала называет прежний ключ, и адрес
// следует за ростом сам. Прочие ключи возвращаются как есть: их переезды
// пишут свои писатели, передача и продолжение (DK-1428).
func (s *server) mainChatCarrier(chat string) string {
	if !strings.HasPrefix(chat, chatBlankMark) {
		return chat
	}
	st := s.chatStoreRead(chat)
	if !st.Blank || st.Grown != "" {
		if st.Grown != "" {
			return st.Grown
		}
		return chat
	}
	if st.Tmux == "" {
		return chat
	}
	lifted := st.Lifted
	if lifted == 0 {
		lifted = st.Born
	}
	if owner := sessions.TmuxOwnerSince(s.bindsAll(), st.Tmux, bindTimeSince(lifted)); owner != "" && owner != chat {
		return owner
	}
	return chat
}

// draftDecorChat назначает свежей записи основной чат из свободного разговора,
// в котором её оформили (LLD DK-1422, решение 1, источник «оформление»).
// Свободным считается начатый разговор без записи: реестр знает сессию, и её
// действующая привязка не называет задачи. Проект начатой сессии сверяется
// с проектом записи, и беседа чужого проекта основной лентой не становится.
// Разговор, уже служащий основным другой записи, не отдаётся: у одного
// разговора одна запись, и вторая запись из того же обсуждения получает
// свой чат с итогом постановки и ссылкой на источник. Возвращается заметка
// человеку, когда было что сказать.
func (s *server) draftDecorChat(found *Project, id, chat, title string) string {
	chat = strings.TrimSpace(chat)
	if chat == "" {
		return ""
	}
	if rec, ok := s.binds()[chat]; !ok {
		s.logf("оформление %s: разговора %s нет среди начатых, основным чатом он не стал", id, chat)
		return fmt.Sprintf("разговора %s нет среди начатых, основным чатом черновика он не стал", chat)
	} else if rec.Task != "" || rec.Parent != "" {
		s.logf("оформление %s: разговор %s не свободный, основным чатом он не стал", id, chat)
		return fmt.Sprintf("разговор %s не свободный, основным чатом черновика он не стал", chat)
	} else if rec.Project != "" && rec.Project != found.Name {
		s.logf("оформление %s: разговор %s из проекта %s, основным чатом он не стал", id, chat, rec.Project)
		return fmt.Sprintf("разговор %s из проекта %s, основным чатом черновика он не стал", chat, rec.Project)
	}
	err := mainchat.Put(s.cfg.Home, mainchat.Bind{
		Record: id, Chat: chat, Project: found.Name, Source: mainchat.ByDecor})
	var held *mainchat.HeldError
	if errors.As(err, &held) {
		key := s.draftDecorSeed(found, id, chat, title)
		if key == "" {
			return fmt.Sprintf("обсуждение уже основное у записи %s, свой чат завести не удалось", held.Record)
		}
		s.logf("оформление %s: обсуждение уже основное у %s, заведён свой чат %s", id, held.Record, key)
		return fmt.Sprintf("обсуждение уже основное у записи %s: заведён свой чат со ссылкой на источник", held.Record)
	}
	if err != nil {
		s.logf("назначение основного чата %s не записалось: %v", id, err)
		return ""
	}
	s.logf("оформление %s: разговор %s стал основным чатом записи", id, chat)
	return "разговор стал основным чатом записи"
}

// draftDecorSeed заводит второй записи из одного обсуждения её собственный
// чат: незачатый разговор с готовой первой репликой, итогом постановки
// и ссылкой на общее обсуждение. Сессию не поднимает никто: создание черновика
// разработку не запускает, и сессию поднимет первая реплика человека. Модель
// едет от разговора-источника, когда человек её там выбирал.
func (s *server) draftDecorSeed(found *Project, id, chat, title string) string {
	key := chatBlankID()
	st := chatStore{Blank: true, Born: s.now().Unix(), Project: found.Name, Task: id,
		Draft: fmt.Sprintf("%s\n\nПостановка выделена из общего обсуждения: #%s/chat/%s",
			title, found.Name, chat)}
	if pick := s.chatStoreRead(chat); pick.Model != "" {
		st.Model, st.Harness = pick.Model, pick.Harness
	}
	if err := s.chatStoreWrite(key, st); err != nil {
		s.logf("свой чат записи %s не завёлся: %v", id, err)
		return ""
	}
	if err := mainchat.Put(s.cfg.Home, mainchat.Bind{
		Record: id, Chat: key, Project: found.Name, Source: mainchat.ByDecor}); err != nil {
		s.logf("назначение основного чата %s не записалось: %v", id, err)
	}
	return key
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

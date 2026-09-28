package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/peers"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// stageView это всё, что нужно строке этапа под строкой доски (DK-910): записи
// ~/.devkit/runs проекта, реестр живых сессий клиента и реестр чатов задачи.
// Читается один раз на вызов list или show, строки дальше ходят по картам.
// Реестры читаются лениво, при первой строке с этапом: доска без следа
// агента не платит за проверку сессий.
//
// У строки без записи в runs последний этап берётся из «Хода работы» файла
// задачи (DK-1205): пакет уезжает туда на смене статуса и на парковке, и
// строка, которой агент касался, без записи в runs выглядела бы нетронутой.
// Файл читается из main, а не из ветки: доска и список живут на main, а
// запись runs после смены статуса уже стёрта. Прочитанное кладётся в files, в
// том числе пустым ответом, чтобы show и list не ходили за одним файлом дважды.
type stageView struct {
	main   string
	recs   map[string]stage.Record
	files  map[string]*stage.Record
	peers  map[string]peers.Peer
	binds  map[string][]sessions.Bind
	loaded bool
	now    time.Time
}

// loadStageView собирает снимок для доски проекта root. Без домашней
// директории записей нет, и строка этапа молчит, как молчал бы agentctl stage.
// Запись берётся с любым этапом, а не только с открытым: закрытый последний
// этап тоже строка под задачей (DK-1205).
func loadStageView(root string) *stageView {
	home := stage.Home()
	if home == "" {
		return nil
	}
	v := &stageView{main: stage.MainRoot(root), recs: map[string]stage.Record{}, files: map[string]*stage.Record{}, now: timeNow()}
	for _, rec := range stage.List(home, v.main) {
		if len(rec.Stages) > 0 {
			v.recs[rec.ID] = rec
		}
	}
	return v
}

// registries читает реестр клиента и реестр чатов один раз, при первой
// строке, которой они нужны.
func (v *stageView) registries() {
	if v.loaded {
		return
	}
	v.loaded = true
	home := stage.Home()
	v.peers = peers.Load(home, false)
	v.binds = sessions.LoadAll(home)
}

// record отдаёт запись задачи: из runs, а без неё из «Хода работы» файла
// задачи в main. Второе значение false у строки, которой агент не касался.
func (v *stageView) record(id string) (stage.Record, bool) {
	if rec, ok := v.recs[id]; ok {
		return rec, true
	}
	if rec, ok := v.files[id]; ok {
		return derefRecord(rec)
	}
	rec := readProgressRecord(v.main, id)
	v.files[id] = rec
	return derefRecord(rec)
}

func derefRecord(rec *stage.Record) (stage.Record, bool) {
	if rec == nil {
		return stage.Record{}, false
	}
	return *rec, true
}

// readProgressRecord собирает запись из строк этапов раздела «Ход работы»
// файла задачи. Строки уже лежат закрытыми: пакет уехал в файл целиком, и у
// каждого этапа конец это начало плюс длительность из строки. Этап нулевой
// длины кончается там же, где начался. Файла или строк этапа нет, и записи нет.
func readProgressRecord(main, id string) *stage.Record {
	data, err := os.ReadFile(taskFilePath(main, id))
	if err != nil {
		return nil
	}
	rec := &stage.Record{ID: id, Root: main}
	for _, ln := range taskform.SectionLines(string(data), taskform.Stages) {
		s, span, ok := stage.ParseLine(ln)
		if !ok {
			continue
		}
		s.End = s.Start.Add(span)
		rec.Stages = append(rec.Stages, s)
	}
	if len(rec.Stages) == 0 {
		return nil
	}
	return rec
}

// lastEnded это этап работы записи, кончившийся позже прочих: он и стоит
// под строкой, когда открытого нет. Берётся по концу, а не по месту в записи:
// синхронная вычитка ложится поверх идущей разработки и в записи стоит
// последней, а кончается раньше неё. При равных концах побеждает тот, что
// записан позже. Закрытые ожидания не в счёт: ожидание это остановка на
// этапе, а не этап (DK-1119), и кончившаяся остановка ничего не говорит о
// том, где задача встала. Строка после снятой парковки про ожидание не
// говорит (DK-1193), и запись из одних закрытых ожиданий строки не даёт.
func lastEnded(rec stage.Record) (stage.Stage, bool) {
	at := -1
	for i, s := range rec.Stages {
		if !s.Ended() || !stage.IsWork(s.Kind) {
			continue
		}
		if at < 0 || !s.End.Before(rec.Stages[at].End) {
			at = i
		}
	}
	if at < 0 {
		return stage.Stage{}, false
	}
	return rec.Stages[at], true
}

// stageRound это круг этапа: сколько этапов того же вида накопил пакет
// записи, сам этап включая. Отдельного поля круга в записи нет, и заводить его
// значило бы править всех писателей этапов (предмет DK-911).
func stageRound(rec stage.Record, kind string) int {
	n := 0
	for _, s := range rec.Stages {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// taskSessions называет сессии, ведущие задачу: все рабочие сессии из реестра
// чатов (LLD DK-430, решение 8: у строки бывает и чат поверх работы конвейера)
// плюс сессия, открывшая этап. Запись этапа хранит только последнюю, а реестр
// знает и остальные. У закрытого этапа та же выборка называет голову задачи:
// сессию, которая поднимет следующий этап.
func (v *stageView) taskSessions(id string, st stage.Stage) []string {
	v.registries()
	var out []string
	seen := map[string]bool{}
	if st.Session != "" {
		out, seen[st.Session] = append(out, st.Session), true
	}
	for sid, recs := range v.binds {
		if !seen[sid] && sessions.WorksOn(recs, id) {
			out, seen[sid] = append(out, sid), true
		}
	}
	return out
}

// jsonStage это строка этапа машинным видом: те же куски, что печатает list,
// каждый своим полем. Дашборд берёт их отсюда готовыми и своего расчёта по
// строке не ведёт (DK-910): признак считает одна сторона, и бейдж экрана с
// текстом списка сходятся. State называет, открыт этап или закрыт (DK-1205);
// у закрытого End это его конец, Age считается от конца, а Head говорит о
// голове задачи словами вместо Session.
type jsonStage struct {
	Kind    string `json:"stage"`
	State   string `json:"stage_state"`
	Since   int64  `json:"stage_since"`
	End     int64  `json:"stage_end,omitempty"`
	Round   int    `json:"stage_round"`
	Age     string `json:"stage_age"`
	Session string `json:"stage_session,omitempty"`
	Head    string `json:"stage_head,omitempty"`
	At      string `json:"stage_at,omitempty"`
}

// Значения stage_state: этап идёт либо его закрыл писатель.
const (
	stageOpen   = "открыт"
	stageClosed = "закрыт"
)

// mark собирает поля этапа строки: открытого, а без него последнего
// закрытого. Пусто у строки, которой агент не касался.
func (v *stageView) mark(id string) *jsonStage {
	if v == nil {
		return nil
	}
	rec, ok := v.record(id)
	if !ok {
		return nil
	}
	live, ok := rec.Live()
	if !ok {
		return v.markClosed(id, rec)
	}
	m := &jsonStage{Kind: live.Kind, State: stageOpen, Since: live.Start.Unix(), Round: stageRound(rec, live.Kind), Age: ageSince(live.Start, v.now)}
	// Хвост о сессии стоит там, где этап требует живой сессии (решение
	// человека по DK-910, развилка «секции»). Отвечает на это словарь этапов,
	// stage.NeedsSession, своего списка у списка нет: словарь DK-911 меняет
	// ответ у ожиданий, и строка переедет вместе с ним.
	if stage.NeedsSession(live.Kind) {
		m.Session = lifeWords(peers.Judge(v.peers, v.taskSessions(id, live), v.now))
	} else {
		m.At = stoodAt(rec)
	}
	return m
}

// markClosed собирает поля последнего закрытого этапа работы: возраст от
// конца и голова задачи вместо сессии этапа. Поля stage_at у него нет, оно
// у ожиданий, а закрытым под строку идёт только этап работы (lastEnded). Сессии за закрытым этапом не положено,
// а вот сессия, которая поднимет следующий, либо есть, либо нет, и это
// второе видно только здесь: дальше конвейер молчит (DK-1205).
func (v *stageView) markClosed(id string, rec stage.Record) *jsonStage {
	last, ok := lastEnded(rec)
	if !ok {
		return nil
	}
	m := &jsonStage{Kind: last.Kind, State: stageClosed, Since: last.Start.Unix(), End: last.End.Unix(), Round: stageRound(rec, last.Kind), Age: ageSince(last.End, v.now)}
	m.Head = headWords(peers.Judge(v.peers, v.taskSessions(id, last), v.now), v.now)
	return m
}

// stoodAt называет этап работы, на котором задача встала перед ожиданием.
// Ожидание не несёт своей сессии (словарь DK-911), а лента строки списка и
// шапка формы (DK-1119) подсвечивают не своё деление, а последний этап
// работы перед записью ожидания. Тот же приём уже стоит в
// tools/agentctl/pick.go (afterReview), считает его stage.LastOf.
func stoodAt(rec stage.Record) string {
	if last, ok := stage.LastOf(rec, stage.IsWork); ok {
		return last.Kind
	}
	return ""
}

// note собирает печатную строку этапа: «этап: ревью, круг 2, 12 минут, сессия
// жива», а у закрытого «этап: слияние, закрыт 2 дня назад, головы нет». Круг
// первый не печатается: он у каждого этапа, и слово на нём ничего не говорит.
// Пусто у строки, которой агент не касался.
func (v *stageView) note(id string) string {
	m := v.mark(id)
	if m == nil {
		return ""
	}
	parts := []string{"этап: " + m.Kind}
	if m.Round > 1 {
		parts = append(parts, fmt.Sprintf("круг %d", m.Round))
	}
	if m.State == stageClosed {
		return strings.Join(append(parts, "закрыт "+m.Age+" назад", m.Head), ", ")
	}
	parts = append(parts, m.Age)
	if m.Session != "" {
		parts = append(parts, m.Session)
	}
	return strings.Join(parts, ", ")
}

// headWords переводит живость головы задачи в слова под закрытым этапом. Те
// же три случая, что у lifeWords, только речь не о сессии за этапом, а о той,
// что поднимет следующий: «головы нет» значит, что конвейер на этой строке
// оборвался и следующий этап никто не откроет. Молчание считается словами
// возраста, а не минутами: окно диспетчера живёт днями, и «молчит 2776
// минут» читалось бы хуже, чем «молчит 2 дня».
func headWords(l peers.Life, now time.Time) string {
	switch l.State {
	case peers.Alive:
		return "голова жива"
	case peers.Silent:
		if l.Silence == 0 {
			return "голова молчит, касания не записано"
		}
		return "голова молчит " + ageSince(now.Add(-l.Silence), now)
	}
	return "головы нет"
}

// lifeWords переводит живость в слова под строкой. Брошена это когда сессии за
// записью нет вовсе; живая, но молчащая дольше рубежа, названа своими словами:
// это два разных случая (решение человека по DK-910, развилка «порог»).
func lifeWords(l peers.Life) string {
	switch l.State {
	case peers.Alive:
		return "сессия жива"
	case peers.Silent:
		if l.Silence == 0 {
			return "сессия молчит, касания не записано"
		}
		m := int(l.Silence.Minutes())
		return fmt.Sprintf("сессия молчит %d %s", m, pluralMinutes(m))
	}
	return "сессии нет, брошена"
}

// ageSince это возраст этапа словами: минуты до часа, часы до двух суток,
// дальше дни. Точнее не нужно: строка отвечает «давно ли», а не «во сколько».
func ageSince(start, now time.Time) string {
	d := now.Sub(start)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "меньше минуты"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%d %s", m, pluralMinutes(m))
	case d < 48*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%d %s", h, pluralHours(h))
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%d %s", days, pluralDays(days))
}

// pluralHours склоняет «час» по числу часов.
func pluralHours(n int) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return "часов"
	}
	switch n % 10 {
	case 1:
		return "час"
	case 2, 3, 4:
		return "часа"
	default:
		return "часов"
	}
}

package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/peers"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

// stageView это всё, что нужно строке этапа под строкой доски (DK-910): записи
// ~/.devkit/runs проекта, реестр живых сессий клиента и реестр чатов задачи.
// Читается один раз на вызов list или show, строки дальше ходят по картам.
// Реестры не читаются вовсе, когда ни у одной строки нет открытого этапа:
// доска без работы не платит за проверку сессий.
type stageView struct {
	recs  map[string]stage.Record
	peers map[string]peers.Peer
	binds map[string][]sessions.Bind
	now   time.Time
}

// loadStageView собирает снимок для доски проекта root. Без домашней
// директории записей нет, и строка этапа молчит, как молчал бы agentctl stage.
func loadStageView(root string) *stageView {
	home := stage.Home()
	if home == "" {
		return nil
	}
	v := &stageView{recs: map[string]stage.Record{}, now: timeNow()}
	for _, rec := range stageRecords(home, stage.MainRoot(root)) {
		if _, ok := rec.Live(); ok {
			v.recs[rec.ID] = rec
		}
	}
	if len(v.recs) == 0 {
		return v
	}
	v.peers = peers.Load(home, false)
	v.binds = sessions.LoadAll(home)
	return v
}

// stageRecords читает записи проекта root. Корень сверяется по настоящему
// пути, а не по написанию: писатели этапов берут корень у git, который
// разворачивает символические ссылки, а читатель мог получить корень с
// экрана или из аргумента -C как есть, и на машинах с домом за ссылкой (macOS,
// /var против /private/var) те же записи выпадали бы из списка.
func stageRecords(home, root string) []stage.Record {
	paths, err := filepath.Glob(filepath.Join(stage.Dir(home), "*.run"))
	if err != nil {
		return nil
	}
	sort.Strings(paths)
	want := realPath(root)
	var out []stage.Record
	for _, p := range paths {
		rec, err := stage.Load(p)
		if err != nil || rec.ID == "" || realPath(rec.Root) != want {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// realPath приводит путь к виду без символических ссылок; недоступный путь
// остаётся как есть, чтобы записи несуществующих корней сверялись хотя бы по
// написанию.
func realPath(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// waitStage отвечает, ждёт ли этап чего-то снаружи: за таким этапом живой
// сессии нет по смыслу, и признак «брошена» ему не ставится (решение человека
// по DK-910, развилка «секции»). Это единственная точка, где список судит о
// виде этапа: словарь DK-911 приносит предикат ожидания, и тогда тело этой
// функции меняется на него одной строкой.
func waitStage(kind string) bool { return kind == stage.Outside || kind == stage.Ask }

// stageRound это круг живого этапа: сколько этапов того же вида накопил пакет
// записи, живой включая. Отдельного поля круга в записи нет, и заводить его
// значило бы править всех писателей этапов (предмет DK-911).
func stageRound(rec stage.Record) int {
	live, ok := rec.Live()
	if !ok {
		return 0
	}
	n := 0
	for _, s := range rec.Stages {
		if s.Kind == live.Kind {
			n++
		}
	}
	return n
}

// taskSessions называет сессии, ведущие задачу: все рабочие сессии из реестра
// чатов (LLD DK-430, решение 8: у строки бывает и чат поверх работы конвейера)
// плюс сессия, открывшая живой этап. Запись этапа хранит только последнюю, а
// реестр знает и остальные.
func (v *stageView) taskSessions(id string, live stage.Stage) []string {
	var out []string
	seen := map[string]bool{}
	if live.Session != "" {
		out, seen[live.Session] = append(out, live.Session), true
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
// текстом списка сходятся.
type jsonStage struct {
	Kind    string `json:"stage"`
	Since   int64  `json:"stage_since"`
	Round   int    `json:"stage_round"`
	Age     string `json:"stage_age"`
	Session string `json:"stage_session,omitempty"`
}

// mark собирает поля этапа строки. Пусто у строки без открытого этапа.
func (v *stageView) mark(id string) *jsonStage {
	if v == nil {
		return nil
	}
	rec, ok := v.recs[id]
	if !ok {
		return nil
	}
	live, _ := rec.Live()
	m := &jsonStage{Kind: live.Kind, Since: live.Start.Unix(), Round: stageRound(rec), Age: ageSince(live.Start, v.now)}
	if !waitStage(live.Kind) {
		m.Session = lifeWords(peers.Judge(v.peers, v.taskSessions(id, live), v.now))
	}
	return m
}

// note собирает печатную строку этапа: «этап: ревью, круг 2, 12 минут, сессия
// жива». Круг первый не печатается: он у каждого этапа, и слово на нём ничего
// не говорит. Пусто у строки без открытого этапа.
func (v *stageView) note(id string) string {
	m := v.mark(id)
	if m == nil {
		return ""
	}
	parts := []string{"этап: " + m.Kind}
	if m.Round > 1 {
		parts = append(parts, fmt.Sprintf("круг %d", m.Round))
	}
	parts = append(parts, m.Age)
	if m.Session != "" {
		parts = append(parts, m.Session)
	}
	return strings.Join(parts, ", ")
}

// lifeWords переводит живость в слова под строкой. Брошена это когда сессии за
// записью нет вовсе; живая, но молчащая дольше рубежа, названа своими словами:
// это два разных случая (решение человека по DK-910, развилка «порог»).
func lifeWords(l peers.Life) string {
	switch l.State {
	case peers.Alive:
		return "сессия жива"
	case peers.Silent:
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

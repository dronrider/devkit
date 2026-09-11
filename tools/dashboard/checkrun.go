package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/checkrun"
)

// Подъём прогона сценария после выката (DK-718). Выкат без человека в окне
// доводит задачу до прода и оставляет её в Check: агентскую часть сценария
// гоняет не автор правки, а поднять этого проверяющего было некому, и строка
// стояла до тех пор, пока человек не поднимал сессию руками.
//
// Отбор подъёма (нужен ли он, кто вёл разработку, какой моделью поднимать)
// живёт в internal/checkrun с DK-947: его зовут ещё выкат и тик сторожка через
// `shipctl check-run`, и без дашборда подъём идёт тем же путём. Своего у
// команды тут только носитель: tmux-сессия task-<ID> той же лестницей, что у
// кнопки запуска на экране (runs.go), проверки tmux и прав машинного контура.

// roleReview это роль вердикта, которой назначается проверяющий.
const roleReview = checkrun.RoleReview

// errCheckRunFailed это код возврата команды: подъём был нужен и не вышел.
// Слова про причину уже напечатаны построчно, и повторять их ошибкой незачем.
var errCheckRunFailed = errors.New("подъём прогона не вышел")

// checkRunReport это исход подъёма по одной строке. Форма общая с отбором:
// ею же отвечают второй круг ревью (round.go) и подъём ответом (wake.go).
type checkRunReport = checkrun.Report

// permsRel это перечень прав машинного контура внутри чекаута devkit. Ищется
// он там же и тем же порядком, что оболочка конвейера.
const permsRel = checkrun.PermsRel

func permsPath(roots []string) string {
	if tree := devkitOwnTree(); tree != "" {
		if p := filepath.Join(tree, filepath.FromSlash(permsRel)); isFile(p) {
			return p
		}
	}
	return inRoots(roots, permsRel)
}

// permsRefusal это предполётная проверка прав, тот же барьер, каким виток цели
// закрыт от старта без прав. Прогон идёт в окне без человека, одобрить запрос
// разрешения в нём некому, и сессия без прав вставала на первом же вызове,
// паркуя задачу вопросом к человеку: со стороны это неотличимо от молчания, и
// цель стояла до тех пор, пока человек сам не заглянет в панель (DK-739).
// Пусто значит «поднимать можно», непустое это слова отказа для отчёта.
//
// Права спрашиваются под домом пользователя: демон живёт под launchd с
// подложным HOME, и разложенных доктором настроек харнеса в нём нет.
func (s *server) permsRefusal() string {
	p := permsPath(s.cfg.Roots)
	if p == "" {
		return "перечня прав машинного контура не нашлось в корнях конфига (" + permsRel +
			"): проверить их нечем, а без них сессия без человека встаёт на первом же" +
			" запросе разрешения; нужен чекаут devkit в одном из корней"
	}
	out, err := runProcQuietAt(realHome(), "", true, "python3", p)
	if err == nil {
		return ""
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return msg
	}
	return "перечень прав " + p + " не ответил: " + procErr(err)
}

// checkRow переводит строку ответа taskctl в строку отбора.
func checkRow(row boardRow) checkrun.Row {
	return checkrun.Row{ID: row.ID, Title: row.Title, Sect: row.Sect, Section: row.Section,
		Accept: row.Accept, Fail: row.Fail}
}

// dashCarrier это носитель дашборда: окно tmux task-<ID> с его окружением.
type dashCarrier struct {
	s    *server
	proj *Project
	own  *Harness
}

func (c *dashCarrier) Ready(id string) (string, bool) {
	if m := tmuxMissingCheck(); m != "" {
		return checkrun.NotRaised + ", " + m, true
	}
	sess := "task-" + id
	talk := c.s.tmuxTalk(c.proj.Path)
	for _, name := range tmuxSessions() {
		if name == sess && !talk[name] {
			return checkrun.NoNeed + ", работа уже идёт в tmux-сессии " + sess, false
		}
	}
	if m := claudeMissing(); m != "" {
		return checkrun.NotRaised + ", " + m, true
	}
	// Права машинного контура спрашиваются до подъёма: поднять сессию, которая
	// упрётся в первый же запрос разрешения, дороже, чем отказать словами.
	if why := c.s.permsRefusal(); why != "" {
		return checkrun.NotRaised + ", права машинного контура на машине не разложены: " + why, true
	}
	return "", false
}

func (c *dashCarrier) Tier(id string) (string, string) {
	return c.s.pickTier(c.proj.Path, id, roleReview)
}

// Ladder это лестница подписки по умолчанию. Подписка тут не выбирается:
// подъём идёт клиентом по умолчанию, как шла бы кнопка запуска без выбора
// руки, и раскладка спрашивается только про ярусы.
func (c *dashCarrier) Ladder() []checkrun.Step {
	if c.own == nil || !c.own.Default {
		return nil
	}
	var out []checkrun.Step
	for _, m := range c.own.Models {
		out = append(out, checkrun.Step{Tier: m.Tier, Model: m.Model})
	}
	return out
}

func (c *dashCarrier) Raise(p checkrun.Plan) checkrun.Report {
	id := p.Row.ID
	sess := "task-" + id
	res, err := c.s.startTaskSession(c.proj, id, sess, nil, p.Choice.Model, p.Order, p.Again, true)
	if err != nil {
		// Занятый замок это живая голова, поднятая мимо дашборда, и поломкой он
		// не считается, как и живая tmux-сессия в предполёте.
		var busy *headBusy
		return checkrun.Report{Line: id + ": " + checkrun.NotRaised + ", " + err.Error(), Failed: !errors.As(err, &busy)}
	}
	return checkrun.Report{Line: p.Raised(headWhere(res, sess)), Raised: true}
}

// checkRun поднимает прогон по одной строке Check. Возврат это строка отчёта:
// молчащего исхода тут нет ни одного, зовущий уносит слова в свой журнал.
func (s *server) checkRun(proj *Project, id string, rows map[string]boardRow) checkRunReport {
	return s.checkRunsOf(proj, rows, []string{id})[0]
}

func (s *server) checkRunsOf(proj *Project, rows map[string]boardRow, ids []string) []checkrun.Report {
	conv := map[string]checkrun.Row{}
	for id, row := range rows {
		conv[id] = checkRow(row)
	}
	c := &dashCarrier{s: s, proj: proj, own: s.harnesses().byDefault()}
	return checkrun.Run(c, proj.Path, conv, ids)
}

// checkRuns проходит по названным строкам, а без имён по всей секции Check.
// Пустых исходов не бывает: на каждую строку приходится своя строка отчёта, а
// на пустую секцию одна общая.
func (s *server) checkRuns(proj *Project, ids []string) (lines []string, raised int, failed bool) {
	raw, err := s.projectBoard(proj.Path)
	if err != nil {
		return []string{"доска проекта " + proj.Name + " не прочиталась: " + err.Error()}, 0, true
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return []string{"доска проекта " + proj.Name + " не разобралась: " + err.Error()}, 0, true
	}
	for _, rep := range s.checkRunsOf(proj, rows, ids) {
		lines = append(lines, rep.Line)
		if rep.Raised {
			raised++
		}
		if rep.Failed {
			failed = true
		}
	}
	return lines, raised, failed
}

// cmdCheck это вход команды `dashboard check`: подъём прогона сценария по
// строкам Check названного проекта. Сервер тут собирается на месте, из того же
// конфига, и в сеть не выходит. Выкат и тик сторожка зовут с DK-947 не её, а
// `shipctl check-run`: отбор у них общий, а носитель без дашборда.
func cmdCheck(home, root string, ids []string, out io.Writer) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if !hasBoard(abs) {
		return fmt.Errorf("доски %s в %s нет: прогон поднимается по строке доски", boardRel, abs)
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		return err
	}
	s := newServer(cfg, nil, nil)
	proj := &Project{Name: filepath.Base(abs), Path: abs}
	lines, _, failed := s.checkRuns(proj, ids)
	for _, ln := range lines {
		fmt.Fprintln(out, ln)
	}
	if failed {
		return errCheckRunFailed
	}
	return nil
}
